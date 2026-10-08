package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nuxk.dev/horizon/core/internal/core"
	"nuxk.dev/horizon/core/internal/engine"
	"nuxk.dev/horizon/core/internal/logbuf"
	"nuxk.dev/horizon/core/internal/node"
	"nuxk.dev/horizon/core/internal/state"
)

func agentRouter(t *testing.T) (http.Handler, *logbuf.Ring) {
	t.Helper()
	st, _ := state.Open(t.TempDir())
	reg := engine.NewRegistry()
	hub := core.NewHub("test")
	ring := logbuf.NewRing(10)
	return NewRouter(Deps{
		Version: "t", Engines: reg, Hub: hub, Ctl: core.NewController(reg, st, hub, "t"),
		Token: "tok", Logs: ring, Debug: logbuf.NewDebug(false), Node: node.New("stand", "", "t", "c"),
	}), ring
}

func TestAgentInfoMetricsLogs(t *testing.T) {
	h, ring := agentRouter(t)
	lan := "192.168.2.20:5000"
	if w := do(h, "GET", "/api/v1/info", "", lan, ""); w.Code != 401 {
		t.Fatalf("info without token -> %d", w.Code)
	}
	if w := do(h, "GET", "/api/v1/info", "", lan, "tok"); w.Code != 200 || !strings.Contains(w.Body.String(), `"role":"stand"`) {
		t.Fatalf("info -> %d %s", w.Code, w.Body)
	}
	if w := do(h, "GET", "/api/v1/metrics", "", lan, "tok"); w.Code != 200 || !strings.Contains(w.Body.String(), `"ifaces"`) {
		t.Fatalf("metrics -> %d %s", w.Code, w.Body)
	}
	log := slog.New(logbuf.NewHandler(io.Discard, ring, slog.LevelInfo, slog.LevelInfo))
	log.Info("one")
	log.Warn("two")
	if w := do(h, "GET", "/api/v1/logs?after=1", "", lan, "tok"); !strings.Contains(w.Body.String(), `"msg":"two"`) || strings.Contains(w.Body.String(), `"one"`) {
		t.Fatalf("logs after=1 -> %s", w.Body)
	}
}

func TestSecurityHeadersAndBodyLimit(t *testing.T) {
	h, _ := agentRouter(t)
	w := do(h, "GET", "/api/v1/healthz", "", "10.0.0.1:1", "")
	if w.Header().Get("X-Frame-Options") != "DENY" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("headers = %v", w.Header())
	}
	big := `{"x":"` + strings.Repeat("a", maxBody+10) + `"}`
	if w := do(h, "PUT", "/api/v1/engines/xray/config", big, "10.0.0.1:1", "tok"); w.Code != 400 {
		t.Errorf("oversized body -> %d", w.Code)
	}
}

// The SSE stream must survive the server's WriteTimeout and deliver status
// on connect and log entries as they happen.
func TestEventsStream(t *testing.T) {
	h, ring := agentRouter(t)
	srv := httptest.NewUnstartedServer(h)
	srv.Config.WriteTimeout = 300 * time.Millisecond
	srv.Start()
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/v1/events", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type %q", ct)
	}
	rd := bufio.NewReader(resp.Body)
	next := func() string {
		for {
			l, err := rd.ReadString('\n')
			if err != nil {
				t.Fatalf("stream ended: %v", err)
			}
			if strings.HasPrefix(l, "event: ") {
				return strings.TrimSpace(strings.TrimPrefix(l, "event: "))
			}
		}
	}
	if ev := next(); ev != "status" {
		t.Fatalf("first event %q", ev)
	}
	time.Sleep(500 * time.Millisecond) // past WriteTimeout
	slog.New(logbuf.NewHandler(io.Discard, ring, slog.LevelInfo, slog.LevelInfo)).Info("hello")
	if ev := next(); ev != "log" {
		t.Fatalf("second event %q", ev)
	}
}

func TestLogsDebugSwitch(t *testing.T) {
	h, _ := agentRouter(t)
	lan := "192.168.2.20:5000"
	if w := do(h, "GET", "/api/v1/logs/debug", "", lan, "tok"); w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"on":false}` {
		t.Fatalf("off -> %d %s", w.Code, w.Body)
	}
	w := do(h, "PUT", "/api/v1/logs/debug", `{"on":true,"minutes":15}`, lan, "tok")
	var st logbuf.DebugState
	json.Unmarshal(w.Body.Bytes(), &st)
	left := time.Until(time.UnixMilli(st.Until))
	if w.Code != 200 || !st.On || left < 14*time.Minute || left > 15*time.Minute {
		t.Fatalf("on 15 min -> %d %s", w.Code, w.Body)
	}
	for _, bad := range []string{`{"on":true,"minutes":241}`, `{"on":true,"minutes":-1}`, `nope`} {
		if w := do(h, "PUT", "/api/v1/logs/debug", bad, lan, "tok"); w.Code != 400 {
			t.Errorf("%s -> %d", bad, w.Code)
		}
	}
	if w := do(h, "PUT", "/api/v1/logs/debug", `{"on":false}`, lan, "tok"); w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"on":false}` {
		t.Fatalf("off again -> %d %s", w.Code, w.Body)
	}
	if w := do(h, "PUT", "/api/v1/logs/debug", `{"on":true}`, lan, ""); w.Code != 401 {
		t.Fatalf("without token -> %d", w.Code)
	}
}
