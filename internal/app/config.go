package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func NewSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func EffectiveConfig(s State, secret string, endpointIPs []net.IP) ([]byte, error) {
	if len(s.Proxies) == 0 {
		return nil, errors.New("尚未导入可用节点")
	}
	nodes := make([]string, 0, len(s.Proxies))
	for _, p := range s.Proxies {
		nodes = append(nodes, p["name"].(string))
	}
	groupNames := map[string]bool{"节点选择": true}
	groups := []map[string]any{{"name": "节点选择", "type": "select", "proxies": nodes}}
	for _, g := range s.Groups {
		groupNames[g.Name] = true
		groups = append(groups, map[string]any{"name": g.Name, "type": "select", "proxies": g.Proxies})
	}
	selectedGroup := s.SelectedGroup
	if !groupNames[selectedGroup] {
		selectedGroup = "节点选择"
	}
	mode := s.Mode
	if mode != "rule" && mode != "global" && mode != "direct" {
		mode = "rule"
	}
	excluded := []string{"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16", "223.5.5.5/32", "224.0.0.0/4", "240.0.0.0/4", "::1/128", "fc00::/7", "fe80::/10"}
	for _, ip := range endpointIPs {
		if v4 := ip.To4(); v4 != nil {
			excluded = append(excluded, v4.String()+"/32")
		} else {
			excluded = append(excluded, ip.String()+"/128")
		}
	}
	sort.Strings(excluded)
	dnsNameservers := []string{"https://1.1.1.1/dns-query"}
	if mode == "direct" {
		dnsNameservers = []string{"https://dns.alidns.com/dns-query"}
	}
	dnsConfig := map[string]any{"enable": true, "ipv6": false, "enhanced-mode": "redir-host", "listen": "127.0.0.1:19092", "default-nameserver": []string{"223.5.5.5"}, "nameserver": dnsNameservers, "proxy-server-nameserver": []string{"223.5.5.5"}, "respect-rules": mode != "direct"}
	if mode == "rule" {
		dnsConfig["nameserver-policy"] = map[string]any{"rule-set:cn-domains": "https://dns.alidns.com/dns-query#DIRECT"}
	}
	rules := []string{"IP-CIDR,10.0.0.0/8,DIRECT,no-resolve", "IP-CIDR,172.16.0.0/12,DIRECT,no-resolve", "IP-CIDR,192.168.0.0/16,DIRECT,no-resolve", "IP-CIDR,127.0.0.0/8,DIRECT,no-resolve", "IP-CIDR,169.254.0.0/16,DIRECT,no-resolve"}
	for _, rule := range s.Rules {
		target := rule.Action
		if target == "PROXY" {
			target = selectedGroup
		}
		if target != "DIRECT" && target != "REJECT" && target != selectedGroup {
			continue
		}
		rendered := rule.Type + "," + rule.Value + "," + target
		if rule.NoResolve {
			rendered += ",no-resolve"
		}
		rules = append(rules, rendered)
	}
	rules = append(rules, "RULE-SET,cn-domains,DIRECT", "RULE-SET,cn-ip,DIRECT", "MATCH,"+selectedGroup)
	c := map[string]any{
		"mode": mode, "log-level": "silent", "allow-lan": false, "ipv6": false,
		"external-controller": "127.0.0.1:19091", "secret": secret,
		"profile": map[string]any{"store-selected": true},
		"rule-providers": map[string]any{
			"cn-domains": map[string]any{"type": "file", "behavior": "domain", "format": "mrs", "path": "./rules/cn-domain.mrs"},
			"cn-ip":      map[string]any{"type": "file", "behavior": "ipcidr", "format": "mrs", "path": "./rules/cn-ip.mrs"},
		},
		"dns":     dnsConfig,
		"tun":     map[string]any{"enable": true, "device": "fnvpn0", "stack": "system", "auto-route": true, "auto-redirect": false, "auto-detect-interface": true, "strict-route": true, "dns-hijack": []string{"any:53", "tcp://any:53"}, "route-exclude-address": excluded, "iproute2-table-index": 19091, "iproute2-rule-index": 19091},
		"proxies": s.Proxies, "proxy-groups": groups,
		"rules": rules,
	}
	return yaml.Marshal(c)
}

func ResolveEndpoints(s State) ([]net.IP, error) {
	unique := map[string]net.IP{}
	seenHost := map[string]bool{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, p := range s.Proxies {
		host := p["server"].(string)
		if seenHost[host] {
			continue
		}
		seenHost[host] = true
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(ips) == 0 {
			return nil, errors.New("代理服务器域名无法解析")
		}
		for _, a := range ips {
			unique[a.IP.String()] = a.IP
		}
	}
	out := make([]net.IP, 0, len(unique))
	for _, ip := range unique {
		out = append(out, ip)
	}
	return out, nil
}

func IsPublicIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		a, b, c := v4[0], v4[1], v4[2]
		if a == 0 || a == 10 || a == 127 || a >= 224 || a == 169 && b == 254 || a == 172 && b >= 16 && b <= 31 || a == 192 && b == 168 || a == 100 && b >= 64 && b <= 127 || a == 192 && b == 0 && c == 0 || a == 192 && b == 0 && c == 2 || a == 198 && b >= 18 && b <= 19 || a == 198 && b == 51 && c == 100 || a == 203 && b == 0 && c == 113 {
			return false
		}
		return true
	}
	return (ip[0]&0xe0) == 0x20 && !strings.HasPrefix(ip.String(), "2001:db8:")
}
