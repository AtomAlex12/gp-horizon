package plane

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestAddAutoRejectsAlreadyCoveredDomain(t *testing.T) {
	mChild := NewManager(slowBackend{}, &jsonStore{}, Config{})
	if err := mChild.SetDesired(Desired{Lists: []List{{Name: "Manual", Mode: ModeVless, Domains: []string{"api.example.com"}}}}); err != nil {
		t.Fatal(err)
	}
	if err := mChild.AddAuto(ModeWarp, "example.com"); err == nil {
		t.Error("accepted a parent that overlaps a manually routed subdomain")
	}
	for _, domain := range []string{"example.com", "api.example.com"} {
		t.Run(domain, func(t *testing.T) {
			m := NewManager(slowBackend{}, &jsonStore{}, Config{})
			before := Desired{Lists: []List{{Name: "Manual", Mode: ModeVless, Domains: []string{"example.com"}}}}
			if err := m.SetDesired(before); err != nil {
				t.Fatal(err)
			}
			if err := m.AddAuto(ModeWarp, domain); err == nil {
				t.Error("stale suggestion accepted over a manual route")
			}
			d, err := m.Desired()
			if err != nil || len(d.Lists) != 1 || d.Lists[0].Mode != ModeVless {
				t.Fatalf("manual route changed: %+v, %v", d, err)
			}
		})
	}
	m := NewManager(slowBackend{}, &jsonStore{}, Config{})
	m.st.Foreign = []Foreign{{Group: "foreign", Domains: []string{"example.com"}}}
	if err := m.AddAuto(ModeWarp, "api.example.com"); err == nil {
		t.Error("accepted a domain now covered by a foreign route")
	}
}

// The first automatic save is paused before it publishes, while another edit
// tries to commit. A late automatic write must not erase that newer edit.
type pausedAutoStore struct {
	mu      sync.Mutex
	data    []byte
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (s *pausedAutoStore) LoadJSON(_ string, v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.data) == 0 {
		return nil
	}
	return json.Unmarshal(s.data, v)
}

func (s *pausedAutoStore) SaveJSON(_ string, v any) error {
	d := v.(Desired)
	for _, l := range d.Lists {
		if l.Source == "suggest:warp" {
			s.once.Do(func() { close(s.entered); <-s.release })
			break
		}
	}
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.data = b
	s.mu.Unlock()
	return nil
}

func TestAutomaticAndManualEditsAreSerialized(t *testing.T) {
	for _, importing := range []bool{false, true} {
		name := "replace"
		if importing {
			name = "import"
		}
		t.Run(name, func(t *testing.T) {
			st := &pausedAutoStore{entered: make(chan struct{}), release: make(chan struct{})}
			m := NewManager(slowBackend{}, st, Config{})
			m.st.Foreign = []Foreign{{Group: "foreign", Description: "Manual", Domains: []string{"manual.example"}}}
			autoDone := make(chan error, 1)
			go func() { autoDone <- m.AddAuto(ModeWarp, "auto.example") }()
			<-st.entered
			manualDone := make(chan error, 1)
			go func() {
				if importing {
					_, err := m.Import([]string{"foreign"}, ModeVless)
					manualDone <- err
					return
				}
				manualDone <- m.SetDesired(Desired{Lists: []List{{Name: "Manual", Mode: ModeVless, Domains: []string{"manual.example"}}}})
			}()
			var early bool
			select {
			case err := <-manualDone:
				early = true
				if err != nil {
					t.Error(err)
				}
			case <-time.After(100 * time.Millisecond):
			}
			close(st.release)
			if err := <-autoDone; err != nil {
				t.Fatal(err)
			}
			if !early {
				if err := <-manualDone; err != nil {
					t.Fatal(err)
				}
			}
			d, err := m.Desired()
			if err != nil {
				t.Fatal(err)
			}
			var manual, auto bool
			for _, l := range d.Lists {
				manual = manual || listCovers(l.Domains, "manual.example")
				auto = auto || listCovers(l.Domains, "auto.example")
			}
			if !manual || (importing && !auto) {
				t.Errorf("concurrent edits lost data: %+v", d)
			}
		})
	}
}
