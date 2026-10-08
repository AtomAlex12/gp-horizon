//go:build linux

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	serveUID       = 65534 // nobody: owns DATA_DIR and controller.json
	installTimeout = 20 * time.Minute
	healthTimeout  = 90 * time.Second
	stopGrace      = 10 * time.Second
	maxLogBytes    = 1 << 20
)

// Supervisor is `nuxk-controller supervise`: the container's root process.
// It listens on no network port; the web/API process asks it for plugin work
// over a unix socket only that process can open.
type Supervisor struct {
	DataDir    string
	RecipesDir string
	Socket     string
	// Setpriv drops a plugin's rights (util-linux); "" when not root (tests).
	Setpriv string
	// ServeArgs start the web/API child; nil = don't (tests).
	ServeArgs []string
	// HealthTimeout: how long a starting plugin may take to answer (90 s).
	HealthTimeout time.Duration

	root    bool
	recipes map[string]*Manifest
	mu      sync.Mutex
	procs   map[string]*pluginProc
	http    *http.Client
}

type pluginProc struct {
	m          *Manifest
	st         PluginState
	phase      string
	since      time.Time
	restarts   int
	lastErr    string
	notice     string
	upVersion  string        // the version that last answered its health check
	installing bool          // from the request until the new version is verified
	stop       chan struct{} // closed to stop the run loop
	done       chan struct{} // closed when the run loop has exited
}

func (s *Supervisor) pluginDir(name string) string { return filepath.Join(s.DataDir, "plugins", name) }

// pluginTmp is a plugin's TMPDIR. The container's /tmp is a noexec tmpfs, and
// GP runs a script it writes there (gp-root-helper's multi-domain runner):
// "Permission denied" three seconds into the run. This one is on the data
// volume, emptied at every start, and sticky and world-writable like /tmp.
func (s *Supervisor) pluginTmp(name string) string { return filepath.Join(s.pluginDir(name), "tmp") }

func freshTmp(dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o777|os.ModeSticky)
}

func runSupervisor(ctx context.Context) error {
	s := &Supervisor{
		DataDir:    env("DATA_DIR", "/var/lib/nuxk-controller"),
		RecipesDir: env("PLUGIN_RECIPES", "/usr/share/nuxk/plugins"),
		Socket:     env("SUPERVISOR_SOCK", "/run/nuxk/supervisor.sock"),
	}
	if os.Geteuid() == 0 {
		p, err := exec.LookPath("setpriv")
		if err != nil {
			return errors.New("setpriv (util-linux) is required to run plugins with reduced rights")
		}
		s.Setpriv = p
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	s.ServeArgs = []string{self, "serve"}
	return s.Run(ctx)
}

// Run blocks until ctx ends, then stops every child.
func (s *Supervisor) Run(ctx context.Context) error {
	s.root = os.Geteuid() == 0
	s.http = &http.Client{Timeout: 3 * time.Second}
	if s.HealthTimeout == 0 {
		s.HealthTimeout = healthTimeout
	}
	s.procs = map[string]*pluginProc{}
	recipes, err := LoadRecipes(s.RecipesDir)
	if err != nil {
		return fmt.Errorf("plugin recipes: %w", err)
	}
	s.recipes = recipes
	if err := s.prepareDirs(); err != nil {
		return err
	}
	ln, err := s.listen()
	if err != nil {
		return err
	}
	defer ln.Close()
	go s.serveSocket(ctx, ln)
	go s.tailPluginOutput(ctx)

	var wg sync.WaitGroup
	if s.ServeArgs != nil {
		wg.Add(1)
		go func() { defer wg.Done(); s.keepServe(ctx) }()
	}
	for name, m := range s.recipes {
		p := &pluginProc{m: m, phase: "absent"}
		s.procs[name] = p
		st, _ := s.readState(name)
		p.st = st
		if st.Version != "" {
			p.phase = "stopped"
		}
		if st.Enabled && st.Version != "" {
			s.startLocked(p)
		}
	}
	slog.Info("supervisor", "plugins", len(s.recipes), "socket", s.Socket, "root", s.root)
	<-ctx.Done()
	s.mu.Lock()
	for _, p := range s.procs {
		s.stopLocked(p)
	}
	s.mu.Unlock()
	for _, p := range s.procs {
		if p.done != nil {
			<-p.done
		}
	}
	wg.Wait()
	return nil
}

// prepareDirs: DATA_DIR stays the web process's (0711: others may pass
// through, not list); plugins/ is root's, each plugin dir 0700.
func (s *Supervisor) prepareDirs() error {
	if err := os.MkdirAll(s.DataDir, 0o711); err != nil {
		return err
	}
	if s.root {
		st, err := os.Stat(s.DataDir)
		if err != nil {
			return err
		}
		if sys, ok := st.Sys().(*syscall.Stat_t); ok && sys.Uid == 0 {
			if err := os.Chown(s.DataDir, serveUID, serveUID); err != nil {
				return err
			}
		}
		if err := os.Chmod(s.DataDir, 0o711); err != nil {
			return err
		}
	}
	root := filepath.Join(s.DataDir, "plugins")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	if s.root {
		if err := os.Chown(root, 0, 0); err != nil {
			return err
		}
	}
	// A plugin's code must stay readable after it drops root (nfqws2 does,
	// before running its Lua): the plugin dir is pass-through, versions/ and
	// the code world-readable; its data, state and logs are root's alone.
	for name := range s.recipes {
		for d, mode := range map[string]os.FileMode{"": 0o711, "versions": 0o755, "data": 0o700} {
			p := filepath.Join(s.pluginDir(name), d)
			if err := os.MkdirAll(p, mode); err != nil {
				return err
			}
			if err := os.Chmod(p, mode); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Supervisor) listen() (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(s.Socket), 0o755); err != nil {
		return nil, err
	}
	os.Remove(s.Socket)
	ln, err := net.Listen("unix", s.Socket)
	if err != nil {
		return nil, err
	}
	// only the web/API process may ask for plugin work — and a plugin (root
	// without CAP_DAC_*) can't even enter the dir to swap the socket
	if s.root {
		for _, p := range []string{filepath.Dir(s.Socket), s.Socket} {
			if err := os.Chown(p, serveUID, serveUID); err != nil {
				ln.Close()
				return nil, err
			}
		}
		if err := os.Chmod(filepath.Dir(s.Socket), 0o700); err != nil {
			ln.Close()
			return nil, err
		}
	}
	if err := os.Chmod(s.Socket, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

// keepServe runs the web/API child as nobody, restarting it if it dies.
func (s *Supervisor) keepServe(ctx context.Context) {
	delay := time.Second
	for ctx.Err() == nil {
		cmd := exec.Command(s.ServeArgs[0], s.ServeArgs[1:]...)
		cmd.Env = append(os.Environ(), "SUPERVISOR_SOCK="+s.Socket)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
		if s.root {
			cmd.SysProcAttr.Credential = &syscall.Credential{Uid: serveUID, Gid: serveUID, Groups: []uint32{}}
		}
		start := time.Now()
		if err := cmd.Start(); err != nil {
			slog.Error("serve: start", "err", err)
		} else {
			exited := make(chan error, 1)
			go func() { exited <- cmd.Wait() }()
			select {
			case <-ctx.Done():
				syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
				select {
				case <-exited:
				case <-time.After(stopGrace):
					syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
					<-exited
				}
				return
			case err := <-exited:
				slog.Error("serve exited", "err", err)
			}
		}
		if time.Since(start) > time.Minute {
			delay = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, 30*time.Second)
	}
}

// --- plugin processes -------------------------------------------------------

func (s *Supervisor) setPhase(p *pluginProc, phase, lastErr string) {
	p.phase, p.since = phase, time.Now()
	if lastErr != "" || phase == "running" {
		p.lastErr = lastErr
	}
}

// startLocked starts p's run loop (s.mu held).
func (s *Supervisor) startLocked(p *pluginProc) {
	if p.stop != nil {
		return // already running
	}
	p.stop, p.done = make(chan struct{}), make(chan struct{})
	go s.runLoop(p, p.stop, p.done)
}

// stopLocked asks p's run loop to stop (s.mu held); wait on p.done unlocked.
func (s *Supervisor) stopLocked(p *pluginProc) {
	if p.stop == nil {
		return
	}
	close(p.stop)
	p.stop = nil
}

func (s *Supervisor) stopAndWait(p *pluginProc) {
	s.mu.Lock()
	done := p.done
	s.stopLocked(p)
	s.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (s *Supervisor) runLoop(p *pluginProc, stop, done chan struct{}) {
	defer close(done)
	backoff := 5 * time.Second
	for {
		s.mu.Lock()
		s.setPhase(p, "starting", "")
		s.mu.Unlock()
		started := time.Now()
		err := s.runOnce(p, stop)
		select {
		case <-stop:
			s.mu.Lock()
			s.setPhase(p, "stopped", "")
			s.mu.Unlock()
			return
		default:
		}
		s.mu.Lock()
		p.restarts++
		s.setPhase(p, "failed", fmt.Sprintf("процесс завершился: %v", err))
		s.mu.Unlock()
		slog.Warn("plugin exited", "plugin", p.m.Name, "err", err)
		if time.Since(started) > 10*time.Minute {
			backoff = 5 * time.Second
		}
		select {
		case <-stop:
			s.mu.Lock()
			s.setPhase(p, "stopped", "")
			s.mu.Unlock()
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 5*time.Minute)
	}
}

// runOnce starts the plugin and waits for it to exit or for stop.
func (s *Supervisor) runOnce(p *pluginProc, stop chan struct{}) error {
	dir, err := filepath.EvalSymlinks(filepath.Join(s.pluginDir(p.m.Name), "current"))
	if err != nil {
		return fmt.Errorf("нет установленной версии: %w", err)
	}
	logf, err := openLog(filepath.Join(s.pluginDir(p.m.Name), "run.log"))
	if err != nil {
		return err
	}
	defer logf.Close()
	if err := freshTmp(s.pluginTmp(p.m.Name)); err != nil {
		return err
	}
	s.mu.Lock()
	version := p.st.Version
	s.mu.Unlock()
	argv := append([]string{filepath.Join(dir, p.m.Run[0])}, p.m.Run[1:]...)
	cmd := s.command(argv, p.m.Caps)
	cmd.Dir = dir
	cmd.Env = s.pluginEnv(p.m, version, dir)
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		return err
	}
	slog.Debug("plugin started", "plugin", p.m.Name, "version", version, "pid", cmd.Process.Pid,
		"argv", strings.Join(argv, " "), "caps", strings.Join(p.m.Caps, ","), "tmpdir", s.pluginTmp(p.m.Name))
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	go s.watchHealth(p, stop)
	select {
	case err := <-exited:
		return err
	case <-stop:
		killGroup(cmd, exited)
		return nil
	}
}

// tailPluginOutput: while debug is on, what the plugins print (run.log —
// GP's own errors, a Python traceback) also goes into the host's log, a line
// each. Only what comes after the switch: the file's past stays in «Плагины».
func (s *Supervisor) tailPluginOutput(ctx context.Context) {
	seen := map[string]int64{}
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		for name := range s.recipes {
			path := filepath.Join(s.pluginDir(name), "run.log")
			st, err := os.Stat(path)
			if err != nil {
				continue
			}
			off, known := seen[path]
			if !known || st.Size() < off || procDebug.Level() > slog.LevelDebug {
				seen[path] = st.Size() // switched off, rotated or new: from here on
				continue
			}
			if st.Size() == off {
				continue
			}
			f, err := os.Open(path)
			if err != nil {
				continue
			}
			f.Seek(off, io.SeekStart)
			b, _ := io.ReadAll(io.LimitReader(f, 64<<10))
			f.Close()
			end := strings.LastIndexByte(string(b), '\n') + 1 // whole lines; a partial one waits
			if end == 0 && len(b) == 64<<10 {
				end = len(b) // one huge line: take it as is
			}
			for _, line := range strings.Split(string(b[:end]), "\n") {
				if line = strings.TrimSpace(line); line != "" {
					slog.Debug("plugin output", "plugin", name, "line", line)
				}
			}
			seen[path] = off + int64(end)
		}
	}
}

// watchHealth marks the plugin running once its health path answers.
func (s *Supervisor) watchHealth(p *pluginProc, stop chan struct{}) {
	deadline := time.Now().Add(s.HealthTimeout)
	var last string
	for time.Now().Before(deadline) {
		select {
		case <-stop:
			return
		case <-time.After(s.HealthTimeout / 45):
		}
		resp, err := s.http.Get("http://" + p.m.Listen + p.m.Health)
		if err != nil {
			last = err.Error()
		} else {
			resp.Body.Close()
			last = resp.Status
			if resp.StatusCode == http.StatusOK {
				s.mu.Lock()
				if p.phase == "starting" {
					s.setPhase(p, "running", "")
					p.upVersion = p.st.Version
					slog.Info("plugin running", "plugin", p.m.Name, "version", p.st.Version)
				}
				s.mu.Unlock()
				return
			}
		}
	}
	s.mu.Lock()
	if p.phase == "starting" {
		p.lastErr = "не отвечает на " + p.m.Health + " за " + s.HealthTimeout.String()
		slog.Debug("plugin health: no answer", "plugin", p.m.Name, "url", "http://"+p.m.Listen+p.m.Health, "last", last)
	}
	s.mu.Unlock()
}

// command wraps argv so it runs as uid 0 with only the given capabilities
// and no way to gain more; plain exec when the supervisor isn't root.
func (s *Supervisor) command(argv, caps []string) *exec.Cmd {
	var cmd *exec.Cmd
	if s.Setpriv != "" {
		bs := "-all"
		for _, c := range caps {
			bs += ",+" + strings.ToLower(c)
		}
		args := append([]string{"--no-new-privs", "--clear-groups", "--inh-caps=-all", "--bounding-set=" + bs, "--"}, argv...)
		cmd = exec.Command(s.Setpriv, args...)
	} else {
		cmd = exec.Command(argv[0], argv[1:]...)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	return cmd
}

// pluginEnv is all a plugin gets — nothing of the supervisor's own env
// (AGENT_TOKEN and friends stay out).
func (s *Supervisor) pluginEnv(m *Manifest, version, dir string) []string {
	data := filepath.Join(s.pluginDir(m.Name), "data")
	return []string{
		"PATH=/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=" + data,
		"TMPDIR=" + s.pluginTmp(m.Name),
		"LANG=C.UTF-8",
		"PLUGIN_NAME=" + m.Name,
		"PLUGIN_VERSION=" + version,
		"PLUGIN_DIR=" + dir,
		"PLUGIN_DATA=" + data,
		"PLUGIN_LISTEN=" + m.Listen,
	}
}

func killGroup(cmd *exec.Cmd, exited chan error) {
	syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	select {
	case <-exited:
	case <-time.After(stopGrace):
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-exited
	}
	// processes the plugin left behind in its group
	syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

func openLog(path string) (*os.File, error) {
	if st, err := os.Stat(path); err == nil && st.Size() > maxLogBytes {
		os.Rename(path, path+".1")
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

// --- state ------------------------------------------------------------------

func (s *Supervisor) readState(name string) (PluginState, error) {
	var st PluginState
	b, err := os.ReadFile(filepath.Join(s.pluginDir(name), "state.json"))
	if err != nil {
		return st, err
	}
	return st, json.Unmarshal(b, &st)
}

func (s *Supervisor) writeState(name string, st PluginState) error {
	b, _ := json.MarshalIndent(st, "", "  ")
	p := filepath.Join(s.pluginDir(name), "state.json")
	if err := os.WriteFile(p+".tmp", b, 0o600); err != nil {
		return err
	}
	return os.Rename(p+".tmp", p)
}

// point makes plugins/<name>/current → versions/<version>, atomically.
func (s *Supervisor) point(name, version string) error {
	link := filepath.Join(s.pluginDir(name), "current")
	tmp := link + ".tmp"
	os.Remove(tmp)
	if err := os.Symlink(filepath.Join("versions", version), tmp); err != nil {
		return err
	}
	return os.Rename(tmp, link)
}

// --- install / rollback -----------------------------------------------------

// install runs the recipe into a fresh dir, switches to it and, if the new
// version doesn't come up healthy, switches back.
func (s *Supervisor) install(p *pluginProc, version string) {
	name := p.m.Name
	fail := func(msg string) {
		s.mu.Lock()
		p.installing = false
		p.notice = msg
		if p.st.Version == "" {
			s.setPhase(p, "absent", "")
		}
		s.mu.Unlock()
		slog.Warn("plugin install failed", "plugin", name, "version", version, "err", msg)
	}
	versions := filepath.Join(s.pluginDir(name), "versions")
	tmp, err := os.MkdirTemp(versions, "."+version+".")
	if err != nil {
		fail(err.Error())
		return
	}
	defer os.RemoveAll(tmp)
	if err := os.Chmod(tmp, 0o755); err != nil {
		fail(err.Error())
		return
	}
	if err := s.runRecipe(p.m, version, tmp); err != nil {
		fail("установка не удалась: " + err.Error() + " — подробности в журнале установки")
		return
	}
	if st, err := os.Stat(filepath.Join(tmp, p.m.Run[0])); err != nil || st.Mode()&0o111 == 0 {
		fail("после установки нет исполняемого " + p.m.Run[0])
		return
	}
	final := filepath.Join(versions, version)
	s.mu.Lock()
	old := p.st
	s.mu.Unlock()
	s.stopAndWait(p)
	if old.Version == version {
		os.RemoveAll(final + ".old")
		os.Rename(final, final+".old")
		defer os.RemoveAll(final + ".old")
	}
	if err := os.Rename(tmp, final); err != nil {
		fail(err.Error())
		return
	}
	next := PluginState{Enabled: true, Version: version, Previous: old.Previous}
	if old.Version != "" && old.Version != version {
		next.Previous = old.Version
	}
	if err := s.point(name, version); err == nil {
		err = s.writeState(name, next)
	}
	if err != nil {
		fail(err.Error())
		return
	}
	s.pruneVersions(name, next)
	s.mu.Lock()
	p.st, p.restarts, p.notice, p.upVersion = next, 0, "", ""
	s.startLocked(p)
	s.mu.Unlock()
	slog.Info("plugin installed", "plugin", name, "version", version)
	defer func() {
		s.mu.Lock()
		p.installing = false
		s.mu.Unlock()
	}()
	if s.waitRunning(p, version) {
		return
	}
	if next.Previous == "" {
		s.mu.Lock()
		p.notice = "версия " + version + " установлена, но не запустилась — см. журнал работы"
		s.mu.Unlock()
		return
	}
	if err := s.rollback(p, "версия "+version+" не запустилась — вернул "+next.Previous); err != nil {
		slog.Error("plugin rollback failed", "plugin", name, "err", err)
		s.mu.Lock()
		p.notice = "версия " + version + " не запустилась, откат не удался: " + err.Error()
		s.mu.Unlock()
	}
}

// runRecipe: `sh <recipe>/<install> <version> <dest>` as root without any
// capability (it may download and write its own dir, nothing else).
func (s *Supervisor) runRecipe(m *Manifest, version, dest string) error {
	logf, err := os.OpenFile(filepath.Join(s.pluginDir(m.Name), "install.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	fmt.Fprintf(logf, "== %s %s → %s (%s)\n", m.Name, version, dest, time.Now().Format(time.RFC3339))
	ctx, cancel := context.WithTimeout(context.Background(), installTimeout)
	defer cancel()
	cmd := s.command([]string{"/bin/sh", filepath.Join(m.dir, m.Install), version, dest}, nil)
	cmd.Dir = m.dir
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=" + dest, "TMPDIR=/tmp", "LANG=C.UTF-8"}
	for k, v := range m.InstallEnv {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err := <-exited:
		return err
	case <-ctx.Done():
		killGroup(cmd, exited)
		return fmt.Errorf("дольше %s", installTimeout)
	}
}

// waitRunning: did this version answer its health check in time?
func (s *Supervisor) waitRunning(p *pluginProc, version string) bool {
	deadline := time.Now().Add(s.HealthTimeout + 5*time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		up := p.upVersion == version
		s.mu.Unlock()
		if up {
			return true
		}
		time.Sleep(s.HealthTimeout / 90)
	}
	return false
}

func (s *Supervisor) rollback(p *pluginProc, why string) error {
	s.mu.Lock()
	st := p.st
	s.mu.Unlock()
	if st.Previous == "" {
		return errors.New("нет предыдущей версии")
	}
	if _, err := os.Stat(filepath.Join(s.pluginDir(p.m.Name), "versions", st.Previous)); err != nil {
		return errors.New("предыдущая версия удалена")
	}
	s.stopAndWait(p)
	next := PluginState{Enabled: true, Version: st.Previous, Previous: st.Version}
	if err := s.point(p.m.Name, next.Version); err != nil {
		return err
	}
	if err := s.writeState(p.m.Name, next); err != nil {
		return err
	}
	s.mu.Lock()
	p.st, p.restarts = next, 0
	s.startLocked(p)
	if why != "" {
		p.notice = why
	}
	s.mu.Unlock()
	slog.Warn("plugin rolled back", "plugin", p.m.Name, "to", next.Version, "why", why)
	return nil
}

// pruneVersions keeps only the current and the previous version.
func (s *Supervisor) pruneVersions(name string, st PluginState) {
	dir := filepath.Join(s.pluginDir(name), "versions")
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if n := e.Name(); n != st.Version && n != st.Previous && !strings.HasPrefix(n, ".") {
			os.RemoveAll(filepath.Join(dir, n))
		}
	}
}

// --- socket -----------------------------------------------------------------

func (s *Supervisor) serveSocket(ctx context.Context, ln net.Listener) {
	go func() { <-ctx.Done(); ln.Close() }()
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go s.handleConn(c)
	}
}

func (s *Supervisor) handleConn(c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	var req supReq
	if err := json.NewDecoder(io.LimitReader(c, 4<<10)).Decode(&req); err != nil {
		json.NewEncoder(c).Encode(supResp{Error: "bad request"})
		return
	}
	json.NewEncoder(c).Encode(s.handle(req))
}

func (s *Supervisor) handle(req supReq) supResp {
	switch req.Op {
	case "list":
		return supResp{OK: true, Plugins: s.list()}
	case "logs":
		return supResp{OK: true, Logs: procRing.Since(req.After, logRingSize)}
	case "debug":
		if req.Set {
			procDebug.Set(req.On, time.Duration(req.Minutes)*time.Minute)
		}
		st := procDebug.State()
		return supResp{OK: true, Debug: &st}
	}
	s.mu.Lock()
	p := s.procs[req.Name]
	s.mu.Unlock()
	if p == nil {
		return supResp{Error: "нет такого плагина: " + req.Name}
	}
	switch req.Op {
	case "install":
		v := req.Version
		if v == "" {
			v = p.m.DefaultVersion
		}
		if !versionRe.MatchString(v) {
			return supResp{Error: "недопустимая версия: " + v}
		}
		s.mu.Lock()
		if p.installing {
			s.mu.Unlock()
			return supResp{Error: "установка уже идёт"}
		}
		p.installing = true
		if p.st.Version == "" {
			s.setPhase(p, "installing", "")
		}
		s.mu.Unlock()
		go s.install(p, v)
		return supResp{OK: true}
	case "enable", "disable":
		s.mu.Lock()
		if p.st.Version == "" {
			s.mu.Unlock()
			return supResp{Error: "плагин не установлен"}
		}
		p.st.Enabled = req.Op == "enable"
		st := p.st
		if st.Enabled {
			s.startLocked(p)
		}
		s.mu.Unlock()
		if !st.Enabled {
			s.stopAndWait(p)
		}
		if err := s.writeState(p.m.Name, st); err != nil {
			return supResp{Error: err.Error()}
		}
		return supResp{OK: true}
	case "restart":
		s.stopAndWait(p)
		s.mu.Lock()
		if p.st.Enabled && p.st.Version != "" {
			p.restarts = 0
			s.startLocked(p)
		}
		s.mu.Unlock()
		return supResp{OK: true}
	case "rollback":
		s.mu.Lock()
		busy := p.installing
		s.mu.Unlock()
		if busy {
			return supResp{Error: "идёт установка"}
		}
		if err := s.rollback(p, ""); err != nil {
			return supResp{Error: err.Error()}
		}
		return supResp{OK: true}
	case "log":
		file := "run.log"
		if req.Which == "install" {
			file = "install.log"
		}
		return supResp{OK: true, Log: tail(filepath.Join(s.pluginDir(p.m.Name), file), 64<<10)}
	}
	return supResp{Error: "unknown op " + strconv.Quote(req.Op)}
}

func (s *Supervisor) list() []PluginInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]PluginInfo{}
	for name, p := range s.procs {
		ph := p.phase
		if p.installing {
			ph = "installing"
		}
		info := PluginInfo{
			Name: name, Title: p.m.Title, Description: p.m.Description, Homepage: p.m.Homepage, Releases: p.m.Releases,
			API: p.m.API, Caps: append([]string{}, p.m.Caps...), Listen: p.m.Listen, DefaultVersion: p.m.DefaultVersion,
			Enabled: p.st.Enabled, Version: p.st.Version, Previous: p.st.Previous,
			Phase: ph, Restarts: p.restarts, LastError: p.lastErr, Notice: p.notice,
		}
		if !p.since.IsZero() {
			info.Since = p.since.Unix()
		}
		out[name] = info
	}
	return sortedInfos(out)
}

// tail returns up to n bytes from the end of a file, cut at a line start.
func tail(path string, n int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return ""
	}
	off := max(0, st.Size()-n)
	f.Seek(off, io.SeekStart)
	r := bufio.NewReader(f)
	if off > 0 {
		r.ReadString('\n')
	}
	b, _ := io.ReadAll(r)
	return string(b)
}
