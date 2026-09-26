package usque

import (
	"context"
	"testing"

	"nuxk.dev/horizon/core/internal/engine"
)

// fakeRunner returns canned S51usque output.
type fakeRunner struct {
	info    string
	probe   string
	actions []string
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

const sampleInfo = `name USQUE
version.pkg 0.4.0
version.usque 4.2.0
version.config 1
service.running 1
service.pid 1234
service.uptime_s 3600
tunnel.state connected
tunnel.since 1756668000
tunnel.endpoint 162.159.198.2
iface.name opkgtun0
iface.link up
iface.ip 172.16.1.100
config.sni ozon.ru
config.http2 0
traffic.rx_bytes 999
traffic.tx_bytes 111
routes.count 12
route 104.16.0.0/13
route 1.1.1.1/32
`

const sampleProbe = `ok 1
egress_ip 104.28.51.9
warp on
colo DME
loc RU
rtt_ms 18.4
ts 1756668123
`

func TestInfo(t *testing.T) {
	a := &Adapter{x: &fakeRunner{info: sampleInfo}}
	got, err := a.Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if got.Kind != engine.KindUsque {
		t.Errorf("kind = %q", got.Kind)
	}
	if !got.Running || got.PID != 1234 || got.UptimeSec != 3600 {
		t.Errorf("running/pid/uptime: %+v", got)
	}
	if got.Version != "4.2.0" || got.Iface != "opkgtun0" || got.Endpoint != "162.159.198.2" {
		t.Errorf("version/iface/endpoint: %+v", got)
	}
	if got.Health != engine.HealthOK {
		t.Errorf("health = %q, want ok", got.Health)
	}
	if got.Routes != 12 { // routes.count is authoritative over the 2 route lines
		t.Errorf("routes = %d, want 12", got.Routes)
	}
	if got.Detail["sni"] != "ozon.ru" {
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
	if !got.OK || got.EgressIP != "104.28.51.9" || got.RTTms != 18.4 || got.TS != 1756668123 {
		t.Errorf("probe: %+v", got)
	}
	if got.Detail["warp"] != "on" || got.Detail["colo"] != "DME" {
		t.Errorf("probe detail: %v", got.Detail)
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
