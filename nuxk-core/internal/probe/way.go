package probe

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"slices"
	"time"
)

const (
	maxBody     = 256 << 10 // body bytes read per try
	headBody    = 16 << 10  // body bytes kept for geoSignal
	speedBody   = 128 << 10 // a try this big says something about speed
	freezeBytes = 64 << 10  // the freeze test's upload: well past the ~16 KB the DPI lets through
)

// Way probes one way out to ip: up to Samples tries one after another — in
// parallel, many handshakes for one name can trip the DPI by themselves —
// then one freeze test. It stops as soon as the way can no longer come out
// clean, so a blocked site costs about two tries, not five.
func (p *Prober) Way(ctx context.Context, domain string, ip net.IP, via Via) Result {
	r := Result{Via: via, Iface: p.Ifaces[via]}
	if tunnel(via) {
		if why := p.unavailable(via); why != "" {
			r.Skipped = why
			return r
		}
	}
	addr := net.JoinHostPort(ip.String(), "443")
	n := p.samples()
	need := (4*n + 4) / 5 // ceil(0.8n): what Clean asks for
	var first sample
	var ok []sample
	for i := 0; i < n && r.Opened+n-i >= need && !r.Frozen && ctx.Err() == nil; i++ {
		s := p.sample(ctx, domain, addr, via)
		r.Tries++
		if i == 0 {
			first = s
			r.TCP, r.TCPFail, r.TLS, r.TLSFail = s.tcp, s.tcpFail, s.tls, s.tlsFail
			r.Status, r.HTTPFail, r.Signal = s.status, s.httpFail, s.signal
			if s.tcp && !s.tls {
				nb := p.neutral(ctx, via, addr)
				r.Neutral = &nb
			}
		}
		r.Bytes = max(r.Bytes, s.bytes)
		if s.opened() {
			r.Opened++
			ok = append(ok, s)
			// a page that stopped mid-way is the freeze already; more tries
			// would each wait it out again
			r.Frozen = r.Frozen || s.stalled
		}
	}
	if r.Opened > 0 && !r.Frozen && !p.NoFreeze && ctx.Err() == nil {
		r.Frozen = p.frozen(ctx, domain, addr, via)
	}

	if len(ok) == 0 {
		r.Ms = ms(first.total)
		return r
	}
	var connect, hs, ttfb, total []time.Duration
	var speed []int
	for _, s := range ok {
		connect, hs, ttfb, total = append(connect, s.connect), append(hs, s.handshake), append(ttfb, s.ttfb), append(total, s.total)
		if s.kbps > 0 {
			speed = append(speed, s.kbps)
		}
	}
	r.ConnectMs, r.TLSMs, r.TTFBMs, r.Ms = ms(median(connect)), ms(median(hs)), ms(median(ttfb)), ms(median(total))
	r.Kbps = median(speed)
	return r
}

// sample is one try.
type sample struct {
	tcp      bool
	tcpFail  Fail
	tls      bool
	tlsFail  Fail
	status   int
	httpFail Fail
	signal   Signal

	connect, handshake, ttfb, total time.Duration

	bytes   int64
	kbps    int  // 0 = not measurable
	stalled bool // the body stopped coming mid-way
}

func (s sample) opened() bool {
	return s.tls && s.status > 0 && s.status < 500 && s.signal == SignalNone
}

// sample is one try: dial, TLS, GET /, read the page.
func (p *Prober) sample(ctx context.Context, domain, addr string, via Via) (s sample) {
	start := time.Now()
	defer func() { s.total = time.Since(start) }()

	dctx, cancel := context.WithTimeout(ctx, p.stage())
	conn, err := p.dial(dctx, via, addr)
	cancel()
	if err != nil {
		s.tcpFail = failKind(err)
		return s
	}
	defer conn.Close()
	s.tcp, s.connect = true, time.Since(start)
	// conn deadlines bound the HTTP part; a cancelled ctx cuts it too
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()

	tc, took, err := p.handshake(ctx, conn, domain)
	s.handshake = took
	if err != nil {
		s.tlsFail = failKind(err)
		return s
	}
	s.tls = true

	_ = conn.SetDeadline(time.Now().Add(p.stage()))
	if ctx.Err() != nil { // checked after the deadline is set, so a cancel can't be overwritten
		s.httpFail = FailTimeout
		return s
	}
	req, err := request(http.MethodGet, domain, nil)
	if err != nil {
		s.httpFail = FailOther
		return s
	}
	in := &stamp{r: tc}
	sent := time.Now()
	if err := req.Write(tc); err != nil {
		s.httpFail = failKind(err)
		return s
	}
	resp, err := http.ReadResponse(bufio.NewReader(in), req)
	if err != nil {
		s.httpFail = failKind(err)
		return s
	}
	// resp.Body is left unclosed on purpose: Close would drain the rest of a
	// big page. Closing conn ends it.
	s.ttfb = in.at.Sub(sent)
	s.status = resp.StatusCode
	head, n, took, end := p.readBody(ctx, conn, resp.Body)
	s.bytes = n
	s.signal = geoSignal(resp.StatusCode, resp.Header, string(head))
	s.stalled = end == bodyStalled
	if n >= speedBody || end == bodySlow && n > 0 {
		s.kbps = kbps(n, took)
	}
	return s
}

// handshake is TLS with the real SNI. Certificates are not checked: the
// question is whether the handshake and the answer get through, not who
// signed them — and the router's Entware CA store is not always where Go
// looks for it.
func (p *Prober) handshake(ctx context.Context, conn net.Conn, domain string) (*tls.Conn, time.Duration, error) {
	tc := tls.Client(conn, &tls.Config{ServerName: domain, InsecureSkipVerify: true, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}})
	hctx, cancel := context.WithTimeout(ctx, p.stage())
	defer cancel()
	start := time.Now()
	err := tc.HandshakeContext(hctx)
	return tc, time.Since(start), err
}

// request is what a browser would send, minus cookies. Identity encoding, so
// the byte counts are the page's own.
func request(method, domain string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, "https://"+domain+"/", body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Connection", "close")
	return req, nil
}

// How a body read ended.
type bodyEnd int

const (
	bodyDone    bodyEnd = iota // to the end, to the cap, or cut by an error
	bodySlow                   // still coming when the stage ran out
	bodyStalled                // nothing came for half a stage: the freeze
)

// readBody reads up to maxBody of a page and keeps its first headBody bytes.
// The whole body gets one stage and every read half of one, which tells a
// page that trickles in from one that stopped dead.
func (p *Prober) readBody(ctx context.Context, conn net.Conn, body io.Reader) (head []byte, n int64, took time.Duration, end bodyEnd) {
	start := time.Now()
	stop := start.Add(p.stage())
	buf := make([]byte, 16<<10)
	for n < maxBody {
		dl := time.Now().Add(p.stage() / 2)
		last := !dl.Before(stop)
		if last {
			dl = stop
		}
		_ = conn.SetReadDeadline(dl)
		if ctx.Err() != nil { // checked after the deadline is set, so a cancel can't be overwritten
			break
		}
		k, err := body.Read(buf[:min(int64(len(buf)), maxBody-n)])
		if room := headBody - len(head); room > 0 {
			head = append(head, buf[:min(k, room)]...)
		}
		n += int64(k)
		if err != nil {
			if timedOut(err, dl) && ctx.Err() == nil {
				end = bodyStalled
				if last {
					end = bodySlow
				}
			}
			break
		}
	}
	return head, n, time.Since(start), end
}

// frozen is the freeze test: a 64 KB upload over a fresh connection, then a
// wait for any answer. The DPI that freezes lets the handshake and the first
// ~16 KB through and then silently drops the rest, so silence — while writing
// or while waiting — is the freeze. Any answer means the bytes flowed, even a
// refusal or a reset: a server is free to turn a POST to / away.
func (p *Prober) frozen(ctx context.Context, domain, addr string, via Via) bool {
	dctx, cancel := context.WithTimeout(ctx, p.stage())
	conn, err := p.dial(dctx, via, addr)
	cancel()
	if err != nil {
		return false // the tries speak for connecting; this test is about what comes after
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()
	tc, _, err := p.handshake(ctx, conn, domain)
	if err != nil {
		return false
	}
	payload := make([]byte, freezeBytes)
	_, _ = rand.Read(payload) // random, so nothing on the way can squeeze it
	req, err := request(http.MethodPost, domain, bytes.NewReader(payload))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	// The answer is read while the upload goes: a server may refuse early,
	// and that is an answer too.
	wdl := time.Now().Add(p.stage())
	_ = conn.SetWriteDeadline(wdl)
	_ = conn.SetReadDeadline(wdl.Add(p.stage()))
	got := make(chan error, 1)
	go func() {
		_, err := http.ReadResponse(bufio.NewReader(tc), req)
		got <- err
	}()
	// net/http wraps a failed body write so it no longer says "timeout";
	// the deadline says it instead.
	if err := req.Write(tc); timedOut(err, wdl) {
		_ = conn.SetReadDeadline(time.Now()) // the upload stalled: only an answer already in hand counts
	} else {
		_ = conn.SetReadDeadline(time.Now().Add(p.stage()))
	}
	if ctx.Err() != nil {
		_ = conn.SetReadDeadline(time.Now())
	}
	rerr := <-got
	return ctx.Err() == nil && failKind(rerr) == FailTimeout
}

// neutral reports whether ip completes (or answers with an alert to) a TLS
// handshake for an unrelated name — i.e. the address itself is not cut.
func (p *Prober) neutral(ctx context.Context, via Via, addr string) bool {
	dctx, cancel := context.WithTimeout(ctx, p.stage())
	defer cancel()
	conn, err := p.dial(dctx, via, addr)
	if err != nil {
		return false
	}
	defer conn.Close()
	sni := p.Neutral
	if sni == "" {
		sni = "www.google.com"
	}
	tc := tls.Client(conn, &tls.Config{ServerName: sni, InsecureSkipVerify: true, MinVersion: tls.VersionTLS12})
	err = tc.HandshakeContext(dctx)
	// A server that doesn't know the name and says so is still a server we reached.
	return err == nil || failKind(err) == FailAlert
}

// stamp remembers when the first byte arrived.
type stamp struct {
	r  io.Reader
	at time.Time
}

func (s *stamp) Read(b []byte) (int, error) {
	n, err := s.r.Read(b)
	if n > 0 && s.at.IsZero() {
		s.at = time.Now()
	}
	return n, err
}

// timedOut: err came from hitting deadline (some layers wrap the timeout so
// it no longer says so).
func timedOut(err error, deadline time.Time) bool {
	return err != nil && (failKind(err) == FailTimeout || !time.Now().Before(deadline))
}

func median[T ~int | ~int64](v []T) T {
	if len(v) == 0 {
		return 0
	}
	s := slices.Clone(v)
	slices.Sort(s)
	m := len(s) / 2
	if len(s)%2 == 1 {
		return s[m]
	}
	return (s[m-1] + s[m]) / 2
}

func ms(d time.Duration) int { return int(d / time.Millisecond) }

// kbps is n bytes over d, in kilobits per second.
func kbps(n int64, d time.Duration) int {
	return int(n * 8 * 1000 / max(d.Microseconds(), 1))
}
