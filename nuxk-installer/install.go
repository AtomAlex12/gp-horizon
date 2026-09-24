package main

import (
	"bytes"
	"crypto/rand"
	_ "embed"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

//go:embed router/detect.sh
var detectScript string

// Router paths (the connection's root prefix is applied by Conn).
const (
	pBin      = "/opt/usr/bin/nuxk-core"
	pInit     = "/opt/etc/init.d/S99nuxk-core"
	pShim     = "/opt/etc/nuxk/engines/S51nfqws2-nuxk"
	pConf     = "/opt/etc/nuxk/nuxk.conf"
	pWeb      = "/opt/share/www/nuxk"
	pLog      = "/opt/var/log/nuxk-core.log"
	pFeedConf = "/opt/etc/opkg/nfqws2-keenetic.conf"
	pUsque    = "/opt/etc/init.d/S51usque"

	nfqwsFeed = "src/gz nfqws2-keenetic https://nfqws.github.io/nfqws2-keenetic/all"
)

// Detect surveys the router (read-only).
func Detect(c *Conn) (Report, error) {
	out, err := c.Output("sh -s", strings.NewReader(detectScript))
	if err != nil && out == "" {
		return Report{}, fmt.Errorf("проверка роутера не выполнилась: %w", err)
	}
	return ParseReport(out, time.Now()), nil
}

// Event is one line of install progress sent to the browser.
type Event struct {
	Kind string `json:"kind"` // step | out | ok | fail | info | done
	Text string `json:"text"`
	// done only:
	OK    bool   `json:"ok,omitempty"`
	URL   string `json:"url,omitempty"`
	Token string `json:"token,omitempty"`
}

// Result of a finished install.
type Result struct {
	OK    bool
	URL   string
	Token string
}

// Install re-surveys the router, then runs the selected steps in order.
// It stops at the first failed step; everything before it stays applied.
func Install(c *Conn, p *Payload, selected []string, emit func(Event)) (Result, error) {
	emit(Event{Kind: "step", Text: "Повторная проверка роутера"})
	r, err := Detect(c)
	if err != nil {
		return Result{}, err
	}
	plan := BuildPlan(r, p)
	if plan.Blocked {
		return Result{}, errors.New("установка заблокирована: исправьте пункты с пометкой «блокирует» и проверьте роутер заново")
	}
	want := map[string]bool{}
	for _, id := range selected {
		want[id] = true
	}
	can := map[string]Item{}
	for _, it := range plan.Items {
		if it.Selectable {
			can[it.ID] = it
		}
	}
	emit(Event{Kind: "ok", Text: fmt.Sprintf("%s · %s · свободно %d МБ", firstNonEmpty(r.Model, "роутер"), r.ArchRaw, r.OptFreeKB/1024)})

	steps := map[string]func() error{
		"deps":   func() error { return stepDeps(c, r, emit) },
		"nfqws2": func() error { return stepNfqws2(c, emit) },
		"core":   func() error { return stepCore(c, p, r, emit) },
		"config": func() error { return stepConfig(c, r, p.Version(), emit) },
		"start":  func() error { return stepStart(c, r, emit) },
	}
	for _, id := range stepOrder {
		it, ok := can[id]
		if !ok || !want[id] {
			continue
		}
		emit(Event{Kind: "step", Text: it.Title})
		if err := steps[id](); err != nil {
			emit(Event{Kind: "fail", Text: err.Error()})
			return Result{}, fmt.Errorf("%s: %w", it.Title, err)
		}
		emit(Event{Kind: "ok", Text: it.Title + " — готово"})
	}

	token, listen := readConf(c)
	res := Result{OK: true, Token: token, URL: "http://" + publicAddr(listen, r) + "/"}
	return res, nil
}

func run(c *Conn, emit func(Event), cmd string) error {
	err := c.Run(cmd, nil, func(l string) { emit(Event{Kind: "out", Text: l}) })
	var ee *ssh.ExitError
	if errors.As(err, &ee) {
		return fmt.Errorf("команда завершилась с кодом %d — подробности в строках выше", ee.ExitStatus())
	}
	return err
}

func stepDeps(c *Conn, r Report, emit func(Event)) error {
	var miss []string
	for _, p := range depPkgs {
		if r.Pkgs[p] == "" {
			miss = append(miss, p)
		}
	}
	if len(miss) == 0 {
		return nil
	}
	return run(c, emit, "opkg update && opkg install "+strings.Join(miss, " "))
}

func stepNfqws2(c *Conn, emit func(Event)) error {
	cmd := fmt.Sprintf("mkdir -p \"$(dirname %s)\" && echo %s > %s && opkg update && opkg install nfqws2-keenetic",
		c.P(pFeedConf), shQuote(nfqwsFeed), c.P(pFeedConf))
	return run(c, emit, cmd)
}

func stepCore(c *Conn, p *Payload, r Report, emit func(Event)) error {
	bin, err := p.Read("nuxk-core-" + r.Arch)
	if err != nil {
		return fmt.Errorf("в инсталляторе нет nuxk-core-%s", r.Arch)
	}
	// stop first: a running binary can't be overwritten on some filesystems
	_ = run(c, emit, fmt.Sprintf("[ -x %s ] && %s stop; [ -f %s ] && cp -f %s %s.prev; true",
		c.P(pInit), c.P(pInit), c.P(pBin), c.P(pBin), c.P(pBin)))
	emit(Event{Kind: "out", Text: fmt.Sprintf("nuxk-core-%s → %s (%d КБ)", r.Arch, pBin, len(bin)/1024)})
	if err := c.Upload(pBin, bin, "0755"); err != nil {
		return err
	}
	for _, f := range []struct{ src, dst string }{{"S99nuxk-core", pInit}, {"S51nfqws2-nuxk", pShim}} {
		b, err := p.Read(f.src)
		if err != nil {
			return err
		}
		emit(Event{Kind: "out", Text: f.src + " → " + f.dst})
		if err := c.Upload(f.dst, b, "0755"); err != nil {
			return err
		}
	}
	files, err := p.WebFiles()
	if err != nil {
		return err
	}
	if err := run(c, emit, fmt.Sprintf("rm -rf %s && mkdir -p %s", c.P(pWeb), c.P(pWeb))); err != nil {
		return err
	}
	for _, f := range files {
		b, err := p.Read("web/" + f)
		if err != nil {
			return err
		}
		if err := c.Upload(pWeb+"/"+f, b, "0644"); err != nil {
			return err
		}
	}
	emit(Event{Kind: "out", Text: fmt.Sprintf("веб-интерфейс: %d файлов → %s", len(files), pWeb)})
	out, err := c.Output(c.P(pBin)+" -version", nil)
	if err != nil {
		return fmt.Errorf("nuxk-core не запускается на этом роутере: %w", err)
	}
	emit(Event{Kind: "out", Text: strings.TrimSpace(out)})
	return nil
}

func stepConfig(c *Conn, r Report, version string, emit func(Event)) error {
	usque := ""
	if r.UsqueReady {
		usque = pUsque
	}
	conf := fmt.Sprintf(`# nuxk-core — written by nuxk-installer %s on %s.
# Shell-sourceable KEY="value". Edits are kept: the installer never rewrites
# an existing file.

# LAN address only: reachable from the home network, never bound on WAN.
LISTEN="%s"

# Bearer token for the web UI and API from the LAN. Keep it secret.
API_TOKEN="%s"

STATE_DIR="/opt/etc/nuxk"
WEB_ROOT="%s"

# Engines: empty = disabled. nfqws2 goes through the nuxk adapter shim over
# the stock nfqws2-keenetic package.
ENGINE_NFQWS2="%s"
ENGINE_USQUE="%s"
ENGINE_XRAY=""

# A router CPU is slow: poll engines every 10 s, probe every 2 min.
INFO_EVERY="10"
PROBE_EVERY="120"
`, version, time.Now().Format("2006-01-02"), listenAddr(r), newToken(), pWeb, pShim, usque)
	emit(Event{Kind: "out", Text: pConf + " (права 0600)"})
	return c.Upload(pConf, []byte(conf), "0600")
}

func stepStart(c *Conn, r Report, emit func(Event)) error {
	token, listen := readConf(c)
	if token == "" && !isLoopback(listen) {
		// a LAN-bound API without a token refuses every browser request
		emit(Event{Kind: "info", Text: "в nuxk.conf пустой API_TOKEN — задаю новый"})
		if err := run(c, emit, fmt.Sprintf(`sed -i 's/^API_TOKEN=.*/API_TOKEN="%s"/' %s`, newToken(), c.P(pConf))); err != nil {
			return err
		}
	}
	if err := run(c, emit, c.P(pInit)+" restart"); err != nil {
		return err
	}
	health := "127.0.0.1:4141"
	if listen != "" {
		health = loopbackFor(listen)
	}
	cmd := fmt.Sprintf("i=0; while [ $i -lt 15 ]; do curl -fsS -m 2 http://%s/api/v1/healthz && exit 0; i=$((i+1)); sleep 1; done; exit 1", health)
	if err := run(c, emit, cmd); err != nil {
		_ = run(c, emit, "tail -n 20 "+c.P(pLog)+" 2>/dev/null")
		return errors.New("nuxk-core не ответил на /api/v1/healthz за 15 секунд — последние строки лога выше")
	}
	return nil
}

// readConf returns API_TOKEN and LISTEN from the router's nuxk.conf.
func readConf(c *Conn) (token, listen string) {
	out, _ := c.Output(fmt.Sprintf(`grep -E '^(API_TOKEN|LISTEN)=' %s 2>/dev/null; true`, c.P(pConf)), nil)
	for _, l := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"'`)
		switch k {
		case "API_TOKEN":
			token = v
		case "LISTEN":
			listen = v
		}
	}
	return token, listen
}

// publicAddr is the address to open in a browser.
func publicAddr(listen string, r Report) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return firstNonEmpty(r.LANIP, "192.168.1.1") + ":4141"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = firstNonEmpty(r.LANIP, "192.168.1.1")
	}
	return net.JoinHostPort(host, port)
}

// loopbackFor turns a LISTEN value into an address curl on the router can hit.
func loopbackFor(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "127.0.0.1:4141"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

func isLoopback(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// newToken is 128 bits, lowercase base32 without padding (26 chars).
func newToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	const alpha = "abcdefghijklmnopqrstuvwxyz234567"
	var out bytes.Buffer
	var acc, bits uint
	for _, x := range b {
		acc = acc<<8 | uint(x)
		bits += 8
		for bits >= 5 {
			bits -= 5
			out.WriteByte(alpha[(acc>>bits)&31])
		}
	}
	if bits > 0 {
		out.WriteByte(alpha[(acc<<(5-bits))&31])
	}
	return out.String()
}
