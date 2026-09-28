package app

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"testing"
)

const sample = `proxies:
  - name: test-node
    type: ss
    server: example.com
    port: 443
    cipher: aes-128-gcm
    password: secret-password
proxy-groups:
  - name: work
    type: select
    proxies: [test-node, DIRECT]
rules:
  - MATCH,REJECT
`

func TestParseAndGenerate(t *testing.T) {
	proxies, groups, err := ParseSubscription([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(proxies) != 1 || len(groups) != 1 {
		t.Fatalf("unexpected nodes/groups: %d/%d", len(proxies), len(groups))
	}
	s := NewState()
	s.Proxies = proxies
	s.Groups = groups
	s.SelectedGroup = "work"
	s.SourceURL = "https://example.com/?token=private"
	b, err := EffectiveConfig(s, "controller-secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte("MATCH,work")) {
		t.Fatal("selected group missing from rules")
	}
	if bytes.Contains(b, []byte("MATCH,REJECT")) {
		t.Fatal("untrusted source rules copied")
	}
	if !bytes.Contains(b, []byte("auto-route: true")) {
		t.Fatal("TUN auto-route missing")
	}
	public := PublicState(s, false, "test", nil)
	if strings.Contains(strings.ReplaceAll(strings.TrimSpace(fmt.Sprint(public)), " ", ""), "private") || strings.Contains(fmt.Sprint(public), "secret-password") {
		t.Fatal("secret exposed in public state")
	}
}

func TestEffectiveConfigDNSRouting(t *testing.T) {
	proxies, _, err := ParseSubscription([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	state := NewState()
	state.Proxies = proxies
	config, err := EffectiveConfig(state, "secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"respect-rules: true", "223.5.5.5/32", "proxy-server-nameserver:", "https://1.1.1.1/dns-query"} {
		if !bytes.Contains(config, []byte(required)) {
			t.Errorf("missing DNS routing guard: %s", required)
		}
	}
	state.Mode = "direct"
	config, err = EffectiveConfig(state, "secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(config, []byte("respect-rules: false")) || !bytes.Contains(config, []byte("https://dns.alidns.com/dns-query")) {
		t.Fatal("direct mode DNS did not switch to direct upstream")
	}
}

func TestRuleModeDomesticSplit(t *testing.T) {
	proxies, _, err := ParseSubscription([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	state := NewState()
	state.Proxies = proxies
	config, err := EffectiveConfig(state, "secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	domain := bytes.Index(config, []byte("RULE-SET,cn-domains,DIRECT"))
	ip := bytes.Index(config, []byte("RULE-SET,cn-ip,DIRECT"))
	fallback := bytes.Index(config, []byte("MATCH,节点选择"))
	if domain < 0 || ip <= domain || fallback <= ip {
		t.Fatalf("domestic rules must precede proxy fallback: domain=%d ip=%d fallback=%d", domain, ip, fallback)
	}
	for _, file := range []string{"cn-domain.mrs", "cn-ip.mrs"} {
		if !bytes.Contains(config, []byte(file)) {
			t.Fatalf("missing bundled rule data %s", file)
		}
	}
	if !bytes.Contains(config, []byte("rule-set:cn-domains")) || !bytes.Contains(config, []byte("https://dns.alidns.com/dns-query#DIRECT")) {
		t.Fatal("domestic DNS policy missing")
	}
}

func TestSubscriptionFormatErrors(t *testing.T) {
	base64Links := base64.StdEncoding.EncodeToString([]byte("ss://" + strings.Repeat("a", 80)))
	for _, tc := range []struct {
		body, want string
	}{
		{"<!doctype html><html><body>login</body></html>", "返回了网页"},
		{"ss://example\nvmess://example", "节点链接列表"},
		{base64Links, "Base64 节点链接"},
		{"proxies: [bad", "不是有效的 Clash/Mihomo YAML"},
		{"proxy-providers:\n  sub:\n    type: http", "没有可用的 proxies 节点"},
	} {
		_, _, err := ParseSubscription([]byte(tc.body))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("body category %q: got %v, want %q", tc.want, err, tc.want)
		}
	}
}

func TestImportSkipsBadPortAndKeepsHysteria2PortHopping(t *testing.T) {
	input := `proxies:
  - name: working
    type: ss
    server: example.com
    port: 443
    cipher: aes-128-gcm
    password: secret
  - name: provider-info
    type: ss
    server: example.com
    port: 0
    cipher: aes-128-gcm
    password: secret
  - name: hopping
    type: hysteria2
    server: example.com
    ports: 443-444,8443
    password: secret
proxy-groups:
  - name: choice
    type: select
    proxies: [working, provider-info, hopping]
`
	proxies, groups, report, err := ParseSubscriptionReport([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(proxies) != 2 || report.SkippedInvalidPorts != 1 || report.FirstInvalidIndex != 2 || report.FirstInvalidType != "ss" {
		t.Fatalf("proxies=%d report=%+v", len(proxies), report)
	}
	if proxies[1]["port"] != 443 {
		t.Fatalf("hysteria2 fallback port=%v", proxies[1]["port"])
	}
	if len(groups) != 1 || len(groups[0].Proxies) != 2 || groups[0].Proxies[1] != "hopping" {
		t.Fatalf("groups=%+v", groups)
	}
	if _, _, err := ParseSubscription([]byte(input)); err != nil {
		t.Fatalf("compatibility parser failed: %v", err)
	}
}

func TestImportExplainsWhenAllPortsAreBad(t *testing.T) {
	input := []byte("proxies:\n  - name: private-name\n    type: ss\n    server: secret.example\n    port: 0\n")
	_, _, report, err := ParseSubscriptionReport(input)
	if err == nil || !strings.Contains(err.Error(), "第 1 个") || !strings.Contains(err.Error(), "类型 ss") || strings.Contains(err.Error(), "private-name") || strings.Contains(err.Error(), "secret.example") || report.SkippedInvalidPorts != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestRejectInvalidHysteria2PortRanges(t *testing.T) {
	if first, ok := validPortRanges(443); !ok || first != 443 {
		t.Fatalf("numeric port hopping value: first=%d valid=%v", first, ok)
	}
	for _, ports := range []string{"0-443", "443-2", "1-65536", "443,,8443", "abc"} {
		if _, ok := validPortRanges(ports); ok {
			t.Errorf("accepted ports %q", ports)
		}
	}
}

func TestRejectInvalidInput(t *testing.T) {
	for _, raw := range []string{"http://example.com/path", "https://user:pass@example.com/x", "https://example.com/x#fragment"} {
		if ValidateSourceURL(raw) == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, ip := range []string{"127.0.0.1", "192.168.1.1", "169.254.1.1", "198.18.0.1", "203.0.113.10", "::1", "2001:db8::1"} {
		if IsPublicIP(net.ParseIP(ip)) {
			t.Fatalf("accepted local IP %s", ip)
		}
	}
	for _, raw := range []string{"proxies: []", "proxies:\n  - name: bad\n    type: ss\n    server: example.com\n    port: 0", "proxies: []\n---\nproxies: []"} {
		if _, _, err := ParseSubscription([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
