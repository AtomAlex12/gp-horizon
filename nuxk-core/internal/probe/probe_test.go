package probe

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// site starts a TLS server answering every request with status/body.
func site(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	s.StartTLS()
	t.Cleanup(s.Close)
	return s
}

// resetListener cuts (RST) any connection whose first bytes contain needle —
// a TLS ClientHello carries the SNI in clear, which is what a DPI box reads.
type resetListener struct {
	net.Listener
	needle []byte
}

type resetConn struct {
	net.Conn
	needle []byte
	seen   bool
}

func (l resetListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	return resetConn{Conn: c, needle: l.needle}, err
}

func (c resetConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 && bytes.Contains(p[:n], c.needle) {
		if tc, ok := c.Conn.(*net.TCPConn); ok {
			_ = tc.SetLinger(0)
		}
		_ = c.Conn.Close()
		return 0, errors.New("cut")
	}
	return n, err
}

func sniCut(t *testing.T, needle string) *httptest.Server {
	t.Helper()
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	s.Listener = resetListener{Listener: s.Listener, needle: []byte(needle)}
	s.StartTLS()
	t.Cleanup(s.Close)
	return s
}

// prober sends every path to its own server (nil = connection refused) and
// pretends the tunnels exist unless a Skipped reason is given for them.
func prober(direct *httptest.Server, tunnels map[Via]*httptest.Server, skip map[Via]string) *Prober {
	return &Prober{
		Timeout:  2 * time.Second,
		LookupIP: func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("203.0.113.5")}, nil },
		Avail:    func(v Via) string { return skip[v] },
		Dial: func(ctx context.Context, via Via, addr string) (net.Conn, error) {
			s := direct
			if via != ViaDirect {
				s = tunnels[via]
			}
			if s == nil {
				return nil, errors.New("connection refused")
			}
			var d net.Dialer
			return d.DialContext(ctx, "tcp", s.Listener.Addr().String())
		},
	}
}

func TestOpensDirectNoTunnelProbe(t *testing.T) {
	p := prober(site(t, 200, "ok"), nil, nil)
	r := p.Check(context.Background(), "example.test")
	if !r.Direct.Clean() || len(r.Tunnels) != 0 {
		t.Fatalf("direct clean must stop the walk: %+v", r)
	}
	if s := Classify(r, Options{}); s.Verdict != VerdictOK || s.Mode != "" {
		t.Errorf("verdict %+v", s)
	}
}

func TestGeoBlockConfirmedByTunnel(t *testing.T) {
	deny := site(t, 403, `{"error":{"code":"unsupported_country_region_territory"}}`)
	open := site(t, 200, "welcome")
	p := prober(deny, map[Via]*httptest.Server{ViaVless: open}, nil)
	r := p.Check(context.Background(), "api.example.test")
	s := Classify(r, Options{Desync: true})
	if s.Verdict != VerdictGeo || s.Mode != "vless" || !s.Confirmed {
		t.Fatalf("want confirmed geoblock via vless, got %+v", s)
	}
	if len(r.Tunnels) != 1 {
		t.Errorf("must stop at the first working tunnel, tried %d", len(r.Tunnels))
	}
}

func TestGeoBlockFallsToWarpWhenVlessMissing(t *testing.T) {
	deny := site(t, 403, "This content is not available in your country")
	open := site(t, 200, "welcome")
	p := prober(deny, map[Via]*httptest.Server{ViaWarp: open}, map[Via]string{ViaVless: "интерфейс не задан"})
	s := Classify(p.Check(context.Background(), "x.test"), Options{})
	if s.Mode != "warp" || !s.Confirmed {
		t.Fatalf("got %+v", s)
	}
}

func TestExplicitGeoWithoutTunnelIsUnconfirmed(t *testing.T) {
	deny := site(t, 403, "Service is not available in your region")
	skip := map[Via]string{ViaVless: "нет", ViaWarp: "нет"}
	s := Classify(prober(deny, nil, skip).Check(context.Background(), "x.test"), Options{})
	if s.Verdict != VerdictGeo || s.Mode != "vless" || s.Confirmed {
		t.Fatalf("want unconfirmed vless suggestion, got %+v", s)
	}
}

// A bare 403 proves nothing by itself: without a tunnel to compare against it
// must not become a recommendation.
func TestBare403NeedsConfirmation(t *testing.T) {
	deny := site(t, 403, "Forbidden")
	skip := map[Via]string{ViaVless: "нет", ViaWarp: "нет"}
	s := Classify(prober(deny, nil, skip).Check(context.Background(), "x.test"), Options{})
	if s.Verdict != VerdictUnknown || s.Mode != "" {
		t.Fatalf("got %+v", s)
	}
	// ...but the same 403 that a tunnel gets past is a geoblock
	open := site(t, 200, "ok")
	s = Classify(prober(deny, map[Via]*httptest.Server{ViaVless: open}, nil).Check(context.Background(), "x.test"), Options{})
	if s.Verdict != VerdictGeo || s.Mode != "vless" || !s.Confirmed {
		t.Fatalf("got %+v", s)
	}
}

func TestSiteDownEverywhereIsNotARouteProblem(t *testing.T) {
	broken := site(t, 502, "bad gateway")
	s := Classify(prober(broken, map[Via]*httptest.Server{ViaVless: broken, ViaWarp: broken}, nil).Check(context.Background(), "x.test"), Options{})
	if s.Mode != "" {
		t.Fatalf("a failing site must not be routed: %+v", s)
	}
}

func TestSNIBlockSuggestsDesync(t *testing.T) {
	cut := sniCut(t, "blocked.test")
	open := site(t, 200, "ok")
	p := prober(cut, map[Via]*httptest.Server{ViaVless: open}, nil)
	r := p.Check(context.Background(), "blocked.test")
	if r.Direct.TLS || r.Direct.Neutral == nil || !*r.Direct.Neutral {
		t.Fatalf("want TLS cut with neutral name passing, got %+v", r.Direct)
	}
	if f := r.Direct.TLSFail; f != FailReset && f != FailEOF {
		t.Errorf("want reset/eof, got %q", f)
	}
	s := Classify(r, Options{Desync: true})
	if s.Verdict != VerdictSNI || s.Mode != "desync" || s.Confirmed || s.Alt != "vless" {
		t.Fatalf("got %+v", s)
	}
	// without nfqws2 the verified tunnel is the answer
	s = Classify(r, Options{})
	if s.Verdict != VerdictSNI || s.Mode != "vless" || !s.Confirmed {
		t.Fatalf("got %+v", s)
	}
}

func TestIPBlock(t *testing.T) {
	open := site(t, 200, "ok")
	p := prober(nil, map[Via]*httptest.Server{ViaVless: open}, nil)
	s := Classify(p.Check(context.Background(), "x.test"), Options{Desync: true})
	if s.Verdict != VerdictIP || s.Mode != "vless" || !s.Confirmed {
		t.Fatalf("got %+v", s)
	}
	p = prober(nil, nil, nil) // dead everywhere
	s = Classify(p.Check(context.Background(), "x.test"), Options{})
	if s.Verdict != VerdictUnknown || s.Mode != "" {
		t.Fatalf("got %+v", s)
	}
}

func TestBogusDNS(t *testing.T) {
	p := prober(nil, nil, nil)
	p.LookupIP = func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("10.0.0.1")}, nil }
	r := p.Check(context.Background(), "x.test")
	if s := Classify(r, Options{}); r.DNS != "bogus" || s.Verdict != VerdictDNS || s.Mode != "" {
		t.Fatalf("dns=%q %+v", r.DNS, s)
	}
	if r := p.Check(context.Background(), "1.2.3.4"); r.DNS != "bogus" {
		t.Errorf("an address is not a name to probe, dns=%q", r.DNS)
	}
}

func TestGeoSignal(t *testing.T) {
	cf := http.Header{"Cf-Mitigated": {"challenge"}}
	for _, c := range []struct {
		status int
		h      http.Header
		body   string
		want   Signal
	}{
		{200, nil, "not available in your country", SignalNone}, // a 200 page is not a refusal
		{403, nil, "Error 1009 Access denied", SignalExplicit},
		{451, nil, "", SignalExplicit},
		{403, nil, "<title>Just a moment...</title>", SignalChallenge},
		{403, cf, "", SignalChallenge},
		{403, nil, "Not Available", SignalDenied},
		{500, nil, "oops", SignalNone},
		{404, nil, "nope", SignalNone},
	} {
		if got := geoSignal(c.status, c.h, c.body); got != c.want {
			t.Errorf("geoSignal(%d,%q) = %q, want %q", c.status, c.body, got, c.want)
		}
	}
}
