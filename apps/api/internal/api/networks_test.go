package api

import (
	"net/http"
	"testing"

	"github.com/Sham607/network-orchestrator/apps/api/internal/inventory"
)

// --- GET /networks -----------------------------------------------------------

func TestListNetworksServesTheCachedPage(t *testing.T) {
	inv := &fakeInventory{networks: sampleNetworks()}
	rr := get(t, listRouter(t, inv), "/api/v1/networks")

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
	}
	data, _ := decodeBody(t, rr)["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("data has %d rows, want 2", len(data))
	}

	first, _ := data[0].(map[string]any)
	if first["name"] != "Default" || first["management"] != "GATEWAY" || first["enabled"] != true || first["is_default"] != true {
		t.Fatalf("row = %v, want the default gateway network", first)
	}
	if vlanID, ok := first["vlan_id"].(float64); !ok || int(vlanID) != 1 {
		t.Fatalf("row 1 vlan_id = %v, want 1", first["vlan_id"])
	}
	if first["origin"] != "SYSTEM_DEFINED" || first["configurable"] != true {
		t.Fatalf("row 1 origin/configurable = %v/%v, want SYSTEM_DEFINED/true", first["origin"], first["configurable"])
	}

	assertMatchesContract(t, "NetworkList", rr.Body.String())
}

func TestListNetworksDefaultsToVLANID(t *testing.T) {
	inv := &fakeInventory{networks: sampleNetworks()}
	get(t, listRouter(t, inv), "/api/v1/networks")

	if got := inv.networkQuery(); got.Page != 1 || got.PageSize != 50 || got.Order != "asc" || got.Sort != "vlan_id" {
		t.Fatalf("query = %+v, want page 1, page_size 50, order asc, sort vlan_id (the documented defaults)", got)
	}
}

func TestListNetworksForwardsEveryFilter(t *testing.T) {
	inv := &fakeInventory{networks: sampleNetworks()}
	rr := get(t, listRouter(t, inv),
		"/api/v1/networks?site_id=1F0C9D0A-3B2E-4C5F-8A91-6D7E8F9A0B1C&vlan_id=20"+
			"&management=SWITCH&enabled=true&sort=name&order=desc")

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
	}
	got := inv.networkQuery()
	want := inventory.NetworkQuery{
		Page: 1, PageSize: 50, Sort: "name", Order: "desc",
		SiteID:     "1F0C9D0A-3B2E-4C5F-8A91-6D7E8F9A0B1C",
		VLANID:     ptr(20),
		Management: "SWITCH",
		Enabled:    ptr(true),
	}
	if got.Page != want.Page || got.PageSize != want.PageSize || got.Sort != want.Sort || got.Order != want.Order ||
		got.SiteID != want.SiteID || got.Management != want.Management ||
		got.VLANID == nil || *got.VLANID != *want.VLANID ||
		got.Enabled == nil || *got.Enabled != *want.Enabled {
		t.Fatalf("query = %+v, want %+v", got, want)
	}
}

func TestListNetworksRejectsInvalidFilters(t *testing.T) {
	cases := []struct {
		query   string
		field   string
		problem string
	}{
		{"site_id=not-a-uuid", "site_id", "must be a UUID"},
		{"vlan_id=not-an-int", "vlan_id", "must be an integer"},
		{"vlan_id=-1", "vlan_id", "must be between 0 and 4094"},
		{"vlan_id=4095", "vlan_id", "must be between 0 and 4094"},
		{"management=this-management-value-is-way-too-long-for-the-limit", "management", "must be at most 32 characters"},
		{"enabled=yes", "enabled", "must be a boolean"},
		{"sort=mac", "sort", "must be one of name, vlan_id, synced_at"},
		{"order=sideways", "order", "must be one of asc, desc"},
		{"foo=bar", "foo", "is not a recognized query parameter"},
	}

	for _, tc := range cases {
		inv := &fakeInventory{networks: sampleNetworks()}
		rr := get(t, listRouter(t, inv), "/api/v1/networks?"+tc.query)
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

// --- GET /networks/{id} ------------------------------------------------------

func TestGetNetworkServesTheCachedRow(t *testing.T) {
	inv := &fakeInventory{getNetwork: sampleNetworks().Items[0], getNetworkFound: true}
	rr := get(t, listRouter(t, inv), "/api/v1/networks/5d6e7f8a-9b0c-4d1e-2f3a-4b5c6d7e8f9a")

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
	}
	body := decodeBody(t, rr)
	if body["name"] != "Default" {
		t.Fatalf("name = %v, want Default", body["name"])
	}
	if got := inv.getNetworkCalls; len(got) != 1 || got[0] != "5d6e7f8a-9b0c-4d1e-2f3a-4b5c6d7e8f9a" {
		t.Fatalf("getNetworkCalls = %v, want exactly one call with the path id", got)
	}
	assertMatchesContract(t, "Network", rr.Body.String())
}

func TestGetNetworkRejectsANonUUIDPath(t *testing.T) {
	inv := &fakeInventory{}
	rr := get(t, listRouter(t, inv), "/api/v1/networks/not-a-uuid")

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

func TestGetNetworkNotFoundMatchesTheContract(t *testing.T) {
	inv := &fakeInventory{getNetworkFound: false}
	rr := get(t, listRouter(t, inv), "/api/v1/networks/5d6e7f8a-9b0c-4d1e-2f3a-4b5c6d7e8f9a")

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body: %s)", rr.Code, rr.Body.String())
	}
	body := decodeError(t, rr)
	if body.Code != CodeNotFound {
		t.Fatalf("code = %q, want %q", body.Code, CodeNotFound)
	}
	if body.Details["resource"] != "network" {
		t.Fatalf("details.resource = %v, want network", body.Details["resource"])
	}
	assertMatchesContract(t, "Error", rr.Body.String())
}
