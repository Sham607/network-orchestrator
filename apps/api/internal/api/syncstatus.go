package api

import (
	"time"

	"github.com/Sham607/network-orchestrator/apps/api/internal/inventory"
)

// SyncStatusSource is the freshness view the transport needs. *inventory.Worker
// implements it; the interface keeps handler tests free of a database and a
// UniFi adapter.
type SyncStatusSource interface {
	Status() inventory.Status
}

// statusOf reads the freshness snapshot, falling back to a "no worker" view so
// the endpoints answer with the contract's shape even when UniFi is disabled.
func statusOf(deps Dependencies) inventory.Status {
	if deps.Sync != nil {
		return deps.Sync.Status()
	}
	return inventory.Status{Stale: true, StaleAfter: deps.Config.SyncStaleAfter, Breaker: inventory.BreakerClosed}
}

// syncStatusBody renders inventory.Status as the SyncStatus schema.
func syncStatusBody(st inventory.Status) map[string]any {
	breaker := map[string]any{"state": breakerState(st.Breaker), "opened_at": nil}
	if st.BreakerOpenedAt != nil {
		breaker["opened_at"] = formatTime(*st.BreakerOpenedAt)
	}

	body := map[string]any{
		"worker_running":          st.WorkerRunning,
		"last_successful_sync_at": nil,
		"consecutive_failures":    st.ConsecutiveFailures,
		"stale":                   st.Stale,
		"stale_after_seconds":     staleAfterSeconds(st.StaleAfter),
		"circuit_breaker":         breaker,
	}
	if st.LastSuccessfulSyncAt != nil {
		body["last_successful_sync_at"] = formatTime(*st.LastSuccessfulSyncAt)
	}
	if st.Surface != "" {
		health := map[string]any{"reachable": st.Reachable, "surface": st.Surface, "last_error_kind": nil}
		if st.LastErrorKind != "" {
			health["last_error_kind"] = st.LastErrorKind
		}
		body["unifi"] = health
	}
	if st.LastRun != nil {
		body["last_run"] = syncRunBody(*st.LastRun)
	}
	return body
}

func syncRunBody(run inventory.SyncRun) map[string]any {
	body := map[string]any{
		"surface":           run.Surface,
		"trigger":           run.Trigger,
		"status":            run.Status,
		"started_at":        formatTime(run.StartedAt),
		"finished_at":       nil,
		"duration_ms":       run.DurationMS,
		"sites_total":       run.SitesTotal,
		"sites_ok":          run.SitesOK,
		"sites_failed":      run.SitesFailed,
		"devices_upserted":  run.Devices,
		"clients_upserted":  run.Clients,
		"networks_upserted": run.Networks,
		"rate_limited":      run.RateLimited,
		"error_kind":        nil,
		"error_message":     nil,
	}
	if !run.FinishedAt.IsZero() {
		body["finished_at"] = formatTime(run.FinishedAt)
	}
	if run.ErrorKind != "" {
		body["error_kind"] = run.ErrorKind
	}
	if run.ErrorMessage != "" {
		body["error_message"] = run.ErrorMessage
	}
	return body
}

// breakerState defaults an unset breaker to closed, which is what an operator
// sees before the first cycle.
func breakerState(state inventory.BreakerState) string {
	if state == "" {
		return string(inventory.BreakerClosed)
	}
	return string(state)
}

func staleAfterSeconds(d time.Duration) int {
	if s := int(d.Seconds()); s >= 1 {
		return s
	}
	return 1
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
