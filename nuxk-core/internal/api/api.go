// Package api serves nuxk-core's /api/v1 (see api/openapi.yaml for the full
// draft contract). Internal, single-tenant: bearer token for non-localhost,
// flat JSON responses, errors by HTTP status.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"nuxk.dev/horizon/core/internal/core"
	"nuxk.dev/horizon/core/internal/engine"
	"nuxk.dev/horizon/core/internal/plane"
)

type Deps struct {
	Version string
	Commit  string
	Engines *engine.Registry
	Hub     *core.Hub
	Ctl     *core.Controller // every state change goes through it — see core.Controller
	Plane   *plane.Manager   // nil when PLANE is off
	WebRoot string           // static nuxk-web build; "" = API only
	Token   string           // "" = allow localhost only
}

func NewRouter(d Deps) http.Handler {
	mux := http.NewServeMux()

	// --- unauthenticated liveness ---
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
	v1.HandleFunc("PUT /api/v1/engines/{kind}/config", d.handleEngineConfig)
	v1.HandleFunc("GET /api/v1/plane", d.handlePlane)
	v1.HandleFunc("GET /api/v1/plane/lists", d.handlePlaneLists)
	v1.HandleFunc("PUT /api/v1/plane/lists", d.handlePlaneSetLists)
	v1.HandleFunc("POST /api/v1/plane/import", d.handlePlaneImport)
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
	writeJSON(w, http.StatusOK, map[string]string{"version": d.Version, "commit": d.Commit, "api": "v1"})
}

// handleStatus is served from the Hub — the reconcile loop keeps it fresh, so
// this never blocks on an engine.
func (d Deps) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, d.Hub.Get())
}

func (d Deps) handleEngines(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, d.Hub.Get().Engines)
}

// handleEngine goes live (not the Hub) — a single-engine GET is a deliberate
// "give me the current truth" call.
func (d Deps) handleEngine(w http.ResponseWriter, r *http.Request) {
	k := engine.Kind(r.PathValue("kind"))
	e, ok := d.Engines.Get(k)
	if !ok {
		writeErr(w, http.StatusNotFound, "engine_not_found", "no such engine: "+string(k))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	info, err := e.Info(ctx)
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
	switch action := r.PathValue("action"); action {
	case "start", "stop", "restart":
		err = d.Ctl.Action(ctx, k, action)
	case "probe":
		p, perr := e.Probe(ctx)
		if perr != nil {
			writeErr(w, http.StatusBadGateway, "probe_failed", perr.Error())
			return
		}
		writeJSON(w, http.StatusOK, p)
		return
	case "apply":
		var routing engine.Routing
		if r.Body != nil {
			defer r.Body.Close()
			if derr := json.NewDecoder(r.Body).Decode(&routing); derr != nil && derr != io.EOF {
				writeErr(w, http.StatusBadRequest, "bad_body", "invalid routing JSON: "+derr.Error())
				return
			}
		}
		err = d.Ctl.Apply(ctx, k, routing)
	default:
		writeErr(w, http.StatusBadRequest, "bad_action", "action must be start|stop|restart|probe|apply")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, "engine_error", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "ok"})
}

// handleEngineConfig sets an engine's runtime target (e.g. xray's VLESS URI / subscription).
// Only engines implementing engine.Configurable accept this; others get 404, same as an
// unknown kind — a caller can't tell "no such engine" from "exists, not configurable" by
// design, since neither should be probed for from outside. The config is stored (0600) for
// re-apply and never served back.
func (d Deps) handleEngineConfig(w http.ResponseWriter, r *http.Request) {
	k := engine.Kind(r.PathValue("kind"))
	var cfg map[string]string
	if r.Body != nil {
		defer r.Body.Close()
		if derr := json.NewDecoder(r.Body).Decode(&cfg); derr != nil && derr != io.EOF {
			writeErr(w, http.StatusBadRequest, "bad_body", "invalid config JSON: "+derr.Error())
			return
		}
	}
	if len(cfg) == 0 {
		writeErr(w, http.StatusBadRequest, "bad_body", "config must be a non-empty JSON object")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	switch err := d.Ctl.SetConfig(ctx, k, cfg); {
	case errors.Is(err, core.ErrEngineNotFound):
		writeErr(w, http.StatusNotFound, "engine_not_found", "no such engine: "+string(k))
	case errors.Is(err, core.ErrNotConfigurable):
		writeErr(w, http.StatusNotFound, "not_configurable", "engine does not accept runtime config: "+string(k))
	case err != nil:
		writeErr(w, http.StatusBadGateway, "engine_error", err.Error())
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// auth enforces the bearer token for non-loopback clients.
func (d Deps) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if d.Token == "" && isLoopback(r.RemoteAddr) {
			next.ServeHTTP(w, r)
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if d.Token != "" && subtle.ConstantTimeCompare([]byte(got), []byte(d.Token)) == 1 {
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

// --- routing plane --------------------------------------------------------------

func (d Deps) planeOff(w http.ResponseWriter) bool {
	if d.Plane == nil {
		writeErr(w, http.StatusNotFound, "plane_off", "routing plane is off (PLANE= in nuxk.conf)")
		return true
	}
	return false
}

// handlePlane: backend, apply mode, wanted groups, pending plan, conflicts
// (domains still in the user's own lists), importable user groups.
func (d Deps) handlePlane(w http.ResponseWriter, r *http.Request) {
	if d.planeOff(w) {
		return
	}
	writeJSON(w, http.StatusOK, d.Plane.Status())
}

func (d Deps) handlePlaneLists(w http.ResponseWriter, r *http.Request) {
	if d.planeOff(w) {
		return
	}
	des, err := d.Plane.Desired()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "state_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, des)
}

func (d Deps) handlePlaneSetLists(w http.ResponseWriter, r *http.Request) {
	if d.planeOff(w) {
		return
	}
	var des plane.Desired
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&des); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_body", "invalid lists JSON: "+err.Error())
		return
	}
	if err := d.Plane.SetDesired(des); err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, plane.ErrBadList) || errors.Is(err, plane.ErrBadOnDown) {
			code = http.StatusBadRequest
		}
		writeErr(w, code, "bad_list", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handlePlaneImport copies the user's routed groups into nuxk lists. The
// user's groups stay as they are; their domains are held back as conflicts
// until removed from the old lists.
func (d Deps) handlePlaneImport(w http.ResponseWriter, r *http.Request) {
	if d.planeOff(w) {
		return
	}
	var req struct {
		Groups []string   `json:"groups"`
		Mode   plane.Mode `json:"mode"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil || len(req.Groups) == 0 {
		writeErr(w, http.StatusBadRequest, "bad_body", `want {"groups":["domain-list0"],"mode":"vless"}`)
		return
	}
	des, err := d.Plane.Import(req.Groups, req.Mode)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_import", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, des)
}
