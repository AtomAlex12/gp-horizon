// Package engine defines the single contract every managed engine implements.
//
// The controller talks to engines ONLY through the Engine interface. Engines
// never talk to each other or to the web. An adapter is a thin shim over an
// Entware init script (S51nfqws2 / S51usque / S52xray) plus its list files.
//
// This contract is a stability boundary: xray can become sing-box, HydraRoute
// can become an own plane, nfqws strategies churn monthly — none of that
// changes this file or the API above it.
package engine

import "context"

// Kind is the stable engine identifier.
type Kind string

const (
	KindNfqws2 Kind = "nfqws2" // DPI desync — no reroute, packet manipulation on egress
	KindUsque  Kind = "usque"  // Cloudflare WARP / MASQUE tunnel — interface opkgtunN
	KindXray   Kind = "xray"   // VLESS-Reality proxy — interface tun-xray
)

// Engine is implemented by every adapter.
type Engine interface {
	Kind() Kind

	// Info returns machine-readable current state. Cheap; safe to poll.
	Info(ctx context.Context) (Info, error)

	// Probe runs an ACTIVE connectivity check through this engine's path
	// (real request to a canary target). Has network side effects — not cheap.
	Probe(ctx context.Context) (Probe, error)

	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Restart(ctx context.Context) error

	// ApplyRouting hands the engine the set of targets it must act on.
	//   nfqws2: Domains/CIDRs to desync, plus Endpoints — the IPs of the OTHER
	//           engines' tunnels, so their handshakes survive ТСПУ
	//           ("WARP через nfqws"). Strategy names a strategy snippet.
	//   usque / xray: Domains/CIDRs routed into this tunnel (the plane installs
	//           the fwmark rule; the engine just needs to know its own target set
	//           for probing and reporting).
	ApplyRouting(ctx context.Context, r Routing) error
}

// Info is the common state shape. Adapters fill Detail with engine-specifics.
type Info struct {
	Kind      Kind              `json:"kind"`
	Running   bool              `json:"running"`
	PID       int               `json:"pid,omitempty"`
	UptimeSec int64             `json:"uptime_sec"`
	Version   string            `json:"version,omitempty"`
	Health    Health            `json:"health"`
	Iface     string            `json:"iface,omitempty"`    // opkgtun0 / tun-xray / ""
	Endpoint  string            `json:"endpoint,omitempty"` // upstream the tunnel dials
	Routes    int               `json:"routes"`             // prefixes currently targeted
	Detail    map[string]string `json:"detail,omitempty"`
}

// Health is a coarse rollup. An adapter reports it from the engine's own
// state; the controller then marks a running engine degraded while its last
// active Probe fails (core.probeFailing).
type Health string

const (
	HealthOK       Health = "ok"
	HealthDegraded Health = "degraded"
	HealthDown     Health = "down"
	HealthUnknown  Health = "unknown"
)

// Probe is the result of an active connectivity check.
type Probe struct {
	OK       bool              `json:"ok"`
	EgressIP string            `json:"egress_ip,omitempty"`
	RTTms    float64           `json:"rtt_ms,omitempty"`
	Detail   map[string]string `json:"detail,omitempty"` // warp=on, colo=DME, ...
	Reason   string            `json:"reason,omitempty"` // set when !OK
	TS       int64             `json:"ts"`               // unix seconds
}

// Routing is the target set for one engine.
type Routing struct {
	Domains   []string `json:"domains"`
	CIDRs     []string `json:"cidrs"`
	Endpoints []string `json:"endpoints,omitempty"` // nfqws2 only — other tunnels' upstreams
	Strategy  string   `json:"strategy,omitempty"`  // nfqws2 only — strategy snippet id
}

// Configurable is implemented by engines whose runtime target (server URI, subscription, ...)
// is set at runtime rather than fixed at build time — xray today (nfqws2/usque aren't, yet).
// Callers type-assert for it rather than it being part of Engine, so adapters that don't need
// runtime config don't have to implement a no-op.
type Configurable interface {
	SetConfig(ctx context.Context, cfg map[string]string) error
}
