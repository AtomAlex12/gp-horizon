package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type slowEngine struct {
	inFlight, maxInFlight atomic.Int32
}

func (s *slowEngine) Kind() Kind { return KindUsque }
func (s *slowEngine) Info(ctx context.Context) (Info, error) {
	n := s.inFlight.Add(1)
	defer s.inFlight.Add(-1)
	for {
		m := s.maxInFlight.Load()
		if n <= m || s.maxInFlight.CompareAndSwap(m, n) {
			break
		}
	}
	time.Sleep(5 * time.Millisecond)
	return Info{}, nil
}
func (s *slowEngine) Probe(ctx context.Context) (Probe, error)          { return Probe{}, nil }
func (s *slowEngine) Start(ctx context.Context) error                   { return nil }
func (s *slowEngine) Stop(ctx context.Context) error                    { return nil }
func (s *slowEngine) Restart(ctx context.Context) error                 { return nil }
func (s *slowEngine) ApplyRouting(ctx context.Context, r Routing) error { return nil }

type cfgEngine struct{ slowEngine }

func (c *cfgEngine) Kind() Kind                                               { return KindXray }
func (c *cfgEngine) SetConfig(ctx context.Context, m map[string]string) error { return nil }

func TestRegistrySerialisesCalls(t *testing.T) {
	s := &slowEngine{}
	r := NewRegistry()
	r.Add(s)
	e, _ := r.Get(KindUsque)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = e.Info(context.Background()) }()
	}
	wg.Wait()
	if m := s.maxInFlight.Load(); m != 1 {
		t.Errorf("max concurrent Info = %d, want 1", m)
	}
}

func TestGuardKeepsConfigurable(t *testing.T) {
	r := NewRegistry()
	r.Add(&slowEngine{})
	r.Add(&cfgEngine{})
	u, _ := r.Get(KindUsque)
	x, _ := r.Get(KindXray)
	if _, ok := u.(Configurable); ok {
		t.Error("usque must not look Configurable through the guard")
	}
	if _, ok := x.(Configurable); !ok {
		t.Error("xray must stay Configurable through the guard")
	}
}

type strategist struct{ slowEngine }

func (s *strategist) Kind() Kind                                        { return KindNfqws2 }
func (s *strategist) ApplyStrategies(context.Context, []Strategy) error { return nil }

func TestGuardKeepsStrategist(t *testing.T) {
	r := NewRegistry()
	r.Add(&strategist{})
	r.Add(&slowEngine{})
	n, _ := r.Get(KindNfqws2)
	if _, ok := n.(Strategist); !ok {
		t.Error("nfqws2 lost Strategist through the guard")
	}
	if _, ok := n.(Configurable); ok {
		t.Error("nfqws2 gained Configurable")
	}
	u, _ := r.Get(KindUsque)
	if _, ok := u.(Strategist); ok {
		t.Error("usque must not look like a Strategist")
	}
}
