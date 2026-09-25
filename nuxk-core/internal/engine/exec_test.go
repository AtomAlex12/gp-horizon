package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func script(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "S99fake")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// stderr noise must not become KV keys.
func TestKVIgnoresStderr(t *testing.T) {
	x := Exec{Script: script(t, `echo "iptables: warning bogus" >&2; echo "service.running 1"`)}
	kv, _, err := x.KV(context.Background(), "info")
	if err != nil {
		t.Fatal(err)
	}
	if len(kv) != 1 || kv["service.running"] != "1" {
		t.Errorf("kv = %v, want only service.running", kv)
	}
}

// A failing script's own message reaches the error (and so the dashboard).
func TestActionErrorCarriesOutput(t *testing.T) {
	x := Exec{Script: script(t, `echo "Service NFQWS2 is already running" >&2; exit 1`)}
	err := x.Action(context.Background(), "start")
	var se *ScriptError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want *ScriptError", err)
	}
	if !strings.Contains(err.Error(), "already running") || !strings.Contains(err.Error(), "S99fake start") {
		t.Errorf("err = %q", err)
	}
}

func TestActionWithInputFeedsStdin(t *testing.T) {
	x := Exec{Script: script(t, `cat`)}
	out, err := x.ActionWithInput(context.Background(), "apply", "a.com\nb.com")
	if err != nil || out != "a.com\nb.com" {
		t.Errorf("out = %q, err = %v", out, err)
	}
}

func TestTail(t *testing.T) {
	if got := tail("  line1\nline2  \n", 100); got != "line1 line2" {
		t.Errorf("tail = %q", got)
	}
	if got := tail(strings.Repeat("x", 50), 10); got != "…xxxxxxxxxx" {
		t.Errorf("tail = %q", got)
	}
}

// A script that starts a daemon which keeps our stdout open must not hang
// the call (init scripts do this: `nfqws2 --daemon`, start-stop-daemon -b).
func TestExecDaemonHoldingPipeDoesNotHang(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "S99daemon")
	body := "#!/bin/sh\nsleep 30 &\necho started\nexit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err := Exec{Script: script}.Action(context.Background(), "start")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("took %v: the call waited for the daemon", d)
	}
}
