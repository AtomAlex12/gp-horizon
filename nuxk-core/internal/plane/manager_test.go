package plane

import (
	"context"
	"testing"
	"time"
)

type slowBackend struct{ release chan struct{} }

func (s slowBackend) Name() string { return "slow" }
func (s slowBackend) Observe(ctx context.Context) (Observed, error) {
	<-s.release
	return Observed{Groups: map[string][]string{}}, nil
}
func (s slowBackend) Apply(context.Context, Op) error { return nil }

type memStore struct{}

func (memStore) LoadJSON(string, any) error { return nil }
func (memStore) SaveJSON(string, any) error { return nil }

// Status feeds /status every few seconds: it must answer while a slow
// reconcile pass (RCI can take seconds) is still running.
func TestStatusDoesNotWaitForReconcile(t *testing.T) {
	b := slowBackend{release: make(chan struct{})}
	m := NewManager(b, memStore{}, Config{})
	go m.Reconcile(context.Background())
	time.Sleep(50 * time.Millisecond)
	done := make(chan struct{})
	go func() { m.Status(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Status blocked behind Reconcile")
	}
	close(b.release)
}

type ifaceBackend struct{ ifaces map[string]bool }

func (b ifaceBackend) Name() string { return "ifaces" }
func (b ifaceBackend) Observe(context.Context) (Observed, error) {
	return Observed{Groups: map[string][]string{}, Interfaces: b.ifaces}, nil
}
func (b ifaceBackend) Apply(context.Context, Op) error { return nil }

// The firmware's interfaces, as the last pass saw them: unknown before one,
// and when the backend can't tell.
func TestInterface(t *testing.T) {
	m := NewManager(ifaceBackend{ifaces: map[string]bool{"OpkgTun0": true}}, memStore{}, Config{})
	if _, known := m.Interface("OpkgTun0"); known {
		t.Fatal("known before a pass")
	}
	m.Reconcile(context.Background())
	if ok, known := m.Interface("OpkgTun0"); !ok || !known {
		t.Fatal("OpkgTun0 not seen")
	}
	if ok, known := m.Interface("OpkgTun1"); ok || !known {
		t.Fatal("OpkgTun1 seen")
	}
	if _, known := NewManager(ifaceBackend{}, memStore{}, Config{}).Interface("OpkgTun0"); known {
		t.Fatal("a backend that can't tell")
	}
}
