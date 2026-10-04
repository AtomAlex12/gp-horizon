// Package config loads nuxk-core settings from a small key=value file.
//
// Format is deliberately shell-sourceable (KEY="value" / KEY=value, # comments)
// so the same file can be read by the init script and by ops by hand.
package config

import (
	"bufio"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Listen   string // API listen addr, e.g. 127.0.0.1:4141
	APIToken string // bearer token for programs (the controller); empty = loopback only
	// Browser login: the box's own account, checked against these files
	// (shadow first). AUTH_USER=""/off turns the login off.
	AuthUser  string
	AuthFiles []string
	StateDir  string // flat-file state root, e.g. /opt/etc/nuxk
	WebRoot   string // static build dir for nuxk-web lite; empty = API only
	NodeRole  string // NODE_ROLE: router | stand | host; "" = detect (ndmc → router)

	// Reconcile cadence; 0 = controller defaults (5s / 60s). A MIPS router may
	// want a slower Info poll — every tick forks one shell per engine.
	InfoEvery  time.Duration
	ProbeEvery time.Duration

	Engines EnginesConfig
	Plane   PlaneConfig

	// Updates from the panel: releases from UpdateRepo on GitHub, installed
	// by the router's own `nuxk update` (UpdateCommand; "" = no updates from
	// the panel, only the "there's a new version" note).
	UpdateRepo    string // UPDATE_REPO, owner/name
	UpdateAPI     string // UPDATE_API, GitHub's API (a mirror); default api.github.com
	UpdateCommand string // UPDATE_COMMAND, default /opt/bin/nuxk
	UpdateBaseURL string // UPDATE_BASE_URL: a mirror of the releases' files; "" = GitHub

	// Protected DNS (internal/dns): the forwarder the router's DNS proxy asks
	// when it's on, and the DNS proxy itself (for its checks).
	DNSListen string // DNS_LISTEN; "" = DNSAddr()
	DNSRouter string // DNS_ROUTER; "" = DNSRouterAddr()
	// SmartDNS (beta) in its place: the init script `nuxk dns` puts there
	// (missing = not installed), and the RAM dir for its logs.
	SmartDNSInit string // SMARTDNS_INIT; "off" = not offered
	SmartDNSDir  string // SMARTDNS_DIR
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
		Listen:    "127.0.0.1:4141",
		StateDir:  "/opt/etc/nuxk",
		AuthUser:  "root",
		AuthFiles: []string{"/opt/etc/shadow", "/opt/etc/passwd"},
		Engines: EnginesConfig{
			Nfqws2: "/opt/etc/init.d/S51nfqws2",
			Usque:  "/opt/etc/init.d/S51usque",
			Xray:   "/opt/etc/init.d/S52xray-nuxk",
		},
		Plane: PlaneConfig{
			RCI: "http://127.0.0.1:79", V6Deny: true,
			IfaceWarp: "OpkgTun0", IfaceVless: "OpkgTun1",
		},
		UpdateCommand: "/opt/bin/nuxk",
		SmartDNSInit:  "/opt/etc/init.d/S53smartdns-nuxk",
		SmartDNSDir:   "/tmp/smartdns-nuxk",
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
		case "AUTH_USER":
			if v == "off" {
				v = ""
			}
			cfg.AuthUser = v
		case "AUTH_FILES":
			cfg.AuthFiles = strings.Fields(v)
		case "STATE_DIR":
			cfg.StateDir = v
		case "WEB_ROOT":
			cfg.WebRoot = v
		case "NODE_ROLE":
			cfg.NodeRole = v
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
		case "UPDATE_REPO":
			cfg.UpdateRepo = v
		case "UPDATE_API":
			cfg.UpdateAPI = v
		case "UPDATE_COMMAND":
			cfg.UpdateCommand = v
		case "UPDATE_BASE_URL":
			cfg.UpdateBaseURL = v
		case "DNS_LISTEN":
			cfg.DNSListen = v
		case "DNS_ROUTER":
			cfg.DNSRouter = v
		case "SMARTDNS_INIT":
			if v == "off" {
				v = ""
			}
			cfg.SmartDNSInit = v
		case "SMARTDNS_DIR":
			cfg.SmartDNSDir = v
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

// lanHost: the agent's own address in the home network (LISTEN's host), or ""
// when it listens on loopback or everywhere.
func (c Config) lanHost() string {
	host, _, err := net.SplitHostPort(c.Listen)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || ip.To4() == nil || ip.IsLoopback() || ip.IsUnspecified() {
		return ""
	}
	return host
}

// DNSAddr: where the DNS forwarder listens — DNS_LISTEN, or the router's LAN
// address on port 53053. Not loopback: KeeneticOS refuses a loopback DNS
// server ("invalid IP address: 127.0.0.1").
func (c Config) DNSAddr() string {
	if c.DNSListen != "" {
		return c.DNSListen
	}
	if h := c.lanHost(); h != "" {
		return net.JoinHostPort(h, "53053")
	}
	return "127.0.0.1:53053"
}

// DNSRouterAddr: the router's DNS proxy as devices ask it — DNS_ROUTER, or
// the LAN address on port 53.
func (c Config) DNSRouterAddr() string {
	if c.DNSRouter != "" {
		return c.DNSRouter
	}
	if h := c.lanHost(); h != "" {
		return net.JoinHostPort(h, "53")
	}
	return "127.0.0.1:53"
}
