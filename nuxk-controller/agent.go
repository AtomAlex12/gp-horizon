package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Agent is the controller's view of one nuxk-core agent. Its address and
// token come from the setup wizard (or AGENT_URL/AGENT_TOKEN) and can change
// at runtime — "" = not connected yet.
type Agent struct {
	HTTP *http.Client
	Hist *History

	mu      sync.Mutex
	url     string
	token   string
	info    json.RawMessage
	infoAt  time.Time
	lastOK  time.Time
	lastErr string
}

func NewAgent(url, token string) *Agent {
	a := &Agent{HTTP: &http.Client{Timeout: 8 * time.Second}}
	a.Configure(url, token)
	return a
}

// Configure points the controller at an agent, dropping what it knew about
// the previous one.
func (a *Agent) Configure(url, token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.url, a.token = strings.TrimRight(url, "/"), token
	a.info, a.infoAt, a.lastOK, a.lastErr = nil, time.Time{}, time.Time{}, ""
	a.Hist = NewHistory(720) // 1 h at 5 s
}

// Ref returns the agent's address and token; url "" = not connected.
func (a *Agent) Ref() (url, token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.url, a.token
}

func (a *Agent) history() *History {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.Hist
}

func (a *Agent) get(ctx context.Context, path string, v any) error {
	return a.send(ctx, http.MethodGet, path, nil, v)
}

// agentHTTPError is the agent's answer when it isn't 2xx.
type agentHTTPError struct {
	Path string
	Code int
	Body string
}

func (e *agentHTTPError) Error() string { return fmt.Sprintf("%s: HTTP %d %s", e.Path, e.Code, e.Body) }

// send calls the agent's API with the controller's token: in as the JSON
// body (nil = none), the answer into out (nil = ignored).
func (a *Agent) send(ctx context.Context, method, path string, in, out any) error {
	url, token := a.Ref()
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	ans, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode/100 != 2 {
		return &agentHTTPError{Path: path, Code: resp.StatusCode, Body: strings.TrimSpace(string(ans))}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(ans, out)
}

// Run samples the agent's counters every period; node info every minute.
func (a *Agent) Run(ctx context.Context, period time.Duration) {
	t := time.NewTicker(period)
	defer t.Stop()
	for {
		a.poll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (a *Agent) poll(ctx context.Context) {
	url, _ := a.Ref()
	if url == "" {
		return // the setup wizard hasn't connected a router yet
	}
	var m metrics
	err := a.get(ctx, "/api/v1/metrics", &m)
	if err == nil {
		a.history().Add(m)
		a.mu.Lock()
		stale := time.Since(a.infoAt) > time.Minute
		a.mu.Unlock()
		if stale {
			var info json.RawMessage
			if ierr := a.get(ctx, "/api/v1/info", &info); ierr == nil {
				a.mu.Lock()
				if a.url == url {
					a.info, a.infoAt = info, time.Now()
				}
				a.mu.Unlock()
			}
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if url != a.url {
		return // reconfigured while this poll ran
	}
	if err != nil {
		if err.Error() != a.lastErr {
			slog.Warn("agent unreachable", "url", url, "err", err)
		}
		a.lastErr = err.Error()
		return
	}
	if a.lastErr != "" {
		slog.Info("agent reachable again", "url", url)
	}
	a.lastErr, a.lastOK = "", time.Now()
}

// AgentState is GET /ctl/v1/agent.
type AgentState struct {
	URL       string          `json:"url"`
	Reachable bool            `json:"reachable"`
	LastOK    int64           `json:"last_ok,omitempty"`
	LastError string          `json:"last_error,omitempty"`
	Info      json.RawMessage `json:"info,omitempty"`
	Version   string          `json:"controller_version"`
}

func (a *Agent) State(version string) AgentState {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := AgentState{URL: a.url, Reachable: a.lastErr == "" && !a.lastOK.IsZero(), LastError: a.lastErr, Info: a.info, Version: version}
	if !a.lastOK.IsZero() {
		st.LastOK = a.lastOK.Unix()
	}
	return st
}
