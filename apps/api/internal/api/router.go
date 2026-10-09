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
	// Sync is the cache-freshness view. It is nil when UniFi is disabled, which
	// keeps /ready a database-only probe.
	Sync SyncStatusSource
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

// handleReady is a readiness probe. It requires a reachable database and, when
// UniFi is enabled, a cache fresher than SYNC_STALE_AFTER (ADR-003 §6).
//
// The database is checked first, so the two failures stay distinguishable: a
// stale cache is 503 unifi_unreachable (the controller is the broken
// dependency), an unreachable database is 503 service_unavailable. Neither is
// 500 internal_error — that code means an unexpected bug (ADR-004).
func handleReady(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.DB != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if err := deps.DB.Ping(ctx); err != nil {
				deps.Logger.Warn("readiness check failed", "module", "api", "event", "ready_failed", "reason", "database_unreachable")
				writeError(w, r, CodeServiceUnavailable, "database is not reachable", map[string]any{
					"reason": "database_unreachable",
				})
				return
			}
		}

		status := statusOf(deps)
		body := map[string]any{
			"status":        "ready",
			"unifi_enabled": deps.Config.UnifiEnabled,
			"unifi_mode":    string(deps.Config.UnifiMode),
		}

		if deps.Config.UnifiEnabled {
			if status.Stale {
				deps.Logger.Warn("readiness check failed", "module", "api", "event", "ready_failed", "reason", "cache_stale")
				details := map[string]any{"reason": "cache_stale", "last_successful_sync_at": nil}
				if status.LastSuccessfulSyncAt != nil {
					details["last_successful_sync_at"] = formatTime(*status.LastSuccessfulSyncAt)
				}
				writeError(w, r, CodeUnifiUnreachable, "the UniFi cache is stale", details)
				return
			}
			body["stale"] = false
			body["last_successful_sync_at"] = nil
			if status.LastSuccessfulSyncAt != nil {
				body["last_successful_sync_at"] = formatTime(*status.LastSuccessfulSyncAt)
			}
		}

		writeJSON(w, http.StatusOK, body)
	}
}

// handleSyncStatus reports cache freshness to operators.
func handleSyncStatus(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, syncStatusBody(statusOf(deps)))
	}
}
