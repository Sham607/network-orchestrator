package api

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Sham607/network-orchestrator/apps/api/internal/inventory"
)

// networkBody is the contract's Network schema.
type networkBody struct {
	ID           string  `json:"id"`
	SiteID       string  `json:"site_id"`
	Name         string  `json:"name"`
	VLANID       *int    `json:"vlan_id"`
	Management   *string `json:"management"`
	Enabled      *bool   `json:"enabled"`
	IsDefault    *bool   `json:"is_default"`
	Origin       *string `json:"origin"`
	Configurable *bool   `json:"configurable"`
	SyncedAt     string  `json:"synced_at"`
}

// networkListBody is the contract's NetworkList schema.
type networkListBody struct {
	Data       []networkBody  `json:"data"`
	Pagination paginationBody `json:"pagination"`
}

// networkBodyOf renders one cached network as the contract's Network schema.
func networkBodyOf(n inventory.Network) networkBody {
	return networkBody{
		ID:           n.ID,
		SiteID:       n.SiteID,
		Name:         n.Name,
		VLANID:       n.VLANID,
		Management:   n.Management,
		Enabled:      n.Enabled,
		IsDefault:    n.IsDefault,
		Origin:       n.Origin,
		Configurable: n.Configurable,
		SyncedAt:     formatTime(n.SyncedAt),
	}
}

// handleListNetworks serves GET /networks from the local cache with every
// filter applied server-side.
func handleListNetworks(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fields := fieldErrors{}
		values := r.URL.Query()
		validateAllowedParams(fields, values, "page", "page_size", "order", "sort",
			"site_id", "vlan_id", "management", "enabled")
		params := parseListParams(r, fields)

		query := inventory.NetworkQuery{
			Page:     params.Page,
			PageSize: params.PageSize,
			Order:    params.Order,
			Sort:     "vlan_id", // the contract's default
		}
		if raw := values.Get("sort"); raw != "" && validateEnum(fields, "sort", raw, "name", "vlan_id", "synced_at") {
			query.Sort = raw
		}
		if raw := values.Get("site_id"); raw != "" && validateUUID(fields, "site_id", raw) {
			query.SiteID = raw
		}
		if raw := values.Get("vlan_id"); raw != "" {
			if v, ok := validateIntRange(fields, "vlan_id", raw, 0, 4094); ok {
				query.VLANID = &v
			}
		}
		if raw := values.Get("management"); raw != "" && validateMaxLength(fields, "management", raw, 32) {
			query.Management = raw
		}
		if raw := values.Get("enabled"); raw != "" {
			if v, ok := validateBool(fields, "enabled", raw); ok {
				query.Enabled = &v
			}
		}
		if fields.invalid() {
			writeValidationFailed(w, r, fields)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), listTimeout)
		defer cancel()

		page, err := deps.Inventory.ListNetworks(ctx, query)
		if err != nil {
			writeCacheUnavailable(w, r, deps, err)
			return
		}
		if cacheStaleAndControllerOffline(deps) {
			deps.Logger.Warn("list answered without a fresh cache", "module", "api", "event", "cache_stale_controller_offline", "endpoint", "networks")
			writeError(w, r, CodeUnifiUnreachable, "UniFi controller is not reachable", map[string]any{
				"reason":                  "cache_stale",
				"last_successful_sync_at": formatTimePtr(deps.Sync.Status().LastSuccessfulSyncAt),
			})
			return
		}

		body := networkListBody{
			Data:       make([]networkBody, 0, len(page.Items)),
			Pagination: newPaginationBody(query.Page, query.PageSize, page.Total),
		}
		for _, network := range page.Items {
			body.Data = append(body.Data, networkBodyOf(network))
		}
		writeJSON(w, http.StatusOK, body)
	}
}

// handleGetNetwork serves GET /networks/{id} from the local cache.
func handleGetNetwork(deps Dependencies) http.HandlerFunc {
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

		network, found, err := deps.Inventory.GetNetwork(ctx, id)
		if err != nil {
			writeCacheUnavailable(w, r, deps, err)
			return
		}
		if cacheStaleAndControllerOffline(deps) {
			deps.Logger.Warn("get answered without a fresh cache", "module", "api", "event", "cache_stale_controller_offline", "endpoint", "networks")
			writeError(w, r, CodeUnifiUnreachable, "UniFi controller is not reachable", map[string]any{
				"reason":                  "cache_stale",
				"last_successful_sync_at": formatTimePtr(deps.Sync.Status().LastSuccessfulSyncAt),
			})
			return
		}
		if !found {
			writeError(w, r, CodeNotFound, "network not found", map[string]any{"resource": "network"})
			return
		}

		writeJSON(w, http.StatusOK, networkBodyOf(network))
	}
}
