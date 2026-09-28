package main

import (
	"net"
	"regexp"
)

type siteCheck struct {
	Name              string `json:"name"`
	Host              string `json:"host"`
	TargetIPv4        string `json:"target_ipv4,omitempty"`
	Reachable         bool   `json:"reachable"`
	HTTPStatus        int    `json:"http_status,omitempty"`
	Error             string `json:"error,omitempty"`
	DurationMS        int64  `json:"duration_ms"`
	RouteInterface    string `json:"route_interface,omitempty"`
	LANRouteInterface string `json:"lan_route_interface,omitempty"`
}

type dnsProbe struct {
	Name           string `json:"name"`
	Reachable      bool   `json:"reachable"`
	Error          string `json:"error,omitempty"`
	DurationMS     int64  `json:"duration_ms"`
	RouteInterface string `json:"route_interface,omitempty"`
}

type routeProbe struct {
	Target            string `json:"target"`
	RouteInterface    string `json:"route_interface,omitempty"`
	LANRouteInterface string `json:"lan_route_interface,omitempty"`
}

func dnsGuidance(checks []siteCheck, probes []dnsProbe) string {
	if len(checks) == 0 {
		return ""
	}
	for _, check := range checks {
		if check.Error != "DNS 解析失败" {
			return ""
		}
	}
	if len(probes) < 2 {
		return "系统 DNS 解析均失败；需要比较 Mihomo DNS 与节点引导 DNS。"
	}
	if probes[0].Reachable {
		return "Mihomo 本地 DNS 可以回答，但系统 DNS 查询未完成；请检查 DNS 劫持和源地址策略路由。"
	}
	if probes[1].Reachable {
		return "节点引导 DNS 可以回答，但 Mihomo 本地 DNS 未回答；请检查所选代理节点及上游 DoH。"
	}
	return "Mihomo 本地 DNS 和节点引导 DNS 均未回答；请检查 DNS 出站路由、网络连通性及所选节点。"
}

var routeDevicePattern = regexp.MustCompile(`\bdev\s+(\S+)`)
var routeSourcePattern = regexp.MustCompile(`\bsrc\s+(\S+)`)

func routeDevice(output string) string {
	match := routeDevicePattern.FindStringSubmatch(output)
	if len(match) == 2 {
		return match[1]
	}
	return ""
}

func defaultRouteSource(output string) string {
	match := routeSourcePattern.FindStringSubmatch(output)
	if len(match) == 2 && net.ParseIP(match[1]) != nil {
		return match[1]
	}
	return ""
}

func connectivityGuidance(running bool, mode string, tun, rule, table bool, checks []siteCheck) string {
	if !running {
		return "系统 TUN 未运行；这些结果是宿主机直连基线。"
	}
	if mode == "direct" {
		return "当前是直连模式；公网请求直连是预期行为。"
	}
	if !tun || !rule || !table {
		return "TUN 接口或策略路由不完整；请先查看诊断报告中的路由，暂勿认为宿主机流量已接管。"
	}
	viaTun, knownRoutes, boundRoutes, boundBypass := 0, 0, 0, 0
	for _, item := range checks {
		if item.RouteInterface != "" {
			knownRoutes++
		}
		if item.LANRouteInterface != "" {
			boundRoutes++
		}
		if item.RouteInterface == "fnvpn0" {
			viaTun++
			if item.LANRouteInterface != "" && item.LANRouteInterface != "fnvpn0" {
				boundBypass++
			}
		}
	}
	if knownRoutes == 0 {
		return "目标地址均未取得路由结果；请先检查宿主机 DNS 和路由。"
	}
	if viaTun == 0 {
		return "已解析目标的普通路由均未指向 fnvpn0；请检查 fnOS 策略路由优先级。"
	}
	if viaTun < knownRoutes {
		return "部分目标未指向 fnvpn0；可能存在目标排除或策略路由覆盖。"
	}
	if boundBypass > 0 {
		return "普通请求路由指向 fnvpn0，但指定 NAS 地址时有目标绕过 TUN；源地址策略路由可能优先于代理规则。"
	}
	if knownRoutes < len(checks) {
		return "已解析目标的普通路由指向 fnvpn0；其余目标未取得路由结果，请检查 DNS。"
	}
	if boundRoutes == 0 {
		return "普通请求路由指向 fnvpn0；未能核对绑定 NAS 地址的请求，请检查默认路由源地址。"
	}
	return "测试目标路由指向 fnvpn0；可达性仍不能单独证明代理出口，请与关闭 TUN 时的出口 IP 对比。"
}
