package main

import (
	"strings"
	"testing"
)

func TestRouteParsing(t *testing.T) {
	if got := defaultRouteSource("default via 192.0.2.1 dev ens32 proto dhcp src 192.0.2.10 metric 100"); got != "192.0.2.10" {
		t.Fatalf("default source = %q", got)
	}
	if got := defaultRouteSource("default dev ens32"); got != "" {
		t.Fatalf("missing source = %q", got)
	}
	if got := routeDevice("142.250.1.1 dev fnvpn0 src 198.18.0.1 uid 0\n"); got != "fnvpn0" {
		t.Fatalf("TUN route device = %q", got)
	}
	if got := routeDevice("142.250.1.1 via 192.0.2.1 dev ens32 src 192.0.2.10\n"); got != "ens32" {
		t.Fatalf("bound route device = %q", got)
	}
}

func TestConnectivityGuidanceSourceRouteBypass(t *testing.T) {
	checks := []siteCheck{{RouteInterface: "fnvpn0", LANRouteInterface: "ens32"}}
	guidance := connectivityGuidance(true, "rule", true, true, true, checks)
	if !strings.Contains(guidance, "指定 NAS 地址时有目标绕过 TUN") {
		t.Fatalf("unexpected guidance: %q", guidance)
	}
}

func TestConnectivityGuidanceWithoutResolvedRoutes(t *testing.T) {
	checks := []siteCheck{{Error: "DNS 解析失败"}}
	guidance := connectivityGuidance(true, "rule", true, true, true, checks)
	if !strings.Contains(guidance, "DNS") || strings.Contains(guidance, "均未指向 fnvpn0") {
		t.Fatalf("unexpected guidance: %q", guidance)
	}
}

func TestDNSGuidanceSeparatesHijackFromUpstream(t *testing.T) {
	checks := []siteCheck{{Error: "DNS 解析失败"}, {Error: "DNS 解析失败"}}
	for _, tc := range []struct {
		probes []dnsProbe
		want   string
	}{
		{[]dnsProbe{{Reachable: true}, {}}, "DNS 劫持"},
		{[]dnsProbe{{}, {Reachable: true}}, "Mihomo 本地 DNS 未回答"},
		{[]dnsProbe{{}, {}}, "均未回答"},
	} {
		if got := dnsGuidance(checks, tc.probes); !strings.Contains(got, tc.want) {
			t.Errorf("guidance = %q, want %q", got, tc.want)
		}
	}
}
