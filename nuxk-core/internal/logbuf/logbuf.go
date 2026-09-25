// Package logbuf keeps nuxk-core's log: the last entries in memory (for
// GET /api/v1/logs and the UI) and, optionally, a size-capped file on the
// router's flash (rotated in-process, so it can't grow while running).
package logbuf

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

// Entry is one log record.
type Entry struct {
	Seq   uint64 `json:"seq"`
	TS    int64  `json:"ts"` // unix milliseconds
	Level string `json:"level"`
	Msg   string `json:"msg"`
	Attrs string `json:"attrs,omitempty"` // key=value … as the text log shows them
}

// Ring holds the last Cap entries. Safe for concurrent use.
type Ring struct {
	mu   sync.Mutex
	buf  []Entry
	next int
	full bool
	seq  uint64
	subs map[chan Entry]struct{}
}

func NewRing(capacity int) *Ring {
	return &Ring{buf: make([]Entry, capacity), subs: map[chan Entry]struct{}{}}
}

func (r *Ring) add(e Entry) {
	r.mu.Lock()
	r.seq++
	e.Seq = r.seq
	r.buf[r.next] = e
	r.next = (r.next + 1) % len(r.buf)
	if r.next == 0 {
		r.full = true
	}
	for ch := range r.subs {
		select {
		case ch <- e:
		default: // slow reader: it can catch up through Since
		}
	}
	r.mu.Unlock()
}

// Since returns entries with Seq > after, oldest first, at most limit.
func (r *Ring) Since(after uint64, limit int) []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	var all []Entry
	if r.full {
		all = append(all, r.buf[r.next:]...)
	}
	all = append(all, r.buf[:r.next]...)
	out := []Entry{}
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

// Subscribe streams new entries until the returned func is called.
func (r *Ring) Subscribe() (<-chan Entry, func()) {
	ch := make(chan Entry, 64)
	r.mu.Lock()
	r.subs[ch] = struct{}{}
	r.mu.Unlock()
	return ch, func() {
		r.mu.Lock()
		delete(r.subs, ch)
		r.mu.Unlock()
	}
}

// Handler is a slog.Handler that writes text to w and records into a Ring.
type Handler struct {
	text  slog.Handler
	ring  *Ring
	attrs []slog.Attr
	group string
}

func NewHandler(w io.Writer, ring *Ring, level slog.Leveler) *Handler {
	return &Handler{text: slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}), ring: ring}
}

func (h *Handler) Enabled(ctx context.Context, l slog.Level) bool { return h.text.Enabled(ctx, l) }

func (h *Handler) Handle(ctx context.Context, rec slog.Record) error {
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
	h.ring.add(Entry{TS: ts.UnixMilli(), Level: strings.ToLower(rec.Level.String()), Msg: rec.Message, Attrs: b.String()})
	return h.text.Handle(ctx, rec)
}

func (h *Handler) WithAttrs(as []slog.Attr) slog.Handler {
	return &Handler{text: h.text.WithAttrs(as), ring: h.ring, attrs: append(append([]slog.Attr{}, h.attrs...), as...), group: h.group}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	g := name
	if h.group != "" {
		g = h.group + "." + name
	}
	return &Handler{text: h.text.WithGroup(name), ring: h.ring, attrs: h.attrs, group: g}
}

// File is an append-only log file that keeps at most ~Max bytes: when it
// grows past Max it is renamed to <path>.1 (replacing the old one) and a
// fresh file is started. Two files × Max bounds the flash use.
type File struct {
	Path string
	Max  int64

	mu   sync.Mutex
	f    *os.File
	size int64
}

func OpenFile(path string, max int64) (*File, error) {
	lf := &File{Path: path, Max: max}
	return lf, lf.open()
}

func (lf *File) open() error {
	f, err := os.OpenFile(lf.Path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	lf.f, lf.size = f, st.Size()
	return nil
}

func (lf *File) Write(p []byte) (int, error) {
	lf.mu.Lock()
	defer lf.mu.Unlock()
	if lf.f == nil {
		if err := lf.open(); err != nil {
			return 0, err
		}
	}
	if lf.size+int64(len(p)) > lf.Max && lf.size > 0 {
		lf.f.Close()
		os.Rename(lf.Path, lf.Path+".1")
		if err := lf.open(); err != nil {
			lf.f = nil
			return 0, err
		}
	}
	n, err := lf.f.Write(p)
	lf.size += int64(n)
	return n, err
}
