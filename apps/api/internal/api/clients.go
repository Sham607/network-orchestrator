package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Sham607/network-orchestrator/apps/api/internal/inventory"
)

// clientBody is the contract's Client schema. ssid and signal_dbm stay null in
// phase 1 — the Integration API does not expose them yet — and the transport
// must never fabricate a value for them (openapi/mock-expectations.md §4,
// docs/data-model.md §4.5).
type clientBody struct {
	ID             string  `json:"id"`
	SiteID         string  `json:"site_id"`
	MAC            *string `json:"mac"`
	Hostname       *string `json:"hostname"`
	IPAddress      *string `json:"ip_address"`
	ClientType     *string `json:"client_type"`
	AccessType     *string `json:"access_type"`
	UplinkDeviceID *string `json:"uplink_device_id"`
	SSID           *string `json:"ssid"`
	SignalDBm      *int    `json:"signal_dbm"`
	IsConnected    bool    `json:"is_connected"`
	ConnectedAt    *string `json:"connected_at"`
	DisconnectedAt *string `json:"disconnected_at"`
	FirstSeen      string  `json:"first_seen"`
	LastSeen       string  `json:"last_seen"`
	SyncedAt       string  `json:"synced_at"`
}

// clientListBody is the contract's ClientList schema.
type clientListBody struct {
	Data       []clientBody   `json:"data"`
	Pagination paginationBody `json:"pagination"`
}

// clientBodyOf renders one cached client as the contract's Client schema.
func clientBodyOf(client inventory.Client) clientBody {
	return clientBody{
		ID:             client.ID,
		SiteID:         client.SiteID,
		MAC:            client.MAC,
		Hostname:       client.Hostname,
		IPAddress:      client.IPAddress,
		ClientType:     client.ClientType,
		AccessType:     client.AccessType,
		UplinkDeviceID: client.UplinkDeviceID,
		SSID:           client.SSID,
		SignalDBm:      client.SignalDBm,
		IsConnected:    client.IsConnected,
		ConnectedAt:    formatTimePtr(client.ConnectedAt),
		DisconnectedAt: formatTimePtr(client.DisconnectedAt),
		FirstSeen:      formatTime(client.FirstSeen),
		LastSeen:       formatTime(client.LastSeen),
		SyncedAt:       formatTime(client.SyncedAt),
	}
}

// handleListClients serves GET /clients from the local cache with every
// filter applied server-side (ZENI-26).
func handleListClients(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fields := fieldErrors{}
		values := r.URL.Query()
		validateAllowedParams(fields, values, "page", "page_size", "order", "sort",
			"site_id", "connected", "uplink_device_id", "client_type", "mac", "ssid")
		params := parseListParams(r, fields)

		query := inventory.ClientQuery{
			Page:     params.Page,
			PageSize: params.PageSize,
			Order:    params.Order,
			Sort:     "last_seen", // the contract's default
		}
		if raw := values.Get("sort"); raw != "" && validateEnum(fields, "sort", raw, "hostname", "last_seen", "connected_at", "synced_at") {
			query.Sort = raw
		}
		if raw := values.Get("site_id"); raw != "" && validateUUID(fields, "site_id", raw) {
			query.SiteID = raw
		}
		if raw := values.Get("connected"); raw != "" {
			if v, ok := validateBool(fields, "connected", raw); ok {
				query.Connected = &v
			}
		}
		if raw := values.Get("uplink_device_id"); raw != "" && validateUUID(fields, "uplink_device_id", raw) {
			query.UplinkDeviceID = raw
		}
		if raw := values.Get("client_type"); raw != "" && validateEnum(fields, "client_type", raw, "WIRELESS", "WIRED") {
			query.ClientType = raw
		}
		if raw := values.Get("mac"); raw != "" && validateMAC(fields, "mac", raw) {
			query.MAC = strings.ToLower(raw)
		}
		// ssid is published but inert (openapi/mock-expectations.md §5): the
		// column is always null, so this filter can never match a row. It is
		// still accepted and forwarded rather than rejected as unrecognized.
		if raw := values.Get("ssid"); raw != "" {
			query.SSID = raw
		}
		if fields.invalid() {
			writeValidationFailed(w, r, fields)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), listTimeout)
		defer cancel()

		page, err := deps.Inventory.ListClients(ctx, query)
		if err != nil {
			writeCacheUnavailable(w, r, deps, err)
			return
		}
		if cacheStaleAndControllerOffline(deps) {
			deps.Logger.Warn("list answered without a fresh cache", "module", "api", "event", "cache_stale_controller_offline", "endpoint", "clients")
			writeError(w, r, CodeUnifiUnreachable, "UniFi controller is not reachable", map[string]any{
				"reason":                  "cache_stale",
				"last_successful_sync_at": formatTimePtr(deps.Sync.Status().LastSuccessfulSyncAt),
			})
			return
		}

		body := clientListBody{
			Data:       make([]clientBody, 0, len(page.Items)),
			Pagination: newPaginationBody(query.Page, query.PageSize, page.Total),
		}
		for _, client := range page.Items {
			body.Data = append(body.Data, clientBodyOf(client))
		}
		writeJSON(w, http.StatusOK, body)
	}
}

// handleGetClient serves GET /clients/{id} from the local cache (ZENI-26).
func handleGetClient(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fields := fieldErrors{}
		id := chi.URLParam(r, "id")
		validateUUID(fields, "id", id)
		if fields.invalid() {
			writeValidationFailed(w, r, fields)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), listTimeout)
		defer cancel()

		client, found, err := deps.Inventory.GetClient(ctx, id)
		if err != nil {
			writeCacheUnavailable(w, r, deps, err)
			return
		}
		// A stale cache with an offline controller cannot distinguish "truly
		// absent" from "we do not know yet", so it answers 503 rather than a
		// possibly-wrong 404 (ADR-004, same rule as the list endpoints).
		if cacheStaleAndControllerOffline(deps) {
			deps.Logger.Warn("get answered without a fresh cache", "module", "api", "event", "cache_stale_controller_offline", "endpoint", "clients")
			writeError(w, r, CodeUnifiUnreachable, "UniFi controller is not reachable", map[string]any{
				"reason":                  "cache_stale",
				"last_successful_sync_at": formatTimePtr(deps.Sync.Status().LastSuccessfulSyncAt),
			})
			return
		}
		if !found {
			writeError(w, r, CodeNotFound, "client not found", map[string]any{"resource": "client"})
			return
		}

		writeJSON(w, http.StatusOK, clientBodyOf(client))
	}
}
