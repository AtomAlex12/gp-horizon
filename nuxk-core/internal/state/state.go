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

	"nuxk.dev/horizon/core/internal/engine"
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

func atomicWrite(path string, b []byte) error { return atomicWriteMode(path, b, 0o644) }

// atomicWriteMode writes via a synced temp file and a rename, then syncs the
// directory: a router losing power mid-write keeps either the old or the new
// file, never an empty one.
func atomicWriteMode(path string, b []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, werr := f.Write(b)
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(tmp)
		return werr
	}
	if err := os.Chmod(tmp, mode); err != nil { // OpenFile keeps the mode of a leftover tmp
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

// Desired is what the user asked of one engine — the controller's source of
// truth, persisted as engines/<kind>.json. The engine's own init script may
// lose it (container recreated, router flashed, list file wiped); the
// controller re-applies it. Nil fields are "not managed": nuxk-core leaves
// that aspect of the engine alone.
type Desired struct {
	// Run: true = keep it running (auto-restart with backoff), false = keep it
	// stopped, nil = never touch start/stop on our own.
	Run *bool `json:"run,omitempty"`
	// Routing last applied via POST /engines/{kind}/apply.
	Routing *engine.Routing `json:"routing,omitempty"`
	// Config last set via PUT /engines/{kind}/config. May hold secrets (a
	// vless:// UUID) — the file is written 0600 and never served back.
	Config map[string]string `json:"config,omitempty"`
	// Strategies last set via PUT /engines/{kind}/strategies (nfqws2).
	Strategies []engine.Strategy `json:"strategies,omitempty"`
	// ProbeTargets: the sites the probe opens, set via PUT
	// /engines/{kind}/probe-targets (nfqws2); empty = chosen automatically.
	ProbeTargets []string `json:"probe_targets,omitempty"`
}

// LoadDesired returns engines/<kind>.json, or a zero Desired if absent.
func (s *Store) LoadDesired(kind engine.Kind) (Desired, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var d Desired
	b, err := os.ReadFile(s.desiredPath(kind))
	if os.IsNotExist(err) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	return d, json.Unmarshal(b, &d)
}

// UpdateDesired read-modify-writes engines/<kind>.json under the store lock,
// so concurrent API calls on one engine don't lose each other's fields.
func (s *Store) UpdateDesired(kind engine.Kind, fn func(*Desired)) (Desired, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var d Desired
	b, err := os.ReadFile(s.desiredPath(kind))
	switch {
	case os.IsNotExist(err):
	case err != nil:
		return d, err
	default:
		if err := json.Unmarshal(b, &d); err != nil {
			return d, err
		}
	}
	fn(&d)
	out, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return d, err
	}
	return d, atomicWriteMode(s.desiredPath(kind), out, 0o600)
}

func (s *Store) desiredPath(kind engine.Kind) string {
	return filepath.Join(s.dir, "engines", string(kind)+".json")
}

// LoadJSON reads <name>.json from the state root into v. A missing file
// leaves v untouched and is not an error.
func (s *Store) LoadJSON(name string, v any) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, err := os.ReadFile(filepath.Join(s.dir, name+".json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// SaveJSON writes v as <name>.json atomically.
func (s *Store) SaveJSON(name string, v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(s.dir, name+".json"), b)
}
