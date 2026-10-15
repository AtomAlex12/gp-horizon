package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeGP speaks the bits of GP's API the controller uses: admin/admin until
// the password is changed, bearer tokens, a couple of core endpoints.
type fakeGP struct {
	mu       sync.Mutex
	password string
	tokens   map[string]bool
	n        int
	calls    []string
	// onChange runs when GP takes a new password; lose: GP takes it but the
	// answer never arrives whole
	onChange func()
	lose     bool
}

func (f *fakeGP) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var c struct{ Username, Password string }
		json.NewDecoder(r.Body).Decode(&c)
		f.mu.Lock()
		defer f.mu.Unlock()
		if c.Username != "admin" || c.Password != f.password {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"error":{"code":"unauthorized","message":"bad","details":{}}}`)
			return
		}
		f.n++
		tok := fmt.Sprintf("tok%d", f.n)
		f.tokens[tok] = true
		fmt.Fprintf(w, `{"access_token":%q,"token_type":"bearer","expires_in":86400}`, tok)
	})
	mux.HandleFunc("POST /api/auth/change-password", func(w http.ResponseWriter, r *http.Request) {
		var c struct {
			Current string `json:"current_password"`
			New     string `json:"new_password"`
		}
		json.NewDecoder(r.Body).Decode(&c)
		f.mu.Lock()
		defer f.mu.Unlock()
		if !f.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")] || c.Current != f.password {
			w.WriteHeader(401)
			return
		}
		f.password = c.New
		f.tokens = map[string]bool{} // GP rotates tokens on a password change
		if f.onChange != nil {
			f.onChange()
		}
		if f.lose {
			w.WriteHeader(502)
			return
		}
		f.n++
		tok := fmt.Sprintf("tok%d", f.n)
		f.tokens[tok] = true
		fmt.Fprintf(w, `{"access_token":%q,"token_type":"bearer","expires_in":86400}`, tok)
	})
	// size bytes; chunked: without a length, as a stream
	mux.HandleFunc("GET /api/core/strategy-candidates/export", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("size"))
		w.Header().Set("Content-Type", "application/x-ndjson")
		if r.URL.Query().Get("chunked") == "" {
			w.Header().Set("Content-Length", strconv.Itoa(n))
		} else {
			w.(http.Flusher).Flush()
		}
		w.Write([]byte(strings.Repeat("x", n)))
	})
	mux.HandleFunc("GET /api/core/backups/download-archive", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="gp-backup.zip"`)
		w.Write([]byte("PKzip"))
	})
	mux.HandleFunc("/api/service/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls = append(f.calls, r.Method+" "+r.URL.RequestURI())
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"state":"ready"}`)
	})
	mux.HandleFunc("/api/core/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		ok := f.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		f.calls = append(f.calls, r.Method+" "+r.URL.RequestURI())
		f.mu.Unlock()
		if !ok {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			w.WriteHeader(202)
			body, _ := json.Marshal(map[string]string{"got": readAll(r), "type": r.Header.Get("Content-Type")})
			w.Write(body)
			return
		}
		fmt.Fprint(w, `{"state":"idle"}`)
	})
	return mux
}

func readAll(r *http.Request) string {
	b := new(strings.Builder)
	buf := make([]byte, 512)
	for {
		n, err := r.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			return b.String()
		}
	}
}

func gpRunning(t *testing.T) string {
	return fakeSupervisor(t, func(r supReq) supResp {
		return supResp{OK: true, Plugins: []PluginInfo{{Name: "gp", Phase: "running", Listen: "127.0.0.1:1"}}}
	})
}

// GP takes the new password, then the store refuses to write it: it was
// kept as pending first, so the next login (after a restart too) finds it.
func TestGPPasswordSwitchStoreFails(t *testing.T) {
	gp := &fakeGP{password: "admin", tokens: map[string]bool{}}
	srv := httptest.NewServer(gp.handler())
	defer srv.Close()
	dir := t.TempDir()
	st, _ := OpenStore(dir)
	block := filepath.Join(dir, "controller.json.tmp")
	gp.onChange = func() { os.MkdirAll(filepath.Join(block, "x"), 0o755) }
	c := NewGPClient(srv.URL, st, NewPluginHost(gpRunning(t)))
	if _, err := c.Token(context.Background()); err != nil {
		t.Fatalf("GP took the password, the token works: %v", err)
	}
	if st.PluginSecret("gp") != "" || st.PluginNext("gp") != gp.password {
		t.Fatalf("pending %q, stored %q, GP has %q", st.PluginNext("gp"), st.PluginSecret("gp"), gp.password)
	}
	os.RemoveAll(block)
	gp.onChange = nil
	st2, _ := OpenStore(dir) // the controller restarted
	c2 := NewGPClient(srv.URL, st2, NewPluginHost(gpRunning(t)))
	if _, err := c2.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st2.PluginSecret("gp") != gp.password || st2.PluginNext("gp") != "" {
		t.Fatalf("switch not completed: stored %q, pending %q", st2.PluginSecret("gp"), st2.PluginNext("gp"))
	}
}

// GP takes the new password but the answer is lost: the next login uses the
// kept one instead of locking the controller out.
func TestGPPasswordSwitchAnswerLost(t *testing.T) {
	gp := &fakeGP{password: "admin", tokens: map[string]bool{}, lose: true}
	srv := httptest.NewServer(gp.handler())
	defer srv.Close()
	st, _ := OpenStore(t.TempDir())
	c := NewGPClient(srv.URL, st, NewPluginHost(gpRunning(t)))
	if _, err := c.Token(context.Background()); err == nil {
		t.Fatal("a lost answer is an error this time")
	}
	gp.mu.Lock()
	gp.lose = false
	gp.mu.Unlock()
	if _, err := c.Token(context.Background()); err != nil {
		t.Fatalf("locked out: %v", err)
	}
	if st.PluginSecret("gp") != gp.password {
		t.Fatalf("stored %q, GP has %q", st.PluginSecret("gp"), gp.password)
	}
}

// An answer bigger than the limit never reaches the browser as a whole-
// looking file: refused before it's sent, or the connection breaks.
func TestGPProxyLimits(t *testing.T) {
	old := gpReplyMax
	gpReplyMax = 1024
	t.Cleanup(func() { gpReplyMax = old })
	gp := &fakeGP{password: "admin", tokens: map[string]bool{}}
	srv := httptest.NewServer(gp.handler())
	defer srv.Close()
	st, _ := OpenStore(t.TempDir())
	c := NewGPClient(srv.URL, st, NewPluginHost(gpRunning(t)))
	mux := http.NewServeMux()
	mux.HandleFunc("/ctl/v1/gp/{path...}", c.handle)
	front := httptest.NewServer(mux)
	defer front.Close()
	get := func(q string) (*http.Response, []byte, error) {
		resp, err := http.Get(front.URL + "/ctl/v1/gp/strategy-candidates/export?" + q)
		if err != nil {
			return nil, nil, err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		return resp, b, err
	}
	if resp, b, err := get("size=1000"); err != nil || resp.StatusCode != 200 || len(b) != 1000 || resp.ContentLength != 1000 {
		t.Fatalf("within the limit: %v %v %d", err, resp, len(b))
	}
	if resp, b, err := get("size=2048"); err != nil || resp.StatusCode != 502 || !strings.Contains(string(b), "больше") {
		t.Fatalf("known too big: %v %v %s", err, resp, b)
	}
	if _, b, err := get("size=2048&chunked=1"); err == nil {
		t.Fatalf("a stream past the limit must break the download, got %d bytes whole", len(b))
	}
}

func TestGPProxy(t *testing.T) {
	gp := &fakeGP{password: "admin", tokens: map[string]bool{}}
	srv := httptest.NewServer(gp.handler())
	defer srv.Close()
	phase := "running"
	sock := fakeSupervisor(t, func(r supReq) supResp {
		return supResp{OK: true, Plugins: []PluginInfo{{Name: "gp", Phase: phase, Listen: "127.0.0.1:1"}}}
	})
	st, _ := OpenStore(t.TempDir())
	c := NewGPClient(srv.URL, st, NewPluginHost(sock))
	mux := http.NewServeMux()
	mux.HandleFunc("/ctl/v1/gp/{path...}", c.handle)
	do := func(method, path, body string) (int, string) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w.Code, w.Body.String()
	}

	if code, body := do("GET", "/ctl/v1/gp/status", ""); code != 200 || !strings.Contains(body, `"idle"`) {
		t.Fatalf("status -> %d %s", code, body)
	}
	pw := st.PluginSecret("gp")
	if pw == "" || pw == "admin" || gp.password != pw {
		t.Fatalf("factory password not replaced: stored %q, gp has %q", pw, gp.password)
	}
	if code, body := do("POST", "/ctl/v1/gp/strategy-discovery/start-run", `{"domains":["rutracker.org"]}`); code != 202 || !strings.Contains(body, "rutracker.org") {
		t.Errorf("start-run -> %d %s", code, body)
	}
	if code, _ := do("GET", "/ctl/v1/gp/strategy-candidates?domain=rutracker.org", ""); code != 200 {
		t.Errorf("candidates -> %d", code)
	}
	if last := gp.calls[len(gp.calls)-1]; last != "GET /api/core/strategy-candidates?domain=rutracker.org" {
		t.Errorf("query not passed through: %s", last)
	}

	// GP restarted and forgot its tokens: the proxy logs in again with the stored password
	gp.mu.Lock()
	gp.tokens = map[string]bool{}
	gp.mu.Unlock()
	if code, _ := do("GET", "/ctl/v1/gp/status", ""); code != 200 {
		t.Errorf("after GP lost its tokens -> %d", code)
	}
	if st.PluginSecret("gp") != pw {
		t.Error("a relogin must not rotate the password again")
	}

	// GP's service API: under /api/service, not /api/core
	if code, _ := do("GET", "/ctl/v1/gp/service/v2fly/local-storage-status", ""); code != 200 || gp.calls[len(gp.calls)-1] != "GET /api/service/v2fly/local-storage-status" {
		t.Errorf("service -> %d, %s", code, gp.calls[len(gp.calls)-1])
	}
	// a backup archive goes up as it is, application/zip only; comes down with its name
	up := func(ct, body string) (int, string) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/ctl/v1/gp/backups/upload", strings.NewReader(body))
		req.Header.Set("Content-Type", ct)
		mux.ServeHTTP(w, req)
		return w.Code, w.Body.String()
	}
	if code, body := up("application/zip", "PK-archive"); code != 202 || !strings.Contains(body, `"type":"application/zip"`) || !strings.Contains(body, "PK-archive") {
		t.Errorf("upload -> %d %s", code, body)
	}
	if code, _ := up("text/plain", "x"); code != 415 {
		t.Errorf("upload as text -> %d", code)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/ctl/v1/gp/backups/download-archive?snapshot_id=s1", nil))
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Disposition"), "gp-backup.zip") || w.Header().Get("Content-Type") != "application/zip" {
		t.Errorf("download -> %d %v", w.Code, w.Header())
	}

	// never GP's own auth, its installer's vaults, its web UI's slices
	for _, p := range []string{"/ctl/v1/gp/../auth/change-password", "/ctl/v1/gp/clean-install-vaults/restore", "/ctl/v1/gp/status/../../auth/login", "/ctl/v1/gp/service/../web/presets/save", "/ctl/v1/gp/web/presets/save"} {
		if code, _ := do("POST", p, "{}"); code != 404 && code != 301 && code != 307 {
			t.Errorf("POST %s -> %d, must not reach GP", p, code)
		}
	}
	if code, _ := do("DELETE", "/ctl/v1/gp/status", ""); code != 404 {
		t.Errorf("wrong method -> %d", code)
	}

	phase = "stopped"
	if code, body := do("GET", "/ctl/v1/gp/status", ""); code != 503 || !strings.Contains(body, "не запущен") {
		t.Errorf("GP stopped -> %d %s", code, body)
	}
	phase = "running"

	// GP's data was reset to admin/admin while we still hold the old password
	gp.mu.Lock()
	gp.password, gp.tokens = "admin", map[string]bool{}
	gp.mu.Unlock()
	c.forget()
	if code, _ := do("GET", "/ctl/v1/gp/status", ""); code != 200 || st.PluginSecret("gp") == pw {
		t.Errorf("reset GP must be taken over again -> %d", code)
	}
	// and a GP that takes neither
	gp.mu.Lock()
	gp.password, gp.tokens = "someone-else", map[string]bool{}
	gp.mu.Unlock()
	c.forget()
	if code, body := do("GET", "/ctl/v1/gp/status", ""); code != 502 || !strings.Contains(body, "не принял пароль") {
		t.Errorf("foreign password -> %d %s", code, body)
	}
}
