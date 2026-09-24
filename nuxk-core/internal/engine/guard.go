package engine

import (
	"context"
	"sync"
)

// guarded serialises every call into one engine. Init scripts are not
// re-entrant: a reconcile `info` racing a user `restart`, or two `set-config`
// calls from two browser tabs, would otherwise run the same script twice at
// once. Registry.Add wraps every adapter in one.
type guarded struct {
	inner Engine
	mu    sync.Mutex
}

func (g *guarded) Kind() Kind { return g.inner.Kind() }

func (g *guarded) Info(ctx context.Context) (Info, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inner.Info(ctx)
}

func (g *guarded) Probe(ctx context.Context) (Probe, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inner.Probe(ctx)
}

func (g *guarded) Start(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inner.Start(ctx)
}

func (g *guarded) Stop(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inner.Stop(ctx)
}

func (g *guarded) Restart(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inner.Restart(ctx)
}

func (g *guarded) ApplyRouting(ctx context.Context, r Routing) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inner.ApplyRouting(ctx, r)
}

// guardedConfigurable is a guarded engine whose adapter also implements
// Configurable — a separate type so the type assertion `e.(Configurable)`
// keeps working through the wrapper and stays false for the others.
type guardedConfigurable struct {
	*guarded
	c Configurable
}

func (g guardedConfigurable) SetConfig(ctx context.Context, cfg map[string]string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.c.SetConfig(ctx, cfg)
}

func guard(e Engine) Engine {
	g := &guarded{inner: e}
	if c, ok := e.(Configurable); ok {
		return guardedConfigurable{guarded: g, c: c}
	}
	return g
}
