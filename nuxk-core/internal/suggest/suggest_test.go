package suggest

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nuxk.dev/horizon/core/internal/plane"
	"nuxk.dev/horizon/core/internal/probe"
	"nuxk.dev/horizon/core/internal/state"
)

type fakeRouter struct {
	covered map[string]string
	added   []string
	err     error
}

func (f *fakeRouter) Covered(d string) string { return f.covered[d] }
func (f *fakeRouter) AddAuto(m plane.Mode, d string) error {
	if f.err != nil {
		return f.err
	}
	f.added = append(f.added, string(m)+":"+d)
	return nil
}

func tlsSite(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s
}

// newService: direct is geo-blocked, vless opens the site.
func newService(t *testing.T, rt Router) (*Service, *state.Store) {
	t.Helper()
	deny := tlsSite(t, 403, "unsupported_country_region_territory")
	open := tlsSite(t, 200, "ok")
	p := &probe.Prober{
		Timeout:  2 * time.Second,
		LookupIP: func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("203.0.113.9")}, nil },
		Avail: func(v probe.Via) string {
			if v == probe.ViaWarp {
				return "выключен"
			}
			return ""
		},
		Dial: func(ctx context.Context, via probe.Via, _ string) (net.Conn, error) {
			s := deny
			if via == probe.ViaVless {
				s = open
			}
			var d net.Dialer
			return d.DialContext(ctx, "tcp", s.Listener.Addr().String())
		},
	}
	st, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return New(p, st, rt, func() bool { return true }), st
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

func TestCheckAcceptFlow(t *testing.T) {
	rt := &fakeRouter{covered: map[string]string{"mine.com": "список «Мои»"}}
	s, st := newService(t, rt)

	q := s.Check([]string{"ChatGPT.com", "mine.com", "not a host"}, true)
	if len(q.Queued) != 1 || q.Queued[0] != "chatgpt.com" || len(q.Skipped) != 2 {
		t.Fatalf("queued/skipped wrong: %+v", q)
	}
	if ov := s.Overview(); ov.Busy != 1 || ov.Items[0].State != StateChecking {
		t.Fatalf("while queued: %+v", ov)
	}
	drain(s)

	ov := s.Overview()
	if ov.Busy != 0 || len(ov.Items) != 1 {
		t.Fatalf("after: %+v", ov)
	}
	it := ov.Items[0]
	if it.State != StateReady || it.Verdict != "geoblock" || it.Mode != "vless" || !it.Confirmed || len(it.Paths) != 2 {
		t.Fatalf("item: %+v", it)
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
	if err := s.Accept("chatgpt.com", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("second accept: %v", err)
	}

	// persisted: a fresh service sees the accepted item and keeps it out of scans
	s2 := New(s.P, st, rt, nil)
	if got := s2.Overview().Items; len(got) != 1 || got[0].State != StateAccepted {
		t.Fatalf("reloaded: %+v", got)
	}
	if q := s2.Check([]string{"chatgpt.com"}, false); len(q.Queued) != 0 {
		t.Errorf("a scan must not redo an accepted domain: %+v", q)
	}
}

func TestDismissSticksForScansNotForExplicitChecks(t *testing.T) {
	s, _ := newService(t, &fakeRouter{})
	s.Check([]string{"claude.ai"}, true)
	drain(s)
	if err := s.Dismiss("claude.ai"); err != nil {
		t.Fatal(err)
	}
	if q := s.Check([]string{"claude.ai"}, false); len(q.Queued) != 0 || len(q.Skipped) != 1 {
		t.Errorf("scan: %+v", q)
	}
	if q := s.Check([]string{"claude.ai"}, true); len(q.Queued) != 1 {
		t.Errorf("explicit check must redo it: %+v", q)
	}
	if err := s.Dismiss("claude.ai"); !errors.Is(err, ErrNotFound) {
		t.Errorf("can't dismiss what is still being checked: %v", err)
	}
}

func TestAcceptWithoutPlane(t *testing.T) {
	s, _ := newService(t, nil)
	s.Check([]string{"chatgpt.com"}, true)
	drain(s)
	if err := s.Accept("chatgpt.com", ""); !errors.Is(err, ErrNoPlane) {
		t.Errorf("got %v", err)
	}
}

func TestQueueIsBounded(t *testing.T) {
	s, _ := newService(t, &fakeRouter{})
	var names []string
	for i := 0; i < maxQueue+5; i++ {
		names = append(names, string(rune('a'+i%26))+string(rune('a'+i/26))+".example.com")
	}
	q := s.Check(names, true)
	if len(q.Queued) != maxQueue || len(q.Skipped) != 5 {
		t.Fatalf("queued %d skipped %d", len(q.Queued), len(q.Skipped))
	}
}
