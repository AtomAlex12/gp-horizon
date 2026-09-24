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
