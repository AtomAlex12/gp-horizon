package plane

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

const (
	WarpUSQUE = "usque"
	WarpAWG   = "amneziawg"
	AWGDevice = "awg0"
	warpGroup = "nuxk-warp"
)

type WarpRouting interface {
	Check(context.Context) error
	Set(context.Context, []string, bool) error
}

func withAWG(want []Group, iface string) []Group {
	want = slices.Clone(want)
	for i := range want {
		if want[i].Mode == ModeWarp {
			want[i].Interface = iface
		}
	}
	return want
}

func warpNames(groups []Group, conflicts []Conflict) []string {
	blocked := map[string]bool{}
	for _, conflict := range conflicts {
		if conflict.Group == warpGroup {
			blocked[conflict.Domain] = true
		}
	}
	var names []string
	for _, group := range groups {
		if group.Mode == ModeWarp {
			for _, name := range group.Domains {
				if !blocked[name] {
					names = append(names, name)
				}
			}
		}
	}
	return names
}

type WarpTransition func(prepare, bind, restore func(context.Context) error, commit func() error) error

// SwitchWarp serializes a transport transition with edits to plane.json and
// with automatic reconciliation. Only the nuxk-warp route changes here.
func (m *Manager) SwitchWarp(ctx context.Context, target, oldDevice string, transition WarpTransition) error {
	m.edit.Lock()
	defer m.edit.Unlock()
	m.run.Lock()
	defer m.run.Unlock()
	if !m.Cfg.Apply || (m.AWG == nil && m.Cfg.WarpNativeIface == "") || m.Cfg.Ifaces[ModeWarp] == "" {
		return errors.New("переключение WARP требует активной маршрутизации Keenetic и настроенного интерфейса")
	}
	if target != WarpAWG && target != WarpUSQUE {
		return errors.New("неизвестный транспорт WARP")
	}
	d, err := m.Desired()
	if err != nil {
		return err
	}
	previous := d.WarpTransport
	if previous == "" {
		previous = WarpUSQUE
	}
	if previous == target {
		return nil
	}
	if target == WarpAWG && d.OnDown == OnDownBlock {
		return ErrAWGBlockUnsupported
	}
	if previous == WarpAWG && d.WarpNativeIface != m.Cfg.WarpNativeIface {
		return errors.New("восстановите прежний интерфейс WARP перед переключением")
	}
	want := Build(d, m.Cfg.Ifaces)
	obs, err := m.B.Observe(ctx)
	if err != nil {
		return err
	}
	if iface := m.Cfg.WarpNativeIface; iface != "" {
		if strings.EqualFold(iface, m.Cfg.Ifaces[ModeVless]) || strings.EqualFold(iface, m.Cfg.Ifaces[ModeWarp]) {
			return errors.New("для WARP нужен отдельный интерфейс WireGuard")
		}
		for _, route := range obs.Routes {
			if strings.EqualFold(route.Interface, iface) && route.Group != warpGroup {
				return errors.New("выбранный интерфейс WireGuard используется другим списком Keenetic")
			}
		}
	}
	_, conflicts := Plan(want, obs, false)
	names := warpNames(want, conflicts)
	block := d.OnDown == OnDownBlock
	set := func(ctx context.Context, transport string) error {
		if transport == WarpAWG && m.AWG != nil {
			if err := m.AWG.Set(ctx, names, block); err != nil {
				return err
			}
		}
		if err := m.setFirmwareWarp(ctx, transport, want); err != nil {
			return err
		}
		if transport == WarpUSQUE && m.AWG != nil {
			return m.AWG.Set(ctx, nil, false)
		}
		return nil
	}
	prepare := func(ctx context.Context) error {
		if check, ok := m.B.(interface{ CheckPost(context.Context) error }); ok {
			if err := check.CheckPost(ctx); err != nil {
				return err
			}
		}
		if target == WarpAWG {
			if m.AWG != nil {
				if err := m.AWG.Check(ctx); err != nil {
					return err
				}
			} else if obs.Interfaces != nil && !obs.Interfaces[m.Cfg.WarpNativeIface] {
				return errors.New("интерфейс WireGuard не найден в KeeneticOS")
			}
			legacy := m.Cfg.Ifaces[ModeWarp]
			if !strings.EqualFold(oldDevice, legacy) || strings.EqualFold(m.Cfg.Ifaces[ModeVless], legacy) {
				return errors.New("не удалось подтвердить принадлежность интерфейса действующему WARP")
			}
			for _, r := range obs.Routes {
				if r.Interface == legacy && r.Group != warpGroup {
					return errors.New("интерфейс USQUE используется другим списком Keenetic")
				}
			}
		}
		// Verify write access by applying the exact current route first.
		// A write-only 403 leaves the running engine and route in place.
		for _, r := range obs.Routes {
			if r.Group == warpGroup {
				if err := m.B.Apply(ctx, Op{Kind: OpAddRoute, Group: warpGroup, Interface: r.Interface, Block: !r.Auto}); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return transition(prepare, func(ctx context.Context) error {
		return set(ctx, target)
	}, func(ctx context.Context) error {
		return set(ctx, previous)
	}, func() error {
		d.WarpTransport = target
		d.WarpNativeIface = ""
		if target == WarpAWG {
			d.WarpNativeIface = m.Cfg.WarpNativeIface
		}
		if err := m.Store.SaveJSON(stateName, d); err != nil {
			return err
		}
		m.Kick()
		return nil
	})
}

func (m *Manager) setFirmwareWarp(ctx context.Context, transport string, want []Group) error {
	obs, err := m.B.Observe(ctx)
	if err != nil {
		return err
	}
	if transport == WarpAWG {
		iface := m.Cfg.WarpNativeIface
		if iface == "" {
			iface = AWGDevice
		}
		want = withAWG(want, iface)
	}
	ops, _ := Plan(want, obs, false)
	for _, op := range ops {
		if op.Group != warpGroup || (op.Kind == OpAddRoute && op.Interface == AWGDevice) {
			continue
		}
		if err := m.B.Apply(ctx, op); err != nil {
			return err
		}
	}
	final, err := m.B.Observe(ctx)
	if err != nil {
		return err
	}
	if transport == WarpAWG && m.Cfg.WarpNativeIface == "" {
		for _, route := range final.Routes {
			if route.Group == warpGroup {
				return fmt.Errorf("старый маршрут WARP на %s остался в RCI", route.Interface)
			}
		}
	}
	if m.Cfg.WarpNativeIface != "" {
		ops, _ := Plan(want, final, false)
		for _, op := range ops {
			if op.Group == warpGroup {
				return errors.New("KeeneticOS не подтвердил привязку списка WARP")
			}
		}
	}
	return nil
}
