package keeneticwg

import (
	"context"
	"strings"
	"testing"
)

func TestInfoAndProbeUseDifferentInterfaceNames(t *testing.T) {
	a := New("Wireguard0", "nwg0")
	a.run = func(_ context.Context, command string) (string, error) {
		if command != "show interface Wireguard0" {
			t.Fatalf("unexpected command %q", command)
		}
		return "id: Wireguard0\ntype: Wireguard\nstate: up\nonline: yes\n", nil
	}
	a.trace = func(_ context.Context, iface string) (string, error) {
		if iface != "nwg0" {
			t.Fatalf("probe bound to %q", iface)
		}
		return "warp=on\nip=192.0.2.1\n", nil
	}
	info, err := a.Info(context.Background())
	if err != nil || !info.Running || info.Iface != "nwg0" {
		t.Fatalf("info=%+v %v", info, err)
	}
	p, err := a.Probe(context.Background(), nil)
	if err != nil || !p.OK || !strings.EqualFold(p.EgressIP, "192.0.2.1") {
		t.Fatalf("probe=%+v %v", p, err)
	}
}

func TestRejectMismatchedInterface(t *testing.T) {
	a := New("Wireguard0", "nwg0")
	a.run = func(context.Context, string) (string, error) {
		return "id: Wireguard1\ntype: Wireguard\nstate: up\n", nil
	}
	if err := a.Preflight(context.Background()); err == nil {
		t.Fatal("accepted a different interface")
	}
}
