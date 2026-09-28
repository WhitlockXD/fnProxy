package app

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSubscriptionRulesAppliedInOrder(t *testing.T) {
	input := `proxies:
  - name: test-node
    type: ss
    server: example.com
    port: 443
    cipher: aes-128-gcm
    password: secret
proxy-groups:
  - name: work
    type: select
    proxies: [test-node, DIRECT]
rules:
  - DOMAIN-SUFFIX,google.com,Foreign
  - DOMAIN-SUFFIX,baidu.com,DIRECT
  - IP-CIDR,1.2.3.0/24,Foreign,no-resolve
  - GEOIP,CN,DIRECT
  - GEOSITE,cn,DIRECT
  - DOMAIN,blocked.example,REJECT
  - RULE-SET,remote,DIRECT
  - PROCESS-NAME,example,Foreign
  - MATCH,Foreign
`
	proxies, groups, report, err := ParseSubscriptionReport([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if report.SourceRules != 9 || len(report.Rules) != 6 || report.SkippedRules != 3 {
		t.Fatalf("unexpected rule counts: %+v", report)
	}
	s := NewState()
	s.Proxies, s.Groups, s.Rules = proxies, groups, report.Rules
	s.SelectedGroup = "work"
	config, err := EffectiveConfig(s, "secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	var generated struct {
		Rules []string `yaml:"rules"`
	}
	if err := yaml.Unmarshal(config, &generated); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"DOMAIN-SUFFIX,google.com,work",
		"DOMAIN-SUFFIX,baidu.com,DIRECT",
		"IP-CIDR,1.2.3.0/24,work,no-resolve",
		"RULE-SET,cn-ip,DIRECT",
		"RULE-SET,cn-domains,DIRECT",
		"DOMAIN,blocked.example,REJECT",
		"RULE-SET,cn-domains,DIRECT",
		"RULE-SET,cn-ip,DIRECT",
		"MATCH,work",
	}
	if len(generated.Rules) != 5+len(want) {
		t.Fatalf("got %d rules, want %d: %v", len(generated.Rules), 5+len(want), generated.Rules)
	}
	for i, value := range want {
		if generated.Rules[5+i] != value {
			t.Fatalf("rule %d = %q, want %q", i, generated.Rules[5+i], value)
		}
	}
	for _, rule := range generated.Rules {
		if strings.Contains(rule, "remote") || strings.Contains(rule, "PROCESS-NAME") {
			t.Fatalf("unsupported rule copied: %q", rule)
		}
	}
}

func TestSubscriptionRuleValidation(t *testing.T) {
	for _, source := range []string{
		"DOMAIN-SUFFIX,example.com\nMATCH,DIRECT,PROXY",
		"DOMAIN-SUFFIX,example.com,PROXY\nDIRECT",
		"DOMAIN-SUFFIX,example.com,PROXY,no-resolve",
		"IP-CIDR,invalid,PROXY",
		"IP-CIDR6,1.2.3.0/24,PROXY",
		"GEOSITE,private,DIRECT",
		"RULE-SET,remote,DIRECT",
	} {
		if _, ok := parseSubscriptionRule(source); ok {
			t.Errorf("accepted unsupported rule %q", source)
		}
	}
	if _, ok := parseSubscriptionRule("IP-CIDR6,2001:db8::/32,PROXY,no-resolve"); !ok {
		t.Fatal("valid IPv6 CIDR rule rejected")
	}
}
