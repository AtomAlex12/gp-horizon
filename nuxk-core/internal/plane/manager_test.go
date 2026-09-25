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
