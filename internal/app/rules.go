package app

import (
	"net"
	"strings"
	"unicode"
)

const maxSubscriptionRules = 5000

type Rule struct {
	Type      string `json:"type"`
	Value     string `json:"value"`
	Action    string `json:"action"`
	NoResolve bool   `json:"no_resolve,omitempty"`
}

func parseSubscriptionRules(source []any) []Rule {
	rules := make([]Rule, 0, min(len(source), maxSubscriptionRules))
	for _, entry := range source {
		if len(rules) >= maxSubscriptionRules {
			break
		}
		text, ok := entry.(string)
		if !ok {
			continue
		}
		if rule, ok := parseSubscriptionRule(text); ok {
			rules = append(rules, rule)
		}
	}
	return rules
}

func parseSubscriptionRule(text string) (Rule, bool) {
	parts := strings.Split(text, ",")
	if len(parts) < 3 || len(parts) > 4 {
		return Rule{}, false
	}
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
	}
	kind := strings.ToUpper(parts[0])
	value := parts[1]
	target := parts[2]
	if !validRuleToken(target, 120) {
		return Rule{}, false
	}
	action := "PROXY"
	switch strings.ToUpper(target) {
	case "DIRECT":
		action = "DIRECT"
	case "REJECT", "REJECT-DROP", "REJECT-TINYGIF":
		action = "REJECT"
	case "PASS":
		return Rule{}, false
	}
	noResolve := false
	if len(parts) == 4 {
		if !strings.EqualFold(parts[3], "no-resolve") {
			return Rule{}, false
		}
		noResolve = true
	}
	switch kind {
	case "DOMAIN", "DOMAIN-SUFFIX", "DOMAIN-KEYWORD", "DOMAIN-WILDCARD":
		if noResolve || !validRuleToken(value, 253) {
			return Rule{}, false
		}
	case "IP-CIDR", "IP-CIDR6":
		address, _, err := net.ParseCIDR(value)
		if err != nil || (kind == "IP-CIDR" && address.To4() == nil) || (kind == "IP-CIDR6" && address.To4() != nil) {
			return Rule{}, false
		}
	case "GEOSITE":
		if noResolve || !strings.EqualFold(value, "cn") {
			return Rule{}, false
		}
		kind, value = "RULE-SET", "cn-domains"
	case "GEOIP":
		if !strings.EqualFold(value, "cn") {
			return Rule{}, false
		}
		kind, value = "RULE-SET", "cn-ip"
	default:
		return Rule{}, false
	}
	return Rule{Type: kind, Value: value, Action: action, NoResolve: noResolve}, true
}

func validRuleToken(value string, limit int) bool {
	if value == "" || len(value) > limit {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) || unicode.IsSpace(character) || character == ',' || character == '/' || character == '\\' || character == '#' {
			return false
		}
	}
	return true
}
