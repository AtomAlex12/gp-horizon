package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
			fmt.Fprint(w, `{"version":"x","engines":[]}`)
		}
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

func TestControllerProxyAuthHistory(t *testing.T) {
	ag, _ := fakeAgent(t, "agent-secret")
	defer ag.Close()
	a := NewAgent(ag.URL, "agent-secret")
	a.poll(context.Background())
	a.poll(context.Background())
	srv := httptest.NewServer(NewServer(a, "ui-secret", "", "t"))
	defer srv.Close()

	get := func(path, tok string) (*http.Response, string) {
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b := new(strings.Builder)
		bufio.NewReader(resp.Body).WriteTo(b)
		return resp, b.String()
	}
	if r, _ := get("/api/v1/status", ""); r.StatusCode != 401 {
		t.Errorf("no token -> %d", r.StatusCode)
	}
	// the browser must not be able to use the agent's token against the controller
	if r, _ := get("/api/v1/status", "agent-secret"); r.StatusCode != 401 {
		t.Errorf("agent token accepted by controller -> %d", r.StatusCode)
	}
	if r, body := get("/api/v1/status", "ui-secret"); r.StatusCode != 200 || !strings.Contains(body, `"engines"`) {
		t.Fatalf("proxied status -> %d %s", r.StatusCode, body)
	}
	_, body := get("/ctl/v1/agent", "ui-secret")
	var st AgentState
	json.Unmarshal([]byte(body), &st)
	if !st.Reachable || !strings.Contains(string(st.Info), `"router"`) {
		t.Errorf("agent state = %s", body)
	}
	_, body = get("/ctl/v1/history", "ui-secret")
	var s Series
	json.Unmarshal([]byte(body), &s)
	if s.WAN != "ppp0" || len(s.WanRx) != 1 || s.WanRx[0] != 8000 || s.NFQ[0] != 10 {
		t.Errorf("history = %s", body)
	}
}

func TestControllerStreamsEvents(t *testing.T) {
	ag, _ := fakeAgent(t, "a")
	defer ag.Close()
	srv := httptest.NewServer(NewServer(NewAgent(ag.URL, "a"), "u", "", "t"))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/v1/events", nil)
	req.Header.Set("Authorization", "Bearer u")
	resp, err := http.DefaultClient.Do(req)
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
	srv := httptest.NewServer(NewServer(a, "u", "", "t"))
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/status", nil)
	req.Header.Set("Authorization", "Bearer u")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 502 {
		t.Errorf("agent down -> %d", resp.StatusCode)
	}
}

func TestUITokenGeneratedOnceAndKept(t *testing.T) {
	dir := t.TempDir()
	a, err := uiToken("", dir)
	if err != nil || len(a) < 20 {
		t.Fatal(a, err)
	}
	b, _ := uiToken("", dir)
	if a != b {
		t.Error("token changed between starts")
	}
	if st, _ := os.Stat(filepath.Join(dir, "ui-token")); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
	if c, _ := uiToken("fixed", dir); c != "fixed" {
		t.Error("CONTROLLER_TOKEN must win")
	}
}
