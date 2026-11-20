package nfqws

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestListFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"video.list", "user.list", "auto.list-opkg", "exclude.list", "backup.list-old", "archive.list.gz", "notes.txt", ".hidden.list"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "directory.list"), 0700); err != nil {
		t.Fatal(err)
	}
	got, err := ListFiles(dir)
	want := []ListFile{
		{Name: "user.list", Group: "standard"},
		{Name: "exclude.list", Group: "standard"},
		{Name: "video.list", Group: "additional"},
		{Name: "archive.list.gz", Group: "archive", ReadOnly: true},
		{Name: "auto.list-opkg", Group: "archive"},
		{Name: "backup.list-old", Group: "archive"},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("ListFiles = %#v, %v; want %#v", got, err, want)
	}
}

func TestListFilesMissingDirectory(t *testing.T) {
	if _, err := ListFiles(filepath.Join(t.TempDir(), "missing")); !os.IsNotExist(err) {
		t.Fatalf("expected missing directory error, got %v", err)
	}
}
