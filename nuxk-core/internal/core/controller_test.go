package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"nuxk.dev/horizon/core/internal/engine"
	"nuxk.dev/horizon/core/internal/state"
)

// fakeEngine is an in-memory engine: Start/Stop flip running, calls are logged.
type fakeEngine struct {
	kind     engine.Kind
	mu       sync.Mutex
	running  bool
	endpoint string
	health   engine.Health
	probe    *engine.Probe
	pid      int
	startErr error
	calls    []string
	routings []engine.Routing
	configs  []map[string]string
	items    string     // Detail["items"]: the hostlist nfqws2 reports
	targets  [][]string // the sites each Probe was asked to open
	onInfo   func()     // runs inside Info, outside the lock
	failNext bool       // fakeCfgEngine: the next SetConfig fails
}

func (f *fakeEngine) log(c string)      { f.mu.Lock(); f.calls = append(f.calls, c); f.mu.Unlock() }
func (f *fakeEngine) Kind() engine.Kind { return f.kind }
func (f *fakeEngine) Info(context.Context) (engine.Info, error) {
	if f.onInfo != nil {
		f.onInfo()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return engine.Info{Kind: f.kind, Running: f.running, Endpoint: f.endpoint, Health: f.health, PID: f.pid,
		Detail: map[string]string{"items": f.items}}, nil
}
func (f *fakeEngine) Probe(_ context.Context, targets []string) (engine.Probe, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.targets = append(f.targets, targets)
	if f.probe == nil {
		return engine.Probe{OK: true}, nil
	}
	return *f.probe, nil
}
func (f *fakeEngine) Start(context.Context) error {
	f.log("start")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return f.startErr
	}
	f.running = true
	return nil
}
func (f *fakeEngine) Stop(context.Context) error {
	f.log("stop")
	f.mu.Lock()
	f.running = false
	f.mu.Unlock()
	return nil
}
func (f *fakeEngine) Restart(ctx context.Context) error { f.log("restart"); return nil }
func (f *fakeEngine) ApplyRouting(_ context.Context, r engine.Routing) error {
	f.log("apply")
	f.mu.Lock()
	f.routings = append(f.routings, r)
	f.mu.Unlock()
	return nil
}
func (f *fakeEngine) count(c string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, x := range f.calls {
		if x == c {
			n++
		}
	}
	return n
}
func (f *fakeEngine) lastRouting() engine.Routing {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.routings[len(f.routings)-1]
}

type fakeCfgEngine struct{ *fakeEngine }

// SetConfig: a link is one server; a subscription is three (a, b, c), "pick"
// chooses by key; "fail" in the config fails it.
func (f fakeCfgEngine) SetConfig(_ context.Context, m map[string]string) (engine.Upstream, error) {
	f.log("config")
	f.mu.Lock()
	defer f.mu.Unlock()
	if m["fail"] != "" || f.failNext {
		return engine.Upstream{}, errors.New("subscription down")
	}
	f.configs = append(f.configs, m)
	f.endpoint = "203.0.113.9:443"
	if m["sub_url"] == "" {
		return engine.Upstream{Source: "link", Servers: []engine.UpstreamServer{{Key: "x@1.1.1.1:443"}}, FetchedAt: time.Now().Unix()}, nil
	}
	up := engine.Upstream{Source: "subscription", Title: "home", RefreshS: 3 * 3600, FetchedAt: time.Now().Unix()}
	for i, n := range []string{"a", "b", "c"} {
		up.Servers = append(up.Servers, engine.UpstreamServer{Key: n + "@10.0.0.1:443", Name: n})
		if m["pick"] == n+"@10.0.0.1:443" {
			up.Active = i
		}
	}
	return up, nil
}

func newTestController(t *testing.T, engines ...engine.Engine) (*Controller, *state.Store) {
	t.Helper()
	st, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg := engine.NewRegistry()
	for _, e := range engines {
		reg.Add(e)
	}
	return NewController(reg, st, NewHub("test"), "test"), st
}

func TestConvergeRestartsWantedEngineWithBackoff(t *testing.T) {
	u := &fakeEngine{kind: engine.KindUsque, startErr: context.DeadlineExceeded}
	c, st := newTestController(t, u)
	yes := true
	if _, err := st.UpdateDesired(engine.KindUsque, func(d *state.Desired) { d.Run = &yes }); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	c.tick(ctx, false)
	c.tick(ctx, false) // inside the backoff window — no second attempt
	if n := u.count("start"); n != 1 {
		t.Fatalf("start attempts = %d, want 1 (backoff)", n)
	}
	if got := c.Hub.Get().Engines[0].LastError; got == "" {
		t.Error("failed auto-start must surface as last_error")
	}

	// Backoff expires, the engine starts, the error clears.
	u.mu.Lock()
	u.startErr = nil
	u.mu.Unlock()
	c.mu.Lock()
	c.backoff["run:usque"].next = time.Time{}
	c.mu.Unlock()
	c.tick(ctx, false)
	c.tick(ctx, false)
	if n := u.count("start"); n != 2 {
		t.Errorf("start attempts = %d, want 2", n)
	}
	if s := c.Hub.Get().Engines[0]; !s.Running || s.LastError != "" || s.WantRun == nil || !*s.WantRun {
		t.Errorf("state after recovery = %+v", s)
	}
}

func TestUnmanagedEngineIsLeftAlone(t *testing.T) {
	u := &fakeEngine{kind: engine.KindUsque}
	c, _ := newTestController(t, u)
	c.tick(context.Background(), false)
	if n := u.count("start") + u.count("stop"); n != 0 {
		t.Errorf("unmanaged engine got %d start/stop calls", n)
	}
}

func TestActionRecordsIntent(t *testing.T) {
	u := &fakeEngine{kind: engine.KindUsque, running: true}
	c, st := newTestController(t, u)
	if err := c.Action(context.Background(), engine.KindUsque, "stop"); err != nil {
		t.Fatal(err)
	}
	d, _ := st.LoadDesired(engine.KindUsque)
	if d.Run == nil || *d.Run {
		t.Errorf("desired run = %v, want false", d.Run)
	}
	if err := c.Action(context.Background(), engine.KindUsque, "bogus"); err != ErrBadAction {
		t.Errorf("bogus action err = %v", err)
	}
}

// Routing and config lost by the engine (fresh container) are re-applied on
// the first tick; config only when the engine reports no upstream.
func TestRestoreOnFirstTick(t *testing.T) {
	n := &fakeEngine{kind: engine.KindNfqws2, running: true}
	x := fakeCfgEngine{&fakeEngine{kind: engine.KindXray, running: true}}
	c, st := newTestController(t, n, x)
	_, _ = st.UpdateDesired(engine.KindNfqws2, func(d *state.Desired) {
		d.Routing = &engine.Routing{Domains: []string{"a.com"}}
	})
	_, _ = st.UpdateDesired(engine.KindXray, func(d *state.Desired) {
		d.Config = map[string]string{"vless_uri": "vless://x@203.0.113.9:443"}
	})
	c.tick(context.Background(), false)
	if len(n.routings) == 0 || n.routings[0].Domains[0] != "a.com" {
		t.Errorf("nfqws2 routing not restored: %+v", n.routings)
	}
	if x.count("config") != 1 {
		t.Errorf("xray config restores = %d, want 1", x.count("config"))
	}
	c.tick(context.Background(), false)
	if x.count("config") != 1 {
		t.Error("config must be restored once, not every tick")
	}
}

// Tunnel upstream IPs flow into nfqws2's endpoints list automatically,
// merged with the manual ones, without touching the desync domains.
func TestHardenPushesTunnelEndpoints(t *testing.T) {
	n := &fakeEngine{kind: engine.KindNfqws2, running: true}
	u := &fakeEngine{kind: engine.KindUsque, running: true, endpoint: "162.159.198.2:443"}
	x := &fakeEngine{kind: engine.KindXray, running: true, endpoint: "engage.example.com:443"}
	c, st := newTestController(t, n, u, x)
	_, _ = st.UpdateDesired(engine.KindNfqws2, func(d *state.Desired) {
		d.Routing = &engine.Routing{Domains: []string{"a.com"}, Endpoints: []string{"198.51.100.7"}}
	})
	ctx := context.Background()
	c.tick(ctx, false)

	r := n.lastRouting()
	if r.Domains != nil {
		t.Errorf("harden must not rewrite desync domains, got %v", r.Domains)
	}
	want := []string{"198.51.100.7", "162.159.198.2"}
	if len(r.Endpoints) != 2 || r.Endpoints[0] != want[0] || r.Endpoints[1] != want[1] {
		t.Errorf("endpoints = %v, want %v (hostname upstream skipped)", r.Endpoints, want)
	}

	before := n.count("apply")
	c.tick(ctx, false)
	if n.count("apply") != before {
		t.Error("unchanged upstreams must not re-push")
	}

	u.mu.Lock()
	u.endpoint = "162.159.198.1:443"
	u.mu.Unlock()
	c.tick(ctx, false)
	if got := n.lastRouting().Endpoints; len(got) != 2 || got[1] != "162.159.198.1" {
		t.Errorf("after upstream change endpoints = %v", got)
	}
}

// The web sends empty endpoints when the field is left blank — stored manual
// endpoints survive, and auto ones are merged in.
func TestApplyKeepsStoredEndpoints(t *testing.T) {
	n := &fakeEngine{kind: engine.KindNfqws2, running: true}
	c, st := newTestController(t, n)
	ctx := context.Background()
	if err := c.Apply(ctx, engine.KindNfqws2, engine.Routing{Domains: []string{"a.com"}, Endpoints: []string{"198.51.100.7"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Apply(ctx, engine.KindNfqws2, engine.Routing{Domains: []string{"b.com"}}); err != nil {
		t.Fatal(err)
	}
	r := n.lastRouting()
	if len(r.Endpoints) != 1 || r.Endpoints[0] != "198.51.100.7" || r.Domains[0] != "b.com" {
		t.Errorf("applied = %+v", r)
	}
	d, _ := st.LoadDesired(engine.KindNfqws2)
	if d.Routing == nil || d.Routing.Domains[0] != "b.com" || d.Routing.Endpoints[0] != "198.51.100.7" {
		t.Errorf("stored = %+v", d.Routing)
	}
}

func TestUpstreamSubscription(t *testing.T) {
	x := fakeCfgEngine{&fakeEngine{kind: engine.KindXray, running: true}}
	c, st := newTestController(t, x)
	ctx := context.Background()

	if up, err := c.Upstream(engine.KindXray); err != nil || up.Source != "" || up.Servers == nil {
		t.Errorf("nothing set yet: %+v %v", up, err)
	}
	if _, err := c.SetConfig(ctx, engine.KindXray, map[string]string{"vless_uri": "vless://…"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PickServer(ctx, engine.KindXray, "b@10.0.0.1:443"); !errors.Is(err, ErrNotSubscription) {
		t.Errorf("a link has nothing to pick: %v", err)
	}

	if _, err := c.SetConfig(ctx, engine.KindXray, map[string]string{"sub_url": "https://panel/sub/x"}); err != nil {
		t.Fatal(err)
	}
	up, err := c.PickServer(ctx, engine.KindXray, "b@10.0.0.1:443")
	if err != nil || up.Active != 1 {
		t.Fatalf("pick b: %+v %v", up, err)
	}
	d, _ := st.LoadDesired(engine.KindXray)
	if d.Config["sub_url"] != "https://panel/sub/x" || d.Config["pick"] != "b@10.0.0.1:443" || d.Upstream == nil || d.Upstream.Active != 1 {
		t.Errorf("stored: %+v %+v", d.Config, d.Upstream)
	}
	if up, _ := c.Upstream(engine.KindXray); up.Title != "home" || len(up.Servers) != 3 || up.Active != 1 {
		t.Errorf("served: %+v", up)
	}

	// a refresh keeps the pick; a failed one keeps what runs and says why
	if up, err := c.RefreshUpstream(ctx, engine.KindXray); err != nil || up.Active != 1 {
		t.Errorf("refresh: %+v %v", up, err)
	}
	x.mu.Lock()
	x.failNext = true
	x.mu.Unlock()
	if up, err := c.RefreshUpstream(ctx, engine.KindXray); err == nil || up.Error != "subscription down" || up.Active != 1 {
		t.Errorf("failed refresh: %+v %v", up, err)
	}
	if d, _ := st.LoadDesired(engine.KindXray); d.Config["pick"] != "b@10.0.0.1:443" || d.Upstream.Error == "" {
		t.Errorf("after a failed refresh: %+v %+v", d.Config, d.Upstream)
	}
	x.mu.Lock()
	x.failNext = false
	x.mu.Unlock()

	// due: every Profile-Update-Interval (3 h here), not before
	n := x.count("config")
	c.refreshDue(ctx, time.Now().Add(time.Hour))
	if x.count("config") != n {
		t.Error("refreshed before it was due")
	}
	c.refreshDue(ctx, time.Now().Add(4*time.Hour))
	if x.count("config") != n+1 {
		t.Error("not refreshed when due")
	}
	if up, _ := c.Upstream(engine.KindXray); up.Error != "" {
		t.Errorf("a good refresh clears the error: %+v", up)
	}
}

func TestSetConfigErrors(t *testing.T) {
	u := &fakeEngine{kind: engine.KindUsque}
	c, st := newTestController(t, u)
	ctx := context.Background()
	if _, err := c.SetConfig(ctx, engine.KindXray, map[string]string{"a": "b"}); err != ErrEngineNotFound {
		t.Errorf("unknown engine err = %v", err)
	}
	if _, err := c.SetConfig(ctx, engine.KindUsque, map[string]string{"a": "b"}); err != ErrNotConfigurable {
		t.Errorf("usque err = %v", err)
	}
	if d, _ := st.LoadDesired(engine.KindUsque); d.Config != nil {
		t.Error("rejected config must not be stored")
	}
}

func TestEndpointIP(t *testing.T) {
	for in, want := range map[string]string{
		"":                       "",
		"162.159.198.2":          "162.159.198.2",
		"162.159.198.2:443":      "162.159.198.2",
		"[2606:4700::1]:443":     "2606:4700::1",
		"engage.example.com:443": "",
		"::ffff:1.2.3.4":         "1.2.3.4",
	} {
		if got := endpointIP(in); got != want {
			t.Errorf("endpointIP(%q) = %q, want %q", in, got, want)
		}
	}
}

// A running engine whose probe fails is degraded, not "ok"; a probe of an
// earlier run doesn't count against the current one.
func TestFailingProbeDegradesHealth(t *testing.T) {
	n := &fakeEngine{kind: engine.KindNfqws2, running: true, health: engine.HealthOK,
		probe: &engine.Probe{OK: false, Reason: "timeout_or_reset"}}
	c, _ := newTestController(t, n)
	ctx := context.Background()
	c.tick(ctx, true)
	if h := c.Hub.Get().Engines[0].Health; h != engine.HealthDegraded {
		t.Fatalf("health with a failing probe = %s, want degraded", h)
	}
	n.mu.Lock()
	n.probe = nil
	n.mu.Unlock()
	c.tick(ctx, true)
	if h := c.Hub.Get().Engines[0].Health; h != engine.HealthOK {
		t.Errorf("health after a good probe = %s, want ok", h)
	}

	now := time.Unix(1_700_000_000, 0)
	old := EngineState{Info: engine.Info{Running: true, UptimeSec: 60}, Probe: &engine.Probe{OK: false}, ProbeAt: now.Unix() - 120}
	if probeFailing(old, now) {
		t.Error("a failed probe from before this run must not degrade it")
	}
	old.ProbeAt = now.Unix() - 30
	if !probeFailing(old, now) {
		t.Error("a failed probe of this run degrades it")
	}
}

func TestAutoSites(t *testing.T) {
	hostlist := []string{"rutracker.org", "rr1---sn-abc.googlevideo.com", "instagram.com", "static.cdninstagram.com",
		"x.com", "chatgpt.com", "BrowserLeaks.com", "bad;name", "youtube.com", "rutracker.org", "4pda.to", "ytimg.com"}
	tunneled := []string{"chatgpt.com", "cdninstagram.com", "instagram.com"}
	// sites before subdomains; tunnel-bound (and their subdomains), junk and
	// repeats out; six sites left, five spread over them
	got := autoSites(hostlist, tunneled, 5)
	if want := "rutracker.org,x.com,browserleaks.com,youtube.com,4pda.to"; strings.Join(got, ",") != want {
		t.Errorf("got %v, want %s", got, want)
	}
	if got := autoSites(hostlist[:2], nil, 5); strings.Join(got, ",") != "rutracker.org,rr1---sn-abc.googlevideo.com" {
		t.Errorf("few sites: subdomains fill up: %v", got)
	}
	if got := autoSites([]string{""}, nil, 5); len(got) != 0 {
		t.Errorf("empty hostlist: %v", got)
	}
}

func TestProbeSites(t *testing.T) {
	n := &fakeEngine{kind: engine.KindNfqws2, running: true, health: engine.HealthOK,
		items: "rutracker.org,chatgpt.com,x.com"}
	u := &fakeEngine{kind: engine.KindUsque, running: true, health: engine.HealthOK}
	c, _ := newTestController(t, n, u)
	c.Tunneled = func() []string { return []string{"chatgpt.com"} }
	ctx := context.Background()
	c.tick(ctx, true)

	n.mu.Lock()
	asked := n.targets
	n.mu.Unlock()
	if len(asked) != 1 || strings.Join(asked[0], ",") != "rutracker.org,x.com" {
		t.Errorf("auto sites without the tunneled one: %v", asked)
	}
	if u.targets[0] != nil {
		t.Errorf("a tunnel's probe takes no sites: %v", u.targets)
	}
	ps, err := c.ProbeTargets(engine.KindNfqws2)
	if err != nil || !ps.Auto || len(ps.Targets) != 2 {
		t.Errorf("GET: %+v %v", ps, err)
	}

	ps, err = c.SetProbeTargets(engine.KindNfqws2, []string{" https://RuTracker.org/forum/", "x.com.", "x.com", ""})
	if err != nil || ps.Auto || strings.Join(ps.Targets, ",") != "rutracker.org,x.com" {
		t.Errorf("own sites, cleaned: %+v %v", ps, err)
	}
	if _, err := c.SetProbeTargets(engine.KindNfqws2, []string{"a.com; reboot"}); !errors.Is(err, ErrBadProbeSites) {
		t.Errorf("junk refused: %v", err)
	}
	if _, err := c.SetProbeTargets(engine.KindUsque, []string{"a.com"}); !errors.Is(err, ErrNoProbeSites) {
		t.Errorf("usque takes no sites: %v", err)
	}

	// a probe on demand uses them and is kept like a scheduled one
	n.mu.Lock()
	n.probe = &engine.Probe{OK: false, Reason: engine.ReasonTLSTimeout}
	n.mu.Unlock()
	if _, err := c.Probe(ctx, engine.KindNfqws2); err != nil {
		t.Fatal(err)
	}
	n.mu.Lock()
	last := n.targets[len(n.targets)-1]
	n.mu.Unlock()
	if strings.Join(last, ",") != "rutracker.org,x.com" {
		t.Errorf("on-demand probe sites: %v", last)
	}
	c.tick(ctx, false)
	for _, e := range c.Hub.Get().Engines {
		if e.Kind == engine.KindNfqws2 && (e.Probe == nil || e.Probe.Reason != engine.ReasonTLSTimeout || e.Health != engine.HealthDegraded) {
			t.Errorf("on-demand probe not kept: %+v", e)
		}
	}

	ps, _ = c.SetProbeTargets(engine.KindNfqws2, nil)
	if !ps.Auto {
		t.Errorf("none = automatic again: %+v", ps)
	}
}

// An info tick that started before a probe on demand finished must not put
// the older probe back (it waits on the same engine while the probe runs).
func TestProbeOnDemandSurvivesATickInFlight(t *testing.T) {
	n := &fakeEngine{kind: engine.KindNfqws2, running: true, health: engine.HealthOK}
	c, _ := newTestController(t, n)
	ctx := context.Background()
	c.tick(ctx, true) // the scheduled probe: ok
	newer := &engine.Probe{OK: false, Reason: engine.ReasonReset}
	n.onInfo = func() { // the probe on demand lands mid-tick
		c.mu.Lock()
		st := c.last[engine.KindNfqws2]
		st.Probe, st.ProbeAt = newer, time.Now().Unix()+1
		c.last[engine.KindNfqws2] = st
		c.mu.Unlock()
	}
	c.tick(ctx, false)
	if p := c.Hub.Get().Engines[0].Probe; p == nil || p.Reason != engine.ReasonReset {
		t.Errorf("the newer probe was overwritten: %+v", p)
	}
}

func TestUptimeFromProc(t *testing.T) {
	root := t.TempDir()
	defer func(p string) { engine.ProcRoot = p }(engine.ProcRoot)
	engine.ProcRoot = root
	os.MkdirAll(filepath.Join(root, "974"), 0o755)
	// started 1000 s after boot (100000 ticks); the box is up 4600 s
	os.WriteFile(filepath.Join(root, "974", "stat"), []byte("974 (nfqws2 x) S 1 974 974 0 -1 4194560 1 0 0 0 5 3 0 0 20 0 1 0 100000 1 1 18446744073709551615 0"), 0o644)
	os.WriteFile(filepath.Join(root, "uptime"), []byte("4600.52 9000.00"), 0o644)

	n := &fakeEngine{kind: engine.KindNfqws2, running: true, pid: 974}
	c, _ := newTestController(t, n)
	c.tick(context.Background(), false)
	if up := c.Hub.Get().Engines[0].UptimeSec; up != 3600 {
		t.Errorf("uptime = %d, want 3600", up)
	}
	if engine.ProcUptime(12345) != 0 {
		t.Error("no such pid → 0")
	}
}

// strategyEngine is a fakeEngine that takes strategies.
type strategyEngine struct {
	*fakeEngine
	got [][]engine.Strategy
	err error
}

func (s *strategyEngine) ApplyStrategies(_ context.Context, ss []engine.Strategy) error {
	s.got = append(s.got, ss)
	return s.err
}

func TestSetStrategies(t *testing.T) {
	n := &strategyEngine{fakeEngine: &fakeEngine{kind: engine.KindNfqws2, running: true}}
	c, st := newTestController(t, n)
	ctx := context.Background()
	s := engine.Strategy{ID: "gp-1", Protocol: "tls", Domains: []string{"a.com"}, Args: "nfqws2 --filter-tcp=443 --lua-desync=multisplit:pos=1"}
	if err := c.SetStrategies(ctx, engine.KindNfqws2, []engine.Strategy{s}); err != nil {
		t.Fatal(err)
	}
	if got := n.got[0][0]; got.Args != "--lua-desync=multisplit:pos=1" || got.AppliedAt == 0 {
		t.Errorf("normalized/stamped: %+v", got)
	}
	d, _ := st.LoadDesired(engine.KindNfqws2)
	if len(d.Strategies) != 1 {
		t.Fatalf("stored %+v", d.Strategies)
	}
	first := d.Strategies[0].AppliedAt

	// applying again keeps when each one was first applied
	time.Sleep(1100 * time.Millisecond)
	c.SetStrategies(ctx, engine.KindNfqws2, []engine.Strategy{s})
	if d, _ := st.LoadDesired(engine.KindNfqws2); d.Strategies[0].AppliedAt != first {
		t.Error("applied_at must survive a re-apply")
	}

	bad := s
	bad.Args = "--lua-desync=fake:blob=@/etc/shadow"
	if err := c.SetStrategies(ctx, engine.KindNfqws2, []engine.Strategy{bad}); !errors.Is(err, ErrBadStrategy) {
		t.Errorf("bad args: %v", err)
	}
	if err := c.SetStrategies(ctx, engine.KindNfqws2, []engine.Strategy{s, s}); !errors.Is(err, ErrBadStrategy) {
		t.Errorf("duplicate id: %v", err)
	}

	// the engine refused (e.g. nfqws2 wouldn't start): nothing stored
	n.err = errors.New("nfqws2 не запустился с новой стратегией — вернул прежний конфиг")
	if err := c.SetStrategies(ctx, engine.KindNfqws2, nil); err == nil {
		t.Error("engine error must come back")
	}
	if d, _ := st.LoadDesired(engine.KindNfqws2); len(d.Strategies) != 1 {
		t.Error("a refused set must not replace the stored one")
	}

	u := &fakeEngine{kind: engine.KindUsque}
	c2, _ := newTestController(t, u)
	if err := c2.SetStrategies(ctx, engine.KindUsque, nil); !errors.Is(err, ErrNoStrategies) {
		t.Errorf("usque takes none: %v", err)
	}
}
