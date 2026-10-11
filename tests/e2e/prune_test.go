//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestClientEventsRetentionPrunesOnSchedule is the end-to-end acceptance test
// for ADR-005 (ZENI-21).
//
// It runs the shipped binary with a one-hour window and a one-second schedule,
// inserts client_events rows around that window *after* the API is already up,
// and then asserts the user-visible consequences: the old rows disappear, the
// recent one and the audit trail stay, and every pruning run records its
// deleted count in a structured log line.
//
// Seeding after startup is what makes this a test of the schedule rather than
// of a startup-only pass: only a later tick can see these rows.
func TestClientEventsRetentionPrunesOnSchedule(t *testing.T) {
	s := startStack(t, stackOptions{
		extraEnv: map[string]string{
			"CLIENT_EVENTS_RETENTION":      "1h",
			"CLIENT_EVENTS_PRUNE_INTERVAL": "1s",
			"LOG_LEVEL":                    "info",
		},
	})

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to the e2e database: %v", err)
	}
	t.Cleanup(pool.Close)

	// client_events carries FKs to a site and a client, so both come first.
	var siteID, clientID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO sites (unifi_site_id, console_id, name, source, synced_at)
		VALUES ('retention-site', 'console-e2e', 'Retention Site', 'console', now())
		RETURNING id::text`).Scan(&siteID); err != nil {
		t.Fatalf("seed site: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO clients (site_id, unifi_client_id, is_connected, synced_at)
		VALUES ($1::uuid, 'retention-client', true, now())
		RETURNING id::text`, siteID).Scan(&clientID); err != nil {
		t.Fatalf("seed client: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO client_events (client_id, site_id, event_type, occurred_at) VALUES
			($1::uuid, $2::uuid, 'connected',    now() - interval '2 hours'),
			($1::uuid, $2::uuid, 'disconnected', now() - interval '61 minutes'),
			($1::uuid, $2::uuid, 'connected',    now() - interval '1 minute')`,
		clientID, siteID); err != nil {
		t.Fatalf("seed client_events: %v", err)
	}

	// Old enough to fall outside any sane window, and evidence rather than
	// telemetry: ADR-005 excludes audit_log from retention.
	if _, err := pool.Exec(ctx, `
		INSERT INTO audit_log (actor_type, action, created_at)
		VALUES ('system', 'sync.cycle', now() - interval '30 days')`); err != nil {
		t.Fatalf("seed audit_log: %v", err)
	}

	s.waitFor(30*time.Second, "the retention job to delete client_events older than the configured window", func() bool {
		var old int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM client_events WHERE occurred_at < now() - interval '1 hour'`).Scan(&old); err != nil {
			return false
		}
		return old == 0
	})

	countRows := func(table string) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		return n
	}
	if got := countRows("client_events"); got != 1 {
		t.Errorf("client_events holds %d rows, want 1 (only the row inside the 1h window)", got)
	}
	if got := countRows("audit_log"); got != 1 {
		t.Errorf("audit_log holds %d rows, want 1: retention must not touch the audit trail", got)
	}

	if logs := s.logs.String(); !strings.Contains(logs, `"rows_deleted"`) {
		t.Errorf("no structured pruning line with rows_deleted in the API log:\n%s", logs)
	}
}
