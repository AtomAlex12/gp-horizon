package engine

import (
	"bufio"
	"context"
	"os/exec"
	"strings"
	"time"
)

// Exec is a shared helper for adapters that drive an Entware init script.
//
// Convention (also implemented by engines/nuxk-usque today):
//   <script> info   → flat "key value" lines on stdout  (parsed by ParseKV)
//   <script> probe  → flat "key value" lines
//   <script> start|stop|restart  → exit code 0 on success
//
// nfqws2-keenetic does not speak this yet — the nuxk-nfqws2 fork adds an
// `S51nfqws2 info --json` (candidate for upstream, Q6).
type Exec struct {
	Script string // e.g. /opt/etc/init.d/S51usque
}

func (e Exec) run(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, e.Script, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// Action runs start / stop / restart / reregister.
func (e Exec) Action(ctx context.Context, action string) error {
	_, err := e.run(ctx, action)
	return err
}

// KV runs a subcommand expected to print flat "key value" lines.
func (e Exec) KV(ctx context.Context, sub string) (map[string]string, []string, error) {
	out, err := e.run(ctx, sub)
	if err != nil {
		return nil, nil, err
	}
	kv, list := ParseKV(out)
	return kv, list, nil
}

// ParseKV turns "key value" lines into a map. Bare "route <x>" style repeated
// keys are collected into the returned slice (used for route/prefix lists).
func ParseKV(s string) (map[string]string, []string) {
	kv := map[string]string{}
	var list []string
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r\n")
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, " ")
		if !ok {
			kv[k] = ""
			continue
		}
		if k == "route" || k == "item" {
			list = append(list, v)
			continue
		}
		kv[k] = v
	}
	return kv, list
}
