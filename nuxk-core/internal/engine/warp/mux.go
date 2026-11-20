package warp

import (
	"context"
	"errors"
	"sync"

	"nuxk.dev/horizon/core/internal/engine"
	"nuxk.dev/horizon/core/internal/plane"
)

var ErrTransport = errors.New("неизвестный транспорт WARP")
var ErrProfileBusy = errors.New("для замены профиля дождитесь конца операции и выберите USQUE")
var ErrProfileUnavailable = errors.New("импорт профиля AmneziaWG недоступен на этом узле")

type Status struct {
	Transport         string `json:"transport"`
	Available         bool   `json:"available"`
	Pending           bool   `json:"pending"`
	Error             string `json:"error,omitempty"`
	AWGInstalled      *bool  `json:"awg_installed,omitempty"`
	ProfileConfigured *bool  `json:"profile_configured,omitempty"`
}

func (m *Mux) ProfileConfigured() bool {
	adapter, ok := m.awg.(interface{ ProfileConfigured() bool })
	return ok && adapter.ProfileConfigured()
}

func (m *Mux) SaveProfile(config string) error {
	if !m.action.TryLock() {
		return ErrProfileBusy
	}
	defer m.action.Unlock()
	m.mu.RLock()
	busy := m.state.Pending || m.state.Transport == plane.WarpAWG
	m.mu.RUnlock()
	if busy {
		return ErrProfileBusy
	}
	adapter, ok := m.awg.(interface{ SaveProfile(string) error })
	if !ok {
		return ErrProfileUnavailable
	}
	return adapter.SaveProfile(config)
}

type Mux struct {
	usque, awg engine.Engine
	action     sync.Mutex
	mu         sync.RWMutex
	state      Status
}

func New(usque, awg engine.Engine, saved string) *Mux {
	if saved != plane.WarpAWG {
		saved = plane.WarpUSQUE
	}
	return &Mux{usque: usque, awg: awg, state: Status{Transport: saved}}
}

func (m *Mux) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := m.state
	s.Available = m.awg != nil
	return s
}

func (m *Mux) KernelInterface(usque string) string {
	if m.Status().Transport == plane.WarpAWG {
		return plane.AWGDevice
	}
	return usque
}

func (m *Mux) selected() engine.Engine {
	if m.Status().Transport == plane.WarpAWG {
		return m.awg
	}
	return m.usque
}

func (m *Mux) Kind() engine.Kind { return engine.KindUsque }

func (m *Mux) withSelected(fn func(engine.Engine) error) error {
	m.action.Lock()
	defer m.action.Unlock()
	e := m.selected()
	if e == nil {
		return errors.New("настройка выбранного транспорта WARP недоступна")
	}
	return fn(e)
}

func (m *Mux) Start(ctx context.Context) error {
	return m.withSelected(func(e engine.Engine) error {
		if err := m.stopInactive(ctx); err != nil {
			return err
		}
		return e.Start(ctx)
	})
}

// Recover stops a process left active by an interrupted transition or by
// Entware boot before the agent reads its own saved selection.
func (m *Mux) Recover(ctx context.Context) error {
	m.action.Lock()
	defer m.action.Unlock()
	return m.stopInactive(ctx)
}

func (m *Mux) stopInactive(ctx context.Context) error {
	inactive := m.awg
	if m.Status().Transport == plane.WarpAWG {
		inactive = m.usque
	}
	if inactive == nil {
		return nil
	}
	info, err := inactive.Info(ctx)
	if err != nil || !info.Running {
		return err
	}
	return stopAndCheck(ctx, inactive)
}

func (m *Mux) Stop(ctx context.Context) error {
	return m.withSelected(func(e engine.Engine) error { return e.Stop(ctx) })
}

func (m *Mux) Restart(ctx context.Context) error {
	return m.withSelected(func(e engine.Engine) error {
		if err := m.stopInactive(ctx); err != nil {
			return err
		}
		return e.Restart(ctx)
	})
}

func (m *Mux) ApplyRouting(ctx context.Context, r engine.Routing) error {
	return m.withSelected(func(e engine.Engine) error { return e.ApplyRouting(ctx, r) })
}

func (m *Mux) Probe(ctx context.Context, target []string) (engine.Probe, error) {
	if e := m.selected(); e != nil {
		return e.Probe(ctx, target)
	}
	return engine.Probe{}, errors.New("настройка выбранного транспорта WARP недоступна")
}

func (m *Mux) Info(ctx context.Context) (engine.Info, error) {
	if e := m.selected(); e != nil {
		return e.Info(ctx)
	}
	return engine.Info{Kind: m.Kind(), Health: engine.HealthDown}, nil
}

// Queue accepts a single transition and lets it finish even if the browser
// or the controller connection closes. Status carries the result.
func (m *Mux) Queue(ctx context.Context, p *plane.Manager, target string, onFinish func()) error {
	if target != plane.WarpUSQUE && target != plane.WarpAWG {
		return ErrTransport
	}
	m.mu.Lock()
	if m.state.Pending || p == nil || m.awg == nil {
		m.mu.Unlock()
		return errors.New("переключение WARP занято или недоступно")
	}
	if m.state.Transport == target {
		m.mu.Unlock()
		onFinish()
		return nil
	}
	m.state.Pending, m.state.Error = true, ""
	m.mu.Unlock()
	go func() {
		m.action.Lock()
		err := m.change(ctx, p, target)
		m.action.Unlock()
		m.mu.Lock()
		m.state.Pending = false
		if err != nil {
			m.state.Error = err.Error()
		}
		m.mu.Unlock()
		onFinish()
	}()
	return nil
}

func (m *Mux) change(ctx context.Context, p *plane.Manager, target string) error {
	old, next := m.selected(), m.usque
	if target == plane.WarpAWG {
		next = m.awg
	}
	if ready, ok := next.(interface{ Preflight(context.Context) error }); ok {
		if err := ready.Preflight(ctx); err != nil {
			return err
		}
	}
	info, err := old.Info(ctx)
	if err != nil {
		return err
	}
	return p.SwitchWarp(ctx, target, info.Iface, func(prepare, bind, restore func(context.Context) error, commit func() error) error {
		return switchTransport(ctx, old, next, switchHooks{prepare: prepare, bind: bind, restore: restore, commit: func() error {
			if err := commit(); err != nil {
				return err
			}
			m.mu.Lock()
			m.state.Transport = target
			m.mu.Unlock()
			return nil
		}})
	})
}
