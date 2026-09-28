//go:build linux

package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"fnvpn/internal/app"
)

type request struct {
	Op      string `json:"op"`
	URL     string `json:"url,omitempty"`
	Content []byte `json:"content,omitempty"`
	Group   string `json:"group,omitempty"`
	Node    string `json:"node,omitempty"`
	Mode    string `json:"mode,omitempty"`
}
type response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Data  any    `json:"data,omitempty"`
}
type helper struct {
	mu                                sync.Mutex
	state                             app.State
	lastConnectivity                  map[string]any
	dest, etc, varDir, binary, secret string
	core                              *exec.Cmd
	done                              chan struct{}
	pending                           bool
	timer                             *time.Timer
	shutdown                          chan struct{}
	version                           string
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: fnvpn daemon|web|ctl|recover")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "daemon":
		err = runDaemon()
	case "web":
		err = runWeb()
	case "ctl":
		err = runCtl()
	case "recover":
		err = recoverStandalone()
	default:
		err = errors.New("unknown command")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func envPath(key string) (string, error) {
	p := os.Getenv(key)
	if p == "" || !filepath.IsAbs(p) {
		return "", fmt.Errorf("missing %s", key)
	}
	return p, nil
}
func atomicWrite(path string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".fnvpn-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func (h *helper) statePath() string { return filepath.Join(h.etc, "state.json") }
func (h *helper) save() error {
	b, err := json.Marshal(h.state)
	if err != nil {
		return err
	}
	return atomicWrite(h.statePath(), b, 0600)
}
func (h *helper) load() error {
	h.state = app.NewState()
	b, err := os.ReadFile(h.statePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = json.Unmarshal(b, &h.state); err != nil {
		return err
	}
	if h.state.Selected == nil {
		h.state.Selected = map[string]string{}
	}
	return nil
}
func (h *helper) logEvent(event string) {
	f, err := os.OpenFile(filepath.Join(h.varDir, "events.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err == nil {
		fmt.Fprintln(f, time.Now().Format(time.RFC3339), event)
		f.Close()
	}
}

func runDaemon() error {
	dest, e := envPath("TRIM_APPDEST")
	if e != nil {
		return e
	}
	etc, e := envPath("TRIM_PKGETC")
	if e != nil {
		return e
	}
	v, e := envPath("TRIM_PKGVAR")
	if e != nil {
		return e
	}
	uid, e := strconv.Atoi(os.Getenv("FNVPN_UID"))
	if e != nil || uid <= 0 {
		return errors.New("invalid package uid")
	}
	gid, e := strconv.Atoi(os.Getenv("FNVPN_GID"))
	if e != nil || gid <= 0 {
		return errors.New("invalid package gid")
	}
	if os.Geteuid() != 0 {
		return errors.New("root required for TUN helper")
	}
	if err := os.MkdirAll(etc, 0700); err != nil {
		return err
	}
	if err := os.MkdirAll(v, 0700); err != nil {
		return err
	}
	h := &helper{dest: dest, etc: etc, varDir: v, binary: filepath.Join(dest, "bin", "mihomo"), shutdown: make(chan struct{}), version: "v1.19.31"}
	if err := h.load(); err != nil {
		return err
	}
	if err := h.recover(); err != nil {
		h.logEvent("恢复检查失败")
		return err
	}
	secretBytes, e := os.ReadFile(filepath.Join(etc, "control.secret"))
	if e == nil {
		h.secret = strings.TrimSpace(string(secretBytes))
	}
	if h.secret == "" {
		h.secret, e = app.NewSecret()
		if e != nil {
			return errors.New("无法生成控制密钥")
		}
		if e := atomicWrite(filepath.Join(etc, "control.secret"), []byte(h.secret), 0600); e != nil {
			return e
		}
	}
	helperSock := filepath.Join(dest, "helper.sock")
	_ = os.Remove(helperSock)
	l, e := net.Listen("unix", helperSock)
	if e != nil {
		return e
	}
	defer l.Close()
	defer os.Remove(helperSock)
	if e = os.Chown(helperSock, 0, gid); e != nil {
		return e
	}
	if e = os.Chmod(helperSock, 0660); e != nil {
		return e
	}
	appSock := filepath.Join(dest, "app.sock")
	_ = os.Remove(appSock)
	webListener, e := net.Listen("unix", appSock)
	if e != nil {
		return e
	}
	defer webListener.Close()
	defer os.Remove(appSock)
	gwUID := 0
	if raw := os.Getenv("FNVPN_GATEWAY_UID"); raw != "" {
		gwUID, e = strconv.Atoi(raw)
		if e != nil || gwUID < 0 {
			return errors.New("invalid gateway uid")
		}
	}
	if e = os.Chown(appSock, gwUID, 0); e != nil {
		return e
	}
	if e = os.Chmod(appSock, 0600); e != nil {
		return e
	}
	file, e := webListener.(*net.UnixListener).File()
	if e != nil {
		return e
	}
	defer file.Close()
	cmd := exec.Command(os.Args[0], "web")
	cmd.ExtraFiles = []*os.File{file}
	cmd.Env = []string{"TRIM_APPDEST=" + dest, "FNVPN_HELPER_SOCKET=" + helperSock, "FNVPN_GATEWAY_UID=" + strconv.Itoa(gwUID)}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if e = cmd.Start(); e != nil {
		return e
	}
	webDone := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(webDone)
		select {
		case h.shutdown <- struct{}{}:
		default:
		}
	}()
	if h.state.Enabled && len(h.state.Proxies) > 0 {
		h.mu.Lock()
		if e = h.startCore(true); e != nil {
			h.state.Enabled = false
			h.state.LastError = e.Error()
			_ = h.save()
		}
		h.mu.Unlock()
	}
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go h.handle(conn, uid)
		}
	}()
	guardStop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-guardStop:
				return
			case <-ticker.C:
				h.mu.Lock()
				if h.core != nil {
					if checkResolver() != nil {
						h.stopCore()
						h.state.Enabled = false
						h.state.LastError = "系统 DNS 变为本地或私有地址，已恢复网络"
						_ = h.save()
						h.logEvent("DNS 配置变化，网络已恢复")
						h.mu.Unlock()
						continue
					}
					v6, _ := command(2*time.Second, "ip", "-6", "route", "show", "default")
					if strings.TrimSpace(v6) != "" {
						h.stopCore()
						h.state.Enabled = false
						h.state.LastError = "检测到新增 IPv6 默认路由，已恢复网络"
						_ = h.save()
						h.logEvent("IPv6 路由变化，网络已恢复")
					}
				}
				h.mu.Unlock()
			}
		}
	}()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	select {
	case <-sig:
	case <-h.shutdown:
	case <-webDone:
	}
	close(guardStop)
	h.mu.Lock()
	h.stopCore()
	h.mu.Unlock()
	_ = cmd.Process.Kill()
	select {
	case <-webDone:
	case <-time.After(2 * time.Second):
	}
	return nil
}

func peerUID(c *net.UnixConn) (uint32, error) {
	raw, e := c.SyscallConn()
	if e != nil {
		return 0, e
	}
	var u *syscall.Ucred
	var inner error
	e = raw.Control(func(fd uintptr) { u, inner = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED) })
	if e != nil {
		return 0, e
	}
	if inner != nil {
		return 0, inner
	}
	return u.Uid, nil
}
func (h *helper) handle(c net.Conn, uid int) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(90 * time.Second))
	u, e := peerUID(c.(*net.UnixConn))
	if e != nil || (u != uint32(uid) && u != 0) {
		return
	}
	var r request
	if e = json.NewDecoder(io.LimitReader(c, 3<<20)).Decode(&r); e != nil {
		return
	}
	if r.Op == "connectivity" {
		h.mu.Lock()
		running, mode := h.core != nil, h.state.Mode
		h.mu.Unlock()
		result := runConnectivityDiagnostics(running, mode)
		h.mu.Lock()
		h.lastConnectivity = result
		h.mu.Unlock()
		_ = json.NewEncoder(c).Encode(ok(result))
		return
	}
	h.mu.Lock()
	out := h.dispatch(r)
	if !out.OK {
		h.state.LastError = out.Error
		_ = h.save()
		h.logEvent("操作失败")
	}
	h.mu.Unlock()
	_ = json.NewEncoder(c).Encode(out)
}
func fail(err error) response { return response{Error: err.Error()} }
func ok(data any) response    { return response{OK: true, Data: data} }
func (h *helper) dispatch(r request) response {
	if h.pending && (r.Op == "import-file" || r.Op == "import-url" || r.Op == "update" || r.Op == "select" || r.Op == "mode") {
		return fail(errors.New("请先等待管理连接确认"))
	}
	switch r.Op {
	case "status":
		return ok(h.status())
	case "import-file":
		return h.importData(r.Content, "")
	case "import-url":
		if e := app.ValidateSourceURL(r.URL); e != nil {
			return fail(e)
		}
		b, e := fetchSubscription(r.URL)
		if e != nil {
			return fail(e)
		}
		return h.importData(b, r.URL)
	case "update":
		if h.state.SourceURL == "" {
			return fail(errors.New("当前配置不是 URL 订阅"))
		}
		b, e := fetchSubscription(h.state.SourceURL)
		if e != nil {
			return fail(e)
		}
		return h.importData(b, h.state.SourceURL)
	case "start":
		if h.core != nil {
			return ok(h.status())
		}
		if e := h.startCore(true); e != nil {
			h.state.LastError = e.Error()
			_ = h.save()
			return fail(e)
		}
		return ok(h.status())
	case "confirm":
		if h.core == nil || !h.pending {
			return fail(errors.New("没有待确认的启动"))
		}
		h.pending = false
		if h.timer != nil {
			h.timer.Stop()
		}
		h.state.Enabled = true
		h.state.LastError = ""
		_ = h.save()
		h.logEvent("TUN 已确认开启")
		return ok(h.status())
	case "stop", "recover":
		h.stopCore()
		h.state.Enabled = false
		h.state.LastError = ""
		_ = h.save()
		h.logEvent("网络已恢复")
		return ok(h.status())
	case "select":
		return h.selectNode(r.Group, r.Node)
	case "mode":
		return h.setMode(r.Mode)
	case "diagnostics":
		return ok(h.diagnostics())
	case "verify":
		return ok(verifyExit(h.core != nil))
	case "shutdown":
		h.stopCore()
		h.state.Enabled = false
		_ = h.save()
		go func() { h.shutdown <- struct{}{} }()
		return ok(nil)
	default:
		return fail(errors.New("不支持的操作"))
	}
}

func (h *helper) status() map[string]any {
	return app.PublicState(h.state, h.core != nil, h.version, h.checks())
}
func (h *helper) checks() map[string]any {
	dns := "公网 DNS"
	if checkResolver() != nil {
		dns = "本地或私有 DNS，阻止开启"
	}
	return map[string]any{"tun_device": fileExists("/dev/net/tun"), "dns": dns, "host_ipv6": "未验证；存在 IPv6 默认路由时阻止开启", "docker": "未覆盖", "management_confirmation": !h.pending, "api": "仅经 fnOS 网关"}
}
func fileExists(p string) bool { _, e := os.Stat(p); return e == nil }
func (h *helper) diagnostics() map[string]any {
	b, _ := os.ReadFile(filepath.Join(h.varDir, "events.log"))
	lines := strings.Split(string(b), "\n")
	if len(lines) > 100 {
		lines = lines[len(lines)-100:]
	}
	status := map[string]any{"configured": len(h.state.Proxies) > 0, "node_count": len(h.state.Proxies), "source_rule_count": h.state.SourceRules, "applied_rule_count": len(h.state.Rules), "skipped_rule_count": h.state.SkippedRules, "running": h.core != nil, "tun": h.core != nil, "mode": h.state.Mode, "last_update": h.state.LastUpdate, "last_error": h.state.LastError, "core_version": h.version, "checks": h.checks()}
	interfaces := []map[string]any{}
	if list, e := net.Interfaces(); e == nil {
		for _, iface := range list {
			interfaces = append(interfaces, map[string]any{"name": iface.Name, "up": iface.Flags&net.FlagUp != 0, "mtu": iface.MTU})
		}
	}
	versions := map[string]string{"mihomo": h.version, "fnos": os.Getenv("TRIM_SYS_VERSION"), "kernel": os.Getenv("TRIM_KERNEL_VERSION")}
	return map[string]any{"status": status, "events": strings.Join(lines, "\n"), "routes": safeRoutes(), "interfaces": interfaces, "versions": versions, "connectivity": h.lastConnectivity, "notice": "日志只记录固定事件和脱敏错误；不包含原始内核输出或订阅内容"}
}

func fetchSubscription(raw string) ([]byte, error) {
	u, _ := url.Parse(raw)
	host := u.Hostname()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ips, e := net.DefaultResolver.LookupIPAddr(ctx, host)
	if e != nil || len(ips) == 0 {
		return nil, errors.New("订阅域名解析失败")
	}
	for _, a := range ips {
		if !app.IsPublicIP(a.IP) {
			return nil, errors.New("订阅地址指向非公网 IP")
		}
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	if port != "443" {
		return nil, errors.New("订阅仅允许 HTTPS 标准端口")
	}
	addr := net.JoinHostPort(ips[0].IP.String(), port)
	tr := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, addr)
	}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect blocked") }}
	req, _ := http.NewRequestWithContext(ctx, "GET", raw, nil)
	req.Header.Set("User-Agent", "clash.meta")
	resp, e := client.Do(req)
	if e != nil {
		return nil, errors.New("订阅下载失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, errors.New("订阅服务器返回非成功状态")
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, 2<<20+1))
	if e != nil || len(b) > 2<<20 {
		return nil, errors.New("订阅内容读取失败或过大")
	}
	return b, nil
}

func (h *helper) importData(b []byte, source string) response {
	proxies, groups, report, e := app.ParseSubscriptionReport(b)
	if e != nil {
		return fail(e)
	}
	candidate := h.state
	candidate.Proxies = proxies
	candidate.Groups = groups
	candidate.Rules = report.Rules
	candidate.SourceRules = report.SourceRules
	candidate.SkippedRules = report.SkippedRules
	candidate.SourceURL = source
	candidate.Selected = map[string]string{"节点选择": proxies[0]["name"].(string)}
	candidate.SelectedGroup = "节点选择"
	candidate.LastUpdate = time.Now().Format(time.RFC3339)
	candidate.LastError = ""
	if candidate.Mode == "" {
		candidate.Mode = "rule"
	}
	ip, e := app.ResolveEndpoints(candidate)
	if e != nil {
		return fail(e)
	}
	config, e := app.EffectiveConfig(candidate, h.secret, ip)
	if e != nil {
		return fail(e)
	}
	path := filepath.Join(h.etc, "candidate.yaml")
	if e = atomicWrite(path, config, 0600); e != nil {
		return fail(errors.New("无法保存候选配置"))
	}
	defer os.Remove(path)
	if e = h.testConfig(path); e != nil {
		return fail(e)
	}
	old := h.state
	wasRunning := h.core != nil
	active := filepath.Join(h.etc, "active.yaml")
	previous, readErr := os.ReadFile(active)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return fail(errors.New("无法读取旧配置"))
	}
	if wasRunning {
		h.stopCore()
	}
	if e = atomicWrite(active, config, 0600); e != nil {
		if wasRunning {
			_ = h.startCore(false)
		}
		return fail(errors.New("保存有效配置失败"))
	}
	rollback := func() {
		if h.core != nil {
			h.stopCore()
		}
		h.state = old
		if readErr == nil {
			_ = atomicWrite(active, previous, 0600)
		} else {
			_ = os.Remove(active)
		}
		if wasRunning {
			_ = h.startCore(false)
		}
	}
	if wasRunning {
		if e = h.launchCore(active); e != nil {
			rollback()
			return fail(errors.New("新配置启动失败，已尝试恢复旧配置"))
		}
		payload, _ := json.Marshal(map[string]string{"name": candidate.Selected["节点选择"]})
		if e = h.coreRequest("PUT", "/proxies/"+url.PathEscape("节点选择"), payload); e != nil {
			rollback()
			return fail(errors.New("新节点未能生效，已尝试恢复旧配置"))
		}
	}
	h.state = candidate
	h.state.Enabled = wasRunning && old.Enabled
	if e = h.save(); e != nil {
		rollback()
		return fail(errors.New("保存订阅状态失败，已尝试恢复旧配置"))
	}
	result := h.status()
	messages := []string{}
	if report.SkippedInvalidPorts > 0 {
		messages = append(messages, fmt.Sprintf("已跳过 %d 个端口无效的节点", report.SkippedInvalidPorts))
	}
	if report.SourceRules == 0 {
		messages = append(messages, "订阅未包含内嵌规则，使用内置分流")
	} else {
		messages = append(messages, fmt.Sprintf("已采用 %d/%d 条订阅规则，内置分流兜底", len(report.Rules), report.SourceRules))
		if report.SkippedRules > 0 {
			messages = append(messages, fmt.Sprintf("已跳过 %d 条暂不支持的规则", report.SkippedRules))
		}
	}
	message := strings.Join(messages, "；")
	result["import_notice"] = message
	h.logEvent("订阅已校验并更新；" + message)
	return ok(result)
}

func (h *helper) selectNode(group, node string) response {
	valid := false
	if group == "节点选择" {
		for _, p := range h.state.Proxies {
			if p["name"] == node {
				valid = true
			}
		}
	} else {
		for _, g := range h.state.Groups {
			if g.Name == group {
				for _, n := range g.Proxies {
					if n == node {
						valid = true
					}
				}
			}
		}
	}
	if !valid {
		return fail(errors.New("代理组或节点不在当前配置中"))
	}
	if h.core != nil && group != h.state.SelectedGroup {
		old := h.state
		candidate := h.state
		candidate.Selected = map[string]string{}
		for k, v := range h.state.Selected {
			candidate.Selected[k] = v
		}
		candidate.Selected[group] = node
		candidate.SelectedGroup = group
		ips, e := app.ResolveEndpoints(candidate)
		if e != nil {
			return fail(e)
		}
		b, e := app.EffectiveConfig(candidate, h.secret, ips)
		if e != nil {
			return fail(e)
		}
		path := filepath.Join(h.etc, "candidate.yaml")
		if e = atomicWrite(path, b, 0600); e != nil {
			return fail(errors.New("无法保存候选配置"))
		}
		defer os.Remove(path)
		if e = h.testConfig(path); e != nil {
			return fail(e)
		}
		active := filepath.Join(h.etc, "active.yaml")
		previous, readErr := os.ReadFile(active)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return fail(errors.New("无法读取旧配置"))
		}
		h.stopCore()
		if e = atomicWrite(active, b, 0600); e != nil {
			_ = h.startCore(false)
			return fail(errors.New("保存代理组配置失败"))
		}
		rollback := func() {
			if h.core != nil {
				h.stopCore()
			}
			h.state = old
			if readErr == nil {
				_ = atomicWrite(active, previous, 0600)
			} else {
				_ = os.Remove(active)
			}
			_ = h.startCore(false)
		}
		if e = h.launchCore(active); e != nil {
			rollback()
			return fail(errors.New("代理组切换失败，已尝试恢复旧配置"))
		}
		payload, _ := json.Marshal(map[string]string{"name": node})
		if e = h.coreRequest("PUT", "/proxies/"+url.PathEscape(group), payload); e != nil {
			rollback()
			return fail(e)
		}
		h.state = candidate
		if e = h.save(); e != nil {
			rollback()
			return fail(errors.New("保存代理组状态失败"))
		}
	} else {
		old := h.state
		candidate := h.state
		candidate.Selected = map[string]string{}
		for k, v := range h.state.Selected {
			candidate.Selected[k] = v
		}
		candidate.Selected[group] = node
		candidate.SelectedGroup = group
		if h.core != nil {
			payload, _ := json.Marshal(map[string]string{"name": node})
			if e := h.coreRequest("PUT", "/proxies/"+url.PathEscape(group), payload); e != nil {
				return fail(e)
			}
		}
		h.state = candidate
		if e := h.save(); e != nil {
			h.state = old
			if h.core != nil {
				if oldNode := old.Selected[group]; oldNode != "" {
					payload, _ := json.Marshal(map[string]string{"name": oldNode})
					_ = h.coreRequest("PUT", "/proxies/"+url.PathEscape(group), payload)
				}
			}
			return fail(errors.New("保存节点选择失败"))
		}
	}
	return ok(h.status())
}
func (h *helper) setMode(mode string) response {
	if mode != "rule" && mode != "global" && mode != "direct" {
		return fail(errors.New("模式无效"))
	}
	old := h.state.Mode
	if h.core != nil {
		b, _ := json.Marshal(map[string]string{"mode": mode})
		if e := h.coreRequest("PATCH", "/configs", b); e != nil {
			return fail(e)
		}
	}
	h.state.Mode = mode
	if e := h.save(); e != nil {
		h.state.Mode = old
		if h.core != nil {
			b, _ := json.Marshal(map[string]string{"mode": old})
			_ = h.coreRequest("PATCH", "/configs", b)
		}
		return fail(errors.New("保存模式失败"))
	}
	return ok(h.status())
}
