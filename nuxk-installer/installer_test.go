package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// ---------------------------------------------------------------------------
// unit: report parsing and arch mapping

func TestArchFor(t *testing.T) {
	for _, c := range []struct{ opkg, uname, endian, want string }{
		{"mipsel-3.4", "mips", "", "mipsel"},
		{"", "mips", "1", "mipsel"},
		{"", "mips", "2", "mips"},
		{"", "mips", "", ""},
		{"mips-3.4", "mips", "", "mips"},
		{"aarch64-3.10", "aarch64", "", "aarch64"},
		{"x64-3.2", "x86_64", "", "x86_64"},
		{"mips64-3.4", "mips64", "", ""},
		{"armv7-3.2", "armv7l", "", ""},
	} {
		if got := archFor(c.opkg, c.uname, c.endian); got != c.want {
			t.Errorf("archFor(%q,%q,%q) = %q, want %q", c.opkg, c.uname, c.endian, got, c.want)
		}
	}
}

func TestParseReport(t *testing.T) {
	at := time.Unix(1_800_000_000, 0)
	r := ParseReport("opkg 1\nopkg_arch mipsel-3.4\nuname_m mips\nopt_free_kb 51200\nclock 1800000600\n"+
		"pkg.curl 8.9.1-1\npkg.ipset \nkmod.xt_NFQUEUE missing\ninit.S51usque 1\nnuxk_core 0.1.0-alpha.1\r\n", at)
	if r.Arch != "mipsel" || !r.Entware || r.OptFreeKB != 51200 || r.ClockSkew != 600 {
		t.Errorf("report = %+v", r)
	}
	if r.Pkgs["curl"] != "8.9.1-1" || r.Pkgs["ipset"] != "" || r.Kmods["xt_NFQUEUE"] != "missing" || !r.Init["S51usque"] || r.NuxkCore != "0.1.0-alpha.1" {
		t.Errorf("maps = %+v %+v %+v core=%q", r.Pkgs, r.Kmods, r.Init, r.NuxkCore)
	}
}

// ---------------------------------------------------------------------------
// unit: plan

func testPayload(t *testing.T, version string) (*Payload, string) {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string, mode os.FileMode) {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("VERSION", version+"\n", 0o644)
	write("nuxk-core-x86_64", "#!/bin/sh\n[ \"$1\" = -version ] && echo \"nuxk-core "+version+" abc123\"\n", 0o755)
	// a fake init: restart marks it running, status reports it
	write("S99nuxk-core", `#!/bin/sh
M="$(dirname "$0")/../../var/run/nuxk.running"
case "$1" in
restart|start) mkdir -p "$(dirname "$M")"; touch "$M"; echo "Started nuxk-core" ;;
stop) rm -f "$M"; echo "Stopped nuxk-core" ;;
status) [ -f "$M" ] && echo "nuxk-core is running (PID 1)" || echo "nuxk-core is stopped" ;;
esac
`, 0o755)
	shim, err := os.ReadFile("../engines/nuxk-nfqws2/S51nfqws2-nuxk")
	if err != nil {
		t.Fatal(err)
	}
	write("S51nfqws2-nuxk", string(shim), 0o755)
	xshim, err := os.ReadFile("../engines/nuxk-xray/S52xray-nuxk")
	if err != nil {
		t.Fatal(err)
	}
	write("S52xray-nuxk", string(xshim), 0o755)
	write("usque-keenetic-x86_64.ipk", "fake ipk", 0o644)
	write("web/index.html", "<!doctype html><title>nuxk</title>", 0o644)
	write("web/assets/app-1.js", "console.log(1)", 0o644)
	p, err := NewPayload(dir)
	if err != nil {
		t.Fatal(err)
	}
	return p, dir
}

func item(pl Plan, id string) Item {
	for _, it := range pl.Items {
		if it.ID == id {
			return it
		}
	}
	return Item{}
}

func TestPlanFreshRouter(t *testing.T) {
	p, _ := testPayload(t, "0.1.0-beta.1")
	r := Report{Entware: true, Arch: "x86_64", ArchRaw: "x64-3.2", OptFreeKB: 500000, LANIP: "192.168.2.1",
		Pkgs: map[string]string{"curl": "8"}, Kmods: map[string]string{"xt_NFQUEUE": "loaded"}, Init: map[string]bool{}}
	pl := BuildPlan(r, p)
	if pl.Blocked {
		t.Fatal("fresh router must not be blocked")
	}
	for _, id := range []string{"deps", "nfqws2", "core", "config", "start"} {
		if it := item(pl, id); it.Status != StInstall || !it.Selected {
			t.Errorf("%s = %+v, want install+selected", id, it)
		}
	}
	if d := item(pl, "deps").Detail; d != "opkg install ca-certificates ipset" {
		t.Errorf("deps detail = %q", d)
	}
	if d := item(pl, "config").Detail; !strings.Contains(d, "192.168.2.1:4141") {
		t.Errorf("config must listen on LAN: %q", d)
	}
}

func TestPlanUpToDate(t *testing.T) {
	p, _ := testPayload(t, "0.1.0-beta.1")
	r := Report{Entware: true, Arch: "x86_64", OptFreeKB: 500000, NuxkCore: "0.1.0-beta.1", NuxkConf: true, NuxkRunning: true,
		Pkgs: map[string]string{"curl": "1", "ca-certificates": "1", "ipset": "1", "nfqws2-keenetic": "2.1"}, Kmods: map[string]string{}, Init: map[string]bool{}}
	pl := BuildPlan(r, p)
	for _, it := range pl.Items {
		if it.Selected {
			t.Errorf("%s selected on an up-to-date router", it.ID)
		}
	}
	if item(pl, "core").Status != StOK || !item(pl, "core").Selectable {
		t.Error("core must be ok but re-installable")
	}
	r.NuxkCore = "0.1.0-alpha.1"
	if it := item(BuildPlan(r, p), "core"); it.Status != StUpgrade || !it.Selected {
		t.Errorf("older core = %+v, want upgrade", it)
	}
}

func TestPlanBlocked(t *testing.T) {
	p, _ := testPayload(t, "x")
	for name, r := range map[string]Report{
		"no entware": {Entware: false, Arch: "x86_64"},
		"arm":        {Entware: true, Arch: "", ArchRaw: "armv7"},
		"no space":   {Entware: true, Arch: "x86_64", OptFreeKB: 4096},
		"no binary":  {Entware: true, Arch: "mipsel", OptFreeKB: 500000},
	} {
		pl := BuildPlan(r, p)
		if !pl.Blocked {
			t.Errorf("%s: not blocked", name)
		}
		for _, it := range pl.Items {
			if it.Selected || it.Selectable {
				t.Errorf("%s: %s still selectable", name, it.ID)
			}
		}
	}
}

func TestPlanWarnsWithoutBlocking(t *testing.T) {
	p, _ := testPayload(t, "x")
	r := Report{Entware: true, Arch: "x86_64", OptFreeKB: 500000, ClockSkew: -3600,
		Kmods: map[string]string{"xt_NFQUEUE": "missing", "xt_connbytes": "available"}, Pkgs: map[string]string{}, Init: map[string]bool{}}
	pl := BuildPlan(r, p)
	if pl.Blocked || item(pl, "kmods").Status != StWarn || item(pl, "clock").Status != StWarn {
		t.Errorf("plan = %+v", pl.Items)
	}
	if !strings.Contains(item(pl, "kmods").Detail, "xt_NFQUEUE") || strings.Contains(item(pl, "kmods").Detail, "connbytes") {
		t.Errorf("kmods detail = %q", item(pl, "kmods").Detail)
	}
}

// ---------------------------------------------------------------------------
// end to end: real SSH server, fake router under a temp root

type fakeRouter struct {
	root, bin string
	addr      string
	hostKey   string
}

func newFakeRouter(t *testing.T) *fakeRouter {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "fakebin")
	for _, d := range []string{bin, "opt/etc/init.d", "opt/usr/bin", "opt/var/run", "opt/var/log"} {
		if err := os.MkdirAll(filepath.Join(root, strings.TrimPrefix(d, root)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	state := filepath.Join(root, "opkg.installed")
	must(t, os.WriteFile(state, []byte("curl - 8.9.1-1\n"), 0o644))
	fake := map[string]string{
		// opkg: print-architecture / list-installed / update / install, all logged
		"opkg": `#!/bin/sh
echo "opkg $*" >>"` + root + `/opkg.log"
S="` + state + `"
case "$1" in
print-architecture) echo "arch all 1"; echo "arch noarch 1"; echo "arch x64-3.2 10" ;;
list-installed) [ -n "$2" ] && grep "^$2 " "$S" || cat "$S" ;;
update) echo "Downloading ..." ;;
install) shift; for p in "$@"; do case "$p" in --*) continue ;; esac; echo "Installing $p"
  if [ "${p%.ipk}" != "$p" ]; then
    # usque-keenetic ipk: its postinst leaves an init with the nuxk contract and a tunnel
    [ "$(cat "$p")" = "fake ipk" ] || exit 1
    echo "usque-keenetic - 0.4.0" >>"$S"
    mkdir -p "` + root + `/opt/etc/usque"; echo 'IFACE="opkgtun0"' >"` + root + `/opt/etc/usque/usque.conf"
    printf '#!/bin/sh\n[ "$1" = info ] && { echo "service.running 1"; echo "tunnel.state connected"; }\ntrue\n' >"` + root + `/opt/etc/init.d/S51usque"
    chmod +x "` + root + `/opt/etc/init.d/S51usque"; continue
  fi
  echo "$p - 1.0-test" >>"$S";
  if [ "$p" = nfqws2-keenetic ]; then printf '#!/bin/sh\necho stock $1\n' >"` + root + `/opt/etc/init.d/S51nfqws2"; chmod +x "` + root + `/opt/etc/init.d/S51nfqws2"; fi; done ;;
esac
`,
		// ndmc: the version, and interfaces that remember their description
		"ndmc": `#!/bin/sh
echo "$2" >>"` + root + `/ndmc.log"
D="` + root + `/ndm"
case "$2" in
"show version") echo '   release: 4.3.1'; echo '     model: Keenetic Test' ;;
"show interface "*) f="$D/${2#show interface }"; [ -f "$f" ] || exit 1; echo "  description: $(cat "$f")" ;;
"interface "*" description "*) r=${2#interface }; mkdir -p "$D"; echo "${r#* description }" >"$D/${r%% *}" ;;
"interface "*) i=${2#interface }; mkdir -p "$D"; [ -f "$D/$i" ] || echo - >"$D/$i" ;;
"system configuration save") echo saved >>"` + root + `/ndm.saved" ;;
esac
`,
		"lsmod": "#!/bin/sh\necho 'nfnetlink_queue 1 0'\necho 'xt_NFQUEUE 1 0'\necho 'xt_connbytes 1 0'\necho 'xt_multiport 1 0'\n",
		"curl":  "#!/bin/sh\necho '{\"status\":\"ok\"}'\n",
	}
	for n, body := range fake {
		must(t, os.WriteFile(filepath.Join(bin, n), []byte(body), 0o755))
	}
	fr := &fakeRouter{root: root, bin: bin}
	fr.serve(t)
	return fr
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// serve runs a minimal SSH server: password "keenetic", "exec" requests run
// under sh with the fake tools first on PATH.
func (fr *fakeRouter) serve(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, err := ssh.NewSignerFromKey(priv)
	must(t, err)
	fr.hostKey = ssh.FingerprintSHA256(signer.PublicKey())
	cfg := &ssh.ServerConfig{PasswordCallback: func(_ ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
		if string(pw) == "keenetic" {
			return nil, nil
		}
		return nil, io.EOF
	}}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	must(t, err)
	t.Cleanup(func() { ln.Close() })
	fr.addr = ln.Addr().String()
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go fr.handle(nc, cfg)
		}
	}()
}

func (fr *fakeRouter) handle(nc net.Conn, cfg *ssh.ServerConfig) {
	_, chans, reqs, err := ssh.NewServerConn(nc, cfg)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	for nch := range chans {
		ch, creqs, err := nch.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer ch.Close()
			for req := range creqs {
				if req.Type != "exec" {
					req.Reply(false, nil)
					continue
				}
				n := binary.BigEndian.Uint32(req.Payload[:4])
				cmdline := string(req.Payload[4 : 4+n])
				req.Reply(true, nil)
				cmd := exec.Command("sh", "-c", cmdline)
				cmd.Env = append(os.Environ(), "PATH="+fr.bin+":"+os.Getenv("PATH"))
				stdin, _ := cmd.StdinPipe()
				cmd.Stdout, cmd.Stderr = ch, ch.Stderr()
				go func() { io.Copy(stdin, ch); stdin.Close() }()
				code := 0
				if err := cmd.Run(); err != nil {
					code = 1
					if ee, ok := err.(*exec.ExitError); ok {
						code = ee.ExitCode()
					}
				}
				status := make([]byte, 4)
				binary.BigEndian.PutUint32(status, uint32(code))
				ch.SendRequest("exit-status", false, status)
				return
			}
		}()
	}
}

func (fr *fakeRouter) target() Target {
	host, port, _ := net.SplitHostPort(fr.addr)
	return Target{Host: host, Port: atoi(port), User: "root", Password: "keenetic", root: fr.root}
}

func TestEndToEndInstall(t *testing.T) {
	fr := newFakeRouter(t)
	p, _ := testPayload(t, "0.1.0-beta.1")

	// wrong password: a clear, actionable message
	bad := fr.target()
	bad.Password = "nope"
	if _, err := Dial(bad); err == nil || !strings.Contains(err.Error(), "root / keenetic") {
		t.Errorf("bad password err = %v", err)
	}

	c, err := Dial(fr.target())
	must(t, err)
	if c.HostKey != fr.hostKey {
		t.Errorf("host key %q, want %q", c.HostKey, fr.hostKey)
	}
	rep, err := Detect(c)
	must(t, err)
	c.Close()
	if rep.Arch != "x86_64" || rep.KeeneticOS != "4.3.1" || rep.Pkgs["curl"] == "" || rep.Pkgs["ipset"] != "" || rep.NuxkCore != "" {
		t.Fatalf("detect = %+v", rep)
	}
	plan := BuildPlan(rep, p)
	var sel []string
	for _, it := range plan.Items {
		if it.Selected {
			sel = append(sel, it.ID)
		}
	}
	if strings.Join(sel, ",") != "deps,nfqws2,core,config,start" {
		t.Fatalf("selected = %v", sel)
	}

	// a different host key must be refused before any command runs
	spoof := fr.target()
	spoof.HostKey = "SHA256:someone-else"
	if _, err := Dial(spoof); err == nil || !strings.Contains(err.Error(), "ключ хоста изменился") {
		t.Errorf("spoofed key err = %v", err)
	}

	pinned := fr.target()
	pinned.HostKey = fr.hostKey
	c, err = Dial(pinned)
	must(t, err)
	var events []Event
	res, err := Install(c, p, sel, func(e Event) { events = append(events, e) })
	c.Close()
	if err != nil {
		for _, e := range events {
			t.Log(e.Kind, e.Text)
		}
		t.Fatal(err)
	}
	if !res.OK || len(res.Token) != 26 || res.URL != "http://127.0.0.1:4141/" {
		t.Errorf("result = %+v", res)
	}

	// what landed on the "router"
	opkgLog, _ := os.ReadFile(filepath.Join(fr.root, "opkg.log"))
	for _, want := range []string{"opkg install ca-certificates ipset", "opkg install nfqws2-keenetic"} {
		if !strings.Contains(string(opkgLog), want) {
			t.Errorf("opkg log lacks %q:\n%s", want, opkgLog)
		}
	}
	feed, _ := os.ReadFile(filepath.Join(fr.root, "opt/etc/opkg/nfqws2-keenetic.conf"))
	if strings.TrimSpace(string(feed)) != nfqwsFeed {
		t.Errorf("feed = %q", feed)
	}
	conf := filepath.Join(fr.root, "opt/etc/nuxk/nuxk.conf")
	st, err := os.Stat(conf)
	must(t, err)
	if st.Mode().Perm() != 0o600 {
		t.Errorf("nuxk.conf mode %v, want 0600", st.Mode().Perm())
	}
	body, _ := os.ReadFile(conf)
	for _, want := range []string{`API_TOKEN="` + res.Token + `"`, `ENGINE_NFQWS2="/opt/etc/nuxk/engines/S51nfqws2-nuxk"`, `ENGINE_USQUE=""`, `LISTEN="127.0.0.1:4141"`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("nuxk.conf lacks %s", want)
		}
	}
	for _, f := range []string{"opt/usr/bin/nuxk-core", "opt/etc/init.d/S99nuxk-core", "opt/etc/nuxk/engines/S51nfqws2-nuxk", "opt/share/www/nuxk/index.html", "opt/share/www/nuxk/assets/app-1.js"} {
		if _, err := os.Stat(filepath.Join(fr.root, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	matches, _ := filepath.Glob(filepath.Join(fr.root, "opt/*/*/*.nuxk-new"))
	if len(matches) > 0 {
		t.Errorf("temp files left: %v", matches)
	}

	// re-check: everything in place, nothing selected; token survives
	c, err = Dial(pinned)
	must(t, err)
	rep2, err := Detect(c)
	must(t, err)
	pl2 := BuildPlan(rep2, p)
	for _, it := range pl2.Items {
		if it.Selected {
			t.Errorf("after install %s still selected (%s: %s)", it.ID, it.Status, it.Detail)
		}
	}
	res2, err := Install(c, p, []string{"core", "start"}, func(Event) {})
	c.Close()
	must(t, err)
	if res2.Token != res.Token {
		t.Error("reinstall must keep the existing token")
	}
	if _, err := os.Stat(filepath.Join(fr.root, "opt/usr/bin/nuxk-core.prev")); err != nil {
		t.Error("reinstall must keep the previous binary as .prev")
	}
}

func TestPlanUsqueIsOptIn(t *testing.T) {
	p, _ := testPayload(t, "0.1.0-beta.1")
	r := Report{Entware: true, Arch: "x86_64", OptFreeKB: 500000, Pkgs: map[string]string{}, Kmods: map[string]string{}, Init: map[string]bool{}}
	it := item(BuildPlan(r, p), "usque")
	if it.Status != StInstall || !it.Selectable || it.Selected || !strings.Contains(it.Detail, "Cloudflare") {
		t.Errorf("usque = %+v, want installable, not preselected, with the consent text", it)
	}
	r.Arch = "mips" // no ipk for it in this payload
	if it := item(BuildPlan(r, p), "usque"); it.Selectable || it.Status != StInfo {
		t.Errorf("usque without ipk = %+v", it)
	}
}

func TestEndToEndUsque(t *testing.T) {
	fr := newFakeRouter(t)
	p, _ := testPayload(t, "0.1.0-beta.1")
	lists := filepath.Join(fr.root, "opt/etc/nfqws2/lists")
	must(t, os.MkdirAll(lists, 0o755))
	must(t, os.WriteFile(filepath.Join(lists, "user.list"), []byte("# mine\nyoutube.com\n"), 0o644))

	pinned := fr.target()
	c, err := Dial(pinned)
	must(t, err)
	_, err = Install(c, p, []string{"deps", "nfqws2", "core", "config", "start"}, func(Event) {})
	must(t, err)
	// first start seeds the «DPI» list from nfqws2's own list
	seed, _ := os.ReadFile(filepath.Join(fr.root, "opt/etc/nuxk/plane.json"))
	if !strings.Contains(string(seed), `"youtube.com"`) || !strings.Contains(string(seed), `"desync"`) || !strings.Contains(string(seed), `"manage_desync": true`) || strings.Contains(string(seed), "mine") {
		t.Errorf("plane.json = %s", seed)
	}

	var events []Event
	_, err = Install(c, p, []string{"usque"}, func(e Event) { events = append(events, e) })
	c.Close()
	if err != nil {
		for _, e := range events {
			t.Log(e.Kind, e.Text)
		}
		t.Fatal(err)
	}
	ul, _ := os.ReadFile(filepath.Join(lists, "user.list"))
	if !strings.Contains(string(ul), "youtube.com") || !strings.Contains(string(ul), "cloudflareclient.com") {
		t.Errorf("user.list = %q: WARP hosts must be added, the user's kept", ul)
	}
	conf, _ := os.ReadFile(filepath.Join(fr.root, "opt/etc/nuxk/nuxk.conf"))
	for _, want := range []string{`ENGINE_USQUE="/opt/etc/init.d/S51usque"`, `PLANE_IFACE_WARP="OpkgTun0"`} {
		if !strings.Contains(string(conf), want) {
			t.Errorf("nuxk.conf lacks %s", want)
		}
	}
	if _, err := os.Stat(filepath.Join(fr.root, "opt/tmp/usque-keenetic.ipk")); err == nil {
		t.Error("ipk left in /opt/tmp")
	}
	started := false
	for _, e := range events {
		if e.Kind == "ok" && strings.HasPrefix(e.Text, "Запуск и проверка") {
			started = true
		}
	}
	if !started {
		t.Error("installing usque must restart nuxk-core")
	}
}

// ---------------------------------------------------------------------------
// xray (VLESS)

func TestXrayIface(t *testing.T) {
	for name, c := range map[string]struct {
		tuns  map[string]string
		usque string
		want  string
	}{
		"fresh":                   {nil, "", "OpkgTun1"},
		"usque on 0":              {map[string]string{"OpkgTun0": "usque"}, "OpkgTun0", "OpkgTun1"},
		"usque on 1":              {map[string]string{"OpkgTun1": "usque"}, "OpkgTun1", "OpkgTun2"},
		"someone else's 1":        {map[string]string{"OpkgTun0": "usque", "OpkgTun1": "my-vpn"}, "OpkgTun0", "OpkgTun2"},
		"ours again, wherever":    {map[string]string{"OpkgTun1": "my-vpn", "OpkgTun3": "nuxk-vless"}, "", "OpkgTun3"},
		"no description is taken": {map[string]string{"OpkgTun1": "-"}, "", "OpkgTun2"},
	} {
		if got := xrayIface(Report{NdmTuns: c.tuns, UsqueIface: c.usque}); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
	all := map[string]string{}
	for n := 1; n < 10; n++ {
		all[fmt.Sprintf("OpkgTun%d", n)] = "x"
	}
	if got := xrayIface(Report{NdmTuns: all}); got != "" {
		t.Errorf("all taken: %q", got)
	}
}

func TestPlanXray(t *testing.T) {
	p, _ := testPayload(t, "0.1.0-beta.1")
	r := Report{Entware: true, Arch: "x86_64", OptFreeKB: 500000, Pkgs: map[string]string{}, Kmods: map[string]string{}, Init: map[string]bool{}}
	it := item(BuildPlan(r, p), "xray")
	if it.Status != StInstall || !it.Selectable || it.Selected || !strings.Contains(it.Detail, "сохранит конфигурацию роутера") || !strings.Contains(it.Detail, "OpkgTun1") {
		t.Errorf("xray = %+v, want installable, not preselected, with the consent text", it)
	}
	r.OptFreeKB = 30 * 1024
	if it := item(BuildPlan(r, p), "xray"); it.Selectable || !strings.Contains(it.Detail, "мало места") {
		t.Errorf("no room: %+v", it)
	}
	r.OptFreeKB, r.XrayReady, r.XrayVersion = 500000, true, "25.1.1"
	if it := item(BuildPlan(r, p), "xray"); it.Status != StUpgrade || it.Selected {
		t.Errorf("older xray: %+v", it)
	}
	r.XrayVersion = xrayVersion
	if it := item(BuildPlan(r, p), "xray"); it.Status != StOK || !it.Selectable {
		t.Errorf("current xray: %+v", it)
	}
	r.Arch = "armv7"
	if it := item(BuildPlan(r, p), "xray"); it.Selectable {
		t.Errorf("no xray build: %+v", it)
	}
}

// fakeXTLS serves a release archive like XTLS's, with a script for xray,
// and pins its hash for x86_64 while the test runs.
func fakeXTLS(t *testing.T) *httptest.Server {
	t.Helper()
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	w, _ := zw.Create("xray")
	_, _ = io.WriteString(w, "#!/bin/sh\n[ \"$1\" = version ] && echo 'Xray "+xrayVersion+" (Xray, Penetrates Everything.) test'\ntrue\n")
	g, _ := zw.Create("geoip.dat")
	_, _ = g.Write(make([]byte, 1000))
	must(t, zw.Close())
	sum := sha256.Sum256(zb.Bytes())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v"+xrayVersion+"/Xray-linux-64.zip" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(zb.Bytes())
	}))
	t.Cleanup(srv.Close)
	oldURL, oldAsset := xrayBaseURL, xrayAssets["x86_64"]
	t.Cleanup(func() { xrayBaseURL, xrayAssets["x86_64"] = oldURL, oldAsset })
	xrayBaseURL = srv.URL
	xrayAssets["x86_64"] = xrayAsset{"Xray-linux-64.zip", "xray", hex.EncodeToString(sum[:])}
	return srv
}

func TestFetchXrayRefusesAnotherFile(t *testing.T) {
	fakeXTLS(t)
	a := xrayAssets["x86_64"]
	a.sha256 = strings.Repeat("0", 64)
	xrayAssets["x86_64"] = a
	if _, err := fetchXray(context.Background(), "x86_64", func(Event) {}); err == nil || !strings.Contains(err.Error(), "хеш архива xray не совпал") {
		t.Errorf("err = %v", err)
	}
}

func TestEndToEndXray(t *testing.T) {
	fr := newFakeRouter(t)
	p, _ := testPayload(t, "0.1.0-beta.1")
	fakeXTLS(t)
	// usque already took OpkgTun0; someone's own VPN sits on OpkgTun1
	must(t, os.MkdirAll(filepath.Join(fr.root, "ndm"), 0o755))
	must(t, os.WriteFile(filepath.Join(fr.root, "ndm/OpkgTun0"), []byte("usque\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(fr.root, "ndm/OpkgTun1"), []byte("my-vpn\n"), 0o644))

	c, err := Dial(fr.target())
	must(t, err)
	defer c.Close()
	_, err = Install(c, p, []string{"deps", "nfqws2", "core", "config", "start"}, func(Event) {})
	must(t, err)

	var events []Event
	_, err = Install(c, p, []string{"xray"}, func(e Event) { events = append(events, e) })
	if err != nil {
		for _, e := range events {
			t.Log(e.Kind, e.Text)
		}
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(fr.root, "opt/sbin/xray")); err != nil || !strings.Contains(string(b), "Penetrates") {
		t.Errorf("xray binary: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fr.root, "opt/sbin/geoip.dat")); err == nil {
		t.Error("geoip.dat must not go to the router")
	}
	if b, _ := os.ReadFile(filepath.Join(fr.root, "opt/etc/init.d/S52xray-nuxk")); !strings.Contains(string(b), "S52xray-nuxk") {
		t.Error("init script not installed")
	}
	if d, _ := os.ReadFile(filepath.Join(fr.root, "ndm/OpkgTun2")); strings.TrimSpace(string(d)) != "nuxk-vless" {
		t.Errorf("OpkgTun2 description = %q, want nuxk-vless", d)
	}
	if d, _ := os.ReadFile(filepath.Join(fr.root, "ndm/OpkgTun1")); strings.TrimSpace(string(d)) != "my-vpn" {
		t.Errorf("someone else's OpkgTun1 touched: %q", d)
	}
	if b, _ := os.ReadFile(filepath.Join(fr.root, "ndm.saved")); !strings.Contains(string(b), "saved") {
		t.Error("the router's config must be saved after creating the interface")
	}
	conf, _ := os.ReadFile(filepath.Join(fr.root, "opt/etc/nuxk/nuxk.conf"))
	for _, want := range []string{`ENGINE_XRAY="/opt/etc/init.d/S52xray-nuxk"`, `PLANE_IFACE_VLESS="OpkgTun2"`} {
		if !strings.Contains(string(conf), want) {
			t.Errorf("nuxk.conf lacks %s", want)
		}
	}

	// the second time: the same interface, found by its description, not a new one
	must(t, os.Remove(filepath.Join(fr.root, "ndm.saved")))
	r, err := Detect(c)
	must(t, err)
	if !r.XrayReady || r.XrayVersion != xrayVersion || xrayIface(r) != "OpkgTun2" {
		t.Errorf("after install: ready=%v version=%q iface=%q", r.XrayReady, r.XrayVersion, xrayIface(r))
	}
	_, err = Install(c, p, []string{"xray"}, func(Event) {})
	must(t, err)
	if _, err := os.Stat(filepath.Join(fr.root, "ndm/OpkgTun3")); err == nil {
		t.Error("reinstall created another interface")
	}
}
