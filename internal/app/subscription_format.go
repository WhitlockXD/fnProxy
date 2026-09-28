package app

import (
	"encoding/base64"
	"errors"
	"strings"
)

func subscriptionFormatError(data []byte) error {
	content := strings.TrimSpace(string(data))
	lower := strings.ToLower(content)
	if strings.HasPrefix(lower, "<!doctype") || strings.HasPrefix(lower, "<html") || strings.HasPrefix(lower, "<?xml") {
		return errors.New("订阅地址返回了网页，不是 Clash/Mihomo YAML；请检查服务商提供的订阅类型或登录状态")
	}
	for _, scheme := range []string{"ss://", "ssr://", "vmess://", "vless://", "trojan://", "hysteria2://", "hy2://", "tuic://"} {
		if strings.HasPrefix(lower, scheme) {
			return errors.New("订阅返回节点链接列表；请使用服务商的 Clash/Mihomo YAML 订阅地址")
		}
	}
	if len(content) > 80 && len(content) < 2<<20 {
		compact := strings.NewReplacer("\r", "", "\n", "").Replace(content)
		if decoded, err := base64.StdEncoding.DecodeString(compact); err == nil && hasNodeLink(string(decoded)) {
			return errors.New("订阅返回 Base64 节点链接；请使用服务商的 Clash/Mihomo YAML 订阅地址")
		}
	}
	return errors.New("订阅内容不是有效的 Clash/Mihomo YAML；请确认该链接返回的是 YAML 格式")
}

func hasNodeLink(content string) bool {
	lower := strings.ToLower(strings.TrimSpace(content))
	for _, scheme := range []string{"ss://", "ssr://", "vmess://", "vless://", "trojan://", "hysteria2://", "hy2://", "tuic://"} {
		if strings.HasPrefix(lower, scheme) {
			return true
		}
	}
	return false
}
