//go:build linux

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A recipe whose "plugin" is python3's http.server: install.sh writes a
// bin/run for the requested version. Special versions: "bad" fails to
// install, "dead" installs a run that exits at once.
const testInstall = `#!/bin/sh
set -eu
v="$1"; dest="$2"
echo "installing $v"
[ "$v" = bad ] && { echo "recipe says no" >&2; exit 3; }
mkdir -p "$dest/bin" "$dest/www"
echo "$v" > "$dest/www/version.txt"
if [ "$v" = dead ]; then
  printf '#!/bin/sh\nexit 1\n' > "$dest/bin/run"
else
  printf '#!/bin/sh\nenv > "$PLUGIN_DATA/env.txt"\ncd "$PLUGIN_DIR/www"\nexec python3 -m http.server --bind 127.0.0.1 %s\n' "$PORT" > "$dest/bin/run"
fi
chmod +x "$dest/bin/run"
`

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func startSupervisor(t *testing.T) (*PluginHost, string, string) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is needed for the test plugin")
	}
	base := t.TempDir()
	port := freePort(t)
	recipe := filepath.Join(base, "recipes", "demo")
	os.MkdirAll(recipe, 0o755)
	m := Manifest{Name: "demo", Title: "Demo", DefaultVersion: "v1", Install: "install.sh",
		InstallEnv: map[string]string{"PORT": fmt.Sprint(port)},
		Run:        []string{"bin/run"}, Listen: fmt.Sprintf("127.0.0.1:%d", port), Health: "/version.txt", API: "demo"}
	b, _ := json.Marshal(m)
	os.WriteFile(filepath.Join(recipe, "plugin.json"), b, 0o644)
	os.WriteFile(filepath.Join(recipe, "install.sh"), []byte(testInstall), 0o755)
	s := &Supervisor{
		DataDir: filepath.Join(base, "data"), RecipesDir: filepath.Join(base, "recipes"),
		Socket: filepath.Join(base, "run", "s.sock"), HealthTimeout: 4 * time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Error("supervisor did not stop")
		}
	})
	h := NewPluginHost(s.Socket)
	for i := 0; i < 50; i++ {
		if _, err := h.List(); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return h, s.DataDir, m.Listen
}

func waitPhase(t *testing.T, h *PluginHost, want, version string) PluginInfo {
	t.Helper()
	var p PluginInfo
	for i := 0; i < 250; i++ {
		p, _ = h.Get("demo")
		if p.Phase == want && (version == "" || p.Version == version) {
			return p
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("plugin never reached %s %s: %+v", want, version, p)
	return p
}

func TestSupervisorLifecycle(t *testing.T) {
	t.Setenv("AGENT_TOKEN", "router-secret") // must not reach the plugin
	h, data, _ := startSupervisor(t)

	if p, _ := h.Get("demo"); p.Phase != "absent" {
		t.Fatalf("fresh: %+v", p)
	}
	// the host's debug switch and its log, over the socket
	if st, err := h.SetDebug(true, 5); err != nil || !st.On || st.Until == 0 {
		t.Fatalf("debug on: %+v %v", st, err)
	}
	procRing.add(LogEntry{Level: "debug", Msg: "from the host"})
	if es, err := h.LogsSince(0); err != nil || len(es) == 0 || es[len(es)-1].Msg != "from the host" {
		t.Fatalf("host logs: %+v %v", es, err)
	}
	if st, err := h.SetDebug(false, 0); err != nil || st.On {
		t.Fatalf("debug off: %+v %v", st, err)
	}
	if _, err := h.call(supReq{Op: "install", Name: "demo"}); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, h, "running", "v1")

	env, _ := os.ReadFile(filepath.Join(data, "plugins", "demo", "data", "env.txt"))
	if strings.Contains(string(env), "router-secret") || !strings.Contains(string(env), "PLUGIN_NAME=demo") {
		t.Errorf("plugin env leaks the supervisor's or lacks its own:\n%s", env)
	}
	// its own TMPDIR, where it may run what it writes (the container's /tmp is noexec)
	tmp := filepath.Join(data, "plugins", "demo", "tmp")
	if !strings.Contains(string(env), "TMPDIR="+tmp+"\n") {
		t.Errorf("plugin TMPDIR is not its own %s:\n%s", tmp, env)
	}
	if st, err := os.Stat(tmp); err != nil || st.Mode()&os.ModeSticky == 0 || st.Mode().Perm() != 0o777 {
		t.Errorf("plugin tmp: %v %v", st, err)
	}

	// a failing recipe leaves the running version alone
	h.call(supReq{Op: "install", Name: "demo", Version: "bad"})
	p := waitPhase(t, h, "running", "v1")
	for i := 0; i < 50 && !strings.Contains(p.Notice, "установка не удалась"); i++ {
		time.Sleep(100 * time.Millisecond)
		p, _ = h.Get("demo")
	}
	if !strings.Contains(p.Notice, "установка не удалась") {
		t.Errorf("failed install not reported: %+v", p)
	}
	if r, _ := h.call(supReq{Op: "log", Name: "demo", Which: "install"}); !strings.Contains(r.Log, "recipe says no") {
		t.Errorf("install log: %q", r.Log)
	}

	// upgrade keeps the old one for rollback
	h.call(supReq{Op: "install", Name: "demo", Version: "v2"})
	if p := waitPhase(t, h, "running", "v2"); p.Previous != "v1" || p.Notice != "" {
		t.Errorf("after a good install: previous %q, notice %q", p.Previous, p.Notice)
	}

	// a version that never answers is rolled back by itself
	if _, err := h.call(supReq{Op: "install", Name: "demo", Version: "dead"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.call(supReq{Op: "install", Name: "demo", Version: "v3"}); err == nil {
		t.Error("a second install while the first is being verified must wait")
	}
	p = waitPhase(t, h, "running", "v2")
	if !strings.Contains(p.Notice, "не запустилась") || p.Previous != "dead" {
		t.Errorf("auto rollback not reported: %+v", p)
	}

	if _, err := h.call(supReq{Op: "disable", Name: "demo"}); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, h, "stopped", "v2")
	if _, err := h.call(supReq{Op: "enable", Name: "demo"}); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, h, "running", "v2")

	if _, err := h.call(supReq{Op: "install", Name: "demo", Version: "../../etc"}); err == nil {
		t.Error("a path as version must be refused")
	}
	if _, err := h.call(supReq{Op: "install", Name: "nope"}); err == nil {
		t.Error("unknown plugin")
	}
	// code readable by a process that dropped root, data not
	for rel, want := range map[string]os.FileMode{"plugins/demo": 0o711, "plugins/demo/versions": 0o755, "plugins/demo/data": 0o700} {
		if fi, err := os.Stat(filepath.Join(data, rel)); err != nil || fi.Mode().Perm() != want {
			t.Errorf("%s: mode %v, want %v (%v)", rel, fi.Mode().Perm(), want, err)
		}
	}
	if fi, err := os.Stat(filepath.Join(data, "plugins", "demo", "versions", "v2")); err != nil || fi.Mode().Perm()&0o005 != 0o005 {
		t.Errorf("installed version must be world-readable: %v %v", fi.Mode(), err)
	}

	var st PluginState
	b, _ := os.ReadFile(filepath.Join(data, "plugins", "demo", "state.json"))
	json.Unmarshal(b, &st)
	if !st.Enabled || st.Version != "v2" {
		t.Errorf("state.json %s", b)
	}
	if fi, err := os.Stat(filepath.Join(filepath.Dir(h.Socket), "s.sock")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("socket mode %v %v", fi, err)
	}
}
