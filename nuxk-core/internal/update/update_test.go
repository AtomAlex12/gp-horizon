package update

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"0.4.0", "0.3.0", true},
		{"0.3.0", "0.4.0", false},
		{"0.10.0", "0.9.9", true},
		{"1.0.0", "0.99.99", true},
		{"0.4.0", "0.4.0", false},
		{"0.4.0", "0.4.0-beta.2", true},
		{"0.4.0-beta.2", "0.4.0", false},
		{"0.4.0-beta.10", "0.4.0-beta.9", true},
		{"0.4.0-beta.2", "0.4.0-alpha.9", true},
		{"0.4.0-rc.1", "0.4.0-beta.3", true},
		{"0.4.0-beta.1.1", "0.4.0-beta.1", true},
		{"v0.4.1", "0.4.0", true},
		{"0.3.0", "0.0.0-dev", true},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%s, %s) = %v", c.a, c.b, got)
		}
	}
	for v, ok := range map[string]bool{"0.4.0": true, "v1.2.3-beta.1": true, "1.2": false, "latest": false, "1.2.3-": false, "1.2.3 x": false} {
		if Valid(v) != ok {
			t.Errorf("Valid(%q) != %v", v, ok)
		}
	}
}

const releases = `[
 {"tag_name":"v0.5.0-beta.1","name":"0.5.0-beta.1","body":"beta","html_url":"https://x/0.5.0-beta.1","published_at":"2026-10-03T10:00:00Z","prerelease":true,"draft":false},
 {"tag_name":"v0.4.1","name":"0.4.1","body":"## Исправлено\n- всё","html_url":"https://x/0.4.1","published_at":"2026-10-01T10:00:00Z","prerelease":false,"draft":false},
 {"tag_name":"v0.9.0","name":"draft","body":"","html_url":"","published_at":"2026-10-04T10:00:00Z","prerelease":false,"draft":true},
 {"tag_name":"nightly","name":"n","body":"","html_url":"","published_at":"2026-10-04T10:00:00Z","prerelease":false,"draft":false},
 {"tag_name":"v0.4.0","name":"0.4.0","body":"","html_url":"https://x/0.4.0","published_at":"2026-09-28T10:00:00Z","prerelease":false,"draft":false}
]`

func github(t *testing.T, code int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/me/nuxk/releases" || !strings.HasPrefix(r.Header.Get("User-Agent"), "nuxk-core/") {
			t.Errorf("request %s, UA %q", r.URL, r.Header.Get("User-Agent"))
		}
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

type memStore map[string]any

func (m memStore) LoadJSON(string, any) error     { return nil }
func (m memStore) SaveJSON(n string, v any) error { m[n] = v; return nil }

func newUpdater(t *testing.T, api, command string) *Updater {
	t.Helper()
	return New(Options{Current: "0.4.0", Repo: "me/nuxk", API: api, Command: command, Dir: t.TempDir()}, memStore{})
}

func TestCheckStableAndBeta(t *testing.T) {
	srv := github(t, 200, releases)
	u := newUpdater(t, srv.URL, "")
	st := u.Check(context.Background())
	if st.CheckError != "" || st.Latest == nil || st.Latest.Version != "0.4.1" || !st.Available || st.CheckedAt == 0 {
		t.Fatalf("stable: %+v", st)
	}
	if st.Latest.Notes != "## Исправлено\n- всё" || st.Latest.URL != "https://x/0.4.1" {
		t.Fatalf("release: %+v", st.Latest)
	}
	if st.CanApply || !strings.Contains(st.Cannot, "UPDATE_COMMAND") {
		t.Fatalf("no command, still can apply: %+v", st)
	}
	if st, _ = u.SetSettings(Settings{Check: true, Channel: ChannelBeta}); st.Latest != nil {
		t.Fatal("another channel kept the old find")
	}
	if st = u.Check(context.Background()); st.Latest == nil || st.Latest.Version != "0.5.0-beta.1" || !st.Latest.Prerelease {
		t.Fatalf("beta: %+v", st.Latest)
	}
	if _, err := u.SetSettings(Settings{Channel: "nightly"}); !errors.Is(err, ErrBadSettings) {
		t.Fatalf("bad channel: %v", err)
	}
}

func TestCheckErrors(t *testing.T) {
	for _, c := range []struct {
		code int
		body string
		want string
	}{
		{404, `{"message":"Not Found"}`, "нет репозитория me/nuxk"},
		{403, `{"message":"rate limit"}`, "реже"},
		{200, `[]`, "нет релизов"},
		{200, `{`, "не понял"},
	} {
		u := newUpdater(t, github(t, c.code, c.body).URL, "")
		if st := u.Check(context.Background()); !strings.Contains(st.CheckError, c.want) || st.Latest != nil {
			t.Errorf("%d %s: %+v", c.code, c.body, st)
		}
	}
	// a failed look keeps the last find
	srv := github(t, 200, releases)
	u := newUpdater(t, srv.URL, "")
	u.Check(context.Background())
	u.o.API = "http://127.0.0.1:1"
	if st := u.Check(context.Background()); st.Latest == nil || !strings.Contains(st.CheckError, "нет связи") {
		t.Fatalf("after a failure: %+v", st)
	}
}

// A fake `nuxk`: reports like install/nuxk-lite.sh does, with what the agent
// gave it in the environment.
const fakeNuxk = `#!/bin/sh
[ "$1 $2" = "update --yes" ] || exit 9
echo "▸ nuxk-core $NUXK_VERSION"
{ echo "state done"; echo "from $NUXK_FROM"; echo "to $NUXK_VERSION"; echo "pid $$"; echo "started $NUXK_STARTED"; echo "at $(date +%s)"; echo "message nuxk Horizon $NUXK_VERSION работает"; } >"$NUXK_STATUS.tmp" && mv "$NUXK_STATUS.tmp" "$NUXK_STATUS"
`

func TestStart(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	cmd := filepath.Join(t.TempDir(), "nuxk")
	if err := os.WriteFile(cmd, []byte(fakeNuxk), 0o755); err != nil {
		t.Fatal(err)
	}
	u := newUpdater(t, github(t, 200, releases).URL, cmd)
	if _, err := u.Start("0.4.1"); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("before a check: %v", err)
	}
	u.Check(context.Background())
	if _, err := u.Start("0.3.0"); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("not the one found: %v", err)
	}
	st, err := u.Start("0.4.1")
	if err != nil || st.Run == nil || st.Run.To != "0.4.1" || st.Run.From != "0.4.0" {
		t.Fatalf("start: %v %+v", err, st.Run)
	}
	deadline := time.Now().Add(10 * time.Second)
	for st.Run.State != "done" && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		st = u.Status()
	}
	r := st.Run
	if r.State != "done" || r.Message != "nuxk Horizon 0.4.1 работает" || r.StartedAt == 0 || len(r.Log) != 1 || r.Log[0] != "▸ nuxk-core 0.4.1" {
		t.Fatalf("run: %+v", r)
	}
}

func TestStartRefuses(t *testing.T) {
	cmd := filepath.Join(t.TempDir(), "nuxk")
	_ = os.WriteFile(cmd, []byte(fakeNuxk), 0o755)
	u := newUpdater(t, github(t, 200, releases).URL, cmd)
	u.Check(context.Background())

	// a run going on: not a second one
	now := time.Now().Unix()
	_ = writeFile(u.statusPath(), "state running\nfrom 0.4.0\nto 0.4.1\npid 4242\nstarted 1\nat "+strconv.FormatInt(now, 10)+"\nmessage Скачиваю\n")
	u.alive = func(pid int) bool { return pid == 4242 }
	if _, err := u.Start("0.4.1"); !errors.Is(err, ErrBusy) {
		t.Fatalf("busy: %v", err)
	}
	// its script gone (a reboot): cut short, and a new one may start
	u.alive = func(int) bool { return false }
	if r := u.Status().Run; r.State != "interrupted" || !strings.Contains(r.Message, "Скачиваю") {
		t.Fatalf("interrupted: %+v", r)
	}
	// not older than what runs
	u.o.Current = "0.4.1"
	if _, err := u.Start("0.4.1"); !errors.Is(err, ErrNotNewer) {
		t.Fatalf("same version: %v", err)
	}
	// no command
	u.o.Command = filepath.Join(t.TempDir(), "missing")
	if _, err := u.Start("0.4.1"); !errors.Is(err, ErrCannot) {
		t.Fatalf("no command: %v", err)
	}
}

// Restarted by an update: the new agent looks for releases in seconds, not
// minutes — the panel is watching.
func TestFirstLookAfterAnUpdate(t *testing.T) {
	u := newUpdater(t, "http://127.0.0.1:1", "")
	if d := u.firstLook(); d != 2*time.Minute {
		t.Fatalf("plain start: %v", d)
	}
	now := strconv.FormatInt(time.Now().Unix(), 10)
	_ = writeFile(u.statusPath(), "state running\nfrom 0.4.0\nto 0.4.1\npid 1\nstarted "+now+"\nat "+now+"\nmessage Перезапускаю\n")
	u.alive = func(int) bool { return true }
	if d := u.firstLook(); d != 5*time.Second {
		t.Fatalf("after an update: %v", d)
	}
}
