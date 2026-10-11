package inventory

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The retention job talks to real Postgres. Like the other repository tests,
// these skip unless TEST_DATABASE_URL points at a throwaway database:
//
//	TEST_DATABASE_URL='postgres://orchestrator:orchestrator@localhost:5432/orchestrator?sslmode=disable' go test ./apps/api/...

// seedSiteWithClient creates the two parent rows a client_event needs, since
// client_events carries FKs to both.
func seedSiteWithClient(t *testing.T, ctx context.Context, pool *pgxpool.Pool, key string) (siteID, clientID string) {
	t.Helper()
	if err := pool.QueryRow(ctx, `
		INSERT INTO sites (unifi_site_id, console_id, name, source, synced_at)
		VALUES ($1, 'console-a', $1, 'console', now())
		RETURNING id::text`, key).Scan(&siteID); err != nil {
		t.Fatalf("seed site %s: %v", key, err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO clients (site_id, unifi_client_id, is_connected, synced_at)
		VALUES ($1::uuid, $2, true, now())
		RETURNING id::text`, siteID, key+"-client").Scan(&clientID); err != nil {
		t.Fatalf("seed client for %s: %v", key, err)
	}
	return siteID, clientID
}

func seedClientEvent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, siteID, clientID, eventType string, at time.Time) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO client_events (client_id, site_id, event_type, occurred_at)
		VALUES ($1::uuid, $2::uuid, $3, $4)`, clientID, siteID, eventType, at); err != nil {
		t.Fatalf("insert %s event at %v: %v", eventType, at, err)
	}
}

// TestPostgresRetentionDeletesOnlyRowsOlderThanTheThreshold is the acceptance
// test for ADR-005: rows older than the window go, rows at or newer than it
// stay, every site is covered, audit_log is untouched, and a second run is a
// no-op.
func TestPostgresRetentionDeletesOnlyRowsOlderThanTheThreshold(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := NewPostgresRepository(pool)

	// Fixed clock: the repository takes the cutoff as an argument, so the test
	// does not depend on wall time.
	base := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	cutoff := base.Add(-time.Hour)

	siteA, clientA := seedSiteWithClient(t, ctx, pool, "site-a")
	siteB, clientB := seedSiteWithClient(t, ctx, pool, "site-b")

	// Two sites, three rows older than the window: all of them must go. The
	// second site is what proves the job does not prune only the first one.
	seedClientEvent(t, ctx, pool, siteA, clientA, "connected", cutoff.Add(-time.Hour))
	seedClientEvent(t, ctx, pool, siteA, clientA, "disconnected", cutoff.Add(-time.Minute))
	seedClientEvent(t, ctx, pool, siteB, clientB, "connected", cutoff.Add(-2*time.Hour))
	// Exactly at the threshold, and newer than it: both must stay. "Older than
	// the threshold" is strict, so a row at the boundary is still inside it.
	seedClientEvent(t, ctx, pool, siteA, clientA, "connected", cutoff)
	seedClientEvent(t, ctx, pool, siteA, clientA, "disconnected", cutoff.Add(time.Minute))
	seedClientEvent(t, ctx, pool, siteB, clientB, "connected", base)

	// Something old that is evidence rather than telemetry: ADR-005 keeps
	// audit_log out of the retention window, so it must survive.
	if _, err := pool.Exec(ctx, `
		INSERT INTO audit_log (actor_type, action, created_at)
		VALUES ('system', 'sync.cycle', $1)`, cutoff.Add(-24*time.Hour)); err != nil {
		t.Fatalf("seed audit_log: %v", err)
	}

	deleted, err := repo.DeleteClientEventsBefore(ctx, cutoff)
	if err != nil {
		t.Fatalf("DeleteClientEventsBefore: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("deleted = %d rows, want 3 (older than the threshold, across both sites)", deleted)
	}

	// Site A keeps the boundary row and the newer one; site B keeps only the
	// newer one.
	for siteID, want := range map[string]int{siteA: 2, siteB: 1} {
		var remaining int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM client_events WHERE site_id = $1::uuid`, siteID).Scan(&remaining); err != nil {
			t.Fatalf("count client_events: %v", err)
		}
		if remaining != want {
			t.Errorf("site %s kept %d client_events rows, want %d", siteID, remaining, want)
		}
	}

	var newest time.Time
	if err := pool.QueryRow(ctx, `SELECT max(occurred_at) FROM client_events`).Scan(&newest); err != nil {
		t.Fatalf("read newest surviving event: %v", err)
	}
	if !newest.Equal(base) {
		t.Fatalf("newest surviving event = %v, want %v (the newest row must be untouched)", newest, base)
	}

	var auditRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log`).Scan(&auditRows); err != nil {
		t.Fatalf("count audit_log: %v", err)
	}
	if auditRows != 1 {
		t.Fatalf("audit_log holds %d rows, want 1: retention must not touch the audit trail", auditRows)
	}

	// Idempotent: repeating the run changes nothing and does not error.
	deletedAgain, err := repo.DeleteClientEventsBefore(ctx, cutoff)
	if err != nil {
		t.Fatalf("second DeleteClientEventsBefore: %v", err)
	}
	if deletedAgain != 0 {
		t.Fatalf("second run deleted %d rows, want 0", deletedAgain)
	}
	var afterSecondRun int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM client_events`).Scan(&afterSecondRun); err != nil {
		t.Fatalf("count client_events after the second run: %v", err)
	}
	if afterSecondRun != 3 {
		t.Fatalf("client_events holds %d rows after the second run, want 3 (unchanged)", afterSecondRun)
	}
}

// TestPostgresRetentionPrunesThroughThePruner exercises the job itself, not
// just the SQL: the cutoff it computes is what deletes the old rows.
func TestPostgresRetentionPrunesThroughThePruner(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	siteID, clientID := seedSiteWithClient(t, ctx, pool, "site-pruner")
	now := time.Now().UTC()
	seedClientEvent(t, ctx, pool, siteID, clientID, "connected", now.Add(-31*24*time.Hour))
	seedClientEvent(t, ctx, pool, siteID, clientID, "connected", now.Add(-time.Minute))

	p := NewPruner(PrunerConfig{
		Repo:      NewPostgresRepository(pool),
		Logger:    quietLogger(),
		Retention: 30 * 24 * time.Hour,
		Interval:  time.Hour,
	})

	result := p.pruneOnce(ctx)
	if result.Deleted != 1 {
		t.Fatalf("deleted = %d rows, want 1 (one row is 31 days old, the other a minute old)", result.Deleted)
	}

	var remaining int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM client_events`).Scan(&remaining); err != nil {
		t.Fatalf("count client_events: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("client_events holds %d rows, want 1", remaining)
	}
}

// TestPostgresRetentionPredicateUsesTheExistingCompositeIndex guards the index
// decision. Deleting per site is only worth it while
// client_events_site_time_idx (site_id, occurred_at DESC) can serve the
// predicate; if that stops being true, the reason to avoid an index on
// occurred_at (or a partition) has changed.
func TestPostgresRetentionPredicateUsesTheExistingCompositeIndex(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	siteID, clientID := seedSiteWithClient(t, ctx, pool, "site-plan")
	seedClientEvent(t, ctx, pool, siteID, clientID, "connected", time.Now().UTC().Add(-31*24*time.Hour))

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// A tiny table would otherwise be seq-scanned, which says nothing about
	// whether the predicate is indexable.
	if _, err := tx.Exec(ctx, "SET LOCAL enable_seqscan = off"); err != nil {
		t.Fatalf("disable seqscan: %v", err)
	}
	rows, err := tx.Query(ctx,
		"EXPLAIN SELECT 1 FROM client_events WHERE site_id = $1::uuid AND occurred_at < $2",
		siteID, time.Now().UTC())
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	var plan []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			rows.Close()
			t.Fatalf("scan plan line: %v", err)
		}
		plan = append(plan, line)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate plan: %v", err)
	}

	joined := strings.Join(plan, "\n")
	if !strings.Contains(joined, "client_events_site_time_idx") {
		t.Fatalf("the retention predicate is not served by client_events_site_time_idx:\n%s", joined)
	}
}
