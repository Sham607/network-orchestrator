//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// uuidRe mirrors the contract's `format: uuid`. The cache assigns its own ids,
// so the harness cannot hard-code them; it validates the shape instead.
var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// deref renders an optional string for messages without printing a pointer.
func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

type pagination struct {
	Page       int `json:"page"`
	PageSize   int `json:"page_size"`
	Total      int `json:"total"`
	TotalPages int `json:"total_pages"`
}

type siteRow struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Timezone    *string `json:"timezone"`
	GatewayMAC  *string `json:"gateway_mac"`
	SyncedAt    string  `json:"synced_at"`
}

type deviceRow struct {
	ID         string  `json:"id"`
	SiteID     string  `json:"site_id"`
	Name       *string `json:"name"`
	MAC        *string `json:"mac"`
	Model      *string `json:"model"`
	DeviceType string  `json:"device_type"`
	Status     *string `json:"status"`
	Radios     []any   `json:"radios"`
	SyncedAt   string  `json:"synced_at"`
}

type siteList struct {
	Data       []siteRow  `json:"data"`
	Pagination pagination `json:"pagination"`
}

type deviceList struct {
	Data       []deviceRow `json:"data"`
	Pagination pagination  `json:"pagination"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details struct {
		Reason string            `json:"reason"`
		Fields map[string]string `json:"fields"`
	} `json:"details"`
}

// TestVerticalSlice is the first full slice: a request walks GET /sites ->
// GET /devices against the cache the sync worker filled from a mocked UniFi,
// with /health, /ready and /sync/status telling the operator it is fresh. Every
// 200 body is validated against openapi/openapi.yaml as it is read.
func TestVerticalSlice(t *testing.T) {
	s := startStack(t, stackOptions{unifiEnabled: true})

	// /health is liveness only: 200 with a request-id, no dependency.
	resp, err := s.client.Get(s.baseURL + "/api/v1/health")
	if err != nil {
		t.Fatalf("GET /api/v1/health: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/health status = %d, want 200", resp.StatusCode)
	}
	if resp.Header.Get("X-Request-Id") == "" {
		t.Error("/health is missing the X-Request-Id correlation header")
	}

	s.waitForSynced(e2eDeviceTotal)

	var sites siteList
	s.getJSON("/api/v1/sites", &sites)
	if sites.Pagination.Total != 1 || len(sites.Data) != 1 {
		t.Fatalf("sites total = %d (%d rows), want 1", sites.Pagination.Total, len(sites.Data))
	}
	if sites.Data[0].Name != e2eSiteName {
		t.Errorf("site name = %q, want %q", sites.Data[0].Name, e2eSiteName)
	}
	if !uuidRe.MatchString(sites.Data[0].ID) {
		t.Errorf("site id %q is not a UUID", sites.Data[0].ID)
	}
	if _, err := time.Parse(time.RFC3339, sites.Data[0].SyncedAt); err != nil {
		t.Errorf("site synced_at %q is not RFC3339: %v", sites.Data[0].SyncedAt, err)
	}
	siteID := sites.Data[0].ID

	var devices deviceList
	s.getJSON("/api/v1/devices", &devices)
	if devices.Pagination.Total != e2eDeviceTotal || len(devices.Data) != e2eDeviceTotal {
		t.Fatalf("devices total = %d (%d rows), want %d", devices.Pagination.Total, len(devices.Data), e2eDeviceTotal)
	}
	types := map[string]bool{}
	for _, d := range devices.Data {
		if d.SiteID != siteID {
			t.Errorf("device %q site_id = %s, want %s", deref(d.Name), d.SiteID, siteID)
		}
		if d.MAC == nil || *d.MAC != strings.ToLower(*d.MAC) {
			t.Errorf("device %v mac is not normalized lower-case: %v", d.Name, d.MAC)
		}
		types[d.DeviceType] = true
	}
	for _, want := range []string{"ap", "switch", "gateway"} {
		if !types[want] {
			t.Errorf("no device classified as %q; got types %v", want, types)
		}
	}

	// The cache on the wire is what the dashboard navigates with: site id from
	// /sites drives the /devices filter.
	var filtered deviceList
	s.getJSON("/api/v1/devices?site_id="+siteID, &filtered)
	if filtered.Pagination.Total != e2eDeviceTotal {
		t.Errorf("devices for site %s total = %d, want %d", siteID, filtered.Pagination.Total, e2eDeviceTotal)
	}

	var ready struct {
		Status       string `json:"status"`
		UnifiEnabled bool   `json:"unifi_enabled"`
		UnifiMode    string `json:"unifi_mode"`
		Stale        bool   `json:"stale"`
	}
	s.getJSON("/api/v1/ready", &ready)
	if ready.Status != "ready" || !ready.UnifiEnabled || ready.Stale {
		t.Errorf("/ready = %+v, want ready with unifi_enabled and no staleness", ready)
	}

	var status struct {
		WorkerRunning        bool    `json:"worker_running"`
		Stale                bool    `json:"stale"`
		LastSuccessfulSyncAt *string `json:"last_successful_sync_at"`
		Unifi                struct {
			Reachable bool   `json:"reachable"`
			Surface   string `json:"surface"`
		} `json:"unifi"`
	}
	s.getJSON("/api/v1/sync/status", &status)
	if !status.WorkerRunning || status.Stale || status.LastSuccessfulSyncAt == nil {
		t.Errorf("/sync/status = %+v, want a running, fresh worker", status)
	}
	if !status.Unifi.Reachable || status.Unifi.Surface != "console" {
		t.Errorf("/sync/status unifi = %+v, want reachable console surface", status.Unifi)
	}
}

// TestStatusFilterUsesStatusNotState proves the contract's parameter name. It
// asserts that `state=OFFLINE` is rejected with 400 validation_failed naming
// the undeclared parameter (ZENI-19).
func TestStatusFilterUsesStatusNotState(t *testing.T) {
	s := startStack(t, stackOptions{unifiEnabled: true})
	s.waitForSynced(e2eDeviceTotal)

	var byStatus deviceList
	s.getJSON("/api/v1/devices?status=OFFLINE", &byStatus)
	if byStatus.Pagination.Total != e2eOfflineCount {
		t.Errorf("status=OFFLINE total = %d, want %d", byStatus.Pagination.Total, e2eOfflineCount)
	}
	for _, d := range byStatus.Data {
		if d.Status == nil || *d.Status != "OFFLINE" {
			t.Errorf("status=OFFLINE returned %v", d.Status)
		}
	}

	// The contract stores UniFi state upper-case; the handler normalizes, so a
	// lower-case caller value must match the same rows.
	var lower deviceList
	s.getJSON("/api/v1/devices?status=offline", &lower)
	if lower.Pagination.Total != e2eOfflineCount {
		t.Errorf("status=offline total = %d, want %d (case-normalized)", lower.Pagination.Total, e2eOfflineCount)
	}

	// `state` is not the contract's parameter. Undeclared query parameters must be
	// rejected with 400 validation_failed.
	code, body := s.get("/api/v1/devices?state=OFFLINE")
	if code != http.StatusBadRequest {
		t.Fatalf("state=OFFLINE status = %d, want 400\nbody: %s", code, body)
	}
	var errResp errorBody
	if err := json.Unmarshal(body, &errResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if errResp.Code != "validation_failed" {
		t.Errorf("code = %q, want validation_failed", errResp.Code)
	}
	if errResp.Details.Fields == nil || errResp.Details.Fields["state"] == "" {
		t.Errorf("expected 'state' in details.fields: %+v", errResp.Details.Fields)
	}
}

func TestListPagination(t *testing.T) {
	s := startStack(t, stackOptions{unifiEnabled: true})
	s.waitForSynced(e2eDeviceTotal)

	var first deviceList
	s.getJSON("/api/v1/devices?page_size=2", &first)
	if len(first.Data) != 2 || first.Pagination.Total != e2eDeviceTotal || first.Pagination.TotalPages != 2 {
		t.Fatalf("page_size=2 = %d rows, total %d, total_pages %d; want 2/%d/2",
			len(first.Data), first.Pagination.Total, first.Pagination.TotalPages, e2eDeviceTotal)
	}

	var second deviceList
	s.getJSON("/api/v1/devices?page=2&page_size=2", &second)
	if len(second.Data) != 1 || second.Pagination.Page != 2 {
		t.Fatalf("page=2&page_size=2 = %d rows on page %d; want 1 row on page 2", len(second.Data), second.Pagination.Page)
	}

	// A page beyond the data is an empty 200 with the correct total, not a 404.
	var beyond deviceList
	s.getJSON("/api/v1/devices?page=5&page_size=2", &beyond)
	if len(beyond.Data) != 0 || beyond.Pagination.Total != e2eDeviceTotal {
		t.Fatalf("page=5 = %d rows, total %d; want 0 rows and total %d", len(beyond.Data), beyond.Pagination.Total, e2eDeviceTotal)
	}
}

// TestValidationErrors covers the negative/boundary matrix: every contract
// violation is a 400 validation_failed naming the offending field, and nothing
// is silently clamped or ignored.
func TestValidationErrors(t *testing.T) {
	s := startStack(t, stackOptions{unifiEnabled: true})
	s.waitForSynced(e2eDeviceTotal)

	cases := []struct {
		name  string
		path  string
		field string
	}{
		{"device page_size above max", "/api/v1/devices?page_size=201", "page_size"},
		{"device page_size zero", "/api/v1/devices?page_size=0", "page_size"},
		{"device page_size not a number", "/api/v1/devices?page_size=abc", "page_size"},
		{"device page below one", "/api/v1/devices?page=0", "page"},
		{"device page not a number", "/api/v1/devices?page=abc", "page"},
		{"device sort unknown", "/api/v1/devices?sort=bogus", "sort"},
		{"device order unknown", "/api/v1/devices?order=bogus", "order"},
		{"device type unknown", "/api/v1/devices?device_type=bogus", "device_type"},
		{"device site_id not a uuid", "/api/v1/devices?site_id=not-a-uuid", "site_id"},
		{"device mac malformed", "/api/v1/devices?mac=zz:zz", "mac"},
		{"device unknown query param", "/api/v1/devices?bogus_param=1", "bogus_param"},
		{"site page_size above max", "/api/v1/sites?page_size=201", "page_size"},
		{"site sort unknown", "/api/v1/sites?sort=bogus", "sort"},
		{"site q too long", "/api/v1/sites?q=" + strings.Repeat("a", 129), "q"},
		{"site unknown query param", "/api/v1/sites?bogus_param=1", "bogus_param"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := s.get(tc.path)
			if code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400\nbody: %s", code, body)
			}
			var e errorBody
			if err := json.Unmarshal(body, &e); err != nil {
				t.Fatalf("decode error body: %v\nbody: %s", err, body)
			}
			if e.Code != "validation_failed" {
				t.Errorf("code = %q, want validation_failed", e.Code)
			}
			if _, ok := e.Details.Fields[tc.field]; !ok {
				t.Errorf("details.fields = %v, want the offending field %q", e.Details.Fields, tc.field)
			}
			s.assertContract("Error", body)
		})
	}
}

func TestUnknownRouteIsNotFound(t *testing.T) {
	s := startStack(t, stackOptions{unifiEnabled: false})
	code, body := s.get("/api/v1/nope")
	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404\nbody: %s", code, body)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	s := startStack(t, stackOptions{unifiEnabled: false})
	for _, path := range []string{"/api/v1/sites", "/api/v1/devices"} {
		code, body, err := s.do(http.MethodPost, path, []byte(`{}`))
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		if code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s status = %d, want 405\nbody: %s", path, code, body)
		}
	}
}

// TestEndpointsAreUnauthenticatedInPhase1 documents a deliberate deferral: the
// contract declares 401 for these routes, but operator auth (FR-6) is not wired
// yet (ZENI-15 recorded the same). The test pins the current behaviour so the
// day auth lands, this test fails loudly and is updated with it.
func TestEndpointsAreUnauthenticatedInPhase1(t *testing.T) {
	s := startStack(t, stackOptions{unifiEnabled: true})
	s.waitForSynced(e2eDeviceTotal)
	code, body := s.get("/api/v1/sites")
	if code != http.StatusOK {
		t.Fatalf("unauthenticated GET /sites status = %d, want 200 while FR-6 is deferred\nbody: %s", code, body)
	}
}

// TestConcurrentReadsWhileSyncing is the race edge case: reads must stay
// consistent and never 5xx while the worker rewrites the cache underneath them.
func TestConcurrentReadsWhileSyncing(t *testing.T) {
	s := startStack(t, stackOptions{unifiEnabled: true, interval: time.Second, staleAfter: 30 * time.Second})
	s.waitForSynced(e2eDeviceTotal)

	const workers = 24
	var wg sync.WaitGroup
	errs := make(chan string, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			path := "/api/v1/devices"
			if i%2 == 0 {
				path = "/api/v1/sites"
			}
			code, body, err := s.do(http.MethodGet, path, nil)
			if err != nil {
				errs <- path + ": " + err.Error()
				return
			}
			if code != http.StatusOK {
				errs <- path + ": status " + http.StatusText(code)
				return
			}
			var page struct {
				Pagination pagination `json:"pagination"`
			}
			if err := json.Unmarshal(body, &page); err != nil {
				errs <- path + ": decode: " + err.Error()
				return
			}
			want := 1
			if path == "/api/v1/devices" {
				want = e2eDeviceTotal
			}
			if page.Pagination.Total != want {
				errs <- path + ": inconsistent total"
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

// TestReadsDoNotCallTheController proves the cache boundary: a list request is
// answered from Postgres even when every UniFi call would block for seconds,
// and it does not add a single controller request.
func TestReadsDoNotCallTheController(t *testing.T) {
	s := startStack(t, stackOptions{unifiEnabled: true})
	s.waitForSynced(e2eDeviceTotal)

	s.mock.control("slow")
	defer s.mock.reset()
	before := s.mock.requestCount()

	start := time.Now()
	code, body := s.get("/api/v1/devices")
	elapsed := time.Since(start)
	if code != http.StatusOK {
		t.Fatalf("GET /devices while the controller is slow: status %d\nbody: %s", code, body)
	}
	if elapsed > 2*time.Second {
		t.Errorf("GET /devices took %s with a 3s controller: the read path waited on UniFi", elapsed)
	}
	if after := s.mock.requestCount(); after != before {
		t.Errorf("GET /devices called the controller: request count %d -> %d", before, after)
	}
}
