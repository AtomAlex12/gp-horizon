package dns

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSmartDNSConf(t *testing.T) {
	c := SmartDNSConf{
		Listen: "192.168.1.1:53053", Allow: []string{"192.168.1.1", "127.0.0.1"},
		Resolvers: Catalog[:2], Iface: "opkgtun0", Cache: true, Dir: "/tmp/smartdns-nuxk",
	}
	got := c.Render()
	for _, want := range []string{
		"bind 192.168.1.1:53053\n",
		"bind-tcp 192.168.1.1:53053\n",
		"acl-enable yes\nclient-rules 192.168.1.1/32\nclient-rules 127.0.0.1/32\n",
		"speed-check-mode none\n",
		"serve-expired yes\n",
		"cache-persist no\n",
		"audit-file /tmp/smartdns-nuxk/audit.log\n",
		// through WARP, at a fixed address: SmartDNS never asks DNS itself
		"server-https https://1.1.1.1/dns-query -host-name cloudflare-dns.com -http-host cloudflare-dns.com -tls-host-verify cloudflare-dns.com -interface opkgtun0\n",
		"server-https https://8.8.8.8/dns-query -host-name dns.google -http-host dns.google -tls-host-verify dns.google -interface opkgtun0\n",
		// straight, only when WARP's haven't answered
		"server-https https://1.1.1.1/dns-query -host-name cloudflare-dns.com -http-host cloudflare-dns.com -tls-host-verify cloudflare-dns.com -fallback\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in\n%s", want, got)
		}
	}

	c.Iface, c.Cache = "", false
	got = c.Render()
	if strings.Contains(got, "-interface") || strings.Contains(got, "-fallback") {
		t.Errorf("straight: no tunnel, no fallback\n%s", got)
	}
	if !strings.Contains(got, "cache-size 0\n") || !strings.Contains(got, "serve-expired no\n") {
		t.Errorf("cache off\n%s", got)
	}
}

func TestParseAudit(t *testing.T) {
	for _, c := range []struct {
		line string
		ok   bool
		want LogEntry
	}{
		{"[2026-10-04 21:14:08,123] 192.168.1.1 query Example.COM, type 1, time 34ms, speed: -0.1ms, group default, result 93.184.215.14", true,
			LogEntry{Domain: "example.com", Type: "A", Ms: 34, Source: SrcUpstream, Answer: "93.184.215.14"}},
		{"[2026-10-04 21:14:08,123] 192.168.1.1 query i.instagram.com, type 1, time 0ms, speed: -0.1ms, group default, result 157.240.205.63, 157.240.205.64, 157.240.205.65", true,
			LogEntry{Domain: "i.instagram.com", Type: "A", Ms: 0, Source: SrcCache, Answer: "157.240.205.63, 157.240.205.64, …"}},
		{"[2026-10-04 21:14:09,001] 127.0.0.1 query youtube.com, type 28, time 12ms, speed: -0.1ms, group default, result soa", true,
			LogEntry{Domain: "youtube.com", Type: "AAAA", Ms: 12, Source: SrcUpstream, Answer: "empty"}},
		{"[2026-10-04 21:14:09,001] 127.0.0.1 query x.com, type 65, time 40ms, speed: -0.1ms, group default, result ", true,
			LogEntry{Domain: "x.com", Type: "HTTPS", Ms: 40, Source: SrcUpstream, Answer: "empty"}},
		{"garbage", false, LogEntry{}},
		{"[x] 1.2.3.4 query a.b, type z, time 1ms", false, LogEntry{}},
	} {
		got, ok := parseAudit(c.line)
		if ok != c.ok || got != c.want {
			t.Errorf("%q:\n got %+v %v\nwant %+v %v", c.line, got, ok, c.want, c.ok)
		}
	}
}

func TestRecorder(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	r := newRecorder(func() time.Time { return now })
	base := now.Unix()
	r.add(LogEntry{At: base - 125, Domain: "old.example", Type: "A", Source: SrcUpstream, Ms: 10})
	r.add(LogEntry{At: base - 2, Domain: "a.example", Type: "A", Source: SrcCache})
	r.add(LogEntry{At: base - 1, Domain: "a.example", Type: "AAAA", Source: SrcStale})
	r.add(LogEntry{At: base, Domain: "b.example", Type: "A", Source: SrcUpstream, Ms: 30})
	r.add(LogEntry{At: base, Domain: "c.example", Type: "A", Source: SrcFailed})
	r.add(LogEntry{At: base - 4000, Domain: "gone.example", Type: "A", Source: SrcCache}) // over an hour ago

	st := r.stats()
	if len(st.Minutes) != statMinutes || st.Minutes[statMinutes-1].T != base-base%60 {
		t.Fatalf("minutes %d, last %d", len(st.Minutes), st.Minutes[statMinutes-1].T)
	}
	if st.Queries != 5 || st.Cache != 1 || st.Stale != 1 || st.Upstream != 2 || st.Failed != 1 || st.AvgMs != 20 {
		t.Fatalf("%+v", st)
	}
	if st.Top[0] != (Count{"a.example", 2}) || st.Types[0] != (Count{"A", 4}) {
		t.Fatalf("top %+v types %+v", st.Top, st.Types)
	}
	log := r.log("", 3)
	if len(log) != 3 || log[0].Domain != "gone.example" || log[1].Domain != "c.example" {
		t.Fatalf("newest first: %+v", log)
	}
	if l := r.log("a.ex", 10); len(l) != 2 {
		t.Fatalf("filtered: %+v", l)
	}
	for i := 0; i < statRecent+5; i++ {
		r.add(LogEntry{At: base, Domain: "n.example", Source: SrcCache})
	}
	if l := r.log("", 500); len(l) != statRecent || l[0].Domain != "n.example" {
		t.Fatalf("the ring: %d", len(l))
	}
}

// smartFake: an init script that records what it's asked, and a «SmartDNS»
// on the address that answers while the script has it started.
type smartFake struct {
	dir   string
	opts  *SmartDNSOptions
	calls func() string
	conf  func() string
}

func newSmartFake(t *testing.T, addr string, refuse bool) *smartFake {
	t.Helper()
	dir := t.TempDir()
	up := filepath.Join(dir, "up")
	start := "touch " + up
	if refuse { // starts, but never answers
		start = ":"
	}
	script := filepath.Join(dir, "S53smartdns-nuxk")
	body := "#!/bin/sh\necho \"$1\" >>" + dir + "/calls\ncase \"$1\" in\n" +
		"set-config) cat >" + dir + "/smartdns.conf; " + start + " ;;\n" +
		"up|restart) " + start + " ;;\n" +
		"down) rm -f " + up + " ;;\n" +
		"info) echo 'version Release48.4' ;;\n" +
		"esac\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	f := &smartFake{dir: dir, opts: &SmartDNSOptions{Script: script, Dir: dir, WarpIface: func() string { return "opkgtun0" }}}
	f.calls = func() string {
		b, _ := os.ReadFile(filepath.Join(dir, "calls"))
		return strings.Join(strings.Fields(string(b)), " ")
	}
	f.conf = func() string {
		b, _ := os.ReadFile(filepath.Join(dir, "smartdns.conf"))
		return string(b)
	}
	return f
}

// serve answers on addr while the fake is started.
func (f *smartFake) serve(t *testing.T, addr string) {
	t.Helper()
	pc := listenUDP(t, addr)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if _, err := os.Stat(filepath.Join(f.dir, "up")); err != nil {
				continue
			}
			q := append([]byte{}, buf[:n]...)
			_, _ = pc.WriteTo(respond(q, zone[QName(q)]...), from)
		}
	}()
}

func TestSmartDNSEnable(t *testing.T) {
	srv, _ := dohServer(t)
	var dead atomic.Bool
	h := &fakeHook{}
	addr := freeAddr(t)
	proxy := fakeProxy(t, addr, &dead)
	f := newSmartFake(t, addr, false)
	f.serve(t, addr)
	s := newService(t, srv, Options{Listen: addr, Hook: h, RouterDNS: proxy, SmartDNS: f.opts})
	s.o.Resolvers[0].IPs = []string{"192.0.2.53"}

	st, err := s.SetSettings(context.Background(), Settings{Enabled: true, Engine: EngineSmartDNS})
	if err != nil {
		t.Fatal(err)
	}
	if st.Settings.Engine != EngineSmartDNS || !st.Attached || !st.Running || st.SmartDNS == nil ||
		!st.SmartDNS.Installed || st.SmartDNS.Version != "Release48.4" || st.SmartDNS.Iface != "opkgtun0" {
		t.Fatalf("%+v %+v", st, st.SmartDNS)
	}
	if f.calls() != "set-config info" || !strings.Contains(f.conf(), "bind "+addr+"\n") || h.log() != "attach "+addr {
		t.Fatalf("calls %q hook %q conf\n%s", f.calls(), h.log(), f.conf())
	}
	// the built-in forwarder doesn't listen meanwhile: the address is SmartDNS's
	s.mu.Lock()
	listening := s.udp != nil
	s.mu.Unlock()
	if listening {
		t.Fatal("the forwarder listens next to SmartDNS")
	}

	// the way out changed: a new configuration, straight
	if _, err := s.SetSettings(context.Background(), Settings{Enabled: true, Via: ViaDirect}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.conf(), "-interface") || f.calls() != "set-config info set-config" {
		t.Fatalf("calls %q conf\n%s", f.calls(), f.conf())
	}

	st, err = s.SetSettings(context.Background(), Settings{Enabled: false})
	if err != nil || st.Attached || !strings.HasSuffix(f.calls(), " down") || !strings.HasSuffix(h.log(), "|detach "+addr) {
		t.Fatalf("off: %v %+v calls %q hook %q", err, st, f.calls(), h.log())
	}
}

// Switching while on: the router's DNS proxy isn't touched; SmartDNS that
// doesn't answer goes, the built-in forwarder comes back.
func TestSmartDNSSwitchBack(t *testing.T) {
	srv, _ := dohServer(t)
	var dead atomic.Bool
	h := &fakeHook{}
	addr := freeAddr(t)
	proxy := fakeProxy(t, addr, &dead)
	f := newSmartFake(t, addr, true)
	s := newService(t, srv, Options{Listen: addr, Hook: h, RouterDNS: proxy, SmartDNS: f.opts})
	s.warm = time.Second
	if _, err := s.SetSettings(context.Background(), Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	st, err := s.SetSettings(context.Background(), Settings{Enabled: true, Engine: EngineSmartDNS})
	if err == nil || !strings.Contains(err.Error(), "вернул прежний") {
		t.Fatalf("err %v", err)
	}
	if st.Settings.Engine != EngineNuxk || !st.Running || !st.Attached || h.log() != "attach "+addr {
		t.Fatalf("%+v hook %q", st, h.log())
	}
	if !strings.HasPrefix(f.calls(), "set-config") || !strings.HasSuffix(f.calls(), "down") {
		t.Fatalf("calls %q", f.calls())
	}
	ask(t, "udp", addr, "cloudflare.com") // the forwarder answers again
}

func TestSmartDNSNotInstalled(t *testing.T) {
	srv, _ := dohServer(t)
	s := newService(t, srv, Options{Hook: &fakeHook{}, SmartDNS: &SmartDNSOptions{Script: "/nonexistent/S53smartdns-nuxk"}})
	if _, err := s.SetSettings(context.Background(), Settings{Engine: EngineSmartDNS}); !errors.Is(err, ErrNoSmartDNS) {
		t.Fatalf("err %v", err)
	}
	if st := s.Status(); st.SmartDNS == nil || st.SmartDNS.Installed || st.Settings.Engine != EngineNuxk {
		t.Fatalf("%+v", st.SmartDNS)
	}
	if st := New(Options{}, nil).Status(); st.SmartDNS != nil {
		t.Fatal("not offered: no smartdns in the status")
	}
}

// The audit log, read as it grows: what was there before is skipped, a
// half-written line waits, a rotated log is read from its start.
func TestSmartDNSTail(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1_790_000_000, 0)
	d := newSmartDNS(&SmartDNSOptions{Script: "x", Dir: dir})
	rec := newRecorder(func() time.Time { return now })
	line := func(dom string, ms int) string {
		return "[2026-10-04 21:14:08,123] 192.168.1.1 query " + dom + ", type 1, time " + itoa(ms) + "ms, speed: -0.1ms, group default, result 192.0.2.1\n"
	}
	p := filepath.Join(dir, "audit.log")
	write := func(s string) {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.WriteString(s)
		f.Close()
	}
	d.tail(rec, now) // no log yet
	write(line("before.example", 30))
	d.tail(rec, now) // skipped: from before the agent
	write(line("a.example", 0) + line("b.example", 25) + "[2026-10-04 21:14:09,000] 192.168.1.1 query half")
	d.tail(rec, now)
	if st := rec.stats(); st.Queries != 2 || st.Cache != 1 || st.Upstream != 1 {
		t.Fatalf("%+v", st)
	}
	write(".example, type 1, time 5ms, speed: -0.1ms, group default, result 192.0.2.2\n")
	d.tail(rec, now)
	if l := rec.log("", 1); len(l) != 1 || l[0].Domain != "half.example" {
		t.Fatalf("%+v", l)
	}
	// rotated: a new, shorter file
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	write(line("c.example", 9))
	d.tail(rec, now)
	if st := rec.stats(); st.Queries != 4 {
		t.Fatalf("after rotation %+v", st)
	}
}

func listenUDP(t *testing.T, addr string) net.PacketConn {
	t.Helper()
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	return pc
}
