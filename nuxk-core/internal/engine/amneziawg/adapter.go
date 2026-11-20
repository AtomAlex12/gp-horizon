package amneziawg

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"nuxk.dev/horizon/core/internal/engine"
)

const Interface = "awg0"

var (
	ErrProfileUnavailable = errors.New("профиль AmneziaWG недоступен или имеет небезопасные права доступа")
	ErrStart              = errors.New("AmneziaWG не запустился: проверьте amneziawg-go, awg, профиль и доступность awg0")
)

type runner interface {
	ActionWithInput(context.Context, string, string) (string, error)
	KV(context.Context, string) (map[string]string, []string, error)
}

type driverExec struct{ engine.Exec }

func (x driverExec) ActionWithInput(ctx context.Context, action, input string) (string, error) {
	if input == "" {
		return x.Exec.ActionWithInput(ctx, action, "")
	}
	// awg may echo an invalid config line. Discard both streams when the
	// profile, including the private key, is given to the driver.
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, x.Script, action)
	cmd.Stdin = strings.NewReader(input)
	start := time.Now()
	err := cmd.Run()
	slog.Debug("warp driver", "script", filepath.Base(x.Script), "action", action,
		"took", time.Since(start).Round(time.Millisecond), "err", err)
	return "", err
}

type Adapter struct {
	driver runner
	config string
}

func New(driver, config string) *Adapter {
	return &Adapter{driver: driverExec{engine.Exec{Script: driver}}, config: config}
}

func (a *Adapter) Kind() engine.Kind { return engine.KindUsque }

func (a *Adapter) profile() (profile, error) {
	stat, err := os.Lstat(a.config)
	if err != nil || !stat.Mode().IsRegular() || stat.Mode().Perm()&0077 != 0 {
		return profile{}, ErrProfileUnavailable
	}
	f, err := os.Open(a.config)
	if err != nil {
		return profile{}, ErrProfileUnavailable
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(stat, opened) || opened.Mode().Perm()&0077 != 0 {
		return profile{}, ErrProfileUnavailable
	}
	return parseProfile(f)
}

func (a *Adapter) Preflight(ctx context.Context) error {
	if _, err := a.profile(); err != nil {
		return err
	}
	_, err := a.driver.ActionWithInput(ctx, "check", "")
	return err
}

func (a *Adapter) Start(ctx context.Context) error {
	p, err := a.profile()
	if err != nil {
		return err
	}
	input := fmt.Sprintf("%s %d\n%s", p.IPv4, p.MTU, p.DeviceConfig)
	if _, err := a.driver.ActionWithInput(ctx, "start", input); err != nil {
		return ErrStart
	}
	return nil
}

func (a *Adapter) Stop(ctx context.Context) error {
	_, err := a.driver.ActionWithInput(ctx, "stop", "")
	return err
}

func (a *Adapter) Restart(ctx context.Context) error {
	if err := a.Preflight(ctx); err != nil {
		return err
	}
	if err := a.Stop(ctx); err != nil {
		return err
	}
	return a.Start(ctx)
}

func (a *Adapter) Info(ctx context.Context) (engine.Info, error) {
	kv, _, err := a.driver.KV(ctx, "info")
	if err != nil {
		return engine.Info{}, err
	}
	info := engine.Info{Kind: a.Kind(), Iface: Interface, Health: engine.HealthDown}
	info.Running = kv["service.running"] == "1"
	if info.Running {
		info.Health = engine.HealthUnknown
	}
	info.PID, _ = strconv.Atoi(kv["service.pid"])
	info.Endpoint = kv["tunnel.endpoint"]
	info.Detail = map[string]string{
		"transport": "amneziawg",
		"rx_bytes":  kv["traffic.rx_bytes"],
		"tx_bytes":  kv["traffic.tx_bytes"],
	}
	return info, nil
}

func (a *Adapter) Probe(ctx context.Context, _ []string) (engine.Probe, error) {
	kv, _, err := a.driver.KV(ctx, "probe")
	if err != nil {
		return engine.Probe{}, err
	}
	p := engine.Probe{TS: time.Now().Unix(), Detail: map[string]string{
		"transport": "amneziawg", "warp": kv["warp"], "colo": kv["colo"],
	}}
	ip, ipErr := netip.ParseAddr(kv["ip"])
	p.OK = kv["ok"] == "1" && ipErr == nil && (kv["warp"] == "on" || kv["warp"] == "plus")
	if p.OK {
		p.EgressIP = ip.String()
	} else {
		p.Reason = "AmneziaWG: Cloudflare WARP не ответил через awg0"
	}
	return p, nil
}

func (a *Adapter) ApplyRouting(context.Context, engine.Routing) error { return nil }
