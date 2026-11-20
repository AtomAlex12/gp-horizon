package keeneticwg

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"nuxk.dev/horizon/core/internal/engine"
	"nuxk.dev/horizon/core/internal/plane"
)

var validID = regexp.MustCompile("^Wireguard[0-9]{1,3}$")
var validDevice = regexp.MustCompile("^nwg[0-9]{1,3}$")

// Adapter controls a dedicated tunnel already imported in KeeneticOS.
type Adapter struct {
	id, device string
	run, trace func(context.Context, string) (string, error)
}

func New(id, device string) *Adapter {
	if !validID.MatchString(id) || !validDevice.MatchString(device) {
		return nil
	}
	return &Adapter{id: id, device: device, run: command, trace: cloudflare}
}

func (a *Adapter) Kind() engine.Kind                                  { return engine.KindUsque }
func (a *Adapter) KernelInterface() string                            { return a.device }
func (a *Adapter) NativeInterface() string                            { return a.id }
func (a *Adapter) ProfileConfigured() bool                            { return true }
func (a *Adapter) ApplyRouting(context.Context, engine.Routing) error { return nil }

func command(ctx context.Context, line string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/ndmc", "-c", line)
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "LD_LIBRARY_PATH=") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("CLI Keenetic: %w", err)
	}
	return string(out), nil
}

func cloudflare(ctx context.Context, iface string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/opt/bin/curl", "-4fsS", "--noproxy", "*", "--interface", "if!"+iface,
		"--max-time", "10", "https://www.cloudflare.com/cdn-cgi/trace").Output()
	return string(out), err
}

func (a *Adapter) Info(ctx context.Context) (engine.Info, error) {
	info := engine.Info{Kind: a.Kind(), Iface: a.device, Health: engine.HealthDown,
		Detail: map[string]string{"transport": plane.WarpAWG, "tunnel_state": "disconnected"}}
	out, err := a.run(ctx, "show interface "+a.id)
	if err != nil {
		return info, err
	}
	var id, kind, state, online string
	for _, line := range strings.Split(out, "\n") {
		key, value, _ := strings.Cut(strings.TrimSpace(line), ":")
		value = strings.TrimSpace(value)
		switch key {
		case "id":
			id = value
		case "type":
			kind = value
		case "state":
			state = value
		case "online":
			online = value
		case "remote-endpoint-address":
			info.Endpoint = value
		}
	}
	if id != a.id || kind != "Wireguard" || (state != "up" && state != "down") {
		return info, errors.New("выбранный интерфейс WireGuard не найден в KeeneticOS")
	}
	info.Running = state == "up"
	if info.Running {
		info.Health = engine.HealthDegraded
		if online == "yes" {
			info.Health = engine.HealthOK
			info.Detail["tunnel_state"] = "connected"
		}
	}
	return info, nil
}

func (a *Adapter) Preflight(ctx context.Context) error {
	_, err := a.Info(ctx)
	return err
}

func (a *Adapter) set(ctx context.Context, enabled bool) error {
	info, err := a.Info(ctx)
	if err != nil || info.Running == enabled {
		return err
	}
	action := "down"
	if enabled {
		action = "up"
	}
	if _, err := a.run(ctx, "interface "+a.id+" "+action); err != nil {
		return err
	}
	info, err = a.Info(ctx)
	if err == nil && info.Running != enabled {
		return errors.New("KeeneticOS не изменил состояние WireGuard")
	}
	return err
}

func (a *Adapter) Start(ctx context.Context) error { return a.set(ctx, true) }
func (a *Adapter) Stop(ctx context.Context) error  { return a.set(ctx, false) }
func (a *Adapter) Restart(ctx context.Context) error {
	if err := a.Stop(ctx); err != nil {
		return err
	}
	return a.Start(ctx)
}

func (a *Adapter) Probe(ctx context.Context, _ []string) (engine.Probe, error) {
	start := time.Now()
	p := engine.Probe{TS: start.Unix()}
	out, err := a.trace(ctx, a.device)
	if err != nil {
		p.Reason = "no-response"
		var exit *exec.ExitError
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) ||
			(errors.As(err, &exit) && (exit.ExitCode() == 126 || exit.ExitCode() == 127)) {
			p.Reason = "curl-unavailable"
		}
		return p, nil
	}
	p.Detail = make(map[string]string)
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "warp", "colo", "loc":
			p.Detail[k] = v
		case "ip":
			p.EgressIP = v
		}
	}
	p.RTTms = float64(time.Since(start).Milliseconds())
	p.OK = p.Detail["warp"] == "on" || p.Detail["warp"] == "plus"
	if !p.OK {
		p.Reason = "warp-off"
	}
	return p, nil
}
