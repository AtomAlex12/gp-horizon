package amneziawg

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type testRunner struct {
	action  string
	input   string
	failure error
}

func (r *testRunner) ActionWithInput(_ context.Context, action, input string) (string, error) {
	r.action, r.input = action, input
	return "", r.failure
}

func (r *testRunner) KV(_ context.Context, action string) (map[string]string, []string, error) {
	return map[string]string{"ok": "1", "warp": "off"}, nil, nil
}

func TestStartUsesPrivateStdinAndSanitizesCommandErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the driver requires Unix file permissions")
	}
	path := filepath.Join(t.TempDir(), "warp-awg.conf")
	if err := os.WriteFile(path, []byte(testProfile()), 0600); err != nil {
		t.Fatal(err)
	}
	r := &testRunner{failure: errors.New("secret printed by driver")}
	err := (&Adapter{driver: r, config: path}).Start(context.Background())
	if r.action != "start" || !strings.HasPrefix(r.input, "172.16.0.2/32 1280\n[Interface]\n") ||
		!strings.Contains(r.input, "PrivateKey = ") {
		t.Fatal("unexpected command or private stdin")
	}
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("expected redacted error, got %v", err)
	}
}

func TestProbeRejectsDirectCloudflareEgress(t *testing.T) {
	p, err := (&Adapter{driver: &testRunner{}}).Probe(context.Background(), nil)
	if err != nil || p.OK {
		t.Fatalf("direct exit reported as WARP: %+v (%v)", p, err)
	}
}
