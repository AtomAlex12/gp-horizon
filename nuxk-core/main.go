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
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"nuxk.dev/horizon/core/internal/api"
	"nuxk.dev/horizon/core/internal/auth"
	"nuxk.dev/horizon/core/internal/config"
	"nuxk.dev/horizon/core/internal/core"
	"nuxk.dev/horizon/core/internal/engine"
	"nuxk.dev/horizon/core/internal/engine/nfqws2"
	"nuxk.dev/horizon/core/internal/engine/usque"
	"nuxk.dev/horizon/core/internal/engine/xray"
	"nuxk.dev/horizon/core/internal/logbuf"
	"nuxk.dev/horizon/core/internal/node"
	"nuxk.dev/horizon/core/internal/plane"
	"nuxk.dev/horizon/core/internal/plane/keenetic"
	"nuxk.dev/horizon/core/internal/state"
)

// Set by -ldflags at build time (see VERSION at the repo root and the Makefile).
var (
	version = "0.0.0-dev"
	commit  = "unknown"
)

func main() {
	var (
		cfgPath = flag.String("config", "/opt/etc/nuxk/nuxk.conf", "path to config file")
		listen  = flag.String("listen", "", "override API listen address")
		webRoot = flag.String("web", "", "dir to serve nuxk-web static build (empty = API only)")
		debug   = flag.Bool("debug", false, "verbose logging")
		showVer = flag.Bool("version", false, "print version and exit")
		logPath = flag.String("log", "", "log file, rotated in-process at 512 KiB (empty = stderr)")
	)
	flag.Parse()
	if *showVer {
		// One machine-readable line — nuxk-installer parses it to decide
		// whether the router's copy needs an upgrade.
		fmt.Printf("nuxk-core %s %s\n", version, commit)
		return
	}

	lvl := slog.LevelInfo
	if *debug {
		lvl = slog.LevelDebug
	}
	var logOut io.Writer = os.Stderr
	if *logPath != "" {
		lf, err := logbuf.OpenFile(*logPath, 512<<10)
		if err != nil {
			fmt.Fprintln(os.Stderr, "open log:", err)
			os.Exit(1)
		}
		logOut = lf
	}
	logs := logbuf.NewRing(500) // GET /api/v1/logs
	slog.SetDefault(slog.New(logbuf.NewHandler(logOut, logs, lvl)))
	slog.Info("nuxk-core starting", "version", version, "commit", commit)

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

	// Engine registry. An engine is wired only when its init script is
	// configured and present — a box running just usque shouldn't poll two
	// missing scripts every 5s and show two permanently "unknown" cards.
	//   MVP-1: usque   MVP-2: + nfqws2   MVP-3: + xray
	reg := engine.NewRegistry()
	for _, w := range []struct {
		kind   engine.Kind
		script string
		mk     func(string) engine.Engine
	}{
		{engine.KindUsque, cfg.Engines.Usque, func(s string) engine.Engine { return usque.New(s) }},
		{engine.KindNfqws2, cfg.Engines.Nfqws2, func(s string) engine.Engine { return nfqws2.New(s) }},
		{engine.KindXray, cfg.Engines.Xray, func(s string) engine.Engine { return xray.New(s) }},
	} {
		switch _, err := os.Stat(w.script); {
		case w.script == "":
			slog.Info("engine disabled in config", "engine", w.kind)
		case err != nil:
			slog.Warn("engine script not found, engine not wired", "engine", w.kind, "script", w.script)
		default:
			reg.Add(w.mk(w.script))
			slog.Info("engine wired", "engine", w.kind, "script", w.script)
		}
	}

	hub := core.NewHub(version)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ctl := core.NewController(reg, st, hub, version)
	if cfg.InfoEvery > 0 {
		ctl.InfoEvery = cfg.InfoEvery
	}
	if cfg.ProbeEvery > 0 {
		ctl.ProbeEvery = cfg.ProbeEvery
	}

	// Routing plane: drives the firmware's DNS routing. Off unless configured,
	// plan-only unless PLANE_APPLY=1.
	var pm *plane.Manager
	switch cfg.Plane.Backend {
	case "", "off":
	case "keenetic":
		pc := plane.Config{
			Ifaces: map[plane.Mode]string{plane.ModeWarp: cfg.Plane.IfaceWarp, plane.ModeVless: cfg.Plane.IfaceVless},
			Apply:  cfg.Plane.Apply, V6Deny: cfg.Plane.V6Deny, Every: cfg.Plane.Every,
		}
		if _, ok := reg.Get(engine.KindNfqws2); ok {
			// «DPI» list → nfqws2 hostlist, through the controller so it is
			// stored and restored like any other routing change
			pc.Desync = func(ctx context.Context, doms []string) error {
				return ctl.Apply(ctx, engine.KindNfqws2, engine.Routing{Domains: append([]string{}, doms...)})
			}
		}
		pm = plane.NewManager(keenetic.New(cfg.Plane.RCI), st, pc)
		ctl.PlaneStatus = func() any { return pm.Status() }
		// nfqws2's probe skips what goes into a tunnel: the person's own
		// routed lists (e.g. «Meta» → Wireguard1) and nuxk's WARP/VLESS
		// groups — once they are applied, not while only planned
		ctl.Tunneled = func() []string {
			s := pm.Status()
			var doms []string
			for _, g := range s.Groups {
				if s.Apply {
					doms = append(doms, g.Domains...)
				}
			}
			for _, f := range s.Foreign {
				doms = append(doms, f.Domains...)
			}
			return doms
		}
		slog.Info("routing plane", "backend", "keenetic", "apply", cfg.Plane.Apply, "v6deny", cfg.Plane.V6Deny)
		go pm.Run(ctx)
	default:
		slog.Error("unknown PLANE backend", "plane", cfg.Plane.Backend)
		os.Exit(1)
	}
	go ctl.Run(ctx)

	var guard *auth.Guard
	if cfg.AuthUser != "" {
		guard = auth.New(cfg.AuthUser, cfg.AuthFiles...)
	}

	srv := &http.Server{
		Addr: cfg.Listen,
		Handler: api.NewRouter(api.Deps{
			Version: version, Commit: commit, Engines: reg, Hub: hub, Ctl: ctl, Plane: pm,
			WebRoot: cfg.WebRoot, Token: cfg.APIToken, Auth: guard, Logs: logs,
			Node: node.New(cfg.NodeRole, cfg.Plane.RCI, version, commit),
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
