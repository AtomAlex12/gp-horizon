package main

import (
	"encoding/json"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// NewServer: /api/v1/* is proxied to the agent (the browser's session
// checked, the agent's token swapped in), /ctl/v1/* is the controller's own
// API (setup, login, history), / is the web UI.
func NewServer(a *Agent, st *Store, ses *Sessions, ph *PluginHost, vl *Vless, su *SelfUpdater, lg *Logs, webRoot, version string) http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			base, tok := a.Ref()
			target, _ := url.Parse(base)
			r.SetURL(target)
			r.Out.Host = target.Host
			r.Out.Header.Set("Authorization", "Bearer "+tok)
			r.Out.Header.Del("Cookie")
		},
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Del("Set-Cookie") // the agent's cookies are not the controller's
			return nil
		},
		FlushInterval: -1, // SSE (/api/v1/events) passes through unbuffered
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			writeErr(w, http.StatusBadGateway, "agent_unreachable", "роутер (nuxk-core) не отвечает: "+err.Error())
		},
	}
	h := handlers{st: st, ses: ses, ag: a, version: version}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ctl/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /ctl/v1/setup", h.setupState)
	mux.HandleFunc("POST /ctl/v1/setup/admin", h.setupAdmin)
	mux.HandleFunc("POST /ctl/v1/auth/login", h.login)
	mux.HandleFunc("POST /ctl/v1/auth/logout", h.logout)

	authed := http.NewServeMux()
	authed.HandleFunc("POST /ctl/v1/setup/agent", h.setupAgent)
	authed.HandleFunc("GET /ctl/v1/agent", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, a.State(version))
	})
	authed.HandleFunc("GET /ctl/v1/history", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, a.history().Series())
	})
	ph.routes(authed)
	vl.routes(authed)
	if su != nil {
		su.routes(authed)
	}
	if lg != nil {
		lg.routes(authed)
	}
	authed.HandleFunc("/ctl/v1/gp/{path...}", NewGPClient("", st, ph).handle)
	// the agent's own login is for its own page, not through the controller
	authed.HandleFunc("/api/v1/auth/", http.NotFound)
	authed.Handle("/api/v1/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if base, _ := a.Ref(); base == "" {
			writeErr(w, http.StatusServiceUnavailable, "agent_not_configured", "роутер ещё не подключён — пройдите настройку")
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	guard := auth(ses, authed)
	mux.Handle("/ctl/v1/", guard)
	mux.Handle("/api/v1/", guard)
	if webRoot != "" {
		mux.Handle("/", spa(webRoot))
	}
	return secure(logRequests(mux))
}

// auth lets in a logged-in browser; its writes only from this very page.
func auth(ses *Sessions, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ses.fromRequest(r) {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "login required")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
			writeErr(w, http.StatusForbidden, "cross_origin", "request from another site")
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
			limit := int64(4 << 20)
			if r.URL.Path == "/ctl/v1/gp/backups/upload" {
				limit = gpUploadMax // a GP backup archive
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}

// spa serves the web build, falling back to index.html for client routes.
func spa(root string) http.Handler {
	fs := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// the page is asked for again each time (a new version must reach
		// the browser); the hashed files under /assets/ may be kept
		if !strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "no-cache")
		}
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
