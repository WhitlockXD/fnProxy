//go:build linux

package main

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type peerListener struct {
	net.Listener
	uid uint32
}

func (l peerListener) Accept() (net.Conn, error) {
	for {
		c, e := l.Listener.Accept()
		if e != nil {
			return nil, e
		}
		u, e := peerUID(c.(*net.UnixConn))
		if e == nil && u == l.uid {
			return c, nil
		}
		c.Close()
	}
}

func helperCall(r request) (response, error) {
	path := os.Getenv("FNVPN_HELPER_SOCKET")
	if path == "" {
		dest, e := envPath("TRIM_APPDEST")
		if e != nil {
			return response{}, e
		}
		path = filepath.Join(dest, "helper.sock")
	}
	c, e := net.DialTimeout("unix", path, 2*time.Second)
	if e != nil {
		return response{}, e
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(90 * time.Second))
	if e = json.NewEncoder(c).Encode(r); e != nil {
		return response{}, e
	}
	var out response
	e = json.NewDecoder(io.LimitReader(c, 4<<20)).Decode(&out)
	return out, e
}

func runCtl() error {
	if len(os.Args) < 3 {
		return errors.New("missing operation")
	}
	out, e := helperCall(request{Op: os.Args[2]})
	if e != nil {
		return e
	}
	if !out.OK {
		return errors.New(out.Error)
	}
	return nil
}

func runWeb() error {
	f := os.NewFile(3, "app.sock")
	if f == nil {
		return errors.New("missing gateway socket")
	}
	l, e := net.FileListener(f)
	f.Close()
	if e != nil {
		return e
	}
	allowed := uint32(0)
	if raw := os.Getenv("FNVPN_GATEWAY_UID"); raw != "" {
		n, e := strconv.ParseUint(raw, 10, 32)
		if e != nil {
			return e
		}
		allowed = uint32(n)
	}
	dest, e := envPath("TRIM_APPDEST")
	if e != nil {
		return e
	}
	static := filepath.Join(dest, "www")
	mux := http.NewServeMux()
	prefix := "/app/fnvpn"
	mux.HandleFunc(prefix+"/api/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Header.Get("X-Trim-Isadmin") != "true" || r.Header.Get("X-Trim-Userid") == "" {
			writeAPIError(w, http.StatusForbidden, "仅管理员可访问")
			return
		}
		path := strings.TrimPrefix(r.URL.Path, prefix+"/api/")
		op := map[string]string{"status": "status", "diagnostics": "diagnostics", "connectivity": "connectivity", "import-file": "import-file", "import-url": "import-url", "update": "update", "start": "start", "confirm": "confirm", "stop": "stop", "recover": "recover", "select": "select", "mode": "mode", "verify": "verify"}[path]
		if op == "" {
			writeAPIError(w, http.StatusNotFound, "接口不存在")
			return
		}
		if (op == "status" || op == "diagnostics" || op == "connectivity" || op == "verify") != (r.Method == http.MethodGet) {
			writeAPIError(w, http.StatusMethodNotAllowed, "方法不允许")
			return
		}
		var body request
		body.Op = op
		if r.Method == http.MethodPost {
			if status, message := validateAPIWriteRequest(r); status != 0 {
				writeAPIError(w, status, message)
				return
			}
			if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 3<<20)).Decode(&body); e != nil {
				writeAPIError(w, http.StatusBadRequest, "请求内容无效")
				return
			}
			body.Op = op
		}
		out, e := helperCall(body)
		if e != nil {
			writeAPIError(w, http.StatusServiceUnavailable, "本地管理服务不可用")
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if !out.OK {
			w.WriteHeader(http.StatusBadRequest)
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	registerStatic(mux, prefix, static)
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 90 * time.Second, WriteTimeout: 90 * time.Second, MaxHeaderBytes: 16 << 10}
	return server.Serve(peerListener{Listener: l, uid: allowed})
}
