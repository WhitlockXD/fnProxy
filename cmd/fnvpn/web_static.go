package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func registerStatic(mux *http.ServeMux, prefix, directory string) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Trim-Isadmin") != "true" || r.Header.Get("X-Trim-Userid") == "" {
			http.Error(w, "仅管理员可访问", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		name := strings.TrimPrefix(r.URL.Path, prefix)
		switch name {
		case "", "/":
			name = "index.html"
		case "/app.js":
			name = "app.js"
		case "/style.css":
			name = "style.css"
		case "/icon.png":
			name = "icon.png"
		default:
			http.NotFound(w, r)
			return
		}
		b, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		switch filepath.Ext(name) {
		case ".html":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		case ".js":
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		case ".css":
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		case ".png":
			w.Header().Set("Content-Type", "image/png")
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(b)
	}
	mux.HandleFunc(prefix, handler)
	mux.HandleFunc(prefix+"/", handler)
}
