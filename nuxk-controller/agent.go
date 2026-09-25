package main

import (
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

// Agent is the controller's view of one nuxk-core agent.
type Agent struct {
	URL   string
	Token string
	HTTP  *http.Client
	Hist  *History

	mu      sync.Mutex
	info    json.RawMessage
	infoAt  time.Time
	lastOK  time.Time
	lastErr string
}

func NewAgent(url, token string) *Agent {
	return &Agent{
		URL: strings.TrimRight(url, "/"), Token: token,
		HTTP: &http.Client{Timeout: 8 * time.Second},
		Hist: NewHistory(720), // 1 h at 5 s
	}
}

func (a *Agent) get(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.Token)
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.Unmarshal(body, v)
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
	var m metrics
	err := a.get(ctx, "/api/v1/metrics", &m)
	if err == nil {
		a.Hist.Add(m)
		a.mu.Lock()
		stale := time.Since(a.infoAt) > time.Minute
		a.mu.Unlock()
		if stale {
			var info json.RawMessage
			if ierr := a.get(ctx, "/api/v1/info", &info); ierr == nil {
				a.mu.Lock()
				a.info, a.infoAt = info, time.Now()
				a.mu.Unlock()
			}
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		if err.Error() != a.lastErr {
			slog.Warn("agent unreachable", "url", a.URL, "err", err)
		}
		a.lastErr = err.Error()
		return
	}
	if a.lastErr != "" {
		slog.Info("agent reachable again", "url", a.URL)
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
	st := AgentState{URL: a.URL, Reachable: a.lastErr == "" && !a.lastOK.IsZero(), LastError: a.lastErr, Info: a.info, Version: version}
	if !a.lastOK.IsZero() {
		st.LastOK = a.lastOK.Unix()
	}
	return st
}
