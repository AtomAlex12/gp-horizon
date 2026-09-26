package xray

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nuxk.dev/horizon/core/internal/engine"
)

// fakeRunner returns canned S52xray output.
type fakeRunner struct {
	info    string
	probe   string
	actions []string
	inputs  []string // "action:input" pairs, in call order
	fail    error
}

func (f *fakeRunner) KV(_ context.Context, sub string) (map[string]string, []string, error) {
	if f.fail != nil {
		return nil, nil, f.fail
	}
	switch sub {
	case "info":
		kv, list := engine.ParseKV(f.info)
		return kv, list, nil
	case "probe":
		kv, list := engine.ParseKV(f.probe)
		return kv, list, nil
	}
	return map[string]string{}, nil, nil
}

func (f *fakeRunner) Action(_ context.Context, action string) error {
	f.actions = append(f.actions, action)
	return f.fail
}

func (f *fakeRunner) ActionWithInput(_ context.Context, action, input string) (string, error) {
	f.inputs = append(f.inputs, action+":"+input)
	return "", f.fail
}

const sampleInfo = `name XRAY
version.xray 25.9.11
version.pkg 0.1.0
service.running 1
service.pid 777
service.uptime_s 300
tunnel.state connected
iface.name tun-xray
config.server 203.0.113.9:443
config.sni www.microsoft.com
config.fingerprint chrome
config.flow xtls-rprx-vision
traffic.rx_bytes 5000
traffic.tx_bytes 900
routes.count 4
route 0.0.0.0/0
`

const sampleProbe = `ok 1
egress_ip 203.0.113.9
server 203.0.113.9
rtt_ms 24.7
ts 1758268800
`

func TestInfo(t *testing.T) {
	a := &Adapter{x: &fakeRunner{info: sampleInfo}}
	got, err := a.Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if got.Kind != engine.KindXray {
		t.Errorf("kind = %q", got.Kind)
	}
	if !got.Running || got.PID != 777 || got.UptimeSec != 300 {
		t.Errorf("running/pid/uptime: %+v", got)
	}
	if got.Version != "25.9.11" || got.Iface != "tun-xray" || got.Endpoint != "203.0.113.9:443" {
		t.Errorf("version/iface/endpoint: %+v", got)
	}
	if got.Health != engine.HealthOK {
		t.Errorf("health = %q, want ok", got.Health)
	}
	if got.Routes != 4 { // routes.count is authoritative over the 1 route line
		t.Errorf("routes = %d, want 4", got.Routes)
	}
	if got.Detail["fingerprint"] != "chrome" || got.Detail["flow"] != "xtls-rprx-vision" {
		t.Errorf("detail fingerprint/flow: %v", got.Detail)
	}
	if got.Detail["sni"] != "www.microsoft.com" {
		t.Errorf("detail sni = %q", got.Detail["sni"])
	}
}

func TestInfoHealth(t *testing.T) {
	cases := []struct {
		info string
		want engine.Health
	}{
		{"service.running 1\ntunnel.state connected\n", engine.HealthOK},
		{"service.running 1\ntunnel.state disconnected\n", engine.HealthDegraded},
		{"service.running 1\ntunnel.state unknown\n", engine.HealthUnknown},
		{"service.running 0\ntunnel.state stopped\n", engine.HealthDown},
	}
	for _, c := range cases {
		a := &Adapter{x: &fakeRunner{info: c.info}}
		got, _ := a.Info(context.Background())
		if got.Health != c.want {
			t.Errorf("info %q -> health %q, want %q", c.info, got.Health, c.want)
		}
	}
}

func TestProbe(t *testing.T) {
	a := &Adapter{x: &fakeRunner{probe: sampleProbe}}
	got, err := a.Probe(context.Background(), nil)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !got.OK || got.EgressIP != "203.0.113.9" || got.RTTms != 24.7 || got.TS != 1758268800 {
		t.Errorf("probe: %+v", got)
	}
	if got.Detail["server"] != "203.0.113.9" {
		t.Errorf("probe detail server = %q", got.Detail["server"])
	}
}

func TestActions(t *testing.T) {
	f := &fakeRunner{}
	a := &Adapter{x: f}
	_ = a.Start(context.Background())
	_ = a.Restart(context.Background())
	_ = a.Stop(context.Background())
	if len(f.actions) != 3 || f.actions[0] != "start" || f.actions[2] != "stop" {
		t.Errorf("actions = %v", f.actions)
	}
}

func TestSetConfigVlessURI(t *testing.T) {
	f := &fakeRunner{}
	a := &Adapter{x: f, Iface: "opkgtun1"}
	if err := a.SetConfig(context.Background(), map[string]string{"vless_uri": realityLink}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if len(f.inputs) != 1 {
		t.Fatalf("inputs = %v, want 1 call", f.inputs)
	}
	in := strings.TrimPrefix(f.inputs[0], "set-config:")
	head, conf, ok := strings.Cut(in, "---\n")
	if !ok {
		t.Fatalf("no --- between meta and config: %q", in)
	}
	for _, want := range []string{"server 203.0.113.9:443\n", "endpoint 203.0.113.9:443\n", "security reality\n", "flow xtls-rprx-vision\n", "iface opkgtun1\n"} {
		if !strings.Contains(head, want) {
			t.Errorf("meta lacks %q: %q", want, head)
		}
	}
	if strings.Contains(head, "0e2b3c4d") {
		t.Error("the user id must not go into meta (info shows it)")
	}
	var c map[string]any
	if err := json.Unmarshal([]byte(conf), &c); err != nil {
		t.Fatalf("config is not JSON: %v", err)
	}
}

func TestSetConfigRefusals(t *testing.T) {
	f := &fakeRunner{}
	a := &Adapter{x: f, Iface: "opkgtun1"}
	for _, cfg := range []map[string]string{
		{},
		{"vless_uri": realityLink, "sub_url": "https://panel.example/sub/x"},
		{"vless_uri": "vmess://abc"},
		{"vless_uri": strings.Replace(realityLink, "pbk=", "nopbk=", 1)},
	} {
		if err := a.SetConfig(context.Background(), cfg); !errors.Is(err, engine.ErrBadConfig) {
			t.Errorf("%v: err = %v, want ErrBadConfig", cfg, err)
		}
	}
	if len(f.inputs) != 0 {
		t.Errorf("nothing may reach the router on a bad link: %v", f.inputs)
	}
}

func TestSetConfigSubURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 3x-ui: base64 of one link per line, other protocols mixed in
		body := "vmess://eyJhZGQiOiJ4In0=\n" + realityLink + "\n" + strings.Replace(realityLink, "203.0.113.9", "198.51.100.7", 1) + "\n"
		_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(body))))
	}))
	defer srv.Close()
	f := &fakeRunner{}
	a := &Adapter{x: f, Iface: "opkgtun1", HTTP: srv.Client()}
	if err := a.SetConfig(context.Background(), map[string]string{"sub_url": srv.URL + "/sub/xxxx"}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if len(f.inputs) != 1 || !strings.Contains(f.inputs[0], "server 203.0.113.9:443\n") {
		t.Errorf("the subscription's first VLESS server: %v", f.inputs)
	}
}

func TestApplyRoutingNoOp(t *testing.T) {
	f := &fakeRunner{}
	a := &Adapter{x: f}
	if err := a.ApplyRouting(context.Background(), engine.Routing{Domains: []string{"a.com"}}); err != nil {
		t.Fatalf("ApplyRouting: %v", err)
	}
	if len(f.actions) != 0 || len(f.inputs) != 0 {
		t.Errorf("ApplyRouting should not call the runner: actions=%v inputs=%v", f.actions, f.inputs)
	}
}
