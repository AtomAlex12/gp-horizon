// Command nuxk-controller runs on the Pi: the full web UI, metric history and
// (later) strategy selection for one nuxk-core agent on the router. It talks
// to the agent only over the agent's API contract (nuxk-core/api/openapi.yaml)
// with the agent's token; browsers log in to the controller as "admin".
//
// A fresh controller opens a setup wizard: 1) the admin password, 2) the
// router — its address and root login/password, traded once for the agent's
// API token (POST /api/v1/auth/pair). Both land in DATA_DIR/controller.json.
//
//	LISTEN       :4200
//	WEB_ROOT     full nuxk-web build
//	DATA_DIR     /var/lib/nuxk-controller
//	AGENT_URL    optional: the router's agent, with AGENT_TOKEN (its API_TOKEN)
//	AGENT_TOKEN  — skips wizard step 2 when nothing is stored yet
//
// In the container it starts as `nuxk-controller supervise` (root): that
// process runs `serve` (this web/API, as nobody) and the plugins, each with
// only the rights it declares — see plugin.go. Plain `nuxk-controller` (or
// `serve`) is the web/API alone, without plugins.
//
//	PLUGIN_RECIPES  /usr/share/nuxk/plugins   (supervise)
//	SUPERVISOR_SOCK /run/nuxk/supervisor.sock (set for serve by supervise)
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
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

	switch flag.Arg(0) {
	case "supervise":
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err := runSupervisor(ctx); err != nil {
			slog.Error("supervise", "err", err)
			os.Exit(1)
		}
		return
	case "", "serve":
	default:
		fmt.Fprintln(os.Stderr, "usage: nuxk-controller [serve | supervise | -version]")
		os.Exit(2)
	}
	serve()
}

func serve() {
	st, err := OpenStore(env("DATA_DIR", "/var/lib/nuxk-controller"))
	if err != nil {
		slog.Error("settings", "err", err)
		os.Exit(1)
	}
	if env("CONTROLLER_TOKEN", "") != "" {
		slog.Warn("CONTROLLER_TOKEN is no longer used: the web UI asks for the admin login")
	}
	// an older deployment configured the router by env: keep it connected
	if u, t := env("AGENT_URL", ""), env("AGENT_TOKEN", ""); u != "" && t != "" && st.Agent() == nil {
		if base, err := agentBase(u); err == nil {
			if err := st.SetAgent(AgentRef{URL: base, Token: t}); err != nil {
				slog.Error("settings", "err", err)
				os.Exit(1)
			}
			slog.Info("router taken from AGENT_URL / AGENT_TOKEN", "agent", base)
		}
	}
	ag := NewAgent("", "")
	if ref := st.Agent(); ref != nil {
		ag.Configure(ref.URL, ref.Token)
	}
	if !st.HasAdmin() {
		slog.Info("first start: open the web UI to set the admin password and connect the router")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go ag.Run(ctx, 5*time.Second)
	vl := NewVless(st, ag)
	go vl.Run(ctx)

	srv := &http.Server{
		Addr:              env("LISTEN", ":4200"),
		Handler:           NewServer(ag, st, NewSessions(), NewPluginHost(os.Getenv("SUPERVISOR_SOCK")), vl, env("WEB_ROOT", ""), version),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
		// no WriteTimeout: /api/v1/events is a long-lived stream
	}
	agentURL, _ := ag.Ref()
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
