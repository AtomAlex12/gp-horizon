package main

import (
	"testing"

	"nuxk.dev/horizon/core/internal/config"
	"nuxk.dev/horizon/core/internal/core"
	"nuxk.dev/horizon/core/internal/engine"
)

// DNS goes through WARP only while its tunnel is connected: bound to a WARP
// still connecting, SmartDNS waited out every question (the router after a
// reboot: 1.8 s a name, then a storm of retries).
func TestWarpIfaceOnlyWhileConnected(t *testing.T) {
	cfg := config.Defaults()
	usque := func(running bool, state string, h engine.Health) core.Snapshot {
		return core.Snapshot{Engines: []core.EngineState{{Info: engine.Info{
			Kind: engine.KindUsque, Running: running, Health: h, Iface: "opkgtun0",
			Detail: map[string]string{"tunnel_state": state},
		}}}}
	}
	for name, c := range map[string]struct {
		snap core.Snapshot
		want string
	}{
		"connected":         {usque(true, "connected", engine.HealthOK), "opkgtun0"},
		"script can't tell": {usque(true, "unknown", engine.HealthUnknown), "opkgtun0"},
		"connecting":        {usque(true, "disconnected", engine.HealthDegraded), ""},
		"stopped":           {usque(false, "stopped", engine.HealthDown), ""},
		"not seen yet":      {core.Snapshot{}, ""},
	} {
		if got := warpIface(c.snap, cfg); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}
