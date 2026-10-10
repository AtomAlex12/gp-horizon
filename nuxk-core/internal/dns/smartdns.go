package dns

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SmartDNS (beta): the same place in the router's DNS proxy — the LAN
// address, port 53053 — answered by SmartDNS instead of the built-in
// forwarder. It keeps a cache, refreshes names in use ahead and hands out
// expired answers while no server answers; its questions go out as DoH
// through WARP's interface (-interface, SO_BINDTODEVICE), and straight to
// the same servers when WARP doesn't answer (-fallback). VLESS isn't a way
// out for it: a round trip to a far server is too slow for DNS.
//
// SmartDNS is the official release (pymumu/smartdns), pinned by hash in the
// installer (`nuxk dns`); S53smartdns-nuxk runs it. Its configuration is
// written here, from the panel's settings — nobody edits it by hand. The
// charts come from its audit log, read as it grows: SmartDNS's own API
// needs a plugin its releases don't ship for routers.

const (
	EngineNuxk     = "nuxk"     // the built-in forwarder
	EngineSmartDNS = "smartdns" // SmartDNS, run by S53smartdns-nuxk
)

// SmartDNSOptions: where SmartDNS lives on the router. nil = not wired.
type SmartDNSOptions struct {
	Script    string        // S53smartdns-nuxk; missing = not installed (`nuxk dns`)
	Dir       string        // its log and audit log, in RAM: /tmp/smartdns-nuxk
	WarpIface func() string // WARP's interface (opkgtun0); "" = no WARP
}

// SmartDNSState: SmartDNS as the panel sees it (Status.SmartDNS).
type SmartDNSState struct {
	Installed bool   `json:"installed"`         // the init script is there
	Running   bool   `json:"running"`           // it answered the last check
	Version   string `json:"version,omitempty"` // SmartDNS's own, e.g. Release48.4
	Iface     string `json:"iface,omitempty"`   // the questions go out through it (WARP)
	Fallback  bool   `json:"fallback"`          // straight when WARP doesn't answer
	Error     string `json:"error,omitempty"`
}

type smartDNS struct {
	o SmartDNSOptions

	mu      sync.Mutex
	applied string // the configuration SmartDNS runs with
	version string
	up      bool
	lastErr string

	// the audit log, read as it grows
	off  int64
	ino  uint64
	part []byte
	seen bool // skipped what was there before the agent started
}

func newSmartDNS(o *SmartDNSOptions) *smartDNS {
	if o == nil || o.Script == "" {
		return nil
	}
	if o.Dir == "" {
		o.Dir = "/tmp/smartdns-nuxk"
	}
	if o.WarpIface == nil {
		o.WarpIface = func() string { return "" }
	}
	return &smartDNS{o: *o}
}

func (d *smartDNS) installed() bool {
	if d == nil {
		return false
	}
	_, err := os.Stat(d.o.Script)
	return err == nil
}

func (d *smartDNS) audit() string { return d.o.Dir + "/audit.log" }

// SmartDNSConf is what the configuration is made of.
type SmartDNSConf struct {
	Listen    string     // 192.168.1.1:53053
	Allow     []string   // who may ask: the router itself
	Resolvers []Resolver // DoH servers, reached at their first address
	Iface     string     // through WARP ("" = straight)
	Cache     bool
	Dir       string // the log and the audit log
}

// Render: smartdns.conf. Every option is from SmartDNS's own list
// (src/dns_conf) as of Release48.
func (c SmartDNSConf) Render() string {
	var b strings.Builder
	w := func(f string, a ...any) { fmt.Fprintf(&b, f+"\n", a...) }
	w("# nuxk — written by nuxk-core from the panel's settings; edits here are lost")
	w("server-name nuxk")
	w("bind %s", c.Listen)
	w("bind-tcp %s", c.Listen)
	// only the router asks: devices ask its DNS proxy, where domain routing lives
	w("acl-enable yes")
	for _, a := range c.Allow {
		w("client-rules %s/32", a)
	}
	// pinging addresses would go around the tunnel; the first answer wins
	w("speed-check-mode none")
	w("response-mode fastest-response")
	w("dualstack-ip-selection no")
	if c.Cache {
		w("cache-size 4096")
		w("prefetch-domain yes")
		w("serve-expired yes")
		w("serve-expired-ttl %d", int(staleMax.Seconds()))
		w("serve-expired-reply-ttl %d", staleTTL)
		w("rr-ttl-max %d", maxTTL)
	} else {
		w("cache-size 0")
		w("prefetch-domain no")
		w("serve-expired no")
	}
	w("cache-persist no") // in RAM: the router's flash isn't worn
	w("log-level warn")
	w("log-file %s/smartdns.log", c.Dir)
	w("log-size 131072")
	w("log-num 1")
	w("audit-enable yes")
	w("audit-file %s/audit.log", c.Dir)
	w("audit-size 524288")
	w("audit-num 1")
	for _, r := range c.Resolvers {
		if len(r.IPs) == 0 {
			continue
		}
		host := urlHost(r.URL)
		u := strings.Replace(r.URL, "://"+host, "://"+r.IPs[0], 1)
		opts := fmt.Sprintf("-host-name %s -http-host %s -tls-host-verify %s", host, host, host)
		if c.Iface != "" {
			w("server-https %s %s -interface %s", u, opts, c.Iface)
		} else {
			w("server-https %s %s", u, opts)
		}
	}
	if c.Iface != "" {
		// asked only when the servers through WARP haven't answered
		for _, r := range c.Resolvers {
			if len(r.IPs) == 0 {
				continue
			}
			host := urlHost(r.URL)
			u := strings.Replace(r.URL, "://"+host, "://"+r.IPs[0], 1)
			w("server-https %s -host-name %s -http-host %s -tls-host-verify %s -fallback", u, host, host, host)
		}
	}
	return b.String()
}

func urlHost(u string) string {
	h := u
	if i := strings.Index(h, "://"); i >= 0 {
		h = h[i+3:]
	}
	if i := strings.IndexAny(h, "/:"); i >= 0 {
		h = h[:i]
	}
	return h
}

// run: the init script, with what it printed on failure.
func (d *smartDNS) run(ctx context.Context, stdin string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, d.o.Script, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	cmd.WaitDelay = 3 * time.Second // SmartDNS daemonizes, its pipes may linger
	start := time.Now()
	err := cmd.Run()
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil
	}
	// never stdin: it is SmartDNS's config
	slog.Debug("dns: smartdns script", "args", strings.Join(args, " "), "took", time.Since(start).Round(time.Millisecond), "err", err)
	if err != nil {
		msg := strings.Join(strings.Fields(errb.String()+" "+out.String()), " ")
		if len(msg) > 400 {
			msg = "…" + msg[len(msg)-400:]
		}
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), errors.New(msg)
	}
	return out.String(), nil
}

// apply: SmartDNS running with this configuration — written and restarted
// only if it changed; the script takes the old one back if it won't start.
func (d *smartDNS) apply(ctx context.Context, conf string) error {
	d.mu.Lock()
	same := d.applied == conf
	d.mu.Unlock()
	if same {
		_, err := d.run(ctx, "", "up") // already running: nothing happens
		return err
	}
	if _, err := d.run(ctx, conf, "set-config"); err != nil {
		return err
	}
	d.mu.Lock()
	d.applied = conf
	d.mu.Unlock()
	if d.ver() == "" {
		d.info(ctx)
	}
	return nil
}

// outdated: SmartDNS may run with another configuration than conf — or the
// agent has just started and doesn't know what it runs with: at boot the
// init script starts it with the last one, bound to a WARP that may not be
// up. Applying the same configuration to a running SmartDNS changes nothing.
func (d *smartDNS) outdated(conf string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.applied != conf
}

func (d *smartDNS) stop(ctx context.Context) error {
	_, err := d.run(ctx, "", "down") // and not at the next boot either
	d.mu.Lock()
	d.up = false
	d.mu.Unlock()
	return err
}

func (d *smartDNS) restart(ctx context.Context) error {
	_, err := d.run(ctx, "", "restart")
	return err
}

// info: the version, once (a fork; not on every status).
func (d *smartDNS) info(ctx context.Context) {
	out, err := d.run(ctx, "", "info")
	if err != nil {
		return
	}
	for _, l := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(l, "version "); ok {
			d.mu.Lock()
			d.version = strings.TrimSpace(v)
			d.mu.Unlock()
		}
	}
}

func (d *smartDNS) ver() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.version
}

func (d *smartDNS) state() (up bool, err string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.up, d.lastErr
}

func (d *smartDNS) note(up bool, err string) {
	d.mu.Lock()
	d.up, d.lastErr = up, err
	d.mu.Unlock()
}

// tail reads what the audit log got since the last time into rec. What was
// there before the agent started is skipped (it would land in this minute);
// a rotated log is read from its start.
func (d *smartDNS) tail(rec *recorder, now time.Time) {
	f, err := os.Open(d.audit())
	if err != nil {
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return
	}
	ino := inode(fi)
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.seen {
		d.seen, d.off, d.ino = true, fi.Size(), ino
		return
	}
	if ino != d.ino || fi.Size() < d.off {
		d.off, d.ino, d.part = 0, ino, nil
	}
	if fi.Size() == d.off {
		return
	}
	if _, err := f.Seek(d.off, io.SeekStart); err != nil {
		return
	}
	// at most 256 KB at once: a burst is read over the next ticks
	buf, err := io.ReadAll(io.LimitReader(f, 256<<10))
	if err != nil {
		return
	}
	d.off += int64(len(buf))
	buf = append(d.part, buf...)
	last := bytes.LastIndexByte(buf, '\n')
	if last < 0 {
		d.part = buf
		return
	}
	d.part = append([]byte(nil), buf[last+1:]...)
	sc := bufio.NewScanner(bytes.NewReader(buf[:last]))
	sc.Buffer(make([]byte, 4096), 8192)
	at := now.Unix()
	for sc.Scan() {
		if q, ok := parseAudit(sc.Text()); ok {
			q.At = at
			rec.add(q)
		}
	}
}

// parseAudit reads one line of SmartDNS's audit log:
//
//	[2026-10-04 21:14:08,123] 192.168.1.1 query example.com, type 1, time 34ms, speed: -0.1ms, group default, result 93.184.215.14
//
// A question answered within a millisecond came from its cache (it doesn't
// say so itself); an expired answer it hands out looks the same.
func parseAudit(l string) (LogEntry, bool) {
	var q LogEntry
	i := strings.Index(l, " query ")
	if i < 0 {
		return q, false
	}
	rest := l[i+len(" query "):]
	dom, rest, ok := strings.Cut(rest, ", type ")
	if !ok || dom == "" {
		return q, false
	}
	ts, rest, ok := strings.Cut(rest, ", time ")
	if !ok {
		return q, false
	}
	ms, rest, ok := strings.Cut(rest, "ms")
	if !ok {
		return q, false
	}
	t, err1 := strconv.Atoi(ts)
	m, err2 := strconv.Atoi(ms)
	if err1 != nil || err2 != nil || t < 0 || t > 65535 || m < 0 {
		return q, false
	}
	q.Domain = strings.ToLower(strings.TrimSuffix(dom, "."))
	q.Type = typeName(uint16(t))
	q.Ms = m
	q.Source = SrcUpstream
	if m <= 1 {
		q.Source = SrcCache
	}
	if _, res, ok := strings.Cut(rest, ", result "); ok {
		res = strings.TrimSpace(res)
		switch {
		case res == "":
			q.Answer = "empty"
		case strings.Contains(res, "soa"):
			q.Answer = "empty"
		default:
			parts := strings.Split(res, ", ")
			if len(parts) > 2 {
				parts = append(parts[:2], "…")
			}
			q.Answer = strings.Join(parts, ", ")
		}
	}
	return q, true
}

// smartConf: the configuration for the current settings.
func (s *Service) smartConf() string {
	set := s.Settings()
	iface := ""
	if set.Via != ViaDirect {
		iface = s.sd.o.WarpIface()
	}
	host, _, _ := net.SplitHostPort(s.o.Listen)
	allow := []string{"127.0.0.1"}
	if ip := net.ParseIP(host); ip != nil && ip.To4() != nil && !ip.IsLoopback() && !ip.IsUnspecified() {
		allow = append([]string{host}, allow...)
	}
	var rs []Resolver
	for _, id := range set.Resolvers {
		if r := s.resolverByID(id); r != nil {
			rs = append(rs, *r)
		}
	}
	return SmartDNSConf{
		Listen: s.o.Listen, Allow: allow, Resolvers: rs, Iface: iface,
		Cache: set.Cache, Dir: s.sd.o.Dir,
	}.Render()
}

func (s *Service) smartState() *SmartDNSState {
	if s.sd == nil {
		return nil
	}
	st := &SmartDNSState{Installed: s.sd.installed(), Version: s.sd.ver()}
	st.Running, st.Error = s.sd.state()
	if s.Settings().Via != ViaDirect {
		st.Iface = s.sd.o.WarpIface()
	}
	st.Fallback = st.Iface != ""
	return st
}
