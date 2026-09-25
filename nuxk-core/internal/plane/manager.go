package plane

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sort"
	"sync"
	"time"
)

// Store persists the desired plane (state/plane.json).
type Store interface {
	LoadJSON(name string, v any) error
	SaveJSON(name string, v any) error
}

// Config is the plane's part of nuxk.conf.
type Config struct {
	Ifaces map[Mode]string // mode → route target (PLANE_IFACE_WARP / _VLESS)
	Apply  bool            // PLANE_APPLY=1: change the router; otherwise plan only
	V6Deny bool            // PLANE_V6=deny (default): refuse IPv6 to nuxk groups
	Every  time.Duration   // reconcile period
	// Desync pushes the DPI hostlist to nfqws2 (nil = no nfqws2 engine). An
	// empty non-nil slice clears the list.
	Desync func(ctx context.Context, domains []string) error
}

// Status is what /api/v1/plane and the Hub show.
type Status struct {
	Backend   string          `json:"backend"`
	Apply     bool            `json:"apply"`
	Ifaces    map[Mode]string `json:"ifaces"`
	OnDown    OnDown          `json:"on_down"`
	Groups    []Group         `json:"groups"`              // wanted firmware groups
	Desync    []string        `json:"desync"`              // wanted nfqws2 hostlist
	DesyncOK  bool            `json:"desync_ok"`           // nfqws2 has exactly Desync
	DesyncOn  bool            `json:"desync_managed"`      // nuxk owns nfqws2's hostlist (see Desired.ManageDesync)
	Lists     []List          `json:"lists"`               // desired lists as stored
	Pending   []Op            `json:"pending"`             // plan not applied (plan-only mode, or failed)
	Conflicts []Conflict      `json:"conflicts,omitempty"` // held back: still in a user list
	Foreign   []Foreign       `json:"foreign,omitempty"`   // user groups with routes — importable
	Warnings  []string        `json:"warnings,omitempty"`
	LastError string          `json:"last_error,omitempty"`
	CheckedAt int64           `json:"checked_at,omitempty"`
	AppliedAt int64           `json:"applied_at,omitempty"`
}

// Foreign is a user's (non-nuxk) routed group, offered for import.
type Foreign struct {
	Group       string   `json:"group"`
	Description string   `json:"description,omitempty"`
	Interface   string   `json:"interface"`
	Domains     []string `json:"domains"`
}

// Manager keeps the firmware's nuxk-* objects equal to the desired lists.
type Manager struct {
	B     Backend
	Store Store
	Cfg   Config

	kick   chan struct{}
	run    sync.Mutex // one reconcile at a time; guards pushed
	pushed []string   // last desync list nfqws2 accepted; nil = not pushed yet
	mu     sync.Mutex // guards st only: Status never waits for a slow pass
	st     Status
}

const stateName = "plane"

func NewManager(b Backend, st Store, cfg Config) *Manager {
	if cfg.Every <= 0 {
		cfg.Every = 60 * time.Second
	}
	m := &Manager{B: b, Store: st, Cfg: cfg, kick: make(chan struct{}, 1)}
	m.st = Status{Backend: b.Name(), Apply: cfg.Apply, Ifaces: cfg.Ifaces}
	return m
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.st
}

func (m *Manager) Desired() (Desired, error) {
	var d Desired
	err := m.Store.LoadJSON(stateName, &d)
	return d, err
}

var ErrBadList = errors.New("list needs a name, mode desync|warp|vless and at least one domain")

var ErrBadOnDown = errors.New("on_down must be direct or block")

// SetDesired replaces the desired lists and reconciles right away.
func (m *Manager) SetDesired(d Desired) error {
	if d.OnDown == "" {
		d.OnDown = OnDownDirect
	}
	if !d.OnDown.Valid() {
		return ErrBadOnDown
	}
	for i, l := range d.Lists {
		l.Domains = Normalize(l.Domains)
		if l.Name == "" || !l.Mode.Valid() || len(l.Domains) == 0 {
			return ErrBadList
		}
		if l.Source == "" {
			l.Source = "manual"
		}
		d.Lists[i] = l
		if l.Mode == ModeDesync {
			d.ManageDesync = true
		}
	}
	if prev, err := m.Desired(); err == nil && prev.ManageDesync {
		d.ManageDesync = true
	}
	if err := m.Store.SaveJSON(stateName, d); err != nil {
		return err
	}
	m.Kick()
	return nil
}

// Import copies user groups into desired lists with the given mode. The user's
// groups and routes are left as they are — the domains are held back as
// conflicts until the user takes them out of the old lists.
func (m *Manager) Import(groups []string, mode Mode) (Desired, error) {
	if !mode.Valid() {
		return Desired{}, ErrBadList
	}
	st := m.Status()
	d, err := m.Desired()
	if err != nil {
		return d, err
	}
	for _, name := range groups {
		for _, f := range st.Foreign {
			if f.Group != name {
				continue
			}
			l := List{Name: firstNonEmpty(f.Description, f.Group), Mode: mode, Domains: f.Domains, Source: "imported:" + f.Group}
			replaced := false
			for i := range d.Lists {
				if d.Lists[i].Source == l.Source {
					d.Lists[i], replaced = l, true
				}
			}
			if !replaced {
				d.Lists = append(d.Lists, l)
			}
		}
	}
	return d, m.SetDesired(d)
}

func (m *Manager) Kick() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

// Run reconciles every Cfg.Every and on Kick until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	t := time.NewTicker(m.Cfg.Every)
	defer t.Stop()
	m.Reconcile(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-m.kick:
		}
		m.Reconcile(ctx)
	}
}

// Reconcile observes the firmware, plans, and — with Apply — executes the
// plan op by op, stopping at the first failure (the next pass retries).
func (m *Manager) Reconcile(ctx context.Context) {
	m.run.Lock()
	defer m.run.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	prev := m.Status()
	st := Status{Backend: m.B.Name(), Apply: m.Cfg.Apply, Ifaces: m.Cfg.Ifaces, AppliedAt: prev.AppliedAt, CheckedAt: time.Now().Unix()}
	publish := func() {
		m.mu.Lock()
		m.st = st
		m.mu.Unlock()
	}
	fail := func(err error) {
		st.LastError = err.Error()
		if err.Error() != prev.LastError { // a persistent failure is logged once
			slog.Warn("plane", "err", err)
		}
		publish()
	}

	d, err := m.Desired()
	if err != nil {
		fail(err)
		return
	}
	st.Lists, st.OnDown = d.Lists, d.OnDown
	if st.OnDown == "" {
		st.OnDown = OnDownDirect
	}
	obs, err := m.B.Observe(ctx)
	if err != nil {
		fail(err)
		return
	}
	st.DesyncOn = d.ManageDesync && m.Cfg.Desync != nil
	st.Desync = Desync(d, m.Cfg.Ifaces[ModeWarp] != "")
	st.DesyncOK = m.pushed != nil && slices.Equal(m.pushed, st.Desync)
	if st.Desync == nil {
		st.Desync = []string{}
	}
	want := Build(d, m.Cfg.Ifaces)
	if obs.Interfaces != nil {
		// a route to a missing interface fails the whole pass: skip that mode
		// (its tunnel isn't installed yet) and say so
		kept := want[:0]
		for _, g := range want {
			if obs.Interfaces[g.Interface] {
				kept = append(kept, g)
			} else {
				st.Warnings = append(st.Warnings, "интерфейс "+g.Interface+" для режима "+string(g.Mode)+" не найден на роутере — список пока не применяется")
			}
		}
		want = kept
	}
	ops, conflicts := Plan(want, obs, m.Cfg.V6Deny)
	st.Groups, st.Conflicts, st.Foreign = want, conflicts, foreign(obs)

	if !m.Cfg.Apply {
		st.Pending = pendingOnly(ops)
		publish()
		return
	}
	// DPI first: it is cheap, local and independent of the firmware objects
	if m.Cfg.Desync != nil && !st.DesyncOK && d.ManageDesync {
		if err := m.Cfg.Desync(ctx, st.Desync); err != nil {
			st.Warnings = append(st.Warnings, "список DPI не передан в nfqws2: "+err.Error())
		} else {
			m.pushed, st.DesyncOK = slices.Clone(st.Desync), true
			slog.Info("plane: desync list pushed to nfqws2", "domains", len(st.Desync))
		}
	}
	for i, op := range ops {
		if err := m.B.Apply(ctx, op); err != nil {
			st.Pending = pendingOnly(ops[i:])
			fail(err)
			return
		}
		if op.Kind != OpEnsureV6Deny {
			slog.Info("plane: applied", "op", op.Kind, "group", op.Group, "iface", op.Interface, "domains", len(op.Domains))
		}
	}
	if Changes(ops) > 0 {
		st.AppliedAt = time.Now().Unix()
	}
	publish()
}

// pendingOnly drops the idempotent v6 check from what's shown as pending.
func pendingOnly(ops []Op) []Op {
	var out []Op
	for _, o := range ops {
		if o.Kind != OpEnsureV6Deny {
			out = append(out, o)
		}
	}
	return out
}

func foreign(obs Observed) []Foreign {
	var out []Foreign
	for _, r := range obs.Routes {
		if Owned(r.Group) {
			continue
		}
		doms, ok := obs.Groups[r.Group]
		if !ok {
			continue
		}
		out = append(out, Foreign{Group: r.Group, Description: obs.Descr[r.Group], Interface: r.Interface, Domains: doms})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Group < out[j].Group })
	return out
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
