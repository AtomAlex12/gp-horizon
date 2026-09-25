// Package config loads nuxk-core settings from a small key=value file.
//
// Format is deliberately shell-sourceable (KEY="value" / KEY=value, # comments)
// so the same file can be read by the init script and by ops by hand.
package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Listen   string // API listen addr, e.g. 127.0.0.1:4141
	APIToken string // bearer token for non-localhost clients; empty = localhost only
	StateDir string // flat-file state root, e.g. /opt/etc/nuxk
	WebRoot  string // static build dir for nuxk-web lite; empty = API only

	// Reconcile cadence; 0 = controller defaults (5s / 60s). A MIPS router may
	// want a slower Info poll — every tick forks one shell per engine.
	InfoEvery  time.Duration
	ProbeEvery time.Duration

	Engines EnginesConfig
	Plane   PlaneConfig
}

// PlaneConfig drives the routing plane (internal/plane). Off unless PLANE is
// set; plan-only unless PLANE_APPLY=1 — a fresh install never changes the
// router's routing by itself.
type PlaneConfig struct {
	Backend    string        // PLANE: "" / off | keenetic
	RCI        string        // PLANE_RCI: KeeneticOS RCI base URL
	Apply      bool          // PLANE_APPLY=1
	V6Deny     bool          // PLANE_V6=deny (default) | off
	IfaceWarp  string        // PLANE_IFACE_WARP, default OpkgTun0 (usque)
	IfaceVless string        // PLANE_IFACE_VLESS, default OpkgTun1 (xray)
	Every      time.Duration // PLANE_EVERY seconds, default 60
}

type EnginesConfig struct {
	// Each entry is the init-script the adapter shells to. Empty = engine
	// disabled; a path that doesn't exist = engine not installed, not wired.
	Nfqws2 string
	Usque  string
	Xray   string
}

// Defaults returns a Config with production-sane paths.
func Defaults() Config {
	return Config{
		Listen:   "127.0.0.1:4141",
		StateDir: "/opt/etc/nuxk",
		Engines: EnginesConfig{
			Nfqws2: "/opt/etc/init.d/S51nfqws2",
			Usque:  "/opt/etc/init.d/S51usque",
			Xray:   "/opt/etc/init.d/S52xray",
		},
		Plane: PlaneConfig{
			RCI: "http://127.0.0.1:79", V6Deny: true,
			IfaceWarp: "OpkgTun0", IfaceVless: "OpkgTun1",
		},
	}
}

// Load reads path over Defaults(). A missing file is not an error — defaults win.
func Load(path string) (Config, error) {
	cfg := Defaults()
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch k {
		case "LISTEN":
			cfg.Listen = v
		case "API_TOKEN":
			cfg.APIToken = v
		case "STATE_DIR":
			cfg.StateDir = v
		case "WEB_ROOT":
			cfg.WebRoot = v
		case "ENGINE_NFQWS2":
			cfg.Engines.Nfqws2 = v
		case "ENGINE_USQUE":
			cfg.Engines.Usque = v
		case "ENGINE_XRAY":
			cfg.Engines.Xray = v
		case "INFO_EVERY":
			cfg.InfoEvery = seconds(v)
		case "PROBE_EVERY":
			cfg.ProbeEvery = seconds(v)
		case "PLANE":
			cfg.Plane.Backend = v
		case "PLANE_RCI":
			cfg.Plane.RCI = v
		case "PLANE_APPLY":
			cfg.Plane.Apply = v == "1" || v == "yes" || v == "true"
		case "PLANE_V6":
			cfg.Plane.V6Deny = v != "off"
		case "PLANE_IFACE_WARP":
			cfg.Plane.IfaceWarp = v
		case "PLANE_IFACE_VLESS":
			cfg.Plane.IfaceVless = v
		case "PLANE_EVERY":
			cfg.Plane.Every = seconds(v)
		}
	}
	return cfg, sc.Err()
}

// seconds parses a positive integer number of seconds; anything else is 0
// (= use the default).
func seconds(v string) time.Duration {
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Second
}
