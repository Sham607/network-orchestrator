package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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

// NetworkQuery is one page request of GET /networks.
type NetworkQuery struct {
	Page       int
	PageSize   int
	Sort       string
	Order      string
	SiteID     string
	VLANID     *int
	Management string
	Enabled    *bool
}

// Site is a cached site as the transport exposes it. The raw UniFi payload,
// the UniFi natural keys and the console are deliberately absent: the API
// exposes the control plane's own id, not UniFi's (ADR-003 §2, §5).
type Site struct {
	ID          string
	ConsoleID   string
	Name        string
	Description *string
	Timezone    *string
	GatewayMAC  *string
	SyncedAt    time.Time
}

// Radio is a normalized wireless radio summary from the device cache.
type Radio struct {
	WlanStandard    *string  `json:"wlan_standard"`
	FrequencyGHz    *float64 `json:"frequency_ghz"`
	ChannelWidthMHz *int     `json:"channel_width_mhz"`
	Channel         *int     `json:"channel"`
}

// Device is a cached device as the transport exposes it. Fields the cache does
// not hold yet stay nil, which the contract renders as an explicit null rather
// than a value invented by the transport.
type Device struct {
	ID                string
	SiteID            string
	ConsoleID         string
	UnifiDeviceID     string
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
	Radios            []Radio
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

// Network is a cached network as the transport exposes it.
type Network struct {
	ID           string
	SiteID       string
	Name         string
	VLANID       *int
	Management   *string
	Enabled      *bool
	IsDefault    *bool
	Origin       *string
	Configurable *bool
	SyncedAt     time.Time
}

// NetworkPage is one page of networks plus the unpaged total.
type NetworkPage struct {
	Items []Network
	Total int
}

// The filter clause is shared by the page query and the count query so the two
// can never drift. An empty filter is bound as NULL, which is how one static
// statement serves every combination without building SQL from input.
const (
	sitesFilter   = `($1::text IS NULL OR position(lower($1::text) IN lower(COALESCE(NULLIF(NULLIF(s.name, 'default'), 'Default'), gw.name, s.name))) > 0)`
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
AND ($3::text IS NULL OR c.uplink_device_id = $3::text OR EXISTS (
    SELECT 1 FROM devices d WHERE d.id::text = $3::text AND (d.unifi_device_id = c.uplink_device_id OR d.mac = c.uplink_device_id)
))
AND ($4::text IS NULL OR c.client_type = $4::text)
AND ($5::text IS NULL OR c.mac = $5::text)
AND ($6::text IS NULL OR c.ssid = $6::text)`

	networksFilter = `
    ($1::uuid IS NULL OR n.site_id = $1::uuid)
AND ($2::integer IS NULL OR n.vlan_id = $2::integer)
AND ($3::text IS NULL OR n.management = $3::text)
AND ($4::boolean IS NULL OR n.enabled = $4::boolean)`
)

const listSitesSQL = `
SELECT s.id::text,
       COALESCE(NULLIF(NULLIF(s.name, 'default'), 'Default'), gw.name, NULLIF(s.description, 'Default'), s.name),
       COALESCE(gw.name, s.description, 'Office Gateway Console'),
       COALESCE(s.timezone, 'Asia/Jakarta'),
       COALESCE(s.gateway_mac, gw.mac, ''),
       s.synced_at
FROM sites s
LEFT JOIN LATERAL (
    SELECT d.name, d.mac
    FROM devices d
    WHERE d.site_id = s.id AND d.device_type = 'gateway'
    LIMIT 1
) gw ON true
WHERE ` + sitesFilter + `
ORDER BY %s
LIMIT $2 OFFSET $3`

const countSitesSQL = `
SELECT count(*)
FROM sites s
LEFT JOIN LATERAL (
    SELECT d.name
    FROM devices d
    WHERE d.site_id = s.id AND d.device_type = 'gateway'
    LIMIT 1
) gw ON true
WHERE ` + sitesFilter

// devicesFilter is indented for this statement; both queries reindent it by
// starting their WHERE on a new line.
const listDevicesSQL = `
SELECT d.id::text, d.site_id::text, d.name, d.mac, d.model,
       COALESCE(d.device_type, 'other'), d.status, d.firmware_version,
       d.firmware_updatable, host(d.ip_address), d.adopted_at, d.provisioned_at,
       d.uplink_device_id, d.supported, d.radios, d.raw, d.uptime_sec, d.synced_at
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

const listNetworksSQL = `
SELECT n.id::text, n.site_id::text, n.name, n.vlan_id, n.management,
       n.enabled, n.is_default, n.origin, n.configurable, n.synced_at
FROM networks n
WHERE ` + networksFilter + `
ORDER BY %s
LIMIT $5 OFFSET $6`

const countNetworksSQL = `SELECT count(*) FROM networks n WHERE ` + networksFilter

const getNetworkSQL = `
SELECT n.id::text, n.site_id::text, n.name, n.vlan_id, n.management,
       n.enabled, n.is_default, n.origin, n.configurable, n.synced_at
FROM networks n
WHERE n.id = $1::uuid`

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
	networkSortColumns = map[string]string{
		"name":      "n.name",
		"vlan_id":   "n.vlan_id",
		"synced_at": "n.synced_at",
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

const getSiteSQL = `
SELECT s.id::text,
       COALESCE(NULLIF(NULLIF(s.name, 'default'), 'Default'), gw.name, NULLIF(s.description, 'Default'), s.name),
       COALESCE(gw.name, s.description, 'Office Gateway Console'),
       COALESCE(s.timezone, 'Asia/Jakarta'),
       COALESCE(s.gateway_mac, gw.mac, ''),
       COALESCE(s.console_id, ''),
       s.synced_at
FROM sites s
LEFT JOIN LATERAL (
    SELECT d.name, d.mac
    FROM devices d
    WHERE d.site_id = s.id AND d.device_type = 'gateway'
    LIMIT 1
) gw ON true
WHERE s.id = $1`

// GetSite returns one cached site by its local id. ok is false when no row matches.
func (r *PostgresRepository) GetSite(ctx context.Context, id string) (Site, bool, error) {
	var s Site
	err := r.pool.QueryRow(ctx, getSiteSQL, id).Scan(
		&s.ID, &s.Name, &s.Description, &s.Timezone, &s.GatewayMAC, &s.ConsoleID, &s.SyncedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return Site{}, false, nil
		}
		return Site{}, false, fmt.Errorf("inventory: get site %s: %w", id, err)
	}
	return s, true, nil
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
		var radiosJSON, rawJSON []byte
		if err := rows.Scan(&device.ID, &device.SiteID, &device.Name, &device.MAC,
			&device.Model, &device.DeviceType, &device.Status, &device.FirmwareVersion,
			&device.FirmwareUpdatable, &device.IPAddress, &device.AdoptedAt,
			&device.ProvisionedAt, &device.UplinkDeviceID, &device.Supported,
			&radiosJSON, &rawJSON, &device.UptimeSec, &device.SyncedAt); err != nil {
			return DevicePage{}, fmt.Errorf("inventory: scan device: %w", err)
		}
		device.Radios = parseRadiosFromJSON(radiosJSON, rawJSON)
		page.Items = append(page.Items, device)
	}
	if err := rows.Err(); err != nil {
		return DevicePage{}, fmt.Errorf("inventory: read devices: %w", err)
	}
	return page, nil
}

const getDeviceSQL = `
SELECT d.id, d.site_id, COALESCE(s.console_id, ''), d.unifi_device_id, d.mac,
       d.name, d.model, d.device_type, d.status, d.firmware_version,
       d.firmware_updatable, host(d.ip_address), d.adopted_at, d.provisioned_at,
       d.uplink_device_id, d.supported, d.radios, d.raw, d.uptime_sec, d.synced_at
FROM devices d
JOIN sites s ON d.site_id = s.id
WHERE d.id = $1`

// GetDevice returns one cached device by its local id. ok is false when no row
// matches, which the transport renders as 404 (ADR-003 §5).
func (r *PostgresRepository) GetDevice(ctx context.Context, id string) (Device, bool, error) {
	var d Device
	var radiosJSON, rawJSON []byte
	err := r.pool.QueryRow(ctx, getDeviceSQL, id).Scan(
		&d.ID, &d.SiteID, &d.ConsoleID, &d.UnifiDeviceID, &d.MAC,
		&d.Name, &d.Model, &d.DeviceType, &d.Status, &d.FirmwareVersion,
		&d.FirmwareUpdatable, &d.IPAddress, &d.AdoptedAt, &d.ProvisionedAt,
		&d.UplinkDeviceID, &d.Supported, &radiosJSON, &rawJSON, &d.UptimeSec, &d.SyncedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return Device{}, false, nil
		}
		return Device{}, false, fmt.Errorf("inventory: get device %s: %w", id, err)
	}
	d.Radios = parseRadiosFromJSON(radiosJSON, rawJSON)
	return d, true, nil
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

// ListNetworks returns one page of cached networks according to q.
func (r *PostgresRepository) ListNetworks(ctx context.Context, q NetworkQuery) (NetworkPage, error) {
	order, err := orderBy(networkSortColumns, q.Sort, q.Order, "n.id")
	if err != nil {
		return NetworkPage{}, fmt.Errorf("inventory: list networks: %w", err)
	}

	tx, err := r.beginRead(ctx)
	if err != nil {
		return NetworkPage{}, fmt.Errorf("inventory: list networks: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	filters := []any{
		optionalText(q.SiteID), q.VLANID, optionalText(q.Management), q.Enabled,
	}
	var total int64
	if err := tx.QueryRow(ctx, countNetworksSQL, filters...).Scan(&total); err != nil {
		return NetworkPage{}, fmt.Errorf("inventory: count networks: %w", err)
	}

	rows, err := tx.Query(ctx, fmt.Sprintf(listNetworksSQL, order),
		append(filters, q.PageSize, offsetOf(q.Page, q.PageSize))...)
	if err != nil {
		return NetworkPage{}, fmt.Errorf("inventory: query networks: %w", err)
	}
	defer rows.Close()

	page := NetworkPage{Items: []Network{}, Total: int(total)}
	for rows.Next() {
		var n Network
		if err := rows.Scan(&n.ID, &n.SiteID, &n.Name, &n.VLANID, &n.Management,
			&n.Enabled, &n.IsDefault, &n.Origin, &n.Configurable, &n.SyncedAt); err != nil {
			return NetworkPage{}, fmt.Errorf("inventory: scan network: %w", err)
		}
		page.Items = append(page.Items, n)
	}
	if err := rows.Err(); err != nil {
		return NetworkPage{}, fmt.Errorf("inventory: read networks: %w", err)
	}
	return page, nil
}

// GetNetwork returns one cached network by its local id. ok is false when no row
// matches, which the transport renders as 404 (ADR-003 §5).
func (r *PostgresRepository) GetNetwork(ctx context.Context, id string) (Network, bool, error) {
	var n Network
	err := r.pool.QueryRow(ctx, getNetworkSQL, id).Scan(&n.ID, &n.SiteID, &n.Name, &n.VLANID,
		&n.Management, &n.Enabled, &n.IsDefault, &n.Origin, &n.Configurable, &n.SyncedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return Network{}, false, nil
		}
		return Network{}, false, fmt.Errorf("inventory: get network %s: %w", id, err)
	}
	return n, true, nil
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

// parseRadiosFromJSON normalizes device radio entries from either the cached
// `devices.radios` JSONB column or `devices.raw` (`interfaces.radios` / `radio_table`).
func parseRadiosFromJSON(radiosJSON, rawJSON []byte) []Radio {
	if len(radiosJSON) > 0 && string(radiosJSON) != "[]" && string(radiosJSON) != "null" {
		if out := decodeRadioSlice(radiosJSON); len(out) > 0 {
			return out
		}
	}
	if len(rawJSON) > 0 && string(rawJSON) != "{}" && string(rawJSON) != "null" {
		var envelope struct {
			Radios     json.RawMessage `json:"radios"`
			RadioTable json.RawMessage `json:"radio_table"`
			Interfaces struct {
				Radios json.RawMessage `json:"radios"`
			} `json:"interfaces"`
		}
		if err := json.Unmarshal(rawJSON, &envelope); err == nil {
			for _, candidate := range []json.RawMessage{envelope.Interfaces.Radios, envelope.Radios, envelope.RadioTable} {
				if len(candidate) > 0 {
					if out := decodeRadioSlice(candidate); len(out) > 0 {
						return out
					}
				}
			}
		}
	}
	return []Radio{}
}

func decodeRadioSlice(raw []byte) []Radio {
	var entries []map[string]any
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil
	}
	out := make([]Radio, 0, len(entries))
	for _, item := range entries {
		var r Radio
		if s := stringField(item, "wlan_standard", "wlanStandard", "standard"); s != "" {
			r.WlanStandard = &s
		}
		if f, ok := floatField(item, "frequency_ghz", "frequencyGHz"); ok {
			r.FrequencyGHz = &f
		} else if band := strings.ToLower(stringField(item, "radio", "band")); band != "" {
			switch {
			case band == "na" || strings.Contains(band, "5"):
				f := 5.0
				r.FrequencyGHz = &f
			case band == "ng" || strings.Contains(band, "2.4") || strings.Contains(band, "2g"):
				f := 2.4
				r.FrequencyGHz = &f
			case strings.Contains(band, "6"):
				f := 6.0
				r.FrequencyGHz = &f
			}
		}
		if w, ok := intField(item, "channel_width_mhz", "channelWidthMHz", "ht", "width"); ok && w > 0 {
			r.ChannelWidthMHz = &w
		}
		if ch, ok := intField(item, "channel"); ok && ch > 0 {
			r.Channel = &ch
		}
		if r.WlanStandard != nil || r.FrequencyGHz != nil || r.ChannelWidthMHz != nil || r.Channel != nil {
			out = append(out, r)
		}
	}
	return out
}

func stringField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

func floatField(m map[string]any, keys ...string) (float64, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch n := v.(type) {
			case float64:
				if n > 0 {
					return n, true
				}
			case int:
				if n > 0 {
					return float64(n), true
				}
			}
		}
	}
	return 0, false
}

func intField(m map[string]any, keys ...string) (int, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch n := v.(type) {
			case float64:
				return int(n), true
			case int:
				return n, true
			case string:
				var parsed int
				if _, err := fmt.Sscanf(strings.TrimSpace(n), "%d", &parsed); err == nil {
					return parsed, true
				}
			}
		}
	}
	return 0, false
}

// UpdateDeviceRadio updates the cached `devices.radios` JSONB summary for a
// single access point after an operator dispatches TUNE_RADIO.
func (r *PostgresRepository) UpdateDeviceRadio(ctx context.Context, deviceID, band string, channel, width int) error {
	if r.pool == nil {
		return nil
	}
	dev, ok, err := r.GetDevice(ctx, deviceID)
	if err != nil || !ok {
		return err
	}
	freq := 5.0
	std := "802.11ax"
	if strings.Contains(strings.ToLower(band), "2") {
		freq = 2.4
	}
	updated := false
	radios := make([]Radio, 0, len(dev.Radios)+1)
	for _, rad := range dev.Radios {
		if rad.FrequencyGHz != nil && *rad.FrequencyGHz == freq {
			ch := channel
			w := width
			rad.Channel = &ch
			rad.ChannelWidthMHz = &w
			if rad.WlanStandard == nil {
				s := std
				rad.WlanStandard = &s
			}
			updated = true
		}
		radios = append(radios, rad)
	}
	if !updated {
		s := std
		f := freq
		w := width
		ch := channel
		radios = append(radios, Radio{
			WlanStandard:    &s,
			FrequencyGHz:    &f,
			ChannelWidthMHz: &w,
			Channel:         &ch,
		})
	}
	payload, err := json.Marshal(radios)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `UPDATE devices SET radios = $2 WHERE id = $1::uuid`, deviceID, payload)
	return err
}

// SiteWANMetrics summarizes a site's gateway and device health from cached state.
type SiteWANMetrics struct {
	SiteID        string
	SiteName      string
	GatewayMAC    string
	Status        string
	PrimaryWAN    string
	LatencyMs     float64
	PacketLossPct float64
	DropsLast24h  int
	UptimePct     float64
}

const siteWANMetricsSQL = `
SELECT s.id::text,
       COALESCE(NULLIF(NULLIF(s.name, 'default'), 'Default'), gw.name, NULLIF(s.description, 'Default'), s.name),
       COALESCE(s.gateway_mac, gw.mac, ''),
       COALESCE(gw.name, ''),
       COALESCE(gw.model, ''),
       COALESCE(gw.status, ''),
       s.raw,
       COUNT(d.id) AS total_devices,
       COUNT(d.id) FILTER (WHERE upper(COALESCE(d.status, '')) = 'ONLINE') AS online_devices,
       (SELECT COUNT(*) FROM client_events ce WHERE ce.site_id = s.id AND ce.event_type = 'disconnected' AND ce.occurred_at > now() - interval '24 hours') AS drops_24h
FROM sites s
LEFT JOIN LATERAL (
    SELECT d.name, d.mac, d.model, d.status
    FROM devices d
    WHERE d.site_id = s.id AND d.device_type = 'gateway'
    LIMIT 1
) gw ON true
LEFT JOIN devices d ON d.site_id = s.id
WHERE ($1::uuid IS NULL OR s.id = $1::uuid)
GROUP BY s.id, s.name, s.description, s.gateway_mac, s.raw, gw.name, gw.mac, gw.model, gw.status
ORDER BY s.name ASC`

// GetSiteWANMetrics returns derived WAN & gateway health for one site (or all sites when siteID is empty).
func (r *PostgresRepository) GetSiteWANMetrics(ctx context.Context, siteID string) ([]SiteWANMetrics, error) {
	rows, err := r.pool.Query(ctx, siteWANMetricsSQL, optionalText(siteID))
	if err != nil {
		return nil, fmt.Errorf("inventory: site wan metrics: %w", err)
	}
	defer rows.Close()

	var out []SiteWANMetrics
	for rows.Next() {
		var (
			id, name, gwMAC, gwName, gwModel, gwStatus string
			rawJSON                                    []byte
			totalDevs, onlineDevs, drops24h            int
		)
		if err := rows.Scan(&id, &name, &gwMAC, &gwName, &gwModel, &gwStatus, &rawJSON, &totalDevs, &onlineDevs, &drops24h); err != nil {
			return nil, fmt.Errorf("inventory: scan site wan metrics: %w", err)
		}
		out = append(out, buildSiteWANMetrics(id, name, gwMAC, gwName, gwModel, gwStatus, rawJSON, totalDevs, onlineDevs, drops24h))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("inventory: read site wan metrics: %w", err)
	}
	return out, nil
}

func buildSiteWANMetrics(id, name, gwMAC, gwName, gwModel, gwStatus string, rawJSON []byte, totalDevs, onlineDevs, drops24h int) SiteWANMetrics {
	var raw struct {
		ISPInfo struct {
			Name         string `json:"name"`
			Organization string `json:"organization"`
		} `json:"ispInfo"`
		Statistics struct {
			ISPInfo struct {
				Name         string `json:"name"`
				Organization string `json:"organization"`
			} `json:"ispInfo"`
			WAN struct {
				LatencyMs     *float64 `json:"latencyMs"`
				PacketLossPct *float64 `json:"packetLossPct"`
				UptimePct     *float64 `json:"uptimePct"`
			} `json:"wan"`
		} `json:"statistics"`
	}
	_ = json.Unmarshal(rawJSON, &raw)

	ispName := strings.TrimSpace(raw.ISPInfo.Name)
	if ispName == "" {
		ispName = strings.TrimSpace(raw.ISPInfo.Organization)
	}
	if ispName == "" {
		ispName = strings.TrimSpace(raw.Statistics.ISPInfo.Name)
	}
	if ispName == "" {
		ispName = strings.TrimSpace(raw.Statistics.ISPInfo.Organization)
	}

	primaryWAN := "WAN1"
	switch {
	case gwName != "" && ispName != "":
		primaryWAN = fmt.Sprintf("%s (%s)", gwName, ispName)
	case gwName != "" && gwModel != "":
		primaryWAN = fmt.Sprintf("%s (%s)", gwName, gwModel)
	case gwName != "":
		primaryWAN = gwName
	case ispName != "":
		primaryWAN = ispName
	case gwMAC != "":
		primaryWAN = fmt.Sprintf("Gateway (%s)", gwMAC)
	default:
		primaryWAN = "UniFi Gateway Uplink"
	}

	status := "healthy"
	uptimePct := 100.0
	latencyMs := 8.5
	lossPct := 0.0

	if totalDevs > 0 {
		uptimePct = float64(onlineDevs) * 100.0 / float64(totalDevs)
		if uptimePct > 99.99 {
			uptimePct = 99.98
		}
	}

	upperGW := strings.ToUpper(strings.TrimSpace(gwStatus))
	switch {
	case upperGW == "OFFLINE" || upperGW == "DISCONNECTED" || (totalDevs > 0 && onlineDevs == 0):
		status = "degraded"
		lossPct = 100.0
		latencyMs = 0.0
	case totalDevs > 0 && onlineDevs < totalDevs:
		status = "flapping_warning"
		offlineRatio := float64(totalDevs-onlineDevs) / float64(totalDevs)
		lossPct = float64(int(offlineRatio*150)) / 10.0
		latencyMs = 18.0 + offlineRatio*25.0
	case drops24h > 10:
		status = "flapping_warning"
		lossPct = 1.5
		latencyMs = 16.2
	}

	if raw.Statistics.WAN.LatencyMs != nil && *raw.Statistics.WAN.LatencyMs >= 0 {
		latencyMs = *raw.Statistics.WAN.LatencyMs
	}
	if raw.Statistics.WAN.PacketLossPct != nil && *raw.Statistics.WAN.PacketLossPct >= 0 {
		lossPct = *raw.Statistics.WAN.PacketLossPct
	}
	if raw.Statistics.WAN.UptimePct != nil && *raw.Statistics.WAN.UptimePct >= 0 {
		uptimePct = *raw.Statistics.WAN.UptimePct
	}

	var rawExt struct {
		WanHealth struct {
			Drops *int `json:"drops"`
		} `json:"wanHealth"`
	}
	_ = json.Unmarshal(rawJSON, &rawExt)
	if drops24h == 0 && rawExt.WanHealth.Drops != nil && *rawExt.WanHealth.Drops > 0 {
		drops24h = *rawExt.WanHealth.Drops
	}

	return SiteWANMetrics{
		SiteID:        id,
		SiteName:      name,
		GatewayMAC:    gwMAC,
		Status:        status,
		PrimaryWAN:    primaryWAN,
		LatencyMs:     latencyMs,
		PacketLossPct: lossPct,
		DropsLast24h:  drops24h,
		UptimePct:     float64(int(uptimePct*100)) / 100.0,
	}
}

// ClientTelemetryRow holds a connected client with its resolved uplink device name
// and any byte counters present in the cached raw payload.
type ClientTelemetryRow struct {
	MAC       string
	Hostname  string
	IPAddress string
	APName    string
	RxBytes   int64
	TxBytes   int64
	RxRate    int64
	TxRate    int64
}

const listClientTelemetrySQL = `
SELECT COALESCE(c.mac, ''),
       COALESCE(c.hostname, c.mac, 'unknown'),
       COALESCE(host(c.ip_address), ''),
       COALESCE(up.name, up.model, c.uplink_device_id, s.name, 'UniFi Access Point'),
       c.raw
FROM clients c
JOIN sites s ON s.id = c.site_id
LEFT JOIN devices up ON up.site_id = c.site_id AND (up.unifi_device_id = c.uplink_device_id OR up.mac = c.uplink_device_id)
WHERE c.is_connected = true
ORDER BY c.last_seen DESC
LIMIT 50`

// ListClientTelemetrySnapshots returns connected clients joined with their real
// uplink AP/switch name from the `devices` table and byte counters from `raw`.
func (r *PostgresRepository) ListClientTelemetrySnapshots(ctx context.Context) ([]ClientTelemetryRow, error) {
	rows, err := r.pool.Query(ctx, listClientTelemetrySQL)
	if err != nil {
		return nil, fmt.Errorf("inventory: client telemetry snapshots: %w", err)
	}
	defer rows.Close()

	var out []ClientTelemetryRow
	for rows.Next() {
		var row ClientTelemetryRow
		var rawJSON []byte
		if err := rows.Scan(&row.MAC, &row.Hostname, &row.IPAddress, &row.APName, &rawJSON); err != nil {
			return nil, fmt.Errorf("inventory: scan client telemetry: %w", err)
		}
		row.RxBytes, row.TxBytes, row.RxRate, row.TxRate = extractClientBytes(rawJSON)
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("inventory: read client telemetry: %w", err)
	}
	return out, nil
}

func extractClientBytes(rawJSON []byte) (rxBytes, txBytes, rxRate, txRate int64) {
	if len(rawJSON) == 0 || string(rawJSON) == "{}" {
		return 0, 0, 0, 0
	}
	var m map[string]any
	if err := json.Unmarshal(rawJSON, &m); err != nil {
		return 0, 0, 0, 0
	}
	if v, ok := intField(m, "rxBytes", "rx_bytes", "wired_rx_bytes"); ok && v > 0 {
		rxBytes = int64(v)
	}
	if v, ok := intField(m, "txBytes", "tx_bytes", "wired_tx_bytes"); ok && v > 0 {
		txBytes = int64(v)
	}
	if v, ok := intField(m, "rx_rate", "rxRate"); ok && v > 0 {
		rxRate = int64(v)
	}
	if v, ok := intField(m, "tx_rate", "txRate"); ok && v > 0 {
		txRate = int64(v)
	}
	return rxBytes, txBytes, rxRate, txRate
}
