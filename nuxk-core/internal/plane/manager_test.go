package plane

import (
	"context"
	"encoding/json"
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

// jsonStore keeps what SaveJSON got, so a later LoadJSON sees it.
type jsonStore struct{ m map[string][]byte }

func (s *jsonStore) LoadJSON(n string, v any) error {
	if b, ok := s.m[n]; ok {
		return json.Unmarshal(b, v)
	}
	return nil
}
func (s *jsonStore) SaveJSON(n string, v any) error {
	b, err := json.Marshal(v)
	if s.m == nil {
		s.m = map[string][]byte{}
	}
	s.m[n] = b
	return err
}

func TestCoveredAndAddAuto(t *testing.T) {
	m := NewManager(slowBackend{}, &jsonStore{}, Config{})
	if err := m.SetDesired(Desired{Lists: []List{{Name: "Мой", Mode: ModeVless, Domains: []string{"openai.com"}}}}); err != nil {
		t.Fatal(err)
	}
	for d, want := range map[string]bool{"openai.com": true, "api.openai.com": true, "chatgpt.com": false, "notopenai.com": false} {
		if got := m.Covered(d) != ""; got != want {
			t.Errorf("Covered(%s) = %v, want %v", d, got, want)
		}
	}
	if err := m.AddAuto(ModeVless, "chatgpt.com"); err != nil {
		t.Fatal(err)
	}
	if err := m.AddAuto(ModeVless, "claude.ai"); err != nil {
		t.Fatal(err)
	}
	d, _ := m.Desired()
	var auto *List
	for i := range d.Lists {
		if d.Lists[i].Source == "suggest:vless" {
			auto = &d.Lists[i]
		}
	}
	if auto == nil || len(auto.Domains) != 2 || len(d.Lists) != 2 {
		t.Fatalf("one «Подбор» list with both domains expected: %+v", d.Lists)
	}
	if m.Covered("chatgpt.com") == "" {
		t.Error("an accepted domain must count as covered")
	}
	if err := m.AddAuto("tor", "x.com"); err == nil {
		t.Error("bad mode must be refused")
	}
}

// A tunnel wins over DPI: a domain routed into a tunnel no longer meets nfqws2.
func TestModeOf(t *testing.T) {
	m := NewManager(slowBackend{}, &jsonStore{}, Config{})
	if err := m.SetDesired(Desired{Lists: []List{
		{Name: "DPI", Mode: ModeDesync, Domains: []string{"youtube.com", "x.com"}},
		{Name: "WARP", Mode: ModeWarp, Domains: []string{"x.com"}},
	}}); err != nil {
		t.Fatal(err)
	}
	for d, want := range map[string]Mode{"m.youtube.com": ModeDesync, "x.com": ModeWarp, "chatgpt.com": ""} {
		if got := m.ModeOf(d); got != want {
			t.Errorf("ModeOf(%s) = %q, want %q", d, got, want)
		}
	}
}
