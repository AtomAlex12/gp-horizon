package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeAgent speaks the bits of the agent contract the controller uses.
func fakeAgent(t *testing.T, token string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var n atomic.Int64 // counter base, grows per metrics call
	mux := http.NewServeMux()
	check := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(401)
			return false
		}
		return true
	}
	mux.HandleFunc("GET /api/v1/metrics", func(w http.ResponseWriter, r *http.Request) {
		if !check(w, r) {
			return
		}
		i := n.Add(1)
		fmt.Fprintf(w, `{"ts":%d,"wan":"ppp0","ifaces":{"ppp0":{"rx":%d,"tx":%d},"opkgtun0":{"rx":%d,"tx":0}},"nfqueues":[{"num":300,"packets":%d}],"conntrack":42}`,
			i*1000, i*1000, i*500, i*125, i*10)
	})
	mux.HandleFunc("GET /api/v1/info", func(w http.ResponseWriter, r *http.Request) {
		if check(w, r) {
			fmt.Fprint(w, `{"role":"router","model":"Ultra"}`)
		}
	})
	mux.HandleFunc("GET /api/v1/status", func(w http.ResponseWriter, r *http.Request) {
		if check(w, r) {
			http.SetCookie(w, &http.Cookie{Name: "nuxk_agent", Value: "leak"})
			fmt.Fprint(w, `{"version":"x","engines":[]}`)
		}
	})
	mux.HandleFunc("POST /api/v1/auth/pair", func(w http.ResponseWriter, r *http.Request) {
		var c loginReq
		json.NewDecoder(r.Body).Decode(&c)
		if c.User != "root" || c.Password != "Hello world!" {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"error":{"code":"bad_credentials","message":"неверный логин или пароль"}}`)
			return
		}
		fmt.Fprintf(w, `{"token":%q}`, token)
	})
	mux.HandleFunc("GET /api/v1/events", func(w http.ResponseWriter, r *http.Request) {
		if !check(w, r) {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		fmt.Fprint(w, "event: status\ndata: {}\n\n")
		fl.Flush()
		<-r.Context().Done()
	})
	return httptest.NewServer(mux), &n
}

func TestHistoryRates(t *testing.T) {
	h := NewHistory(3)
	for i := uint64(1); i <= 5; i++ {
		h.Add(metrics{TS: int64(i) * 2000, WAN: "ppp0", Ifaces: map[string]iface{"ppp0": {Rx: i * 1000}, "opkgtun0": {Tx: i * 100}}, NFQueues: []nfqueue{{Packets: i * 50}}})
	}
	s := h.Series()
	if len(s.TS) != 3 || s.WanRx[0] != 4000 || s.NFQ[2] != 25 || s.Tunnels["opkgtun0"]["tx_bps"][1] != 400 {
		t.Fatalf("series = %+v", s)
	}
	// a counter reset (router reboot) is a 0 step, not a huge negative spike
	h.Add(metrics{TS: 12000, WAN: "ppp0", Ifaces: map[string]iface{"ppp0": {Rx: 10}}})
	if s := h.Series(); s.WanRx[len(s.WanRx)-1] != 0 {
		t.Errorf("after reset: %v", s.WanRx)
	}
}

// client is a browser: a cookie jar and this page's Origin on every request.
type client struct {
	t   *testing.T
	srv *httptest.Server
	c   *http.Client
}

func newClient(t *testing.T, srv *httptest.Server) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t, srv, &http.Client{Jar: jar}}
}

func (c *client) do(method, path, body, origin string) (int, string) {
	c.t.Helper()
	req, _ := http.NewRequest(method, c.srv.URL+path, strings.NewReader(body))
	if origin == "" {
		origin = c.srv.URL
	}
	req.Header.Set("Origin", origin)
	resp, err := c.c.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (c *client) login() {
	c.t.Helper()
	if code, body := c.do("POST", "/ctl/v1/auth/login", `{"user":"admin","password":"correct horse"}`, ""); code != 200 {
		c.t.Fatalf("login -> %d %s", code, body)
	}
}

// newCtl: a controller over DATA_DIR dir; agentURL "" = not connected,
// adminPw "" = fresh (no admin yet).
func newCtl(t *testing.T, dir, agentURL, token, adminPw string) *httptest.Server {
	t.Helper()
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if adminPw != "" {
		if err := st.SetAdmin(adminPw); err != nil {
			t.Fatal(err)
		}
	}
	a := NewAgent(agentURL, token)
	if agentURL != "" {
		a.poll(context.Background())
		a.poll(context.Background())
	}
	srv := httptest.NewServer(NewServer(a, st, NewSessions(), "", "t"))
	t.Cleanup(srv.Close)
	return srv
}

func TestSetupWizard(t *testing.T) {
	ag, _ := fakeAgent(t, "agent-secret")
	defer ag.Close()
	dir := t.TempDir()
	srv := newCtl(t, dir, "", "", "")
	b := newClient(t, srv)

	if _, body := b.do("GET", "/ctl/v1/setup", "", ""); !strings.Contains(body, `"admin":false`) || !strings.Contains(body, `"logged_in":false`) {
		t.Fatalf("fresh setup = %s", body)
	}
	if code, _ := b.do("GET", "/ctl/v1/agent", "", ""); code != 401 {
		t.Errorf("before setup, the API needs a login -> %d", code)
	}
	if code, _ := b.do("POST", "/ctl/v1/setup/admin", `{"password":"short"}`, ""); code != 400 {
		t.Errorf("short password -> %d", code)
	}
	if code, _ := b.do("POST", "/ctl/v1/setup/admin", `{"password":"correct horse"}`, "http://evil.example"); code != 403 {
		t.Errorf("setup from another site -> %d", code)
	}
	if code, body := b.do("POST", "/ctl/v1/setup/admin", `{"password":"correct horse"}`, ""); code != 200 {
		t.Fatalf("set admin -> %d %s", code, body)
	}
	if _, body := b.do("GET", "/ctl/v1/setup", "", ""); !strings.Contains(body, `"admin":true`) || !strings.Contains(body, `"logged_in":true`) || !strings.Contains(body, `"agent":false`) {
		t.Fatalf("after step 1 = %s", body)
	}
	if code, _ := newClient(t, srv).do("POST", "/ctl/v1/setup/admin", `{"password":"another one"}`, ""); code != 409 {
		t.Errorf("a second admin password -> %d", code)
	}
	if code, body := b.do("GET", "/api/v1/status", "", ""); code != 503 || !strings.Contains(body, "agent_not_configured") {
		t.Errorf("no router yet -> %d %s", code, body)
	}

	// step 2: the router
	host := strings.TrimPrefix(ag.URL, "http://")
	if code, body := b.do("POST", "/ctl/v1/setup/agent", `{"url":"`+host+`","user":"root","password":"nope"}`, ""); code != 422 || !strings.Contains(body, "root") {
		t.Errorf("wrong root password -> %d %s", code, body)
	}
	if code, body := b.do("POST", "/ctl/v1/setup/agent", `{"url":"`+host+`","user":"root","password":"Hello world!"}`, ""); code != 200 || !strings.Contains(body, `"reachable":true`) {
		t.Fatalf("connect router -> %d %s", code, body)
	}
	if code, body := b.do("GET", "/api/v1/status", "", ""); code != 200 || !strings.Contains(body, `"engines"`) {
		t.Errorf("proxied after setup -> %d %s", code, body)
	}

	// kept across a restart; no password is written down in clear
	raw, _ := os.ReadFile(filepath.Join(dir, "controller.json"))
	if strings.Contains(string(raw), "Hello world!") || strings.Contains(string(raw), "correct horse") {
		t.Fatal("a password was stored in clear")
	}
	st2, _ := OpenStore(dir)
	if ref := st2.Agent(); ref == nil || ref.Token != "agent-secret" || ref.URL != ag.URL {
		t.Errorf("stored agent = %+v", ref)
	}
	if !st2.CheckAdmin("admin", "correct horse") || st2.CheckAdmin("admin", "correct horsE") {
		t.Error("stored admin password check")
	}
	if st, _ := os.Stat(filepath.Join(dir, "controller.json")); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("settings mode %v", st.Mode().Perm())
	}
}

func TestLoginLogout(t *testing.T) {
	ag, _ := fakeAgent(t, "a")
	defer ag.Close()
	srv := newCtl(t, t.TempDir(), ag.URL, "a", "correct horse")
	b := newClient(t, srv)
	if code, _ := b.do("POST", "/ctl/v1/auth/login", `{"user":"admin","password":"wrong"}`, ""); code != 401 {
		t.Errorf("wrong password -> %d", code)
	}
	if code, _ := b.do("POST", "/ctl/v1/auth/login", `{"user":"root","password":"correct horse"}`, ""); code != 401 {
		t.Errorf("wrong user -> %d", code)
	}
	b.login()
	if code, _ := b.do("GET", "/ctl/v1/agent", "", ""); code != 200 {
		t.Errorf("after login -> %d", code)
	}
	if code, _ := b.do("POST", "/api/v1/engines/usque/probe", "", "http://evil.example"); code != 403 {
		t.Errorf("cross-site write with the session -> %d", code)
	}
	if code, _ := b.do("POST", "/ctl/v1/auth/logout", "", ""); code != 200 {
		t.Fatalf("logout -> %d", code)
	}
	if code, _ := b.do("GET", "/ctl/v1/agent", "", ""); code != 401 {
		t.Errorf("after logout -> %d", code)
	}

	c := newClient(t, srv)
	code := 0
	for range 6 {
		code, _ = c.do("POST", "/ctl/v1/auth/login", `{"user":"admin","password":"x"}`, "")
	}
	if code != 429 {
		t.Errorf("6th failure -> %d", code)
	}
}

func TestControllerProxyAuthHistory(t *testing.T) {
	ag, _ := fakeAgent(t, "agent-secret")
	defer ag.Close()
	srv := newCtl(t, t.TempDir(), ag.URL, "agent-secret", "correct horse")

	// the browser must not be able to use the agent's token against the controller
	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/status", nil)
	req.Header.Set("Authorization", "Bearer agent-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("agent token accepted by controller -> %d", resp.StatusCode)
	}

	b := newClient(t, srv)
	b.login()
	code, body := b.do("GET", "/api/v1/status", "", "")
	if code != 200 || !strings.Contains(body, `"engines"`) {
		t.Fatalf("proxied status -> %d %s", code, body)
	}
	u, _ := url.Parse(srv.URL)
	for _, c := range b.c.Jar.Cookies(u) {
		if c.Name == "nuxk_agent" {
			t.Error("the agent's Set-Cookie leaked through the proxy")
		}
	}
	if code, _ := b.do("POST", "/api/v1/auth/login", `{}`, ""); code != 404 {
		t.Errorf("the agent's login through the controller -> %d", code)
	}
	_, body = b.do("GET", "/ctl/v1/agent", "", "")
	var st AgentState
	json.Unmarshal([]byte(body), &st)
	if !st.Reachable || !strings.Contains(string(st.Info), `"router"`) {
		t.Errorf("agent state = %s", body)
	}
	_, body = b.do("GET", "/ctl/v1/history", "", "")
	var s Series
	json.Unmarshal([]byte(body), &s)
	if s.WAN != "ppp0" || len(s.WanRx) != 1 || s.WanRx[0] != 8000 || s.NFQ[0] != 10 {
		t.Errorf("history = %s", body)
	}
}

func TestControllerStreamsEvents(t *testing.T) {
	ag, _ := fakeAgent(t, "a")
	defer ag.Close()
	srv := newCtl(t, t.TempDir(), ag.URL, "a", "correct horse")
	b := newClient(t, srv)
	b.login()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/v1/events", nil)
	resp, err := b.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != "event: status\n" {
		t.Fatalf("first line %q err %v — the proxy buffered the stream", line, err)
	}
}

func TestAgentDownIsReported(t *testing.T) {
	a := NewAgent("http://127.0.0.1:1", "x")
	a.poll(context.Background())
	if st := a.State("t"); st.Reachable || st.LastError == "" {
		t.Errorf("state = %+v", st)
	}
	st, _ := OpenStore(t.TempDir())
	st.SetAdmin("correct horse")
	srv := httptest.NewServer(NewServer(a, st, NewSessions(), "", "t"))
	defer srv.Close()
	b := newClient(t, srv)
	b.login()
	if code, _ := b.do("GET", "/api/v1/status", "", ""); code != 502 {
		t.Errorf("agent down -> %d", code)
	}
}

func TestIsTunnel(t *testing.T) {
	for name, want := range map[string]bool{"opkgtun0": true, "tun-xray": true, "tun0": true, "tunl0": false, "eth3": false, "nwg0": false} {
		if isTunnel(name) != want {
			t.Errorf("isTunnel(%q) = %v", name, !want)
		}
	}
}

func TestAgentBase(t *testing.T) {
	for in, want := range map[string]string{
		"192.168.2.1":             "http://192.168.2.1:4141",
		" 192.168.2.1:4141 ":      "http://192.168.2.1:4141",
		"http://router.lan:8080/": "http://router.lan:8080",
		"https://10.0.0.1":        "https://10.0.0.1:4141",
	} {
		if got, err := agentBase(in); err != nil || got != want {
			t.Errorf("agentBase(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "ftp://x", "http://"} {
		if _, err := agentBase(bad); err == nil {
			t.Errorf("agentBase(%q) must fail", bad)
		}
	}
}
