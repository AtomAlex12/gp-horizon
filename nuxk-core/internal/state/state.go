// Package state persists nuxk-core's model as flat files under a root dir.
//
// No SQLite: a cgo-free static binary on mips matters more than query power,
// and the data is tiny (a few engines, a few hundred decisions, some lists).
//
// Layout under StateDir (default /opt/etc/nuxk):
//
//	nuxk.conf              config (read by config.Load, not written here)
//	decisions.json         learned "domain -> mode" cache
//	engines/<kind>.json    per-engine config (vless uri, sni, strategy ref)
//	lists/<kind>.list      domain / ip / cidr, one per line ("#" comments ok)
//	lists/exclude.list
//	presets/<id>.list      synced preset bodies (itdoginfo)
package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Store struct {
	dir string
	mu  sync.RWMutex
}

func Open(dir string) (*Store, error) {
	for _, sub := range []string{"engines", "lists", "presets"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	return &Store{dir: dir}, nil
}

// Mode is what a domain resolves to.
type Mode string

const (
	ModeDirect Mode = "direct"
	ModeDesync Mode = "desync" // nfqws2, no tunnel
	ModeWarp   Mode = "warp"   // usque tunnel
	ModeVless  Mode = "vless"  // xray tunnel
)

// Source records how a Decision was reached.
type Source string

const (
	SourceManual Source = "manual" // user pinned — auto-discovery must not touch
	SourcePreset Source = "preset" // from a preset, mode still to be discovered
	SourceAuto   Source = "auto"   // ladder sweep result
)

// Decision is one entry in the learned cache.
type Decision struct {
	Domain     string  `json:"domain"`
	Mode       Mode    `json:"mode"`
	Strategy   string  `json:"strategy,omitempty"` // nfqws strategy snippet id, if desync
	RTTms      float64 `json:"rtt_ms,omitempty"`
	Source     Source  `json:"source"`
	VerifiedAt int64   `json:"verified_at"` // unix seconds; 0 = never
}

// Decisions is the whole cache, keyed by domain.
type Decisions map[string]Decision

func (s *Store) LoadDecisions() (Decisions, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d := Decisions{}
	b, err := os.ReadFile(filepath.Join(s.dir, "decisions.json"))
	if os.IsNotExist(err) {
		return d, nil
	}
	if err != nil {
		return nil, err
	}
	return d, json.Unmarshal(b, &d)
}

func (s *Store) SaveDecisions(d Decisions) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(s.dir, "decisions.json"), b)
}

// ReadList returns the non-empty, non-comment lines of lists/<name>.list.
func (s *Store) ReadList(name string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(s.dir, "lists", name+".list"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, nil
}

// WriteList replaces lists/<name>.list with the given lines.
func (s *Store) WriteList(name string, lines []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	body := strings.Join(lines, "\n")
	if body != "" {
		body += "\n"
	}
	return atomicWrite(filepath.Join(s.dir, "lists", name+".list"), []byte(body))
}

func atomicWrite(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
