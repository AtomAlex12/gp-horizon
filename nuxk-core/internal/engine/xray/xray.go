// Package xray adapts the nuxk-xray engine (VLESS-Reality tunnel, vendored xray-core) to the
// engine.Engine contract by shelling to its S52xray init script.
//
// S52xray speaks the same flat "key value" info/probe contract as S51usque/S51nfqws2 (see
// engine.Exec's doc comment), plus a set-config action that takes VLESS_URI= or SUB_URL= on
// stdin — see engines/nuxk-xray/README.md's config surface and engine.Configurable.
package xray

import (
	"context"
	"strconv"
	"strings"

	"nuxk.dev/horizon/core/internal/engine"
)

// runner is the slice of engine.Exec the adapter needs — an interface so tests can inject a
// fake without a shell.
type runner interface {
	KV(ctx context.Context, sub string) (map[string]string, []string, error)
	Action(ctx context.Context, action string) error
	ActionWithInput(ctx context.Context, action, input string) (string, error)
}

type Adapter struct {
	x runner
}

// New returns an xray adapter driving the given S52xray script path.
func New(script string) *Adapter {
	return &Adapter{x: engine.Exec{Script: script}}
}

func (a *Adapter) Kind() engine.Kind { return engine.KindXray }

func (a *Adapter) Start(ctx context.Context) error   { return a.x.Action(ctx, "start") }
func (a *Adapter) Stop(ctx context.Context) error    { return a.x.Action(ctx, "stop") }
func (a *Adapter) Restart(ctx context.Context) error { return a.x.Action(ctx, "restart") }

func (a *Adapter) Info(ctx context.Context) (engine.Info, error) {
	kv, routes, err := a.x.KV(ctx, "info")
	if err != nil {
		return engine.Info{}, err
	}

	running := kv["service.running"] == "1"

	info := engine.Info{
		Kind:      engine.KindXray,
		Running:   running,
		PID:       atoi(kv["service.pid"]),
		UptimeSec: atoi64(kv["service.uptime_s"]),
		Version:   kv["version.xray"],
		Health:    healthFromState(running, kv["tunnel.state"]),
		Iface:     kv["iface.name"],
		Endpoint:  kv["config.server"],
		Routes:    len(routes),
		Detail: map[string]string{
			"tunnel_state": kv["tunnel.state"],
			"sni":          kv["config.sni"],
			"fingerprint":  kv["config.fingerprint"],
			"flow":         kv["config.flow"],
			"rx_bytes":     kv["traffic.rx_bytes"],
			"tx_bytes":     kv["traffic.tx_bytes"],
			"pkg_version":  kv["version.pkg"],
		},
	}
	if n := atoi(kv["routes.count"]); n > info.Routes {
		info.Routes = n // routes.count is authoritative; the route lines may be capped
	}
	return info, nil
}

func (a *Adapter) Probe(ctx context.Context, _ []string) (engine.Probe, error) {
	kv, _, err := a.x.KV(ctx, "probe")
	if err != nil {
		return engine.Probe{}, err
	}
	p := engine.Probe{
		OK:       kv["ok"] == "1",
		EgressIP: kv["egress_ip"],
		RTTms:    atof(kv["rtt_ms"]),
		Reason:   kv["reason"],
		TS:       atoi64(kv["ts"]),
		Detail: map[string]string{
			"server": kv["server"],
		},
	}
	return p, nil
}

// SetConfig writes the engine's server target — exactly one of a raw vless:// URI or a 3x-ui
// subscription URL — and asks S52xray to regenerate config.json and restart. Unknown keys in
// cfg are ignored; the init script is the source of truth for validation (e.g. rejecting both
// or neither being set).
func (a *Adapter) SetConfig(ctx context.Context, cfg map[string]string) error {
	var b strings.Builder
	if v := cfg["vless_uri"]; v != "" {
		b.WriteString("VLESS_URI=" + v + "\n")
	}
	if v := cfg["sub_url"]; v != "" {
		b.WriteString("SUB_URL=" + v + "\n")
	}
	_, err := a.x.ActionWithInput(ctx, "set-config", b.String())
	return err
}

// ApplyRouting is a no-op for xray until nuxk-plane owns the fwmark rule (MVP-3 §C) — same
// pattern usque.go already uses. Only recorded for probing/reporting context.
func (a *Adapter) ApplyRouting(ctx context.Context, r engine.Routing) error {
	_ = ctx
	_ = r
	return nil
}

func healthFromState(running bool, tunnel string) engine.Health {
	if !running {
		return engine.HealthDown
	}
	switch tunnel {
	case "connected":
		return engine.HealthOK
	case "disconnected":
		return engine.HealthDegraded
	default:
		return engine.HealthUnknown
	}
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
func atoi64(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}
func atof(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}
