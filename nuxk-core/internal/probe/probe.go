// Package probe answers one question about a domain: why doesn't it open from
// here, and does it open some other way?
//
// A probe walks the path a browser would — DNS, TCP :443, TLS with the real
// SNI, one HTTP GET — over the router's default route ("direct") and, only
// when direct is not clean, over each tunnel interface. Classify turns the
// Report into a verdict. Nothing here changes the router; a probe is a few
// short connections to the site itself, with no cookies and no credentials.
package probe

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"
)

// Via is the way out a probe takes.
type Via string

const (
	ViaDirect Via = "direct" // the router's own default route
	ViaVless  Via = "vless"  // xray tunnel interface
	ViaWarp   Via = "warp"   // usque tunnel interface
)

// Fail is why a stage did not complete.
type Fail string

const (
	FailNone        Fail = ""
	FailTimeout     Fail = "timeout"     // silence: a black hole or a drop
	FailReset       Fail = "reset"       // RST in the middle: the classic DPI cut
	FailEOF         Fail = "eof"         // closed without an answer
	FailRefused     Fail = "refused"     // nothing listens there
	FailUnreachable Fail = "unreachable" // no route
	FailAlert       Fail = "alert"       // the server itself answered with a TLS alert
	FailOther       Fail = "other"
)

// Signal is how an HTTP answer says "not for your country".
type Signal string

const (
	SignalNone      Signal = ""
	SignalExplicit  Signal = "explicit"  // the page names the country/region, or 451
	SignalChallenge Signal = "challenge" // CDN challenge page (also shown to plain bots)
	SignalDenied    Signal = "denied"    // bare 403, no explanation
)

// Result is one path's walk.
type Result struct {
	Via     Via
	Iface   string
	Skipped string // why the path was not probed (empty = it was)

	TCP     bool
	TCPFail Fail
	TLS     bool
	TLSFail Fail
	// Neutral is set only when TLS failed after TCP worked: did the same IP
	// complete a handshake for an unrelated name? true = only the real name is
	// cut (SNI filter); false = the IP itself is cut.
	Neutral *bool

	Status   int // HTTP status, 0 = no answer
	HTTPFail Fail
	Signal   Signal
	Ms       int
}

// Clean means the site opened normally on this path.
func (r Result) Clean() bool {
	return r.Skipped == "" && r.TLS && r.Status > 0 && r.Status < 500 && r.Signal == SignalNone
}

// Report is everything one check learned about a domain.
type Report struct {
	Domain  string
	Addr    string // the IPv4 every path was pointed at
	DNS     string // "" = resolved | nxdomain | timeout | bogus (private/zero answer)
	Direct  Result
	Tunnels []Result // only the tunnels actually tried, in preference order
}

// Prober runs checks. The zero value probes direct only.
type Prober struct {
	Ifaces  map[Via]string // tunnel interface per path
	Timeout time.Duration  // per stage; 0 = 6s
	Neutral string         // SNI of the control handshake; "" = www.google.com

	// Test hooks. nil = the real network.
	LookupIP func(ctx context.Context, host string) ([]net.IP, error)
	Dial     func(ctx context.Context, via Via, addr string) (net.Conn, error)
	Avail    func(via Via) string // "" = usable, else why not
}

func (p *Prober) stage() time.Duration {
	if p.Timeout > 0 {
		return p.Timeout
	}
	return 6 * time.Second
}

// Check probes domain. Tunnels are tried only when direct is not clean, VLESS
// first, and the walk stops at the first tunnel that opens the site.
func (p *Prober) Check(ctx context.Context, domain string) Report {
	rep := Report{Domain: domain}
	ip, why := p.resolve(ctx, domain)
	if why != "" {
		rep.DNS = why
		return rep
	}
	rep.Addr = ip.String()
	rep.Direct = p.path(ctx, domain, ip, ViaDirect)
	if rep.Direct.Clean() {
		return rep
	}
	for _, via := range []Via{ViaVless, ViaWarp} {
		r := p.path(ctx, domain, ip, via)
		rep.Tunnels = append(rep.Tunnels, r)
		if r.Clean() {
			break
		}
	}
	return rep
}

func (p *Prober) resolve(ctx context.Context, host string) (net.IP, string) {
	if net.ParseIP(host) != nil {
		return nil, "bogus" // a probe takes names, not addresses
	}
	rctx, cancel := context.WithTimeout(ctx, p.stage())
	defer cancel()
	var ips []net.IP
	var err error
	if p.LookupIP != nil {
		ips, err = p.LookupIP(rctx, host)
	} else {
		var as []net.IPAddr
		as, err = net.DefaultResolver.LookupIPAddr(rctx, host)
		for _, a := range as {
			ips = append(ips, a.IP)
		}
	}
	if err != nil {
		var de *net.DNSError
		if errors.As(err, &de) && de.IsNotFound {
			return nil, "nxdomain"
		}
		return nil, "timeout"
	}
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil { // IPv6 is not routed for tunnels here
			if v4.IsPrivate() || v4.IsLoopback() || v4.IsUnspecified() || v4.IsLinkLocalUnicast() || v4.IsMulticast() {
				return nil, "bogus"
			}
			return v4, ""
		}
	}
	return nil, "nxdomain"
}

// path walks one way out to ip.
func (p *Prober) path(ctx context.Context, domain string, ip net.IP, via Via) (r Result) {
	r = Result{Via: via, Iface: p.Ifaces[via]}
	if via != ViaDirect {
		if why := p.unavailable(via); why != "" {
			r.Skipped = why
			return r
		}
	}
	start := time.Now()
	defer func() { r.Ms = int(time.Since(start) / time.Millisecond) }()

	addr := net.JoinHostPort(ip.String(), "443")
	dctx, cancel := context.WithTimeout(ctx, p.stage())
	conn, err := p.dial(dctx, via, addr)
	cancel()
	if err != nil {
		r.TCPFail = failKind(err)
		return r
	}
	defer conn.Close()
	r.TCP = true

	// Certificates are not checked: the question is whether the handshake and
	// the answer get through, not who signed them — and the router's Entware
	// CA store is not always where Go looks for it.
	tc := tls.Client(conn, &tls.Config{ServerName: domain, InsecureSkipVerify: true, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}})
	hctx, hcancel := context.WithTimeout(ctx, p.stage())
	err = tc.HandshakeContext(hctx)
	hcancel()
	if err != nil {
		r.TLSFail = failKind(err)
		n := p.neutral(ctx, via, addr)
		r.Neutral = &n
		return r
	}
	r.TLS = true

	_ = conn.SetDeadline(time.Now().Add(p.stage()))
	req, _ := http.NewRequest(http.MethodGet, "https://"+domain+"/", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Connection", "close")
	if err := req.Write(tc); err != nil {
		r.HTTPFail = failKind(err)
		return r
	}
	resp, err := http.ReadResponse(bufio.NewReader(tc), req)
	if err != nil {
		r.HTTPFail = failKind(err)
		return r
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	r.Status = resp.StatusCode
	r.Signal = geoSignal(resp.StatusCode, resp.Header, string(body))
	return r
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

func (p *Prober) dial(ctx context.Context, via Via, addr string) (net.Conn, error) {
	if p.Dial != nil {
		return p.Dial(ctx, via, addr)
	}
	d := net.Dialer{Timeout: p.stage()}
	if via != ViaDirect {
		d.Control = bindControl(p.Ifaces[via])
	}
	return d.DialContext(ctx, "tcp4", addr)
}

// unavailable says why a tunnel can't be probed, in words for the user.
func (p *Prober) unavailable(via Via) string {
	if p.Avail != nil {
		return p.Avail(via)
	}
	name := p.Ifaces[via]
	if name == "" {
		return "интерфейс туннеля не задан"
	}
	ifc, err := net.InterfaceByName(name)
	if err != nil {
		return "интерфейс " + name + " не найден"
	}
	if ifc.Flags&net.FlagUp == 0 {
		return "интерфейс " + name + " выключен"
	}
	return ""
}

// Paths lists the tunnel ways out and whether each can be probed right now.
func (p *Prober) Paths() []PathState {
	out := []PathState{{Via: ViaDirect, Usable: true}}
	for _, via := range []Via{ViaVless, ViaWarp} {
		why := p.unavailable(via)
		out = append(out, PathState{Via: via, Iface: p.Ifaces[via], Usable: why == "", Why: why})
	}
	return out
}

// PathState is one way out and whether it can be probed.
type PathState struct {
	Via    Via
	Iface  string
	Usable bool
	Why    string
}

// failKind buckets an error into what the network actually did.
func failKind(err error) Fail {
	if err == nil {
		return FailNone
	}
	var ne net.Error
	msg := err.Error()
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return FailTimeout
	case errors.Is(err, syscall.ECONNRESET), strings.Contains(msg, "connection reset"), strings.Contains(msg, "forcibly closed"):
		return FailReset
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return FailEOF
	case errors.Is(err, syscall.ECONNREFUSED), strings.Contains(msg, "refused"):
		return FailRefused
	case errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EHOSTUNREACH), strings.Contains(msg, "unreachable"):
		return FailUnreachable
	case strings.Contains(msg, "remote error"):
		return FailAlert
	}
	return FailOther
}

var explicitMarkers = []string{
	"unsupported_country_region_territory",
	"country, region, or territory not supported",
	"not available in your country",
	"not available in your region",
	"isn't available in your country",
	"isn't available in your region",
	"unavailable in your country",
	"unavailable in your region",
	"owner has banned the country or region",
	"error 1009",
	"недоступен в вашей стране",
	"недоступен в вашем регионе",
}

// geoSignal reads an HTTP answer for "this is about where you are". Only a
// page that says so is explicit; a challenge or a bare 403 is weaker evidence
// and is only believed once another path opens the same site.
func geoSignal(status int, h http.Header, body string) Signal {
	if status < 400 {
		return SignalNone
	}
	if status == http.StatusUnavailableForLegalReasons {
		return SignalExplicit
	}
	b := strings.ToLower(body)
	for _, m := range explicitMarkers {
		if strings.Contains(b, m) {
			return SignalExplicit
		}
	}
	if strings.EqualFold(h.Get("Cf-Mitigated"), "challenge") ||
		(status == 403 || status == 503) && (strings.Contains(b, "just a moment") || strings.Contains(b, "challenge-platform")) {
		return SignalChallenge
	}
	if status == http.StatusForbidden {
		return SignalDenied
	}
	return SignalNone
}
