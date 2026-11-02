package dns

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSmartDNSBrokenWarpUsesDirectUntilProvenRecovered(t *testing.T) {
	old := slowAnswer
	slowAnswer = 50 * time.Millisecond
	t.Cleanup(func() { slowAnswer = old })
	srv, _ := dohServer(t)
	var dead atomic.Bool
	addr := freeAddr(t)
	f := newSmartFake(t, addr, false)
	f.serve(t, addr)
	now := time.Unix(1790000000, 0)
	var warpDown atomic.Bool
	warpDown.Store(true)
	var probes atomic.Int32
	s := newService(t, srv, Options{Listen: addr, Hook: &fakeHook{}, RouterDNS: fakeProxy(t, addr, &dead), SmartDNS: f.opts, Clock: func() time.Time { return now }, Transport: func(p Path) http.RoundTripper {
		return rtFunc(func(r *http.Request) (*http.Response, error) {
			if p.Name == ViaWarp {
				probes.Add(1)
				if warpDown.Load() {
					return nil, errors.New("tunnel blackhole")
				}
			}
			return srv.Client().Transport.RoundTrip(r)
		})
	}})
	s.o.Resolvers[0].IPs = []string{"192.0.2.53"}
	ctx := context.Background()
	if _, err := s.SetSettings(ctx, Settings{Enabled: true, Engine: EngineSmartDNS}); err != nil {
		t.Fatal(err)
	}
	// Interface remains connected, but DNS through it stops responding promptly.
	f.slow.Store(true)
	s.tick(ctx)
	if strings.Contains(f.conf(), "-interface") {
		t.Fatal("SmartDNS remains bound to broken WARP")
	}
	if s.Status().SmartDNS.Iface != "" {
		t.Fatal("status still claims a WARP binding")
	}
	configs := strings.Count(f.calls(), "set-config")
	f.slow.Store(false) // the direct path now answers normally
	for i := 0; i < 8; i++ {
		now = now.Add(30 * time.Second)
		s.tick(ctx)
	}
	if strings.Count(f.calls(), "set-config") != configs || probes.Load() != 0 {
		t.Fatal("repeated failures reconfigure or retry WARP during hold")
	}
	now = now.Add(2 * time.Minute)
	s.tick(ctx) // failed recovery extends hold
	count := probes.Load()
	warpDown.Store(false)
	now = now.Add(time.Minute)
	s.tick(ctx)
	if probes.Load() != count {
		t.Fatal("failed recovery was retried before cooldown")
	}
	now = now.Add(smartWarpHold)
	s.tick(ctx)
	if strings.Contains(f.conf(), "-interface") {
		t.Fatal("one successful probe restored WARP too early")
	}
	s.tick(ctx)
	if probes.Load() != count+1 {
		t.Fatal("two probes ran on the same tick time")
	}
	now = now.Add(smartWarpProbeGap)
	s.tick(ctx)
	if !strings.Contains(f.conf(), "-interface opkgtun0") {
		t.Fatal("two successful probes did not restore WARP")
	}
	if !s.Status().Attached || s.Status().Suspended {
		t.Fatal("recovered DNS was not available to router")
	}
}

func TestSmartDNSBootUsesDirectDespiteConnectedWarp(t *testing.T) {
	srv, _ := dohServer(t)
	addr := freeAddr(t)
	f := newSmartFake(t, addr, false)
	s := newService(t, srv, Options{Listen: addr, Hook: &fakeHook{}, SmartDNS: f.opts})
	s.o.Resolvers[0].IPs = []string{"192.0.2.53"}
	s.set.Enabled = true
	s.set.Engine = EngineSmartDNS
	s.resume()
	if strings.Contains(f.conf(), "-interface") {
		t.Fatal("startup depends on saved WARP interface")
	}
	if !s.Status().Attached {
		t.Fatal("startup no longer tracks possible saved DNS attachment")
	}
}

func TestSmartDNSLostWarpDoesNotImmediatelyRebind(t *testing.T) {
	srv, _ := dohServer(t)
	var dead atomic.Bool
	addr := freeAddr(t)
	f := newSmartFake(t, addr, false)
	f.serve(t, addr)
	iface := "opkgtun0"
	f.opts.WarpIface = func() string { return iface }
	now := time.Unix(1790000000, 0)
	s := newService(t, srv, Options{Listen: addr, Hook: &fakeHook{}, RouterDNS: fakeProxy(t, addr, &dead), SmartDNS: f.opts, Clock: func() time.Time { return now }})
	s.o.Resolvers[0].IPs = []string{"192.0.2.53"}
	if _, err := s.SetSettings(context.Background(), Settings{Enabled: true, Engine: EngineSmartDNS}); err != nil {
		t.Fatal(err)
	}
	iface = ""
	s.tick(context.Background())
	if strings.Contains(f.conf(), "-interface") {
		t.Fatal("lost WARP remains selected")
	}
	iface = "opkgtun0"
	now = now.Add(time.Minute)
	s.tick(context.Background())
	if strings.Contains(f.conf(), "-interface") {
		t.Fatal("interface reappeared and bypassed the recovery hold")
	}
}
