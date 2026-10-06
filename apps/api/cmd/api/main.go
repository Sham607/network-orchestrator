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
	"github.com/Sham607/network-orchestrator/apps/api/internal/config"
	"github.com/Sham607/network-orchestrator/apps/api/internal/observability"
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

	handler := api.NewRouter(api.Dependencies{
		Config: cfg,
		Logger: logger,
		DB:     pool,
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
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return srv.Shutdown(ctx)
		}
	}
}
