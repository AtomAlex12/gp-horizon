package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func goodManifest() Manifest {
	return Manifest{
		Name: "gp", Title: "GP", DefaultVersion: "v0.4.2", Install: "install.sh",
		Run: []string{"bin/run"}, Caps: []string{"NET_ADMIN"}, Listen: "127.0.0.1:8081", Health: "/api/health",
		Releases: "github:balbomush/GP-access-control-plane", API: "gp",
	}
}

func TestManifestValidate(t *testing.T) {
	if m := goodManifest(); m.validate() != nil {
		t.Fatalf("good manifest: %v", m.validate())
	}
	for name, mut := range map[string]func(*Manifest){
		"bad name":          func(m *Manifest) { m.Name = "GP!" },
		"bad version":       func(m *Manifest) { m.DefaultVersion = "../x" },
		"install with path": func(m *Manifest) { m.Install = "../../bin/sh" },
		"absolute run":      func(m *Manifest) { m.Run = []string{"/bin/sh"} },
		"run escapes":       func(m *Manifest) { m.Run = []string{"../x"} },
		"listens outside":   func(m *Manifest) { m.Listen = "0.0.0.0:8081" },
		"controller port":   func(m *Manifest) { m.Listen = "127.0.0.1:4200" },
		"privileged port":   func(m *Manifest) { m.Listen = "127.0.0.1:80" },
		"reads others":      func(m *Manifest) { m.Caps = []string{"DAC_OVERRIDE"} },
		"ptrace":            func(m *Manifest) { m.Caps = []string{"SYS_PTRACE"} },
		"sys admin":         func(m *Manifest) { m.Caps = []string{"SYS_ADMIN"} },
		"health not a path": func(m *Manifest) { m.Health = "http://x" },
		"releases source":   func(m *Manifest) { m.Releases = "https://evil" },
	} {
		m := goodManifest()
		mut(&m)
		if m.validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestLoadRecipes(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "gp"), 0o755)
	b, _ := json.Marshal(goodManifest())
	os.WriteFile(filepath.Join(dir, "gp", "plugin.json"), b, 0o644)
	rs, err := LoadRecipes(dir)
	if err != nil || rs["gp"] == nil || rs["gp"].dir != filepath.Join(dir, "gp") {
		t.Fatalf("recipes %v err %v", rs, err)
	}
	os.MkdirAll(filepath.Join(dir, "other"), 0o755)
	os.WriteFile(filepath.Join(dir, "other", "plugin.json"), b, 0o644) // name "gp" in dir "other"
	if _, err := LoadRecipes(dir); err == nil {
		t.Error("a recipe whose name differs from its dir must be refused")
	}
	if rs, err := LoadRecipes(filepath.Join(dir, "none")); err != nil || len(rs) != 0 {
		t.Errorf("missing dir: %v %v", rs, err)
	}
}

// The recipes shipped in the image load and validate.
func TestShippedRecipes(t *testing.T) {
	rs, err := LoadRecipes("plugins")
	if err != nil {
		t.Fatal(err)
	}
	gp := rs["gp"]
	if gp == nil || gp.API != "gp" || gp.InstallEnv["ZAPRET_REF"] == "" {
		t.Fatalf("gp recipe: %+v", gp)
	}
	if _, err := os.Stat(filepath.Join(gp.dir, gp.Install)); err != nil {
		t.Error(err)
	}
}

// fakeSupervisor answers the socket protocol from a script of responses.
func fakeSupervisor(t *testing.T, handle func(supReq) supResp) string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skip("unix sockets unavailable:", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			var req supReq
			json.NewDecoder(c).Decode(&req)
			json.NewEncoder(c).Encode(handle(req))
			c.Close()
		}
	}()
	return sock
}

func TestPluginsHTTP(t *testing.T) {
	var got []supReq
	sock := fakeSupervisor(t, func(r supReq) supResp {
		got = append(got, r)
		switch r.Op {
		case "list":
			return supResp{OK: true, Plugins: []PluginInfo{{Name: "gp", Phase: "running", Version: "v0.4.2", Releases: "github:o/r"}}}
		case "install":
			if r.Version == "busy" {
				return supResp{Error: "установка уже идёт"}
			}
			return supResp{OK: true}
		case "log":
			return supResp{OK: true, Log: "line " + r.Which}
		}
		return supResp{OK: true}
	})
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/releases" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `[{"tag_name":"v0.4.3","prerelease":false},{"tag_name":"v0.5.0-alpha.1","prerelease":true},{"tag_name":"v9","draft":true},{"tag_name":"../x"}]`)
	}))
	defer gh.Close()
	defer func(u string) { githubAPI = u }(githubAPI)
	githubAPI = gh.URL

	mux := http.NewServeMux()
	NewPluginHost(sock).routes(mux)
	do := func(method, path, body string) (int, string) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w.Code, w.Body.String()
	}
	if code, body := do("GET", "/ctl/v1/plugins", ""); code != 200 || !strings.Contains(body, `"host":true`) || !strings.Contains(body, `"phase":"running"`) {
		t.Errorf("list -> %d %s", code, body)
	}
	if code, _ := do("POST", "/ctl/v1/plugins/gp/install", `{"version":"v0.4.3"}`); code != 200 {
		t.Errorf("install -> %d", code)
	}
	if got[len(got)-1].Version != "v0.4.3" || got[len(got)-1].Name != "gp" {
		t.Errorf("forwarded %+v", got[len(got)-1])
	}
	if code, body := do("POST", "/ctl/v1/plugins/gp/install", `{"version":"busy"}`); code != 409 || !strings.Contains(body, "уже идёт") {
		t.Errorf("busy install -> %d %s", code, body)
	}
	if code, _ := do("POST", "/ctl/v1/plugins/gp/format-disk", ""); code != 404 {
		t.Errorf("unknown op -> %d", code)
	}
	if _, body := do("GET", "/ctl/v1/plugins/gp/log?which=install", ""); !strings.Contains(body, "line install") {
		t.Errorf("log %s", body)
	}
	code, body := do("GET", "/ctl/v1/plugins/gp/releases", "")
	if code != 200 || !strings.Contains(body, `"v0.4.3"`) || !strings.Contains(body, `"v0.5.0-alpha.1"`) || strings.Contains(body, "v9") || strings.Contains(body, "../x") {
		t.Errorf("releases -> %d %s", code, body)
	}

	// no supervisor: the controller runs, the plugins say why they are absent
	mux = http.NewServeMux()
	NewPluginHost("").routes(mux)
	if code, body := do("GET", "/ctl/v1/plugins", ""); code != 200 || !strings.Contains(body, `"host":false`) || !strings.Contains(body, "без хоста плагинов") {
		t.Errorf("no host -> %d %s", code, body)
	}
	if code, _ := do("POST", "/ctl/v1/plugins/gp/enable", ""); code != 503 {
		t.Errorf("no host enable -> %d", code)
	}
}
