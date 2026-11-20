package plane

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
)

type warpTestBackend struct {
	obs      Observed
	writeErr error
}

func (b *warpTestBackend) Name() string { return "test" }
func (b *warpTestBackend) Observe(context.Context) (Observed, error) {
	return b.obs, nil
}
func (b *warpTestBackend) Apply(_ context.Context, op Op) error {
	if b.writeErr != nil {
		return b.writeErr
	}
	if op.Group != warpGroup || op.Interface == AWGDevice {
		return errors.New("unexpected RCI write")
	}
	switch op.Kind {
	case OpAddRoute:
		for i, r := range b.obs.Routes {
			if r.Group == op.Group && r.Interface == op.Interface {
				b.obs.Routes[i].Auto = !op.Block
				return nil
			}
		}
		b.obs.Routes = append(b.obs.Routes, Route{Group: op.Group, Interface: op.Interface, Auto: !op.Block})
	case OpDelRoute:
		b.obs.Routes = slices.DeleteFunc(b.obs.Routes, func(r Route) bool {
			return r.Group == op.Group && r.Interface == op.Interface
		})
	}
	return nil
}

type warpTestRoutes struct{ domains []string }

func (*warpTestRoutes) Check(context.Context) error { return nil }
func (r *warpTestRoutes) Set(_ context.Context, domains []string, _ bool) error {
	r.domains = slices.Clone(domains)
	return nil
}

func TestWarpSwitchMovesOneGroupAndPreservesTransportOnListEdit(t *testing.T) {
	ctx := context.Background()
	b := &warpTestBackend{obs: Observed{
		Groups: map[string][]string{warpGroup: {"example.org"}, "nuxk-vless": {"example.net"}},
		Routes: []Route{{Group: warpGroup, Interface: "OpkgTun0", Auto: true}, {Group: "nuxk-vless", Interface: "OpkgTun1", Auto: true}},
	}}
	m := NewManager(b, &jsonStore{}, Config{Apply: true, Ifaces: map[Mode]string{ModeWarp: "OpkgTun0", ModeVless: "OpkgTun1"}})
	r := &warpTestRoutes{}
	m.AWG = r
	lists := []List{{Name: "WARP", Mode: ModeWarp, Domains: []string{"example.org"}}, {Name: "VLESS", Mode: ModeVless, Domains: []string{"example.net"}}}
	if err := m.SetDesired(Desired{Lists: lists}); err != nil {
		t.Fatal(err)
	}
	move := func(prepare, bind, _ func(context.Context) error, commit func() error) error {
		if err := prepare(ctx); err != nil {
			return err
		}
		if err := bind(ctx); err != nil {
			return err
		}
		return commit()
	}
	if err := m.SwitchWarp(ctx, WarpAWG, "opkgtun0", move); err != nil {
		t.Fatal(err)
	}
	d, err := m.Desired()
	if err != nil || d.WarpTransport != WarpAWG || !reflect.DeepEqual(d.Lists, lists) ||
		!reflect.DeepEqual(r.domains, []string{"example.org"}) {
		t.Fatalf("lists or transport changed unexpectedly: %+v %v (%v)", d, r.domains, err)
	}
	if len(b.obs.Routes) != 1 || b.obs.Routes[0].Group != "nuxk-vless" || len(b.obs.Groups) != 2 {
		t.Fatalf("unexpected firmware state after AWG: %+v", b.obs)
	}
	if err := m.SetDesired(Desired{Lists: lists, WarpTransport: WarpUSQUE}); err != nil {
		t.Fatal(err)
	}
	d, _ = m.Desired()
	if d.WarpTransport != WarpAWG {
		t.Fatal("editing lists overwrote the selected transport")
	}
	if err := m.SwitchWarp(ctx, WarpUSQUE, AWGDevice, move); err != nil {
		t.Fatal(err)
	}
	if len(r.domains) != 0 || len(b.obs.Routes) != 2 || b.obs.Routes[1].Interface != "OpkgTun0" {
		t.Fatalf("unexpected firmware state after USQUE: %+v, AWG: %+v", b.obs, r.domains)
	}
}

func TestWarpSwitchRejectsRCIWriteBeforeStoppingUSQUE(t *testing.T) {
	ctx := context.Background()
	forbidden := errors.New("RCI POST: HTTP 403")
	b := &warpTestBackend{obs: Observed{
		Routes: []Route{{Group: warpGroup, Interface: "OpkgTun0", Auto: true}},
	}, writeErr: forbidden}
	m := NewManager(b, &jsonStore{}, Config{Apply: true, Ifaces: map[Mode]string{ModeWarp: "OpkgTun0"}})
	m.AWG = &warpTestRoutes{}
	err := m.SwitchWarp(ctx, WarpAWG, "opkgtun0",
		func(prepare, _, _ func(context.Context) error, _ func() error) error { return prepare(ctx) })
	d, _ := m.Desired()
	if !errors.Is(err, forbidden) || d.WarpTransport != "" || len(b.obs.Routes) != 1 {
		t.Fatalf("403 changed routing or desired state: %v, %+v, %+v", err, d, b.obs)
	}
}

func TestWarpSwitchRefusesUnverifiedBlockPolicy(t *testing.T) {
	ctx := context.Background()
	m := NewManager(&warpTestBackend{}, &jsonStore{}, Config{Apply: true, Ifaces: map[Mode]string{ModeWarp: "OpkgTun0"}})
	m.AWG = &warpTestRoutes{}
	if err := m.SetDesired(Desired{OnDown: OnDownBlock}); err != nil {
		t.Fatal(err)
	}
	err := m.SwitchWarp(ctx, WarpAWG, "opkgtun0",
		func(_, _, _ func(context.Context) error, _ func() error) error {
			t.Fatal("the original transport should stay running")
			return nil
		})
	if !errors.Is(err, ErrAWGBlockUnsupported) {
		t.Fatalf("unexpected error: %v", err)
	}
}
