package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nuxk.dev/horizon/core/internal/auth"
	"nuxk.dev/horizon/core/internal/core"
	"nuxk.dev/horizon/core/internal/engine"
	"nuxk.dev/horizon/core/internal/state"
)

// root's password here is "Hello world!" (SHA-512-crypt, Drepper's vector).
const testShadow = "root:$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFNjnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1:19000:0:99999:7:::\n"

func newLoginRouter(t *testing.T, token string) http.Handler {
	t.Helper()
	sh := filepath.Join(t.TempDir(), "shadow")
	if err := os.WriteFile(sh, []byte(testShadow), 0o600); err != nil {
		t.Fatal(err)
	}
	st, _ := state.Open(t.TempDir())
	reg := engine.NewRegistry()
	hub := core.NewHub("test")
	return NewRouter(Deps{
		Version: "test", Engines: reg, Hub: hub, Ctl: core.NewController(reg, st, hub, "test"),
		Token: token, Auth: auth.New("root", sh),
	})
}

type req struct {
	method, path, body, origin, cookie string
}

func send(h http.Handler, q req) *httptest.ResponseRecorder {
	r := httptest.NewRequest(q.method, q.path, strings.NewReader(q.body))
	r.RemoteAddr = "192.168.2.50:40000"
	r.Host = "192.168.2.1:4141"
	if q.origin != "" {
		r.Header.Set("Origin", q.origin)
	}
	if q.cookie != "" {
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: q.cookie})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func sessionOf(w *httptest.ResponseRecorder) string {
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			return c.Value
		}
	}
	return ""
}

const self = "http://192.168.2.1:4141"

func TestLoginSession(t *testing.T) {
	h := newLoginRouter(t, "tok")

	w := send(h, req{method: "POST", path: "/api/v1/auth/login", body: `{"user":"root","password":"nope"}`, origin: self})
	if w.Code != http.StatusUnauthorized || sessionOf(w) != "" {
		t.Fatalf("wrong password -> %d cookie %q", w.Code, sessionOf(w))
	}
	if w := send(h, req{method: "POST", path: "/api/v1/auth/login", body: `{"user":"root","password":"Hello world!"}`, origin: "http://evil.example"}); w.Code != http.StatusForbidden {
		t.Errorf("login from another site -> %d", w.Code)
	}

	w = send(h, req{method: "POST", path: "/api/v1/auth/login", body: `{"user":"root","password":"Hello world!"}`, origin: self})
	sid := sessionOf(w)
	if w.Code != http.StatusOK || sid == "" || !strings.Contains(w.Body.String(), `"user":"root"`) {
		t.Fatalf("login -> %d %s cookie %q", w.Code, w.Body, sid)
	}
	c := w.Result().Cookies()[0]
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Errorf("cookie must be HttpOnly + SameSite=Strict: %+v", c)
	}

	if w := send(h, req{method: "GET", path: "/api/v1/version", cookie: sid}); w.Code != http.StatusOK {
		t.Errorf("GET with session -> %d", w.Code)
	}
	if w := send(h, req{method: "GET", path: "/api/v1/version", cookie: "forged"}); w.Code != http.StatusUnauthorized {
		t.Errorf("unknown session -> %d", w.Code)
	}
	// a write with the session: only from this page
	if w := send(h, req{method: "POST", path: "/api/v1/engines/usque/start", cookie: sid, origin: "http://evil.example"}); w.Code != http.StatusForbidden {
		t.Errorf("cross-site write -> %d", w.Code)
	}
	if w := send(h, req{method: "POST", path: "/api/v1/engines/usque/start", cookie: sid, origin: self}); w.Code != http.StatusNotFound {
		t.Errorf("same-origin write reaches the handler (no such engine here) -> %d %s", w.Code, w.Body)
	}

	w = send(h, req{method: "POST", path: "/api/v1/auth/logout", cookie: sid})
	if w.Code != http.StatusOK {
		t.Fatalf("logout -> %d", w.Code)
	}
	if w := send(h, req{method: "GET", path: "/api/v1/version", cookie: sid}); w.Code != http.StatusUnauthorized {
		t.Errorf("after logout -> %d", w.Code)
	}
}

func TestPair(t *testing.T) {
	h := newLoginRouter(t, "the-api-token")
	if w := send(h, req{method: "POST", path: "/api/v1/auth/pair", body: `{"user":"root","password":"bad"}`}); w.Code != http.StatusUnauthorized {
		t.Errorf("pair, wrong password -> %d", w.Code)
	}
	w := send(h, req{method: "POST", path: "/api/v1/auth/pair", body: `{"user":"root","password":"Hello world!"}`})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"token":"the-api-token"`) {
		t.Fatalf("pair -> %d %s", w.Code, w.Body)
	}
	if w := send(newLoginRouter(t, ""), req{method: "POST", path: "/api/v1/auth/pair", body: `{"user":"root","password":"Hello world!"}`}); w.Code != http.StatusConflict {
		t.Errorf("pair without a configured token -> %d", w.Code)
	}
}

func TestLoginLockout(t *testing.T) {
	h := newLoginRouter(t, "tok")
	var w *httptest.ResponseRecorder
	for range 6 {
		w = send(h, req{method: "POST", path: "/api/v1/auth/login", body: `{"user":"root","password":"x"}`, origin: self})
	}
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Errorf("6th failure -> %d, Retry-After %q", w.Code, w.Header().Get("Retry-After"))
	}
}

func TestLoginOff(t *testing.T) {
	h := newTestRouter(t, "tok") // no Guard
	if w := do(h, "POST", "/api/v1/auth/login", `{"user":"root","password":"x"}`, "192.168.2.50:1", ""); w.Code != http.StatusNotFound {
		t.Errorf("login off -> %d", w.Code)
	}
}
