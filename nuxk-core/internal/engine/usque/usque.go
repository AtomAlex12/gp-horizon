// Package usque adapts the usque-keenetic engine (Cloudflare WARP / MASQUE)
// to the engine.Engine contract by shelling to its S51usque init script.
//
// S51usque already speaks the flat "key value" convention (info / probe /
// start|stop|restart|reregister) — see engines/nuxk-usque.
package usque

import (
	"context"
	"strconv"
	"strings"

	"nuxk.dev/horizon/core/internal/engine"
)

// runner is the slice of engine.Exec the adapter needs — an interface so tests
// can inject a fake without a shell.
type runner interface {
	KV(ctx context.Context, sub string) (map[string]string, []string, error)
	Action(ctx context.Context, action string) error
}

type Adapter struct {
	x runner
}

// New returns a usque adapter driving the given S51usque script path.
func New(script string) *Adapter {
	return &Adapter{x: engine.Exec{Script: script}}
}

func (a *Adapter) Kind() engine.Kind { return engine.KindUsque }

func (a *Adapter) Start(ctx context.Context) error   { return a.x.Action(ctx, "start") }
func (a *Adapter) Stop(ctx context.Context) error    { return a.x.Action(ctx, "stop") }
func (a *Adapter) Restart(ctx context.Context) error { return a.x.Action(ctx, "restart") }

func (a *Adapter) Info(ctx context.Context) (engine.Info, error) {
	kv, routes, err := a.x.KV(ctx, "info")
	if err != nil {
		return engine.Info{}, err
	}

	running := kv["service.running"] == "1"
	tunnel := kv["tunnel.state"] // connected | disconnected | unknown | stopped

	info := engine.Info{
		Kind:      engine.KindUsque,
		Running:   running,
		PID:       atoi(kv["service.pid"]),
		UptimeSec: atoi64(kv["service.uptime_s"]),
		Version:   kv["version.usque"],
		Health:    healthFromTunnel(running, tunnel),
		Iface:     kv["iface.name"],
		Endpoint:  kv["tunnel.endpoint"],
		Routes:    len(routes),
		Detail: map[string]string{
			"tunnel_state": tunnel,
			"tunnel_since": kv["tunnel.since"],
			"iface_link":   kv["iface.link"],
			"iface_ip":     kv["iface.ip"],
			"sni":          kv["config.sni"],
			"http2":        kv["config.http2"],
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
			"warp": kv["warp"],
			"colo": kv["colo"],
			"loc":  kv["loc"],
			"sni":  kv["sni"],
		},
	}
	return p, nil
}

// ApplyRouting is a no-op for usque until nuxk-plane lands (MVP-2): the plane
// owns the fwmark rule that steers matched destinations into opkgtun0. Here we
// only record the target set so Info/probe context is available.
func (a *Adapter) ApplyRouting(ctx context.Context, r engine.Routing) error {
	// TODO(MVP-2): hand r.Domains / r.CIDRs to nuxk-plane; write a hint file for
	// operators (/opt/etc/usque/routed.list).
	_ = ctx
	_ = r
	return nil
}

func healthFromTunnel(running bool, tunnel string) engine.Health {
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
