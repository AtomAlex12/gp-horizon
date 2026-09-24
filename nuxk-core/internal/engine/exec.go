package engine

import (
	"bufio"
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Exec is a shared helper for adapters that drive an Entware init script.
//
// Convention (also implemented by engines/nuxk-usque today):
//
//	<script> info   → flat "key value" lines on stdout  (parsed by ParseKV)
//	<script> probe  → flat "key value" lines
//	<script> start|stop|restart  → exit code 0 on success
//
// nfqws2-keenetic does not speak this yet — the nuxk-nfqws2 fork adds an
// `S51nfqws2 info --json` (candidate for upstream, Q6).
type Exec struct {
	Script string // e.g. /opt/etc/init.d/S51usque
}

// run executes the script and returns stdout and stderr separately. On failure
// the error carries the tail of the script's own output — "exit status 1" alone
// is useless in the dashboard.
func (e Exec) run(ctx context.Context, stdin string, args ...string) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, e.Script, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		err = &ScriptError{Script: e.Script, Args: args, Err: err, Output: tail(stderr.String()+stdout.String(), 400)}
	}
	return stdout.String(), stderr.String(), err
}

// ScriptError is a failed init-script call with the tail of what it printed.
type ScriptError struct {
	Script string
	Args   []string
	Err    error
	Output string
}

func (e *ScriptError) Error() string {
	msg := filepath.Base(e.Script) + " " + strings.Join(e.Args, " ") + ": " + e.Err.Error()
	if e.Output != "" {
		msg += ": " + e.Output
	}
	return msg
}

func (e *ScriptError) Unwrap() error { return e.Err }

// tail returns the last n bytes of s, trimmed and flattened to one line.
func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = "…" + s[len(s)-n:]
	}
	return strings.Join(strings.Fields(s), " ")
}

// Action runs start / stop / restart / reregister.
func (e Exec) Action(ctx context.Context, action string) error {
	_, _, err := e.run(ctx, "", action)
	return err
}

// ActionWithInput runs a subcommand, feeding data on stdin — for actions that
// take a payload (e.g. nfqws2's apply-desync/apply-endpoints, which read a
// newline-delimited list from stdin rather than an argv flag).
func (e Exec) ActionWithInput(ctx context.Context, action, input string) (string, error) {
	out, _, err := e.run(ctx, input, action)
	return out, err
}

// KV runs a subcommand expected to print flat "key value" lines. Only stdout
// is parsed — stderr is diagnostics (iptables/curl warnings) and must not turn
// into bogus keys.
func (e Exec) KV(ctx context.Context, sub string) (map[string]string, []string, error) {
	out, _, err := e.run(ctx, "", sub)
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
