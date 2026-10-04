package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRouter: the agent's /api/v1/update — at "current" until asked to
// update, then at "to" after two looks (or a failed run, if fail).
type fakeRouter struct {
	mu      sync.Mutex
	current string
	latest  string
	to      string
	looks   int
	fail    bool
	posted  int
}

// seen: what it was asked and where it is now
func (f *fakeRouter) seen() (posted int, current string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.posted, f.current
}

func (f *fakeRouter) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/update":
			var in struct{ Version string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			f.to, f.posted = in.Version, f.posted+1
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/update":
			run := any(nil)
			if f.to != "" {
				f.looks++
				state := "running"
				if f.looks > 2 {
					state = "done"
					if f.fail {
						state = "rolled_back"
					} else {
						f.current = f.to
					}
				}
				run = map[string]string{"state": state, "to": f.to, "message": "новая версия не ответила"}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"current": f.current, "latest": map[string]string{"version": f.latest}, "run": run})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func selfUpdater(t *testing.T, router *fakeRouter, current string, helper bool) (*SelfUpdater, string, string) {
	t.Helper()
	dir, data := t.TempDir(), t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "inbox"), 0o777)
	if helper {
		_ = os.WriteFile(filepath.Join(dir, helperFile), []byte("0.5.0\n"), 0o644)
	}
	ag := NewAgent("", "")
	ag.Configure(router.server(t).URL, "tok")
	u := NewSelfUpdater(dir, data, current, ag, NewSessions())
	u.poll = 10 * time.Millisecond
	return u, dir, data
}

func waitAll(t *testing.T, u *SelfUpdater, f func(*AllRun) bool) *AllRun {
	t.Helper()
	for i := 0; i < 300; i++ {
		if a := u.readAll(); a != nil && f(a) {
			return a
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out: %+v", u.readAll())
	return nil
}

// «Обновить всё»: the router first, then a request for the Pi's helper; the
// new controller, started, finishes the record.
func TestUpdateAll(t *testing.T) {
	router := &fakeRouter{current: "0.5.0", latest: "0.5.1"}
	u, dir, data := selfUpdater(t, router, "0.5.0", true)
	ctx := context.Background()
	if st := u.Status(ctx); !st.Available || !st.CanApply || st.Latest != "0.5.1" || st.Router != "0.5.0" {
		t.Fatalf("status: %+v", st)
	}
	if err := u.StartAll(ctx, "0.5.0"); !errors.Is(err, errUpdStale) {
		t.Fatalf("not the newest: %v", err)
	}
	if err := u.StartAll(ctx, "0.5.1"); err != nil {
		t.Fatal(err)
	}
	if err := u.StartAll(ctx, "0.5.1"); !errors.Is(err, errUpdBusy) {
		t.Fatalf("twice: %v", err)
	}
	a := waitAll(t, u, func(a *AllRun) bool { return a.Step == "controller" })
	if posted, cur := router.seen(); a.State != "running" || posted != 1 || cur != "0.5.1" {
		t.Fatalf("after the router: %+v, posted %d, at %s", a, posted, cur)
	}
	req, err := os.ReadFile(filepath.Join(dir, requestFile))
	if err != nil || !strings.HasPrefix(string(req), "version 0.5.1\n") {
		t.Fatalf("request: %q %v", req, err)
	}
	if r := u.Status(ctx).Run; r == nil || r.State != "running" {
		t.Fatalf("waiting for the helper: %+v", r)
	}
	// the Pi's helper took it; the new controller starts on the same data
	_ = os.Remove(filepath.Join(dir, requestFile))
	_ = os.WriteFile(filepath.Join(dir, hostStatus), []byte("state done\nfrom 0.5.0\nto 0.5.1\nmessage контроллер 0.5.1 работает \n"), 0o644)
	next := NewSelfUpdater(dir, data, "0.5.1", u.ag, NewSessions())
	next.Resume()
	if st := next.Status(ctx); st.All == nil || st.All.State != "done" || st.Available || st.Run.Message != "контроллер 0.5.1 работает" {
		t.Fatalf("done: %+v %+v", st.All, st.Run)
	}
}

// The router's update didn't go: the controller stays as it is.
func TestUpdateAllRouterFails(t *testing.T) {
	router := &fakeRouter{current: "0.5.0", latest: "0.5.1", fail: true}
	u, dir, _ := selfUpdater(t, router, "0.5.0", true)
	if err := u.StartAll(context.Background(), "0.5.1"); err != nil {
		t.Fatal(err)
	}
	a := waitAll(t, u, func(a *AllRun) bool { return a.State != "running" })
	if a.State != "failed" || !strings.Contains(a.Message, "новая версия не ответила") {
		t.Fatalf("%+v", a)
	}
	if _, err := os.Stat(filepath.Join(dir, requestFile)); err == nil {
		t.Fatal("the controller asked for an update after the router failed")
	}
}

// The router already there: straight to the controller. Without the Pi's
// helper the controller can't be updated from here.
func TestUpdateAllControllerOnly(t *testing.T) {
	router := &fakeRouter{current: "0.5.1", latest: "0.5.1"}
	u, _, _ := selfUpdater(t, router, "0.5.0", false)
	ctx := context.Background()
	if st := u.Status(ctx); st.CanApply || !strings.Contains(st.Cannot, "помощника") {
		t.Fatalf("no helper: %+v", st)
	}
	if err := u.StartAll(ctx, "0.5.1"); !errors.Is(err, errUpdCannot) {
		t.Fatalf("no helper: %v", err)
	}
	_ = os.WriteFile(filepath.Join(u.dir, helperFile), []byte("x"), 0o644)
	if err := u.Start(ctx, "0.5.1"); err != nil {
		t.Fatal(err)
	}
	if posted, _ := router.seen(); posted != 0 {
		t.Fatal("the router asked to update to its own version")
	}
	// the helper didn't take it within a minute: said so
	u.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if r := u.Status(ctx).Run; r.State != "failed" || !strings.Contains(r.Message, "nuxk-update.path") {
		t.Fatalf("not picked up: %+v", r)
	}
	// both there: nothing to do
	u2, _, _ := selfUpdater(t, &fakeRouter{current: "0.5.1", latest: "0.5.1"}, "0.5.1", true)
	if err := u2.StartAll(ctx, "0.5.1"); !errors.Is(err, errUpdNothing) {
		t.Fatalf("nothing: %v", err)
	}
}

// The logins go to the controller that replaces this one, as hashes; an old
// handoff is ignored.
func TestSessionsHandoff(t *testing.T) {
	path := filepath.Join(t.TempDir(), handoffFile)
	s := NewSessions()
	id, _ := s.New()
	if err := s.Handoff(path); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), id) {
		t.Fatal("a session id written to disk")
	}
	next := NewSessions()
	if n := next.TakeHandoff(path); n != 1 || !next.Valid(id) || next.Valid("other") {
		t.Fatalf("taken %d", n)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("the handoff file stays")
	}
	_ = s.Handoff(path)
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(path, old, old)
	if n := NewSessions().TakeHandoff(path); n != 0 {
		t.Fatal("an old handoff taken")
	}
}
