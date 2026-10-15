package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nuxk.dev/horizon/core/internal/core"
	"nuxk.dev/horizon/core/internal/engine"
	"nuxk.dev/horizon/core/internal/plane"
	"nuxk.dev/horizon/core/internal/probe"
	"nuxk.dev/horizon/core/internal/state"
	"nuxk.dev/horizon/core/internal/suggest"
)

func TestSuggestAPI(t *testing.T) {
	deny := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte("unsupported_country_region_territory"))
	}))
	open := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer deny.Close()
	defer open.Close()
	p := &probe.Prober{
		Timeout:  2 * time.Second,
		LookupIP: func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("203.0.113.7")}, nil },
		Avail:    func(v probe.Via) string { return map[probe.Via]string{probe.ViaWarp: "выключен"}[v] },
		Dial: func(ctx context.Context, via probe.Via, _ string) (net.Conn, error) {
			s := deny
			if via == probe.ViaVless {
				s = open
			}
			var d net.Dialer
			return d.DialContext(ctx, "tcp", s.Listener.Addr().String())
		},
	}
	st, _ := state.Open(t.TempDir())
	reg := engine.NewRegistry()
	hub := core.NewHub("t")
	pm := plane.NewManager(&stubPlane{groups: map[string][]string{}}, st, plane.Config{Ifaces: map[plane.Mode]string{plane.ModeVless: "Wireguard1"}})
	sg := suggest.New(p, st, pm, func() bool { return false })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sg.Run(ctx)
	h := NewRouter(Deps{Version: "t", Engines: reg, Hub: hub, Ctl: core.NewController(reg, st, hub, "t"), Plane: pm, Suggest: sg})
	lo := "127.0.0.1:1"

	if w := do(h, "POST", "/api/v1/suggest/check", `{"domains":[]}`, lo, ""); w.Code != 400 {
		t.Errorf("empty check -> %d", w.Code)
	}
	if w := do(h, "POST", "/api/v1/suggest/check", `{"domains":["chatgpt.com"]}`, lo, ""); w.Code != 202 || !strings.Contains(w.Body.String(), `"chatgpt.com"`) {
		t.Fatalf("check -> %d %s", w.Code, w.Body)
	}
	var ov suggest.Overview
	for i := 0; i < 100; i++ {
		_ = json.Unmarshal(do(h, "GET", "/api/v1/suggest", "", lo, "").Body.Bytes(), &ov)
		if ov.Busy == 0 && len(ov.Items) == 1 && ov.Items[0].State == suggest.StateReady {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(ov.Items) != 1 || ov.Items[0].Mode != "vless" || !ov.Items[0].Confirmed {
		t.Fatalf("overview: %+v", ov)
	}
	if w := do(h, "POST", "/api/v1/suggest/chatgpt.com/accept", `{"mode":"warp"}`, lo, ""); w.Code != 400 {
		t.Errorf("unverified mode -> %d %s", w.Code, w.Body)
	}
	if w := do(h, "POST", "/api/v1/suggest/chatgpt.com/accept", ``, lo, ""); w.Code != 200 {
		t.Fatalf("accept -> %d %s", w.Code, w.Body)
	}
	if w := do(h, "GET", "/api/v1/plane/lists", "", lo, ""); !strings.Contains(w.Body.String(), `"suggest:vless"`) || !strings.Contains(w.Body.String(), `chatgpt.com`) {
		t.Errorf("plane lists after accept: %s", w.Body)
	}
	if w := do(h, "DELETE", "/api/v1/suggest/nope.com", "", lo, ""); w.Code != 404 {
		t.Errorf("dismiss unknown -> %d", w.Code)
	}

	off := NewRouter(Deps{Version: "t", Engines: reg, Hub: hub, Ctl: core.NewController(reg, st, hub, "t")})
	if w := do(off, "GET", "/api/v1/suggest", "", lo, ""); w.Code != 404 {
		t.Errorf("suggest off -> %d", w.Code)
	}
}
