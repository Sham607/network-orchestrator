package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Sham607/network-orchestrator/apps/api/internal/audit"
	"github.com/Sham607/network-orchestrator/apps/api/internal/unifi"
)

// The repository talks to real Postgres. CI has no database service, so these
// tests skip unless TEST_DATABASE_URL is set; the harness that always runs them
// belongs to ZENI-16. Run them with:
//
//	docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=orchestrator \
//	  -e POSTGRES_USER=orchestrator -e POSTGRES_DB=orchestrator postgres:16-alpine
//	TEST_DATABASE_URL='postgres://orchestrator:orchestrator@localhost:5432/orchestrator?sslmode=disable' go test ./...
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping the real-Postgres repository test")
	}

	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect for setup: %v", err)
	}
	schema := fmt.Sprintf("inventory_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}

	pool, err := pgxpool.New(ctx, withSearchPath(dsn, schema))
	if err != nil {
		admin.Close()
		t.Fatalf("connect to %s: %v", schema, err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})

	if _, err := pool.Exec(ctx, migrationUp(t), pgx.QueryExecModeSimpleProtocol); err != nil {
		t.Fatalf("apply migrations/0001_init.sql: %v", err)
	}
	return pool
}

// withSearchPath scopes the pool to one throwaway schema so a developer's real
// database is never touched.
func withSearchPath(dsn, schema string) string {
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + "search_path=" + schema
}

func migrationUp(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", "migrations", "0001_init.sql")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	_, after, ok := strings.Cut(body, "-- +goose Up")
	if !ok {
		t.Fatalf("%s has no '-- +goose Up' marker", path)
	}
	up, _, _ := strings.Cut(after, "-- +goose Down")
	return up
}

func sampleSnapshot(at time.Time) SiteSnapshot {
	return SiteSnapshot{
		Site:      unifi.Site{ID: "site-1", Name: "Head Office", Desc: "HQ", ConsoleID: "host-a"},
		ConsoleID: "host-a",
		Source:    SourceCloud,
		SyncedAt:  at,
		Devices: []unifi.Device{{
			ID: "dev-1", SiteID: "site-1", MAC: "aa:bb:cc:00:00:01", Name: "U6 Pro",
			Model: "U6 Pro", Type: "ap", Status: "ONLINE", Firmware: "6.6.55",
			IP: "10.0.0.11", UptimeSec: 3600,
		}},
		Clients: []unifi.ClientInfo{{
			ID: "cli-1", SiteID: "site-1", MAC: "aa:bb:cc:00:00:99",
			Hostname: "laptop", IP: "10.0.0.50", APID: "dev-1",
		}},
		Networks: []unifi.Network{{
			ID: "net-1", SiteID: "site-1", Name: "Default", VLAN: 1, Enabled: true,
		}},
	}
}

func TestPostgresApplySiteIsIdempotent(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := NewPostgresRepository(pool)

	first := time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC)
	snap := sampleSnapshot(first)
	if err := repo.ApplySite(ctx, snap); err != nil {
		t.Fatalf("first ApplySite: %v", err)
	}

	// A second cycle with a renamed device must update in place, not duplicate.
	second := first.Add(60 * time.Second)
	updated := sampleSnapshot(second)
	updated.Devices[0].Name = "U6 Pro (renamed)"
	if err := repo.ApplySite(ctx, updated); err != nil {
		t.Fatalf("second ApplySite: %v", err)
	}

	var sites, devices, clients, networks int
	for table, dest := range map[string]*int{
		"sites": &sites, "devices": &devices, "clients": &clients, "networks": &networks,
	} {
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(dest); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
	}
	if sites != 1 || devices != 1 || clients != 1 || networks != 1 {
		t.Fatalf("row counts = sites %d, devices %d, clients %d, networks %d; want 1 of each (upsert, not insert)",
			sites, devices, clients, networks)
	}

	var (
		consoleID string
		name      string
		raw       []byte
	)
	if err := pool.QueryRow(ctx, `SELECT console_id, name, raw FROM sites`).Scan(&consoleID, &name, &raw); err != nil {
		t.Fatalf("read site: %v", err)
	}
	if consoleID != "host-a" {
		t.Fatalf("console_id = %q, want %q", consoleID, "host-a")
	}
	if name != "Head Office" {
		t.Fatalf("site name = %q, want %q", name, "Head Office")
	}
	if !json.Valid(raw) {
		t.Fatalf("raw is not valid JSON: %q", raw)
	}

	var deviceName, ip string
	if err := pool.QueryRow(ctx, `SELECT name, host(ip_address) FROM devices`).Scan(&deviceName, &ip); err != nil {
		t.Fatalf("read device: %v", err)
	}
	if deviceName != "U6 Pro (renamed)" {
		t.Fatalf("device name = %q, want the updated name", deviceName)
	}
	if ip != "10.0.0.11" {
		t.Fatalf("device ip_address = %q, want %q", ip, "10.0.0.11")
	}

	// first_seen records when the client was first observed; last_seen follows
	// the newest cycle.
	var firstSeen, lastSeen time.Time
	if err := pool.QueryRow(ctx, `SELECT first_seen, last_seen FROM clients`).Scan(&firstSeen, &lastSeen); err != nil {
		t.Fatalf("read client: %v", err)
	}
	if !firstSeen.Equal(first) {
		t.Fatalf("first_seen = %v, want %v (must not be overwritten)", firstSeen, first)
	}
	if !lastSeen.Equal(second) {
		t.Fatalf("last_seen = %v, want %v", lastSeen, second)
	}

	// NULL placeholders stay NULL rather than becoming "" or 0.
	var ssid *string
	var signal *int
	if err := pool.QueryRow(ctx, `SELECT ssid, signal_dbm FROM clients`).Scan(&ssid, &signal); err != nil {
		t.Fatalf("read client placeholders: %v", err)
	}
	if ssid != nil || signal != nil {
		t.Fatalf("ssid/signal_dbm = %v/%v, want NULL for fields v1 does not expose", ssid, signal)
	}
}

func TestPostgresApplySiteRejectsRowsWithoutANaturalKey(t *testing.T) {
	pool := testPool(t)
	repo := NewPostgresRepository(pool)

	cases := map[string]SiteSnapshot{
		"no unifi_site_id": {Site: unifi.Site{Name: "x", ConsoleID: "host-a"}, Source: SourceCloud, SyncedAt: time.Now()},
		"no name":          {Site: unifi.Site{ID: "s", ConsoleID: "host-a"}, Source: SourceCloud, SyncedAt: time.Now()},
		"no console_id":    {Site: unifi.Site{ID: "s", Name: "x"}, Source: SourceCloud, SyncedAt: time.Now()},
		"no source":        {Site: unifi.Site{ID: "s", Name: "x", ConsoleID: "host-a"}, SyncedAt: time.Now()},
	}
	for name, snap := range cases {
		if err := repo.ApplySite(context.Background(), snap); err == nil {
			t.Errorf("%s: ApplySite returned nil, want an error", name)
		}
	}
}

func TestPostgresRecordsRunsAndFreshnessAnchor(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := NewPostgresRepository(pool)

	if _, ok, err := repo.LastSuccessfulSyncAt(ctx); err != nil || ok {
		t.Fatalf("freshness anchor on an empty table = %v/%v, want no anchor and no error", ok, err)
	}

	started := time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC)
	failed := SyncRun{
		Surface: SourceCloud, Trigger: TriggerStartup, Status: RunFailed,
		StartedAt: started, FinishedAt: started.Add(time.Second), DurationMS: 1000,
		ErrorKind: string(unifi.ErrUnreachable), ErrorMessage: "controller did not answer",
	}
	if _, err := repo.RecordSyncRun(ctx, failed); err != nil {
		t.Fatalf("RecordSyncRun(failed): %v", err)
	}
	if _, ok, err := repo.LastSuccessfulSyncAt(ctx); err != nil || ok {
		t.Fatalf("a failed run must not become the freshness anchor (ok=%v err=%v)", ok, err)
	}

	success := failed
	success.Status = RunSuccess
	success.ErrorKind = ""
	success.ErrorMessage = ""
	success.StartedAt = started.Add(time.Minute)
	success.FinishedAt = success.StartedAt.Add(2 * time.Second)
	success.DurationMS = 2000
	success.SitesTotal, success.SitesOK = 21, 21
	success.Devices = 120
	id, err := repo.RecordSyncRun(ctx, success)
	if err != nil {
		t.Fatalf("RecordSyncRun(success): %v", err)
	}
	if id == "" {
		t.Fatal("RecordSyncRun returned an empty id")
	}

	anchor, ok, err := repo.LastSuccessfulSyncAt(ctx)
	if err != nil || !ok {
		t.Fatalf("LastSuccessfulSyncAt = %v/%v, want an anchor", ok, err)
	}
	if !anchor.Equal(success.FinishedAt) {
		t.Fatalf("anchor = %v, want %v", anchor, success.FinishedAt)
	}

	var storedStatus string
	var storedError *string
	if err := pool.QueryRow(ctx, `SELECT status, error_kind FROM sync_runs WHERE id = $1::uuid`, id).Scan(&storedStatus, &storedError); err != nil {
		t.Fatalf("read sync_run: %v", err)
	}
	if storedStatus != RunSuccess || storedError != nil {
		t.Fatalf("stored run = %q/%v, want success with a NULL error_kind", storedStatus, storedError)
	}
}

// TestWorkerFillsTheCacheAgainstPostgres is the acceptance test for the sync
// worker: a cycle against a real database fills sites and devices, records the
// run, writes the audit entry and moves the freshness anchor.
func TestWorkerFillsTheCacheAgainstPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	h := newHarness(t, cloudFleet)
	worker := NewWorker(WorkerConfig{
		Client:     h.client,
		Repo:       NewPostgresRepository(pool),
		Audit:      audit.NewPostgresRecorder(pool),
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Surface:    SourceCloud,
		Interval:   60 * time.Second,
		StaleAfter: 3 * time.Minute,
	})

	first := worker.runCycle(ctx, TriggerStartup)
	if first.Status != RunSuccess {
		t.Fatalf("cycle status = %q, want %q (%s)", first.Status, RunSuccess, first.ErrorMessage)
	}
	if first.ID == "" {
		t.Fatal("cycle was not recorded in sync_runs")
	}

	var siteCount, deviceCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sites`).Scan(&siteCount); err != nil {
		t.Fatalf("count sites: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM devices`).Scan(&deviceCount); err != nil {
		t.Fatalf("count devices: %v", err)
	}
	if siteCount != 2 || deviceCount != 3 {
		t.Fatalf("cache holds %d sites and %d devices, want 2 and 3", siteCount, deviceCount)
	}

	var consoles []string
	rows, err := pool.Query(ctx, `SELECT DISTINCT console_id FROM sites ORDER BY console_id`)
	if err != nil {
		t.Fatalf("read console ids: %v", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan console id: %v", err)
		}
		consoles = append(consoles, id)
	}
	rows.Close()
	if len(consoles) != 2 || consoles[0] != "host-a" || consoles[1] != "host-b" {
		t.Fatalf("console ids = %v, want [host-a host-b]", consoles)
	}

	var (
		action   string
		actor    string
		targetID string
	)
	if err := pool.QueryRow(ctx, `SELECT action, actor_type, target_id FROM audit_log`).Scan(&action, &actor, &targetID); err != nil {
		t.Fatalf("read audit_log: %v", err)
	}
	if action != "sync.cycle" || actor != "system" || targetID != first.ID {
		t.Fatalf("audit entry = %q/%q/%q, want sync.cycle/system/%s", action, actor, targetID, first.ID)
	}

	// The freshness anchor moves with the second cycle, which is what makes
	// /ready and /sync/status observe progress.
	time.Sleep(10 * time.Millisecond)
	second := worker.runCycle(ctx, TriggerSchedule)
	if second.Status != RunSuccess {
		t.Fatalf("second cycle status = %q, want %q", second.Status, RunSuccess)
	}
	anchor, ok, err := NewPostgresRepository(pool).LastSuccessfulSyncAt(ctx)
	if err != nil || !ok {
		t.Fatalf("LastSuccessfulSyncAt = %v/%v, want an anchor", ok, err)
	}
	if !anchor.After(first.FinishedAt) {
		t.Fatalf("anchor = %v, want it after the first cycle at %v", anchor, first.FinishedAt)
	}

	var runCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sync_runs`).Scan(&runCount); err != nil {
		t.Fatalf("count sync_runs: %v", err)
	}
	if runCount != 2 {
		t.Fatalf("sync_runs rows = %d, want one row per cycle", runCount)
	}
}

// TestWorkerSyncsThroughTheRealAdapterIntoPostgres exercises the whole write
// path in one go: HTTP → adapter → worker → repository → Postgres, using the
// adapter's own recorded Site Manager payloads.
func TestWorkerSyncsThroughTheRealAdapterIntoPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	payloads := map[string][]byte{
		"/v1/hosts":   readFixture(t, "cloud_hosts.json"),
		"/v1/sites":   readFixture(t, "cloud_sites.json"),
		"/v1/devices": readFixture(t, "cloud_devices.json"),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := payloads[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	client, err := unifi.NewCloudClient(unifi.Config{
		APIKey:       "not-a-real-key",
		CloudBaseURL: srv.URL,
		HTTPClient:   srv.Client(),
	})
	if err != nil {
		t.Fatalf("NewCloudClient: %v", err)
	}

	worker := NewWorker(WorkerConfig{
		Client:     client,
		Repo:       NewPostgresRepository(pool),
		Audit:      audit.NewPostgresRecorder(pool),
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Surface:    SourceCloud,
		Interval:   60 * time.Second,
		StaleAfter: 3 * time.Minute,
	})

	run := worker.runCycle(ctx, TriggerStartup)
	if run.Status != RunSuccess {
		t.Fatalf("cycle status = %q, want %q (%s: %s)", run.Status, RunSuccess, run.ErrorKind, run.ErrorMessage)
	}
	if run.SitesTotal != 2 || run.Devices != 3 {
		t.Fatalf("cycle wrote %d sites and %d devices, want 2 and 3", run.SitesTotal, run.Devices)
	}

	var consoleID, name string
	if err := pool.QueryRow(ctx, `SELECT console_id, name FROM sites WHERE unifi_site_id = 'site-1'`).Scan(&consoleID, &name); err != nil {
		t.Fatalf("read site-1: %v", err)
	}
	if consoleID != "console-1" || name != "default" {
		t.Fatalf("site-1 = %q/%q, want console-1/default", consoleID, name)
	}

	rows, err := pool.Query(ctx, `
		SELECT s.unifi_site_id, d.unifi_device_id, d.device_type, host(d.ip_address)
		FROM devices d JOIN sites s ON s.id = d.site_id
		ORDER BY d.unifi_device_id`)
	if err != nil {
		t.Fatalf("read devices: %v", err)
	}
	defer rows.Close()

	type deviceRow struct{ site, id, kind, ip string }
	var got []deviceRow
	for rows.Next() {
		var r deviceRow
		var ip *string
		if err := rows.Scan(&r.site, &r.id, &r.kind, &ip); err != nil {
			t.Fatalf("scan device: %v", err)
		}
		if ip != nil {
			r.ip = *ip
		}
		got = append(got, r)
	}
	want := []deviceRow{
		{"site-1", "dev-1", "ap", "10.0.0.11"},
		{"site-1", "dev-2", "gateway", "10.0.0.1"},
		{"site-2", "dev-3", "switch", ""},
	}
	if len(got) != len(want) {
		t.Fatalf("devices = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("device %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	status := worker.Status()
	if status.LastSuccessfulSyncAt == nil || status.Stale || !status.Reachable {
		t.Fatalf("status = %+v, want a fresh, reachable cache", status)
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("..", "unifi", "testdata", name)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return body
}
