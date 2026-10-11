package inventory

import (
	"time"

	"github.com/Sham607/network-orchestrator/apps/api/internal/unifi"
)

// Source values mirror the `source` CHECK constraint on the cached tables.
const (
	SourceCloud   = "cloud"
	SourceConsole = "console"
	SourceMixed   = "mixed"
)

// sync_runs.status values (CHECK-constrained).
const (
	RunRunning = "running"
	RunSuccess = "success"
	RunPartial = "partial"
	RunFailed  = "failed"
	RunSkipped = "skipped"
)

// sync_runs.trigger values (CHECK-constrained).
const (
	TriggerSchedule = "schedule"
	TriggerManual   = "manual"
	TriggerStartup  = "startup"
)

// SiteSnapshot is one site with its children exactly as the adapter returned
// them, ready to be applied as a single idempotent upsert.
type SiteSnapshot struct {
	Site       unifi.Site
	ConsoleID  string
	Source     string
	SyncedAt   time.Time
	Devices    []unifi.Device
	Clients    []unifi.ClientInfo
	Networks   []unifi.Network
	HasClients bool
}

// SyncRun is one sync cycle as recorded in sync_runs. ID is empty until the row
// is written.
type SyncRun struct {
	ID           string
	Surface      string
	Trigger      string
	Status       string
	StartedAt    time.Time
	FinishedAt   time.Time
	DurationMS   int
	SitesTotal   int
	SitesOK      int
	SitesFailed  int
	Devices      int
	Clients      int
	Networks     int
	RateLimited  bool
	ErrorKind    string
	ErrorMessage string
}

// BreakerState is the sync circuit-breaker state (ADR-003 §3).
type BreakerState string

const (
	BreakerClosed   BreakerState = "closed"
	BreakerOpen     BreakerState = "open"
	BreakerHalfOpen BreakerState = "half_open"
)

// Status is the operator-facing freshness snapshot rendered by
// GET /api/v1/sync/status and used to gate GET /api/v1/ready.
type Status struct {
	WorkerRunning bool
	// LastSuccessfulSyncAt is nil until a cycle has ever succeeded.
	LastSuccessfulSyncAt *time.Time
	ConsecutiveFailures  int
	// Stale is true when the cache is older than StaleAfter, or when no cycle
	// has ever succeeded.
	Stale      bool
	StaleAfter time.Duration

	Breaker         BreakerState
	BreakerOpenedAt *time.Time

	// Surface is the UniFi surface the worker polls; empty when no worker runs.
	Surface string
	// Reachable is the result of the last controller health probe.
	Reachable     bool
	LastErrorKind string
	// LastRun is the most recent cycle, nil before the first one.
	LastRun *SyncRun
}
