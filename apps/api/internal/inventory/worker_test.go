package inventory

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Sham607/network-orchestrator/apps/api/internal/audit"
	"github.com/Sham607/network-orchestrator/apps/api/internal/unifi"
)

// --- fakes -----------------------------------------------------------------

type fakeClient struct {
	healthErr error
	sites     []unifi.Site
	sitesErr  error

	devices    map[string][]unifi.Device
	deviceErr  map[string]error
	clients    map[string][]unifi.ClientInfo
	clientErr  map[string]error
	networks   map[string][]unifi.Network
	networkErr map[string]error

	healthCalls int
	siteCalls   int
}

func (f *fakeClient) Health(context.Context) error {
	f.healthCalls++
	return f.healthErr
}

func (f *fakeClient) ListSites(context.Context) ([]unifi.Site, error) {
	f.siteCalls++
	return f.sites, f.sitesErr
}

func (f *fakeClient) ListDevices(_ context.Context, siteID string) ([]unifi.Device, error) {
	return f.devices[siteID], f.deviceErr[siteID]
}

func (f *fakeClient) ListClients(_ context.Context, siteID string) ([]unifi.ClientInfo, error) {
	return f.clients[siteID], f.clientErr[siteID]
}

func (f *fakeClient) ListNetworks(_ context.Context, siteID string) ([]unifi.Network, error) {
	return f.networks[siteID], f.networkErr[siteID]
}

type fakeRepo struct {
	applied  []SiteSnapshot
	applyErr map[string]error
	runs     []SyncRun
	recorded chan SyncRun

	anchor    time.Time
	anchorOK  bool
	anchorErr error
}

func (f *fakeRepo) ApplySite(_ context.Context, snap SiteSnapshot) error {
	if err := f.applyErr[snap.Site.ID]; err != nil {
		return err
	}
	f.applied = append(f.applied, snap)
	return nil
}

func (f *fakeRepo) RecordSyncRun(_ context.Context, run SyncRun) (string, error) {
	f.runs = append(f.runs, run)
	if f.recorded != nil {
		f.recorded <- run
	}
	return fmt.Sprintf("run-%d", len(f.runs)), nil
}

func (f *fakeRepo) LastSuccessfulSyncAt(context.Context) (time.Time, bool, error) {
	return f.anchor, f.anchorOK, f.anchorErr
}

type fakeAudit struct {
	entries []audit.Entry
	err     error
}

func (f *fakeAudit) Record(_ context.Context, e audit.Entry) error {
	if f.err != nil {
		return f.err
	}
	f.entries = append(f.entries, e)
	return nil
}

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// --- helpers ---------------------------------------------------------------

type harness struct {
	client *fakeClient
	repo   *fakeRepo
	audit  *fakeAudit
	clock  *testClock
	worker *Worker
	logs   *bytes.Buffer
}

func newHarness(t *testing.T, mutate func(*fakeClient)) *harness {
	t.Helper()

	client := &fakeClient{
		devices:    map[string][]unifi.Device{},
		deviceErr:  map[string]error{},
		clients:    map[string][]unifi.ClientInfo{},
		clientErr:  map[string]error{},
		networks:   map[string][]unifi.Network{},
		networkErr: map[string]error{},
	}
	repo := &fakeRepo{applyErr: map[string]error{}}
	rec := &fakeAudit{}
	clock := &testClock{t: time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC)}
	if mutate != nil {
		mutate(client)
	}

	logs := &bytes.Buffer{}
	worker := NewWorker(WorkerConfig{
		Client:     client,
		Repo:       repo,
		Audit:      rec,
		Logger:     slog.New(slog.NewTextHandler(logs, nil)),
		Surface:    SourceCloud,
		Interval:   60 * time.Second,
		StaleAfter: 3 * time.Minute,
	})
	worker.now = clock.now

	return &harness{client: client, repo: repo, audit: rec, clock: clock, worker: worker, logs: logs}
}

func notFound() error {
	return &unifi.Error{Kind: unifi.ErrNotFound, Message: "surface does not serve this collection"}
}

func cloudFleet(client *fakeClient) {
	client.sites = []unifi.Site{
		{ID: "site-1", Name: "Head Office", ConsoleID: "host-a"},
		{ID: "site-2", Name: "Warehouse", ConsoleID: "host-b"},
	}
	client.devices["site-1"] = []unifi.Device{
		{ID: "dev-1", SiteID: "site-1", MAC: "aa:bb:cc:00:00:01", Name: "U6 Pro", Type: "ap", Status: "ONLINE"},
		{ID: "dev-2", SiteID: "site-1", MAC: "aa:bb:cc:00:00:02", Name: "USW Flex", Type: "switch", Status: "ONLINE"},
	}
	client.devices["site-2"] = []unifi.Device{
		{ID: "dev-3", SiteID: "site-2", MAC: "aa:bb:cc:00:00:03", Name: "UDM Pro", Type: "gateway", Status: "ONLINE"},
	}
	// The cloud surface answers neither clients nor networks (ADR-001).
	client.clientErr["site-1"] = notFound()
	client.clientErr["site-2"] = notFound()
	client.networkErr["site-1"] = notFound()
	client.networkErr["site-2"] = notFound()
}

// --- cycle -----------------------------------------------------------------

func TestRunCycleUpsertsSitesAndDevices(t *testing.T) {
	h := newHarness(t, cloudFleet)

	run := h.worker.runCycle(context.Background(), TriggerStartup)

	if run.Status != RunSuccess {
		t.Fatalf("status = %q, want %q (error: %s)", run.Status, RunSuccess, run.ErrorMessage)
	}
	if run.SitesTotal != 2 || run.SitesOK != 2 || run.SitesFailed != 0 {
		t.Fatalf("sites total/ok/failed = %d/%d/%d, want 2/2/0", run.SitesTotal, run.SitesOK, run.SitesFailed)
	}
	if run.Devices != 3 {
		t.Fatalf("devices upserted = %d, want 3", run.Devices)
	}
	if len(h.repo.applied) != 2 {
		t.Fatalf("applied snapshots = %d, want 2", len(h.repo.applied))
	}
	if got := h.repo.applied[0].ConsoleID; got != "host-a" {
		t.Fatalf("console id = %q, want %q", got, "host-a")
	}
	if got := h.repo.applied[0].Source; got != SourceCloud {
		t.Fatalf("source = %q, want %q", got, SourceCloud)
	}
	if h.repo.runs[0].Status != RunSuccess {
		t.Fatalf("recorded run status = %q, want %q", h.repo.runs[0].Status, RunSuccess)
	}
	if len(h.audit.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(h.audit.entries))
	}
	if got := h.audit.entries[0].Action; got != "sync.cycle" {
		t.Fatalf("audit action = %q, want %q", got, "sync.cycle")
	}
	if run.ID == "" || h.audit.entries[0].TargetID != run.ID {
		t.Fatalf("audit target_id = %q, want the sync_runs id %q", h.audit.entries[0].TargetID, run.ID)
	}

	status := h.worker.Status()
	if status.Stale {
		t.Fatal("cache is stale right after a successful cycle")
	}
	if status.LastSuccessfulSyncAt == nil || !status.LastSuccessfulSyncAt.Equal(run.FinishedAt) {
		t.Fatalf("last_successful_sync_at = %v, want %v", status.LastSuccessfulSyncAt, run.FinishedAt)
	}
	if !status.Reachable {
		t.Fatal("controller should be reachable after a successful health probe")
	}
	if status.Breaker != BreakerClosed {
		t.Fatalf("breaker = %q, want %q", status.Breaker, BreakerClosed)
	}
}

func TestRunCycleRecordsControllerFailure(t *testing.T) {
	h := newHarness(t, func(c *fakeClient) {
		cloudFleet(c)
		c.healthErr = &unifi.Error{Kind: unifi.ErrUnreachable, Message: "controller did not answer"}
	})

	run := h.worker.runCycle(context.Background(), TriggerSchedule)

	if run.Status != RunFailed {
		t.Fatalf("status = %q, want %q", run.Status, RunFailed)
	}
	if run.ErrorKind != string(unifi.ErrUnreachable) {
		t.Fatalf("error_kind = %q, want %q", run.ErrorKind, unifi.ErrUnreachable)
	}
	if len(h.repo.applied) != 0 {
		t.Fatalf("applied %d snapshots on a failed health probe, want 0", len(h.repo.applied))
	}

	status := h.worker.Status()
	if status.ConsecutiveFailures != 1 {
		t.Fatalf("consecutive_failures = %d, want 1", status.ConsecutiveFailures)
	}
	if status.Reachable {
		t.Fatal("controller must not be reported reachable after a failed health probe")
	}
	if status.LastSuccessfulSyncAt != nil {
		t.Fatal("a failed first cycle must not set last_successful_sync_at")
	}
	if !status.Stale {
		t.Fatal("cache must be stale while nothing has ever synced")
	}
	if status.LastErrorKind != string(unifi.ErrUnreachable) {
		t.Fatalf("last_error_kind = %q, want %q", status.LastErrorKind, unifi.ErrUnreachable)
	}
}

func TestRunCycleGoesStaleAfterTheWindow(t *testing.T) {
	h := newHarness(t, cloudFleet)
	h.worker.runCycle(context.Background(), TriggerStartup)

	if h.worker.Status().Stale {
		t.Fatal("cache should be fresh immediately after a cycle")
	}
	h.clock.advance(3*time.Minute + time.Second)
	if !h.worker.Status().Stale {
		t.Fatal("cache should be stale once SYNC_STALE_AFTER has elapsed")
	}
}

func TestRunCycleKeepsPartialSitesWhenOneFails(t *testing.T) {
	h := newHarness(t, cloudFleet)
	h.repo.applyErr["site-2"] = errors.New("site write failed")

	run := h.worker.runCycle(context.Background(), TriggerStartup)

	if run.Status != RunPartial {
		t.Fatalf("status = %q, want %q", run.Status, RunPartial)
	}
	if run.SitesOK != 1 || run.SitesFailed != 1 {
		t.Fatalf("sites ok/failed = %d/%d, want 1/1", run.SitesOK, run.SitesFailed)
	}
	// One failing site must not stall the whole cycle (ADR-003 §3).
	if run.Devices != 2 {
		t.Fatalf("devices upserted = %d, want 2", run.Devices)
	}
	if h.worker.Status().ConsecutiveFailures != 0 {
		t.Fatal("a partial cycle means the controller answered; failures must not accumulate")
	}

	// A partial cycle has to name the site and the cause somewhere. The run
	// record is sanitized on purpose, so that somewhere is the log.
	logged := h.logs.String()
	if !strings.Contains(logged, "sync: site failed") {
		t.Fatalf("no per-site failure logged; log was:\n%s", logged)
	}
	for _, want := range []string{"site_id=site-2", "site write failed"} {
		if !strings.Contains(logged, want) {
			t.Fatalf("log does not mention %q; log was:\n%s", want, logged)
		}
	}
	if strings.Contains(logged, "site_id=site-1") {
		t.Fatalf("the site that succeeded must not be logged as failed; log was:\n%s", logged)
	}
}

func TestClassifyKeepsTheSQLStateAndDropsTheDriverText(t *testing.T) {
	serverText := `relation "clients" does not exist`
	pgErr := &pgconn.PgError{Code: "42P01", Message: serverText}

	kind, message := classify(fmt.Errorf("inventory: upsert site: %w", pgErr))

	if kind != ErrKindInternal {
		t.Fatalf("kind = %q, want %q", kind, ErrKindInternal)
	}
	if message != "database error (SQLSTATE 42P01)" {
		t.Fatalf("message = %q, want the SQLSTATE form", message)
	}
	// The code is safe to persist; the server text is not guaranteed to be, and
	// it may carry row values. It stays out of the record.
	if strings.Contains(message, serverText) {
		t.Fatalf("message echoed driver text: %q", message)
	}
}

func TestClassifyLeavesNonDatabaseInternalsOpaque(t *testing.T) {
	kind, message := classify(errors.New("dial tcp 10.0.0.5:8080: connect: connection refused"))

	if kind != ErrKindInternal {
		t.Fatalf("kind = %q, want %q", kind, ErrKindInternal)
	}
	if message != "sync cycle failed: internal error" {
		t.Fatalf("message = %q, want the opaque form", message)
	}
}

func TestRunCycleToleratesAnUnsupportedCollection(t *testing.T) {
	h := newHarness(t, func(c *fakeClient) {
		c.sites = []unifi.Site{{ID: "site-1", Name: "Head Office", ConsoleID: "host-a"}}
		c.devices["site-1"] = []unifi.Device{{ID: "dev-1", SiteID: "site-1", Type: "ap"}}
		c.clientErr["site-1"] = notFound()
		c.networkErr["site-1"] = notFound()
	})

	run := h.worker.runCycle(context.Background(), TriggerStartup)

	if run.Status != RunSuccess || run.SitesOK != 1 {
		t.Fatalf("status/sites_ok = %q/%d, want success/1", run.Status, run.SitesOK)
	}
}

// --- circuit breaker -------------------------------------------------------

func TestBreakerOpensSkipsAndRecovers(t *testing.T) {
	h := newHarness(t, func(c *fakeClient) {
		cloudFleet(c)
		c.healthErr = &unifi.Error{Kind: unifi.ErrUnreachable, Message: "controller did not answer"}
	})
	ctx := context.Background()

	for i := 1; i <= breakerThreshold; i++ {
		if run := h.worker.runCycle(ctx, TriggerSchedule); run.Status != RunFailed {
			t.Fatalf("cycle %d status = %q, want %q", i, run.Status, RunFailed)
		}
	}
	if got := h.worker.Status().Breaker; got != BreakerOpen {
		t.Fatalf("breaker = %q after %d failures, want %q", got, breakerThreshold, BreakerOpen)
	}

	// While open, a tick is recorded as skipped and never reaches the controller.
	probes := h.client.healthCalls
	run := h.worker.runCycle(ctx, TriggerSchedule)
	if run.Status != RunSkipped {
		t.Fatalf("status = %q, want %q", run.Status, RunSkipped)
	}
	if h.client.healthCalls != probes {
		t.Fatal("an open breaker must not call the controller")
	}
	if h.worker.Status().Breaker != BreakerOpen {
		t.Fatal("a skipped cycle must leave the breaker open")
	}

	// After the cooldown one probe is allowed through.
	h.clock.advance(breakerCooldown)
	if run := h.worker.runCycle(ctx, TriggerSchedule); run.Status != RunFailed {
		t.Fatalf("probe status = %q, want %q", run.Status, RunFailed)
	}
	if h.worker.Status().Breaker != BreakerOpen {
		t.Fatal("a failed probe must re-open the breaker")
	}

	// A healthy controller closes it again and clears the failure count.
	h.clock.advance(breakerCooldown)
	h.client.healthErr = nil
	if run := h.worker.runCycle(ctx, TriggerSchedule); run.Status != RunSuccess {
		t.Fatalf("recovery status = %q, want %q", run.Status, RunSuccess)
	}
	status := h.worker.Status()
	if status.Breaker != BreakerClosed || status.ConsecutiveFailures != 0 {
		t.Fatalf("breaker/failures = %q/%d, want closed/0", status.Breaker, status.ConsecutiveFailures)
	}
}

// --- lifecycle -------------------------------------------------------------

// A database outage is a failure on our side: it must not suspend syncing for
// the breaker cooldown once the database is back.
func TestOwnOutageDoesNotTripTheBreaker(t *testing.T) {
	h := newHarness(t, cloudFleet)
	ctx := context.Background()
	dbDown := errors.New("connection refused")
	h.repo.applyErr["site-1"] = dbDown
	h.repo.applyErr["site-2"] = dbDown

	for i := 1; i <= breakerThreshold+1; i++ {
		run := h.worker.runCycle(ctx, TriggerSchedule)
		if run.Status != RunFailed || run.ErrorKind != ErrKindInternal {
			t.Fatalf("cycle %d = %q/%q, want %q/%q", i, run.Status, run.ErrorKind, RunFailed, ErrKindInternal)
		}
	}
	status := h.worker.Status()
	if status.Breaker != BreakerClosed || status.ConsecutiveFailures != 0 {
		t.Fatalf("breaker/failures = %q/%d, want closed/0 (the breaker protects the controller, not our database)",
			status.Breaker, status.ConsecutiveFailures)
	}

	delete(h.repo.applyErr, "site-1")
	delete(h.repo.applyErr, "site-2")
	if run := h.worker.runCycle(ctx, TriggerSchedule); run.Status != RunSuccess {
		t.Fatalf("recovery status = %q, want %q", run.Status, RunSuccess)
	}
	status = h.worker.Status()
	if status.Breaker != BreakerClosed || status.LastSuccessfulSyncAt == nil {
		t.Fatalf("breaker = %q after recovery, want a closed breaker and a fresh anchor", status.Breaker)
	}
}

// --- lifecycle -------------------------------------------------------------

func TestRunStopsCleanlyOnContextCancel(t *testing.T) {
	h := newHarness(t, cloudFleet)
	h.worker.cfg.Interval = 10 * time.Millisecond
	h.repo.recorded = make(chan SyncRun, 16)

	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.worker.Run(ctx) }()

	select {
	case <-h.repo.recorded:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("worker did not complete a cycle within 5s")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil on cancellation", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s of cancellation")
	}

	if h.worker.Status().WorkerRunning {
		t.Fatal("worker_running must be false after Run returns")
	}

	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutines before/after = %d/%d; Run leaked a goroutine", before, after)
	}
}

func TestRunSeedsFreshnessFromPreviousRuns(t *testing.T) {
	h := newHarness(t, func(c *fakeClient) {
		cloudFleet(c)
		// The controller is down at boot, but the cache is still fresh from the
		// previous process: a restart must not report a healthy cache as stale.
		c.healthErr = &unifi.Error{Kind: unifi.ErrUnreachable, Message: "controller did not answer"}
	})
	h.worker.cfg.Interval = time.Hour
	h.repo.anchor = h.clock.now()
	h.repo.anchorOK = true
	h.repo.recorded = make(chan SyncRun, 16)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.worker.Run(ctx) }()

	select {
	case <-h.repo.recorded:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not complete its startup cycle within 5s")
	}
	cancel()
	<-done

	status := h.worker.Status()
	if status.LastSuccessfulSyncAt == nil {
		t.Fatal("freshness anchor from sync_runs was not seeded")
	}
	if status.Stale {
		t.Fatal("a restart with a fresh cache must not report stale")
	}
	if status.ConsecutiveFailures != 1 {
		t.Fatalf("consecutive_failures = %d, want 1", status.ConsecutiveFailures)
	}
}

// --- redaction -------------------------------------------------------------

func TestFailureNeverLeaksTheCredential(t *testing.T) {
	// Assembled at runtime so the repository's own secret scanner does not flag
	// this test as a leaked token.
	secret := "ghp_" + strings.Repeat("A1b2C3d4", 5)
	h := newHarness(t, func(c *fakeClient) {
		cloudFleet(c)
		c.healthErr = &unifi.Error{
			Kind:    unifi.ErrUnauthorized,
			Message: "controller rejected the credential",
			Cause:   fmt.Errorf("X-API-KEY: %s", secret),
		}
	})

	run := h.worker.runCycle(context.Background(), TriggerSchedule)

	for name, got := range map[string]string{
		"error_message": run.ErrorMessage,
		"error_kind":    run.ErrorKind,
		"sync_runs row": h.repo.runs[0].ErrorMessage,
	} {
		if strings.Contains(got, secret) {
			t.Fatalf("%s leaked the credential: %q", name, got)
		}
	}
	if strings.Contains(h.logs.String(), secret) {
		t.Fatal("logs leaked the credential")
	}
}

func TestInternalErrorsAreNotEchoed(t *testing.T) {
	h := newHarness(t, cloudFleet)
	for _, site := range []string{"site-1", "site-2"} {
		h.repo.applyErr[site] = errors.New("host=db user=orchestrator password=hunter2")
	}

	run := h.worker.runCycle(context.Background(), TriggerStartup)

	if run.ErrorKind != ErrKindInternal {
		t.Fatalf("error_kind = %q, want %q", run.ErrorKind, ErrKindInternal)
	}
	if strings.Contains(run.ErrorMessage, "hunter2") {
		t.Fatalf("error_message echoed a connection string: %q", run.ErrorMessage)
	}
}

func TestRunCycleDualConsoleSiteDeduplication(t *testing.T) {
	// Regression test for ZENI-20: if ListSites returns multiple entries for the same siteID,
	// sites_total and sites_ok must reflect the number of unique sites, and the site must
	// not be double-processed.
	h := newHarness(t, func(c *fakeClient) {
		c.sites = []unifi.Site{
			{ID: "shared-site", Name: "Shared HQ", ConsoleID: "host-1"},
			{ID: "shared-site", Name: "Shared HQ", ConsoleID: "host-2"},
		}
		c.devices["shared-site"] = []unifi.Device{
			{ID: "dev-1", SiteID: "shared-site", MAC: "aa:bb:cc:00:00:01", Name: "AP 1", Type: "ap", Status: "ONLINE"},
			{ID: "dev-2", SiteID: "shared-site", MAC: "aa:bb:cc:00:00:02", Name: "UDM", Type: "gateway", Status: "ONLINE"},
			{ID: "dev-3", SiteID: "shared-site", MAC: "aa:bb:cc:00:00:03", Name: "AP 2", Type: "ap", Status: "ONLINE"},
			{ID: "dev-4", SiteID: "shared-site", MAC: "aa:bb:cc:00:00:04", Name: "Switch", Type: "switch", Status: "ONLINE"},
		}
		c.clientErr["shared-site"] = notFound()
		c.networkErr["shared-site"] = notFound()
	})

	run := h.worker.runCycle(context.Background(), TriggerStartup)

	if run.Status != RunSuccess {
		t.Fatalf("status = %q, want %q (error: %s)", run.Status, RunSuccess, run.ErrorMessage)
	}
	if run.SitesTotal != 1 || run.SitesOK != 1 || run.SitesFailed != 0 {
		t.Fatalf("sites total/ok/failed = %d/%d/%d, want 1/1/0", run.SitesTotal, run.SitesOK, run.SitesFailed)
	}
	if run.Devices != 4 {
		t.Fatalf("devices = %d, want 4", run.Devices)
	}
	if len(h.repo.applied) != 1 {
		t.Fatalf("applied sites = %d, want 1", len(h.repo.applied))
	}
}

func TestRunCycleSiteAllowlistFiltering(t *testing.T) {
	// ZENI-19: Worker must be able to scope sync to an allowlist of sites.
	h := newHarness(t, cloudFleet)
	h.worker.cfg.SiteAllowlist = []string{"site-2"} // only Warehouse

	run := h.worker.runCycle(context.Background(), TriggerStartup)

	if run.Status != RunSuccess {
		t.Fatalf("status = %q, want %q (error: %s)", run.Status, RunSuccess, run.ErrorMessage)
	}
	if run.SitesTotal != 1 || run.SitesOK != 1 || run.SitesFailed != 0 {
		t.Fatalf("sites total/ok/failed = %d/%d/%d, want 1/1/0", run.SitesTotal, run.SitesOK, run.SitesFailed)
	}
	if len(h.repo.applied) != 1 || h.repo.applied[0].Site.ID != "site-2" {
		t.Fatalf("expected only site-2 applied, got %+v", h.repo.applied)
	}
}

func TestRunCycleCollectsClientsWhenSupported(t *testing.T) {
	h := newHarness(t, func(client *fakeClient) {
		client.sites = []unifi.Site{
			{ID: "site-1", Name: "Console Site", ConsoleID: "console-1"},
		}
		client.devices["site-1"] = []unifi.Device{
			{ID: "dev-1", SiteID: "site-1", MAC: "aa:bb:cc:00:00:01", Name: "U6 Pro", Type: "ap", Status: "ONLINE"},
		}
		client.clients["site-1"] = []unifi.ClientInfo{
			{ID: "cli-1", SiteID: "site-1", MAC: "aa:bb:cc:11:22:33", Hostname: "phone", IP: "10.0.0.51", APID: "dev-1"},
			{ID: "cli-2", SiteID: "site-1", MAC: "aa:bb:cc:11:22:44", Hostname: "laptop", IP: "10.0.0.52", APID: "dev-1"},
		}
	})

	run := h.worker.runCycle(context.Background(), TriggerStartup)

	if run.Status != RunSuccess {
		t.Fatalf("status = %q, want %q (error: %s)", run.Status, RunSuccess, run.ErrorMessage)
	}
	if run.Clients != 2 {
		t.Fatalf("run.Clients = %d, want 2", run.Clients)
	}
	if len(h.repo.applied) != 1 {
		t.Fatalf("applied sites = %d, want 1", len(h.repo.applied))
	}
	snap := h.repo.applied[0]
	if !snap.HasClients {
		t.Fatal("expected snap.HasClients = true")
	}
	if len(snap.Clients) != 2 {
		t.Fatalf("len(snap.Clients) = %d, want 2", len(snap.Clients))
	}
}
