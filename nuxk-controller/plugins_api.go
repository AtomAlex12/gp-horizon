package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// PluginHost is the web/API side of the plugin host: it asks the supervisor
// (root, over its unix socket) for everything that needs rights.
type PluginHost struct {
	Socket string // "" = no supervisor: the controller runs without plugins
	HTTP   *http.Client

	mu       sync.Mutex
	releases map[string]releasesCache
}

type releasesCache struct {
	at   time.Time
	list []Release
	err  string
}

// Release is one version of a plugin, as its source publishes it.
type Release struct {
	Tag        string `json:"tag"`
	Name       string `json:"name,omitempty"`
	Prerelease bool   `json:"prerelease"`
	Published  string `json:"published_at,omitempty"`
	URL        string `json:"url,omitempty"`
}

func NewPluginHost(socket string) *PluginHost {
	return &PluginHost{Socket: socket, HTTP: &http.Client{Timeout: 10 * time.Second}, releases: map[string]releasesCache{}}
}

var errNoHost = errors.New("контроллер запущен без хоста плагинов")

func (h *PluginHost) call(req supReq) (supResp, error) {
	if h == nil || h.Socket == "" {
		return supResp{}, errNoHost
	}
	c, err := net.DialTimeout("unix", h.Socket, 3*time.Second)
	if err != nil {
		return supResp{}, fmt.Errorf("хост плагинов не отвечает: %w", err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(15 * time.Second))
	if err := json.NewEncoder(c).Encode(req); err != nil {
		return supResp{}, err
	}
	var resp supResp
	if err := json.NewDecoder(io.LimitReader(c, 4<<20)).Decode(&resp); err != nil {
		return supResp{}, err
	}
	if !resp.OK {
		return resp, errors.New(resp.Error)
	}
	return resp, nil
}

// List returns the plugins, or nil when there is no host.
func (h *PluginHost) List() ([]PluginInfo, error) {
	r, err := h.call(supReq{Op: "list"})
	return r.Plugins, err
}

// Get returns one plugin's row.
func (h *PluginHost) Get(name string) (PluginInfo, bool) {
	list, err := h.List()
	if err != nil {
		return PluginInfo{}, false
	}
	for _, p := range list {
		if p.Name == name {
			return p, true
		}
	}
	return PluginInfo{}, false
}

// Releases lists a plugin's published versions (GitHub releases), cached.
func (h *PluginHost) Releases(ctx context.Context, p PluginInfo) ([]Release, error) {
	repo, ok := strings.CutPrefix(p.Releases, "github:")
	if !ok {
		return nil, errors.New("у плагина нет источника версий")
	}
	h.mu.Lock()
	c, hit := h.releases[repo]
	h.mu.Unlock()
	if hit && time.Since(c.at) < 30*time.Minute {
		if c.err != "" {
			return nil, errors.New(c.err)
		}
		return c.list, nil
	}
	list, err := h.fetchReleases(ctx, repo)
	c = releasesCache{at: time.Now(), list: list}
	if err != nil {
		c.err = err.Error()
	}
	h.mu.Lock()
	h.releases[repo] = c
	h.mu.Unlock()
	return list, err
}

var githubAPI = "https://api.github.com"

func (h *PluginHost) fetchReleases(ctx context.Context, repo string) ([]Release, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, githubAPI+"/repos/"+repo+"/releases?per_page=20", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub недоступен: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub ответил HTTP %d", resp.StatusCode)
	}
	var raw []struct {
		Tag        string `json:"tag_name"`
		Name       string `json:"name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Published  string `json:"published_at"`
		URL        string `json:"html_url"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&raw); err != nil {
		return nil, err
	}
	out := []Release{}
	for _, r := range raw {
		if r.Draft || !versionRe.MatchString(r.Tag) {
			continue
		}
		out = append(out, Release{Tag: r.Tag, Name: r.Name, Prerelease: r.Prerelease, Published: r.Published, URL: r.URL})
	}
	return out, nil
}

// --- HTTP: /ctl/v1/plugins ---------------------------------------------------

// PluginsState is GET /ctl/v1/plugins.
type PluginsState struct {
	Host    bool         `json:"host"`
	Reason  string       `json:"reason,omitempty"`
	Plugins []PluginInfo `json:"plugins"`
}

func (h *PluginHost) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /ctl/v1/plugins", func(w http.ResponseWriter, r *http.Request) {
		list, err := h.List()
		st := PluginsState{Host: err == nil, Plugins: list}
		if err != nil {
			st.Reason, st.Plugins = err.Error(), []PluginInfo{}
		}
		writeJSON(w, http.StatusOK, st)
	})
	mux.HandleFunc("POST /ctl/v1/plugins/{name}/{op}", func(w http.ResponseWriter, r *http.Request) {
		op := r.PathValue("op")
		switch op {
		case "install", "enable", "disable", "restart", "rollback":
		default:
			writeErr(w, http.StatusNotFound, "bad_op", "нет такого действия: "+op)
			return
		}
		var body struct {
			Version string `json:"version"`
		}
		if op == "install" && r.ContentLength != 0 {
			if !decode(w, r, &body) {
				return
			}
		}
		if _, err := h.call(supReq{Op: op, Name: r.PathValue("name"), Version: body.Version}); err != nil {
			code := http.StatusConflict
			if errors.Is(err, errNoHost) {
				code = http.StatusServiceUnavailable
			}
			writeErr(w, code, "plugin_"+op, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /ctl/v1/plugins/{name}/log", func(w http.ResponseWriter, r *http.Request) {
		which := "run"
		if r.URL.Query().Get("which") == "install" {
			which = "install"
		}
		resp, err := h.call(supReq{Op: "log", Name: r.PathValue("name"), Which: which})
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, "plugin_log", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"log": resp.Log})
	})
	mux.HandleFunc("GET /ctl/v1/plugins/{name}/releases", func(w http.ResponseWriter, r *http.Request) {
		p, ok := h.Get(r.PathValue("name"))
		if !ok {
			writeErr(w, http.StatusNotFound, "no_plugin", "нет такого плагина")
			return
		}
		list, err := h.Releases(r.Context(), p)
		if err != nil {
			writeErr(w, http.StatusBadGateway, "releases", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, list)
	})
}
