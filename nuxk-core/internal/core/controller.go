package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"nuxk.dev/horizon/core/internal/engine"
	"nuxk.dev/horizon/core/internal/state"
)

var (
	ErrEngineNotFound  = errors.New("no such engine")
	ErrNotConfigurable = errors.New("engine does not accept runtime config")
	ErrBadAction       = errors.New("action must be start|stop|restart")
	ErrNoStrategies    = errors.New("engine does not take strategies")
	ErrBadStrategy     = errors.New("invalid strategy")
	ErrNoProbeSites    = errors.New("engine does not take probe sites")
	ErrBadProbeSites   = errors.New("invalid probe sites")
)

const (
	autoProbeSites = 5  // picked from the DPI list when the person chose none
	maxProbeSites  = 10 // checked in parallel, 8 s each, on a small router
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

	// PlaneStatus, when set, fills Snapshot.Plane (the routing plane runs its
	// own loop; the controller only reports it).
	PlaneStatus func() any

	// Tunneled, when set, names the domains the router sends into tunnels
	// (nuxk's WARP/VLESS groups, the person's own routed lists): opening
	// them never meets the ISP's DPI, so they prove nothing about nfqws2.
	Tunneled func() []string

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
	subs := time.NewTicker(time.Hour) // subscriptions: re-read when due
	defer info.Stop()
	defer probe.Stop()
	defer subs.Stop()

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
		case now := <-subs.C:
			c.refreshDue(ctx, now)
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
// engine accepted it, stores it for re-apply, with what it points at.
func (c *Controller) SetConfig(ctx context.Context, k engine.Kind, cfg map[string]string) (engine.Upstream, error) {
	e, ok := c.Reg.Get(k)
	if !ok {
		return engine.Upstream{}, ErrEngineNotFound
	}
	ce, ok := e.(engine.Configurable)
	if !ok {
		return engine.Upstream{}, ErrNotConfigurable
	}
	up, err := ce.SetConfig(ctx, cfg)
	if err != nil {
		c.setLastErr(k, err)
		return up, err
	}
	c.setLastErr(k, nil)
	_, err = c.Store.UpdateDesired(k, func(d *state.Desired) { d.Config, d.Upstream = cfg, &up })
	c.Kick()
	return up, err
}

var ErrNotSubscription = errors.New("the engine's server isn't from a subscription")

// Upstream is what a Configurable engine's config points at (no secrets).
func (c *Controller) Upstream(k engine.Kind) (engine.Upstream, error) {
	e, ok := c.Reg.Get(k)
	if !ok {
		return engine.Upstream{}, ErrEngineNotFound
	}
	if _, ok := e.(engine.Configurable); !ok {
		return engine.Upstream{}, ErrNotConfigurable
	}
	d, err := c.Store.LoadDesired(k)
	if err != nil || d.Upstream == nil {
		return engine.Upstream{Servers: []engine.UpstreamServer{}}, err
	}
	return *d.Upstream, nil
}

// PickServer switches to another server of the stored subscription.
func (c *Controller) PickServer(ctx context.Context, k engine.Kind, key string) (engine.Upstream, error) {
	d, err := c.Store.LoadDesired(k)
	if err != nil {
		return engine.Upstream{}, err
	}
	if d.Config["sub_url"] == "" {
		return engine.Upstream{}, ErrNotSubscription
	}
	cfg := maps.Clone(d.Config)
	cfg["pick"] = key
	return c.SetConfig(ctx, k, cfg)
}

// RefreshUpstream re-reads the stored subscription (or re-applies the link):
// the same server with the same settings restarts nothing. A failure keeps
// the running config and is noted in the Upstream.
func (c *Controller) RefreshUpstream(ctx context.Context, k engine.Kind) (engine.Upstream, error) {
	d, err := c.Store.LoadDesired(k)
	if err != nil {
		return engine.Upstream{}, err
	}
	if len(d.Config) == 0 {
		return engine.Upstream{}, ErrNotSubscription
	}
	up, err := c.SetConfig(ctx, k, d.Config)
	if err != nil && d.Upstream != nil {
		old := *d.Upstream
		old.Error = err.Error()
		_, _ = c.Store.UpdateDesired(k, func(d *state.Desired) { d.Upstream = &old })
		return old, err
	}
	return up, err
}

// subscription refresh: as often as the panel asks (Profile-Update-Interval),
// within [1 h, 24 h]; 12 h when it doesn't say. One HTTPS request each time.
const (
	subRefreshDefault = 12 * time.Hour
	subRefreshMin     = time.Hour
	subRefreshMax     = 24 * time.Hour
)

// refreshDue re-reads the subscriptions whose time has come.
func (c *Controller) refreshDue(ctx context.Context, now time.Time) {
	for _, k := range c.Reg.Kinds() {
		d, err := c.Store.LoadDesired(k)
		if err != nil || d.Upstream == nil || d.Upstream.Source != "subscription" || d.Config["sub_url"] == "" {
			continue
		}
		every := subRefreshDefault
		if d.Upstream.RefreshS > 0 {
			every = min(max(time.Duration(d.Upstream.RefreshS)*time.Second, subRefreshMin), subRefreshMax)
		}
		if now.Sub(time.Unix(d.Upstream.FetchedAt, 0)) < every {
			continue
		}
		rctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		if _, err := c.RefreshUpstream(rctx, k); err != nil {
			slog.Warn("subscription refresh", "engine", k, "err", err)
		} else {
			slog.Info("subscription refreshed", "engine", k)
		}
		cancel()
	}
}

// SetStrategies replaces the engine's nuxk strategies (nfqws2: per-domain
// profiles in NFQWS_ARGS_CUSTOM); an empty set removes them. The engine
// restarts to take them and restores its old config if it won't start —
// then the error says so and nothing is stored.
func (c *Controller) SetStrategies(ctx context.Context, k engine.Kind, ss []engine.Strategy) error {
	e, ok := c.Reg.Get(k)
	if !ok {
		return ErrEngineNotFound
	}
	se, ok := e.(engine.Strategist)
	if !ok {
		return ErrNoStrategies
	}
	old, _ := c.Store.LoadDesired(k)
	since := map[string]int64{}
	for _, s := range old.Strategies {
		since[s.ID] = s.AppliedAt
	}
	now := time.Now().Unix()
	seen := map[string]bool{}
	for i := range ss {
		ss[i].Args = engine.NormalizeArgs(ss[i].Args)
		if err := engine.ValidateStrategy(ss[i]); err != nil {
			return fmt.Errorf("%w: %v", ErrBadStrategy, err)
		}
		if seen[ss[i].ID] {
			return fmt.Errorf("%w: id %q twice", ErrBadStrategy, ss[i].ID)
		}
		seen[ss[i].ID] = true
		ss[i].AppliedAt = now
		if t, ok := since[ss[i].ID]; ok && t > 0 {
			ss[i].AppliedAt = t
		}
	}
	if err := se.ApplyStrategies(ctx, ss); err != nil {
		c.setLastErr(k, err)
		return err
	}
	c.setLastErr(k, nil)
	slog.Info("strategies applied", "engine", k, "count", len(ss))
	_, err := c.Store.UpdateDesired(k, func(d *state.Desired) { d.Strategies = ss })
	c.Kick()
	return err
}

// Strategies returns the engine's stored nuxk strategies.
func (c *Controller) Strategies(k engine.Kind) ([]engine.Strategy, error) {
	e, ok := c.Reg.Get(k)
	if !ok {
		return nil, ErrEngineNotFound
	}
	if _, ok := e.(engine.Strategist); !ok {
		return nil, ErrNoStrategies
	}
	d, err := c.Store.LoadDesired(k)
	if d.Strategies == nil {
		d.Strategies = []engine.Strategy{}
	}
	return d.Strategies, err
}

// ProbeSites is what an engine's probe opens, and whether nuxk chose it.
type ProbeSites struct {
	Targets []string `json:"targets"`
	Auto    bool     `json:"auto"`
}

// ProbeTargets returns the sites nfqws2's probe opens.
func (c *Controller) ProbeTargets(k engine.Kind) (ProbeSites, error) {
	if _, ok := c.Reg.Get(k); !ok {
		return ProbeSites{}, ErrEngineNotFound
	}
	if k != engine.KindNfqws2 {
		return ProbeSites{}, ErrNoProbeSites
	}
	return c.probeSites(k, c.lastItems(k)), nil
}

// SetProbeTargets stores the person's own probe sites; none = automatic.
// A pasted "https://site/path" is cut down to the site.
func (c *Controller) SetProbeTargets(k engine.Kind, targets []string) (ProbeSites, error) {
	if _, ok := c.Reg.Get(k); !ok {
		return ProbeSites{}, ErrEngineNotFound
	}
	if k != engine.KindNfqws2 {
		return ProbeSites{}, ErrNoProbeSites
	}
	clean := []string{}
	for _, t := range targets {
		t = strings.ToLower(strings.TrimSpace(t))
		t = strings.TrimPrefix(strings.TrimPrefix(t, "https://"), "http://")
		t, _, _ = strings.Cut(t, "/")
		t = strings.TrimSuffix(t, ".")
		if t == "" || slices.Contains(clean, t) {
			continue
		}
		if !engine.ValidDomain(t) {
			return ProbeSites{}, fmt.Errorf("%w: %q is not a site name", ErrBadProbeSites, t)
		}
		clean = append(clean, t)
	}
	if len(clean) > maxProbeSites {
		return ProbeSites{}, fmt.Errorf("%w: at most %d sites", ErrBadProbeSites, maxProbeSites)
	}
	if _, err := c.Store.UpdateDesired(k, func(d *state.Desired) { d.ProbeTargets = clean }); err != nil {
		return ProbeSites{}, err
	}
	return c.probeSites(k, c.lastItems(k)), nil
}

// Probe runs an engine's probe now, with its sites, and keeps the result
// as if the schedule had taken it.
func (c *Controller) Probe(ctx context.Context, k engine.Kind) (engine.Probe, error) {
	e, ok := c.Reg.Get(k)
	if !ok {
		return engine.Probe{}, ErrEngineNotFound
	}
	p, err := e.Probe(ctx, c.probeSites(k, c.lastItems(k)).Targets)
	if err != nil {
		return p, err
	}
	c.mu.Lock()
	if st, ok := c.last[k]; ok {
		st.Probe, st.ProbeAt = &p, time.Now().Unix()
		c.last[k] = st
	}
	c.mu.Unlock()
	c.Kick()
	return p, nil
}

// probeSites: the person's own, else picked from the hostlist nfqws2 reports
// (items, comma-joined). Only nfqws2's probe takes sites.
func (c *Controller) probeSites(k engine.Kind, items string) ProbeSites {
	if k != engine.KindNfqws2 {
		return ProbeSites{}
	}
	if d, err := c.Store.LoadDesired(k); err == nil && len(d.ProbeTargets) > 0 {
		return ProbeSites{Targets: d.ProbeTargets}
	}
	var tun []string
	if c.Tunneled != nil {
		tun = c.Tunneled()
	}
	return ProbeSites{Targets: autoSites(strings.Split(items, ","), tun, autoProbeSites), Auto: true}
}

func (c *Controller) lastItems(k engine.Kind) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last[k].Detail["items"]
}

// autoSites picks n probe sites from a hostlist: names the router doesn't
// send into a tunnel, sites (example.com) before subdomains (a CDN name
// often has no page), spread over the list rather than its first letters.
func autoSites(hostlist, tunneled []string, n int) []string {
	tun := map[string]bool{}
	for _, d := range tunneled {
		tun[strings.ToLower(strings.TrimSpace(d))] = true
	}
	covered := func(d string) bool {
		for {
			if tun[d] {
				return true
			}
			_, parent, ok := strings.Cut(d, ".")
			if !ok {
				return false
			}
			d = parent
		}
	}
	var sites, subs []string
	seen := map[string]bool{}
	for _, d := range hostlist {
		d = strings.ToLower(strings.TrimSpace(d))
		if seen[d] || !engine.ValidDomain(d) || covered(d) {
			continue
		}
		seen[d] = true
		if strings.Count(d, ".") == 1 {
			sites = append(sites, d)
		} else {
			subs = append(subs, d)
		}
	}
	pick := func(from []string, k int) []string {
		if len(from) <= k {
			return from
		}
		out := make([]string, 0, k)
		for i := range k {
			out = append(out, from[i*len(from)/k])
		}
		return out
	}
	out := append([]string{}, pick(sites, n)...) // never null in JSON
	return append(out, pick(subs, n-len(out))...)
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
		// a probe on demand (Controller.Probe) may have landed while this
		// tick waited on the engine: keep the newer one
		if cur := c.last[k]; cur.ProbeAt > states[i].ProbeAt {
			states[i].Probe, states[i].ProbeAt = cur.Probe, cur.ProbeAt
		}
		c.last[k] = states[i]
	}
	c.mu.Unlock()

	c.Hub.set(Snapshot{
		Version: c.Version,
		TS:      time.Now().Unix(),
		Engines: states,
		Plane:   c.planeStatus(),
	})
}

// observe reads one engine's Info (and Probe when due), carrying the last
// probe forward between probe ticks.
func (c *Controller) observe(ctx context.Context, k engine.Kind, doProbe bool) EngineState {
	e, _ := c.Reg.Get(k)
	c.mu.Lock()
	st := c.last[k]
	c.mu.Unlock()

	prevErr := st.Detail["error"]
	if inf, err := e.Info(ctx); err != nil {
		if err.Error() != prevErr { // a persistent failure is logged once, not every tick
			slog.Warn("reconcile info", "engine", k, "err", err)
		}
		st.Info = engine.Info{Kind: k, Health: engine.HealthUnknown,
			Detail: map[string]string{"error": err.Error()}}
	} else {
		if prevErr != "" {
			slog.Info("reconcile info: engine answers again", "engine", k)
		}
		st.Info = inf
	}
	if st.Running && st.UptimeSec == 0 {
		st.UptimeSec = engine.ProcUptime(st.PID) // the script couldn't tell
	}
	if doProbe {
		if p, err := e.Probe(ctx, c.probeSites(k, st.Detail["items"]).Targets); err != nil {
			slog.Warn("reconcile probe", "engine", k, "err", err)
		} else {
			failed := 0
			for _, ch := range p.Checks {
				if !ch.OK {
					failed++
				}
			}
			slog.Debug("probe", "engine", k, "ok", p.OK, "rtt_ms", p.RTTms, "egress", p.EgressIP,
				"checks", len(p.Checks), "failed", failed, "reason", p.Reason)
			st.Probe = &p
			st.ProbeAt = time.Now().Unix()
		}
	}
	if st.Health == engine.HealthOK && probeFailing(st, time.Now()) {
		st.Health = engine.HealthDegraded
	}
	if d, err := c.Store.LoadDesired(k); err == nil {
		st.WantRun = d.Run
	}
	return st
}

// probeFailing: the engine runs, but its last probe — taken since this run
// started — failed. The process is up, the job isn't done: that is
// "degraded", not "ok", whatever the init script says.
func probeFailing(st EngineState, now time.Time) bool {
	if !st.Running || st.Probe == nil || st.Probe.OK {
		return false
	}
	if st.UptimeSec > 0 && st.ProbeAt < now.Unix()-st.UptimeSec {
		return false // a probe of the previous run
	}
	return true
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
		if up, err := ce.SetConfig(ctx, d.Config); err != nil {
			slog.Warn("restore config", "engine", k, "err", err)
			c.setLastErr(k, err)
		} else {
			_, _ = c.Store.UpdateDesired(k, func(d *state.Desired) { d.Upstream = &up })
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

func (c *Controller) planeStatus() any {
	if c.PlaneStatus == nil {
		return map[string]any{"backend": "off"}
	}
	return c.PlaneStatus()
}
