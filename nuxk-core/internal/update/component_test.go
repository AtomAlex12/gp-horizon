package update

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// fakeAdd stands for `nuxk warp|vless|dns|nfqws2 --yes`: reports the way
// nuxk-lite.sh does, the task the agent named included.
const fakeAdd = `#!/bin/sh
[ "$2" = "--yes" ] || exit 9
echo "▸ $1"
{ echo "state done"; echo "pid $$"; echo "started $NUXK_STARTED"; echo "task $NUXK_TASK"; echo "at $(date +%s)"; echo "message $1 установлен "; } >"$NUXK_STATUS.tmp" && mv "$NUXK_STATUS.tmp" "$NUXK_STATUS"
`

func componentUpdater(t *testing.T, have map[string]Presence) *Updater {
	t.Helper()
	cmd := filepath.Join(t.TempDir(), "nuxk")
	if err := os.WriteFile(cmd, []byte(fakeAdd), 0o755); err != nil {
		t.Fatal(err)
	}
	return New(Options{Current: "0.5.0", Command: cmd, Dir: t.TempDir(),
		Have: func(id string) Presence { return have[id] }}, memStore{})
}

func item(c Components, id string) Component {
	for _, it := range c.Items {
		if it.ID == id {
			return it
		}
	}
	return Component{}
}

func TestComponents(t *testing.T) {
	u := componentUpdater(t, map[string]Presence{
		"nfqws2":   {Installed: true, Version: "1.3.1"},
		"smartdns": {Cannot: "SmartDNS отключён"},
	})
	c := u.Components()
	if len(c.Items) != 4 || c.Run != nil {
		t.Fatalf("%+v", c)
	}
	if n := item(c, "nfqws2"); !n.Installed || n.Version != "1.3.1" || n.CanInstall || n.Cannot != "" {
		t.Fatalf("installed: %+v", n)
	}
	if w := item(c, "warp"); w.Installed || !w.CanInstall || w.Confirm == "" || w.Name == "" {
		t.Fatalf("addable: %+v", w)
	}
	if s := item(c, "smartdns"); s.CanInstall || s.Cannot != "SmartDNS отключён" {
		t.Fatalf("not here: %+v", s)
	}
	// installed, but broken (WARP without its interface): offered again
	u.o.Have = func(id string) Presence {
		if id == "warp" {
			return Presence{Installed: true, Broken: "интерфейса OpkgTun0 в Keenetic нет"}
		}
		return Presence{}
	}
	if w := item(u.Components(), "warp"); !w.Installed || !w.CanInstall || w.Broken == "" {
		t.Fatalf("broken: %+v", w)
	}
	// not a router: nothing from the panel
	u.o.Have = nil
	if w := item(u.Components(), "warp"); w.CanInstall || w.Cannot == "" {
		t.Fatalf("no Have: %+v", w)
	}
}

// waitRun: the install from the panel is over
func waitRun(t *testing.T, u *Updater) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if r := u.Components().Run; r != nil && r.State != "running" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the install didn't finish")
}

func TestInstall(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	u := componentUpdater(t, map[string]Presence{"nfqws2": {Installed: true}, "warp": {Installed: true, Broken: "нет интерфейса"}})
	t.Setenv("NUXK_BASE_URL", "http://mirror-long-gone:8099") // the installer's, left in the agent
	if _, err := u.Install("tor"); !errors.Is(err, ErrUnknownComponent) {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := u.Install("nfqws2"); !errors.Is(err, ErrInstalled) {
		t.Fatalf("installed: %v", err)
	}
	if c, err := u.Install("warp"); err != nil || c.Run == nil || c.Run.Task != "warp" { // broken: installed again
		t.Fatalf("broken: %v", err)
	}
	waitRun(t, u)
	c, err := u.Install("vless")
	if err != nil || c.Run == nil || c.Run.Task != "vless" {
		t.Fatalf("start: %v %+v", err, c.Run)
	}
	deadline := time.Now().Add(10 * time.Second)
	for c.Run.State != "done" && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		c = u.Components()
	}
	if r := c.Run; r.State != "done" || r.Task != "vless" || r.Message != "vless установлен" || len(r.Log) != 1 || r.Log[0] != "▸ vless" {
		t.Fatalf("run: %+v", r)
	}
	// the update's own report stays apart
	if u.Status().Run != nil {
		t.Fatal("a component install shows up as an update")
	}
}

// One run at a time: an update going blocks an install, and the other way.
func TestInstallAndUpdateTakeTurns(t *testing.T) {
	u := componentUpdater(t, nil)
	now := strconv.FormatInt(time.Now().Unix(), 10)
	u.alive = func(pid int) bool { return pid == 4242 }
	_ = writeFile(u.statusPath(), "state running\nfrom 0.5.0\nto 0.5.1\npid 4242\nstarted 1\nat "+now+"\nmessage Скачиваю\n")
	if _, err := u.Install("warp"); !errors.Is(err, ErrBusy) {
		t.Fatalf("install during an update: %v", err)
	}
	if w := item(u.Components(), "warp"); w.CanInstall || w.Cannot == "" {
		t.Fatalf("shown as addable during an update: %+v", w)
	}
	_ = os.Remove(u.statusPath())
	_ = writeFile(filepath.Join(u.o.Dir, componentStatus), "state running\ntask warp\npid 4242\nstarted 1\nat "+now+"\nmessage Ставлю WARP\n")
	u.latest = &Release{Version: "0.5.1"}
	if _, err := u.Start("0.5.1"); !errors.Is(err, ErrBusy) {
		t.Fatalf("update during an install: %v", err)
	}
	// the install's script gone (a reboot): cut short, and the update may go
	u.alive = func(int) bool { return false }
	if r := u.Components().Run; r.State != "interrupted" || r.Task != "warp" {
		t.Fatalf("interrupted: %+v", r)
	}
}
