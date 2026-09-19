// Command nuxk-core is the nuxk Horizon control daemon.
//
// It owns platform state (the learned domain->mode cache, engine configs,
// settings), supervises the engines (nfqws2, usque, xray) through a single
// adapter contract, drives the routing plane, runs health probes and the
// out-of-band mode auto-discovery, and exposes everything over /api/v1.
//
// The web UI and the engines never talk to each other — only to this daemon.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"nuxk.dev/horizon/core/internal/api"
	"nuxk.dev/horizon/core/internal/config"
	"nuxk.dev/horizon/core/internal/core"
	"nuxk.dev/horizon/core/internal/engine"
	"nuxk.dev/horizon/core/internal/engine/nfqws2"
	"nuxk.dev/horizon/core/internal/engine/usque"
	"nuxk.dev/horizon/core/internal/state"
)

var version = "0.0.0-dev" // set by -ldflags at build time

func main() {
	var (
		cfgPath = flag.String("config", "/opt/etc/nuxk/nuxk.conf", "path to config file")
		listen  = flag.String("listen", "", "override API listen address")
		webRoot = flag.String("web", "", "dir to serve nuxk-web static build (empty = API only)")
		debug   = flag.Bool("debug", false, "verbose logging")
	)
	flag.Parse()

	lvl := slog.LevelInfo
	if *debug {
		lvl = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})))
	slog.Info("nuxk-core starting", "version", version)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		slog.Error("load config", "path", *cfgPath, "err", err)
		os.Exit(1)
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	if *webRoot != "" {
		cfg.WebRoot = *webRoot
	}

	st, err := state.Open(cfg.StateDir)
	if err != nil {
		slog.Error("open state", "dir", cfg.StateDir, "err", err)
		os.Exit(1)
	}

	// Engine registry — adapters are wired here as they land.
	//   MVP-1: usque   MVP-2: + nfqws2   Ф.3: + xray
	reg := engine.NewRegistry()
	reg.Add(usque.New(cfg.Engines.Usque))
	reg.Add(nfqws2.New(cfg.Engines.Nfqws2)) // MVP-2
	// reg.Add(xray.New(cfg.Engines.Xray))    // Ф.3

	hub := core.NewHub(version)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	rc := core.NewReconciler(reg, hub, version)
	go rc.Run(ctx)

	srv := &http.Server{
		Addr: cfg.Listen,
		Handler: api.NewRouter(api.Deps{
			Version: version, State: st, Engines: reg, Hub: hub,
			WebRoot: cfg.WebRoot, Token: cfg.APIToken,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		slog.Info("api listening", "addr", cfg.Listen, "web", cfg.WebRoot != "")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case err := <-errc:
		slog.Error("server error", "err", err)
		os.Exit(1)
	case <-ctx.Done():
		slog.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("graceful shutdown failed", "err", err)
	}
	slog.Info("nuxk-core stopped")
}
