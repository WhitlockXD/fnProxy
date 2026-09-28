package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStaticRoutes(t *testing.T) {
	directory := t.TempDir()
	for name, body := range map[string]string{
		"index.html": "<html>home</html>",
		"style.css":  "body{color:red}",
		"app.js":     "window.loaded=true",
	} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	registerStatic(mux, "/app/fnvpn", directory)

	for _, tc := range []struct {
		path, body, contentType string
	}{
		{"/app/fnvpn", "home", "text/html"},
		{"/app/fnvpn/", "home", "text/html"},
		{"/app/fnvpn/style.css", "body{color:red}", "text/css"},
		{"/app/fnvpn/style.css?v=0.1.9", "body{color:red}", "text/css"},
		{"/app/fnvpn/app.js", "window.loaded=true", "text/javascript"},
		{"/app/fnvpn/app.js?v=0.1.9", "window.loaded=true", "text/javascript"},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		req.Header.Set("X-Trim-Isadmin", "true")
		req.Header.Set("X-Trim-Userid", "1")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), tc.body) || !strings.HasPrefix(w.Header().Get("Content-Type"), tc.contentType) || w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: status=%d type=%q body=%q", tc.path, w.Code, w.Header().Get("Content-Type"), w.Body.String())
		}
	}

	unauthorized := httptest.NewRecorder()
	mux.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/app/fnvpn/style.css", nil))
	if unauthorized.Code != http.StatusForbidden {
		t.Errorf("unauthorized status=%d", unauthorized.Code)
	}
	unknown := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/app/fnvpn/unknown.js", nil)
	req.Header.Set("X-Trim-Isadmin", "true")
	req.Header.Set("X-Trim-Userid", "1")
	mux.ServeHTTP(unknown, req)
	if unknown.Code != http.StatusNotFound {
		t.Errorf("unknown file status=%d", unknown.Code)
	}
}
