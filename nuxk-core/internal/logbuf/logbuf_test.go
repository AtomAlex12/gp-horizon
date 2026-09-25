package logbuf

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRingKeepsLastAndSince(t *testing.T) {
	r := NewRing(3)
	log := slog.New(NewHandler(io.Discard, r, slog.LevelInfo))
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
