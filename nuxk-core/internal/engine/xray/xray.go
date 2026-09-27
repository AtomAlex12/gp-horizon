// Package xray adapts the nuxk-xray engine (a VLESS client: xray-core with a TUN inbound) to
// the engine.Engine contract by shelling to its S52xray-nuxk init script.
//
// S52xray-nuxk speaks the same flat "key value" info/probe contract as S51usque/S51nfqws2
// (see engine.Exec's doc comment), plus set-config, which takes a ready config.json. The
// vless:// link or 3x-ui subscription is parsed and rendered here, in Go (vless.go) — not in
// shell — and the script only tests, swaps and restarts; see engines/nuxk-xray/README.md.
package xray

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

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
	// Iface is the TUN device xray brings up: opkgtunN, which KeeneticOS
	// shows as OpkgTunN — the interface the plane routes the VLESS list to.
	Iface string
	HTTP  *http.Client // fetches subscriptions
}

// New returns an xray adapter driving the given S52xray-nuxk script path;
// iface is the TUN device name (opkgtun1).
func New(script, iface string) *Adapter {
	return &Adapter{x: engine.Exec{Script: script}, Iface: iface, HTTP: http.DefaultClient}
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
		Endpoint:  firstNonEmpty(kv["config.endpoint"], kv["config.server"]),
		Routes:    len(routes),
		Detail: map[string]string{
			"tunnel_state": kv["tunnel.state"],
			"server":       kv["config.server"],
			"security":     kv["config.security"],
			"network":      kv["config.network"],
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

// SetConfig takes the server — exactly one of a vless:// link (vless_uri) or
// a 3x-ui subscription URL (sub_url; "pick" names one of its servers by key,
// else the first) — renders xray's config.json and hands it to
// S52xray-nuxk, which tests it with xray itself, swaps it in and restarts;
// if xray won't start with it, the previous config comes back. The same
// config again (a subscription re-read with nothing new) restarts nothing.
// A link that can't work is engine.ErrBadConfig.
func (a *Adapter) SetConfig(ctx context.Context, cfg map[string]string) (engine.Upstream, error) {
	uri, sub, pick := strings.TrimSpace(cfg["vless_uri"]), strings.TrimSpace(cfg["sub_url"]), cfg["pick"]
	var s Server
	var up engine.Upstream
	switch {
	case uri != "" && sub != "":
		return up, bad("нужно что-то одно: ссылка vless:// или подписка")
	case uri != "":
		var err error
		if s, err = ParseVLESS(uri); err != nil {
			return up, err
		}
		up = engine.Upstream{Source: "link", Servers: []engine.UpstreamServer{s.Public()}}
	case sub != "":
		sb, err := Subscription(ctx, a.HTTP, sub)
		if err != nil {
			return up, err
		}
		i := sb.Pick(pick)
		if i < 0 {
			if pick != "" {
				up.Notice = "выбранного сервера больше нет в подписке — взят первый"
			}
			i = 0
		}
		s = sb.Servers[i]
		up.Source, up.Title, up.Usage, up.RefreshS, up.Skipped, up.Active = "subscription", sb.Title, sb.Usage, sb.RefreshS, sb.Skipped, i
		for _, x := range sb.Servers {
			up.Servers = append(up.Servers, x.Public())
		}
	default:
		return up, bad("нет ни ссылки vless://, ни подписки")
	}
	conf, err := Render(s)
	if err != nil {
		return up, err
	}
	if _, err := a.x.ActionWithInput(ctx, "set-config", meta(s, endpointOf(ctx, s), a.Iface)+"---\n"+string(conf)+"\n"); err != nil {
		return up, err
	}
	up.FetchedAt = time.Now().Unix()
	return up, nil
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// ApplyRouting is a no-op for xray: which traffic enters its TUN device is the router's
// business (the plane routes the VLESS list to OpkgTunN) — same as usque.
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
