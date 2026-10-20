package probe

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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

var testIP = net.ParseIP("203.0.113.5")

// wayProber sends every way to srv with short stages. Dials for which fail(n)
// is true (n counts from 1) are refused, as on a flaky or blocked path. The
// returned slice records the Via of every dial.
func wayProber(srv *httptest.Server, fail func(n int) bool) (*Prober, *[]Via) {
	var vias []Via
	return &Prober{
		Timeout: 500 * time.Millisecond,
		Dial: func(ctx context.Context, via Via, addr string) (net.Conn, error) {
			vias = append(vias, via)
			if fail != nil && fail(len(vias)) {
				return nil, errors.New("connection refused")
			}
			var d net.Dialer
			return d.DialContext(ctx, "tcp", srv.Listener.Addr().String())
		},
	}, &vias
}

func TestWayStopsOnceItCannotBeClean(t *testing.T) {
	ok := site(t, 200, "ok")
	p, vias := wayProber(ok, func(int) bool { return true })
	r := p.Way(context.Background(), "x.test", testIP, ViaDirect)
	if r.Tries != 2 || len(*vias) != 2 || r.Opened != 0 || r.Clean() {
		t.Fatalf("a dead way must cost two tries of five: tries=%d dials=%d %+v", r.Tries, len(*vias), r)
	}
	if r.TCP || r.TCPFail != FailRefused {
		t.Errorf("first try: %+v", r)
	}
	p, _ = wayProber(ok, func(int) bool { return true })
	p.Samples = 3 // 80% of 3 is all 3: the first miss ends it
	if r := p.Way(context.Background(), "x.test", testIP, ViaDirect); r.Tries != 1 {
		t.Errorf("3 samples: tries=%d", r.Tries)
	}
}

func TestWayCleanNeedsFourInFive(t *testing.T) {
	ok := site(t, 200, "ok")
	for _, c := range []struct {
		name          string
		fail          func(int) bool
		opened, tries int
		clean, tcp    bool
	}{
		{"one miss", func(n int) bool { return n == 2 }, 4, 5, true, true},
		{"first try missed", func(n int) bool { return n == 1 }, 4, 5, true, false},
		{"two misses", func(n int) bool { return n >= 4 }, 3, 5, false, true},
		{"two misses early", func(n int) bool { return n <= 2 }, 0, 2, false, false},
	} {
		p, _ := wayProber(ok, c.fail)
		p.NoFreeze = true
		r := p.Way(context.Background(), "x.test", testIP, ViaDirect)
		if r.Opened != c.opened || r.Tries != c.tries || r.Clean() != c.clean || r.TCP != c.tcp {
			t.Errorf("%s: opened %d/%d clean=%v tcp=%v", c.name, r.Opened, r.Tries, r.Clean(), r.TCP)
		}
	}
}

func TestWayMetrics(t *testing.T) {
	slow := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Millisecond)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(slow.Close)
	p, _ := wayProber(slow, nil)
	r := p.Way(context.Background(), "x.test", testIP, ViaDirect)
	if !r.Clean() || r.Tries != 5 || r.Opened != 5 || r.Status != 200 {
		t.Fatalf("%+v", r)
	}
	if r.TTFBMs < 30 || r.Ms < r.TTFBMs {
		t.Errorf("ttfb=%dms whole=%dms", r.TTFBMs, r.Ms)
	}
	if r.Bytes != 2 || r.Kbps != 0 {
		t.Errorf("a 2-byte page says nothing about speed: bytes=%d kbps=%d", r.Bytes, r.Kbps)
	}

	p, _ = wayProber(site(t, 200, strings.Repeat("x", 300<<10)), nil)
	r = p.Way(context.Background(), "x.test", testIP, ViaDirect)
	if !r.Clean() || r.Bytes != maxBody || r.Kbps <= 0 {
		t.Errorf("big page: bytes=%d kbps=%d %+v", r.Bytes, r.Kbps, r)
	}
}

func TestMedian(t *testing.T) {
	if m := median([]time.Duration{5, 1, 3}); m != 3 {
		t.Errorf("odd: %v", m)
	}
	if m := median([]int{4, 1, 3, 2}); m != 2 { // (2+3)/2
		t.Errorf("even: %v", m)
	}
	if m := median([]int(nil)); m != 0 {
		t.Errorf("empty: %v", m)
	}
}

// freezeListener lets each connection's first limit bytes from the client
// through and then reads no more — the DPI freeze, past ~16 KB nothing arrives.
type freezeListener struct {
	net.Listener
	limit int64
}

type freezeConn struct {
	net.Conn
	left   atomic.Int64
	once   sync.Once
	closed chan struct{}
}

func (l freezeListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	fc := &freezeConn{Conn: c, closed: make(chan struct{})}
	fc.left.Store(l.limit)
	return fc, nil
}

func (c *freezeConn) Read(p []byte) (int, error) {
	left := c.left.Load()
	if left <= 0 {
		<-c.closed
		return 0, net.ErrClosed
	}
	n, err := c.Conn.Read(p[:min(int64(len(p)), left)])
	c.left.Add(-int64(n))
	return n, err
}

func (c *freezeConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

// echo answers GET with "ok" and reads a POST through before answering it.
var echo = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	_, _ = w.Write([]byte("ok"))
})

func TestFreezeOnUpload(t *testing.T) {
	s := httptest.NewUnstartedServer(echo)
	s.Listener = freezeListener{Listener: s.Listener, limit: 16 << 10}
	s.StartTLS()
	t.Cleanup(func() { s.CloseClientConnections(); s.Close() }) // a frozen handler waits for its conn to close

	p, _ := wayProber(s, nil)
	r := p.Way(context.Background(), "x.test", testIP, ViaDirect)
	if r.Opened != 5 || !r.Frozen || r.Clean() {
		t.Fatalf("plain GETs pass, the upload hangs: want frozen, got %+v", r)
	}
	p.NoFreeze = true
	if r := p.Way(context.Background(), "x.test", testIP, ViaDirect); r.Frozen || !r.Clean() {
		t.Errorf("NoFreeze must skip the upload test: %+v", r)
	}
}

func TestRefusedUploadIsNotFrozen(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			http.Error(w, "no", http.StatusMethodNotAllowed)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(s.Close)
	p, _ := wayProber(s, nil)
	if r := p.Way(context.Background(), "x.test", testIP, ViaDirect); r.Frozen || !r.Clean() {
		t.Fatalf("a 405 to the upload is an answer, not a freeze: %+v", r)
	}
}

func TestFreezeOnDownload(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), 20<<10))
		w.(http.Flusher).Flush()
		select { // the rest never comes
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(s.Close)
	p, _ := wayProber(s, nil)
	p.NoFreeze = true // the page alone shows it
	r := p.Way(context.Background(), "x.test", testIP, ViaDirect)
	if !r.Frozen || r.Clean() || r.Tries != 1 || r.Bytes != 20<<10 || r.Status != 200 {
		t.Fatalf("a page stopped mid-way is the freeze, and one is enough: %+v", r)
	}
}

func TestDirectAndDesyncAreDistinctWays(t *testing.T) {
	p, vias := wayProber(site(t, 200, "ok"), nil)
	p.Samples, p.NoFreeze = 1, true
	p.Avail = func(Via) string { return "нет" } // no tunnel is up; direct and desync need none
	ctx := context.Background()
	d := p.Way(ctx, "x.test", testIP, ViaDirect)
	s := p.Way(ctx, "x.test", testIP, ViaDesync)
	v := p.Way(ctx, "x.test", testIP, ViaVless)
	if !d.Clean() || !s.Clean() || s.Via != ViaDesync || v.Skipped == "" {
		t.Fatalf("direct %+v\ndesync %+v\nvless %+v", d, s, v)
	}
	if want := []Via{ViaDirect, ViaDesync}; !slices.Equal(*vias, want) {
		t.Errorf("dials %v, want %v", *vias, want)
	}
}
