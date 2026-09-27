package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const (
	linkHome = "vless://0e2b3c4d-1111-4a2b-9c3d-5e6f7a8b9c0d@203.0.113.9:443?type=tcp&security=reality&pbk=KEYONE&sni=a.example&flow=xtls-rprx-vision#home"
	linkNL   = "vless://0e2b3c4d-1111-4a2b-9c3d-5e6f7a8b9c0d@198.51.100.7:8443?type=tcp&security=reality&pbk=KEYONE&sni=a.example#nl"
)

// routerXray is the agent's side: it takes a link on PUT /engines/xray/config.
type routerXray struct {
	mu   sync.Mutex
	got  []string
	fail string // a 400 with this message
}

func (x *routerXray) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v1/engines/xray/config" || r.Header.Get("Authorization") != "Bearer agent-secret" {
			w.WriteHeader(404)
			return
		}
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		x.mu.Lock()
		defer x.mu.Unlock()
		if x.fail != "" {
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"error":{"code":"bad_config","message":"`+x.fail+`"}}`)
			return
		}
		x.got = append(x.got, in["vless_uri"])
		_, _ = io.WriteString(w, `{"source":"link","servers":[],"active":0}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (x *routerXray) last() string {
	x.mu.Lock()
	defer x.mu.Unlock()
	if len(x.got) == 0 {
		return ""
	}
	return x.got[len(x.got)-1]
}

// panel is a 3x-ui subscription whose content the test changes.
type panel struct {
	mu    sync.Mutex
	links []string
}

func (p *panel) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		w.Header().Set("Profile-Title", "base64:"+base64.StdEncoding.EncodeToString([]byte("Мой VPN")))
		w.Header().Set("Subscription-Userinfo", "upload=10; download=20; total=1000; expire=1893456000")
		w.Header().Set("Profile-Update-Interval", "12")
		_, _ = io.WriteString(w, base64.StdEncoding.EncodeToString([]byte(strings.Join(p.links, "\n")+"\nvless://broken\n")))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (p *panel) set(links ...string) {
	p.mu.Lock()
	p.links = links
	p.mu.Unlock()
}

func TestVlessSourcesOnThePi(t *testing.T) {
	var x routerXray
	ag := x.server(t)
	var p panel
	p.set(linkHome, linkNL)
	sub := p.server(t)
	subURL := sub.URL + "/sub/s3cr3t-token"

	srv := newCtl(t, t.TempDir(), ag.URL, "agent-secret", "correct horse")
	b := newClient(t, srv)
	if code, _ := b.do("GET", "/ctl/v1/vless", "", ""); code != 401 {
		t.Fatalf("anonymous -> %d", code)
	}
	b.login()
	secret := func(body string) {
		t.Helper()
		for _, s := range []string{"0e2b3c4d", "s3cr3t-token", "KEYONE"} {
			if strings.Contains(body, s) {
				t.Fatalf("a secret (%s) in the answer: %s", s, body)
			}
		}
	}

	// a link and a subscription
	code, body := b.do("POST", "/ctl/v1/vless/sources", `{"kind":"link","url":"`+linkHome+`","name":"мой сервер"}`, "")
	if code != 200 || !strings.Contains(body, `"key":"home@203.0.113.9:443"`) {
		t.Fatalf("add link -> %d %s", code, body)
	}
	secret(body)
	if code, body := b.do("POST", "/ctl/v1/vless/sources", `{"kind":"link","url":"vmess://x"}`, ""); code != 400 || !strings.Contains(body, "vless://") {
		t.Errorf("bad link -> %d %s", code, body)
	}
	code, body = b.do("POST", "/ctl/v1/vless/sources", `{"kind":"subscription","url":"`+subURL+`"}`, "")
	if code != 200 {
		t.Fatalf("add subscription -> %d %s", code, body)
	}
	secret(body)
	var view VlessView
	_ = json.Unmarshal([]byte(body), &view)
	if len(view.Sources) != 2 {
		t.Fatalf("sources: %s", body)
	}
	s := view.Sources[1]
	if s.Title != "Мой VPN" || len(s.Servers) != 2 || s.Skipped != 1 || s.Usage == nil || s.Usage.Total != 1000 || s.RefreshS != 12*3600 {
		t.Errorf("subscription view: %+v", s)
	}
	if code, _ := b.do("POST", "/ctl/v1/vless/sources", `{"kind":"subscription","url":"`+subURL+`"}`, ""); code != 400 {
		t.Errorf("the same subscription twice -> %d", code)
	}

	// to the router: the one link, never the subscription
	code, body = b.do("POST", "/ctl/v1/vless/use", `{"source":"`+s.ID+`","server":"nl@198.51.100.7:8443"}`, "")
	if code != 200 || x.last() != linkNL || !strings.Contains(body, `"key":"nl@198.51.100.7:8443"`) {
		t.Fatalf("use nl -> %d %s; router got %q", code, body, x.last())
	}
	secret(body)
	if code, _ := b.do("POST", "/ctl/v1/vless/use", `{"source":"`+s.ID+`","server":"nope@1.1.1.1:1"}`, ""); code != 404 {
		t.Errorf("unknown server -> %d", code)
	}

	// the panel rotated nl's keys: the router gets the new link on refresh
	rotated := strings.Replace(linkNL, "KEYONE", "KEYTWO", 1)
	p.set(linkHome, rotated)
	if code, body := b.do("POST", "/ctl/v1/vless/sources/"+s.ID+"/refresh", "", ""); code != 200 || x.last() != rotated {
		t.Fatalf("refresh -> %d %s; router got %q", code, body, x.last())
	}
	// nothing new: nothing sent
	n := len(x.got)
	b.do("POST", "/ctl/v1/vless/sources/"+s.ID+"/refresh", "", "")
	if len(x.got) != n {
		t.Error("an unchanged subscription re-sent the link")
	}
	// nl is gone: the router keeps it, the source says so
	p.set(linkHome)
	_, body = b.do("POST", "/ctl/v1/vless/sources/"+s.ID+"/refresh", "", "")
	if len(x.got) != n || !strings.Contains(body, "больше нет в подписке") {
		t.Errorf("server gone: sent=%d body=%s", len(x.got)-n, body)
	}

	// the router refuses a link: its reason, a 400
	x.mu.Lock()
	x.fail = "для Reality нужен sni"
	x.mu.Unlock()
	if code, body := b.do("POST", "/ctl/v1/vless/use", `{"source":"`+view.Sources[0].ID+`","server":"home@203.0.113.9:443"}`, ""); code != 400 || !strings.Contains(body, "нужен sni") {
		t.Errorf("router refuses -> %d %s", code, body)
	}

	// removing the active source forgets which one it was; the router runs on
	if code, body := b.do("DELETE", "/ctl/v1/vless/sources/"+s.ID, "", ""); code != 200 || strings.Contains(body, `"active"`) {
		t.Errorf("delete -> %d %s", code, body)
	}
	if code, _ := b.do("DELETE", "/ctl/v1/vless/sources/nope", "", ""); code != 404 {
		t.Errorf("delete unknown -> %d", code)
	}
	// a write from another site is refused, like every other write here
	if code, _ := b.do("POST", "/ctl/v1/vless/sources", `{"kind":"link","url":"`+linkNL+`"}`, "https://evil.example"); code != 403 {
		t.Errorf("cross-site write -> %d", code)
	}
}
