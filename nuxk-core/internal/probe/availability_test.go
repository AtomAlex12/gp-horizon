package probe

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

func TestUnavailableDirectIsNotMeasured(t *testing.T) {
	calls := 0
	p := &Prober{
		LookupIP: func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("203.0.113.1")}, nil },
		Avail:    func(Via) string { return "socket setup unavailable" },
		Dial:     func(context.Context, Via, string) (net.Conn, error) { calls++; return nil, errors.New("must not dial") },
	}
	paths := p.Paths()
	if paths[0].Usable || !strings.Contains(paths[0].Why, "unavailable") {
		t.Errorf("unavailable direct is advertised as usable: %+v", paths[0])
	}
	r := p.Check(context.Background(), "example.com")
	if r.Direct.Skipped == "" || r.Direct.Tries != 0 || calls != 0 || len(r.Tunnels) != 0 {
		t.Errorf("unavailable direct was probed or compared: %+v; dial calls=%d", r, calls)
	}
}

func TestClassifySkippedDirectIsUnknown(t *testing.T) {
	r := Report{Direct: Result{Skipped: "socket setup unavailable"}, Tunnels: []Result{
		{Via: ViaWarp, TCP: true, TLS: true, Status: 200, Tries: 5, Opened: 5},
	}}
	s := Classify(r, Options{Desync: true})
	if s.Verdict != VerdictUnknown || s.Mode != "" || s.Alt != "" || s.Confirmed {
		t.Errorf("skipped direct became a routing recommendation: %+v", s)
	}
}
