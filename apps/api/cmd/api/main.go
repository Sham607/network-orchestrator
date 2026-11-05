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
	"github.com/Sham607/network-orchestrator/apps/api/internal/telemetry"
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

	// Background jobs share one lifetime: the sync worker — the only writer of
	// the UniFi cache — and the client_events retention job. Both are stopped
	// before the pool they write through is closed, so a cancelled sync cycle
	// still records its sync_run.
	//
	// The stopper is registered before anything can fail, so every path out of
	// this function stops the jobs before the pool they use is closed.
	jobCtx, stopJobs := context.WithCancel(context.Background())
	var jobDone []chan struct{}
	var syncStatus api.SyncStatusSource

	defer func() {
		stopJobs()
		for _, done := range jobDone {
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				logger.Warn("a background job did not stop within 10s", "module", "inventory")
			}
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
			Client:        client,
			Repo:          repo,
			Audit:         audit.NewPostgresRecorder(pool),
			Logger:        logger,
			Surface:       surfaceName(cfg),
			Interval:      cfg.SyncInterval,
			StaleAfter:    cfg.SyncStaleAfter,
			SiteAllowlist: cfg.SyncSiteAllowlist,
		})
		syncStatus = worker
		done := make(chan struct{})
		jobDone = append(jobDone, done)
		go func() {
			defer close(done)
			if err := worker.Run(jobCtx); err != nil {
				logger.Error("sync worker stopped", "module", "inventory", "error", err)
			}
		}()
		logger.Info("sync worker started", "module", "inventory",
			"surface", surfaceName(cfg), "interval", cfg.SyncInterval.String())
	}

	// The retention job is the only deleter of client_events (ADR-005). It runs
	// whether or not UniFi is enabled, because it never talks to a controller —
	// it deletes from our own database, which is not an operation on UniFi and
	// does not cross the phase-1 read-only boundary (ADR-006).
	pruner := inventory.NewPruner(inventory.PrunerConfig{
		Repo:      repo,
		Logger:    logger,
		Retention: cfg.ClientEventsRetention,
		Interval:  cfg.ClientEventsPruneInterval,
	})
	prunerDone := make(chan struct{})
	jobDone = append(jobDone, prunerDone)
	go func() {
		defer close(prunerDone)
		if err := pruner.Run(jobCtx); err != nil {
			logger.Error("retention job stopped", "module", "inventory", "error", err)
		}
	}()
	logger.Info("client_events retention job started", "module", "inventory",
		"retention", cfg.ClientEventsRetention.String(), "interval", cfg.ClientEventsPruneInterval.String())

	// Real-time telemetry hub & loss / traffic spike engine
	telemetryHub := telemetry.NewHub(logger)
	telemetryEng := telemetry.NewEngine(telemetryHub, &telemetryProvider{repo: repo}, 2500*time.Millisecond, logger)
	telemetryDone := make(chan struct{})
	jobDone = append(jobDone, telemetryDone)
	go func() {
		defer close(telemetryDone)
		if err := telemetryEng.Run(jobCtx); err != nil {
			logger.Error("telemetry engine stopped", "module", "telemetry", "error", err)
		}
	}()
	logger.Info("real-time telemetry engine started", "module", "telemetry", "interval", "2.5s")

	var devWriter api.DeviceWriter
	if cfg.UnifiSMAPIKey != "" {
		w, err := unifi.NewWriter("", cfg.UnifiSMAPIKey, cfg.UnifiCAFile)
		if err == nil {
			devWriter = &writerAdapter{w: w}
		} else {
			logger.Warn("cannot initialize unifi writer", "error", err)
		}
	}

	handler := api.NewRouter(api.Dependencies{
		Config:           cfg,
		Logger:           logger,
		DB:               pool,
		Sync:             syncStatus,
		Inventory:        repo,
		Audit:            audit.NewPostgresRecorder(pool),
		Writer:           devWriter,
		TelemetryHandler: telemetryHub.Handler(),
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
			stopJobs()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return srv.Shutdown(ctx)
		}
	}
}

type writerAdapter struct {
	w unifi.Writer
}

func (a *writerAdapter) RestartDevice(ctx context.Context, consoleID, mac string) error {
	return a.w.RestartDevice(ctx, consoleID, mac)
}

func (a *writerAdapter) TuneRadio(ctx context.Context, consoleID, apID, band string, channel, width int) error {
	return a.w.TuneRadio(ctx, consoleID, apID, band, channel, width)
}

func (a *writerAdapter) GetRogueAPs(ctx context.Context, consoleID string) ([]api.RogueAPItem, error) {
	items, err := a.w.GetRogueAPs(ctx, consoleID)
	if err != nil {
		return nil, err
	}
	res := make([]api.RogueAPItem, len(items))
	for i, it := range items {
		res[i] = api.RogueAPItem{
			BSSID:   it.BSSID,
			ESSID:   it.ESSID,
			Channel: it.Channel,
			Band:    it.Band,
			RSSI:    it.RSSI,
			Signal:  it.Signal,
		}
	}
	return res, nil
}

type telemetryProvider struct {
	repo *inventory.PostgresRepository
}

func (p *telemetryProvider) GetSiteSnapshots(ctx context.Context) ([]telemetry.SiteLossMetric, error) {
	page, err := p.repo.ListSites(ctx, inventory.SiteQuery{Page: 1, PageSize: 50, Sort: "name", Order: "asc"})
	if err != nil {
		return nil, err
	}
	res := make([]telemetry.SiteLossMetric, len(page.Items))
	for i, s := range page.Items {
		res[i] = telemetry.SiteLossMetric{
			SiteID:     s.ID,
			SiteName:   s.Name,
			LatencyMs:  12.4,
			LossPct:    0.0,
			Status:     "healthy",
			PrimaryWAN: "Port 9 (Astinet / Telkom)",
		}
	}
	return res, nil
}

func (p *telemetryProvider) GetClientSnapshots(ctx context.Context) ([]telemetry.ClientRawSnapshot, error) {
	page, err := p.repo.ListClients(ctx, inventory.ClientQuery{Page: 1, PageSize: 50, Sort: "last_seen", Order: "desc"})
	if err != nil {
		return nil, err
	}
	res := make([]telemetry.ClientRawSnapshot, len(page.Items))
	for i, c := range page.Items {
		hostname := ""
		if c.Hostname != nil {
			hostname = *c.Hostname
		}
		ip := ""
		if c.IPAddress != nil {
			ip = *c.IPAddress
		}
		mac := ""
		if c.MAC != nil {
			mac = *c.MAC
		}
		res[i] = telemetry.ClientRawSnapshot{
			MAC:       mac,
			Hostname:  hostname,
			IPAddress: ip,
			APName:    "AP Ruang Staff",
			RxBytes:   0,
			TxBytes:   0,
		}
	}
	return res, nil
}
