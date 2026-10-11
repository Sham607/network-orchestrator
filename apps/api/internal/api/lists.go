package api

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Shared plumbing of the list endpoints (GET /sites, GET /devices): the
// pagination parameters the contract defines once in
// components/parameters (Page, PageSize, Order) and the 400 body every
// contract violation produces.

const (
	// listTimeout bounds one cache query so a stuck database cannot hold a
	// request open indefinitely.
	listTimeout = 5 * time.Second
	// defaultPageSize and maxPageSize mirror the PageSize parameter. A larger
	// page_size is rejected, never clamped: silently answering a different
	// question than the caller asked is worse than a 400.
	defaultPageSize = 50
	maxPageSize     = 200
)

// paginationBody is the contract's Pagination schema.
type paginationBody struct {
	Page       int `json:"page"`
	PageSize   int `json:"page_size"`
	Total      int `json:"total"`
	TotalPages int `json:"total_pages"`
}

// newPaginationBody reports the page actually served. total_pages is 0 for an
// empty result, which is what the contract's `empty` example shows.
func newPaginationBody(page, pageSize, total int) paginationBody {
	totalPages := 0
	if total > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	return paginationBody{Page: page, PageSize: pageSize, Total: total, TotalPages: totalPages}
}

// listParams are the parameters every list endpoint shares.
type listParams struct {
	Page     int
	PageSize int
	Order    string
}

// parseListParams validates page, page_size and order, recording every
// violation in fields. A missing parameter keeps its documented default; an
// unparsable or out-of-range one is reported rather than corrected.
func parseListParams(r *http.Request, fields fieldErrors) listParams {
	params := listParams{Page: 1, PageSize: defaultPageSize, Order: "asc"}
	values := r.URL.Query()

	if raw := values.Get("page"); raw != "" {
		page, err := strconv.ParseInt(raw, 10, 64)
		switch {
		case err != nil:
			fields["page"] = "must be an integer"
		case page < 1:
			fields["page"] = "must be at least 1"
		default:
			params.Page = int(page)
		}
	}

	if raw := values.Get("page_size"); raw != "" {
		size, err := strconv.ParseInt(raw, 10, 64)
		switch {
		case err != nil:
			fields["page_size"] = "must be an integer"
		case size < 1 || size > maxPageSize:
			fields["page_size"] = fmt.Sprintf("must be between 1 and %d", maxPageSize)
		default:
			params.PageSize = int(size)
		}
	}

	if raw := values.Get("order"); raw != "" {
		if validateEnum(fields, "order", raw, "asc", "desc") {
			params.Order = raw
		}
	}

	// A page large enough to overflow the offset would reach the database as a
	// negative OFFSET, so it is a validation error rather than a 503.
	if _, ok := fields["page"]; !ok && int64(params.Page-1) > math.MaxInt64/int64(params.PageSize) {
		fields["page"] = "is too large"
	}

	return params
}

// fieldErrors collects contract violations so one 400 can name every offending
// parameter instead of failing on the first.
type fieldErrors map[string]string

func (f fieldErrors) invalid() bool { return len(f) > 0 }

// message mirrors the contract's `invalid_page` example ("page_size must be
// between 1 and 200") when a single parameter is at fault.
func (f fieldErrors) message() string {
	if len(f) == 1 {
		for name, problem := range f {
			return name + " " + problem
		}
	}
	return "request does not match the API contract"
}

// validateEnum records an enum violation and reports whether value is allowed.
func validateEnum(fields fieldErrors, name, value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	fields[name] = "must be one of " + strings.Join(allowed, ", ")
	return false
}

// validateAllowedParams validates that every query parameter in values is in allowed.
// Any undeclared parameter is flagged with a 400 validation_failed (ZENI-19).
func validateAllowedParams(fields fieldErrors, values url.Values, allowed ...string) {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, a := range allowed {
		allowedSet[a] = struct{}{}
	}
	for name := range values {
		if _, ok := allowedSet[name]; !ok {
			fields[name] = "is not a recognized query parameter"
		}
	}
}

// validateMaxLength enforces a maxLength constraint of the contract.
func validateMaxLength(fields fieldErrors, name, value string, max int) bool {
	if utf8.RuneCountInString(value) > max {
		fields[name] = fmt.Sprintf("must be at most %d characters", max)
		return false
	}
	return true
}

var (
	uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	macPattern  = regexp.MustCompile(`^[0-9a-fA-F]{2}(:[0-9a-fA-F]{2}){5}$`)
)

// validateUUID enforces `format: uuid`, so an id that can never match is a 400
// rather than an empty page.
func validateUUID(fields fieldErrors, name, value string) bool {
	if !uuidPattern.MatchString(value) {
		fields[name] = "must be a UUID"
		return false
	}
	return true
}

// validateMAC enforces the colon-separated form the contract documents. The
// value is matched exactly once normalized, so the case is not part of the
// comparison.
func validateMAC(fields fieldErrors, name, value string) bool {
	if !macPattern.MatchString(value) {
		fields[name] = "must be a colon-separated MAC address"
		return false
	}
	return true
}

// validateBool enforces `format: boolean`, returning the parsed value and
// whether it was valid (mirroring validateUUID's two-step style so a caller
// can assign the field only on success).
func validateBool(fields fieldErrors, name, value string) (bool, bool) {
	switch value {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		fields[name] = "must be a boolean"
		return false, false
	}
}

// validateIntRange enforces an integer range [min, max], returning the parsed value
// and whether it was valid.
func validateIntRange(fields fieldErrors, name, value string, min, max int) (int, bool) {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		fields[name] = "must be an integer"
		return 0, false
	}
	if n < int64(min) || n > int64(max) {
		fields[name] = fmt.Sprintf("must be between %d and %d", min, max)
		return 0, false
	}
	return int(n), true
}

// writeValidationFailed renders the contract's 400 body.
func writeValidationFailed(w http.ResponseWriter, r *http.Request, fields fieldErrors) {
	writeError(w, r, CodeValidationFailed, fields.message(), map[string]any{"fields": fields})
}

// cacheStaleAndControllerOffline reports the one state the list endpoints must
// not answer with data: the cache is older than SYNC_STALE_AFTER (or was never
// filled) *and* the controller is not answering, so nothing can refresh it. The
// endpoints answer 503 unifi_unreachable — the controller is the broken
// dependency — and the dashboard keeps rendering what it already has, with
// /sync/status telling it how old that is (ZENI-15, ADR-004).
//
// A stale cache whose controller still answers is served normally, with per-row
// synced_at: staleness is announced, not hidden.
func cacheStaleAndControllerOffline(deps Dependencies) bool {
	if !deps.Config.UnifiEnabled || deps.Sync == nil {
		return false
	}
	status := deps.Sync.Status()
	return status.Stale && !status.Reachable
}

// writeCacheUnavailable is the 503 shared by both list endpoints: the cache
// query itself failed (a dependency of the API is down).
func writeCacheUnavailable(w http.ResponseWriter, r *http.Request, deps Dependencies, err error) {
	deps.Logger.Error("cache query failed", "module", "api", "event", "cache_query_failed", "error", err)
	writeError(w, r, CodeServiceUnavailable, "the inventory cache is not reachable", map[string]any{
		"reason": "database_unreachable",
	})
}
