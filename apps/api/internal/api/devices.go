package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Sham607/network-orchestrator/apps/api/internal/inventory"
)

// deviceBody is the contract's Device schema. Properties the cache does not
// hold yet are emitted as null, never omitted and never fabricated; `radios` is
// an array, so it is always present (openapi/mock-expectations.md §4).
type deviceBody struct {
	ID                string      `json:"id"`
	SiteID            string      `json:"site_id"`
	Name              *string     `json:"name"`
	MAC               *string     `json:"mac"`
	Model             *string     `json:"model"`
	DeviceType        string      `json:"device_type"`
	Status            *string     `json:"status"`
	FirmwareVersion   *string     `json:"firmware_version"`
	FirmwareUpdatable *bool       `json:"firmware_updatable"`
	IPAddress         *string     `json:"ip_address"`
	AdoptedAt         *string     `json:"adopted_at"`
	ProvisionedAt     *string     `json:"provisioned_at"`
	UplinkDeviceID    *string     `json:"uplink_device_id"`
	Supported         *bool       `json:"supported"`
	Radios            []radioBody `json:"radios"`
	UptimeSec         *int64      `json:"uptime_sec"`
	SyncedAt          string      `json:"synced_at"`
}

// radioBody is the contract's Radio schema: a *normalized* summary of the
// controller's radios. The write path does not normalize it yet, so the list is
// empty — the stored raw payload is never exposed as a substitute
// (docs/adr/003-architecture.md §2).
type radioBody struct {
	WlanStandard    *string  `json:"wlan_standard"`
	FrequencyGHz    *float64 `json:"frequency_ghz"`
	ChannelWidthMHz *int     `json:"channel_width_mhz"`
	Channel         *int     `json:"channel"`
}

// deviceListBody is the contract's DeviceList schema.
type deviceListBody struct {
	Data       []deviceBody   `json:"data"`
	Pagination paginationBody `json:"pagination"`
}

// handleListDevices serves GET /devices from the local cache with every filter
// applied server-side, so the dashboard never fetches the fleet to filter it in
// the browser.
func handleListDevices(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fields := fieldErrors{}
		params := parseListParams(r, fields)
		values := r.URL.Query()

		query := inventory.DeviceQuery{
			Page:     params.Page,
			PageSize: params.PageSize,
			Order:    params.Order,
			Sort:     "name", // the contract's default
		}
		if raw := values.Get("sort"); raw != "" && validateEnum(fields, "sort", raw, "name", "model", "status", "adopted_at", "synced_at") {
			query.Sort = raw
		}
		if raw := values.Get("site_id"); raw != "" && validateUUID(fields, "site_id", raw) {
			query.SiteID = raw
		}
		if raw := values.Get("status"); raw != "" && validateMaxLength(fields, "status", raw, 32) {
			// The contract stores the UniFi state upper-case; normalizing here
			// keeps the match exact without making the caller guess the case.
			query.Status = strings.ToUpper(raw)
		}
		if raw := values.Get("device_type"); raw != "" && validateEnum(fields, "device_type", raw, "ap", "switch", "gateway", "other") {
			query.DeviceType = raw
		}
		if raw := values.Get("mac"); raw != "" && validateMAC(fields, "mac", raw) {
			query.MAC = strings.ToLower(raw)
		}
		if fields.invalid() {
			writeValidationFailed(w, r, fields)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), listTimeout)
		defer cancel()

		page, err := deps.Inventory.ListDevices(ctx, query)
		if err != nil {
			writeCacheUnavailable(w, r, deps, err)
			return
		}
		// The database answered first, so a dead database is still reported as
		// service_unavailable; only a controller we cannot reach — with a cache too
		// old to trust — is unifi_unreachable (ADR-004).
		if cacheStaleAndControllerOffline(deps) {
			deps.Logger.Warn("list answered without a fresh cache", "module", "api", "event", "cache_stale_controller_offline", "endpoint", "devices")
			writeError(w, r, CodeUnifiUnreachable, "UniFi controller is not reachable", map[string]any{
				"reason":                  "cache_stale",
				"last_successful_sync_at": formatTimePtr(deps.Sync.Status().LastSuccessfulSyncAt),
			})
			return
		}

		body := deviceListBody{
			Data:       make([]deviceBody, 0, len(page.Items)),
			Pagination: newPaginationBody(query.Page, query.PageSize, page.Total),
		}
		for _, device := range page.Items {
			body.Data = append(body.Data, deviceBody{
				ID:                device.ID,
				SiteID:            device.SiteID,
				Name:              device.Name,
				MAC:               device.MAC,
				Model:             device.Model,
				DeviceType:        device.DeviceType,
				Status:            device.Status,
				FirmwareVersion:   device.FirmwareVersion,
				FirmwareUpdatable: device.FirmwareUpdatable,
				IPAddress:         device.IPAddress,
				AdoptedAt:         formatTimePtr(device.AdoptedAt),
				ProvisionedAt:     formatTimePtr(device.ProvisionedAt),
				UplinkDeviceID:    device.UplinkDeviceID,
				Supported:         device.Supported,
				Radios:            []radioBody{},
				UptimeSec:         device.UptimeSec,
				SyncedAt:          formatTime(device.SyncedAt),
			})
		}
		writeJSON(w, http.StatusOK, body)
	}
}

// formatTimePtr renders an optional timestamp; nil stays null instead of
// becoming the zero time.
func formatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	formatted := formatTime(*t)
	return &formatted
}
