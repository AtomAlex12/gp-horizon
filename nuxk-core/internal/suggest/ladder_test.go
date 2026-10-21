package suggest

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"nuxk.dev/horizon/core/internal/probe"
)

func TestLadder(t *testing.T) {
	const (
		D = probe.ViaDirect
		W = probe.ViaWarp
		V = probe.ViaVless
	)
	type ways = map[probe.Via]probe.Result
	for _, c := range []struct {
		name    string
		desync  bool
		ways    ways
		skip    []probe.Via
		verdict string
		mode    string
		alt     string
		ok      bool     // confirmed
		walked  []string // ways measured, in order
		reason  string   // a piece of the reason
	}{
		{name: "opens directly", ways: ways{D: opened(300, 5000)},
			verdict: "ok", walked: []string{"direct"}},

		// geo: VLESS only, WARP never — it leaves the network in Moscow
		{name: "geo, vless opens", desync: true, ways: ways{D: geoRefused(), V: opened(400, 0), W: opened(100, 0)},
			verdict: "geoblock", mode: "vless", ok: true, walked: []string{"direct", "vless"}, reason: "Москве"},
		{name: "geo, vless unavailable", ways: ways{D: geoRefused(), W: opened(100, 0)}, skip: []probe.Via{V},
			verdict: "geoblock", mode: "vless", walked: []string{"direct", "vless"}, reason: "непроверенный"},
		{name: "geo, vless slow", ways: ways{D: geoRefused(), V: opened(6000, 0)},
			verdict: "geoblock", mode: "vless", walked: []string{"direct", "vless"}, reason: "первый байт"},
		{name: "geo, vless fails too", ways: ways{D: geoRefused()},
			verdict: "geoblock", walked: []string{"direct", "vless"}},
		{name: "challenge confirmed by vless", ways: ways{D: challenged(), V: opened(400, 0)},
			verdict: "geoblock", mode: "vless", ok: true, walked: []string{"direct", "vless"}},
		{name: "challenge, nothing to compare", ways: ways{D: challenged()}, skip: []probe.Via{V},
			verdict: "unknown", walked: []string{"direct", "vless"}},

		// provider: DPI first (unconfirmed), WARP as the verified way out, VLESS only when WARP doesn't do
		{name: "sni, dpi, warp opens", desync: true, ways: ways{D: sniCut(), W: opened(300, 0), V: opened(300, 0)},
			verdict: "sni-block", mode: "desync", alt: "warp", walked: []string{"direct", "warp"}, reason: "nfqws2"},
		{name: "sni, dpi, warp fails, vless opens", desync: true, ways: ways{D: sniCut(), V: opened(300, 0)},
			verdict: "sni-block", mode: "desync", alt: "vless", walked: []string{"direct", "warp", "vless"}},
		{name: "sni, dpi, no tunnels", desync: true, ways: ways{D: sniCut()}, skip: []probe.Via{W, V},
			verdict: "sni-block", mode: "desync", walked: []string{"direct", "warp", "vless"}},
		{name: "sni, no dpi, warp opens", ways: ways{D: sniCut(), W: opened(300, 0), V: opened(300, 0)},
			verdict: "sni-block", mode: "warp", ok: true, walked: []string{"direct", "warp"}},
		{name: "sni, no dpi, vless after warp", ways: ways{D: sniCut(), V: opened(300, 0)},
			verdict: "sni-block", mode: "vless", ok: true, walked: []string{"direct", "warp", "vless"}},
		{name: "sni, no dpi, warp slow", ways: ways{D: sniCut(), W: opened(7000, 0)}, skip: []probe.Via{V},
			verdict: "sni-block", mode: "warp", walked: []string{"direct", "warp", "vless"}},
		{name: "sni, nothing usable", ways: ways{D: sniCut()}, skip: []probe.Via{W, V},
			verdict: "sni-block", walked: []string{"direct", "warp", "vless"}, reason: "nfqws2 не подключён"},
		{name: "ip cut after tcp, dpi", desync: true, ways: ways{D: ipCutAfterTCP(), W: opened(300, 0)},
			verdict: "ip-block", mode: "desync", alt: "warp", walked: []string{"direct", "warp"}},
		{name: "no tcp: dpi can't help", desync: true, ways: ways{D: refused(), W: opened(300, 0)},
			verdict: "ip-block", mode: "warp", ok: true, walked: []string{"direct", "warp"}, reason: "DPI тут не поможет"},
		{name: "no tcp anywhere", desync: true, ways: ways{D: refused()},
			verdict: "unknown", walked: []string{"direct", "warp", "vless"}},
		{name: "freeze, dpi", desync: true, ways: ways{D: frozen(), W: opened(300, 0)},
			verdict: "freeze", mode: "desync", alt: "warp", walked: []string{"direct", "warp"}, reason: "16 КБ"},
		{name: "freeze, no dpi", ways: ways{D: frozen(), W: opened(300, 0)},
			verdict: "freeze", mode: "warp", ok: true, walked: []string{"direct", "warp"}},
		{name: "slow, dpi, warp better", desync: true, ways: ways{D: opened(6000, 0), W: opened(300, 0)},
			verdict: "slow", mode: "desync", alt: "warp", walked: []string{"direct", "warp"}},
		{name: "slow everywhere", desync: true, ways: ways{D: opened(6000, 0), W: opened(6500, 0), V: opened(7000, 0)},
			verdict: "slow", walked: []string{"direct", "warp", "vless"}, reason: "в самом сайте"},
		{name: "slow, nothing to compare", desync: true, ways: ways{D: opened(6000, 0)}, skip: []probe.Via{W, V},
			verdict: "slow", walked: []string{"direct", "warp", "vless"}},
		{name: "too slow a download", ways: ways{D: opened(300, 800), W: opened(300, 9000)},
			verdict: "slow", mode: "warp", ok: true, walked: []string{"direct", "warp"}},
		{name: "flaky, no dpi", ways: ways{D: flaky(), W: opened(300, 0)},
			verdict: "slow", mode: "warp", ok: true, walked: []string{"direct", "warp"}, reason: "2 из 5"},
		{name: "server error is the site's", desync: true, ways: ways{D: broken(), W: opened(300, 0)},
			verdict: "unknown", walked: []string{"direct"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := newFake()
			for via, r := range c.ways {
				p.set("x.example", via, r)
			}
			for _, via := range c.skip {
				p.skip[via] = "интерфейс не найден"
			}
			b := bar{DefaultSettings(), Reference{Domain: "ya.ru", OK: true, TTFBMs: 1000, Kbps: 0}}
			it := decide(context.Background(), p, b, c.desync, "x.example")
			if it.Verdict != c.verdict || it.Mode != c.mode || it.Alt != c.alt || it.Confirmed != c.ok {
				t.Errorf("got verdict=%s mode=%q alt=%q confirmed=%v (%s)", it.Verdict, it.Mode, it.Alt, it.Confirmed, it.Reason)
			}
			if got := p.walked("x.example"); !reflect.DeepEqual(got, c.walked) {
				t.Errorf("walked %v, want %v", got, c.walked)
			}
			if len(it.Paths) != len(c.walked) {
				t.Errorf("paths %+v", it.Paths)
			}
			if !strings.Contains(it.Reason, c.reason) || it.Reason == "" {
				t.Errorf("reason %q lacks %q", it.Reason, c.reason)
			}
			if c.mode == "" && c.verdict == "geoblock" && strings.Contains(it.Reason, "WARP") && !strings.Contains(it.Reason, "Москве") {
				t.Errorf("geo must not point at WARP: %q", it.Reason)
			}
		})
	}
}

func TestLadderDNS(t *testing.T) {
	p := newFake()
	p.dns["x.example"] = "bogus"
	it := decide(context.Background(), p, bar{DefaultSettings(), Reference{}}, true, "x.example")
	if it.Verdict != "dns-block" || it.Mode != "" || !strings.Contains(it.Reason, "защищённый DNS") || len(p.walked("x.example")) != 0 {
		t.Fatalf("%+v", it)
	}
	p.dns["x.example"] = "nxdomain"
	if it := decide(context.Background(), p, bar{DefaultSettings(), Reference{}}, true, "x.example"); it.Verdict != "unknown" {
		t.Fatalf("%+v", it)
	}
}

func TestJudge(t *testing.T) {
	set := DefaultSettings()
	withRef := bar{set, Reference{Domain: "ya.ru", OK: true, ConnectMs: 10, TTFBMs: 100, Kbps: 20000}}
	at := func(connectMs int, r probe.Result) probe.Result { r.ConnectMs = connectMs; return r }
	noRef := bar{set, Reference{Domain: "ya.ru"}}
	for _, c := range []struct {
		name string
		b    bar
		m    probe.Result
		ok   bool
	}{
		{"fast", noRef, opened(300, 0), true},
		{"4 of 5", noRef, probe.Result{TLS: true, Status: 200, Tries: 5, Opened: 4, TTFBMs: 300}, true},
		{"3 of 5", noRef, probe.Result{TLS: true, Status: 200, Tries: 5, Opened: 3, TTFBMs: 300}, false},
		{"the first try refused, 4 of 5 opened", noRef, probe.Result{TLS: true, Status: 503, Tries: 5, Opened: 4, TTFBMs: 300}, true},
		{"a challenge first, 4 of 5 opened", noRef, probe.Result{TLS: true, Status: 403, Signal: probe.SignalChallenge, Tries: 5, Opened: 4, TTFBMs: 300}, true},
		{"frozen", noRef, frozen(), false},
		{"skipped", noRef, probe.Result{Skipped: "нет"}, false},
		{"not measured", noRef, probe.Result{}, false},
		{"refused by the site", noRef, geoRefused(), false},
		{"first byte past the limit", noRef, opened(5001, 0), false},
		{"first byte at the limit", noRef, opened(5000, 0), true},
		{"3x the reference + 500 is fine", withRef, opened(800, 0), true},
		{"past 3x the reference + 500", withRef, opened(801, 0), false},
		{"reference not known: only the limit", noRef, opened(4000, 0), true},
		{"speed not measurable: not judged", withRef, opened(300, 0), true},
		{"below the speed floor", noRef, opened(300, 1999), false},
		{"above the floor, no reference", noRef, opened(300, 2000), true},
		{"below 30% of the reference", withRef, at(40, opened(300, 5999)), false},
		{"30% of the reference", withRef, at(40, opened(300, 6000)), true},
		{"far away: only the floor", withRef, at(41, opened(300, 2000)), true},
		{"far away, below the floor", withRef, at(200, opened(300, 1999)), false},
		{"connect time unknown: only the floor", withRef, opened(300, 2000), true},
		{"interrupted", noRef, probe.Result{Skipped: probe.Interrupted, Tries: 1, Opened: 1}, false},
	} {
		if ok, why := c.b.judge(c.m); ok != c.ok || ok != (why == "") {
			t.Errorf("%s: got %v %q, want %v", c.name, ok, why, c.ok)
		}
	}
}
