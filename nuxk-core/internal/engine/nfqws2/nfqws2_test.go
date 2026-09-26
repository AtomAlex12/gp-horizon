package nfqws2

import (
	"context"
	"strings"
	"testing"

	"nuxk.dev/horizon/core/internal/engine"
)

// fakeRunner returns canned S51nfqws2 output.
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

func (f *fakeRunner) KVWithInput(ctx context.Context, sub, input string) (map[string]string, []string, error) {
	f.inputs = append(f.inputs, sub+":"+input)
	return f.KV(ctx, sub)
}

func (f *fakeRunner) Action(_ context.Context, action string) error {
	f.actions = append(f.actions, action)
	return f.fail
}

func (f *fakeRunner) ActionWithInput(_ context.Context, action, input string) (string, error) {
	f.inputs = append(f.inputs, action+":"+input)
	return "", f.fail
}

const sampleInfo = `name NFQWS2
service.running 1
service.pid 4242
service.uptime_s 120
config.iface eth0
config.nfqueue_num 300
firewall.installed 1
lists.desync_count 2
lists.endpoints_count 1
lists.auto_count 3
item www.youtube.com
item browserleaks.com
`

const sampleProbe = `ok 1
target browserleaks.com
rtt_ms 42.1
ts 1758268800
`

func TestInfo(t *testing.T) {
	a := &Adapter{x: &fakeRunner{info: sampleInfo}}
	got, err := a.Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if got.Kind != engine.KindNfqws2 {
		t.Errorf("kind = %q", got.Kind)
	}
	if !got.Running || got.PID != 4242 || got.UptimeSec != 120 {
		t.Errorf("running/pid/uptime: %+v", got)
	}
	if got.Iface != "eth0" {
		t.Errorf("iface = %q, want eth0", got.Iface)
	}
	if got.Health != engine.HealthOK {
		t.Errorf("health = %q, want ok", got.Health)
	}
	if got.Routes != 3 { // desync_count(2) + endpoints_count(1)
		t.Errorf("routes = %d, want 3", got.Routes)
	}
	if got.Detail["nfqueue_num"] != "300" {
		t.Errorf("detail nfqueue_num = %q", got.Detail["nfqueue_num"])
	}
}

func TestInfoHealth(t *testing.T) {
	cases := []struct {
		info string
		want engine.Health
	}{
		{"service.running 1\nfirewall.installed 1\n", engine.HealthOK},
		{"service.running 1\nfirewall.installed 0\n", engine.HealthDegraded},
		{"service.running 0\nfirewall.installed 1\n", engine.HealthDown},
		{"service.running 0\nfirewall.installed 0\n", engine.HealthDown},
	}
	for _, c := range cases {
		a := &Adapter{x: &fakeRunner{info: c.info}}
		got, _ := a.Info(context.Background())
		if got.Health != c.want {
			t.Errorf("info %q -> health %q, want %q", c.info, got.Health, c.want)
		}
	}
}

// a shim from before per-site checks: one canary, ok/target/rtt_ms
func TestProbeOldShim(t *testing.T) {
	a := &Adapter{x: &fakeRunner{probe: sampleProbe}}
	got, err := a.Probe(context.Background(), nil)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !got.OK || got.RTTms != 42.1 || got.TS != 1758268800 {
		t.Errorf("probe: %+v", got)
	}
	if got.Detail["target"] != "browserleaks.com" {
		t.Errorf("probe detail target = %q", got.Detail["target"])
	}
}

func TestProbeSites(t *testing.T) {
	// the parallel checks finish in any order
	out := "check b.com 0 8001 tls_timeout\ncheck c.com 1 120\ncheck a.com 1 350\ncheck d.com 0 30 reset\nts 1758268800\n"
	f := &fakeRunner{probe: out}
	a := &Adapter{x: f}
	got, err := a.Probe(context.Background(), []string{"a.com", "b.com", "c.com", "d.com"})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.inputs) != 1 || f.inputs[0] != "probe:a.com\nb.com\nc.com\nd.com" {
		t.Errorf("sites to the shim: %q", f.inputs)
	}
	var order []string
	for _, c := range got.Checks {
		order = append(order, c.Domain)
	}
	if strings.Join(order, ",") != "a.com,b.com,c.com,d.com" {
		t.Errorf("checks in the order asked: %v", order)
	}
	if !got.OK || got.RTTms != 120 || got.Detail["opened"] != "2" || got.Detail["total"] != "4" {
		t.Errorf("two of four open → ok, fastest rtt: %+v", got)
	}
	if b := got.Checks[1]; b.OK || b.Reason != engine.ReasonTLSTimeout {
		t.Errorf("b.com: %+v", b)
	}

	// none opens: not ok, the commonest reason
	f.probe = "check a.com 0 30 reset\ncheck b.com 0 8001 tls_timeout\ncheck c.com 0 8001 tls_timeout\nts 1\n"
	got, _ = a.Probe(context.Background(), []string{"a.com", "b.com", "c.com"})
	if got.OK || got.Reason != engine.ReasonTLSTimeout || got.Detail["opened"] != "0" {
		t.Errorf("none opens: %+v", got)
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

func TestApplyRouting(t *testing.T) {
	f := &fakeRunner{}
	a := &Adapter{x: f}
	err := a.ApplyRouting(context.Background(), engine.Routing{
		Domains:   []string{"www.youtube.com", "browserleaks.com"},
		Endpoints: []string{"162.159.198.2"},
	})
	if err != nil {
		t.Fatalf("ApplyRouting: %v", err)
	}
	if len(f.inputs) != 2 {
		t.Fatalf("inputs = %v, want 2 calls", f.inputs)
	}
	if f.inputs[0] != "apply-desync:www.youtube.com\nbrowserleaks.com" {
		t.Errorf("apply-desync input = %q", f.inputs[0])
	}
	if f.inputs[1] != "apply-endpoints:162.159.198.2" {
		t.Errorf("apply-endpoints input = %q", f.inputs[1])
	}
}

func TestApplyRoutingNoEndpoints(t *testing.T) {
	f := &fakeRunner{}
	a := &Adapter{x: f}
	if err := a.ApplyRouting(context.Background(), engine.Routing{Domains: []string{"a.com"}}); err != nil {
		t.Fatalf("ApplyRouting: %v", err)
	}
	if len(f.inputs) != 1 {
		t.Errorf("inputs = %v, want only apply-desync", f.inputs)
	}
}

// nil Domains = endpoints-only push (controller hardening) — the desync list
// must not be wiped; an empty non-nil Endpoints clears the endpoints list.
func TestApplyRoutingNilDomainsEmptyEndpoints(t *testing.T) {
	f := &fakeRunner{}
	a := &Adapter{x: f}
	if err := a.ApplyRouting(context.Background(), engine.Routing{Endpoints: []string{}}); err != nil {
		t.Fatalf("ApplyRouting: %v", err)
	}
	if len(f.inputs) != 1 || f.inputs[0] != "apply-endpoints:" {
		t.Errorf("inputs = %q, want only an empty apply-endpoints", f.inputs)
	}
}

func TestApplyStrategiesInput(t *testing.T) {
	f := &fakeRunner{}
	a := &Adapter{x: f}
	ss := []engine.Strategy{{ID: "gp-1", Protocol: "tls", Domains: []string{"a.com", "b.com"}, Args: "--lua-desync=multisplit:pos=1"}}
	if err := a.ApplyStrategies(context.Background(), ss); err != nil {
		t.Fatal(err)
	}
	want := "apply-strategies:list nuxk-s1.list\na.com\nb.com\n.\n" +
		"custom --filter-tcp=443 --filter-l7=tls --hostlist=/opt/etc/nfqws2/lists/nuxk-s1.list --lua-desync=multisplit:pos=1\n"
	if len(f.inputs) != 1 || f.inputs[0] != want {
		t.Errorf("shim input:\n%q\nwant\n%q", f.inputs, want)
	}
	f.inputs = nil
	a.ApplyStrategies(context.Background(), nil)
	if f.inputs[0] != "apply-strategies:custom \n" {
		t.Errorf("clearing: %q", f.inputs[0])
	}
}
