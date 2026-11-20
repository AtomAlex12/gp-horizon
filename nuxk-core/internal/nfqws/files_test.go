package nfqws

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "lists"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lists", "user.list"), []byte("video.example\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFile(dir, "lists", "user.list")
	if err != nil || got != "video.example\n" {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, tc := range [][2]string{{"lists", "../nfqws2.conf"}, {"scripts", "user.list"}, {"config", "other.conf"}, {"other", "user.list"}} {
		if _, err := ReadFile(dir, tc[0], tc[1]); !errors.Is(err, os.ErrInvalid) {
			t.Errorf("%v: %v", tc, err)
		}
	}
}
