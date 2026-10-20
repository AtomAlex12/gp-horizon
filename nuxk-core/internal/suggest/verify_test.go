package suggest

import (
	"context"
	"strings"
	"testing"
	"time"

	"nuxk.dev/horizon/core/internal/plane"
	"nuxk.dev/horizon/core/internal/probe"
)

// applying: the plane in apply mode with nfqws2's list and the tunnel groups
// carrying domain.
func applying(domain string) plane.Status {
	return plane.Status{
		Apply: true, DesyncOn: true, DesyncOK: true, Desync: []string{domain},
		Groups: []plane.Group{{Mode: plane.ModeWarp, Domains: []string{domain}}, {Mode: plane.ModeVless, Domains: []string{domain}}},
	}
}

// accepted: x.example suggested (SNI cut, DPI with WARP as the alternative)
// and accepted as mode.
func accepted(t *testing.T, mode string) (*Service, *fakeProber, *fakeRouter, *clock) {
	t.Helper()
	p := newFake()
	p.set("x.example", probe.ViaDirect, sniCut())
	p.set("x.example", probe.ViaWarp, opened(300, 0))
	rt := &fakeRouter{status: applying("x.example")}
	s, _, c := newService(t, p, rt, true)
	s.Check([]string{"x.example"}, true)
	drain(s)
	if it := item(t, s, "x.example"); it.Mode != "desync" || it.Alt != "warp" {
		t.Fatalf("suggestion: %+v", it)
	}
	if err := s.Accept("x.example", mode); err != nil {
		t.Fatal(err)
	}
	p.reset()
	return s, p, rt, c
}

func TestVerifyDesyncWorks(t *testing.T) {
	s, p, _, c := accepted(t, "desync")
	p.set("x.example", probe.ViaDesync, opened(400, 0))
	c.advance(time.Minute)
	s.verifyDue(context.Background())
	if len(p.calls) != 0 {
		t.Fatalf("measured before the plane had time: %v", p.calls)
	}
	c.advance(verifyAfter)
	s.verifyDue(context.Background())
	it := item(t, s, "x.example")
	if !it.Verified || it.VerifiedAt != c.t.Unix() || it.VerifyDue != c.t.Add(reverifyEvery).Unix() || it.Next != "" || !strings.Contains(it.VerifyNote, "DPI") {
		t.Fatalf("after verify: %+v", it)
	}
	if got := p.walked("x.example"); len(got) != 1 || got[0] != "desync" {
		t.Errorf("walked %v", got)
	}
	if it.Paths[len(it.Paths)-1].Via != "desync" || !it.Paths[len(it.Paths)-1].Acceptable {
		t.Errorf("the check's measurement must be in the paths: %+v", it.Paths)
	}

	// daily: measured again a day later, not before
	p.reset()
	c.advance(time.Hour)
	s.verifyDue(context.Background())
	if len(p.calls) != 0 {
		t.Fatalf("re-measured too soon: %v", p.calls)
	}
	p.set("x.example", probe.ViaDesync, sniCut()) // the strategy stopped working
	c.advance(reverifyEvery)
	s.verifyDue(context.Background())
	if it := item(t, s, "x.example"); it.Verified || it.Next != NextSearch || it.Interim != "warp" {
		t.Fatalf("a day later: %+v", it)
	}
}

func TestVerifyDesyncFailsAdvisesSearch(t *testing.T) {
	s, p, _, c := accepted(t, "desync")
	p.set("x.example", probe.ViaDesync, sniCut())
	c.advance(verifyAfter)
	s.verifyDue(context.Background())
	it := item(t, s, "x.example")
	if it.Verified || it.VerifiedAt == 0 || it.Next != NextSearch || it.Interim != "warp" || !strings.Contains(it.VerifyNote, "подбор стратегии") {
		t.Fatalf("%+v", it)
	}
	if got := p.walked("x.example"); strings.Join(got, ",") != "desync,warp" {
		t.Errorf("walked %v", got)
	}
	if it.State != StateAccepted {
		t.Errorf("a failed check changes nothing: %+v", it)
	}
}

func TestVerifyDesyncFailsNoInterim(t *testing.T) {
	s, p, _, c := accepted(t, "desync")
	p.set("x.example", probe.ViaDesync, sniCut())
	p.set("x.example", probe.ViaWarp, refused())
	c.advance(verifyAfter)
	s.verifyDue(context.Background())
	if it := item(t, s, "x.example"); it.Next != NextSearch || it.Interim != "" {
		t.Fatalf("%+v", it)
	}
}

func TestVerifyTunnelFails(t *testing.T) {
	s, p, _, c := accepted(t, "warp")
	p.set("x.example", probe.ViaWarp, refused())
	c.advance(verifyAfter)
	s.verifyDue(context.Background())
	it := item(t, s, "x.example")
	if it.Verified || it.Next != "" || !strings.Contains(it.VerifyNote, "WARP") {
		t.Fatalf("%+v", it)
	}
	if got := p.walked("x.example"); strings.Join(got, ",") != "warp" {
		t.Errorf("walked %v", got)
	}
}

func TestVerifyWaitsForThePlane(t *testing.T) {
	s, p, rt, c := accepted(t, "desync")
	rt.status.Apply = false
	c.advance(verifyAfter)
	s.verifyDue(context.Background())
	it := item(t, s, "x.example")
	if len(p.calls) != 0 || it.VerifiedAt != 0 || it.VerifyDue != c.t.Add(verifyWait).Unix() || !strings.Contains(it.VerifyNote, "PLANE_APPLY") {
		t.Fatalf("plan mode: %+v calls %v", it, p.calls)
	}
	rt.status = applying("x.example")
	rt.status.DesyncOK = false // nfqws2 hasn't got the list yet
	c.advance(verifyWait)
	s.verifyDue(context.Background())
	if it := item(t, s, "x.example"); len(p.calls) != 0 || !strings.Contains(it.VerifyNote, "nfqws2") {
		t.Fatalf("list not pushed: %+v", it)
	}
	rt.added = nil // taken out of the lists by hand
	c.advance(verifyWait)
	s.verifyDue(context.Background())
	if it := item(t, s, "x.example"); it.VerifyDue != 0 || len(p.calls) != 0 {
		t.Fatalf("removed: %+v", it)
	}
}

func TestDismissedNeverVerified(t *testing.T) {
	p := newFake()
	p.set("x.example", probe.ViaDirect, sniCut())
	p.set("x.example", probe.ViaWarp, opened(300, 0))
	s, _, c := newService(t, p, &fakeRouter{status: applying("x.example")}, true)
	s.Check([]string{"x.example"}, true)
	drain(s)
	if err := s.Dismiss("x.example"); err != nil {
		t.Fatal(err)
	}
	p.reset()
	c.advance(48 * time.Hour)
	s.verifyDue(context.Background())
	s.discover()
	if len(p.calls) != 0 {
		t.Fatalf("a dismissed item was measured: %v", p.calls)
	}
}
