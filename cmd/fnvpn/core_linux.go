//go:build linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"fnvpn/internal/app"
)

func command(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	b, e := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if e != nil {
		return "", e
	}
	return string(b), nil
}
func safeRoutes() map[string]string {
	out := map[string]string{}
	for _, v := range []struct {
		k string
		a []string
	}{{"ipv4_default", []string{"-4", "route", "show", "default"}}, {"ipv4_rules", []string{"-4", "rule", "show"}}, {"fnvpn_table", []string{"-4", "route", "show", "table", "19091"}}, {"ipv6_default", []string{"-6", "route", "show", "default"}}} {
		s, e := command(2*time.Second, "ip", v.a...)
		if e == nil {
			if v.k == "fnvpn_table" {
				entries, throughTun := 0, 0
				for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
					if line == "" {
						continue
					}
					entries++
					if strings.Contains(line, " dev fnvpn0") {
						throughTun++
					}
				}
				out[v.k] = fmt.Sprintf("共 %d 条路由，其中 %d 条经 fnvpn0", entries, throughTun)
			} else {
				out[v.k] = redactAddresses(s)
			}
		}
	}
	return out
}
func redactAddresses(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for j, line := range lines {
		parts := strings.Fields(line)
		for i, p := range parts {
			if net.ParseIP(p) != nil {
				parts[i] = "[IP]"
			} else if _, _, err := net.ParseCIDR(p); err == nil {
				parts[i] = "[CIDR]"
			}
		}
		lines[j] = strings.Join(parts, " ")
	}
	return strings.Join(lines, "\n")
}
func (h *helper) preflight() error {
	if os.Geteuid() != 0 {
		return errors.New("需要特权组件")
	}
	if !fileExists("/dev/net/tun") {
		return errors.New("设备缺少 /dev/net/tun")
	}
	if e := checkResolver(); e != nil {
		return e
	}
	defaults, e := command(3*time.Second, "ip", "-4", "route", "show", "default")
	if e != nil || len(strings.Split(strings.TrimSpace(defaults), "\n")) != 1 || strings.TrimSpace(defaults) == "" {
		return errors.New("需要恰好一条 IPv4 默认路由")
	}
	v6, _ := command(3*time.Second, "ip", "-6", "route", "show", "default")
	if strings.TrimSpace(v6) != "" {
		return errors.New("检测到 IPv6 默认路由；当前版本尚未验证 IPv6 TUN，已阻止开启")
	}
	if fileExists("/sys/class/net/fnvpn0") {
		return errors.New("专用 TUN 接口已存在，请先恢复网络")
	}
	rules, _ := command(3*time.Second, "ip", "-4", "rule", "show")
	if strings.Contains(rules, "19091:") || strings.Contains(rules, "lookup 19091") {
		return errors.New("专用策略路由编号已被占用")
	}
	routes, _ := command(3*time.Second, "ip", "-4", "route", "show", "table", "19091")
	if strings.TrimSpace(routes) != "" {
		return errors.New("专用路由表已被占用")
	}
	for _, port := range []string{"19091", "19092"} {
		l, e := net.Listen("tcp", "127.0.0.1:"+port)
		if e != nil {
			return errors.New("Mihomo 控制或 DNS 端口被占用")
		}
		l.Close()
	}
	return nil
}

func checkResolver() error {
	f, e := os.Open("/etc/resolv.conf")
	if e != nil {
		return errors.New("无法检查系统 DNS")
	}
	defer f.Close()
	count := 0
	scanner := bufio.NewScanner(io.LimitReader(f, 64<<10))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}
		count++
		ip := net.ParseIP(fields[1])
		if !app.IsPublicIP(ip) {
			return errors.New("系统 DNS 指向本地或私有地址；为避免 DNS 绕过 TUN，已阻止开启")
		}
	}
	if scanner.Err() != nil || count == 0 {
		return errors.New("系统 DNS 配置无法验证，已阻止开启")
	}
	return nil
}

func (h *helper) prepareRuleData() error {
	for _, item := range []struct{ name, digest string }{
		{"cn-domain.mrs", "6c4f403acc88c339a9aa77ecd7f711bc2506699cf810681b868547fcd1a480a1"},
		{"cn-ip.mrs", "4cc9ab3b7e2bbd18e0420e09af42818d0748a596dd3daaed470bb2d9958d118d"},
	} {
		content, err := os.ReadFile(filepath.Join(h.dest, "rules", item.name))
		if err != nil || fmt.Sprintf("%x", sha256.Sum256(content)) != item.digest {
			return errors.New("内置分流数据缺失或校验失败")
		}
		target := filepath.Join(h.varDir, "rules", item.name)
		if previous, err := os.ReadFile(target); err == nil && bytes.Equal(previous, content) {
			continue
		}
		if err := atomicWrite(target, content, 0600); err != nil {
			return errors.New("无法保存分流数据")
		}
	}
	return nil
}

func (h *helper) testConfig(path string) error {
	if err := h.prepareRuleData(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, h.binary, "-t", "-f", path, "-d", h.varDir)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=" + h.varDir}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if e := cmd.Run(); e != nil {
		return errors.New("Mihomo 配置校验未通过")
	}
	return nil
}

func (h *helper) startCore(confirmation bool) error {
	if len(h.state.Proxies) == 0 {
		return errors.New("请先导入可用配置")
	}
	if e := h.preflight(); e != nil {
		return e
	}
	ips, e := app.ResolveEndpoints(h.state)
	if e != nil {
		return e
	}
	b, e := app.EffectiveConfig(h.state, h.secret, ips)
	if e != nil {
		return e
	}
	path := filepath.Join(h.etc, "active.yaml")
	if e = atomicWrite(path, b, 0600); e != nil {
		return errors.New("无法保存配置")
	}
	if e = h.testConfig(path); e != nil {
		return e
	}
	if e = h.launchCore(path); e != nil {
		return e
	}
	for group, node := range h.state.Selected {
		payload := []byte(fmt.Sprintf(`{"name":%q}`, node))
		if e := h.coreRequest("PUT", "/proxies/"+url.PathEscape(group), payload); e != nil {
			h.stopCore()
			return errors.New("已保存节点未能生效")
		}
	}
	if confirmation {
		h.pending = true
		h.timer = time.AfterFunc(30*time.Second, func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if h.pending {
				h.stopCore()
				h.state.Enabled = false
				h.state.LastError = "管理页面未确认连通，TUN 已自动恢复"
				_ = h.save()
				h.logEvent("确认超时，网络已恢复")
			}
		})
	}
	return nil
}

func (h *helper) launchCore(path string) error {
	if e := h.preflight(); e != nil {
		return e
	}
	if e := h.snapshotNetwork(); e != nil {
		return errors.New("无法保存启用前网络快照")
	}
	if e := atomicWrite(filepath.Join(h.varDir, "owned.marker"), []byte("fnvpn0 19091\n"), 0600); e != nil {
		return errors.New("无法记录恢复标记")
	}
	cmd := exec.Command(h.binary, "-f", path, "-d", h.varDir)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=" + h.varDir}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
	if e := cmd.Start(); e != nil {
		h.cleanup()
		return errors.New("Mihomo 启动失败")
	}
	h.core = cmd
	done := make(chan struct{})
	h.done = done
	_ = atomicWrite(filepath.Join(h.varDir, "core.pid"), []byte(strconv.Itoa(cmd.Process.Pid)), 0600)
	go func() {
		_ = cmd.Wait()
		close(done)
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.core == cmd {
			h.core = nil
			h.pending = false
			h.cleanup()
			h.state.Enabled = false
			h.state.LastError = "Mihomo 意外退出，网络已尝试恢复"
			_ = h.save()
			h.logEvent("内核异常退出，已执行恢复")
		}
	}()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		if h.coreHealth() == nil && fileExists("/sys/class/net/fnvpn0") {
			h.logEvent("Mihomo TUN 已启动")
			return nil
		}
		select {
		case <-done:
			h.core = nil
			h.cleanup()
			return errors.New("Mihomo 启动后异常退出")
		default:
		}
		time.Sleep(300 * time.Millisecond)
	}
	h.stopCore()
	return errors.New("Mihomo TUN 健康检查超时")
}

func (h *helper) snapshotNetwork() error {
	snapshot := map[string]string{"at": time.Now().Format(time.RFC3339)}
	for _, item := range []struct {
		name string
		args []string
	}{{"ipv4_routes", []string{"-4", "route", "show", "table", "all"}}, {"ipv6_routes", []string{"-6", "route", "show", "table", "all"}}, {"ipv4_rules", []string{"-4", "rule", "show"}}, {"ipv6_rules", []string{"-6", "rule", "show"}}} {
		s, e := command(3*time.Second, "ip", item.args...)
		if e != nil {
			return e
		}
		snapshot[item.name] = s
	}
	b, e := os.ReadFile("/etc/resolv.conf")
	if e != nil {
		return e
	}
	snapshot["resolv_conf"] = string(b)
	data, e := json.Marshal(snapshot)
	if e != nil {
		return e
	}
	return atomicWrite(filepath.Join(h.varDir, "network-before.json"), data, 0600)
}

func (h *helper) coreHealth() error { return h.coreRequest("GET", "/version", nil) }
func (h *helper) coreRequest(method, path string, body []byte) error {
	client := &http.Client{Timeout: 2 * time.Second}
	req, e := http.NewRequest(method, "http://127.0.0.1:19091"+path, bytes.NewReader(body))
	if e != nil {
		return errors.New("控制请求无效")
	}
	req.Header.Set("Authorization", "Bearer "+h.secret)
	req.Header.Set("Content-Type", "application/json")
	resp, e := client.Do(req)
	if e != nil {
		return errors.New("Mihomo 控制接口不可达")
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("Mihomo 拒绝配置变更")
	}
	return nil
}

func (h *helper) stopCore() {
	if h.timer != nil {
		h.timer.Stop()
	}
	h.pending = false
	if h.core != nil {
		cmd := h.core
		h.core = nil
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-h.done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			select {
			case <-h.done:
			case <-time.After(time.Second):
			}
		}
	}
	h.cleanup()
}

func (h *helper) cleanup() {
	marker := filepath.Join(h.varDir, "owned.marker")
	if !fileExists(marker) {
		return
	}
	for _, fam := range []string{"-4", "-6"} {
		for i := 0; i < 10; i++ {
			rules, _ := command(2*time.Second, "ip", fam, "rule", "show")
			priority := ""
			for _, line := range strings.Split(rules, "\n") {
				if strings.Contains(line, "lookup 19091") {
					m := regexp.MustCompile(`^\s*([0-9]+):`).FindStringSubmatch(line)
					if len(m) == 2 {
						priority = m[1]
						break
					}
				}
			}
			if priority == "" {
				break
			}
			_, _ = command(2*time.Second, "ip", fam, "rule", "del", "priority", priority)
		}
		_, _ = command(2*time.Second, "ip", fam, "route", "flush", "table", "19091")
	}
	if fileExists("/sys/class/net/fnvpn0") {
		_, _ = command(2*time.Second, "ip", "link", "delete", "fnvpn0")
	}
	_ = os.Remove(filepath.Join(h.varDir, "core.pid"))
	_ = os.Remove(marker)
}

func (h *helper) recover() error {
	marker := filepath.Join(h.varDir, "owned.marker")
	if !fileExists(marker) {
		return nil
	}
	b, _ := os.ReadFile(filepath.Join(h.varDir, "core.pid"))
	pid, e := strconv.Atoi(strings.TrimSpace(string(b)))
	if e == nil && pid > 1 {
		exe, e := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
		realBinary, resolveErr := filepath.EvalSymlinks(h.binary)
		if resolveErr != nil {
			realBinary = h.binary
		}
		if e == nil && strings.TrimSuffix(exe, " (deleted)") == realBinary {
			_ = syscall.Kill(pid, syscall.SIGTERM)
			time.Sleep(time.Second)
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
	h.cleanup()
	h.logEvent("发现残留标记，已执行恢复")
	return nil
}

func verifyExit(running bool) map[string]any {
	result := map[string]any{"running": running, "checked_at": time.Now().Format(time.RFC3339)}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 8 * time.Second}
	resp, e := client.Get("https://api.ipify.org?format=json")
	if e != nil {
		result["error"] = "宿主机 HTTPS 出口检测失败"
		return result
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		result["error"] = "出口检测服务返回错误"
		return result
	}
	var data struct {
		IP string `json:"ip"`
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 1024)).Decode(&data); e != nil || net.ParseIP(data.IP) == nil {
		result["error"] = "出口检测响应无效"
		return result
	}
	result["ip"] = data.IP
	return result
}

func recoverStandalone() error {
	dest, e := envPath("TRIM_APPDEST")
	if e != nil {
		return e
	}
	v, e := envPath("TRIM_PKGVAR")
	if e != nil {
		return e
	}
	h := &helper{dest: dest, varDir: v, binary: filepath.Join(dest, "bin", "mihomo")}
	return h.recover()
}
