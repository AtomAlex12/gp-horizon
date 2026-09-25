package main

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// NewServer: /api/v1/* is proxied to the agent (browser token checked, agent
// token swapped in), /ctl/v1/* is the controller's own API, / is the web UI.
func NewServer(a *Agent, uiToken, webRoot, version string) http.Handler {
	target, _ := url.Parse(a.URL)
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			r.Out.Host = target.Host
			r.Out.Header.Set("Authorization", "Bearer "+a.Token)
			r.Out.Header.Del("Cookie")
		},
		FlushInterval: -1, // SSE (/api/v1/events) passes through unbuffered
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			writeErr(w, http.StatusBadGateway, "agent_unreachable", "роутер (nuxk-core) не отвечает: "+err.Error())
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ctl/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	authed := http.NewServeMux()
	authed.HandleFunc("GET /ctl/v1/agent", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, a.State(version))
	})
	authed.HandleFunc("GET /ctl/v1/history", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, a.Hist.Series())
	})
	authed.Handle("/api/v1/", proxy)
	guard := auth(uiToken, authed)
	mux.Handle("/ctl/v1/", guard)
	mux.Handle("/api/v1/", guard)
	if webRoot != "" {
		mux.Handle("/", spa(webRoot))
	}
	return secure(mux)
}

func auth(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
		}
		next.ServeHTTP(w, r)
	})
}

// spa serves the web build, falling back to index.html for client routes.
func spa(root string) http.Handler {
	fs := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := filepath.Clean("/" + r.URL.Path)
		if st, err := os.Stat(filepath.Join(root, clean)); (err != nil || st.IsDir()) && !strings.Contains(filepath.Base(clean), ".") {
			http.ServeFile(w, r, filepath.Join(root, "index.html"))
			return
		}
		fs.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, c, msg string) {
	writeJSON(w, code, map[string]any{"error": map[string]string{"code": c, "message": msg}})
}
