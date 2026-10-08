// Package dns: the router's DNS answers, protected from substitution.
//
// In Russia plain DNS (UDP/53) is intercepted on the way, even to 8.8.8.8, and
// answered with stubs; encrypted DNS to known resolvers gets blocked from time
// to time. Keenetic's domain routing takes the address the DNS answered — a
// substituted one sends a stub into the tunnel.
//
// So nuxk-core runs a small forwarder on the router (its LAN address, port
// 53053 — KeeneticOS refuses a loopback DNS server): a query
// from Keenetic's DNS proxy goes out as DNS-over-HTTPS through a tunnel —
// VLESS, WARP — or, with no tunnel up, straight. The provider sees neither the
// question nor the answer and has nothing to block. The answers are kept for
// their TTL (cache.go): names in use are refreshed ahead, and an expired
// answer stands in while no way out answers; the DNS proxy does the rest.
//
// The DNS proxy itself stays (domain routing lives in it): the forwarder is
// added to it as one more server ("ip name-server 192.168.1.1:53053", running
// config only). Turning it on is the person's choice in the panel; it is
// checked on the way and taken back if the router's DNS stops answering.
package dns

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Resolver is a DoH server, reached at fixed addresses: the forwarder never
// asks DNS itself, so it can't loop through the DNS proxy it serves.
type Resolver struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	URL  string   `json:"url"`
	IPs  []string `json:"-"`
}

// Catalog: resolvers whose certificates name them by host and that answer
// DoH at those addresses.
var Catalog = []Resolver{
	{ID: "cloudflare", Name: "Cloudflare", URL: "https://cloudflare-dns.com/dns-query", IPs: []string{"1.1.1.1", "1.0.0.1"}},
	{ID: "google", Name: "Google", URL: "https://dns.google/dns-query", IPs: []string{"8.8.8.8", "8.8.4.4"}},
	{ID: "quad9", Name: "Quad9", URL: "https://dns.quad9.net/dns-query", IPs: []string{"9.9.9.9", "149.112.112.112"}},
}

const (
	ViaAuto   = "auto"   // VLESS, then WARP, then straight
	ViaVless  = "vless"  // VLESS, straight if it's down
	ViaWarp   = "warp"   // WARP, straight if it's down
	ViaDirect = "direct" // straight to the resolvers (DoH is still encrypted)

	PathDirect = "direct"
)

// Settings: set in the panel, kept in the state dir.
type Settings struct {
	Enabled   bool     `json:"enabled"`   // the router's DNS proxy asks nuxk
	Via       string   `json:"via"`       // auto | vless | warp | direct
	Resolvers []string `json:"resolvers"` // Catalog ids, in order of preference
	Cache     bool     `json:"cache"`     // answers kept for their TTL (on unless turned off)
	Engine    string   `json:"engine"`    // nuxk (the built-in forwarder) | smartdns (beta)
}

// Path is a way out: a tunnel's interface, or direct ("" iface).
type Path struct {
	Name  string // vless | warp | direct
	Iface string // the kernel's name, opkgtun1
}

// Hook adds nuxk's forwarder to the router's DNS proxy and takes it back.
type Hook interface {
	Attach(ctx context.Context, addr string) error
	Detach(ctx context.Context, addr string) error
}

// Store keeps the settings (state.Store).
type Store interface {
	LoadJSON(name string, v any) error
	SaveJSON(name string, v any) error
}

type Options struct {
	Listen    string                       // the forwarder: the LAN address, port 53053
	RouterDNS string                       // what devices ask: the DNS proxy, 127.0.0.1:53
	PlainDNS  string                       // asked plainly by the check (PlainDNS)
	Paths     func() []Path                // tunnels up right now, in order of preference
	Domains   func() []string              // the person's lists, for the spoofing check
	Hook      Hook                         // nil: not a Keenetic, nothing to attach to
	Resolvers []Resolver                   // Catalog (tests replace it)
	Transport func(Path) http.RoundTripper // tests: DoH without the internet
	Clock     func() time.Time             // tests: time of their own
	SmartDNS  *SmartDNSOptions             // nil: SmartDNS isn't offered
}

// PathStat: how one way out has been doing.
type PathStat struct {
	Name    string  `json:"name"`
	Iface   string  `json:"iface,omitempty"`
	Up      bool    `json:"up"` // usable now (a tunnel: its engine runs)
	OK      uint64  `json:"ok"`
	Failed  uint64  `json:"failed"`
	LastOK  int64   `json:"last_ok,omitempty"`
	RTTms   float64 `json:"rtt_ms,omitempty"` // the last answer's time
	LastErr string  `json:"last_error,omitempty"`
}

// Status is GET /api/v1/dns.
type Status struct {
	Settings  Settings       `json:"settings"`
	Listen    string         `json:"listen"`
	Running   bool           `json:"running"`   // the forwarder listens
	Attached  bool           `json:"attached"`  // added to the router's DNS proxy
	Suspended bool           `json:"suspended"` // taken back for a while: no way out answered
	CanAttach bool           `json:"can_attach"`
	Cannot    string         `json:"cannot,omitempty"`
	Queries   uint64         `json:"queries"`
	Failed    uint64         `json:"failed"`
	LastQuery int64          `json:"last_query,omitempty"` // the DNS proxy last asked, unix
	Consulted bool           `json:"consulted"`            // the proxy passed nuxk's own test question on
	LastPath  string         `json:"last_path,omitempty"`
	Resolver  string         `json:"resolver,omitempty"` // the one that answered last
	Paths     []PathStat     `json:"paths"`
	Cache     CacheStat      `json:"cache"`
	SmartDNS  *SmartDNSState `json:"smartdns,omitempty"` // nil: not offered on this box
	Error     string         `json:"error,omitempty"`
	Catalog   []Resolver     `json:"catalog"`
}

var (
	ErrBadSettings = errors.New("способ — auto, vless, warp или direct; серверы — из списка; движок — nuxk или smartdns")
	ErrNoSmartDNS  = errors.New("SmartDNS не установлен на роутере — поставьте его командой nuxk dns")
	ErrNoHook      = errors.New("подключить нечего: это не роутер Keenetic (или маршрутизация выключена)")
)

type Service struct {
	o  Options
	st Store

	mu        sync.Mutex
	set       Settings
	udp       *net.UDPConn
	tcp       net.Listener
	attached  bool
	suspended bool
	fails     int // health checks failed in a row
	lastErr   string
	consulted bool
	probe     string // the test question now in flight
	clients   map[Path]*http.Client
	stats     map[string]*PathStat
	lastPath  string
	resolver  int // index into the resolver order that answered last

	cache     *cache
	fmu       sync.Mutex
	inflight  map[string]*call // questions out to the resolvers now, by cache key
	staleWait time.Duration
	warm      time.Duration // how long a just started SmartDNS gets to answer
	clock     func() time.Time

	sd  *smartDNS // nil: not offered
	rec *recorder // the charts: every answer, by minute

	queries   atomic.Uint64
	failed    atomic.Uint64
	lastQuery atomic.Int64
	sem       chan struct{}
}

func New(o Options, st Store) *Service {
	if o.Listen == "" {
		o.Listen = "127.0.0.1:53053"
	}
	if o.RouterDNS == "" {
		o.RouterDNS = "127.0.0.1:53"
	}
	if o.PlainDNS == "" {
		o.PlainDNS = PlainDNS
	}
	if o.Resolvers == nil {
		o.Resolvers = Catalog
	}
	if o.Paths == nil {
		o.Paths = func() []Path { return nil }
	}
	if o.Clock == nil {
		o.Clock = time.Now
	}
	s := &Service{
		o: o, st: st,
		set:       Settings{Via: ViaAuto, Cache: true}, // what a saved file leaves out stays so
		clients:   map[Path]*http.Client{},
		stats:     map[string]*PathStat{},
		cache:     newCache(),
		inflight:  map[string]*call{},
		staleWait: staleWait,
		warm:      10 * time.Second,
		clock:     o.Clock,
		sem:       make(chan struct{}, 64),
		sd:        newSmartDNS(o.SmartDNS),
		rec:       newRecorder(o.Clock),
	}
	if st != nil {
		_ = st.LoadJSON("dns", &s.set)
	}
	s.set = s.normal(s.set)
	return s
}

func (s *Service) normal(set Settings) Settings {
	if set.Engine != EngineSmartDNS || s.sd == nil {
		set.Engine = EngineNuxk
	}
	switch set.Via {
	case ViaVless, ViaWarp, ViaDirect:
	default:
		set.Via = ViaAuto
	}
	var ids []string
	for _, id := range set.Resolvers {
		if s.resolverByID(id) != nil {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		for _, r := range s.o.Resolvers {
			ids = append(ids, r.ID)
		}
	}
	set.Resolvers = ids
	return set
}

func (s *Service) resolverByID(id string) *Resolver {
	for i := range s.o.Resolvers {
		if s.o.Resolvers[i].ID == id {
			return &s.o.Resolvers[i]
		}
	}
	return nil
}

// --- the forwarder -------------------------------------------------------------

// start: the chosen engine answering on the address — SmartDNS started
// with its configuration, or the forwarder's own listeners.
func (s *Service) start() error {
	if s.engine() == EngineSmartDNS {
		return s.startSmart(context.Background())
	}
	return s.listen()
}

func (s *Service) engine() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.set.Engine
}

func (s *Service) startSmart(ctx context.Context) error {
	if !s.sd.installed() {
		return ErrNoSmartDNS
	}
	if err := s.sd.apply(ctx, s.smartConf()); err != nil {
		s.sd.note(false, err.Error())
		return fmt.Errorf("SmartDNS не запустился: %w", err)
	}
	return nil
}

// stopAll: neither engine answers (protection turned off).
func (s *Service) stopAll(ctx context.Context) {
	s.stop()
	if s.sd != nil && s.sd.installed() {
		if err := s.sd.stop(ctx); err != nil {
			slog.Warn("dns: smartdns stop", "err", err)
		}
	}
}

// listen opens the forwarder's listeners (UDP and TCP on the same address).
func (s *Service) listen() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.udp != nil {
		return nil
	}
	ua, err := net.ResolveUDPAddr("udp", s.o.Listen)
	if err != nil {
		return err
	}
	u, err := net.ListenUDP("udp", ua)
	if err != nil {
		return fmt.Errorf("DNS nuxk не открылся на %s: %w", s.o.Listen, err)
	}
	t, err := net.Listen("tcp", s.o.Listen)
	if err != nil {
		u.Close()
		return fmt.Errorf("DNS nuxk не открылся на %s (TCP): %w", s.o.Listen, err)
	}
	s.udp, s.tcp = u, t
	go s.serveUDP(u)
	go s.serveTCP(t)
	slog.Info("dns forwarder listening", "addr", s.o.Listen)
	return nil
}

func (s *Service) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.udp != nil {
		s.udp.Close()
		s.tcp.Close()
		s.udp, s.tcp = nil, nil
	}
}

// Allowed: only the router itself asks — its DNS proxy, from the address the
// forwarder listens on (or loopback). The forwarder sits on the LAN address
// (KeeneticOS refuses a loopback server), and devices on the network are not
// to use it directly: they ask the DNS proxy, where domain routing lives.
func (s *Service) Allowed(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	host, _, err := net.SplitHostPort(s.o.Listen)
	if err != nil {
		return false
	}
	own := net.ParseIP(host)
	return own != nil && (own.IsUnspecified() || own.Equal(ip))
}

func (s *Service) serveUDP(c *net.UDPConn) {
	buf := make([]byte, 4096)
	for {
		n, from, err := c.ReadFromUDP(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		if !s.Allowed(from.IP) {
			continue
		}
		q := append([]byte{}, buf[:n]...)
		select {
		case s.sem <- struct{}{}:
		default:
			continue // overloaded: the asker retries
		}
		go func() {
			defer func() { <-s.sem }()
			resp := s.answer(q)
			if resp == nil {
				return
			}
			if len(resp) > UDPLimit(q) {
				resp = Truncated(resp)
			}
			_, _ = c.WriteToUDP(resp, from)
		}()
	}
}

func (s *Service) serveTCP(l net.Listener) {
	for {
		c, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		if ta, ok := c.RemoteAddr().(*net.TCPAddr); !ok || !s.Allowed(ta.IP) {
			c.Close()
			continue
		}
		go func() {
			defer c.Close()
			for {
				_ = c.SetDeadline(time.Now().Add(15 * time.Second))
				var l [2]byte
				if _, err := io.ReadFull(c, l[:]); err != nil {
					return
				}
				q := make([]byte, binary.BigEndian.Uint16(l[:]))
				if _, err := io.ReadFull(c, q); err != nil {
					return
				}
				resp := s.answer(q)
				if resp == nil {
					return
				}
				out := binary.BigEndian.AppendUint16(nil, uint16(len(resp)))
				if _, err := c.Write(append(out, resp...)); err != nil {
					return
				}
			}
		}()
	}
}

// answer: one query in, one response out (SERVFAIL when no way worked).
func (s *Service) answer(q []byte) []byte {
	if len(q) < headerLen || q[2]&0x80 != 0 {
		return nil
	}
	t0 := time.Now()
	resp, src := s.reply(q)
	s.rec.add(LogEntry{
		At: s.clock().Unix(), Domain: QName(q), Type: typeName(QType(q)),
		Answer: summary(resp), Source: src, Ms: int(time.Since(t0).Milliseconds()),
	})
	return resp
}

func (s *Service) reply(q []byte) ([]byte, string) {
	s.queries.Add(1)
	s.lastQuery.Store(time.Now().Unix())
	if name := QName(q); name != "" {
		s.mu.Lock()
		if s.probe != "" && name == s.probe {
			s.consulted = true
		}
		s.mu.Unlock()
	}
	if s.cacheOn() {
		if key := cacheKey(q); key != "" {
			return s.cached(key, q)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	resp, _, err := s.resolve(ctx, q)
	if err != nil {
		s.failed.Add(1)
		return ServFail(q), SrcFailed
	}
	return resp, SrcUpstream
}

// cached: from the cache while fresh; otherwise from the resolvers — and if
// they don't answer in time (or fail), the expired answer: better than none.
func (s *Service) cached(key string, q []byte) ([]byte, string) {
	now := s.clock()
	e, fresh := s.cache.get(key, now)
	if fresh {
		s.cache.count(func(c *CacheStat) { c.Hits++ })
		return e.reply(q, now, false), SrcCache
	}
	s.cache.count(func(c *CacheStat) { c.Misses++ })
	c := s.flight(key, q, false)
	if c != nil {
		var wait <-chan time.Time
		if e != nil {
			t := time.NewTimer(s.staleWait)
			defer t.Stop()
			wait = t.C
		}
		select {
		case <-c.done:
			if c.err == nil && (e == nil || c.resp[3]&0x0F != RcodeServFail) {
				return own(c.resp, q), SrcUpstream
			}
		case <-wait:
		}
	}
	if e != nil {
		s.cache.count(func(c *CacheStat) { c.Stale++ })
		return e.reply(q, s.clock(), true), SrcStale
	}
	s.failed.Add(1)
	return ServFail(q), SrcFailed
}

type call struct {
	done chan struct{}
	resp []byte
	err  error
}

// maxFlights: questions out to the resolvers at once, at most.
const maxFlights = 64

// flight asks the resolvers once for a question, however many ask meanwhile;
// the answer goes to the cache. nil: too many out already.
func (s *Service) flight(key string, q []byte, refresh bool) *call {
	s.fmu.Lock()
	defer s.fmu.Unlock()
	if c := s.inflight[key]; c != nil {
		return c
	}
	if len(s.inflight) >= maxFlights {
		return nil
	}
	c := &call{done: make(chan struct{})}
	s.inflight[key] = c
	q = append([]byte(nil), q...)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		resp, _, err := s.resolve(ctx, q)
		cancel()
		if err == nil && s.cacheOn() {
			if e := parseEntry(key, q, resp); e != nil {
				s.cache.put(e, s.clock())
				if refresh {
					s.cache.count(func(c *CacheStat) { c.Refreshed++ })
				}
			}
		}
		c.resp, c.err = resp, err
		s.fmu.Lock()
		delete(s.inflight, key)
		s.fmu.Unlock()
		close(c.done)
	}()
	return c
}

// refresh asks again, in the background, for names in use about to expire:
// the DNS proxy finds them fresh, and they're fresh should a tunnel drop.
func (s *Service) refresh() {
	if set := s.Settings(); !set.Enabled || !set.Cache {
		return
	}
	s.mu.Lock()
	on := s.udp != nil && !s.suspended
	s.mu.Unlock()
	if !on {
		return
	}
	for _, e := range s.cache.due(s.clock(), 16) {
		if s.flight(e.key, e.query, true) == nil {
			return
		}
	}
}

func (s *Service) cacheOn() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.set.Cache
}

// FlushCache forgets every kept answer (the counters stay).
// With SmartDNS: restarted — its cache lives only in its memory.
func (s *Service) FlushCache(ctx context.Context) (Status, error) {
	s.cache.flush()
	if s.engine() == EngineSmartDNS && s.Settings().Enabled {
		if err := s.sd.restart(ctx); err != nil {
			return s.Status(), fmt.Errorf("SmartDNS не перезапустился: %w", err)
		}
	}
	return s.Status(), nil
}

// Stats: the last hour of answers, for the charts.
func (s *Service) Stats() Stats {
	st := s.rec.stats()
	st.Engine = s.engine()
	st.StaleKnown = st.Engine == EngineNuxk
	if !st.StaleKnown {
		st.Note = "SmartDNS не сообщает, откуда ответ: «из кэша» — ответы быстрее 1 мс (среди них и устаревшие), «нет ответа» не отличить от пустого ответа"
	}
	return st
}

// Log: the last questions, newest first; match filters by part of the name.
func (s *Service) Log(match string, n int) []LogEntry {
	if n <= 0 || n > statRecent {
		n = statRecent
	}
	return s.rec.log(match, n)
}

// order: the ways out to try, in turn — the chosen tunnel(s), then direct.
func (s *Service) order() []Path {
	s.mu.Lock()
	via := s.set.Via
	s.mu.Unlock()
	var ps []Path
	if via != ViaDirect {
		for _, p := range s.o.Paths() {
			if via == ViaAuto || p.Name == via {
				ps = append(ps, p)
			}
		}
	}
	return append(ps, Path{Name: PathDirect})
}

func (s *Service) resolvers() []Resolver {
	s.mu.Lock()
	defer s.mu.Unlock()
	var rs []Resolver
	for _, id := range s.set.Resolvers {
		if r := s.resolverByID(id); r != nil {
			rs = append(rs, *r)
		}
	}
	// the one that answered last goes first
	if s.resolver > 0 && s.resolver < len(rs) {
		rs[0], rs[s.resolver] = rs[s.resolver], rs[0]
	}
	return rs
}

// resolve asks the resolvers through each way out in turn: the answer, and
// which way it came.
func (s *Service) resolve(ctx context.Context, q []byte) ([]byte, string, error) {
	rs := s.resolvers()
	var last error
	for _, p := range s.order() {
		c := s.client(p)
		for _, r := range rs {
			actx, cancel := context.WithTimeout(ctx, 3*time.Second)
			t0 := time.Now()
			resp, err := doh(actx, c, r, q)
			cancel()
			s.note(p, err, time.Since(t0))
			if err == nil {
				s.mu.Lock()
				s.lastPath = p.Name
				for i, id := range s.set.Resolvers {
					if id == r.ID {
						s.resolver = i
					}
				}
				s.mu.Unlock()
				return resp, p.Name, nil
			}
			last = fmt.Errorf("%s через %s: %w", r.Name, pathTitle(p.Name), err)
			slog.Debug("dns: resolver failed, trying the next", "resolver", r.Name, "path", p.Name, "err", err)
			if ctx.Err() != nil {
				return nil, "", last
			}
		}
	}
	if last == nil {
		last = errors.New("нет ни одного DoH-сервера")
	}
	return nil, "", last
}

func pathTitle(name string) string {
	switch name {
	case "vless":
		return "VLESS"
	case "warp":
		return "WARP"
	}
	return "напрямую"
}

func (s *Service) note(p Path, err error, took time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.stats[p.Name]
	if st == nil {
		st = &PathStat{Name: p.Name}
		s.stats[p.Name] = st
	}
	st.Iface = p.Iface
	if err != nil {
		st.Failed++
		st.LastErr = err.Error()
		return
	}
	st.OK++
	st.LastOK = time.Now().Unix()
	st.RTTms = float64(took.Microseconds()) / 1000
	st.LastErr = ""
}

// client: one HTTP client per way out, kept (connections are reused: a DoH
// query on a warm connection is one round trip).
func (s *Service) client(p Path) *http.Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.clients[p]; c != nil {
		return c
	}
	var rt http.RoundTripper
	if s.o.Transport != nil {
		rt = s.o.Transport(p)
	} else {
		rt = s.transport(p)
	}
	c := &http.Client{Transport: rt, Timeout: 5 * time.Second}
	s.clients[p] = c
	return c
}

func (s *Service) transport(p Path) *http.Transport {
	d := &net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}
	if p.Iface != "" {
		d.Control = bindTo(p.Iface)
	}
	return &http.Transport{
		// the resolver's host → its fixed addresses: no DNS lookup, ever
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			var last error
			for _, ip := range s.ipsOf(host) {
				c, err := d.DialContext(ctx, network, net.JoinHostPort(ip, port))
				if err == nil {
					return c, nil
				}
				last = err
			}
			if last == nil {
				last = fmt.Errorf("неизвестный DoH-сервер %s", host)
			}
			return nil, last
		},
		ForceAttemptHTTP2:   true,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 4 * time.Second,
	}
}

func (s *Service) ipsOf(host string) []string {
	for _, r := range s.o.Resolvers {
		if strings.Contains(r.URL, "://"+host+"/") || strings.Contains(r.URL, "://"+host+":") {
			return r.IPs
		}
	}
	return nil
}

// doh: RFC 8484, POST. The ID goes out as 0 (as the RFC asks) and the
// asker's comes back in the answer.
func doh(ctx context.Context, c *http.Client, r Resolver, q []byte) ([]byte, error) {
	body := append([]byte{}, q...)
	body[0], body[1] = 0, 0
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	ans, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	if len(ans) < headerLen || ans[2]&0x80 == 0 {
		return nil, errors.New("не DNS-ответ")
	}
	ans[0], ans[1] = q[0], q[1]
	return ans, nil
}

// --- on, off, and keeping it honest ---------------------------------------------

// Run keeps the forwarder as the settings say: listening and attached while
// on; every 30 s it checks that some way out answers — none does three
// times running: taken back from the DNS proxy until one does again. Every
// 5 s names in use about to expire are refreshed.
func (s *Service) Run(ctx context.Context) {
	if s.Settings().Enabled {
		if err := s.start(); err != nil {
			s.setErr(err.Error())
		}
	}
	t := time.NewTimer(5 * time.Second)
	defer t.Stop()
	r := time.NewTicker(5 * time.Second)
	defer r.Stop()
	for {
		select {
		case <-ctx.Done():
			// the attachment stays: an update restarts the agent in seconds;
			// SmartDNS keeps answering meanwhile
			s.stop()
			return
		case <-r.C:
			if s.engine() == EngineSmartDNS {
				s.sd.tail(s.rec, s.clock())
			} else {
				s.refresh()
			}
			continue
		case <-t.C:
		}
		s.tick(ctx)
		t.Reset(30 * time.Second)
	}
}

func (s *Service) tick(ctx context.Context) {
	if !s.Settings().Enabled {
		return
	}
	smart := s.engine() == EngineSmartDNS
	// SmartDNS runs on its own: started again only when it doesn't answer —
	// or with a new configuration when WARP's interface came or changed
	if smart && s.sd.outdated(s.smartConf()) {
		if err := s.startSmart(ctx); err != nil {
			s.setErr(err.Error())
		}
	}
	if !smart {
		if err := s.start(); err != nil {
			s.setErr(err.Error())
			return
		}
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	_, err := s.lookup(cctx, "cloudflare.com")
	if err != nil && smart {
		if serr := s.start(); serr != nil {
			s.setErr(serr.Error())
		} else {
			_, err = s.lookupWarm(cctx)
		}
	}
	s.mu.Lock()
	if err != nil {
		s.fails++
	} else {
		s.fails = 0
	}
	fails, attached, suspended := s.fails, s.attached, s.suspended
	s.mu.Unlock()
	switch {
	case err != nil && fails >= 3 && attached:
		s.detach(ctx)
		s.mu.Lock()
		s.suspended = true
		s.lastErr = "ни один путь до DoH-серверов не отвечает — DNS роутера пока работает без nuxk: " + err.Error()
		s.mu.Unlock()
		slog.Warn("dns: forwarder taken back from the DNS proxy", "err", err)
	case err == nil && (!attached || suspended) && s.o.Hook != nil:
		if aerr := s.attach(ctx); aerr != nil {
			s.setErr(aerr.Error())
		} else {
			s.mu.Lock()
			s.suspended = false
			s.lastErr = ""
			s.mu.Unlock()
		}
	}
}

func (s *Service) setErr(e string) {
	s.mu.Lock()
	s.lastErr = e
	s.mu.Unlock()
}

// lookup: A records of name through the forwarder's own ways out.
// With SmartDNS: asked on its address, as the router's DNS proxy asks it.
func (s *Service) lookup(ctx context.Context, name string) (Answer, error) {
	if s.engine() == EngineSmartDNS {
		a, err := udpLookup(ctx, s.o.Listen, name)
		if err != nil {
			s.sd.note(false, "SmartDNS не ответил: "+err.Error())
		} else {
			s.sd.note(true, "")
		}
		return a, err
	}
	q, err := Query(randID(), name, TypeA)
	if err != nil {
		return Answer{}, err
	}
	resp, _, err := s.resolve(ctx, q)
	if err != nil {
		return Answer{}, err
	}
	return Parse(resp)
}

// lookupWarm: the engine answers — given a few seconds, as a SmartDNS just
// started opens its first connections through the tunnel.
func (s *Service) lookupWarm(ctx context.Context) (Answer, error) {
	var a Answer
	var err error
	tries := 1
	if s.engine() == EngineSmartDNS {
		tries = 3
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.warm)
		defer cancel()
	}
	for i := 0; i < tries; i++ {
		if a, err = s.lookup(ctx, "cloudflare.com"); err == nil || ctx.Err() != nil {
			break
		}
	}
	return a, err
}

// attach adds the forwarder to the DNS proxy and makes sure the router still
// answers — if it doesn't, takes it back at once.
func (s *Service) attach(ctx context.Context) error {
	if s.o.Hook == nil {
		return ErrNoHook
	}
	if err := s.o.Hook.Attach(ctx, s.o.Listen); err != nil {
		return fmt.Errorf("роутер не принял DNS nuxk: %w", err)
	}
	s.mu.Lock()
	s.attached = true
	s.mu.Unlock()
	if _, err := s.routerLookup(ctx, "cloudflare.com"); err != nil {
		s.detach(ctx)
		return fmt.Errorf("после подключения DNS роутера не ответил (%v) — отключил обратно, всё как было", err)
	}
	// does the proxy pass questions on to nuxk? a name only nuxk would see
	probe := fmt.Sprintf("n%08x.nuxk-check.example.com", randID32())
	s.mu.Lock()
	s.probe, s.consulted = probe, false
	s.mu.Unlock()
	_, _ = s.routerLookup(ctx, probe)
	return nil
}

func (s *Service) detach(ctx context.Context) {
	if s.o.Hook == nil {
		return
	}
	if err := s.o.Hook.Detach(ctx, s.o.Listen); err != nil {
		slog.Warn("dns: detach", "err", err)
	}
	s.mu.Lock()
	s.attached = false
	s.mu.Unlock()
}

// routerLookup asks the DNS proxy — what every device on the network gets.
func (s *Service) routerLookup(ctx context.Context, name string) (Answer, error) {
	return udpLookup(ctx, s.o.RouterDNS, name)
}

func udpLookup(ctx context.Context, server, name string) (Answer, error) {
	var last error
	for try := 0; try < 2; try++ {
		q, err := Query(randID(), name, TypeA)
		if err != nil {
			return Answer{}, err
		}
		d := net.Dialer{Timeout: 2 * time.Second}
		c, err := d.DialContext(ctx, "udp", server)
		if err != nil {
			return Answer{}, err
		}
		_ = c.SetDeadline(time.Now().Add(2500 * time.Millisecond))
		if _, err = c.Write(q); err == nil {
			buf := make([]byte, 4096)
			var n int
			if n, err = c.Read(buf); err == nil {
				c.Close()
				if n < 2 || buf[0] != q[0] || buf[1] != q[1] {
					last = errors.New("чужой ответ")
					continue
				}
				return Parse(buf[:n])
			}
		}
		c.Close()
		last = err
		if ctx.Err() != nil {
			break
		}
	}
	return Answer{}, last
}

func randID() uint16 {
	var b [2]byte
	_, _ = rand.Read(b[:])
	return binary.BigEndian.Uint16(b[:])
}

func randID32() uint32 {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return binary.BigEndian.Uint32(b[:])
}

func (s *Service) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := s.set
	set.Resolvers = slices.Clone(set.Resolvers)
	return set
}

// SetSettings: the panel's choice. Turning on: the forwarder must answer
// through some way out, then it's added to the DNS proxy and the router must
// still answer — otherwise nothing changes. Turning off takes it back and
// forgets the cache, as does turning the cache off.
func (s *Service) SetSettings(ctx context.Context, set Settings) (Status, error) {
	was := s.Settings()
	if set.Via == "" {
		set.Via = was.Via
	}
	if set.Engine == "" {
		set.Engine = was.Engine
	}
	switch set.Engine {
	case EngineNuxk:
	case EngineSmartDNS:
		if !s.sd.installed() {
			return s.Status(), ErrNoSmartDNS
		}
	default:
		return s.Status(), ErrBadSettings
	}
	if len(set.Resolvers) == 0 {
		set.Resolvers = was.Resolvers
	}
	switch set.Via {
	case ViaAuto, ViaVless, ViaWarp, ViaDirect:
	default:
		return s.Status(), ErrBadSettings
	}
	for _, id := range set.Resolvers {
		if s.resolverByID(id) == nil {
			return s.Status(), ErrBadSettings
		}
	}
	s.mu.Lock()
	s.set = s.normal(set)
	s.set.Enabled = was.Enabled
	s.mu.Unlock()

	var err error
	switch {
	case set.Enabled && !was.Enabled:
		err = s.enable(ctx)
	case set.Enabled && was.Enabled && set.Engine != was.Engine:
		err = s.switchEngine(ctx, was.Engine)
	case set.Enabled && was.Enabled && set.Engine == EngineSmartDNS:
		err = s.startSmart(ctx) // the way out, servers or cache changed: a new configuration
	case !set.Enabled && was.Enabled:
		s.detach(ctx)
		s.stopAll(ctx)
		s.cache.flush()
		s.mu.Lock()
		s.set.Enabled, s.suspended, s.lastErr = false, false, ""
		s.mu.Unlock()
	}
	if !set.Cache && was.Cache {
		s.cache.flush()
	}
	if serr := s.save(); err == nil {
		err = serr
	}
	return s.Status(), err
}

func (s *Service) enable(ctx context.Context) error {
	if s.o.Hook == nil {
		return ErrNoHook
	}
	if err := s.start(); err != nil {
		return err
	}
	if _, err := s.lookupWarm(ctx); err != nil {
		s.stopAll(ctx)
		if s.engine() == EngineSmartDNS {
			return fmt.Errorf("SmartDNS запустился, но не отвечает — роутер не трогаю: %w", err)
		}
		return fmt.Errorf("DoH-серверы не отвечают ни через один путь — роутер не трогаю: %w", err)
	}
	if err := s.attach(ctx); err != nil {
		s.stopAll(ctx)
		s.setErr(err.Error())
		return err
	}
	s.mu.Lock()
	s.set.Enabled, s.suspended, s.fails, s.lastErr = true, false, 0, ""
	s.mu.Unlock()
	return nil
}

// switchEngine: the other engine on the same address. The router's DNS proxy
// keeps asking that address — only what answers there changes, for a second
// or two. If the new one doesn't answer, the old one comes back.
func (s *Service) switchEngine(ctx context.Context, old string) error {
	now := s.engine()
	s.halt(ctx, old)
	s.rec.reset()
	err := s.start()
	if err == nil {
		_, err = s.lookupWarm(ctx)
	}
	if err == nil {
		slog.Info("dns: engine switched", "from", old, "to", now)
		return nil
	}
	s.halt(ctx, now)
	s.mu.Lock()
	s.set.Engine = old
	s.mu.Unlock()
	if berr := s.start(); berr != nil {
		s.setErr(berr.Error())
	}
	return fmt.Errorf("%s не ответил — вернул прежний (%s): %w", engineTitle(now), engineTitle(old), err)
}

// halt stops one engine.
func (s *Service) halt(ctx context.Context, engine string) {
	if engine == EngineSmartDNS {
		if s.sd != nil {
			_ = s.sd.stop(ctx)
		}
		return
	}
	s.stop()
}

func engineTitle(e string) string {
	if e == EngineSmartDNS {
		return "SmartDNS"
	}
	return "встроенный DNS nuxk"
}

func (s *Service) save() error {
	if s.st == nil {
		return nil
	}
	return s.st.SaveJSON("dns", s.Settings())
}

func (s *Service) Status() Status {
	s.mu.Lock()
	st := Status{
		Settings: s.set, Listen: s.o.Listen, Running: s.udp != nil,
		Attached: s.attached, Suspended: s.suspended, Error: s.lastErr,
		Consulted: s.consulted, LastPath: s.lastPath, Catalog: s.o.Resolvers,
	}
	if st.Settings.Resolvers != nil && s.resolver < len(st.Settings.Resolvers) && s.lastPath != "" {
		if r := s.resolverByID(st.Settings.Resolvers[s.resolver]); r != nil {
			st.Resolver = r.Name
		}
	}
	stats := map[string]PathStat{}
	for k, v := range s.stats {
		stats[k] = *v
	}
	s.mu.Unlock()
	st.Queries, st.Failed, st.LastQuery = s.queries.Load(), s.failed.Load(), s.lastQuery.Load()
	st.Cache = s.cache.stats()
	st.SmartDNS = s.smartState()
	if st.Settings.Engine == EngineSmartDNS && st.SmartDNS != nil {
		st.Running = st.SmartDNS.Running
	}
	st.CanAttach = s.o.Hook != nil
	if !st.CanAttach {
		st.Cannot = ErrNoHook.Error()
	}
	up := map[string]string{}
	for _, p := range s.o.Paths() {
		up[p.Name] = p.Iface
	}
	for _, name := range []string{"vless", "warp", PathDirect} {
		ps := stats[name]
		ps.Name = name
		iface, ok := up[name]
		ps.Up = ok || name == PathDirect
		if ok {
			ps.Iface = iface
		}
		st.Paths = append(st.Paths, ps)
	}
	return st
}

// IfaceUp: the tunnel's interface exists (cheap: one stat).
func IfaceUp(iface string) bool {
	if iface == "" || !canBind {
		return false
	}
	_, err := os.Stat("/sys/class/net/" + iface)
	return err == nil
}
