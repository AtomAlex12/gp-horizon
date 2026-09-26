package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// GP (plugin "gp") is the strategy search behind «Прогоны»: its core API
// listens on the plugin's loopback port inside this container. The browser
// never sees GP: it calls /ctl/v1/gp/*, the controller adds GP's token.
//
// GP starts with admin/admin; the first time the controller reaches it, it
// replaces that with a random password kept in controller.json.

const gpUser = "admin"

type GPClient struct {
	Base  string // http://127.0.0.1:8081
	Store *Store
	Host  *PluginHost
	HTTP  *http.Client

	mu      sync.Mutex
	token   string
	expires time.Time
}

// NewGPClient: base "" = the plugin's own listen address.
func NewGPClient(base string, st *Store, host *PluginHost) *GPClient {
	return &GPClient{Base: base, Store: st, Host: host, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

type gpBearer struct {
	Token     string `json:"access_token"`
	ExpiresIn int    `json:"expires_in"`
}

// errGPAuth: GP took neither the stored password nor admin/admin.
var errGPAuth = errors.New("GP не принял пароль контроллера — сбросьте данные плагина GP (переустановка) или задайте пароль заново")

func (g *GPClient) post(ctx context.Context, path, token string, body any, out any) (int, error) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, g.Base+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := g.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusOK && out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

func (g *GPClient) login(ctx context.Context, password string) (gpBearer, int, error) {
	var b gpBearer
	code, err := g.post(ctx, "/api/auth/login", "", map[string]string{"username": gpUser, "password": password}, &b)
	return b, code, err
}

// Token returns a valid GP bearer token, logging in (and rotating GP's
// factory password) as needed.
func (g *GPClient) Token(ctx context.Context) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.token != "" && time.Until(g.expires) > 5*time.Minute {
		return g.token, nil
	}
	if pw := g.Store.PluginSecret("gp"); pw != "" {
		b, code, err := g.login(ctx, pw)
		if err != nil {
			return "", fmt.Errorf("GP не отвечает: %w", err)
		}
		if code == http.StatusOK {
			return g.keep(b), nil
		}
	}
	// a fresh GP (or its data was reset): take over from the factory password
	b, code, err := g.login(ctx, "admin")
	if err != nil {
		return "", fmt.Errorf("GP не отвечает: %w", err)
	}
	if code != http.StatusOK {
		return "", errGPAuth
	}
	pw := randomPassword()
	var nb gpBearer
	code, err = g.post(ctx, "/api/auth/change-password", b.Token, map[string]string{"current_password": "admin", "new_password": pw}, &nb)
	if err != nil || code != http.StatusOK {
		return "", fmt.Errorf("GP: не удалось сменить заводской пароль (HTTP %d): %v", code, err)
	}
	if err := g.Store.SetPluginSecret("gp", pw); err != nil {
		return "", err
	}
	slog.Info("gp: factory password replaced")
	return g.keep(nb), nil
}

func (g *GPClient) keep(b gpBearer) string {
	g.token = b.Token
	g.expires = time.Now().Add(time.Duration(max(b.ExpiresIn, 60)) * time.Second)
	return b.Token
}

func (g *GPClient) forget() {
	g.mu.Lock()
	g.token = ""
	g.mu.Unlock()
}

func randomPassword() string {
	b := make([]byte, 24)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// gpAllowed: the GP core calls the UI may make, "METHOD path" under /api/core/.
// Nothing about GP's own auth, backups, service or web slices.
var gpAllowed = map[string]bool{
	"GET status":                                    true,
	"GET strategy-discovery/preflight":              true,
	"GET strategy-discovery/current-run-progress":   true,
	"GET strategy-discovery/current-run-latest-log": true,
	"POST strategy-discovery/start-run":             true,
	"POST strategy-discovery/stop-current-run":      true,
	"GET runs/history":                              true,
	"GET runs/latest-log":                           true,
	"GET strategy-candidates":                       true,
	"GET presets/domain-lists":                      true,
	"GET run-settings":                              true,
}

// handle proxies /ctl/v1/gp/<path> to GP's /api/core/<path>.
func (g *GPClient) handle(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")
	if !gpAllowed[r.Method+" "+path] {
		writeErr(w, http.StatusNotFound, "not_allowed", "этот вызов GP недоступен из панели")
		return
	}
	p, ok := g.Host.Get("gp")
	if !ok || p.Phase != "running" {
		writeErr(w, http.StatusServiceUnavailable, "gp_not_running", "плагин GP не запущен — установите или включите его в «Плагинах»")
		return
	}
	g.mu.Lock()
	if g.Base == "" {
		g.Base = "http://" + p.Listen
	}
	base := g.Base
	g.mu.Unlock()
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(io.LimitReader(r.Body, 1<<20))
	}
	for attempt := 0; attempt < 2; attempt++ {
		tok, err := g.Token(r.Context())
		if err != nil {
			writeErr(w, http.StatusBadGateway, "gp_auth", err.Error())
			return
		}
		u := base + "/api/core/" + path
		if r.URL.RawQuery != "" {
			u += "?" + r.URL.RawQuery
		}
		req, _ := http.NewRequestWithContext(r.Context(), r.Method, u, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		if len(body) > 0 {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := g.HTTP.Do(req)
		if err != nil {
			writeErr(w, http.StatusBadGateway, "gp_unreachable", "GP не отвечает: "+err.Error())
			return
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			resp.Body.Close()
			g.forget() // the token expired or GP restarted: log in again once
			continue
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", strings.TrimSpace(resp.Header.Get("Content-Type")))
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, io.LimitReader(resp.Body, 16<<20))
		return
	}
}
