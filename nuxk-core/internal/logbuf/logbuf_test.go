package logbuf

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRingKeepsLastAndSince(t *testing.T) {
	r := NewRing(3)
	log := slog.New(NewHandler(io.Discard, r, slog.LevelInfo, slog.LevelInfo))
	for i := 0; i < 5; i++ {
		log.Info("m", "i", i)
	}
	all := r.Since(0, 0)
	if len(all) != 3 || all[0].Seq != 3 || all[2].Seq != 5 || all[2].Attrs != "i=4" {
		t.Fatalf("ring = %+v", all)
	}
	if got := r.Since(4, 0); len(got) != 1 || got[0].Seq != 5 {
		t.Fatalf("since 4 = %+v", got)
	}
	log.With("engine", "usque").Warn("down")
	if e := r.Since(5, 0)[0]; e.Level != "warn" || e.Attrs != "engine=usque" {
		t.Fatalf("with attrs = %+v", e)
	}
}

func TestFileRotatesAtMax(t *testing.T) {
	p := filepath.Join(t.TempDir(), "core.log")
	f, err := OpenFile(p, 100)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 39) + "\n"
	for i := 0; i < 10; i++ {
		f.Write([]byte(line))
	}
	a, _ := os.Stat(p)
	b, _ := os.Stat(p + ".1")
	if a == nil || b == nil || a.Size() > 100 || b.Size() > 100 {
		t.Fatalf("sizes: %v %v", a, b)
	}
}

// Debug lines reach the ring only while the switch is on, and never the file
// (unless forced); the switch turns itself off.
func TestDebugSwitch(t *testing.T) {
	r := NewRing(10)
	var file strings.Builder
	d := NewDebug(false)
	log := slog.New(NewHandler(&file, r, d.FileLevel(), d))

	log.Debug("hidden")
	if st := d.State(); st.On || len(r.Since(0, 0)) != 0 {
		t.Fatalf("off: %+v %+v", st, r.Since(0, 0))
	}
	st := d.Set(true, 30*time.Minute)
	if !st.On || st.Until < time.Now().Add(29*time.Minute).UnixMilli() {
		t.Fatalf("on: %+v", st)
	}
	log.Debug("rci", "path", "show/interface")
	got := r.Since(0, 0)
	if e := got[len(got)-1]; e.Level != "debug" || e.Msg != "rci" {
		t.Fatalf("debug not in ring: %+v", got)
	}
	if strings.Contains(file.String(), "rci") {
		t.Fatalf("file: %q", file.String())
	}
	if st := d.Set(true, 10*time.Hour); st.Until > time.Now().Add(MaxDebug+time.Second).UnixMilli() {
		t.Fatalf("not capped: %+v", st)
	}

	d.Set(true, time.Minute)
	d.mu.Lock()
	d.until = time.Now().Add(-time.Second) // as if the minute had passed
	d.mu.Unlock()
	d.expire()
	log.Debug("after")
	if st := d.State(); st.On || r.Since(0, 0)[len(r.Since(0, 0))-1].Msg == "after" {
		t.Fatalf("expired: %+v", st)
	}

	f := NewDebug(true)
	if st := f.Set(false, 0); !st.On || !st.Forced || f.FileLevel() != slog.LevelDebug {
		t.Fatalf("forced: %+v", st)
	}
}
