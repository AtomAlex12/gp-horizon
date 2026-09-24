package core

import (
	"context"
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
	startErr error
	calls    []string
	routings []engine.Routing
	configs  []map[string]string
}

func (f *fakeEngine) log(c string)      { f.mu.Lock(); f.calls = append(f.calls, c); f.mu.Unlock() }
func (f *fakeEngine) Kind() engine.Kind { return f.kind }
func (f *fakeEngine) Info(context.Context) (engine.Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return engine.Info{Kind: f.kind, Running: f.running, Endpoint: f.endpoint}, nil
}
func (f *fakeEngine) Probe(context.Context) (engine.Probe, error) { return engine.Probe{OK: true}, nil }
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

func (f fakeCfgEngine) SetConfig(_ context.Context, m map[string]string) error {
	f.log("config")
	f.mu.Lock()
	f.configs = append(f.configs, m)
	f.endpoint = "203.0.113.9:443"
	f.mu.Unlock()
	return nil
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

func TestSetConfigErrors(t *testing.T) {
	u := &fakeEngine{kind: engine.KindUsque}
	c, st := newTestController(t, u)
	ctx := context.Background()
	if err := c.SetConfig(ctx, engine.KindXray, map[string]string{"a": "b"}); err != ErrEngineNotFound {
		t.Errorf("unknown engine err = %v", err)
	}
	if err := c.SetConfig(ctx, engine.KindUsque, map[string]string{"a": "b"}); err != ErrNotConfigurable {
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
