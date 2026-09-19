// Package nfqws2 adapts the nuxk-nfqws2 engine (NFQUEUE-based DPI desync) to
// the engine.Engine contract by shelling to its S51nfqws2 init script.
//
// nfqws2 does not reroute traffic — it's a passive NFQUEUE hook on WAN egress
// that mangles packets in flight (see engine.KindNfqws2's doc comment). So
// unlike usque/xray, Info has no meaningful Endpoint, and Probe has no
// meaningful EgressIP: the probe instead answers "does a known-blocked domain
// load through this container's real egress" (see S51nfqws2-docker's probe
// subcommand under deploy/proto/engine/).
package nfqws2

import (
	"context"
	"strconv"
	"strings"

	"nuxk.dev/horizon/core/internal/engine"
)

// runner is the slice of engine.Exec the adapter needs — an interface so
// tests can inject a fake without a shell.
type runner interface {
	KV(ctx context.Context, sub string) (map[string]string, []string, error)
	Action(ctx context.Context, action string) error
	ActionWithInput(ctx context.Context, action, input string) (string, error)
}

type Adapter struct {
	x runner
}

// New returns an nfqws2 adapter driving the given S51nfqws2 script path.
func New(script string) *Adapter {
	return &Adapter{x: engine.Exec{Script: script}}
}

func (a *Adapter) Kind() engine.Kind { return engine.KindNfqws2 }

func (a *Adapter) Start(ctx context.Context) error   { return a.x.Action(ctx, "start") }
func (a *Adapter) Stop(ctx context.Context) error    { return a.x.Action(ctx, "stop") }
func (a *Adapter) Restart(ctx context.Context) error { return a.x.Action(ctx, "restart") }

func (a *Adapter) Info(ctx context.Context) (engine.Info, error) {
	kv, items, err := a.x.KV(ctx, "info")
	if err != nil {
		return engine.Info{}, err
	}

	running := kv["service.running"] == "1"
	fwInstalled := kv["firewall.installed"] == "1"

	desync := atoi(kv["lists.desync_count"])
	endpoints := atoi(kv["lists.endpoints_count"])

	info := engine.Info{
		Kind:      engine.KindNfqws2,
		Running:   running,
		PID:       atoi(kv["service.pid"]),
		UptimeSec: atoi64(kv["service.uptime_s"]),
		Health:    healthFromState(running, fwInstalled),
		Iface:     kv["config.iface"], // the WAN-egress interface it hooks, not a tunnel
		Routes:    desync + endpoints,
		Detail: map[string]string{
			"nfqueue_num":     kv["config.nfqueue_num"],
			"firewall":        kv["firewall.installed"],
			"desync_count":    kv["lists.desync_count"],
			"endpoints_count": kv["lists.endpoints_count"],
			"auto_count":      kv["lists.auto_count"],
			"items":           strings.Join(items, ","),
		},
	}
	return info, nil
}

func (a *Adapter) Probe(ctx context.Context) (engine.Probe, error) {
	kv, _, err := a.x.KV(ctx, "probe")
	if err != nil {
		return engine.Probe{}, err
	}
	p := engine.Probe{
		OK:     kv["ok"] == "1",
		RTTms:  atof(kv["rtt_ms"]),
		Reason: kv["reason"],
		TS:     atoi64(kv["ts"]),
		Detail: map[string]string{
			"target": kv["target"],
		},
	}
	return p, nil
}

// ApplyRouting writes Domains to the desync hostlist and Endpoints (other
// engines' tunnel upstream IPs — "WARP через nfqws" endpoint hardening) to
// the endpoints list. CIDRs/Strategy are intentionally unhandled in this
// pass — --ipset wiring is deferred (see deploy/proto plan §6/§8).
func (a *Adapter) ApplyRouting(ctx context.Context, r engine.Routing) error {
	if _, err := a.x.ActionWithInput(ctx, "apply-desync", strings.Join(r.Domains, "\n")); err != nil {
		return err
	}
	if len(r.Endpoints) > 0 {
		if _, err := a.x.ActionWithInput(ctx, "apply-endpoints", strings.Join(r.Endpoints, "\n")); err != nil {
			return err
		}
	}
	// TODO: r.CIDRs / r.Strategy — --ipset filter group, deferred.
	return nil
}

func healthFromState(running, firewallInstalled bool) engine.Health {
	if !running {
		return engine.HealthDown
	}
	if !firewallInstalled {
		return engine.HealthDegraded
	}
	return engine.HealthOK
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
