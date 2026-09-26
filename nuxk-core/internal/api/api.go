// Package api serves nuxk-core's /api/v1 (see api/openapi.yaml for the full
// draft contract). Internal, single-tenant: a browser logs in with the box's
// root account (session cookie), a program uses the bearer token; flat JSON
// responses, errors by HTTP status.
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

	"nuxk.dev/horizon/core/internal/auth"
	"nuxk.dev/horizon/core/internal/core"
	"nuxk.dev/horizon/core/internal/engine"
	"nuxk.dev/horizon/core/internal/logbuf"
	"nuxk.dev/horizon/core/internal/node"
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
	Token   string           // bearer token for programs (the controller); "" = none
	Auth    *auth.Guard      // browser login with the box's root account; nil = off
	Node    *node.Node       // /info, /metrics
	Logs    *logbuf.Ring     // /logs, log events on /events
}

// Route is one API endpoint. Routes is the single list the mux is built from
// and api/openapi.yaml is checked against (TestOpenAPIMatchesRoutes).
type Route struct {
	Pattern string // "METHOD /path"
	Public  bool   // no token or session: liveness, and the login itself
	handler func(Deps) http.HandlerFunc
}

var Routes = []Route{
	{"GET /api/v1/healthz", true, func(Deps) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		}
	}},
	{"POST /api/v1/auth/login", true, func(d Deps) http.HandlerFunc { return d.handleLogin }},
	{"POST /api/v1/auth/logout", true, func(d Deps) http.HandlerFunc { return d.handleLogout }},
	{"POST /api/v1/auth/pair", true, func(d Deps) http.HandlerFunc { return d.handlePair }},
	{"GET /api/v1/version", false, func(d Deps) http.HandlerFunc { return d.handleVersion }},
	{"GET /api/v1/info", false, func(d Deps) http.HandlerFunc { return d.handleInfo }},
	{"GET /api/v1/status", false, func(d Deps) http.HandlerFunc { return d.handleStatus }},
	{"GET /api/v1/metrics", false, func(d Deps) http.HandlerFunc { return d.handleMetrics }},
	{"GET /api/v1/logs", false, func(d Deps) http.HandlerFunc { return d.handleLogs }},
	{"GET /api/v1/events", false, func(d Deps) http.HandlerFunc { return d.handleEvents }},
	{"GET /api/v1/engines", false, func(d Deps) http.HandlerFunc { return d.handleEngines }},
	{"GET /api/v1/engines/{kind}", false, func(d Deps) http.HandlerFunc { return d.handleEngine }},
	{"POST /api/v1/engines/{kind}/{action}", false, func(d Deps) http.HandlerFunc { return d.handleEngineAction }},
	{"PUT /api/v1/engines/{kind}/config", false, func(d Deps) http.HandlerFunc { return d.handleEngineConfig }},
	{"GET /api/v1/engines/{kind}/strategies", false, func(d Deps) http.HandlerFunc { return d.handleStrategies }},
	{"PUT /api/v1/engines/{kind}/strategies", false, func(d Deps) http.HandlerFunc { return d.handleSetStrategies }},
	{"GET /api/v1/plane", false, func(d Deps) http.HandlerFunc { return d.handlePlane }},
	{"GET /api/v1/plane/lists", false, func(d Deps) http.HandlerFunc { return d.handlePlaneLists }},
	{"PUT /api/v1/plane/lists", false, func(d Deps) http.HandlerFunc { return d.handlePlaneSetLists }},
	{"POST /api/v1/plane/import", false, func(d Deps) http.HandlerFunc { return d.handlePlaneImport }},
}

// maxBody bounds every request body (the largest is a full set of lists).
const maxBody = 4 << 20

func NewRouter(d Deps) http.Handler {
	mux := http.NewServeMux()
	v1 := http.NewServeMux()
	for _, rt := range Routes {
		if rt.Public {
			mux.HandleFunc(rt.Pattern, rt.handler(d))
		} else {
			v1.HandleFunc(rt.Pattern, rt.handler(d))
		}
	}
	mux.Handle("/api/v1/", d.auth(v1))

	// --- static web (optional) ---
	if d.WebRoot != "" {
		fs := http.FileServer(http.Dir(d.WebRoot))
		mux.Handle("/", spaFallback(d.WebRoot, fs))
	}

	return logging(secure(mux))
}

// secure bounds request bodies and sets the headers a router admin page needs:
// no framing (clickjacking), no MIME sniffing, no referrer leaking the LAN.
func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		}
		next.ServeHTTP(w, r)
	})
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

// StrategySet is GET/PUT /engines/{kind}/strategies.
type StrategySet struct {
	Strategies []engine.Strategy `json:"strategies"`
}

func (d Deps) handleStrategies(w http.ResponseWriter, r *http.Request) {
	ss, err := d.Ctl.Strategies(engine.Kind(r.PathValue("kind")))
	switch {
	case errors.Is(err, core.ErrEngineNotFound), errors.Is(err, core.ErrNoStrategies):
		writeErr(w, http.StatusNotFound, "no_strategies", "this engine takes no strategies")
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "state", err.Error())
	default:
		writeJSON(w, http.StatusOK, StrategySet{Strategies: ss})
	}
}

// handleSetStrategies replaces the engine's nuxk strategies. It restarts the
// engine; if it won't start with them, the engine's old config is restored
// and the answer is an error.
func (d Deps) handleSetStrategies(w http.ResponseWriter, r *http.Request) {
	var set StrategySet
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&set); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_body", "want {\"strategies\":[...]}")
		return
	}
	if set.Strategies == nil {
		set.Strategies = []engine.Strategy{}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	switch err := d.Ctl.SetStrategies(ctx, engine.Kind(r.PathValue("kind")), set.Strategies); {
	case errors.Is(err, core.ErrEngineNotFound), errors.Is(err, core.ErrNoStrategies):
		writeErr(w, http.StatusNotFound, "no_strategies", "this engine takes no strategies")
	case errors.Is(err, core.ErrBadStrategy):
		writeErr(w, http.StatusBadRequest, "bad_strategy", err.Error())
	case err != nil:
		writeErr(w, http.StatusBadGateway, "engine_error", err.Error())
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// auth lets in the bearer token, a logged-in browser session, or — only
// when no token is configured — a loopback caller. A session's writes must
// come from this very page (see sameOrigin).
func (d Deps) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if d.Token == "" && isLoopback(r.RemoteAddr) {
			next.ServeHTTP(w, r)
			return
		}
		got, bearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if bearer && d.Token != "" && subtle.ConstantTimeCompare([]byte(got), []byte(d.Token)) == 1 {
			next.ServeHTTP(w, r)
			return
		}
		if _, ok := d.sessionUser(r); ok {
			if r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
				writeErr(w, http.StatusForbidden, "cross_origin", "request from another site")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		writeErr(w, http.StatusUnauthorized, "unauthorized", "login required")
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
