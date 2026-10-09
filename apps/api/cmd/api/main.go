// Command api is the single binary of the Network Orchestrator modular monolith.
//
// It wires configuration, observability, the Postgres pool and the HTTP
// transport together, then serves the internal REST API described in
// docs/adr/003-architecture.md.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Sham607/network-orchestrator/apps/api/internal/api"
	"github.com/Sham607/network-orchestrator/apps/api/internal/audit"
	"github.com/Sham607/network-orchestrator/apps/api/internal/config"
	"github.com/Sham607/network-orchestrator/apps/api/internal/inventory"
	"github.com/Sham607/network-orchestrator/apps/api/internal/observability"
	"github.com/Sham607/network-orchestrator/apps/api/internal/unifi"
)

func main() {
	// Container healthcheck mode: distroless has no shell or curl, so the
	// binary probes itself. Used by docker-compose's api healthcheck.
	if len(os.Args) > 1 && os.Args[1] == "--healthcheck" {
		os.Exit(healthcheck())
	}

	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

// healthcheck returns the process exit code for a liveness probe against a
// single JSON endpoint. Keep it dependency-free: no config, no database.
func healthcheck() int {
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + addr + "/api/v1/health")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

// newUnifiClient builds the read-only adapter for the configured surface.
// config.Load has already validated that the credentials for the selected mode
// are present.
func newUnifiClient(cfg config.Config) (unifi.Client, error) {
	ucfg := unifi.Config{CAFile: cfg.UnifiCAFile}
	if cfg.UnifiMode == config.UnifiModeLocal {
		ucfg.Mode = unifi.ModeConsole
		ucfg.APIKey = cfg.UnifiLocalAPIKey
		ucfg.ConsoleID = cfg.UnifiConsoleID
		ucfg.ConsoleBaseURL = cfg.UnifiLocalURL
	} else {
		ucfg.Mode = unifi.ModeCloud
		ucfg.APIKey = cfg.UnifiSMAPIKey
	}
	return unifi.NewClient(ucfg)
}

// surfaceName is the sync_runs.surface / SyncStatus value for the mode.
func surfaceName(cfg config.Config) string {
	if cfg.UnifiMode == config.UnifiModeLocal {
		return inventory.SourceConsole
	}
	return inventory.SourceCloud
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := observability.NewLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	// The pool is created lazily: pgxpool.New only parses the DSN, it does not
	// dial until the first query. A controller or database being down must not
	// prevent the process from starting (see ADR-003 failure modes).
	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// The sync worker is the only writer of the UniFi cache. It lives for the
	// lifetime of the process and is stopped before the pool is closed, so its
	// last cycle is still recorded.
	workerCtx, stopWorker := context.WithCancel(context.Background())
	var workerDone chan struct{}
	var syncStatus api.SyncStatusSource

	// Registered before anything can fail, so every path stops the worker
	// before the pool it writes through is closed.
	defer func() {
		stopWorker()
		if workerDone == nil {
			return
		}
		select {
		case <-workerDone:
		case <-time.After(10 * time.Second):
			logger.Warn("sync worker did not stop within 10s", "module", "inventory")
		}
	}()

	// The cache repository is shared by the writer (the sync worker) and the
	// reader (the list endpoints).
	repo := inventory.NewPostgresRepository(pool)

	if cfg.UnifiEnabled {
		client, err := newUnifiClient(cfg)
		if err != nil {
			return err
		}
		worker := inventory.NewWorker(inventory.WorkerConfig{
			Client:     client,
			Repo:       repo,
			Audit:      audit.NewPostgresRecorder(pool),
			Logger:     logger,
			Surface:    surfaceName(cfg),
			Interval:   cfg.SyncInterval,
			StaleAfter: cfg.SyncStaleAfter,
		})
		syncStatus = worker
		workerDone = make(chan struct{})
		go func() {
			defer close(workerDone)
			if err := worker.Run(workerCtx); err != nil {
				logger.Error("sync worker stopped", "module", "inventory", "error", err)
			}
		}()
		logger.Info("sync worker started", "module", "inventory",
			"surface", surfaceName(cfg), "interval", cfg.SyncInterval.String())
	}

	handler := api.NewRouter(api.Dependencies{
		Config:    cfg,
		Logger:    logger,
		DB:        pool,
		Sync:      syncStatus,
		Inventory: repo,
	})

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Graceful shutdown on SIGINT/SIGTERM. SIGHUP is accepted as a reload
	// signal for credential rotation (ADR-003 §4); for now it is logged and
	// the process keeps running with the existing in-memory configuration.
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", cfg.HTTPAddr, "env", cfg.AppEnv)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	for {
		select {
		case err := <-errCh:
			return err
		case sig := <-shutdown:
			if sig == syscall.SIGHUP {
				logger.Info("received SIGHUP; credential reload is not wired yet, continuing")
				continue
			}
			logger.Info("shutting down", "signal", sig.String())
			stopWorker()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return srv.Shutdown(ctx)
		}
	}
}
