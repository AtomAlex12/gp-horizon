package api

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"nuxk.dev/horizon/core/internal/plane"
	"nuxk.dev/horizon/core/internal/state"
	"nuxk.dev/horizon/core/internal/suggest"
)

type failedSuggestionStore struct{ suggest.Store }

func (failedSuggestionStore) SaveJSON(string, any) error { return errors.New("flash unavailable") }

func TestSuggestSettingsPersistenceErrorIsNotSuccess(t *testing.T) {
	st, _ := state.Open(t.TempDir())
	sg := suggest.New(nil, failedSuggestionStore{st}, nil, nil)
	h := NewRouter(Deps{Suggest: sg})
	w := do(h, "PUT", "/api/v1/suggest/settings", `{"ttfb_max_ms":3000}`, "127.0.0.1:1", "")
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "state_error") {
		t.Errorf("unsaved settings -> %d %s", w.Code, w.Body)
	}
	if sg.Settings().TTFBMaxMs != 5000 {
		t.Error("failed API update changed active settings")
	}
}

func TestSuggestAcceptRejectsStaleManualRoute(t *testing.T) {
	st, _ := state.Open(t.TempDir())
	if err := st.SaveJSON("suggest", map[string]any{
		"settings": suggest.DefaultSettings(),
		"items":    []suggest.Item{{Domain: "api.example.com", State: suggest.StateReady, Mode: "warp", CheckedAt: time.Now().Unix()}},
	}); err != nil {
		t.Fatal(err)
	}
	pm := plane.NewManager(&stubPlane{groups: map[string][]string{}}, st, plane.Config{})
	sg := suggest.New(nil, st, pm, nil)
	if err := pm.SetDesired(plane.Desired{Lists: []plane.List{{Name: "Manual", Mode: plane.ModeVless, Domains: []string{"example.com"}}}}); err != nil {
		t.Fatal(err)
	}
	h := NewRouter(Deps{Suggest: sg, Plane: pm})
	w := do(h, "POST", "/api/v1/suggest/api.example.com/accept", `{}`, "127.0.0.1:1", "")
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "domain_covered") {
		t.Errorf("stale acceptance -> %d %s", w.Code, w.Body)
	}
	d, _ := pm.Desired()
	if len(d.Lists) != 1 {
		t.Errorf("stale acceptance added a conflicting route: %+v", d.Lists)
	}
}
