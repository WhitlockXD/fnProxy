//go:build linux

package main

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

func connectionError(err error) string {
	var timeout net.Error
	var certificate x509.UnknownAuthorityError
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "连接超时"
	}
	if errors.As(err, &certificate) {
		return "TLS 证书验证失败"
	}
	return "连接失败"
}

func checkSite(name, host, path, lanSource string) (result siteCheck) {
	result = siteCheck{Name: name, Host: host}
	started := time.Now()
	defer func() { result.DurationMS = time.Since(started).Milliseconds() }()
	lookupContext, cancelLookup := context.WithTimeout(context.Background(), 8*time.Second)
	addresses, err := net.DefaultResolver.LookupIPAddr(lookupContext, host)
	cancelLookup()
	if err != nil {
		result.Error = "DNS 解析失败"
		return
	}
	var target net.IP
	for _, address := range addresses {
		if ipv4 := address.IP.To4(); ipv4 != nil {
			target = ipv4
			break
		}
	}
	if target == nil {
		result.Error = "没有 IPv4 地址"
		return
	}
	result.TargetIPv4 = target.String()
	if output, err := command(2*time.Second, "ip", "-4", "route", "get", target.String()); err == nil {
		result.RouteInterface = routeDevice(output)
	}
	if lanSource != "" {
		if output, err := command(2*time.Second, "ip", "-4", "route", "get", target.String(), "from", lanSource); err == nil {
			result.LANRouteInterface = routeDevice(output)
		}
	}
	transport := &http.Transport{
		Proxy:               nil,
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: 5 * time.Second,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(target.String(), "443"))
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	requestContext, cancelRequest := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancelRequest()
	req, err := http.NewRequestWithContext(requestContext, http.MethodGet, "https://"+host+path, nil)
	if err != nil {
		result.Error = "测试请求无效"
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		result.Error = connectionError(err)
		return
	}
	resp.Body.Close()
	result.Reachable = true
	result.HTTPStatus = resp.StatusCode
	return
}

func probeDNS(name, address string) (result dnsProbe) {
	result.Name = name
	started := time.Now()
	defer func() { result.DurationMS = time.Since(started).Milliseconds() }()
	if host, _, err := net.SplitHostPort(address); err == nil {
		if output, err := command(2*time.Second, "ip", "-4", "route", "get", host); err == nil {
			result.RouteInterface = routeDevice(output)
		}
	}
	resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "udp", address)
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	addresses, err := resolver.LookupIP(ctx, "ip4", "www.baidu.com")
	if err != nil || len(addresses) == 0 {
		result.Error = "DNS 查询超时或失败"
		return
	}
	result.Reachable = true
	return
}

func probeRoute(target, lanSource string) (result routeProbe) {
	result.Target = target
	if output, err := command(2*time.Second, "ip", "-4", "route", "get", target); err == nil {
		result.RouteInterface = routeDevice(output)
	}
	if lanSource != "" {
		if output, err := command(2*time.Second, "ip", "-4", "route", "get", target, "from", lanSource); err == nil {
			result.LANRouteInterface = routeDevice(output)
		}
	}
	return
}

func runConnectivityDiagnostics(running bool, mode string) map[string]any {
	defaultRoute, _ := command(2*time.Second, "ip", "-4", "route", "show", "default")
	lanSource := defaultRouteSource(defaultRoute)
	publicRoute := probeRoute("1.1.1.1", lanSource)
	rules, _ := command(2*time.Second, "ip", "-4", "rule", "show")
	tableRoutes, _ := command(2*time.Second, "ip", "-4", "route", "show", "table", "19091")
	tun := fileExists("/sys/class/net/fnvpn0")
	rule := strings.Contains(rules, "lookup 19091")
	table := strings.Contains(tableRoutes, "dev fnvpn0")
	targets := []struct{ name, host, path string }{
		{"YouTube", "www.youtube.com", "/generate_204"},
		{"Google", "www.google.com", "/generate_204"},
		{"GitHub", "github.com", "/"},
		{"百度", "www.baidu.com", "/"},
	}
	checks := make([]siteCheck, len(targets))
	dnsChecks := make([]dnsProbe, 2)
	var group sync.WaitGroup
	for index, target := range targets {
		group.Add(1)
		go func(index int, target struct{ name, host, path string }) {
			defer group.Done()
			checks[index] = checkSite(target.name, target.host, target.path, lanSource)
		}(index, target)
	}
	for index, target := range []struct{ name, address string }{{"Mihomo 本地 DNS", "127.0.0.1:19092"}, {"节点引导 DNS", "223.5.5.5:53"}} {
		group.Add(1)
		go func(index int, target struct{ name, address string }) {
			defer group.Done()
			dnsChecks[index] = probeDNS(target.name, target.address)
		}(index, target)
	}
	group.Wait()
	guidance := connectivityGuidance(running, mode, tun, rule, table, checks)
	if detail := dnsGuidance(checks, dnsChecks); detail != "" {
		guidance = detail
	}
	return map[string]any{
		"checked_at":      time.Now().Format(time.RFC3339),
		"running":         running,
		"mode":            mode,
		"tun_interface":   tun,
		"policy_rule":     rule,
		"tun_route_table": table,
		"sites":           checks,
		"dns_checks":      dnsChecks,
		"public_route":    publicRoute,
		"guidance":        guidance,
		"scope":           "由 fnOS 宿主机管理组件发起；不代表 Docker 或其他网络命名空间",
	}
}
