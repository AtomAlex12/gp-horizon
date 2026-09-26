// Package nfqws2 adapts the nuxk-nfqws2 engine (NFQUEUE-based DPI desync) to
// the engine.Engine contract by shelling to its S51nfqws2 init script.
//
// nfqws2 does not reroute traffic — it's a passive NFQUEUE hook on WAN egress
// that mangles packets in flight (see engine.KindNfqws2's doc comment). So
// unlike usque/xray, Info has no meaningful Endpoint, and Probe has no
// meaningful EgressIP: the probe instead answers "do sites of the DPI list
// load through the router's real egress", site by site (see the probe
// subcommand of engines/nuxk-nfqws2/S51nfqws2-nuxk).
package nfqws2

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"nuxk.dev/horizon/core/internal/engine"
)

// runner is the slice of engine.Exec the adapter needs — an interface so
// tests can inject a fake without a shell.
type runner interface {
	KV(ctx context.Context, sub string) (map[string]string, []string, error)
	KVWithInput(ctx context.Context, sub, input string) (map[string]string, []string, error)
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
		Version:   kv["version.pkg"],
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

// Probe opens each target over HTTPS from the router — the shim runs them in
// parallel, 8 s each — and reports where each broke. OK = at least one
// opened: nfqws2 does its job; a site it doesn't open is the strategy's or
// the list's business, named in Checks. No targets = the shim's canary.
func (a *Adapter) Probe(ctx context.Context, targets []string) (engine.Probe, error) {
	kv, lines, err := a.x.KVWithInput(ctx, "probe", strings.Join(targets, "\n"))
	if err != nil {
		return engine.Probe{}, err
	}
	p := engine.Probe{TS: atoi64(kv["ts"])}
	for _, l := range lines {
		f := strings.Fields(l) // check <domain> <ok> <ms> [reason]
		if len(f) < 3 {
			continue
		}
		c := engine.ProbeCheck{Domain: f[0], OK: f[1] == "1", RTTms: atof(f[2])}
		if !c.OK && len(f) > 3 {
			c.Reason = f[3]
		}
		p.Checks = append(p.Checks, c)
	}
	if len(p.Checks) == 0 { // a shim from before per-site checks
		p.OK, p.RTTms, p.Reason = kv["ok"] == "1", atof(kv["rtt_ms"]), kv["reason"]
		p.Detail = map[string]string{"target": kv["target"]}
		return p, nil
	}
	// the order asked, not the order the parallel checks finished in
	pos := map[string]int{}
	for i, d := range targets {
		pos[d] = i + 1
	}
	slices.SortStableFunc(p.Checks, func(x, y engine.ProbeCheck) int {
		px, py := pos[x.Domain], pos[y.Domain]
		if px == 0 {
			px = len(targets) + 1
		}
		if py == 0 {
			py = len(targets) + 1
		}
		return px - py
	})
	opened, reasons := 0, map[string]int{}
	for _, c := range p.Checks {
		if !c.OK {
			reasons[c.Reason]++
			continue
		}
		opened++
		if p.RTTms == 0 || c.RTTms < p.RTTms {
			p.RTTms = c.RTTms
		}
	}
	p.OK = opened > 0
	if !p.OK { // the commonest failure; a tie goes to the first site's
		for _, c := range p.Checks {
			if p.Reason == "" || reasons[c.Reason] > reasons[p.Reason] {
				p.Reason = c.Reason
			}
		}
	}
	p.Detail = map[string]string{"opened": strconv.Itoa(opened), "total": strconv.Itoa(len(p.Checks))}
	return p, nil
}

// ApplyStrategies hands the shim nuxk's per-domain profiles: a hostlist
// each (nuxk-sN.list), then the NFQWS_ARGS_CUSTOM value; an empty set
// clears them. The shim restarts nfqws2 and rolls back if it won't start.
// Strategies must already be validated (engine.ValidateStrategy).
func (a *Adapter) ApplyStrategies(ctx context.Context, ss []engine.Strategy) error {
	var b strings.Builder
	for i, s := range ss {
		fmt.Fprintf(&b, "list nuxk-s%d.list\n%s\n.\n", i+1, strings.Join(s.Domains, "\n"))
	}
	b.WriteString("custom " + engine.RenderStrategies(ss, listDir) + "\n")
	_, err := a.x.ActionWithInput(ctx, "apply-strategies", b.String())
	return err
}

// listDir is where nfqws2-keenetic keeps its lists on the router.
const listDir = "/opt/etc/nfqws2/lists"

// ApplyRouting writes Domains to the desync hostlist and Endpoints (other
// engines' tunnel upstream IPs — "WARP через nfqws" endpoint hardening) to
// the endpoints list. A nil slice leaves that list untouched (the controller
// pushes endpoints alone when a tunnel's upstream changes); an empty non-nil
// slice clears it. CIDRs/Strategy are intentionally unhandled in this pass —
// --ipset wiring is deferred (see deploy/proto plan §6/§8).
func (a *Adapter) ApplyRouting(ctx context.Context, r engine.Routing) error {
	if r.Domains != nil {
		if _, err := a.x.ActionWithInput(ctx, "apply-desync", strings.Join(r.Domains, "\n")); err != nil {
			return err
		}
	}
	if r.Endpoints != nil {
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
