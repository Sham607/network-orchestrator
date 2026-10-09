package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Sham607/network-orchestrator/apps/api/internal/config"
	"github.com/Sham607/network-orchestrator/apps/api/internal/inventory"
)

type fakeDB struct {
	err error
}

func (f fakeDB) Ping(context.Context) error { return f.err }

func testRouter(t *testing.T, db DB) http.Handler {
	t.Helper()
	return testRouterWith(t, config.Config{
		UnifiEnabled:   false,
		UnifiMode:      config.UnifiModeCloud,
		SyncStaleAfter: 180000000000, // 3m, in nanoseconds
	}, db, nil)
}

func testRouterWith(t *testing.T, cfg config.Config, db DB, sync SyncStatusSource) http.Handler {
	t.Helper()
	return testRouterWithInventory(t, cfg, db, sync, nil)
}

func testRouterWithInventory(t *testing.T, cfg config.Config, db DB, sync SyncStatusSource, inv Inventory) http.Handler {
	t.Helper()
	return NewRouter(Dependencies{
		Config:    cfg,
		Logger:    slog.New(slog.NewTextHandler(discard{}, nil)),
		DB:        db,
		Sync:      sync,
		Inventory: inv,
	})
}

// staleStatus is the freshness view of a controller that has never answered.
func staleStatus() inventory.Status {
	return inventory.Status{Stale: true, StaleAfter: 3 * time.Minute, Breaker: inventory.BreakerClosed}
}

type fakeStatus struct{ status inventory.Status }

func (f fakeStatus) Status() inventory.Status { return f.status }

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func TestHealthIsLivenessOnly(t *testing.T) {
	// Even with a failing database, liveness must stay 200.
	rr := httptest.NewRecorder()
	testRouter(t, fakeDB{err: errors.New("boom")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("health status = %d, want %d", rr.Code, http.StatusOK)
	}
	if got := rr.Header().Get(requestIDHeader); got == "" {
		t.Fatal("expected X-Request-Id response header")
	}
}

func TestReadyFailsWhenDatabaseIsDown(t *testing.T) {
	rr := httptest.NewRecorder()
	testRouter(t, fakeDB{err: errors.New("connection refused")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/ready", nil))

	// 503, not 500: an unreachable dependency is expected and recoverable,
	// while 500/internal_error means a bug in our own code (ADR-004).
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}
	var body ErrorBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Code != CodeServiceUnavailable {
		t.Fatalf("code = %q, want %q", body.Code, CodeServiceUnavailable)
	}
	if body.Details["reason"] != "database_unreachable" {
		t.Fatalf("details.reason = %v, want %q", body.Details["reason"], "database_unreachable")
	}
	if body.Details["request_id"] == "" {
		t.Fatal("expected request_id in error details")
	}
}

func TestReadySucceedsWhenDatabaseIsUp(t *testing.T) {
	rr := httptest.NewRecorder()
	testRouter(t, fakeDB{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/ready", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("ready status = %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestErrorStatusMapping(t *testing.T) {
	cases := map[Code]int{
		CodeValidationFailed: http.StatusBadRequest,
		CodeUnauthorized:     http.StatusUnauthorized,
		CodeForbidden:        http.StatusForbidden,
		CodeNotFound:         http.StatusNotFound,
		CodeRateLimited:      http.StatusTooManyRequests,
		CodeUnifiRateLimited: http.StatusTooManyRequests,
		CodeUnifiUnreachable: http.StatusServiceUnavailable,
		CodeUnifiSchemaDrift: http.StatusServiceUnavailable,
		CodeUnifiAuthFailed:  http.StatusBadGateway,
		CodeUnifiForbidden:   http.StatusBadGateway,
		// ADR-004: a dependency we need is down — 503, deliberately not 500.
		CodeServiceUnavailable: http.StatusServiceUnavailable,
		CodeInternalError:      http.StatusInternalServerError,
	}
	for code, want := range cases {
		if got := httpStatus(code); got != want {
			t.Errorf("httpStatus(%q) = %d, want %d", code, got, want)
		}
	}
}

// --- readiness: cache-freshness gating (ADR-003 §6, ADR-004) ----------------

func TestReadyFailsWithUnifiUnreachableWhenCacheIsStale(t *testing.T) {
	cfg := config.Config{UnifiEnabled: true, UnifiMode: config.UnifiModeCloud, SyncStaleAfter: 3 * time.Minute}
	rr := httptest.NewRecorder()
	testRouterWith(t, cfg, fakeDB{}, fakeStatus{status: staleStatus()}).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/ready", nil))

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready status = %d, want %d (body: %s)", rr.Code, http.StatusServiceUnavailable, rr.Body.String())
	}
	var body ErrorBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	// A stale cache means the controller is the broken dependency, so the code
	// names UniFi rather than the service as a whole (ADR-004).
	if body.Code != CodeUnifiUnreachable {
		t.Fatalf("code = %q, want %q", body.Code, CodeUnifiUnreachable)
	}
	if body.Details["reason"] != "cache_stale" {
		t.Fatalf("details.reason = %v, want %q", body.Details["reason"], "cache_stale")
	}
}

func TestReadyDistinguishesAStaleCacheFromADeadDatabase(t *testing.T) {
	cfg := config.Config{UnifiEnabled: true, UnifiMode: config.UnifiModeCloud, SyncStaleAfter: 3 * time.Minute}

	// Both failures are present at once. The database is checked first, so the
	// operator is told the database is down rather than that UniFi is.
	rr := httptest.NewRecorder()
	testRouterWith(t, cfg, fakeDB{err: errors.New("connection refused")}, fakeStatus{status: staleStatus()}).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/ready", nil))

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}
	var body ErrorBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Code != CodeServiceUnavailable {
		t.Fatalf("code = %q, want %q (a dead database must not be reported as unifi_unreachable)", body.Code, CodeServiceUnavailable)
	}
	if body.Details["reason"] != "database_unreachable" {
		t.Fatalf("details.reason = %v, want %q", body.Details["reason"], "database_unreachable")
	}
}

func TestReadySucceedsWhenTheCacheIsFresh(t *testing.T) {
	cfg := config.Config{UnifiEnabled: true, UnifiMode: config.UnifiModeCloud, SyncStaleAfter: 3 * time.Minute}
	synced := time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC)

	rr := httptest.NewRecorder()
	testRouterWith(t, cfg, fakeDB{}, fakeStatus{status: inventory.Status{
		LastSuccessfulSyncAt: &synced,
		StaleAfter:           3 * time.Minute,
		Reachable:            true,
		Surface:              inventory.SourceCloud,
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/ready", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("ready status = %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode ready body: %v", err)
	}
	if body["stale"] != false {
		t.Fatalf("stale = %v, want false", body["stale"])
	}
	if body["last_successful_sync_at"] != synced.Format(time.RFC3339) {
		t.Fatalf("last_successful_sync_at = %v, want %q", body["last_successful_sync_at"], synced.Format(time.RFC3339))
	}
}

func TestReadyIgnoresStalenessWhenUnifiIsDisabled(t *testing.T) {
	rr := httptest.NewRecorder()
	testRouterWith(t, config.Config{UnifiMode: config.UnifiModeCloud, SyncStaleAfter: 3 * time.Minute},
		fakeDB{}, fakeStatus{status: staleStatus()}).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/ready", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("ready status = %d, want %d with UniFi disabled", rr.Code, http.StatusOK)
	}
}

func TestHealthStaysOKWhileTheCacheIsStale(t *testing.T) {
	cfg := config.Config{UnifiEnabled: true, UnifiMode: config.UnifiModeCloud, SyncStaleAfter: 3 * time.Minute}

	// Liveness must never depend on the controller or the database (ADR-003 §6):
	// a stale cache degrades readiness, not liveness.
	rr := httptest.NewRecorder()
	testRouterWith(t, cfg, fakeDB{err: errors.New("connection refused")}, fakeStatus{status: staleStatus()}).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("health status = %d, want %d", rr.Code, http.StatusOK)
	}
}

// --- sync status -----------------------------------------------------------

func TestSyncStatusReportsWorkerValues(t *testing.T) {
	cfg := config.Config{UnifiEnabled: true, UnifiMode: config.UnifiModeCloud, SyncStaleAfter: 3 * time.Minute}
	synced := time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC)
	started := synced.Add(-2 * time.Second)
	opened := synced.Add(time.Minute)

	rr := httptest.NewRecorder()
	testRouterWith(t, cfg, fakeDB{}, fakeStatus{status: inventory.Status{
		WorkerRunning:        true,
		LastSuccessfulSyncAt: &synced,
		ConsecutiveFailures:  2,
		Stale:                true,
		StaleAfter:           3 * time.Minute,
		Breaker:              inventory.BreakerOpen,
		BreakerOpenedAt:      &opened,
		Surface:              inventory.SourceCloud,
		Reachable:            false,
		LastErrorKind:        "unifi_unreachable",
		LastRun: &inventory.SyncRun{
			Surface: inventory.SourceCloud, Trigger: inventory.TriggerSchedule,
			Status: inventory.RunFailed, StartedAt: started, FinishedAt: synced,
			DurationMS: 2000, SitesTotal: 21, ErrorKind: "unifi_unreachable",
		},
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/sync/status", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("sync/status = %d, want %d", rr.Code, http.StatusOK)
	}
	var body struct {
		WorkerRunning        bool   `json:"worker_running"`
		LastSuccessfulSyncAt string `json:"last_successful_sync_at"`
		ConsecutiveFailures  int    `json:"consecutive_failures"`
		Stale                bool   `json:"stale"`
		StaleAfterSeconds    int    `json:"stale_after_seconds"`
		CircuitBreaker       struct {
			State    string `json:"state"`
			OpenedAt string `json:"opened_at"`
		} `json:"circuit_breaker"`
		Unifi struct {
			Reachable     bool   `json:"reachable"`
			Surface       string `json:"surface"`
			LastErrorKind string `json:"last_error_kind"`
		} `json:"unifi"`
		LastRun struct {
			Status     string `json:"status"`
			SitesTotal int    `json:"sites_total"`
			DurationMS int    `json:"duration_ms"`
			ErrorKind  string `json:"error_kind"`
		} `json:"last_run"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode sync/status body: %v", err)
	}

	if !body.WorkerRunning {
		t.Error("worker_running = false, want true")
	}
	if body.LastSuccessfulSyncAt != synced.Format(time.RFC3339) {
		t.Errorf("last_successful_sync_at = %q, want %q", body.LastSuccessfulSyncAt, synced.Format(time.RFC3339))
	}
	if body.ConsecutiveFailures != 2 {
		t.Errorf("consecutive_failures = %d, want 2", body.ConsecutiveFailures)
	}
	if !body.Stale {
		t.Error("stale = false, want true")
	}
	if body.StaleAfterSeconds != 180 {
		t.Errorf("stale_after_seconds = %d, want 180", body.StaleAfterSeconds)
	}
	if body.CircuitBreaker.State != "open" || body.CircuitBreaker.OpenedAt != opened.Format(time.RFC3339) {
		t.Errorf("circuit_breaker = %+v, want open at %s", body.CircuitBreaker, opened.Format(time.RFC3339))
	}
	if body.Unifi.Reachable || body.Unifi.Surface != inventory.SourceCloud || body.Unifi.LastErrorKind != "unifi_unreachable" {
		t.Errorf("unifi = %+v, want unreachable cloud with last_error_kind unifi_unreachable", body.Unifi)
	}
	if body.LastRun.Status != inventory.RunFailed || body.LastRun.SitesTotal != 21 || body.LastRun.ErrorKind != "unifi_unreachable" {
		t.Errorf("last_run = %+v, want a failed run over 21 sites", body.LastRun)
	}
}

func TestSyncStatusWithoutAWorkerStillMatchesTheContract(t *testing.T) {
	rr := httptest.NewRecorder()
	testRouter(t, fakeDB{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/sync/status", nil))

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode sync/status body: %v", err)
	}
	// The SyncStatus schema requires all five of these keys.
	for _, key := range []string{"worker_running", "last_successful_sync_at", "consecutive_failures", "stale", "stale_after_seconds"} {
		if _, ok := body[key]; !ok {
			t.Errorf("sync/status body is missing required key %q", key)
		}
	}
	if body["stale"] != true {
		t.Errorf("stale = %v, want true when no worker is running", body["stale"])
	}
	if body["stale_after_seconds"] != float64(180) {
		t.Errorf("stale_after_seconds = %v, want 180", body["stale_after_seconds"])
	}
}
