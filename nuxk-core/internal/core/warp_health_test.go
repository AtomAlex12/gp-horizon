package core

import (
	"testing"
	"time"

	"nuxk.dev/horizon/core/internal/engine"
)

func TestAWGHealthUsesOnlyCurrentProbe(t *testing.T) {
	now := time.Unix(1000, 0)
	for _, tc := range []struct {
		name      string
		transport string
		age       int64
		ok        bool
		want      engine.Health
	}{
		{"success", "amneziawg", 2, true, engine.HealthOK},
		{"failure", "amneziawg", 2, false, engine.HealthDegraded},
		{"old process", "amneziawg", 25, true, engine.HealthUnknown},
		{"other transport", "usque", 2, true, engine.HealthUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := EngineState{
				Info: engine.Info{Kind: engine.KindUsque, Running: true, UptimeSec: 20, Health: engine.HealthUnknown,
					Detail: map[string]string{"transport": "amneziawg"}},
				Probe:   &engine.Probe{OK: tc.ok, Detail: map[string]string{"transport": tc.transport}},
				ProbeAt: now.Unix() - tc.age,
			}
			applyProbeHealth(&st, now)
			if st.Health != tc.want {
				t.Fatalf("health = %s, want %s", st.Health, tc.want)
			}
		})
	}
}
