// Package core holds nuxk-core's runtime: the reconcile loop and the state hub
// the API reads from. Keeps engine polling off the request path.
package core

import (
	"sync"
	"time"

	"nuxk.dev/horizon/core/internal/engine"
)

// EngineState is one engine's last known Info plus its last active Probe.
type EngineState struct {
	engine.Info
	Probe   *engine.Probe `json:"probe,omitempty"`
	ProbeAt int64         `json:"probe_at,omitempty"` // unix seconds

	// WantRun is the stored run intent (nil = unmanaged). LastError is the
	// last failed controller or user action on this engine, cleared on success.
	WantRun   *bool  `json:"want_run,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

// Snapshot is the whole platform state at a moment.
type Snapshot struct {
	Version string        `json:"version"`
	TS      int64         `json:"ts"`
	Engines []EngineState `json:"engines"`
	Plane   any           `json:"plane"`
}

// Hub stores the latest Snapshot and notifies subscribers on change.
type Hub struct {
	mu   sync.RWMutex
	snap Snapshot
	subs map[chan struct{}]struct{}
}

func NewHub(version string) *Hub {
	return &Hub{
		snap: Snapshot{Version: version, TS: time.Now().Unix(), Engines: []EngineState{}, Plane: map[string]any{"status": "not-wired"}},
		subs: map[chan struct{}]struct{}{},
	}
}

func (h *Hub) Get() Snapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.snap
}

func (h *Hub) set(s Snapshot) {
	h.mu.Lock()
	h.snap = s
	for ch := range h.subs {
		select {
		case ch <- struct{}{}:
		default: // subscriber is slow — drop the tick, it will get the next Get()
		}
	}
	h.mu.Unlock()
}

// Subscribe returns a channel that receives an empty struct on every update,
// and an unsubscribe func. For the SSE endpoint (wired later).
func (h *Hub) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		close(ch)
		h.mu.Unlock()
	}
}
