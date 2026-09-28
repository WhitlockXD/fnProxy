package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateAPIWriteRequestThroughGateway(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://internal-socket/app/fnvpn/api/import-url", nil)
	req.Host = "internal-socket"
	req.Header.Set("Origin", "https://nas.example:5666")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("X-FNVPN-Request", "1")
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	if status, message := validateAPIWriteRequest(req); status != 0 {
		t.Fatalf("same-origin request rejected: %d %s", status, message)
	}

	req.Header.Set("Sec-Fetch-Site", "cross-site")
	if status, _ := validateAPIWriteRequest(req); status != http.StatusForbidden {
		t.Errorf("cross-site status=%d", status)
	}
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Del("X-FNVPN-Request")
	if status, _ := validateAPIWriteRequest(req); status != http.StatusForbidden {
		t.Errorf("missing marker status=%d", status)
	}
	req.Header.Set("X-FNVPN-Request", "1")
	req.Header.Set("Content-Type", "text/plain")
	if status, _ := validateAPIWriteRequest(req); status != http.StatusUnsupportedMediaType {
		t.Errorf("wrong content type status=%d", status)
	}
}

func TestWriteAPIError(t *testing.T) {
	w := httptest.NewRecorder()
	writeAPIError(w, http.StatusForbidden, "来源校验失败")
	if w.Code != http.StatusForbidden || w.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("status=%d content-type=%q", w.Code, w.Header().Get("Content-Type"))
	}
	var body struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.OK || body.Error != "来源校验失败" {
		t.Fatalf("body=%q err=%v", w.Body.String(), err)
	}
}
