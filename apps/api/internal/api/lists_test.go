package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sham607/network-orchestrator/apps/api/internal/config"
	"github.com/Sham607/network-orchestrator/apps/api/internal/inventory"
)

// fakeInventory stands in for the Postgres cache: the handler tests assert what
// the transport does with a page (validate, map, render), not how the cache is
// queried — that is covered by the real-Postgres tests in package inventory.
type fakeInventory struct {
	sites      inventory.SitePage
	siteErr    error
	devices    inventory.DevicePage
	deviceErr  error
	clients    inventory.ClientPage
	clientErr  error
	networks   inventory.NetworkPage
	networkErr error

	getClient      inventory.Client
	getClientFound bool
	getClientErr   error

	getNetwork      inventory.Network
	getNetworkFound bool
	getNetworkErr   error

	siteQueries     []inventory.SiteQuery
	deviceQueries   []inventory.DeviceQuery
	clientQueries   []inventory.ClientQuery
	networkQueries  []inventory.NetworkQuery
	getClientCalls  []string
	getNetworkCalls []string
}

func (f *fakeInventory) ListSites(_ context.Context, q inventory.SiteQuery) (inventory.SitePage, error) {
	f.siteQueries = append(f.siteQueries, q)
	return f.sites, f.siteErr
}

func (f *fakeInventory) ListDevices(_ context.Context, q inventory.DeviceQuery) (inventory.DevicePage, error) {
	f.deviceQueries = append(f.deviceQueries, q)
	return f.devices, f.deviceErr
}

func (f *fakeInventory) ListClients(_ context.Context, q inventory.ClientQuery) (inventory.ClientPage, error) {
	f.clientQueries = append(f.clientQueries, q)
	return f.clients, f.clientErr
}

func (f *fakeInventory) GetClient(_ context.Context, id string) (inventory.Client, bool, error) {
	f.getClientCalls = append(f.getClientCalls, id)
	return f.getClient, f.getClientFound, f.getClientErr
}

func (f *fakeInventory) ListNetworks(_ context.Context, q inventory.NetworkQuery) (inventory.NetworkPage, error) {
	f.networkQueries = append(f.networkQueries, q)
	return f.networks, f.networkErr
}

func (f *fakeInventory) GetNetwork(_ context.Context, id string) (inventory.Network, bool, error) {
	f.getNetworkCalls = append(f.getNetworkCalls, id)
	return f.getNetwork, f.getNetworkFound, f.getNetworkErr
}

func (f *fakeInventory) siteQuery() inventory.SiteQuery {
	if len(f.siteQueries) == 0 {
		return inventory.SiteQuery{}
	}
	return f.siteQueries[len(f.siteQueries)-1]
}

func (f *fakeInventory) deviceQuery() inventory.DeviceQuery {
	if len(f.deviceQueries) == 0 {
		return inventory.DeviceQuery{}
	}
	return f.deviceQueries[len(f.deviceQueries)-1]
}

func (f *fakeInventory) clientQuery() inventory.ClientQuery {
	if len(f.clientQueries) == 0 {
		return inventory.ClientQuery{}
	}
	return f.clientQueries[len(f.clientQueries)-1]
}

func (f *fakeInventory) networkQuery() inventory.NetworkQuery {
	if len(f.networkQueries) == 0 {
		return inventory.NetworkQuery{}
	}
	return f.networkQueries[len(f.networkQueries)-1]
}

func (f *fakeInventory) called() bool {
	return len(f.siteQueries)+len(f.deviceQueries)+len(f.clientQueries)+len(f.networkQueries)+len(f.getClientCalls)+len(f.getNetworkCalls) > 0
}

// listRouter serves the list endpoints with UniFi disabled: the cache is the
// only source, and the controller cannot influence the answer.
func listRouter(t *testing.T, inv Inventory) http.Handler {
	t.Helper()
	return testRouterWithInventory(t, config.Config{
		UnifiMode:      config.UnifiModeCloud,
		SyncStaleAfter: 3 * time.Minute,
	}, fakeDB{}, nil, inv)
}

func get(t *testing.T, handler http.Handler, url string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, url, nil))
	return rr
}

func decodeBody(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v (body: %s)", err, rr.Body.String())
	}
	return body
}

func decodeError(t *testing.T, rr *httptest.ResponseRecorder) ErrorBody {
	t.Helper()
	var body ErrorBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v (body: %s)", err, rr.Body.String())
	}
	return body
}

// fieldProblem returns details.fields[name] from a validation error body.
func fieldProblem(t *testing.T, body ErrorBody, name string) string {
	t.Helper()
	fields, ok := body.Details["fields"].(map[string]any)
	if !ok {
		t.Fatalf("details.fields = %v, want an object", body.Details["fields"])
	}
	problem, _ := fields[name].(string)
	return problem
}

func ptr[T any](v T) *T { return &v }

func sampleSites() inventory.SitePage {
	synced := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	return inventory.SitePage{
		Total: 3,
		Items: []inventory.Site{
			{
				ID: "1f0c9d0a-3b2e-4c5f-8a91-6d7e8f9a0b1c", Name: "HQ",
				Description: ptr("Head office"), Timezone: ptr("Asia/Jakarta"),
				GatewayMAC: ptr("aa:bb:cc:dd:ee:01"), SyncedAt: synced,
			},
			// A site UniFi reported with nothing but an identity: the contract
			// allows null here, and null is what "unknown" must look like.
			{ID: "2f0c9d0a-3b2e-4c5f-8a91-6d7e8f9a0b1d", Name: "Branch", SyncedAt: synced},
		},
	}
}

func sampleDevices() inventory.DevicePage {
	synced := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	adopted := synced.Add(-48 * time.Hour)
	return inventory.DevicePage{
		Total: 2,
		Items: []inventory.Device{
			{
				ID: "2a3b4c5d-6e7f-4a8b-9c0d-1e2f3a4b5c6d", SiteID: "1f0c9d0a-3b2e-4c5f-8a91-6d7e8f9a0b1c",
				Name: ptr("U6 Pro"), MAC: ptr("aa:bb:cc:00:00:01"), Model: ptr("U6 Pro"),
				DeviceType: "ap", Status: ptr("ONLINE"), FirmwareVersion: ptr("6.6.55"),
				IPAddress: ptr("10.0.0.11"), AdoptedAt: &adopted, SyncedAt: synced,
			},
			{
				ID: "3a3b4c5d-6e7f-4a8b-9c0d-1e2f3a4b5c6e", SiteID: "1f0c9d0a-3b2e-4c5f-8a91-6d7e8f9a0b1c",
				DeviceType: "other", SyncedAt: synced,
			},
		},
	}
}

func sampleClients() inventory.ClientPage {
	synced := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	connectedAt := synced.Add(-2 * time.Hour)
	firstSeen := synced.Add(-72 * time.Hour)
	return inventory.ClientPage{
		Total: 2,
		Items: []inventory.Client{
			{
				ID: "4c5d6e7f-8a9b-4c0d-1e2f-3a4b5c6d7e8f", SiteID: "1f0c9d0a-3b2e-4c5f-8a91-6d7e8f9a0b1c",
				MAC: ptr("aa:bb:cc:dd:ee:03"), Hostname: ptr("ivan-laptop"), IPAddress: ptr("10.20.5.44"),
				ClientType: ptr("WIRELESS"), AccessType: ptr("DEFAULT"),
				UplinkDeviceID: ptr("2a3b4c5d-6e7f-4a8b-9c0d-1e2f3a4b5c6d"),
				IsConnected:    true, ConnectedAt: &connectedAt,
				FirstSeen: firstSeen, LastSeen: synced, SyncedAt: synced,
			},
			// A historical row the worker marked disconnected: the contract keeps
			// it (docs/data-model.md §4.1), with ssid/signal_dbm null (phase 1).
			{
				ID: "5c5d6e7f-8a9b-4c0d-1e2f-3a4b5c6d7e90", SiteID: "1f0c9d0a-3b2e-4c5f-8a91-6d7e8f9a0b1c",
				IsConnected: false, DisconnectedAt: &synced,
				FirstSeen: firstSeen, LastSeen: synced, SyncedAt: synced,
			},
		},
	}
}

func sampleNetworks() inventory.NetworkPage {
	synced := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	return inventory.NetworkPage{
		Total: 2,
		Items: []inventory.Network{
			{
				ID:           "5d6e7f8a-9b0c-4d1e-2f3a-4b5c6d7e8f9a",
				SiteID:       "1f0c9d0a-3b2e-4c5f-8a91-6d7e8f9a0b1c",
				Name:         "Default",
				VLANID:       ptr(1),
				Management:   ptr("GATEWAY"),
				Enabled:      ptr(true),
				IsDefault:    ptr(true),
				Origin:       ptr("SYSTEM_DEFINED"),
				Configurable: ptr(true),
				SyncedAt:     synced,
			},
			{
				ID:           "6d6e7f8a-9b0c-4d1e-2f3a-4b5c6d7e8f9b",
				SiteID:       "1f0c9d0a-3b2e-4c5f-8a91-6d7e8f9a0b1c",
				Name:         "Guest",
				VLANID:       ptr(20),
				Management:   ptr("SWITCH"),
				Enabled:      ptr(true),
				IsDefault:    ptr(false),
				Origin:       ptr("USER_DEFINED"),
				Configurable: ptr(true),
				SyncedAt:     synced,
			},
		},
	}
}

// --- GET /sites ------------------------------------------------------------

func TestListSitesServesTheCachedPage(t *testing.T) {
	inv := &fakeInventory{sites: sampleSites()}
	rr := get(t, listRouter(t, inv), "/api/v1/sites")

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
	}
	body := decodeBody(t, rr)
	pagination, _ := body["pagination"].(map[string]any)
	// total comes from the cache (3), while the page carries what fits.
	if pagination["total"] != float64(3) || pagination["total_pages"] != float64(1) {
		t.Fatalf("pagination = %v, want total 3 and total_pages 1", pagination)
	}

	data, _ := body["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("data has %d rows, want 2", len(data))
	}
	first, _ := data[0].(map[string]any)
	if first["name"] != "HQ" || first["synced_at"] != "2026-03-01T08:00:00Z" {
		t.Fatalf("row = %v, want HQ with an RFC3339 synced_at", first)
	}
	// The raw UniFi payload is never exposed, so unknown fields are null.
	second, _ := data[1].(map[string]any)
	for _, key := range []string{"description", "timezone", "gateway_mac"} {
		if value, present := second[key]; !present || value != nil {
			t.Errorf("row 2 %s = %v (present: %v), want an explicit null", key, value, present)
		}
	}
}

func TestListSitesDefaultsToTheFirstFifty(t *testing.T) {
	inv := &fakeInventory{sites: sampleSites()}
	rr := get(t, listRouter(t, inv), "/api/v1/sites")

	if got := inv.siteQuery(); got.Page != 1 || got.PageSize != 50 || got.Order != "asc" || got.Sort != "name" {
		t.Fatalf("query = %+v, want page 1, page_size 50, order asc, sort name (the documented defaults)", got)
	}
	pagination, _ := decodeBody(t, rr)["pagination"].(map[string]any)
	if pagination["page"] != float64(1) || pagination["page_size"] != float64(50) {
		t.Fatalf("pagination = %v, want the page actually served", pagination)
	}
}

func TestListSitesForwardsFiltersAndSort(t *testing.T) {
	inv := &fakeInventory{sites: sampleSites()}
	rr := get(t, listRouter(t, inv), "/api/v1/sites?page=2&page_size=10&sort=synced_at&order=desc&q=Head")

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
	}
	want := inventory.SiteQuery{Page: 2, PageSize: 10, Sort: "synced_at", Order: "desc", Query: "Head"}
	if got := inv.siteQuery(); got != want {
		t.Fatalf("query = %+v, want %+v", got, want)
	}
}

func TestListSitesRejectsAnOverlongQuery(t *testing.T) {
	inv := &fakeInventory{sites: sampleSites()}
	rr := get(t, listRouter(t, inv), "/api/v1/sites?q="+repeat("a", 129))

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
	body := decodeError(t, rr)
	if body.Code != CodeValidationFailed {
		t.Fatalf("code = %q, want %q", body.Code, CodeValidationFailed)
	}
	if problem := fieldProblem(t, body, "q"); problem == "" {
		t.Fatal("details.fields.q is missing")
	}
	if inv.called() {
		t.Fatal("an invalid request reached the cache")
	}
}

func TestListSitesRejectsUnknownQueryParameters(t *testing.T) {
	inv := &fakeInventory{sites: sampleSites()}
	rr := get(t, listRouter(t, inv), "/api/v1/sites?unknown_field=true")

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
	body := decodeError(t, rr)
	if body.Code != CodeValidationFailed {
		t.Fatalf("code = %q, want %q", body.Code, CodeValidationFailed)
	}
	if problem := fieldProblem(t, body, "unknown_field"); problem != "is not a recognized query parameter" {
		t.Fatalf("details.fields.unknown_field = %q, want 'is not a recognized query parameter'", problem)
	}
	if inv.called() {
		t.Fatal("an invalid request reached the cache")
	}
}

// --- pagination validation -------------------------------------------------

// TestPageSizeOverTheMaximumIsRejectedNotClamped pins the decision: silently
// serving 200 rows for `page_size=1000` would answer a different question than
// the caller asked.
func TestPageSizeOverTheMaximumIsRejectedNotClamped(t *testing.T) {
	for _, endpoint := range []string{"sites", "devices", "clients"} {
		inv := &fakeInventory{sites: sampleSites(), devices: sampleDevices(), clients: sampleClients()}
		rr := get(t, listRouter(t, inv), "/api/v1/"+endpoint+"?page_size=201")

		if rr.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want %d (body: %s)", endpoint, rr.Code, http.StatusBadRequest, rr.Body.String())
		}
		body := decodeError(t, rr)
		if body.Code != CodeValidationFailed {
			t.Fatalf("%s: code = %q, want %q", endpoint, body.Code, CodeValidationFailed)
		}
		if problem := fieldProblem(t, body, "page_size"); problem != "must be between 1 and 200" {
			t.Fatalf("%s: details.fields.page_size = %q, want the contract's wording", endpoint, problem)
		}
		// The contract's own example is exactly this message.
		if body.Message != "page_size must be between 1 and 200" {
			t.Fatalf("%s: message = %q, want the contract's example wording", endpoint, body.Message)
		}
		if inv.called() {
			t.Fatalf("%s: an invalid request reached the cache", endpoint)
		}
	}
}

func TestPaginationParameterBoundaries(t *testing.T) {
	cases := []struct {
		query   string
		status  int
		field   string
		problem string
	}{
		{"page=1&page_size=1", http.StatusOK, "", ""},
		{"page=1&page_size=200", http.StatusOK, "", ""},
		{"page=0", http.StatusBadRequest, "page", "must be at least 1"},
		{"page=-3", http.StatusBadRequest, "page", "must be at least 1"},
		{"page=abc", http.StatusBadRequest, "page", "must be an integer"},
		{"page=9223372036854775807", http.StatusBadRequest, "page", "is too large"},
		{"page_size=0", http.StatusBadRequest, "page_size", "must be between 1 and 200"},
		{"page_size=abc", http.StatusBadRequest, "page_size", "must be an integer"},
		{"order=sideways", http.StatusBadRequest, "order", "must be one of asc, desc"},
		{"sort=raw", http.StatusBadRequest, "sort", "must be one of name, synced_at"},
	}

	for _, tc := range cases {
		inv := &fakeInventory{sites: sampleSites()}
		rr := get(t, listRouter(t, inv), "/api/v1/sites?"+tc.query)
		if rr.Code != tc.status {
			t.Errorf("%s: status = %d, want %d (body: %s)", tc.query, rr.Code, tc.status, rr.Body.String())
			continue
		}
		if tc.status == http.StatusOK {
			if got := inv.siteQuery(); got.PageSize == 0 {
				t.Errorf("%s: query = %+v, want a page size", tc.query, got)
			}
			continue
		}
		body := decodeError(t, rr)
		if problem := fieldProblem(t, body, tc.field); problem != tc.problem {
			t.Errorf("%s: details.fields.%s = %q, want %q", tc.query, tc.field, problem, tc.problem)
		}
	}
}

// --- GET /devices ----------------------------------------------------------

func TestListDevicesServesTheCachedPage(t *testing.T) {
	inv := &fakeInventory{devices: sampleDevices()}
	rr := get(t, listRouter(t, inv), "/api/v1/devices")

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
	}
	data, _ := decodeBody(t, rr)["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("data has %d rows, want 2", len(data))
	}

	first, _ := data[0].(map[string]any)
	if first["device_type"] != "ap" || first["status"] != "ONLINE" || first["adopted_at"] != "2026-02-27T08:00:00Z" {
		t.Fatalf("row = %v, want the synced device", first)
	}
	if radios, ok := first["radios"].([]any); !ok || len(radios) != 0 {
		t.Fatalf("radios = %v, want an empty array (nothing normalized yet, and the raw payload is never exposed)", first["radios"])
	}

	// uptime_sec is null in phase 1: the Integration API exposes no telemetry.
	second, _ := data[1].(map[string]any)
	for _, key := range []string{"name", "mac", "model", "status", "firmware_version", "adopted_at", "uptime_sec"} {
		if value, present := second[key]; !present || value != nil {
			t.Errorf("row 2 %s = %v (present: %v), want an explicit null", key, value, present)
		}
	}
	if second["device_type"] != "other" {
		t.Errorf("row 2 device_type = %v, want the controlled-vocabulary fallback", second["device_type"])
	}
}

func TestListDevicesForwardsEveryFilter(t *testing.T) {
	inv := &fakeInventory{devices: sampleDevices()}
	rr := get(t, listRouter(t, inv),
		"/api/v1/devices?site_id=1F0C9D0A-3B2E-4C5F-8A91-6D7E8F9A0B1C&status=online&device_type=ap&mac=AA%3ABB%3ACC%3A00%3A00%3A01&sort=status&order=desc")

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
	}
	want := inventory.DeviceQuery{
		Page: 1, PageSize: 50, Sort: "status", Order: "desc",
		SiteID: "1F0C9D0A-3B2E-4C5F-8A91-6D7E8F9A0B1C",
		// The cache stores the canonical forms the contract documents, so the
		// filter is normalized rather than rejected.
		Status: "ONLINE", DeviceType: "ap", MAC: "aa:bb:cc:00:00:01",
	}
	if got := inv.deviceQuery(); got != want {
		t.Fatalf("query = %+v, want %+v", got, want)
	}
}

// TestDevicesFilterIsStatusNotState guards the trap this issue calls out: the
// contract declares `status`, and an undeclared `state` must not be silently
// ignored — it is rejected with 400 validation_failed naming `state` (ZENI-19).
func TestDevicesFilterIsStatusNotState(t *testing.T) {
	status := &fakeInventory{devices: sampleDevices()}
	if rr := get(t, listRouter(t, status), "/api/v1/devices?status=ONLINE"); rr.Code != http.StatusOK {
		t.Fatalf("status=ONLINE: status = %d, want 200", rr.Code)
	}
	if got := status.deviceQuery().Status; got != "ONLINE" {
		t.Fatalf("query.Status = %q, want %q", got, "ONLINE")
	}

	state := &fakeInventory{devices: sampleDevices()}
	rr := get(t, listRouter(t, state), "/api/v1/devices?state=ONLINE")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("state=ONLINE: status = %d, want 400 validation_failed", rr.Code)
	}
	body := decodeError(t, rr)
	if body.Code != CodeValidationFailed {
		t.Errorf("code = %q, want validation_failed", body.Code)
	}
	if problem := fieldProblem(t, body, "state"); problem != "is not a recognized query parameter" {
		t.Errorf("details.fields.state = %q, want 'is not a recognized query parameter'", problem)
	}
	if state.called() {
		t.Error("an invalid request reached the cache")
	}
}

func TestListDevicesRejectsInvalidFilters(t *testing.T) {
	cases := []struct {
		query   string
		field   string
		problem string
	}{
		{"site_id=not-a-uuid", "site_id", "must be a UUID"},
		{"mac=aa-bb-cc-dd-ee-01", "mac", "must be a colon-separated MAC address"},
		{"device_type=router", "device_type", "must be one of ap, switch, gateway, other"},
		{"status=" + repeat("A", 33), "status", "must be at most 32 characters"},
		{"sort=uptime_sec", "sort", "must be one of name, model, status, adopted_at, synced_at"},
		{"state=OFFLINE", "state", "is not a recognized query parameter"},
		{"bogus_param=123", "bogus_param", "is not a recognized query parameter"},
	}

	for _, tc := range cases {
		inv := &fakeInventory{devices: sampleDevices()}
		rr := get(t, listRouter(t, inv), "/api/v1/devices?"+tc.query)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (body: %s)", tc.query, rr.Code, rr.Body.String())
			continue
		}
		body := decodeError(t, rr)
		if problem := fieldProblem(t, body, tc.field); problem != tc.problem {
			t.Errorf("%s: details.fields.%s = %q, want %q", tc.query, tc.field, problem, tc.problem)
		}
		if inv.called() {
			t.Errorf("%s: an invalid request reached the cache", tc.query)
		}
	}
}

// --- cache states ----------------------------------------------------------

func TestEmptyCacheIsAnEmptyPageNotAnError(t *testing.T) {
	inv := &fakeInventory{
		sites:   inventory.SitePage{Items: []inventory.Site{}},
		devices: inventory.DevicePage{Items: []inventory.Device{}},
		clients: inventory.ClientPage{Items: []inventory.Client{}},
	}
	handler := listRouter(t, inv)

	for _, endpoint := range []string{"sites", "devices", "clients"} {
		rr := get(t, handler, "/api/v1/"+endpoint)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200 (body: %s)", endpoint, rr.Code, rr.Body.String())
		}
		body := decodeBody(t, rr)
		// `data` must be [] rather than null: the contract types it as an array.
		if data, ok := body["data"].([]any); !ok || len(data) != 0 {
			t.Fatalf("%s: data = %v, want []", endpoint, body["data"])
		}
		pagination, _ := body["pagination"].(map[string]any)
		if pagination["total"] != float64(0) || pagination["total_pages"] != float64(0) {
			t.Fatalf("%s: pagination = %v, want total 0 and total_pages 0", endpoint, pagination)
		}
	}
}

// TestStaleCacheIsServedWithPerRowSyncedAt is the degraded mode the contract
// promises for a stale cache the controller can still refresh: an old cache is
// not an error, the dashboard keeps rendering rows and shows how old they are.
func TestStaleCacheIsServedWithPerRowSyncedAt(t *testing.T) {
	synced := time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC)
	cfg := config.Config{UnifiEnabled: true, UnifiMode: config.UnifiModeCloud, SyncStaleAfter: 3 * time.Minute}
	stale := fakeStatus{status: inventory.Status{
		Stale: true, StaleAfter: 3 * time.Minute, Breaker: inventory.BreakerClosed,
		Surface: inventory.SourceCloud, Reachable: true, LastSuccessfulSyncAt: &synced,
	}}

	inv := &fakeInventory{sites: sampleSites(), devices: sampleDevices(), clients: sampleClients()}
	handler := testRouterWithInventory(t, cfg, fakeDB{}, stale, inv)

	for _, endpoint := range []string{"sites", "devices", "clients"} {
		rr := get(t, handler, "/api/v1/"+endpoint)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200 from a stale cache (body: %s)", endpoint, rr.Code, rr.Body.String())
		}
		data, _ := decodeBody(t, rr)["data"].([]any)
		if len(data) == 0 {
			t.Fatalf("%s: no rows served from a stale cache", endpoint)
		}
		row, _ := data[0].(map[string]any)
		if row["synced_at"] == nil {
			t.Fatalf("%s: synced_at is missing; the dashboard cannot tell how old the data is", endpoint)
		}
	}
}

// TestFreshCacheIsServedWhileTheControllerIsOffline: data that is younger than
// SYNC_STALE_AFTER is still the truth even though the controller just went
// away, so it is served with 200.
func TestFreshCacheIsServedWhileTheControllerIsOffline(t *testing.T) {
	cfg := config.Config{UnifiEnabled: true, UnifiMode: config.UnifiModeCloud, SyncStaleAfter: 3 * time.Minute}
	fresh := fakeStatus{status: inventory.Status{
		Stale: false, StaleAfter: 3 * time.Minute, Breaker: inventory.BreakerClosed,
		Surface: inventory.SourceCloud, Reachable: false, LastSuccessfulSyncAt: ptr(time.Now().Add(-time.Minute)),
	}}

	inv := &fakeInventory{sites: sampleSites(), devices: sampleDevices(), clients: sampleClients(), networks: sampleNetworks()}
	handler := testRouterWithInventory(t, cfg, fakeDB{}, fresh, inv)

	for _, endpoint := range []string{"sites", "devices", "clients", "networks"} {
		rr := get(t, handler, "/api/v1/"+endpoint)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200 from a fresh cache (body: %s)", endpoint, rr.Code, rr.Body.String())
		}
		if data, _ := decodeBody(t, rr)["data"].([]any); len(data) == 0 {
			t.Fatalf("%s: no rows served from a fresh cache", endpoint)
		}
	}
}

// TestStaleCacheWithAnOfflineControllerIs503 is the state the epic calls out:
// cache stale *and* controller offline. Fresh data cannot be produced, so the
// list endpoints report the controller as the broken dependency instead of
// answering with rows of unknown age.
func TestStaleCacheWithAnOfflineControllerIs503(t *testing.T) {
	synced := time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC)
	cfg := config.Config{UnifiEnabled: true, UnifiMode: config.UnifiModeCloud, SyncStaleAfter: 3 * time.Minute}
	stale := fakeStatus{status: inventory.Status{
		Stale: true, StaleAfter: 3 * time.Minute, Breaker: inventory.BreakerOpen,
		Surface: inventory.SourceCloud, Reachable: false, LastSuccessfulSyncAt: &synced, LastErrorKind: "unifi_unreachable",
	}}

	inv := &fakeInventory{sites: sampleSites(), devices: sampleDevices(), clients: sampleClients(), networks: sampleNetworks()}
	handler := testRouterWithInventory(t, cfg, fakeDB{}, stale, inv)

	for _, endpoint := range []string{"sites", "devices", "clients", "networks"} {
		rr := get(t, handler, "/api/v1/"+endpoint)
		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: status = %d, want 503 (body: %s)", endpoint, rr.Code, rr.Body.String())
		}
		body := decodeError(t, rr)
		if body.Code != CodeUnifiUnreachable {
			t.Fatalf("%s: code = %q, want %q (the controller is the broken dependency)", endpoint, body.Code, CodeUnifiUnreachable)
		}
		if body.Details["reason"] != "cache_stale" {
			t.Fatalf("%s: details.reason = %v, want cache_stale", endpoint, body.Details["reason"])
		}
		// The contract's controller_offline example tells the dashboard how old the
		// data it keeps rendering is.
		if got := body.Details["last_successful_sync_at"]; got != formatTime(synced) {
			t.Fatalf("%s: details.last_successful_sync_at = %v, want %s", endpoint, got, formatTime(synced))
		}
		if strings.Contains(rr.Body.String(), "U6 Pro") {
			t.Fatalf("%s: a 503 leaked cached rows: %s", endpoint, rr.Body.String())
		}
	}
}

// TestControllerNeverAnsweredIs503 is the other half of the same state: UniFi is
// enabled, no cycle ever succeeded and the controller is not answering, so "no
// sites" cannot be distinguished from "we do not know yet".
func TestControllerNeverAnsweredIs503(t *testing.T) {
	cfg := config.Config{UnifiEnabled: true, UnifiMode: config.UnifiModeCloud, SyncStaleAfter: 3 * time.Minute}
	never := fakeStatus{status: inventory.Status{
		Stale: true, StaleAfter: 3 * time.Minute, Breaker: inventory.BreakerClosed,
		Surface: inventory.SourceCloud, Reachable: false,
	}}

	inv := &fakeInventory{
		sites:    inventory.SitePage{Items: []inventory.Site{}},
		devices:  inventory.DevicePage{Items: []inventory.Device{}},
		clients:  inventory.ClientPage{Items: []inventory.Client{}},
		networks: inventory.NetworkPage{Items: []inventory.Network{}},
	}
	handler := testRouterWithInventory(t, cfg, fakeDB{}, never, inv)

	for _, endpoint := range []string{"sites", "devices", "clients", "networks"} {
		rr := get(t, handler, "/api/v1/"+endpoint)
		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: status = %d, want 503 (body: %s)", endpoint, rr.Code, rr.Body.String())
		}
		body := decodeError(t, rr)
		if body.Code != CodeUnifiUnreachable {
			t.Fatalf("%s: code = %q, want %q (the controller is the broken dependency)", endpoint, body.Code, CodeUnifiUnreachable)
		}
		if body.Details["reason"] != "cache_stale" {
			t.Fatalf("%s: details.reason = %v, want cache_stale", endpoint, body.Details["reason"])
		}
		if got := body.Details["last_successful_sync_at"]; got != nil {
			t.Fatalf("%s: details.last_successful_sync_at = %v, want null (nothing ever synced)", endpoint, got)
		}
	}
}

// TestCacheFailureIs503ServiceUnavailable keeps the two 503s apart: a database
// we cannot read is our own dependency failing, not the controller's fault
// (ADR-004).
func TestCacheFailureIs503ServiceUnavailable(t *testing.T) {
	inv := &fakeInventory{sites: inventory.SitePage{}, siteErr: errors.New("connection refused")}
	rr := get(t, listRouter(t, inv), "/api/v1/sites")

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d (body: %s)", rr.Code, http.StatusServiceUnavailable, rr.Body.String())
	}
	body := decodeError(t, rr)
	if body.Code != CodeServiceUnavailable {
		t.Fatalf("code = %q, want %q", body.Code, CodeServiceUnavailable)
	}
	if body.Details["reason"] != "database_unreachable" {
		t.Fatalf("details.reason = %v, want database_unreachable", body.Details["reason"])
	}
	// The internal error must not leak into the response.
	if strings.Contains(rr.Body.String(), "connection refused") {
		t.Fatalf("the response body leaks the driver error: %s", rr.Body.String())
	}
}

func repeat(s string, n int) string {
	return strings.Repeat(s, n)
}
