// Package api is the HTTP transport of the modular monolith. It maps domain
// results and typed errors onto the REST/JSON contract under /api/v1 and never
// talks to UniFi directly — it goes through inventory (docs/adr/003 §1, §5).
package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/Sham607/network-orchestrator/apps/api/internal/config"
)

// DB is the subset of the Postgres pool the transport needs. Keeping it as an
// interface lets handler tests run without a database.
type DB interface {
	Ping(ctx context.Context) error
}

// Dependencies are the collaborators a router needs.
type Dependencies struct {
	Config config.Config
	Logger *slog.Logger
	DB     DB
}

// NewRouter builds the API router.
func NewRouter(deps Dependencies) http.Handler {
	r := chi.NewRouter()
	r.Use(requestIDMiddleware)
	r.Use(middleware.Recoverer)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", handleHealth)
		r.Get("/ready", handleReady(deps))
		r.Get("/sync/status", handleSyncStatus(deps))
	})

	return r
}

// handleHealth is a liveness probe: it reports only that the process is up and
// must never touch the database or UniFi (ADR-003 §6).
func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

// handleReady is a readiness probe: the process can serve traffic only if the
// database is reachable. Cache-freshness gating arrives with the sync worker
// (ZENI-5); until then readiness deliberately does not depend on UniFi.
func handleReady(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.DB != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if err := deps.DB.Ping(ctx); err != nil {
				deps.Logger.Warn("readiness check failed", "module", "api", "event", "ready_failed", "reason", "database_unreachable")
				writeError(w, r, CodeInternalError, "database is not reachable", nil)
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":        "ready",
			"unifi_enabled": deps.Config.UnifiEnabled,
			"unifi_mode":    string(deps.Config.UnifiMode),
		})
	}
}

// handleSyncStatus reports cache freshness to operators. The shape is stable
// now so the dashboard can be built against it; the sync worker (ZENI-5) will
// start populating the values.
func handleSyncStatus(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"worker_running":          false,
			"last_successful_sync_at": nil,
			"consecutive_failures":    0,
			"stale":                   true,
			"stale_after_seconds":     int(deps.Config.SyncStaleAfter.Seconds()),
		})
	}
}
