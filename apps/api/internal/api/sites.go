package api

import (
	"context"
	"net/http"

	"github.com/Sham607/network-orchestrator/apps/api/internal/inventory"
)

// siteBody is the contract's Site schema. Every property is present, with null
// where the cache holds nothing, so the dashboard can tell "unknown" from
// "field the backend forgot" (openapi/mock-expectations.md §4).
type siteBody struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Timezone    *string `json:"timezone"`
	GatewayMAC  *string `json:"gateway_mac"`
	SyncedAt    string  `json:"synced_at"`
}

// siteListBody is the contract's SiteList schema.
type siteListBody struct {
	Data       []siteBody     `json:"data"`
	Pagination paginationBody `json:"pagination"`
}

// handleListSites serves GET /sites from the local cache. It never calls UniFi:
// a slow or offline controller degrades to stale rows plus their synced_at, not
// to a failure (docs/adr/003-architecture.md §1, §3).
func handleListSites(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fields := fieldErrors{}
		params := parseListParams(r, fields)
		values := r.URL.Query()

		query := inventory.SiteQuery{
			Page:     params.Page,
			PageSize: params.PageSize,
			Order:    params.Order,
			Sort:     "name", // the contract's default
		}
		if raw := values.Get("sort"); raw != "" && validateEnum(fields, "sort", raw, "name", "synced_at") {
			query.Sort = raw
		}
		if raw := values.Get("q"); raw != "" && validateMaxLength(fields, "q", raw, 128) {
			query.Query = raw
		}
		if fields.invalid() {
			writeValidationFailed(w, r, fields)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), listTimeout)
		defer cancel()

		page, err := deps.Inventory.ListSites(ctx, query)
		if err != nil {
			writeCacheUnavailable(w, r, deps, err)
			return
		}
		// The database answered first, so a dead database is still reported as
		// service_unavailable; only a controller we cannot reach — with a cache too
		// old to trust — is unifi_unreachable (ADR-004).
		if cacheStaleAndControllerOffline(deps) {
			deps.Logger.Warn("list answered without a fresh cache", "module", "api", "event", "cache_stale_controller_offline", "endpoint", "sites")
			writeError(w, r, CodeUnifiUnreachable, "UniFi controller is not reachable", map[string]any{
				"reason":                  "cache_stale",
				"last_successful_sync_at": formatTimePtr(deps.Sync.Status().LastSuccessfulSyncAt),
			})
			return
		}

		body := siteListBody{
			Data:       make([]siteBody, 0, len(page.Items)),
			Pagination: newPaginationBody(query.Page, query.PageSize, page.Total),
		}
		for _, site := range page.Items {
			body.Data = append(body.Data, siteBody{
				ID:          site.ID,
				Name:        site.Name,
				Description: site.Description,
				Timezone:    site.Timezone,
				GatewayMAC:  site.GatewayMAC,
				SyncedAt:    formatTime(site.SyncedAt),
			})
		}
		writeJSON(w, http.StatusOK, body)
	}
}
