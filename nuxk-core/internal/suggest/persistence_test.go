package suggest

import (
	"context"
	"errors"
	"testing"
)

type failedSaveStore struct {
	Store
	err error
}

func (s failedSaveStore) SaveJSON(string, any) error { return s.err }

func TestSettingsWriteFailureKeepsPreviousState(t *testing.T) {
	s, st, _ := newService(t, newFake(), nil, false)
	s.bar(context.Background())
	before, reference := s.Settings(), s.Overview().Reference
	failure := errors.New("flash unavailable")
	s.Store = failedSaveStore{Store: st, err: failure}
	next := before
	next.TTFBMaxMs, next.Reference = 3000, "example.org"
	got, err := s.SetSettings(next)
	if !errors.Is(err, failure) {
		t.Errorf("saving failed, got error %v", err)
	}
	if got != before || s.Settings() != before || s.Overview().Reference != reference {
		t.Errorf("failed save changed settings/reference: returned=%+v current=%+v reference=%+v", got, s.Settings(), s.Overview().Reference)
	}
	if reloaded := New(newFake(), st, nil, nil).Settings(); reloaded != before {
		t.Errorf("restart disagrees with retained settings: %+v", reloaded)
	}
}
