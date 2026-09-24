package engine

import (
	"sort"
	"sync"
)

// Registry holds the wired engine adapters. Safe for concurrent use.
type Registry struct {
	mu sync.RWMutex
	m  map[Kind]Engine
}

func NewRegistry() *Registry { return &Registry{m: map[Kind]Engine{}} }

// Add registers an adapter. Every call into it is serialised (see guarded).
func (r *Registry) Add(e Engine) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[e.Kind()] = guard(e)
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
