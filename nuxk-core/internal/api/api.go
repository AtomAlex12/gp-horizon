// Package api serves nuxk-core's /api/v1 (see api/openapi.yaml for the full
// draft contract). Internal, single-tenant: bearer token for non-localhost,
// flat JSON responses, errors by HTTP status.
package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"nuxk.dev/horizon/core/internal/engine"
	"nuxk.dev/horizon/core/internal/state"
)

type Deps struct {
	Version string
	State   *state.Store
	Engines *engine.Registry
	WebRoot string // static nuxk-web build; "" = API only
	Token   string // "" = allow localhost only
}

func NewRouter(d Deps) http.Handler {
	mux := http.NewServeMux()

	// --- unauthenticated ---
	mux.HandleFunc("GET /api/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// --- v1 (authenticated) ---
	v1 := http.NewServeMux()
	v1.HandleFunc("GET /api/v1/version", d.handleVersion)
	v1.HandleFunc("GET /api/v1/status", d.handleStatus)
	v1.HandleFunc("GET /api/v1/engines", d.handleEngines)
	v1.HandleFunc("GET /api/v1/engines/{kind}", d.handleEngine)
	v1.HandleFunc("POST /api/v1/engines/{kind}/{action}", d.handleEngineAction)
	// TODO: /lists/{kind}, /decisions, /discover, /apply, /presets, /settings, /events(SSE)
	mux.Handle("/api/v1/", d.auth(v1))

	// --- static web (optional) ---
	if d.WebRoot != "" {
		fs := http.FileServer(http.Dir(d.WebRoot))
		mux.Handle("/", spaFallback(d.WebRoot, fs))
	}

	return logging(mux)
}

func (d Deps) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": d.Version, "api": "v1"})
}

func (d Deps) handleStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, map[string]any{
		"version": d.Version,
		"engines": d.Engines.Snapshot(ctx),
		"plane":   map[string]any{"status": "not-wired"}, // TODO MVP-2
		"ts":      time.Now().Unix(),
	})
}

func (d Deps) handleEngines(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, d.Engines.Snapshot(ctx))
}

func (d Deps) handleEngine(w http.ResponseWriter, r *http.Request) {
	k := engine.Kind(r.PathValue("kind"))
	e, ok := d.Engines.Get(k)
	if !ok {
		writeErr(w, http.StatusNotFound, "engine_not_found", "no such engine: "+string(k))
		return
	}
	info, err := e.Info(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, "engine_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (d Deps) handleEngineAction(w http.ResponseWriter, r *http.Request) {
	k := engine.Kind(r.PathValue("kind"))
	e, ok := d.Engines.Get(k)
	if !ok {
		writeErr(w, http.StatusNotFound, "engine_not_found", "no such engine: "+string(k))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	var err error
	switch r.PathValue("action") {
	case "start":
		err = e.Start(ctx)
	case "stop":
		err = e.Stop(ctx)
	case "restart":
		err = e.Restart(ctx)
	case "probe":
		p, perr := e.Probe(ctx)
		if perr != nil {
			writeErr(w, http.StatusBadGateway, "probe_failed", perr.Error())
			return
		}
		writeJSON(w, http.StatusOK, p)
		return
	default:
		writeErr(w, http.StatusBadRequest, "bad_action", "action must be start|stop|restart|probe")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, "engine_error", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "ok"})
}

// auth enforces the bearer token for non-loopback clients.
func (d Deps) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if d.Token == "" && isLoopback(r.RemoteAddr) {
			next.ServeHTTP(w, r)
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if d.Token != "" && got == d.Token {
			next.ServeHTTP(w, r)
			return
		}
		writeErr(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token")
	})
}

func isLoopback(remoteAddr string) bool {
	host := remoteAddr
	if i := strings.LastIndex(remoteAddr, ":"); i > 0 {
		host = remoteAddr[:i]
	}
	host = strings.Trim(host, "[]")
	return host == "127.0.0.1" || host == "::1"
}
