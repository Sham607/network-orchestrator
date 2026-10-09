package inventory

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// The read side of the cache: GET /sites and GET /devices are served from
// Postgres and never call UniFi (docs/adr/003-architecture.md §1, ADR-004).
// The query structs mirror the parameters the contract exposes and nothing
// else, so a new cache column is never exposed by accident.

// SiteQuery is one page request of GET /sites.
type SiteQuery struct {
	Page     int
	PageSize int
	Sort     string
	Order    string
	// Query is the contract's `q`: case-insensitive substring of the name.
	Query string
}

// DeviceQuery is one page request of GET /devices.
type DeviceQuery struct {
	Page       int
	PageSize   int
	Sort       string
	Order      string
	SiteID     string
	Status     string
	DeviceType string
	MAC        string
}

// Site is a cached site as the transport exposes it. The raw UniFi payload,
// the UniFi natural keys and the console are deliberately absent: the API
// exposes the control plane's own id, not UniFi's (ADR-003 §2, §5).
type Site struct {
	ID          string
	Name        string
	Description *string
	Timezone    *string
	GatewayMAC  *string
	SyncedAt    time.Time
}

// Device is a cached device as the transport exposes it. Fields the cache does
// not hold yet stay nil, which the contract renders as an explicit null rather
// than a value invented by the transport.
type Device struct {
	ID                string
	SiteID            string
	Name              *string
	MAC               *string
	Model             *string
	DeviceType        string
	Status            *string
	FirmwareVersion   *string
	FirmwareUpdatable *bool
	IPAddress         *string
	AdoptedAt         *time.Time
	ProvisionedAt     *time.Time
	UplinkDeviceID    *string
	Supported         *bool
	UptimeSec         *int64
	SyncedAt          time.Time
}

// SitePage is one page of sites plus the unpaged total the contract reports.
type SitePage struct {
	Items []Site
	Total int
}

// DevicePage is one page of devices plus the unpaged total.
type DevicePage struct {
	Items []Device
	Total int
}

// The filter clause is shared by the page query and the count query so the two
// can never drift. An empty filter is bound as NULL, which is how one static
// statement serves every combination without building SQL from input.
const (
	sitesFilter = `($1::text IS NULL OR position(lower($1::text) IN lower(s.name)) > 0)`

	devicesFilter = `
    ($1::uuid IS NULL OR d.site_id = $1::uuid)
AND ($2::text IS NULL OR d.status = $2::text)
AND ($3::text IS NULL OR d.device_type = $3::text)
AND ($4::text IS NULL OR d.mac = $4::text)`
)

const listSitesSQL = `
SELECT s.id::text, s.name, s.description, s.timezone, s.gateway_mac, s.synced_at
FROM sites s
WHERE ` + sitesFilter + `
ORDER BY %s
LIMIT $2 OFFSET $3`

const countSitesSQL = `SELECT count(*) FROM sites s WHERE ` + sitesFilter

// devicesFilter is indented for this statement; both queries reindent it by
// starting their WHERE on a new line.
const listDevicesSQL = `
SELECT d.id::text, d.site_id::text, d.name, d.mac, d.model,
       COALESCE(d.device_type, 'other'), d.status, d.firmware_version,
       d.firmware_updatable, host(d.ip_address), d.adopted_at, d.provisioned_at,
       d.uplink_device_id, d.supported, d.uptime_sec, d.synced_at
FROM devices d
WHERE ` + devicesFilter + `
ORDER BY %s
LIMIT $5 OFFSET $6`

const countDevicesSQL = `SELECT count(*) FROM devices d WHERE ` + devicesFilter

// sortColumns whitelist the sort fields each endpoint declares. The transport
// validates the enum first; the map is what keeps user input out of the SQL
// text, and an unknown field is an error rather than a silent fallback.
var (
	siteSortColumns = map[string]string{
		"name":      "s.name",
		"synced_at": "s.synced_at",
	}
	deviceSortColumns = map[string]string{
		"name":       "d.name",
		"model":      "d.model",
		"status":     "d.status",
		"adopted_at": "d.adopted_at",
		"synced_at":  "d.synced_at",
	}
)

// ListSites returns one page of the cached sites.
func (r *PostgresRepository) ListSites(ctx context.Context, q SiteQuery) (SitePage, error) {
	order, err := orderBy(siteSortColumns, q.Sort, q.Order, "s.id")
	if err != nil {
		return SitePage{}, fmt.Errorf("inventory: list sites: %w", err)
	}

	tx, err := r.beginRead(ctx)
	if err != nil {
		return SitePage{}, fmt.Errorf("inventory: list sites: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	filter := optionalText(q.Query)
	var total int64
	if err := tx.QueryRow(ctx, countSitesSQL, filter).Scan(&total); err != nil {
		return SitePage{}, fmt.Errorf("inventory: count sites: %w", err)
	}

	rows, err := tx.Query(ctx, fmt.Sprintf(listSitesSQL, order), filter, q.PageSize, offsetOf(q.Page, q.PageSize))
	if err != nil {
		return SitePage{}, fmt.Errorf("inventory: query sites: %w", err)
	}
	defer rows.Close()

	page := SitePage{Items: []Site{}, Total: int(total)}
	for rows.Next() {
		var site Site
		if err := rows.Scan(&site.ID, &site.Name, &site.Description, &site.Timezone,
			&site.GatewayMAC, &site.SyncedAt); err != nil {
			return SitePage{}, fmt.Errorf("inventory: scan site: %w", err)
		}
		page.Items = append(page.Items, site)
	}
	if err := rows.Err(); err != nil {
		return SitePage{}, fmt.Errorf("inventory: read sites: %w", err)
	}
	return page, nil
}

// ListDevices returns one page of the cached devices.
func (r *PostgresRepository) ListDevices(ctx context.Context, q DeviceQuery) (DevicePage, error) {
	order, err := orderBy(deviceSortColumns, q.Sort, q.Order, "d.id")
	if err != nil {
		return DevicePage{}, fmt.Errorf("inventory: list devices: %w", err)
	}

	tx, err := r.beginRead(ctx)
	if err != nil {
		return DevicePage{}, fmt.Errorf("inventory: list devices: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	filters := []any{optionalText(q.SiteID), optionalText(q.Status), optionalText(q.DeviceType), optionalText(q.MAC)}
	var total int64
	if err := tx.QueryRow(ctx, countDevicesSQL, filters...).Scan(&total); err != nil {
		return DevicePage{}, fmt.Errorf("inventory: count devices: %w", err)
	}

	rows, err := tx.Query(ctx, fmt.Sprintf(listDevicesSQL, order),
		append(filters, q.PageSize, offsetOf(q.Page, q.PageSize))...)
	if err != nil {
		return DevicePage{}, fmt.Errorf("inventory: query devices: %w", err)
	}
	defer rows.Close()

	page := DevicePage{Items: []Device{}, Total: int(total)}
	for rows.Next() {
		var device Device
		if err := rows.Scan(&device.ID, &device.SiteID, &device.Name, &device.MAC,
			&device.Model, &device.DeviceType, &device.Status, &device.FirmwareVersion,
			&device.FirmwareUpdatable, &device.IPAddress, &device.AdoptedAt,
			&device.ProvisionedAt, &device.UplinkDeviceID, &device.Supported,
			&device.UptimeSec, &device.SyncedAt); err != nil {
			return DevicePage{}, fmt.Errorf("inventory: scan device: %w", err)
		}
		page.Items = append(page.Items, device)
	}
	if err := rows.Err(); err != nil {
		return DevicePage{}, fmt.Errorf("inventory: read devices: %w", err)
	}
	return page, nil
}

// beginRead opens the read-only, repeatable-read transaction a page is served
// from. The count and the page must describe the same snapshot: the cache is
// being rewritten by a background cycle, and READ COMMITTED would let the two
// statements see different moments of it.
func (r *PostgresRepository) beginRead(ctx context.Context) (pgx.Tx, error) {
	return r.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
}

// orderBy turns the contract's sort/order pair into an ORDER BY fragment. The
// id tiebreaker keeps paging stable while several rows share a sort value.
func orderBy(columns map[string]string, sort, order, tieBreak string) (string, error) {
	column, ok := columns[sort]
	if !ok {
		return "", fmt.Errorf("unsupported sort field %q", sort)
	}
	switch order {
	case "asc":
		return column + " ASC, " + tieBreak, nil
	case "desc":
		return column + " DESC, " + tieBreak, nil
	default:
		return "", fmt.Errorf("unsupported sort order %q", order)
	}
}

// offsetOf converts the 1-based page of the contract into a row offset. Either
// value is already validated by the transport (page >= 1, 1 <= page_size <=
// 200), so the product cannot overflow.
func offsetOf(page, pageSize int) int64 {
	return int64(page-1) * int64(pageSize)
}
