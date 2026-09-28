package main

import (
	"encoding/json"
	"mime"
	"net/http"
	"strings"
)

func writeAPIError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}{Error: message})
}

// The fnOS gateway may rewrite Host when forwarding to the Unix socket.
// JSON plus a custom header requires a browser preflight for cross-origin
// callers; Fetch Metadata rejects cross-site requests when available.
func validateAPIWriteRequest(r *http.Request) (int, string) {
	if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
		return http.StatusForbidden, "不允许跨站写入请求"
	}
	if r.Header.Get("X-FNVPN-Request") != "1" {
		return http.StatusForbidden, "缺少请求标记"
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return http.StatusUnsupportedMediaType, "仅接受 JSON"
	}
	return 0, ""
}
