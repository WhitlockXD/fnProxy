package app

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Group struct {
	Name    string   `json:"name" yaml:"name"`
	Type    string   `json:"type" yaml:"type"`
	Proxies []string `json:"proxies" yaml:"proxies"`
}

type State struct {
	SourceURL     string            `json:"source_url,omitempty"`
	Proxies       []map[string]any  `json:"proxies"`
	Groups        []Group           `json:"groups"`
	Rules         []Rule            `json:"rules,omitempty"`
	SourceRules   int               `json:"source_rules,omitempty"`
	SkippedRules  int               `json:"skipped_rules,omitempty"`
	SelectedGroup string            `json:"selected_group"`
	Selected      map[string]string `json:"selected"`
	Mode          string            `json:"mode"`
	Enabled       bool              `json:"enabled"`
	LastUpdate    string            `json:"last_update"`
	LastError     string            `json:"last_error"`
}

func NewState() State { return State{Selected: map[string]string{}, Mode: "rule"} }

func ParseSubscription(data []byte) ([]map[string]any, []Group, error) {
	proxies, groups, _, err := ParseSubscriptionReport(data)
	return proxies, groups, err
}

type ParseReport struct {
	SkippedInvalidPorts int
	FirstInvalidIndex   int
	FirstInvalidType    string
	Rules               []Rule
	SourceRules         int
	SkippedRules        int
}

func ParseSubscriptionReport(data []byte) ([]map[string]any, []Group, ParseReport, error) {
	report := ParseReport{}
	if len(data) == 0 || len(data) > 2<<20 {
		return nil, nil, report, errors.New("配置文件为空或超过 2 MiB")
	}
	var raw struct {
		Proxies []map[string]any `yaml:"proxies"`
		Groups  []Group          `yaml:"proxy-groups"`
		Rules   []any            `yaml:"rules"`
	}
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(false)
	if err := dec.Decode(&raw); err != nil {
		return nil, nil, report, subscriptionFormatError(data)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, nil, report, errors.New("仅支持单份 YAML 文档")
	}
	if len(raw.Proxies) == 0 || len(raw.Proxies) > 500 {
		return nil, nil, report, errors.New("配置中没有可用的 proxies 节点；请使用 Clash/Mihomo YAML 订阅，暂不支持仅含 proxy-providers 的配置")
	}
	names := map[string]bool{"DIRECT": true, "REJECT": true, "GLOBAL": true, "节点选择": true}
	validProxies := make([]map[string]any, 0, len(raw.Proxies))
	for index, p := range raw.Proxies {
		name, ok := p["name"].(string)
		if !ok || len(name) == 0 || len(name) > 120 || names[name] || strings.ContainsAny(name, "\r\n") {
			return nil, nil, report, errors.New("节点名称无效或重复")
		}
		kind, ok := p["type"].(string)
		if !ok || !map[string]bool{"ss": true, "vmess": true, "vless": true, "trojan": true, "socks5": true, "http": true, "hysteria2": true, "tuic": true, "snell": true, "wireguard": true}[kind] {
			return nil, nil, report, errors.New("存在不受支持的节点类型")
		}
		server, ok := p["server"].(string)
		if !ok || server == "" || len(server) > 253 || strings.ContainsAny(server, "/:@ \r\n") {
			return nil, nil, report, errors.New("存在无效的节点服务器地址")
		}
		if !validNodePort(p, kind) {
			report.SkippedInvalidPorts++
			if report.FirstInvalidIndex == 0 {
				report.FirstInvalidIndex = index + 1
				report.FirstInvalidType = kind
			}
			continue
		}
		names[name] = true
		validProxies = append(validProxies, p)
	}
	if len(validProxies) == 0 {
		return nil, nil, report, fmt.Errorf("没有可用节点：%d 个节点的端口无效（首个是第 %d 个，类型 %s）", report.SkippedInvalidPorts, report.FirstInvalidIndex, report.FirstInvalidType)
	}
	groups := []Group{}
	for _, g := range raw.Groups {
		if g.Type != "select" || g.Name == "" || len(g.Name) > 120 || names[g.Name] || strings.ContainsAny(g.Name, ",\r\n") {
			continue
		}
		valid := []string{}
		for _, n := range g.Proxies {
			if names[n] {
				valid = append(valid, n)
			}
		}
		if len(valid) > 0 {
			g.Proxies = valid
			groups = append(groups, g)
			names[g.Name] = true
		}
	}
	report.SourceRules = len(raw.Rules)
	report.Rules = parseSubscriptionRules(raw.Rules)
	report.SkippedRules = report.SourceRules - len(report.Rules)
	return validProxies, groups, report, nil
}

func validNodePort(proxy map[string]any, kind string) bool {
	port, err := strconv.Atoi(fmt.Sprint(proxy["port"]))
	validPort := err == nil && port >= 1 && port <= 65535
	if kind == "hysteria2" && proxy["ports"] != nil {
		first, validRange := validPortRanges(proxy["ports"])
		if !validRange {
			return false
		}
		proxy["ports"] = strings.ReplaceAll(fmt.Sprint(proxy["ports"]), " ", "")
		if !validPort {
			proxy["port"] = first
		}
		return true
	}
	return validPort
}

func validPortRanges(value any) (int, bool) {
	switch value.(type) {
	case string, int, int64:
	default:
		return 0, false
	}
	text := fmt.Sprint(value)
	if len(text) == 0 || len(text) > 512 {
		return 0, false
	}
	parts := strings.Split(text, ",")
	if len(parts) > 64 {
		return 0, false
	}
	first := 0
	for _, part := range parts {
		bounds := strings.Split(strings.TrimSpace(part), "-")
		if len(bounds) < 1 || len(bounds) > 2 {
			return 0, false
		}
		start, err := strconv.Atoi(strings.TrimSpace(bounds[0]))
		if err != nil || start < 1 || start > 65535 {
			return 0, false
		}
		if first == 0 {
			first = start
		}
		if len(bounds) == 2 {
			end, err := strconv.Atoi(strings.TrimSpace(bounds[1]))
			if err != nil || end < start || end > 65535 {
				return 0, false
			}
		}
	}
	return first, true
}

func ValidateSourceURL(raw string) error {
	if len(raw) > 2048 {
		return errors.New("订阅地址过长")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return errors.New("仅支持不含用户名的 HTTPS 订阅地址")
	}
	if strings.ContainsAny(raw, "\r\n") {
		return errors.New("订阅地址无效")
	}
	return nil
}

func PublicState(s State, running bool, coreVersion string, checks map[string]any) map[string]any {
	nodes := make([]string, 0, len(s.Proxies))
	for _, p := range s.Proxies {
		nodes = append(nodes, p["name"].(string))
	}
	groups := []string{"节点选择"}
	groupNodes := map[string][]string{"节点选择": nodes}
	for _, g := range s.Groups {
		groups = append(groups, g.Name)
		groupNodes[g.Name] = g.Proxies
	}
	return map[string]any{"configured": len(s.Proxies) > 0, "source": func() string {
		if s.SourceURL != "" {
			return "已保存 HTTPS 地址"
		}
		if len(s.Proxies) > 0 {
			return "本地配置"
		}
		return "未设置"
	}(), "nodes": nodes, "groups": groups, "group_nodes": groupNodes, "selected_group": s.SelectedGroup, "selected": s.Selected, "rule_count": len(s.Rules), "source_rule_count": s.SourceRules, "skipped_rule_count": s.SkippedRules, "mode": s.Mode, "desired_enabled": s.Enabled, "running": running, "tun": running, "last_update": s.LastUpdate, "last_error": s.LastError, "core_version": coreVersion, "checks": checks, "updated_at": time.Now().Format(time.RFC3339)}
}
