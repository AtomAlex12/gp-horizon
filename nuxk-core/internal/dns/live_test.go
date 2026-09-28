package dns

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLive asks the real resolvers: NUXK_DNS_LIVE=1, and NUXK_DNS_LIVE_IFACE
// names a tunnel's interface to go through (the WARP stand on the Pi). Off by
// default — it needs the internet.
func TestLive(t *testing.T) {
	if os.Getenv("NUXK_DNS_LIVE") == "" {
		t.Skip("NUXK_DNS_LIVE not set")
	}
	iface := os.Getenv("NUXK_DNS_LIVE_IFACE")
	s := New(Options{Listen: "127.0.0.1:0", Paths: func() []Path {
		if iface == "" {
			return nil
		}
		return []Path{{Name: "warp", Iface: iface}}
	}}, nil)
	for _, via := range []string{ViaAuto, ViaDirect} {
		s.set.Via = via
		for _, r := range Catalog {
			s.set.Resolvers = []string{r.ID}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			t0 := time.Now()
			a, err := s.lookup(ctx, "cloudflare.com")
			cancel()
			if err != nil || len(a.Addrs) == 0 {
				// straight to a resolver may be blocked by the provider: that's
				// what the tunnel is for — only the tunnel must always answer
				if via == ViaDirect || iface == "" {
					t.Logf("%-10s via %-6s blocked: %v", r.Name, via, err)
				} else {
					t.Errorf("%s via %s: %v %+v", r.Name, via, err, a)
				}
				continue
			}
			t.Logf("%-10s via %-6s path %-6s %v %s", r.Name, via, s.lastPath, a.Addrs, time.Since(t0).Round(time.Millisecond))
			want := PathDirect
			if via == ViaAuto && iface != "" {
				want = "warp"
			}
			if s.lastPath != want {
				t.Errorf("%s via %s went %s", r.Name, via, s.lastPath)
			}
		}
	}
	s.set = s.normal(Settings{Via: ViaAuto})
	if rd := os.Getenv("NUXK_DNS_LIVE_ROUTER"); rd != "" {
		s.o.RouterDNS = rd
	}
	c := s.Check(context.Background(), nil)
	t.Logf("check: reference via %s; router spoofed %d, plain spoofed %d", c.Path, c.Router, c.Plain)
	for _, it := range c.Items {
		t.Logf("  %-14s router %-8s %v %s | plain %-8s %v %s | truth %v", it.Domain,
			it.RouterVerdict, it.Router, it.RouterNote, it.PlainVerdict, it.Plain, it.PlainNote, it.Truth)
	}
}
