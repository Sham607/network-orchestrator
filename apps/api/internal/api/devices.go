package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Sham607/network-orchestrator/apps/api/internal/audit"
	"github.com/Sham607/network-orchestrator/apps/api/internal/identity"
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
		values := r.URL.Query()
		validateAllowedParams(fields, values, "page", "page_size", "order", "sort", "site_id", "status", "device_type", "mac")
		params := parseListParams(r, fields)

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
			body.Data = append(body.Data, deviceBodyOf(device))
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

func radioBodiesOf(radios []inventory.Radio) []radioBody {
	out := make([]radioBody, 0, len(radios))
	for _, r := range radios {
		out = append(out, radioBody{
			WlanStandard:    r.WlanStandard,
			FrequencyGHz:    r.FrequencyGHz,
			ChannelWidthMHz: r.ChannelWidthMHz,
			Channel:         r.Channel,
		})
	}
	return out
}

func deviceBodyOf(device inventory.Device) deviceBody {
	return deviceBody{
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
		Radios:            radioBodiesOf(device.Radios),
		UptimeSec:         device.UptimeSec,
		SyncedAt:          formatTime(device.SyncedAt),
	}
}

func handleGetDevice(deps Dependencies) http.HandlerFunc {
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

		device, found, err := deps.Inventory.GetDevice(ctx, id)
		if err != nil {
			writeCacheUnavailable(w, r, deps, err)
			return
		}
		if cacheStaleAndControllerOffline(deps) {
			deps.Logger.Warn("get answered without a fresh cache", "module", "api", "event", "cache_stale_controller_offline", "endpoint", "devices")
			writeError(w, r, CodeUnifiUnreachable, "UniFi controller is not reachable", map[string]any{
				"reason":                  "cache_stale",
				"last_successful_sync_at": formatTimePtr(deps.Sync.Status().LastSuccessfulSyncAt),
			})
			return
		}
		if !found {
			writeError(w, r, CodeNotFound, "device not found", map[string]any{"resource": "device"})
			return
		}

		writeJSON(w, http.StatusOK, deviceBodyOf(device))
	}
}

type DeviceActionRequest struct {
	Action  string `json:"action"`
	Band    string `json:"band,omitempty"`
	Channel int    `json:"channel,omitempty"`
	Width   int    `json:"width,omitempty"`
}

func handleDeviceAction(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !deps.Config.WriteEnabled {
			handleWriteDisabled(w, r)
			return
		}

		sess, hasSession := identity.SessionFromContext(r.Context())
		if hasSession {
			if !sess.User.Role.CanOperate() {
				writeError(w, r, CodeForbidden, "operator or admin role required", map[string]any{
					"reason":        "insufficient_role",
					"required_role": "operator",
					"current_role":  string(sess.User.Role),
				})
				return
			}
			if sess.CSRFToken != "" && !identity.VerifyCSRF(sess.CSRFToken, r.Header.Get(csrfHeaderName)) {
				writeError(w, r, CodeForbidden, "invalid or missing CSRF token", map[string]any{
					"reason": "csrf_invalid",
				})
				return
			}
		}
		fields := fieldErrors{}
		id := chi.URLParam(r, "id")
		validateUUID(fields, "id", id)
		if fields.invalid() {
			writeValidationFailed(w, r, fields)
			return
		}

		var req DeviceActionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, r, CodeValidationFailed, "cannot parse JSON body: "+err.Error(), nil)
			return
		}

		action := strings.ToUpper(strings.TrimSpace(req.Action))
		if action != "RESTART" && action != "TUNE_RADIO" {
			writeError(w, r, CodeValidationFailed, fmt.Sprintf("unsupported action %q: must be RESTART or TUNE_RADIO", req.Action), nil)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()

		dev, ok, err := deps.Inventory.GetDevice(ctx, id)
		if err != nil {
			writeError(w, r, CodeInternalError, "cannot query device", map[string]any{"error": err.Error()})
			return
		}
		if !ok {
			writeError(w, r, CodeNotFound, fmt.Sprintf("device %s not found in cache", id), map[string]any{"resource": "device"})
			return
		}

		// SAFETY GUARDRAIL: Only access points! Never gateways or switches.
		if dev.DeviceType != "ap" {
			writeError(w, r, CodeValidationFailed, "actions are restricted strictly to access points (type == 'ap')", map[string]any{
				"reason":      "unsupported_device_type",
				"device_type": dev.DeviceType,
			})
			return
		}

		now := time.Now().UTC()
		var auditID int64
		detail := map[string]any{
			"device_id":       id,
			"device_name":     dev.Name,
			"model":           dev.Model,
			"mac":             dev.MAC,
			"console_id":      dev.ConsoleID,
			"unifi_device_id": dev.UnifiDeviceID,
		}

		if action == "RESTART" {
			if deps.Writer != nil && dev.ConsoleID != "" && dev.MAC != nil {
				if err := deps.Writer.RestartDevice(ctx, dev.ConsoleID, *dev.MAC); err != nil {
					writeError(w, r, CodeUnifiUnreachable, "failed to dispatch restart command to UniFi: "+err.Error(), nil)
					return
				}
			}
		} else if action == "TUNE_RADIO" {
			if req.Channel <= 0 {
				writeError(w, r, CodeValidationFailed, "valid channel integer is required for TUNE_RADIO", nil)
				return
			}
			width := req.Width
			if width <= 0 {
				width = 40 // default HT40 per policy
			}
			band := req.Band
			if band == "" {
				band = "5g"
			}
			detail["target_band"] = band
			detail["target_channel"] = req.Channel
			detail["target_width"] = width

			if deps.Writer != nil && dev.ConsoleID != "" && dev.UnifiDeviceID != "" {
				if err := deps.Writer.TuneRadio(ctx, dev.ConsoleID, dev.UnifiDeviceID, band, req.Channel, width); err != nil {
					writeError(w, r, CodeUnifiUnreachable, "failed to dispatch radio update to UniFi: "+err.Error(), nil)
					return
				}
			}
			if updater, ok := deps.Inventory.(interface {
				UpdateDeviceRadio(ctx context.Context, deviceID, band string, channel, width int) error
			}); ok {
				_ = updater.UpdateDeviceRadio(ctx, id, band, req.Channel, width)
			}
		}

		if deps.Audit != nil {
			actorType := "operator"
			actorLabel := "dashboard-operator"
			var actorUserID *string
			if hasSession {
				actorType = "user"
				actorLabel = sess.User.Email
				uid := sess.User.ID
				actorUserID = &uid
				detail["operator_email"] = sess.User.Email
				detail["operator_role"] = string(sess.User.Role)
			}
			siteID := dev.SiteID
			idVal, createdAt, err := deps.Audit.RecordAction(ctx, audit.Entry{
				ActorUserID: actorUserID,
				ActorType:   actorType,
				ActorLabel:  actorLabel,
				Action:      "device." + strings.ToLower(action),
				TargetType:  "device",
				TargetID:    id,
				SiteID:      &siteID,
				RequestID:   requestIDFrom(r.Context()),
				IP:          clientIP(r),
				Detail:      detail,
			})
			if err == nil {
				auditID = idVal
				now = createdAt
			}
		}

		writeJSON(w, http.StatusAccepted, map[string]any{
			"audit_id":    auditID,
			"action":      action,
			"target_type": "device",
			"target_id":   id,
			"status":      "accepted",
			"accepted_at": now.Format(time.RFC3339),
		})
	}
}
