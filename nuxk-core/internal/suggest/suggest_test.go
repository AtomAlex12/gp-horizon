package suggest

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"nuxk.dev/horizon/core/internal/plane"
	"nuxk.dev/horizon/core/internal/probe"
	"nuxk.dev/horizon/core/internal/state"
)

// --- fakes ----------------------------------------------------------------------

type fakeRouter struct {
	covered map[string]string
	added   []string
	modes   map[string]plane.Mode // moved by hand: overrides what added says
	err     error
	status  plane.Status
}

func (f *fakeRouter) Covered(d string) string {
	if w := f.covered[d]; w != "" {
		return w
	}
	for _, a := range f.added { // an accepted domain is in a «Подбор» list
		if strings.HasSuffix(a, ":"+d) {
			return "список «Подбор»"
		}
	}
	return ""
}

func (f *fakeRouter) ModeOf(d string) plane.Mode {
	if m, ok := f.modes[d]; ok {
		return m
	}
	for i := len(f.added) - 1; i >= 0; i-- {
		if m, dom, _ := strings.Cut(f.added[i], ":"); dom == d {
			return plane.Mode(m)
		}
	}
	return ""
}

func (f *fakeRouter) AddAuto(m plane.Mode, d string) error {
	if f.err != nil {
		return f.err
	}
	f.added = append(f.added, string(m)+":"+d)
	return nil
}

func (f *fakeRouter) Status() plane.Status { return f.status }

// fakeProber answers from tables: ways[domain][via]; a way without an entry
// is refused, a via in skip is unavailable.
type fakeProber struct {
	mu    sync.Mutex
	dns   map[string]string
	ways  map[string]map[probe.Via]probe.Result
	skip  map[probe.Via]string
	hang  map[probe.Via]bool // the way takes until ctx ends, then is Interrupted
	onWay func(probe.Via)    // called as a way is walked (outside the lock)
	calls []string           // "domain via"
}

func newFake() *fakeProber {
	return &fakeProber{
		dns:  map[string]string{},
		ways: map[string]map[probe.Via]probe.Result{"ya.ru": {probe.ViaDirect: opened(100, 0)}},
		skip: map[probe.Via]string{},
	}
}

func (f *fakeProber) set(domain string, via probe.Via, r probe.Result) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ways[domain] == nil {
		f.ways[domain] = map[probe.Via]probe.Result{}
	}
	f.ways[domain][via] = r
}

func (f *fakeProber) Resolve(_ context.Context, d string) (net.IP, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if why := f.dns[d]; why != "" {
		return nil, why
	}
	return net.ParseIP("203.0.113.1"), ""
}

func (f *fakeProber) Way(ctx context.Context, d string, _ net.IP, via probe.Via) probe.Result {
	if f.onWay != nil {
		f.onWay(via)
	}
	f.mu.Lock()
	hang := f.hang[via]
	f.mu.Unlock()
	if hang {
		<-ctx.Done()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, d+" "+string(via))
	if hang {
		return probe.Result{Via: via, Skipped: probe.Interrupted}
	}
	if why := f.skip[via]; why != "" {
		return probe.Result{Via: via, Skipped: why}
	}
	r, ok := f.ways[d][via]
	if !ok {
		r = refused()
	}
	r.Via = via
	return r
}

func (f *fakeProber) Paths() []probe.PathState {
	out := []probe.PathState{{Via: probe.ViaDirect, Usable: true}}
	for _, v := range []probe.Via{probe.ViaVless, probe.ViaWarp} {
		out = append(out, probe.PathState{Via: v, Usable: f.skip[v] == "", Why: f.skip[v]})
	}
	return out
}

// walked: the ways measured for domain, in order (the reference left out).
func (f *fakeProber) walked(domain string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if d, via, _ := strings.Cut(c, " "); d == domain {
			out = append(out, via)
		}
	}
	return out
}

func (f *fakeProber) reset() {
	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()
}

// --- measured ways --------------------------------------------------------------

func opened(ttfb, kbps int) probe.Result {
	return probe.Result{TCP: true, TLS: true, Status: 200, Tries: 5, Opened: 5, TTFBMs: ttfb, Kbps: kbps}
}

func geoRefused() probe.Result {
	return probe.Result{TCP: true, TLS: true, Status: 403, Signal: probe.SignalExplicit, Tries: 2}
}

func challenged() probe.Result {
	return probe.Result{TCP: true, TLS: true, Status: 403, Signal: probe.SignalChallenge, Tries: 2}
}

func sniCut() probe.Result {
	t := true
	return probe.Result{TCP: true, TLSFail: probe.FailReset, Neutral: &t, Tries: 2}
}

func ipCutAfterTCP() probe.Result {
	f := false
	return probe.Result{TCP: true, TLSFail: probe.FailTimeout, Neutral: &f, Tries: 2}
}

func refused() probe.Result { return probe.Result{TCPFail: probe.FailTimeout, Tries: 2} }

func frozen() probe.Result {
	return probe.Result{TCP: true, TLS: true, Status: 200, Tries: 5, Opened: 5, TTFBMs: 200, Frozen: true}
}

func flaky() probe.Result {
	return probe.Result{TCP: true, TLS: true, Status: 200, Tries: 5, Opened: 2, TTFBMs: 300}
}

func broken() probe.Result {
	return probe.Result{TCP: true, TLS: true, Status: 502, Tries: 2}
}

// --- service --------------------------------------------------------------------

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newService(t *testing.T, p Prober, rt Router, desync bool) (*Service, *state.Store, *clock) {
	t.Helper()
	st, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := New(p, st, rt, func() bool { return desync })
	c := &clock{time.Unix(1_800_000_000, 0)}
	s.now = c.now
	return s, st, c
}

// drain runs the worker's job for everything queued.
func drain(s *Service) {
	for {
		select {
		case d := <-s.queue:
			s.process(context.Background(), d)
		default:
			return
		}
	}
}

func item(t *testing.T, s *Service, domain string) Item {
	t.Helper()
	for _, it := range s.Overview().Items {
		if it.Domain == domain {
			return it
		}
	}
	t.Fatalf("no item %s", domain)
	return Item{}
}

func TestCheckAcceptFlow(t *testing.T) {
	p := newFake()
	p.set("chatgpt.com", probe.ViaDirect, geoRefused())
	p.set("chatgpt.com", probe.ViaVless, opened(400, 0))
	rt := &fakeRouter{covered: map[string]string{"mine.com": "список «Мои»"}}
	s, st, _ := newService(t, p, rt, true)

	q := s.Check([]string{"ChatGPT.com", "mine.com", "not a host"}, true)
	if len(q.Queued) != 1 || q.Queued[0] != "chatgpt.com" || len(q.Skipped) != 2 {
		t.Fatalf("queued/skipped wrong: %+v", q)
	}
	if ov := s.Overview(); ov.Busy != 1 || ov.Items[0].State != StateChecking || ov.Items[0].Source != SourceUser {
		t.Fatalf("while queued: %+v", ov)
	}
	drain(s)

	ov := s.Overview()
	if ov.Busy != 0 || len(ov.Items) != 1 {
		t.Fatalf("after: %+v", ov)
	}
	it := ov.Items[0]
	if it.State != StateReady || it.Verdict != "geoblock" || it.Mode != "vless" || !it.Confirmed || len(it.Paths) != 2 || !it.Paths[1].Acceptable {
		t.Fatalf("item: %+v", it)
	}
	if ov.Settings != DefaultSettings() || !ov.Reference.OK || ov.Reference.Domain != "ya.ru" || ov.Reference.TTFBMs != 100 {
		t.Errorf("settings/reference: %+v %+v", ov.Settings, ov.Reference)
	}

	if err := s.Accept("chatgpt.com", "warp"); !errors.Is(err, ErrBadMode) {
		t.Errorf("a mode nobody verified must be refused, got %v", err)
	}
	if err := s.Accept("chatgpt.com", ""); err != nil {
		t.Fatal(err)
	}
	if len(rt.added) != 1 || rt.added[0] != "vless:chatgpt.com" {
		t.Errorf("plane got %v", rt.added)
	}
	if it := item(t, s, "chatgpt.com"); it.Accepted != "vless" || it.VerifyDue == 0 || it.Verified {
		t.Errorf("accepted: %+v", it)
	}
	if err := s.Accept("chatgpt.com", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("second accept: %v", err)
	}

	// persisted: a fresh service sees the accepted item and keeps it out of scans
	s2 := New(p, st, rt, nil)
	if got := s2.Overview().Items; len(got) != 1 || got[0].State != StateAccepted || got[0].Accepted != "vless" {
		t.Fatalf("reloaded: %+v", got)
	}
	if q := s2.Check([]string{"chatgpt.com"}, false); len(q.Queued) != 0 {
		t.Errorf("a scan must not redo an accepted domain: %+v", q)
	}
}

func TestDismissSticksForScansNotForExplicitChecks(t *testing.T) {
	s, _, _ := newService(t, newFake(), &fakeRouter{}, false)
	s.Check([]string{"claude.ai"}, true)
	drain(s)
	if err := s.Dismiss("claude.ai"); err != nil {
		t.Fatal(err)
	}
	if q := s.Scan(); len(q.Skipped) == 0 || q.Skipped[0].Reason != "вы отклонили это предложение" {
		t.Errorf("scan: %+v", q)
	}
	if q := s.Check([]string{"claude.ai"}, true); len(q.Queued) != 1 {
		t.Errorf("explicit check must redo it: %+v", q)
	}
	if err := s.Dismiss("claude.ai"); !errors.Is(err, ErrNotFound) {
		t.Errorf("can't dismiss what is still being checked: %v", err)
	}
}

func TestScanSource(t *testing.T) {
	s, _, _ := newService(t, newFake(), &fakeRouter{}, false)
	q := s.Scan()
	if len(q.Queued) != len(Catalog) {
		t.Fatalf("scan: %+v", q)
	}
	drain(s)
	if it := item(t, s, "udemy.com"); it.Source != SourceScan {
		t.Errorf("source %q", it.Source)
	}
}

func TestAcceptWithoutPlane(t *testing.T) {
	p := newFake()
	p.set("chatgpt.com", probe.ViaDirect, geoRefused())
	p.set("chatgpt.com", probe.ViaVless, opened(400, 0))
	s, _, _ := newService(t, p, nil, false)
	s.Check([]string{"chatgpt.com"}, true)
	drain(s)
	if err := s.Accept("chatgpt.com", ""); !errors.Is(err, ErrNoPlane) {
		t.Errorf("got %v", err)
	}
}

func TestQueueIsBounded(t *testing.T) {
	s, _, _ := newService(t, newFake(), &fakeRouter{}, false)
	var names []string
	for i := 0; i < maxQueue+5; i++ {
		names = append(names, string(rune('a'+i%26))+string(rune('a'+i/26))+".example.com")
	}
	q := s.Check(names, true)
	if len(q.Queued) != maxQueue || len(q.Skipped) != 5 {
		t.Fatalf("queued %d skipped %d", len(q.Queued), len(q.Skipped))
	}
}

func TestSettings(t *testing.T) {
	s, st, _ := newService(t, newFake(), &fakeRouter{}, false)
	for _, bad := range []Settings{
		{TTFBMaxMs: 100, TTFBFactor: 3, KbpsShare: 30, Reference: "ya.ru"},
		{TTFBMaxMs: 5000, TTFBFactor: 0.5, KbpsShare: 30, Reference: "ya.ru"},
		{TTFBMaxMs: 5000, TTFBFactor: 3, KbpsMin: -1, Reference: "ya.ru"},
		{TTFBMaxMs: 5000, TTFBFactor: 3, KbpsShare: 101, Reference: "ya.ru"},
		{TTFBMaxMs: 5000, TTFBFactor: 3, KbpsShare: 30, Reference: "not a site"},
	} {
		if _, err := s.SetSettings(bad); !errors.Is(err, ErrBadSettings) {
			t.Errorf("%+v: want ErrBadSettings, got %v", bad, err)
		}
	}
	want := Settings{TTFBMaxMs: 3000, TTFBFactor: 2.5, KbpsMin: 1000, KbpsShare: 20, Reference: "example.org", Discover: false}
	got, err := s.SetSettings(Settings{TTFBMaxMs: 3000, TTFBFactor: 2.5, KbpsMin: 1000, KbpsShare: 20, Reference: "Example.ORG."})
	if err != nil || got != want {
		t.Fatalf("set: %+v %v", got, err)
	}
	if ov := s.Overview(); ov.Settings != want || ov.Reference.Domain != "example.org" || ov.Reference.At != 0 {
		t.Errorf("overview: %+v %+v", ov.Settings, ov.Reference)
	}
	if s2 := New(newFake(), st, nil, nil); s2.Settings() != want {
		t.Errorf("not kept: %+v", s2.Settings())
	}
}

// The reference is measured with the first check, then at most every 10
// minutes; a way is judged against it.
func TestReference(t *testing.T) {
	p := newFake()
	p.set("far.example", probe.ViaDirect, opened(900, 0)) // 100 ms reference: limit 3×100+500 = 800
	p.set("near.example", probe.ViaDirect, opened(700, 0))
	s, _, c := newService(t, p, &fakeRouter{}, false)
	s.Check([]string{"far.example", "near.example"}, true)
	drain(s)
	if n := len(p.walked("ya.ru")); n != 1 {
		t.Errorf("reference measured %d times, want 1", n)
	}
	if it := item(t, s, "far.example"); it.Verdict != "slow" || !strings.Contains(it.Reason, "эталон ya.ru") {
		t.Errorf("far: %+v", it)
	}
	if it := item(t, s, "near.example"); it.Verdict != "ok" {
		t.Errorf("near: %+v", it)
	}
	c.advance(11 * time.Minute)
	s.Check([]string{"near.example"}, true)
	drain(s)
	if n := len(p.walked("ya.ru")); n != 2 {
		t.Errorf("reference measured %d times after 11 min, want 2", n)
	}
}

// The agent stopping mid-check: the domain is dropped, not saved with a
// verdict the cut walk can't back.
func TestProcessStoppingDropsTheCheck(t *testing.T) {
	p := newFake()
	p.set("x.example", probe.ViaDirect, opened(200, 0))
	s, _, _ := newService(t, p, &fakeRouter{}, false)
	s.Check([]string{"x.example"}, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.process(ctx, <-s.queue)
	if ov := s.Overview(); len(ov.Items) != 0 || ov.Busy != 0 {
		t.Fatalf("%+v", ov)
	}
}

type slowRouter struct {
	*fakeRouter
	entered, gate chan struct{}
}

func (r slowRouter) AddAuto(m plane.Mode, d string) error {
	close(r.entered)
	<-r.gate
	return r.fakeRouter.AddAuto(m, d)
}

// While Accept writes the plane, a second Accept, a dismiss or a recheck of
// the same domain is turned away.
func TestAcceptIsHeldWhileWriting(t *testing.T) {
	p := newFake()
	p.set("x.example", probe.ViaDirect, sniCut())
	p.set("x.example", probe.ViaWarp, opened(300, 0))
	rt := slowRouter{&fakeRouter{}, make(chan struct{}), make(chan struct{})}
	s, _, _ := newService(t, p, rt, true)
	s.Check([]string{"x.example"}, true)
	drain(s)
	done := make(chan error)
	go func() { done <- s.Accept("x.example", "desync") }()
	<-rt.entered
	if err := s.Accept("x.example", "warp"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second accept: %v", err)
	}
	if err := s.Dismiss("x.example"); !errors.Is(err, ErrNotFound) {
		t.Errorf("dismiss: %v", err)
	}
	if q := s.Check([]string{"x.example"}, true); len(q.Queued) != 0 {
		t.Errorf("recheck: %+v", q)
	}
	close(rt.gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if it := item(t, s, "x.example"); it.State != StateAccepted || it.Accepted != "desync" || len(rt.added) != 1 {
		t.Fatalf("%+v added %v", it, rt.added)
	}
}

// Accepted under the build that kept no mode — maybe the alternative: the
// check reads it off the plane's lists.
func TestOldAcceptedReadsTheModeOffThePlane(t *testing.T) {
	st, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	old := file{Settings: DefaultSettings(), Items: []Item{{Domain: "x.example", State: StateAccepted, Source: SourceUser, Mode: "desync", Alt: "warp", Verdict: "sni-block"}}}
	if err := st.SaveJSON(stateKey, old); err != nil {
		t.Fatal(err)
	}
	p := newFake()
	p.set("x.example", probe.ViaWarp, opened(300, 0))
	rt := &fakeRouter{added: []string{"warp:x.example"}, status: plane.Status{Apply: true,
		Groups: []plane.Group{{Mode: plane.ModeWarp, Domains: []string{"x.example"}}}}}
	s := New(p, st, rt, func() bool { return true })
	c := &clock{time.Now()}
	s.now = c.now
	c.advance(verifyAfter)
	s.verifyDue(context.Background())
	if it := item(t, s, "x.example"); it.Accepted != "warp" || it.VerifyNote != "" {
		t.Fatalf("migrated: %+v", it)
	}
	c.advance(verifyAfter)
	s.verifyDue(context.Background())
	if it := item(t, s, "x.example"); !it.Verified || strings.Join(p.walked("x.example"), ",") != "warp" {
		t.Fatalf("verified: %+v walked %v", it, p.walked("x.example"))
	}
}

func TestAddressesAreNotChecked(t *testing.T) {
	s, _, _ := newService(t, newFake(), &fakeRouter{}, false)
	if q := s.Check([]string{"203.0.113.7"}, true); len(q.Queued) != 0 || len(q.Skipped) != 1 || !strings.Contains(q.Skipped[0].Reason, "адрес") {
		t.Fatalf("%+v", q)
	}
}

func TestQuiet(t *testing.T) {
	for _, c := range []struct {
		it    Item
		quiet bool
	}{
		{Item{Verdict: "ok"}, true},
		{Item{Verdict: "unknown"}, true},
		{Item{Verdict: "slow"}, true},
		{Item{Verdict: "slow", Mode: "desync"}, false},
		{Item{Verdict: "ip-block"}, false}, // no way around: it says what to install
		{Item{Verdict: "dns-block"}, false},
		{Item{Verdict: "geoblock", Mode: "vless"}, false},
	} {
		if got := quiet(c.it); got != c.quiet {
			t.Errorf("%+v: got %v", c.it, got)
		}
	}
}

// A ladder that ran out of its time proves nothing: no suggestion, a reason
// that says so.
func TestProcessOverBudget(t *testing.T) {
	defer func(b time.Duration) { probeBudget = b }(probeBudget)
	probeBudget = 50 * time.Millisecond
	p := newFake()
	p.set("x.example", probe.ViaDirect, sniCut())
	p.hang = map[probe.Via]bool{probe.ViaWarp: true}
	s, _, _ := newService(t, p, &fakeRouter{}, true)
	s.Check([]string{"x.example"}, true)
	drain(s)
	if it := item(t, s, "x.example"); it.State != StateReady || it.Verdict != "unknown" || it.Mode != "" || it.Alt != "" || !strings.Contains(it.Reason, "5 минут") {
		t.Fatalf("%+v", it)
	}
}
