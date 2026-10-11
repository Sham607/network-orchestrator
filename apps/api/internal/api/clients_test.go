package api

import (
	"net/http"
	"testing"

	"github.com/Sham607/network-orchestrator/apps/api/internal/inventory"
)

// --- GET /clients ------------------------------------------------------------

func TestListClientsServesTheCachedPage(t *testing.T) {
	inv := &fakeInventory{clients: sampleClients()}
	rr := get(t, listRouter(t, inv), "/api/v1/clients")

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
	}
	data, _ := decodeBody(t, rr)["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("data has %d rows, want 2", len(data))
	}

	first, _ := data[0].(map[string]any)
	if first["hostname"] != "ivan-laptop" || first["is_connected"] != true || first["client_type"] != "WIRELESS" {
		t.Fatalf("row = %v, want the connected client", first)
	}
	// ssid and signal_dbm are always null in phase 1: the Integration API does
	// not expose them yet (docs/data-model.md §4.5).
	for _, key := range []string{"ssid", "signal_dbm"} {
		if value, present := first[key]; !present || value != nil {
			t.Errorf("row 1 %s = %v (present: %v), want an explicit null", key, value, present)
		}
	}

	second, _ := data[1].(map[string]any)
	if second["is_connected"] != false || second["disconnected_at"] == nil {
		t.Fatalf("row 2 = %v, want a disconnected historical row", second)
	}
}

func TestListClientsDefaultsToLastSeen(t *testing.T) {
	inv := &fakeInventory{clients: sampleClients()}
	get(t, listRouter(t, inv), "/api/v1/clients")

	if got := inv.clientQuery(); got.Page != 1 || got.PageSize != 50 || got.Order != "asc" || got.Sort != "last_seen" {
		t.Fatalf("query = %+v, want page 1, page_size 50, order asc, sort last_seen (the documented defaults)", got)
	}
}

func TestListClientsForwardsEveryFilter(t *testing.T) {
	inv := &fakeInventory{clients: sampleClients()}
	rr := get(t, listRouter(t, inv),
		"/api/v1/clients?site_id=1F0C9D0A-3B2E-4C5F-8A91-6D7E8F9A0B1C&connected=true"+
			"&uplink_device_id=2A3B4C5D-6E7F-4A8B-9C0D-1E2F3A4B5C6D&client_type=WIRELESS"+
			"&mac=AA%3ABB%3ACC%3ADD%3AEE%3A03&ssid=corp&sort=hostname&order=desc")

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
	}
	got := inv.clientQuery()
	want := inventory.ClientQuery{
		Page: 1, PageSize: 50, Sort: "hostname", Order: "desc",
		SiteID:         "1F0C9D0A-3B2E-4C5F-8A91-6D7E8F9A0B1C",
		Connected:      ptr(true),
		UplinkDeviceID: "2A3B4C5D-6E7F-4A8B-9C0D-1E2F3A4B5C6D",
		ClientType:     "WIRELESS",
		MAC:            "aa:bb:cc:dd:ee:03",
		SSID:           "corp",
	}
	if got.Page != want.Page || got.PageSize != want.PageSize || got.Sort != want.Sort || got.Order != want.Order ||
		got.SiteID != want.SiteID || got.UplinkDeviceID != want.UplinkDeviceID || got.ClientType != want.ClientType ||
		got.MAC != want.MAC || got.SSID != want.SSID ||
		got.Connected == nil || *got.Connected != *want.Connected {
		t.Fatalf("query = %+v, want %+v", got, want)
	}
}

func TestListClientsRejectsInvalidFilters(t *testing.T) {
	cases := []struct {
		query   string
		field   string
		problem string
	}{
		{"site_id=not-a-uuid", "site_id", "must be a UUID"},
		{"uplink_device_id=not-a-uuid", "uplink_device_id", "must be a UUID"},
		{"mac=aa-bb-cc-dd-ee-01", "mac", "must be a colon-separated MAC address"},
		{"client_type=ethernet", "client_type", "must be one of WIRELESS, WIRED"},
		{"connected=yes", "connected", "must be a boolean"},
		{"sort=mac", "sort", "must be one of hostname, last_seen, connected_at, synced_at"},
		{"state=connected", "state", "is not a recognized query parameter"},
	}

	for _, tc := range cases {
		inv := &fakeInventory{clients: sampleClients()}
		rr := get(t, listRouter(t, inv), "/api/v1/clients?"+tc.query)
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

// TestClientsSSIDFilterIsPublishedButInert pins the contract's documented
// behavior: `ssid` is accepted (never rejected as unrecognized), but the
// column it filters on is always null in phase 1, so it can never match a row
// (openapi/mock-expectations.md §5).
func TestClientsSSIDFilterIsPublishedButInert(t *testing.T) {
	inv := &fakeInventory{clients: inventory.ClientPage{Items: []inventory.Client{}}}
	rr := get(t, listRouter(t, inv), "/api/v1/clients?ssid=corp-wifi")

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	if got := inv.clientQuery().SSID; got != "corp-wifi" {
		t.Fatalf("query.SSID = %q, want it forwarded unchanged", got)
	}
}

// --- GET /clients/{id} -------------------------------------------------------

func TestGetClientServesTheCachedRow(t *testing.T) {
	inv := &fakeInventory{getClient: sampleClients().Items[0], getClientFound: true}
	rr := get(t, listRouter(t, inv), "/api/v1/clients/4c5d6e7f-8a9b-4c0d-1e2f-3a4b5c6d7e8f")

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	body := decodeBody(t, rr)
	if body["hostname"] != "ivan-laptop" {
		t.Fatalf("hostname = %v, want ivan-laptop", body["hostname"])
	}
	if got := inv.getClientCalls; len(got) != 1 || got[0] != "4c5d6e7f-8a9b-4c0d-1e2f-3a4b5c6d7e8f" {
		t.Fatalf("getClientCalls = %v, want exactly one call with the path id", got)
	}
}

func TestGetClientRejectsANonUUIDPath(t *testing.T) {
	inv := &fakeInventory{}
	rr := get(t, listRouter(t, inv), "/api/v1/clients/not-a-uuid")

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rr.Code, rr.Body.String())
	}
	body := decodeError(t, rr)
	if body.Code != CodeValidationFailed {
		t.Fatalf("code = %q, want %q", body.Code, CodeValidationFailed)
	}
	if problem := fieldProblem(t, body, "id"); problem != "must be a UUID" {
		t.Fatalf("details.fields.id = %q, want 'must be a UUID'", problem)
	}
	if inv.called() {
		t.Fatal("an invalid request reached the cache")
	}
}

func TestGetClientNotFoundMatchesTheContract(t *testing.T) {
	inv := &fakeInventory{getClientFound: false}
	rr := get(t, listRouter(t, inv), "/api/v1/clients/4c5d6e7f-8a9b-4c0d-1e2f-3a4b5c6d7e8f")

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body: %s)", rr.Code, rr.Body.String())
	}
	body := decodeError(t, rr)
	if body.Code != CodeNotFound {
		t.Fatalf("code = %q, want %q", body.Code, CodeNotFound)
	}
	if body.Details["resource"] != "client" {
		t.Fatalf("details.resource = %v, want client", body.Details["resource"])
	}
	assertMatchesContract(t, "Error", rr.Body.String())
}
