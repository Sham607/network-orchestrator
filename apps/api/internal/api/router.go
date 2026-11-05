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

	"github.com/Sham607/network-orchestrator/apps/api/internal/audit"
	"github.com/Sham607/network-orchestrator/apps/api/internal/config"
	"github.com/Sham607/network-orchestrator/apps/api/internal/inventory"
)

// DB is the subset of the Postgres pool the transport needs. Keeping it as an
// interface lets handler tests run without a database.
type DB interface {
	Ping(ctx context.Context) error
}

// Inventory is the cache read side the list endpoints need. Keeping it as an
// interface lets handler tests run without a database and without UniFi.
type Inventory interface {
	ListSites(ctx context.Context, q inventory.SiteQuery) (inventory.SitePage, error)
	GetSite(ctx context.Context, id string) (inventory.Site, bool, error)
	ListDevices(ctx context.Context, q inventory.DeviceQuery) (inventory.DevicePage, error)
	GetDevice(ctx context.Context, id string) (inventory.Device, bool, error)
	ListClients(ctx context.Context, q inventory.ClientQuery) (inventory.ClientPage, error)
	GetClient(ctx context.Context, id string) (inventory.Client, bool, error)
	ListNetworks(ctx context.Context, q inventory.NetworkQuery) (inventory.NetworkPage, error)
	GetNetwork(ctx context.Context, id string) (inventory.Network, bool, error)
}

type RogueAPItem struct {
	BSSID   string `json:"bssid"`
	ESSID   string `json:"essid"`
	Channel int    `json:"channel"`
	Band    string `json:"band"`
	RSSI    int    `json:"rssi"`
	Signal  int    `json:"signal"`
}

// DeviceWriter defines the operational actions the router dispatches.
type DeviceWriter interface {
	RestartDevice(ctx context.Context, consoleID, mac string) error
	TuneRadio(ctx context.Context, consoleID, apID, band string, channel, width int) error
	GetRogueAPs(ctx context.Context, consoleID string) ([]RogueAPItem, error)
}

// Dependencies are the collaborators a router needs.
type Dependencies struct {
	Config config.Config
	Logger *slog.Logger
	DB     DB
	// Sync is the cache-freshness view. It is nil when UniFi is disabled, which
	// keeps /ready a database-only probe.
	Sync SyncStatusSource
	// Inventory serves the list endpoints from the cache. It is required.
	Inventory        Inventory
	Audit            audit.Recorder
	Writer           DeviceWriter
	TelemetryHandler http.HandlerFunc
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
		r.Get("/sites", handleListSites(deps))
		r.Get("/sites/{id}", handleGetSite(deps))
		r.Get("/sites/{id}/rf-scan", handleSiteRFScan(deps))
		r.Get("/sites/{id}/wan-health", handleSiteWANHealth(deps))
		r.Get("/devices", handleListDevices(deps))
		r.Get("/devices/{id}", handleGetDevice(deps))
		r.Get("/clients", handleListClients(deps))
		r.Get("/clients/{id}", handleGetClient(deps))
		r.Get("/networks", handleListNetworks(deps))
		r.Get("/networks/{id}", handleGetNetwork(deps))
		if deps.TelemetryHandler != nil {
			r.Get("/ws/telemetry", deps.TelemetryHandler)
		} else {
			r.Get("/ws/telemetry", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotImplemented)
			})
		}

		r.Post("/devices/{id}/actions", handleDeviceAction(deps))
		r.Post("/clients/{id}/actions", handleWriteDisabled)
	})

	return r
}

// handleWriteDisabled is the gated write stub (ZENI-19): write operations are
// not enabled in phase 1, so the route immediately rejects with 403 forbidden
// and details.reason: write_disabled without parsing the body or touching UniFi.
func handleWriteDisabled(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, CodeForbidden, "write operations are not enabled", map[string]any{
		"reason": "write_disabled",
	})
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
// The database is checked first, so the failures stay distinguishable: an
// unreachable database is 503 service_unavailable (details.reason
// database_unreachable), a stale cache is 503 unifi_unreachable (cache_stale) —
// unless the worker recorded a failure of our own (cache_not_fresh, 503
// service_unavailable), because then the controller is not the broken
// dependency. None of them is 500 internal_error — that code means an
// unexpected bug (ADR-004, ADR-007).
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
				// A stale cache blames the controller by default: its silence is what
				// stops the cache from refreshing, and ADR-004 locked that answer.
				// Only positive evidence that our own side failed moves the blame —
				// the worker recorded a failure that did not come from the adapter.
				//
				// Reachability alone is not that evidence. It is refreshed once per
				// cycle, so it lags: a controller that just went offline is still
				// reported as reachable until the next probe fails, which leaves a
				// window where a stale cache sits under a "reachable" controller
				// (ADR-007).
				ourFailure := status.Reachable && status.LastErrorKind == inventory.ErrKindInternal
				code, reason, message := CodeUnifiUnreachable, "cache_stale", "the UniFi cache is stale"
				if ourFailure {
					code = CodeServiceUnavailable
					reason = "cache_not_fresh"
					message = "the cache is stale and the sync worker could not refresh it"
				}
				details := map[string]any{
					"reason":                  reason,
					"last_successful_sync_at": nil,
				}
				if status.LastSuccessfulSyncAt != nil {
					details["last_successful_sync_at"] = formatTime(*status.LastSuccessfulSyncAt)
				}
				if ourFailure {
					details["last_error_kind"] = status.LastErrorKind
				}
				deps.Logger.Warn("readiness check failed", "module", "api", "event", "ready_failed", "reason", reason, "unifi_reachable", status.Reachable, "last_error_kind", status.LastErrorKind)
				writeError(w, r, code, message, details)
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
