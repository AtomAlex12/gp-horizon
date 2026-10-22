// Package probe answers one question about a domain: why doesn't it open from
// here, and does it open some other way?
//
// A probe walks the path a browser would — DNS, TCP :443, TLS with the real
// SNI, one HTTP GET — several times in a row over one way out (Way): the DPI
// here does not only cut, it also freezes connections after the first ~16 KB
// and slows them down, and a single try is noise. Check walks the router's
// default route ("direct") and, only when direct is not clean, each tunnel
// interface; Classify turns the Report into a verdict. Nothing here changes
// the router; a probe is a few short connections to the site itself, with no
// cookies and no credentials.
package probe

import (
	"context"
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
	ViaDirect Via = "direct" // the default route, marked so nfqws2 leaves it alone: the provider's path as it is
	ViaDesync Via = "desync" // the same route unmarked, so through nfqws2 (meaningful for names in its lists)
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

// Result is one way out, tried Tries times. The per-stage fields (TCP …
// Signal) are the first try's: what a single look would have seen.
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
	Ms       int // median whole try (dial → body read) over opened tries; the first try's when none opened

	Tries     int   // tries run; fewer than Samples once the way can no longer come out clean
	Opened    int   // tries where the site opened: TLS, an HTTP answer 1..499, no geo Signal
	ConnectMs int   // median TCP connect over opened tries
	TLSMs     int   // median TLS handshake over opened tries
	TTFBMs    int   // median time from writing the request to the answer's first byte, over opened tries
	Bytes     int64 // most body bytes read in one try (read cap 256 KB)
	// Kbps is the median download speed over opened tries that read at least
	// 128 KB, or that were still reading when the stage ran out (a slow way is
	// a measured one); 0 = not measurable, the page is too small.
	Kbps int
	// Frozen is the 16–20 KB freeze: the start of a connection gets through,
	// then everything stops. Seen when the 64 KB upload test hangs, or when a
	// page whose headers came stops arriving mid-way (no byte for half a stage).
	Frozen bool
	// Cut: in some try the page broke off mid-way — a reset, or the
	// connection closed before the length it declared. Such a try didn't open.
	Cut bool
}

// Interrupted is Skipped for a walk that was cut short (the check's time ran
// out, or the agent is stopping).
const Interrupted = "проверка прервана"

// Clean means the site opened normally on this path: at least 4 tries in 5
// opened and nothing froze.
func (r Result) Clean() bool {
	return r.Skipped == "" && r.Tries > 0 && r.Opened*5 >= r.Tries*4 && !r.Frozen
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
	Ifaces   map[Via]string // tunnel interface per path
	Timeout  time.Duration  // per stage; 0 = 6s
	Neutral  string         // SNI of the control handshake; "" = www.google.com
	Samples  int            // tries per way; 0 = 5
	Mark     uint32         // SO_MARK on direct sockets so nfqws2 leaves them alone; 0 = 0x40000000
	NoFreeze bool           // skip the 64 KB upload test (tests)

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

func (p *Prober) samples() int {
	if p.Samples > 0 {
		return p.Samples
	}
	return 5
}

// mark is nfqws2's own "already handled" fwmark: its iptables rules skip
// packets that carry it.
func (p *Prober) mark() uint32 {
	if p.Mark != 0 {
		return p.Mark
	}
	return 0x40000000
}

// Check probes domain. Tunnels are tried only when direct is not clean, VLESS
// first, and the walk stops at the first tunnel that opens the site.
func (p *Prober) Check(ctx context.Context, domain string) Report {
	rep := Report{Domain: domain}
	ip, why := p.Resolve(ctx, domain)
	if why != "" {
		rep.DNS = why
		return rep
	}
	rep.Addr = ip.String()
	rep.Direct = p.Way(ctx, domain, ip, ViaDirect)
	if rep.Direct.Clean() {
		return rep
	}
	for _, via := range []Via{ViaVless, ViaWarp} {
		r := p.Way(ctx, domain, ip, via)
		rep.Tunnels = append(rep.Tunnels, r)
		if r.Clean() {
			break
		}
	}
	return rep
}

// Resolve gives the IPv4 address every way is pointed at, or why there is
// none: "nxdomain" | "timeout" | "bogus" (a private or zero answer — a stub).
func (p *Prober) Resolve(ctx context.Context, host string) (net.IP, string) {
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
			if !public(v4) {
				return nil, "bogus"
			}
			return v4, ""
		}
	}
	return nil, "nxdomain"
}

// notPublic are IPv4 ranges no public site lives in, beyond what net.IP's
// own checks cover: a DNS answer there is a stub (or a fake-IP resolver).
var notPublic = func() []*net.IPNet {
	var out []*net.IPNet
	for _, c := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "198.18.0.0/15", "240.0.0.0/4"} {
		_, n, _ := net.ParseCIDR(c)
		out = append(out, n)
	}
	return out
}()

// public: an address a site on the internet can have.
func public(v4 net.IP) bool {
	if v4.IsPrivate() || v4.IsLoopback() || v4.IsUnspecified() || v4.IsLinkLocalUnicast() || v4.IsMulticast() {
		return false
	}
	for _, n := range notPublic {
		if n.Contains(v4) {
			return false
		}
	}
	return true
}

func (p *Prober) dial(ctx context.Context, via Via, addr string) (net.Conn, error) {
	if p.Dial != nil {
		return p.Dial(ctx, via, addr)
	}
	d := net.Dialer{Timeout: p.stage()}
	switch {
	case via == ViaDirect:
		d.Control = markControl(p.mark())
	case tunnel(via):
		d.Control = bindControl(p.Ifaces[via])
	}
	// ViaDesync: the default route with no mark. nfqws2's rules sit in mangle
	// POSTROUTING, so the router's own traffic meets it like a LAN client's.
	return d.DialContext(ctx, "tcp4", addr)
}

// tunnel: via leaves through an interface of its own.
func tunnel(via Via) bool { return via != ViaDirect && via != ViaDesync }

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

// locationMarkers: a redirect to a page about the country says it too.
var locationMarkers = []string{
	"unsupported_country", "unsupported-country", "not-available-in-your-country",
	"unavailable-in-your-country", "not_available_in_your_country", "unavailable-in-region",
	"not-available-in-region", "unsupported-region", "unsupported_region", "region-restricted",
	"geoblock", "geo-block", "geo_block", "geo-restricted",
}

// geoSignal reads an HTTP answer for "this is about where you are". Only a
// page that says so is explicit; a challenge or a bare 403 is weaker evidence
// and is only believed once another path opens the same site.
func geoSignal(status int, h http.Header, body string) Signal {
	if status >= 300 && status < 400 {
		loc := strings.ToLower(h.Get("Location"))
		for _, m := range locationMarkers {
			if strings.Contains(loc, m) {
				return SignalExplicit
			}
		}
	}
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
