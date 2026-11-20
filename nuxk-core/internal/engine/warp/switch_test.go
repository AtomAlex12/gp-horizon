package warp

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"nuxk.dev/horizon/core/internal/engine"
)

type fakeEnd struct {
	kind  string
	log   *[]string
	up    bool
	bad   bool
	stuck bool
}

func (e *fakeEnd) Kind() engine.Kind { return engine.KindUsque }
func (e *fakeEnd) Info(context.Context) (engine.Info, error) {
	return engine.Info{Running: e.up}, nil
}
func (e *fakeEnd) Start(context.Context) error {
	*e.log = append(*e.log, "start:"+e.kind)
	e.up = true
	return nil
}
func (e *fakeEnd) Stop(context.Context) error {
	*e.log = append(*e.log, "stop:"+e.kind)
	if !e.stuck {
		e.up = false
	}
	return nil
}
func (e *fakeEnd) Restart(context.Context) error { return errors.New("unexpected restart") }
func (e *fakeEnd) Probe(context.Context, []string) (engine.Probe, error) {
	*e.log = append(*e.log, "probe:"+e.kind)
	return engine.Probe{OK: !e.bad}, nil
}
func (e *fakeEnd) ApplyRouting(context.Context, engine.Routing) error { return nil }

func TestSwitchCommitsAfterProbeAndBinding(t *testing.T) {
	var log []string
	old := &fakeEnd{kind: "usque", log: &log, up: true}
	next := &fakeEnd{kind: "awg", log: &log}
	err := switchTransport(context.Background(), old, next, switchHooks{
		prepare: func(context.Context) error { log = append(log, "prepare"); return nil },
		bind:    func(context.Context) error { log = append(log, "bind"); return nil },
		restore: func(context.Context) error { t.Fatal("unexpected restore"); return nil },
		commit:  func() error { log = append(log, "commit"); return nil },
	})
	want := []string{"prepare", "stop:usque", "start:awg", "probe:awg", "bind", "commit"}
	if err != nil || old.up || !next.up || !reflect.DeepEqual(log, want) {
		t.Fatalf("got %v (%v), want %v", log, err, want)
	}
}

func TestSwitchRollsBackOnFailedProbeOrCommit(t *testing.T) {
	for _, step := range []string{"probe", "commit"} {
		t.Run(step, func(t *testing.T) {
			var log []string
			old := &fakeEnd{kind: "usque", log: &log, up: true}
			next := &fakeEnd{kind: "awg", log: &log, bad: step == "probe"}
			err := switchTransport(context.Background(), old, next, switchHooks{
				prepare: func(context.Context) error { return nil },
				bind:    func(context.Context) error { return nil },
				restore: func(ctx context.Context) error {
					if next.up || !old.up || ctx.Err() != nil {
						t.Fatal("restoring routes before restoring old engine")
					}
					log = append(log, "restore")
					return nil
				},
				commit: func() error {
					if step == "probe" {
						t.Fatal("commit after failed probe")
					}
					return errors.New("read-only flash")
				},
			})
			want := []string{"stop:usque", "start:awg", "probe:awg", "stop:awg", "start:usque", "restore"}
			if err == nil || !reflect.DeepEqual(log, want) {
				t.Fatalf("got %v (%v), want %v", log, err, want)
			}
		})
	}
}

func TestSwitchLeavesOldEngineRunningOnRejectedPreflight(t *testing.T) {
	var log []string
	old := &fakeEnd{kind: "usque", log: &log, up: true}
	next := &fakeEnd{kind: "awg", log: &log}
	err := switchTransport(context.Background(), old, next, switchHooks{
		prepare: func(context.Context) error { return errors.New("RCI POST: HTTP 403") },
	})
	if err == nil || !old.up || next.up || len(log) != 0 {
		t.Fatalf("preflight rejection changed the engines: %v (%v)", log, err)
	}
}

func TestSwitchRefusesSecondEngineWhenFirstStopDidNotTakeEffect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	var log []string
	old := &fakeEnd{kind: "usque", log: &log, up: true, stuck: true}
	next := &fakeEnd{kind: "awg", log: &log}
	err := switchTransport(ctx, old, next, switchHooks{
		prepare: func(context.Context) error { return nil },
		restore: func(context.Context) error { return nil },
	})
	if err == nil || !old.up || next.up || !reflect.DeepEqual(log, []string{"stop:usque"}) {
		t.Fatalf("expected only an attempted stop: %v (%v)", log, err)
	}
}
