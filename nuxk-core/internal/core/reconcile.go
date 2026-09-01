package core

import (
	"context"
	"log/slog"
	"time"

	"nuxk.dev/horizon/core/internal/engine"
)

// Reconciler polls the engines on a timer and publishes the result to the Hub.
// Info is cheap and runs every InfoEvery; Probe has network cost and runs every
// ProbeEvery.
type Reconciler struct {
	Reg        *engine.Registry
	Hub        *Hub
	Version    string
	InfoEvery  time.Duration
	ProbeEvery time.Duration
}

func NewReconciler(reg *engine.Registry, hub *Hub, version string) *Reconciler {
	return &Reconciler{
		Reg: reg, Hub: hub, Version: version,
		InfoEvery:  5 * time.Second,
		ProbeEvery: 60 * time.Second,
	}
}

// Run blocks until ctx is cancelled. One tick fires immediately on start.
func (rc *Reconciler) Run(ctx context.Context) {
	info := time.NewTicker(rc.InfoEvery)
	probe := time.NewTicker(rc.ProbeEvery)
	defer info.Stop()
	defer probe.Stop()

	last := map[engine.Kind]*EngineState{}
	rc.tick(ctx, last, true)

	for {
		select {
		case <-ctx.Done():
			return
		case <-info.C:
			rc.tick(ctx, last, false)
		case <-probe.C:
			rc.tick(ctx, last, true)
		}
	}
}

func (rc *Reconciler) tick(ctx context.Context, last map[engine.Kind]*EngineState, doProbe bool) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	var states []EngineState
	for _, k := range rc.Reg.Kinds() {
		e, _ := rc.Reg.Get(k)
		prev := last[k]

		st := EngineState{}
		if prev != nil {
			st = *prev // carry the last probe forward between probe ticks
		}

		if inf, err := e.Info(ctx); err != nil {
			slog.Warn("reconcile info", "engine", k, "err", err)
			st.Info = engine.Info{Kind: k, Health: engine.HealthUnknown,
				Detail: map[string]string{"error": err.Error()}}
		} else {
			st.Info = inf
		}

		if doProbe {
			if p, err := e.Probe(ctx); err != nil {
				slog.Warn("reconcile probe", "engine", k, "err", err)
			} else {
				pc := p
				st.Probe = &pc
				st.ProbeAt = time.Now().Unix()
			}
		}

		cp := st
		last[k] = &cp
		states = append(states, st)
	}

	rc.Hub.set(Snapshot{
		Version: rc.Version,
		TS:      time.Now().Unix(),
		Engines: states,
		Plane:   map[string]any{"status": "not-wired"}, // TODO MVP-2
	})
}
