package main

import (
	"embed"
	"io/fs"
	"os"
	"path"
	"strings"
)

// The build (`make installer`) copies the router files into payload/ before
// compiling, so one installer binary carries everything it installs.
//
//go:embed all:payload
var embedded embed.FS

// Payload is the set of files pushed to the router.
type Payload struct{ fs fs.FS }

// NewPayload uses dir when given (development), else the embedded copy.
func NewPayload(dir string) (*Payload, error) {
	if dir != "" {
		if _, err := os.Stat(dir); err != nil {
			return nil, err
		}
		return &Payload{fs: os.DirFS(dir)}, nil
	}
	sub, err := fs.Sub(embedded, "payload")
	if err != nil {
		return nil, err
	}
	return &Payload{fs: sub}, nil
}

func (p *Payload) Version() string {
	b, err := fs.ReadFile(p.fs, "VERSION")
	if err != nil {
		return "dev"
	}
	return strings.TrimSpace(string(b))
}

func (p *Payload) HasCore(arch string) bool {
	_, err := fs.Stat(p.fs, "nuxk-core-"+arch)
	return err == nil
}

func (p *Payload) Read(name string) ([]byte, error) { return fs.ReadFile(p.fs, name) }

// WebFiles lists the web build as router-relative paths under web/.
func (p *Payload) WebFiles() ([]string, error) {
	var out []string
	err := fs.WalkDir(p.fs, "web", func(pth string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			out = append(out, strings.TrimPrefix(pth, "web/"))
		}
		return nil
	})
	return out, err
}

// UsqueIPK is the usque-keenetic package for a router arch ("" = none in
// this build: the package is optional, WARP is then skipped).
func (p *Payload) UsqueIPK(arch string) string {
	name := "usque-keenetic-" + arch + ".ipk"
	if _, err := fs.Stat(p.fs, name); err != nil {
		return ""
	}
	return name
}

// Missing returns required payload files that aren't there.
func (p *Payload) Missing() []string {
	var miss []string
	for _, f := range []string{"VERSION", "S99nuxk-core", "S51nfqws2-nuxk", path.Join("web", "index.html")} {
		if _, err := fs.Stat(p.fs, f); err != nil {
			miss = append(miss, f)
		}
	}
	return miss
}
