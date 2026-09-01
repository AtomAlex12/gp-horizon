package engine

import (
	"context"
	"sort"
	"sync"
)

// Registry holds the wired engine adapters. Safe for concurrent use.
type Registry struct {
	mu sync.RWMutex
	m  map[Kind]Engine
}

func NewRegistry() *Registry { return &Registry{m: map[Kind]Engine{}} }

func (r *Registry) Add(e Engine) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[e.Kind()] = e
}

func (r *Registry) Get(k Kind) (Engine, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.m[k]
	return e, ok
}

// Kinds returns the registered engine kinds, sorted, for stable API output.
func (r *Registry) Kinds() []Kind {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ks := make([]Kind, 0, len(r.m))
	for k := range r.m {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool { return ks[i] < ks[j] })
	return ks
}

// Snapshot gathers Info from every engine. Errors are folded into the Info as
// Health=unknown so one broken adapter does not fail the whole call.
func (r *Registry) Snapshot(ctx context.Context) []Info {
	out := make([]Info, 0)
	for _, k := range r.Kinds() {
		e, _ := r.Get(k)
		info, err := e.Info(ctx)
		if err != nil {
			info = Info{Kind: k, Health: HealthUnknown, Detail: map[string]string{"error": err.Error()}}
		}
		out = append(out, info)
	}
	return out
}
