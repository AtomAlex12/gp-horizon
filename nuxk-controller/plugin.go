package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Plugins run next to the controller in the same container, each as its own
// process with only the rights its manifest asks for:
//
//	nuxk-controller supervise   root, no network listener: starts the rest
//	├ nuxk-controller serve     the web/API, uid 65534, no capabilities —
//	│                           the only one that can read controller.json
//	└ plugins/<name>/current    uid 0 with a bounding set of just the listed
//	                            capabilities and no CAP_DAC_* / CAP_SYS_PTRACE:
//	                            root that can't read files it doesn't own
//
// A plugin ships as a recipe (/usr/share/nuxk/plugins/<name>/plugin.json +
// install.sh) inside the controller image; installing a version runs the
// recipe, which fetches the plugin's own release on the device.

// Manifest is plugins/<name>/plugin.json.
type Manifest struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Homepage    string `json:"homepage,omitempty"`
	// Releases: "github:owner/repo" — where new versions are looked up.
	Releases       string `json:"releases,omitempty"`
	DefaultVersion string `json:"default_version"`
	// Install runs `<install> <version> <dest>` from the recipe dir.
	Install    string            `json:"install"`
	InstallEnv map[string]string `json:"install_env,omitempty"`
	// Run is the command inside the installed version dir.
	Run    []string `json:"run"`
	Caps   []string `json:"caps,omitempty"`
	Listen string   `json:"listen"` // 127.0.0.1:port inside the container
	Health string   `json:"health"` // GET path answering 200 when up
	API    string   `json:"api"`    // which built-in integration the UI uses

	dir string // recipe dir
}

// capabilities a plugin may ask for — network work only; nothing that reads
// others' files (DAC_*), others' memory (SYS_PTRACE), mounts or modules
var allowedCaps = []string{"NET_ADMIN", "NET_RAW", "NET_BIND_SERVICE", "SETUID", "SETGID", "KILL"}

var (
	nameRe    = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)
	versionRe = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._+-]{0,39}$`)
)

func (m *Manifest) validate() error {
	switch {
	case !nameRe.MatchString(m.Name):
		return fmt.Errorf("plugin name %q", m.Name)
	case !versionRe.MatchString(m.DefaultVersion):
		return fmt.Errorf("default_version %q", m.DefaultVersion)
	case m.Install == "" || strings.Contains(m.Install, "/") || strings.HasPrefix(m.Install, "."):
		return fmt.Errorf("install must be a file in the recipe dir, got %q", m.Install)
	case len(m.Run) == 0 || strings.HasPrefix(m.Run[0], "/") || strings.Contains(m.Run[0], ".."):
		return fmt.Errorf("run must start with a path inside the plugin dir, got %v", m.Run)
	case !strings.HasPrefix(m.Health, "/"):
		return fmt.Errorf("health must be a path, got %q", m.Health)
	}
	host, port, err := net.SplitHostPort(m.Listen)
	if err != nil || host != "127.0.0.1" {
		return fmt.Errorf("listen must be 127.0.0.1:<port>, got %q", m.Listen)
	}
	if p, err := strconv.Atoi(port); err != nil || p < 1024 || p > 65535 || p == 4200 {
		return fmt.Errorf("listen port %q", port)
	}
	for _, c := range m.Caps {
		if !slices.Contains(allowedCaps, c) {
			return fmt.Errorf("capability %q is not allowed for plugins", c)
		}
	}
	if m.Releases != "" && !regexp.MustCompile(`^github:[\w.-]+/[\w.-]+$`).MatchString(m.Releases) {
		return fmt.Errorf("releases %q", m.Releases)
	}
	return nil
}

// LoadRecipes reads every <dir>/<name>/plugin.json.
func LoadRecipes(dir string) (map[string]*Manifest, error) {
	out := map[string]*Manifest{}
	ents, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(dir, e.Name(), "plugin.json")
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		m := &Manifest{}
		if err := json.Unmarshal(b, m); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		if err := m.validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		if m.Name != e.Name() {
			return nil, fmt.Errorf("%s: name %q != dir %q", p, m.Name, e.Name())
		}
		m.dir = filepath.Join(dir, e.Name())
		out[m.Name] = m
	}
	return out, nil
}

// PluginState is plugins/<name>/state.json, kept by the supervisor.
type PluginState struct {
	Enabled  bool   `json:"enabled"`
	Version  string `json:"version,omitempty"`  // the one "current" points at
	Previous string `json:"previous,omitempty"` // kept for rollback
}

// PluginInfo is one row of GET /ctl/v1/plugins.
type PluginInfo struct {
	Name           string   `json:"name"`
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	Homepage       string   `json:"homepage,omitempty"`
	Releases       string   `json:"releases,omitempty"`
	API            string   `json:"api"`
	Caps           []string `json:"caps"`
	Listen         string   `json:"listen"`
	DefaultVersion string   `json:"default_version"`
	Enabled        bool     `json:"enabled"`
	Version        string   `json:"version,omitempty"`
	Previous       string   `json:"previous,omitempty"`
	// Phase: absent | installing | stopped | starting | running | failed
	Phase     string `json:"phase"`
	Since     int64  `json:"since,omitempty"` // unix: when the phase began
	Restarts  int    `json:"restarts"`
	LastError string `json:"last_error,omitempty"` // the process: why it exited / isn't answering
	// Notice: the last install/rollback event worth telling ("вернул v0.4.2"),
	// kept until the next successful install.
	Notice string `json:"notice,omitempty"`
}

func sortedInfos(m map[string]PluginInfo) []PluginInfo {
	out := make([]PluginInfo, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// --- supervisor socket protocol: one JSON request, one JSON answer ----------

type supReq struct {
	Op      string `json:"op"` // list | install | enable | disable | restart | rollback | log | logs | debug
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
	Which   string `json:"which,omitempty"` // log: install | run
	After   uint64 `json:"after,omitempty"` // logs: the host's own entries after this seq
	Set     bool   `json:"set,omitempty"`   // debug: switch it (On, Minutes); else just read
	On      bool   `json:"on,omitempty"`
	Minutes int    `json:"minutes,omitempty"`
}

type supResp struct {
	OK      bool           `json:"ok"`
	Error   string         `json:"error,omitempty"`
	Plugins []PluginInfo   `json:"plugins,omitempty"`
	Log     string         `json:"log,omitempty"`
	Logs    []LogEntry     `json:"logs,omitempty"`
	Debug   *LogDebugState `json:"debug,omitempty"`
}
