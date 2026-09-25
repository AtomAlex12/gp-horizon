// Command nuxk-controller runs on the Pi: the full web UI, metric history and
// (later) strategy selection for one nuxk-core agent on the router. It talks
// to the agent only over the agent's API contract (nuxk-core/api/openapi.yaml)
// with the agent's token; browsers talk to the controller with its own token.
//
//	AGENT_URL        http://192.168.1.1:4141   (required)
//	AGENT_TOKEN      the router's API_TOKEN     (required)
//	CONTROLLER_TOKEN login token for the UI; empty = generated once, saved
//	                 in DATA_DIR/ui-token and printed to the log
//	LISTEN           :4200
//	WEB_ROOT         full nuxk-web build
//	DATA_DIR         /var/lib/nuxk-controller
package main

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var (
	version = "0.0.0-dev"
	commit  = "unknown"
)

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func main() {
	showVer := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVer {
		fmt.Printf("nuxk-controller %s %s\n", version, commit)
		return
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	agentURL, agentTok := env("AGENT_URL", ""), env("AGENT_TOKEN", "")
	if agentURL == "" || agentTok == "" {
		slog.Error("AGENT_URL and AGENT_TOKEN are required (the router's address and its API_TOKEN from /opt/etc/nuxk/nuxk.conf)")
		os.Exit(1)
	}
	dataDir := env("DATA_DIR", "/var/lib/nuxk-controller")
	uiTok, err := uiToken(env("CONTROLLER_TOKEN", ""), dataDir)
	if err != nil {
		slog.Error("ui token", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ag := NewAgent(agentURL, agentTok)
	go ag.Run(ctx, 5*time.Second)

	srv := &http.Server{
		Addr:              env("LISTEN", ":4200"),
		Handler:           NewServer(ag, uiTok, env("WEB_ROOT", ""), version),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
		// no WriteTimeout: /api/v1/events is a long-lived stream
	}
	slog.Info("nuxk-controller", "version", version, "listen", srv.Addr, "agent", agentURL)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("listen", "err", err)
			os.Exit(1)
		}
	}()
	<-ctx.Done()
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(sctx)
}

// uiToken returns the configured token, or a generated one kept in dataDir.
func uiToken(cfg, dataDir string) (string, error) {
	if cfg != "" {
		return cfg, nil
	}
	p := filepath.Join(dataDir, "ui-token")
	if b, err := os.ReadFile(p); err == nil && len(strings.TrimSpace(string(b))) >= 16 {
		return strings.TrimSpace(string(b)), nil
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	tok := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf))
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(p, []byte(tok+"\n"), 0o600); err != nil {
		return "", err
	}
	slog.Info("generated a UI token (saved in "+p+"); log in to the web UI with it", "token", tok)
	return tok, nil
}
