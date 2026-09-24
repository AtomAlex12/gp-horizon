package core

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"sync"
	"time"

	"nuxk.dev/horizon/core/internal/engine"
	"nuxk.dev/horizon/core/internal/state"
)

var (
	ErrEngineNotFound  = errors.New("no such engine")
	ErrNotConfigurable = errors.New("engine does not accept runtime config")
	ErrBadAction       = errors.New("action must be start|stop|restart")
)

// Controller owns the engines' desired state and converges them to it.
//
// The API never drives an engine directly for a state change: it records the
// intent in the Store (engines/<kind>.json) and asks the Controller to act.
// The reconcile loop then keeps reality matching that intent — restarts an
// engine that should run (with backoff), re-applies routing/config an init
// script lost, and feeds the tunnels' upstream IPs into nfqws2's endpoints
// list ("WARP через nfqws") without anyone typing them in.
type Controller struct {
	Reg     *engine.Registry
	Store   *state.Store
	Hub     *Hub
	Version string

	InfoEvery     time.Duration // cheap Info poll
	ProbeEvery    time.Duration // active Probe (network cost)
	EngineTimeout time.Duration // per engine, per tick — one slow engine can't starve the others

	kick chan struct{}

	mu       sync.Mutex
	last     map[engine.Kind]EngineState
	backoff  map[string]*backoff
	lastErr  map[engine.Kind]string
	auto     []string // tunnel upstream IPs currently derived from usque/xray Info
	hardened []string // auto set last successfully pushed to nfqws2; nil = never
	restored bool
}

func NewController(reg *engine.Registry, st *state.Store, hub *Hub, version string) *Controller {
	return &Controller{
		Reg: reg, Store: st, Hub: hub, Version: version,
		InfoEvery:     5 * time.Second,
		ProbeEvery:    60 * time.Second,
		EngineTimeout: 25 * time.Second,
		kick:          make(chan struct{}, 1),
		last:          map[engine.Kind]EngineState{},
		backoff:       map[string]*backoff{},
		lastErr:       map[engine.Kind]string{},
	}
}

// Kick asks for an immediate Info tick (after a user action), without waiting
// for the timer. Never blocks.
func (c *Controller) Kick() {
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

// Run blocks until ctx is cancelled. One tick (with probes) fires immediately.
func (c *Controller) Run(ctx context.Context) {
	info := time.NewTicker(c.InfoEvery)
	probe := time.NewTicker(c.ProbeEvery)
	defer info.Stop()
	defer probe.Stop()

	c.tick(ctx, true)
	for {
		select {
		case <-ctx.Done():
			return
		case <-info.C:
			c.tick(ctx, false)
		case <-c.kick:
			c.tick(ctx, false)
		case <-probe.C:
			c.tick(ctx, true)
		}
	}
}

// --- user intents (called by the API) ---------------------------------------

// Action records the run intent and performs it now.
func (c *Controller) Action(ctx context.Context, k engine.Kind, action string) error {
	e, ok := c.Reg.Get(k)
	if !ok {
		return ErrEngineNotFound
	}
	var run bool
	var do func(context.Context) error
	switch action {
	case "start":
		run, do = true, e.Start
	case "stop":
		run, do = false, e.Stop
	case "restart":
		run, do = true, e.Restart
	default:
		return ErrBadAction
	}
	if _, err := c.Store.UpdateDesired(k, func(d *state.Desired) { d.Run = &run }); err != nil {
		return err
	}
	c.resetBackoff("run:" + string(k)) // a user action overrides any pending auto-restart delay
	err := do(ctx)
	c.setLastErr(k, err)
	c.Kick()
	return err
}

// Apply stores and applies an engine's routing. For nfqws2 an empty Endpoints
// keeps the stored manual endpoints (the web can't read them back), and the
// auto-derived tunnel upstreams are always merged in.
func (c *Controller) Apply(ctx context.Context, k engine.Kind, r engine.Routing) error {
	e, ok := c.Reg.Get(k)
	if !ok {
		return ErrEngineNotFound
	}
	if k == engine.KindNfqws2 && len(r.Endpoints) == 0 {
		if d, err := c.Store.LoadDesired(k); err == nil && d.Routing != nil {
			r.Endpoints = d.Routing.Endpoints
		}
	}
	if err := e.ApplyRouting(ctx, c.effectiveRouting(k, r)); err != nil {
		c.setLastErr(k, err)
		return err
	}
	c.setLastErr(k, nil)
	if k == engine.KindNfqws2 {
		c.mu.Lock()
		c.hardened = slices.Clone(c.auto)
		c.mu.Unlock()
	}
	_, err := c.Store.UpdateDesired(k, func(d *state.Desired) { d.Routing = &r })
	c.Kick()
	return err
}

// SetConfig applies a Configurable engine's runtime config and, once the
// engine accepted it, stores it for re-apply.
func (c *Controller) SetConfig(ctx context.Context, k engine.Kind, cfg map[string]string) error {
	e, ok := c.Reg.Get(k)
	if !ok {
		return ErrEngineNotFound
	}
	ce, ok := e.(engine.Configurable)
	if !ok {
		return ErrNotConfigurable
	}
	if err := ce.SetConfig(ctx, cfg); err != nil {
		c.setLastErr(k, err)
		return err
	}
	c.setLastErr(k, nil)
	_, err := c.Store.UpdateDesired(k, func(d *state.Desired) { d.Config = cfg })
	c.Kick()
	return err
}

// --- reconcile loop ---------------------------------------------------------

func (c *Controller) tick(ctx context.Context, doProbe bool) {
	kinds := c.Reg.Kinds()
	states := make([]EngineState, len(kinds))

	var wg sync.WaitGroup
	for i, k := range kinds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ectx, cancel := context.WithTimeout(ctx, c.EngineTimeout)
			defer cancel()
			states[i] = c.observe(ectx, k, doProbe)
		}()
	}
	wg.Wait()

	c.mu.Lock()
	firstPass := !c.restored
	c.restored = true
	c.mu.Unlock()

	for i, k := range kinds {
		ectx, cancel := context.WithTimeout(ctx, c.EngineTimeout)
		if firstPass {
			c.restore(ectx, k, states[i])
		}
		c.converge(ectx, k, &states[i])
		cancel()
	}
	c.harden(ctx, states)

	c.mu.Lock()
	for i, k := range kinds {
		states[i].LastError = c.lastErr[k]
		c.last[k] = states[i]
	}
	c.mu.Unlock()

	c.Hub.set(Snapshot{
		Version: c.Version,
		TS:      time.Now().Unix(),
		Engines: states,
		Plane:   map[string]any{"status": "not-wired"}, // nuxk-plane is the next milestone
	})
}

// observe reads one engine's Info (and Probe when due), carrying the last
// probe forward between probe ticks.
func (c *Controller) observe(ctx context.Context, k engine.Kind, doProbe bool) EngineState {
	e, _ := c.Reg.Get(k)
	c.mu.Lock()
	st := c.last[k]
	c.mu.Unlock()

	if inf, err := e.Info(ctx); err != nil {
		slog.Warn("reconcile info", "engine", k, "err", err)
		st.Info = engine.Info{Kind: k, Health: engine.HealthUnknown,
			Detail: map[string]string{"error": err.Error()}}
	} else {
		st.Info = inf
	}
	if doProbe {
		if p, err := e.Probe(ctx); err != nil {
			slog.Warn("reconcile probe", "engine", k, "err", err)
		} else {
			st.Probe = &p
			st.ProbeAt = time.Now().Unix()
		}
	}
	if d, err := c.Store.LoadDesired(k); err == nil {
		st.WantRun = d.Run
	}
	return st
}

// restore re-applies what the engine may have lost across a restart of the
// box, the container or nuxk-core itself. Runs once, on the first tick.
func (c *Controller) restore(ctx context.Context, k engine.Kind, st EngineState) {
	d, err := c.Store.LoadDesired(k)
	if err != nil {
		slog.Warn("restore: load desired", "engine", k, "err", err)
		return
	}
	e, _ := c.Reg.Get(k)
	if d.Routing != nil {
		if err := e.ApplyRouting(ctx, c.effectiveRouting(k, *d.Routing)); err != nil {
			slog.Warn("restore routing", "engine", k, "err", err)
			c.setLastErr(k, err)
		} else {
			slog.Info("restored routing", "engine", k)
		}
	}
	// Config is only re-sent when the engine reports no upstream — re-sending
	// it always would restart a healthy tunnel on every nuxk-core start.
	if ce, ok := e.(engine.Configurable); ok && len(d.Config) > 0 && st.Endpoint == "" && st.Detail["error"] == "" {
		if err := ce.SetConfig(ctx, d.Config); err != nil {
			slog.Warn("restore config", "engine", k, "err", err)
			c.setLastErr(k, err)
		} else {
			slog.Info("restored config", "engine", k)
		}
	}
}

// converge starts/stops an engine whose run state differs from the intent,
// with exponential backoff so a crash-looping engine isn't hammered.
func (c *Controller) converge(ctx context.Context, k engine.Kind, st *EngineState) {
	if st.WantRun == nil || st.Detail["error"] != "" {
		return // unmanaged, or we can't even read its state
	}
	key := "run:" + string(k)
	if *st.WantRun == st.Running {
		c.resetBackoff(key)
		return
	}
	if !c.due(key) {
		return
	}
	e, _ := c.Reg.Get(k)
	var err error
	if *st.WantRun {
		slog.Info("converge: starting engine", "engine", k)
		err = e.Start(ctx)
	} else {
		slog.Info("converge: stopping engine", "engine", k)
		err = e.Stop(ctx)
	}
	if err != nil {
		slog.Warn("converge", "engine", k, "err", err)
	}
	c.setLastErr(k, err)
}

// harden keeps nfqws2's endpoints list = manual endpoints ∪ the IPs the
// tunnel engines currently dial, so their handshakes get desynced too.
func (c *Controller) harden(ctx context.Context, states []EngineState) {
	var auto []string
	for _, st := range states {
		if st.Kind == engine.KindNfqws2 {
			continue
		}
		if ip := endpointIP(st.Endpoint); ip != "" && !slices.Contains(auto, ip) {
			auto = append(auto, ip)
		}
	}
	slices.Sort(auto)

	c.mu.Lock()
	c.auto = auto
	same := c.hardened != nil && slices.Equal(c.hardened, auto)
	c.mu.Unlock()

	e, ok := c.Reg.Get(engine.KindNfqws2)
	if !ok || same || (len(auto) == 0 && c.hardenedNil()) || !c.due("harden") {
		return
	}
	var r engine.Routing // Domains nil = leave the desync list alone
	if d, err := c.Store.LoadDesired(engine.KindNfqws2); err == nil && d.Routing != nil {
		r.Endpoints = d.Routing.Endpoints
	}
	ectx, cancel := context.WithTimeout(ctx, c.EngineTimeout)
	defer cancel()
	if err := e.ApplyRouting(ectx, c.effectiveRouting(engine.KindNfqws2, r)); err != nil {
		slog.Warn("harden: push endpoints to nfqws2", "err", err)
		return
	}
	slog.Info("harden: nfqws2 endpoints updated", "auto", auto)
	c.resetBackoff("harden")
	c.mu.Lock()
	c.hardened = auto
	c.mu.Unlock()
}

func (c *Controller) hardenedNil() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hardened == nil
}

// effectiveRouting merges the auto-derived tunnel endpoints into nfqws2's
// routing. Other engines get r unchanged.
func (c *Controller) effectiveRouting(k engine.Kind, r engine.Routing) engine.Routing {
	if k != engine.KindNfqws2 {
		return r
	}
	c.mu.Lock()
	auto := slices.Clone(c.auto)
	c.mu.Unlock()
	eps := append([]string{}, r.Endpoints...) // non-nil: the controller owns the endpoints list
	for _, ip := range auto {
		if !slices.Contains(eps, ip) {
			eps = append(eps, ip)
		}
	}
	r.Endpoints = eps
	return r
}

// endpointIP extracts the IP literal from "ip", "ip:port" or "[v6]:port".
// Hostnames yield "" — the endpoints list is IP-only.
func endpointIP(ep string) string {
	if ep == "" {
		return ""
	}
	host := ep
	if h, _, err := net.SplitHostPort(ep); err == nil {
		host = h
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return ""
	}
	return a.Unmap().String()
}

func (c *Controller) setLastErr(k engine.Kind, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		delete(c.lastErr, k)
		return
	}
	c.lastErr[k] = err.Error()
}

// --- backoff ----------------------------------------------------------------

type backoff struct {
	fails int
	next  time.Time
}

const (
	backoffBase = 10 * time.Second
	backoffMax  = 5 * time.Minute
)

// due reports whether an attempt for key may run now, and if so books the
// next earliest retry (10s, 20s, 40s … capped at 5m).
func (c *Controller) due(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	b := c.backoff[key]
	if b == nil {
		b = &backoff{}
		c.backoff[key] = b
	}
	now := time.Now()
	if now.Before(b.next) {
		return false
	}
	d := backoffBase << min(b.fails, 5)
	b.next = now.Add(min(d, backoffMax))
	b.fails++
	return true
}

func (c *Controller) resetBackoff(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.backoff, key)
}
