package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The controller's own log for the panel's «Логи», next to the router's: the
// last entries in memory and a debug switch that turns itself off — the same
// as the agent's (nuxk-core/internal/logbuf, a module of its own: a copy).
// Each process keeps one: serve (this web/API, src "controller") and
// supervise (the plugin host, src "plugins"); serve pulls the host's entries
// in when the log is read, and «Отладка» switches all three — the router too.

const (
	maxDebug     = 4 * time.Hour
	logRingSize  = 2000
	defaultDebug = 30 // minutes
)

// LogEntry is one log record; Src says which process wrote it.
type LogEntry struct {
	Seq   uint64 `json:"seq"`
	TS    int64  `json:"ts"` // unix milliseconds
	Level string `json:"level"`
	Msg   string `json:"msg"`
	Attrs string `json:"attrs,omitempty"`
	Src   string `json:"src"` // controller | plugins
}

// LogRing holds the last entries. Safe for concurrent use.
type LogRing struct {
	mu   sync.Mutex
	buf  []LogEntry
	next int
	full bool
	seq  uint64
}

func NewLogRing(n int) *LogRing { return &LogRing{buf: make([]LogEntry, n)} }

func (r *LogRing) add(e LogEntry) {
	r.mu.Lock()
	r.seq++
	e.Seq = r.seq
	r.buf[r.next] = e
	r.next = (r.next + 1) % len(r.buf)
	if r.next == 0 {
		r.full = true
	}
	r.mu.Unlock()
}

// Since returns entries with Seq > after, oldest first, at most limit (newest).
func (r *LogRing) Since(after uint64, limit int) []LogEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	var all []LogEntry
	if r.full {
		all = append(all, r.buf[r.next:]...)
	}
	all = append(all, r.buf[:r.next]...)
	out := []LogEntry{}
	for _, e := range all {
		if e.Seq > after {
			out = append(out, e)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// logHandler writes text (docker logs) at info and up, and records into the
// ring at the debug switch's level.
type logHandler struct {
	text  slog.Handler
	ring  *LogRing
	mem   slog.Leveler
	src   string
	attrs []slog.Attr
	group string
}

func newLogHandler(w io.Writer, ring *LogRing, mem slog.Leveler, src string) *logHandler {
	return &logHandler{text: slog.NewTextHandler(w, nil), ring: ring, mem: mem, src: src}
}

func (h *logHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= slog.LevelInfo || l >= h.mem.Level()
}

func (h *logHandler) Handle(ctx context.Context, rec slog.Record) error {
	if rec.Level >= h.mem.Level() {
		var b strings.Builder
		write := func(a slog.Attr) {
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			k := a.Key
			if h.group != "" {
				k = h.group + "." + k
			}
			fmt.Fprintf(&b, "%s=%v", k, a.Value.Resolve())
		}
		for _, a := range h.attrs {
			write(a)
		}
		rec.Attrs(func(a slog.Attr) bool { write(a); return true })
		ts := rec.Time
		if ts.IsZero() {
			ts = time.Now()
		}
		h.ring.add(LogEntry{TS: ts.UnixMilli(), Level: strings.ToLower(rec.Level.String()), Msg: rec.Message, Attrs: b.String(), Src: h.src})
	}
	if rec.Level >= slog.LevelInfo {
		return h.text.Handle(ctx, rec)
	}
	return nil
}

func (h *logHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return &logHandler{text: h.text.WithAttrs(as), ring: h.ring, mem: h.mem, src: h.src,
		attrs: append(append([]slog.Attr{}, h.attrs...), as...), group: h.group}
}

func (h *logHandler) WithGroup(name string) slog.Handler {
	g := name
	if h.group != "" {
		g = h.group + "." + name
	}
	return &logHandler{text: h.text.WithGroup(name), ring: h.ring, mem: h.mem, src: h.src, attrs: h.attrs, group: g}
}

// LogDebug is the debug switch: debug entries reach the ring for a while.
type LogDebug struct {
	lv    slog.LevelVar
	mu    sync.Mutex
	until time.Time
	timer *time.Timer
}

// LogDebugState is the switch as the API shows it.
type LogDebugState struct {
	On    bool  `json:"on"`
	Until int64 `json:"until,omitempty"` // unix ms when it turns itself off
}

func (d *LogDebug) Level() slog.Level { return d.lv.Level() }

// Set turns debug on for dur (one minute to maxDebug), or off now.
func (d *LogDebug) Set(on bool, dur time.Duration) LogDebugState {
	d.mu.Lock()
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
	if on {
		dur = min(max(dur, time.Minute), maxDebug)
		d.until = time.Now().Add(dur)
		d.lv.Set(slog.LevelDebug)
		d.timer = time.AfterFunc(dur, d.expire)
	} else {
		d.until = time.Time{}
		d.lv.Set(slog.LevelInfo)
	}
	st := d.state()
	d.mu.Unlock()
	if on {
		slog.Info("debug logging on", "until", time.UnixMilli(st.Until).Format("15:04:05"))
	} else {
		slog.Info("debug logging off")
	}
	return st
}

func (d *LogDebug) expire() {
	d.mu.Lock()
	if d.until.IsZero() || time.Now().Before(d.until) {
		d.mu.Unlock() // switched again meanwhile
		return
	}
	d.until, d.timer = time.Time{}, nil
	d.lv.Set(slog.LevelInfo)
	d.mu.Unlock()
	slog.Info("debug logging off: time is up")
}

func (d *LogDebug) State() LogDebugState {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state()
}

func (d *LogDebug) state() LogDebugState {
	if d.until.IsZero() {
		return LogDebugState{}
	}
	return LogDebugState{On: true, Until: d.until.UnixMilli()}
}

// procLog is this process' log and switch (serve's or supervise's).
var (
	procRing  = NewLogRing(logRingSize)
	procDebug = &LogDebug{}
)

func setupLog(src string) {
	slog.SetDefault(slog.New(newLogHandler(os.Stderr, procRing, procDebug, src)))
}

// --- HTTP: /ctl/v1/logs, /ctl/v1/debug -----------------------------------------

// Logs is serve's view: its own ring, with the plugin host's entries pulled
// in on each read (they keep the host's time; the panel sorts by it). The
// host is serve's parent: it never restarts alone, its seq only grows.
type Logs struct {
	ring  *LogRing
	debug *LogDebug
	host  *PluginHost
	ag    *Agent

	boot int64 // this process' start: a new one numbers its log from 1 again

	mu       sync.Mutex
	hostSeen uint64 // the host's last seq pulled
}

func NewLogs(ring *LogRing, debug *LogDebug, host *PluginHost, ag *Agent) *Logs {
	return &Logs{ring: ring, debug: debug, host: host, ag: ag, boot: time.Now().UnixMilli()}
}

func (l *Logs) pull() {
	l.mu.Lock()
	defer l.mu.Unlock()
	es, err := l.host.LogsSince(l.hostSeen)
	if err != nil {
		return // no host (plain serve), or it is busy: next time
	}
	for _, e := range es {
		l.hostSeen = max(l.hostSeen, e.Seq)
		e.Src = "plugins"
		l.ring.add(e)
	}
}

// DebugAll is the switch for everything: this process, the plugin host and
// the router's agent. A part that can't be switched says why in Errors.
type DebugAll struct {
	Controller LogDebugState     `json:"controller"`
	Plugins    *LogDebugState    `json:"plugins,omitempty"`
	Agent      *LogDebugState    `json:"agent,omitempty"`
	Errors     map[string]string `json:"errors,omitempty"` // plugins | agent
}

func (l *Logs) states(ctx context.Context) DebugAll {
	st := DebugAll{Controller: l.debug.State(), Errors: map[string]string{}}
	if s, err := l.host.Debug(); err == nil {
		st.Plugins = &s
	} else if !errors.Is(err, errNoHost) {
		st.Errors["plugins"] = err.Error()
	}
	var a LogDebugState
	if err := l.agentDebug(ctx, http.MethodGet, nil, &a); err == nil {
		st.Agent = &a
	} else {
		st.Errors["agent"] = err.Error()
	}
	return st
}

func (l *Logs) set(ctx context.Context, on bool, minutes int) DebugAll {
	l.debug.Set(on, time.Duration(minutes)*time.Minute)
	st := DebugAll{Errors: map[string]string{}}
	if _, err := l.host.SetDebug(on, minutes); err != nil && !errors.Is(err, errNoHost) {
		st.Errors["plugins"] = err.Error()
	}
	in := map[string]any{"on": on, "minutes": minutes}
	if err := l.agentDebug(ctx, http.MethodPut, in, nil); err != nil {
		st.Errors["agent"] = err.Error()
	}
	all := l.states(ctx)
	for k, v := range st.Errors { // the switching error says more than a read's
		all.Errors[k] = v
	}
	return all
}

func (l *Logs) agentDebug(ctx context.Context, method string, in, out any) error {
	if base, _ := l.ag.Ref(); base == "" {
		return fmt.Errorf("роутер ещё не подключён")
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	err := l.ag.send(cctx, method, "/api/v1/logs/debug", in, out)
	var he *agentHTTPError
	if errors.As(err, &he) && he.Code == http.StatusNotFound {
		return fmt.Errorf("на роутере nuxk-core без отладки — обновите его")
	}
	return err
}

func (l *Logs) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /ctl/v1/logs", func(w http.ResponseWriter, r *http.Request) {
		l.pull()
		after, _ := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > logRingSize {
			limit = logRingSize
		}
		writeJSON(w, http.StatusOK, map[string]any{"boot": l.boot, "items": l.ring.Since(after, limit)})
	})
	mux.HandleFunc("GET /ctl/v1/debug", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, l.states(r.Context()))
	})
	mux.HandleFunc("PUT /ctl/v1/debug", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			On      bool `json:"on"`
			Minutes int  `json:"minutes"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Minutes == 0 {
			in.Minutes = defaultDebug
		}
		if in.Minutes < 1 || time.Duration(in.Minutes)*time.Minute > maxDebug {
			writeErr(w, http.StatusBadRequest, "bad_minutes", "от 1 до 240 минут")
			return
		}
		writeJSON(w, http.StatusOK, l.set(r.Context(), in.On, in.Minutes))
	})
}

// logRequests logs at debug what changes something, fails or drags — not
// the reads the panel polls every few seconds, nor the web's files.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !slog.Default().Enabled(r.Context(), slog.LevelDebug) {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rec := &codeRecorder{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rec, r)
		took := time.Since(start)
		api := strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/ctl/")
		if !api || (r.Method == http.MethodGet && rec.code < 400 && took < time.Second) {
			return
		}
		slog.Debug("http", "method", r.Method, "path", r.URL.Path, "code", rec.code, "took", took.Round(time.Millisecond))
	})
}

type codeRecorder struct {
	http.ResponseWriter
	code int
}

func (c *codeRecorder) WriteHeader(code int) { c.code = code; c.ResponseWriter.WriteHeader(code) }

// Unwrap lets http.ResponseController (the proxy's flushes for SSE) reach w.
func (c *codeRecorder) Unwrap() http.ResponseWriter { return c.ResponseWriter }
