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

// ClientQuery is one page request of GET /clients.
type ClientQuery struct {
	Page           int
	PageSize       int
	Sort           string
	Order          string
	SiteID         string
	Connected      *bool
	UplinkDeviceID string
	ClientType     string
	MAC            string
	SSID           string
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

// Client is a cached client as the transport exposes it. ssid and signal_dbm
// stay nil in phase 1: the Integration API does not expose them yet
// (docs/data-model.md §4.5), and the transport renders that as null rather
// than a fabricated value.
type Client struct {
	ID             string
	SiteID         string
	MAC            *string
	Hostname       *string
	IPAddress      *string
	ClientType     *string
	AccessType     *string
	UplinkDeviceID *string
	SSID           *string
	SignalDBm      *int
	IsConnected    bool
	ConnectedAt    *time.Time
	DisconnectedAt *time.Time
	FirstSeen      time.Time
	LastSeen       time.Time
	SyncedAt       time.Time
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

// ClientPage is one page of clients plus the unpaged total.
type ClientPage struct {
	Items []Client
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

	// The ssid leg is why `?ssid=` on /clients is inert rather than ignored: the
	// column is always null in phase 1, so the filter is simply never satisfied
	// (openapi/mock-expectations.md §5).
	clientsFilter = `
    ($1::uuid IS NULL OR c.site_id = $1::uuid)
AND ($2::boolean IS NULL OR c.is_connected = $2::boolean)
AND ($3::text IS NULL OR c.uplink_device_id = $3::text)
AND ($4::text IS NULL OR c.client_type = $4::text)
AND ($5::text IS NULL OR c.mac = $5::text)
AND ($6::text IS NULL OR c.ssid = $6::text)`
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

const listClientsSQL = `
SELECT c.id::text, c.site_id::text, c.mac, c.hostname, host(c.ip_address), c.client_type,
       c.access_type, c.uplink_device_id, c.ssid, c.signal_dbm, c.is_connected, c.connected_at,
       c.disconnected_at, c.first_seen, c.last_seen, c.synced_at
FROM clients c
WHERE ` + clientsFilter + `
ORDER BY %s
LIMIT $7 OFFSET $8`

const countClientsSQL = `SELECT count(*) FROM clients c WHERE ` + clientsFilter

const getClientSQL = `
SELECT c.id::text, c.site_id::text, c.mac, c.hostname, host(c.ip_address), c.client_type,
       c.access_type, c.uplink_device_id, c.ssid, c.signal_dbm, c.is_connected, c.connected_at,
       c.disconnected_at, c.first_seen, c.last_seen, c.synced_at
FROM clients c
WHERE c.id = $1::uuid`

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
	clientSortColumns = map[string]string{
		"hostname":     "c.hostname",
		"last_seen":    "c.last_seen",
		"connected_at": "c.connected_at",
		"synced_at":    "c.synced_at",
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

// ListClients returns one page of the cached clients.
func (r *PostgresRepository) ListClients(ctx context.Context, q ClientQuery) (ClientPage, error) {
	order, err := orderBy(clientSortColumns, q.Sort, q.Order, "c.id")
	if err != nil {
		return ClientPage{}, fmt.Errorf("inventory: list clients: %w", err)
	}

	tx, err := r.beginRead(ctx)
	if err != nil {
		return ClientPage{}, fmt.Errorf("inventory: list clients: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	filters := []any{
		optionalText(q.SiteID), q.Connected, optionalText(q.UplinkDeviceID),
		optionalText(q.ClientType), optionalText(q.MAC), optionalText(q.SSID),
	}
	var total int64
	if err := tx.QueryRow(ctx, countClientsSQL, filters...).Scan(&total); err != nil {
		return ClientPage{}, fmt.Errorf("inventory: count clients: %w", err)
	}

	rows, err := tx.Query(ctx, fmt.Sprintf(listClientsSQL, order),
		append(filters, q.PageSize, offsetOf(q.Page, q.PageSize))...)
	if err != nil {
		return ClientPage{}, fmt.Errorf("inventory: query clients: %w", err)
	}
	defer rows.Close()

	page := ClientPage{Items: []Client{}, Total: int(total)}
	for rows.Next() {
		var client Client
		if err := rows.Scan(&client.ID, &client.SiteID, &client.MAC, &client.Hostname,
			&client.IPAddress, &client.ClientType, &client.AccessType, &client.UplinkDeviceID,
			&client.SSID, &client.SignalDBm, &client.IsConnected, &client.ConnectedAt,
			&client.DisconnectedAt, &client.FirstSeen, &client.LastSeen, &client.SyncedAt); err != nil {
			return ClientPage{}, fmt.Errorf("inventory: scan client: %w", err)
		}
		page.Items = append(page.Items, client)
	}
	if err := rows.Err(); err != nil {
		return ClientPage{}, fmt.Errorf("inventory: read clients: %w", err)
	}
	return page, nil
}

// GetClient returns one cached client by its local id. ok is false when no row
// matches, the not_found case the transport renders as 404 (ADR-003 §5).
func (r *PostgresRepository) GetClient(ctx context.Context, id string) (Client, bool, error) {
	var client Client
	err := r.pool.QueryRow(ctx, getClientSQL, id).Scan(&client.ID, &client.SiteID, &client.MAC,
		&client.Hostname, &client.IPAddress, &client.ClientType, &client.AccessType,
		&client.UplinkDeviceID, &client.SSID, &client.SignalDBm, &client.IsConnected,
		&client.ConnectedAt, &client.DisconnectedAt, &client.FirstSeen, &client.LastSeen, &client.SyncedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return Client{}, false, nil
		}
		return Client{}, false, fmt.Errorf("inventory: get client %s: %w", id, err)
	}
	return client, true, nil
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
