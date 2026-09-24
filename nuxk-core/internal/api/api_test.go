package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nuxk.dev/horizon/core/internal/core"
	"nuxk.dev/horizon/core/internal/engine"
	"nuxk.dev/horizon/core/internal/engine/usque"
	"nuxk.dev/horizon/core/internal/state"
)

func newTestRouter(t *testing.T, token string) http.Handler {
	t.Helper()
	st, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg := engine.NewRegistry()
	reg.Add(usque.New("/nonexistent/S51usque"))
	hub := core.NewHub("test")
	return NewRouter(Deps{
		Version: "test", Commit: "abc", Engines: reg, Hub: hub,
		Ctl: core.NewController(reg, st, hub, "test"), Token: token,
	})
}

func do(h http.Handler, method, path, body, remote, auth string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = remote
	if auth != "" {
		r.Header.Set("Authorization", "Bearer "+auth)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestAuth(t *testing.T) {
	h := newTestRouter(t, "s3cret")
	for _, c := range []struct {
		remote, auth string
		want         int
	}{
		{"192.168.1.5:5000", "", http.StatusUnauthorized},
		{"192.168.1.5:5000", "wrong", http.StatusUnauthorized},
		{"192.168.1.5:5000", "s3cret", http.StatusOK},
		{"127.0.0.1:5000", "", http.StatusUnauthorized}, // a set token applies to loopback too
	} {
		if w := do(h, "GET", "/api/v1/version", "", c.remote, c.auth); w.Code != c.want {
			t.Errorf("%s auth=%q -> %d, want %d", c.remote, c.auth, w.Code, c.want)
		}
	}
	if w := do(h, "GET", "/api/v1/healthz", "", "192.168.1.5:5000", ""); w.Code != http.StatusOK {
		t.Errorf("healthz must be unauthenticated, got %d", w.Code)
	}
	if w := do(newTestRouter(t, ""), "GET", "/api/v1/version", "", "192.168.1.5:5000", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("no token: non-loopback must be refused, got %d", w.Code)
	}
}

func TestEngineConfigErrors(t *testing.T) {
	h := newTestRouter(t, "")
	lo := "127.0.0.1:5000"
	for _, c := range []struct {
		path, body string
		want       int
	}{
		{"/api/v1/engines/xray/config", `{"vless_uri":"vless://x"}`, http.StatusNotFound}, // not wired
		{"/api/v1/engines/usque/config", `{"a":"b"}`, http.StatusNotFound},                // not configurable
		{"/api/v1/engines/usque/config", `nope`, http.StatusBadRequest},                   // bad JSON
		{"/api/v1/engines/usque/config", `{}`, http.StatusBadRequest},                     // empty
	} {
		if w := do(h, "PUT", c.path, c.body, lo, ""); w.Code != c.want {
			t.Errorf("PUT %s %s -> %d, want %d (%s)", c.path, c.body, w.Code, c.want, w.Body)
		}
	}
}

// A failing script's message must reach the client, not just "exit status".
func TestEngineActionSurfacesScriptError(t *testing.T) {
	h := newTestRouter(t, "")
	w := do(h, "POST", "/api/v1/engines/usque/start", "", "127.0.0.1:5000", "")
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "S51usque start") {
		t.Errorf("-> %d %s", w.Code, w.Body)
	}
}
