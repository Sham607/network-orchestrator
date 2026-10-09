package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository is the cache store the worker writes through. Implementations must
// be idempotent: a cycle may be retried or replayed without duplicating rows.
type Repository interface {
	// ApplySite upserts one site and its children in a single transaction.
	ApplySite(ctx context.Context, snap SiteSnapshot) error
	// RecordSyncRun appends the cycle to sync_runs and returns its id.
	RecordSyncRun(ctx context.Context, run SyncRun) (string, error)
	// LastSuccessfulSyncAt returns the newest finished_at of a cycle that
	// refreshed the cache. ok is false when no cycle ever succeeded.
	LastSuccessfulSyncAt(ctx context.Context) (ts time.Time, ok bool, err error)
}

// PostgresRepository is the pgx-backed Repository.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

// NewPostgresRepository returns a Repository backed by pool.
func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

// upsertSite keeps the natural key stable and refreshes the mutable columns.
// name is NOT NULL: an unnamed site keeps its previous name instead of failing
// the cycle.
const upsertSite = `
INSERT INTO sites (unifi_site_id, console_id, sm_site_id, name, description, source, raw, synced_at)
VALUES ($1, $2, NULLIF($3, ''), $4, NULLIF($5, ''), $6, $7, $8)
ON CONFLICT (unifi_site_id) DO UPDATE SET
    console_id  = EXCLUDED.console_id,
    sm_site_id  = EXCLUDED.sm_site_id,
    name        = EXCLUDED.name,
    description = EXCLUDED.description,
    source      = EXCLUDED.source,
    raw         = EXCLUDED.raw,
    synced_at   = EXCLUDED.synced_at
RETURNING id::text`

const upsertDevice = `
INSERT INTO devices (
    site_id, unifi_device_id, mac, name, model, device_type, status,
    firmware_version, ip_address, adopted_at, uptime_sec, source, raw, synced_at
) VALUES (
    $1, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''), NULLIF($7, ''),
    NULLIF($8, ''), NULLIF($9::text, '')::inet, $10, $11, $12, $13, $14
)
ON CONFLICT (site_id, unifi_device_id) DO UPDATE SET
    mac              = EXCLUDED.mac,
    name             = EXCLUDED.name,
    model            = EXCLUDED.model,
    device_type      = EXCLUDED.device_type,
    status           = EXCLUDED.status,
    firmware_version = EXCLUDED.firmware_version,
    ip_address       = EXCLUDED.ip_address,
    adopted_at       = EXCLUDED.adopted_at,
    uptime_sec       = EXCLUDED.uptime_sec,
    source           = EXCLUDED.source,
    raw              = EXCLUDED.raw,
    synced_at        = EXCLUDED.synced_at`

// first_seen is preserved on conflict: the cache tracks when a client was first
// observed, not when it was last refreshed.
const upsertClient = `
INSERT INTO clients (
    site_id, unifi_client_id, mac, hostname, ip_address, uplink_device_id,
    ssid, signal_dbm, is_connected, connected_at, first_seen, last_seen,
    source, raw, synced_at
) VALUES (
    $1, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5::text, '')::inet, NULLIF($6, ''),
    NULLIF($7, ''), $8, true, $9, $10, $10, $11, $12, $10
)
ON CONFLICT (site_id, unifi_client_id) DO UPDATE SET
    mac              = EXCLUDED.mac,
    hostname         = EXCLUDED.hostname,
    ip_address       = EXCLUDED.ip_address,
    uplink_device_id = EXCLUDED.uplink_device_id,
    ssid             = EXCLUDED.ssid,
    signal_dbm       = EXCLUDED.signal_dbm,
    is_connected     = true,
    connected_at     = EXCLUDED.connected_at,
    disconnected_at  = NULL,
    last_seen        = EXCLUDED.last_seen,
    source           = EXCLUDED.source,
    raw              = EXCLUDED.raw,
    synced_at        = EXCLUDED.synced_at`

const upsertNetwork = `
INSERT INTO networks (
    site_id, unifi_network_id, name, vlan_id, enabled, source, raw, synced_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (site_id, unifi_network_id) DO UPDATE SET
    name       = EXCLUDED.name,
    vlan_id    = EXCLUDED.vlan_id,
    enabled    = EXCLUDED.enabled,
    source     = EXCLUDED.source,
    raw        = EXCLUDED.raw,
    synced_at  = EXCLUDED.synced_at`

const insertSyncRun = `
INSERT INTO sync_runs (
    surface, "trigger", started_at, finished_at, duration_ms, status,
    sites_total, sites_ok, sites_failed, devices_upserted, clients_upserted,
    networks_upserted, rate_limited, error_kind, error_message
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
    NULLIF($14, ''), NULLIF($15, '')
)
RETURNING id::text`

const selectLastSuccessfulSync = `
SELECT finished_at
FROM sync_runs
WHERE status IN ('success', 'partial') AND finished_at IS NOT NULL
ORDER BY finished_at DESC
LIMIT 1`

// ApplySite upserts the site and every child row it carries.
func (r *PostgresRepository) ApplySite(ctx context.Context, snap SiteSnapshot) error {
	site := snap.Site
	if strings.TrimSpace(site.ID) == "" {
		return fmt.Errorf("inventory: site has no unifi_site_id")
	}
	name := strings.TrimSpace(site.Name)
	if name == "" {
		return fmt.Errorf("inventory: site %s has no name", site.ID)
	}
	consoleID := strings.TrimSpace(snap.ConsoleID)
	if consoleID == "" {
		consoleID = strings.TrimSpace(site.ConsoleID)
	}
	if consoleID == "" {
		return fmt.Errorf("inventory: site %s has no console id", site.ID)
	}
	if strings.TrimSpace(snap.Source) == "" {
		return fmt.Errorf("inventory: site %s has no source", site.ID)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("inventory: begin site tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var siteID string
	if err := tx.QueryRow(ctx, upsertSite,
		site.ID, consoleID, site.ID, name, strings.TrimSpace(site.Desc),
		snap.Source, jsonPayload(site.Raw), snap.SyncedAt,
	).Scan(&siteID); err != nil {
		return fmt.Errorf("inventory: upsert site %s: %w", site.ID, err)
	}

	for _, d := range snap.Devices {
		if strings.TrimSpace(d.ID) == "" {
			continue
		}
		if _, err := tx.Exec(ctx, upsertDevice,
			siteID, d.ID, d.MAC, d.Name, d.Model, d.Type, d.Status,
			d.Firmware, optionalText(d.IP), optionalTime(d.AdoptedAt), optionalUptime(d.UptimeSec),
			snap.Source, jsonPayload(d.Raw), snap.SyncedAt,
		); err != nil {
			return fmt.Errorf("inventory: upsert device %s: %w", d.ID, err)
		}
	}

	for _, c := range snap.Clients {
		if strings.TrimSpace(c.ID) == "" {
			continue
		}
		if _, err := tx.Exec(ctx, upsertClient,
			siteID, c.ID, c.MAC, c.Hostname, optionalText(c.IP), c.APID,
			c.SSID, optionalSignal(c.SignalDBm), optionalTime(c.ConnectedAt),
			snap.SyncedAt, snap.Source, jsonPayload(c.Raw),
		); err != nil {
			return fmt.Errorf("inventory: upsert client %s: %w", c.ID, err)
		}
	}

	for _, n := range snap.Networks {
		if strings.TrimSpace(n.ID) == "" || strings.TrimSpace(n.Name) == "" {
			continue
		}
		if _, err := tx.Exec(ctx, upsertNetwork,
			siteID, n.ID, n.Name, optionalVLAN(n.VLAN), n.Enabled,
			snap.Source, jsonPayload(n.Raw), snap.SyncedAt,
		); err != nil {
			return fmt.Errorf("inventory: upsert network %s: %w", n.ID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("inventory: commit site %s: %w", site.ID, err)
	}
	return nil
}

// RecordSyncRun appends the cycle row.
func (r *PostgresRepository) RecordSyncRun(ctx context.Context, run SyncRun) (string, error) {
	finished := run.FinishedAt
	if finished.IsZero() {
		finished = time.Now().UTC()
	}
	var id string
	if err := r.pool.QueryRow(ctx, insertSyncRun,
		run.Surface, run.Trigger, run.StartedAt, finished, run.DurationMS, run.Status,
		run.SitesTotal, run.SitesOK, run.SitesFailed, run.Devices, run.Clients,
		run.Networks, run.RateLimited, run.ErrorKind, run.ErrorMessage,
	).Scan(&id); err != nil {
		return "", fmt.Errorf("inventory: insert sync_run: %w", err)
	}
	return id, nil
}

// LastSuccessfulSyncAt reports the freshness anchor for /ready.
func (r *PostgresRepository) LastSuccessfulSyncAt(ctx context.Context) (time.Time, bool, error) {
	var ts time.Time
	err := r.pool.QueryRow(ctx, selectLastSuccessfulSync).Scan(&ts)
	if err != nil {
		if err == pgx.ErrNoRows {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, fmt.Errorf("inventory: last successful sync: %w", err)
	}
	return ts, true, nil
}

// jsonPayload keeps the NOT NULL DEFAULT '{}' contract even when the adapter
// carried no raw body.
func jsonPayload(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return []byte("{}")
	}
	return raw
}

// optionalText returns nil for a blank string so the column stays NULL rather
// than holding "".
func optionalText(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func optionalTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

// optionalUptime treats 0 as "not reported": the Integration API does not
// expose uptime (docs/data-model.md, unverified fields).
func optionalUptime(v int64) any {
	if v <= 0 {
		return nil
	}
	return v
}

func optionalSignal(v int) any {
	if v == 0 {
		return nil
	}
	return int32(v)
}

func optionalVLAN(v int) any {
	if v <= 0 {
		return nil
	}
	return int32(v)
}
