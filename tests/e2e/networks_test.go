//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// The network/VLAN vertical slice of FR-4: the mocked console reports networks,
// the sync worker writes them into Postgres, and GET /networks plus
// GET /networks/{id} serve that cache.

type networkRow struct {
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

type networkList struct {
	Data       []networkRow `json:"data"`
	Pagination pagination   `json:"pagination"`
}

// networkByName finds a fixture network in the page the API serves.
func (s *stack) networkByName(name string) networkRow {
	s.t.Helper()
	var list networkList
	s.getJSON("/api/v1/networks?page_size=200", &list)
	for _, n := range list.Data {
		if n.Name == name {
			return n
		}
	}
	s.t.Fatalf("no %q network in the cache: %+v", name, list.Data)
	return networkRow{}
}

// networkByID reads one network through the API.
func (s *stack) networkByID(id string) (networkRow, bool) {
	code, body, err := s.do(http.MethodGet, "/api/v1/networks/"+id, nil)
	if err != nil || code != http.StatusOK {
		return networkRow{}, false
	}
	var row networkRow
	if err := json.Unmarshal(body, &row); err != nil {
		s.t.Fatalf("decode network %s: %v", id, err)
	}
	s.assertContract("Network", body)
	return row, true
}

func TestNetworksVerticalSlice(t *testing.T) {
	s := startStack(t, stackOptions{unifiEnabled: true})
	s.waitForSynced(e2eDeviceTotal)

	// 1. Read /networks paged list
	var list networkList
	s.getJSON("/api/v1/networks", &list)
	if list.Pagination.Total != e2eNetworkTotal {
		t.Fatalf("total networks = %d, want %d", list.Pagination.Total, e2eNetworkTotal)
	}
	if len(list.Data) != e2eNetworkTotal {
		t.Fatalf("got %d networks on page 1, want %d", len(list.Data), e2eNetworkTotal)
	}

	// Default sort in contract is vlan_id asc: Default (VLAN 1) should come before Guest (VLAN 20).
	if list.Data[0].Name != "Default" || list.Data[1].Name != "Guest" {
		t.Fatalf("order = [%s, %s], want [Default, Guest] (default sort is vlan_id asc)", list.Data[0].Name, list.Data[1].Name)
	}

	defNet := list.Data[0]
	guestNet := list.Data[1]

	// Verify all 4 completeness fields of FR-4 on Default network:
	if defNet.VLANID == nil || *defNet.VLANID != 1 {
		t.Errorf("Default vlan_id = %v, want 1", defNet.VLANID)
	}
	if defNet.Management == nil || *defNet.Management != "GATEWAY" {
		t.Errorf("Default management = %v, want GATEWAY", defNet.Management)
	}
	if defNet.Enabled == nil || !*defNet.Enabled {
		t.Errorf("Default enabled = %v, want true", defNet.Enabled)
	}
	if defNet.IsDefault == nil || !*defNet.IsDefault {
		t.Errorf("Default is_default = %v, want true", defNet.IsDefault)
	}
	if defNet.Origin == nil || *defNet.Origin != "SYSTEM_DEFINED" {
		t.Errorf("Default origin = %v, want SYSTEM_DEFINED", defNet.Origin)
	}
	if defNet.Configurable == nil || !*defNet.Configurable {
		t.Errorf("Default configurable = %v, want true", defNet.Configurable)
	}

	// Verify Guest network fields:
	if guestNet.VLANID == nil || *guestNet.VLANID != 20 {
		t.Errorf("Guest vlan_id = %v, want 20", guestNet.VLANID)
	}
	if guestNet.Management == nil || *guestNet.Management != "SWITCH" {
		t.Errorf("Guest management = %v, want SWITCH", guestNet.Management)
	}
	if guestNet.Enabled == nil || *guestNet.Enabled {
		t.Errorf("Guest enabled = %v, want false", guestNet.Enabled)
	}
	if guestNet.IsDefault == nil || *guestNet.IsDefault {
		t.Errorf("Guest is_default = %v, want false", guestNet.IsDefault)
	}
	if guestNet.Origin == nil || *guestNet.Origin != "USER_DEFINED" {
		t.Errorf("Guest origin = %v, want USER_DEFINED", guestNet.Origin)
	}
	if guestNet.Configurable == nil || !*guestNet.Configurable {
		t.Errorf("Guest configurable = %v, want true", guestNet.Configurable)
	}

	// 2. Read single network by local UUID: GET /networks/{id}
	singleDef, ok := s.networkByID(defNet.ID)
	if !ok {
		t.Fatalf("GET /networks/%s returned non-200", defNet.ID)
	}
	if singleDef.Name != "Default" || derefInt(singleDef.VLANID) != "1" || deref(singleDef.Management) != "GATEWAY" {
		t.Fatalf("single network = %+v, want Default gateway network", singleDef)
	}

	singleGuest, ok := s.networkByID(guestNet.ID)
	if !ok {
		t.Fatalf("GET /networks/%s returned non-200", guestNet.ID)
	}
	if singleGuest.Name != "Guest" || derefInt(singleGuest.VLANID) != "20" || deref(singleGuest.Management) != "SWITCH" {
		t.Fatalf("single network = %+v, want Guest switch network", singleGuest)
	}

	// Missing ID returns 404
	code, notFoundBody, err := s.do(http.MethodGet, "/api/v1/networks/00000000-0000-0000-0000-000000000000", nil)
	if err != nil || code != http.StatusNotFound {
		t.Fatalf("missing network code = %d, want 404", code)
	}
	s.assertContract("Error", notFoundBody)

	// 3. Server-side filters
	siteID := s.onlySiteID()
	var filtered networkList

	// site_id
	s.getJSON(fmt.Sprintf("/api/v1/networks?site_id=%s", siteID), &filtered)
	if filtered.Pagination.Total != 2 {
		t.Errorf("site_id filter total = %d, want 2", filtered.Pagination.Total)
	}

	// vlan_id
	s.getJSON("/api/v1/networks?vlan_id=1", &filtered)
	if filtered.Pagination.Total != 1 || len(filtered.Data) != 1 || filtered.Data[0].Name != "Default" {
		t.Errorf("vlan_id=1 filter = %+v, want only Default", filtered.Data)
	}
	s.getJSON("/api/v1/networks?vlan_id=20", &filtered)
	if filtered.Pagination.Total != 1 || len(filtered.Data) != 1 || filtered.Data[0].Name != "Guest" {
		t.Errorf("vlan_id=20 filter = %+v, want only Guest", filtered.Data)
	}
	s.getJSON("/api/v1/networks?vlan_id=99", &filtered)
	if filtered.Pagination.Total != 0 || len(filtered.Data) != 0 {
		t.Errorf("vlan_id=99 filter = %+v, want empty", filtered.Data)
	}

	// management
	s.getJSON("/api/v1/networks?management=GATEWAY", &filtered)
	if filtered.Pagination.Total != 1 || len(filtered.Data) != 1 || filtered.Data[0].Name != "Default" {
		t.Errorf("management=GATEWAY filter = %+v, want only Default", filtered.Data)
	}
	s.getJSON("/api/v1/networks?management=SWITCH", &filtered)
	if filtered.Pagination.Total != 1 || len(filtered.Data) != 1 || filtered.Data[0].Name != "Guest" {
		t.Errorf("management=SWITCH filter = %+v, want only Guest", filtered.Data)
	}

	// enabled
	s.getJSON("/api/v1/networks?enabled=true", &filtered)
	if filtered.Pagination.Total != 1 || len(filtered.Data) != 1 || filtered.Data[0].Name != "Default" {
		t.Errorf("enabled=true filter = %+v, want only Default", filtered.Data)
	}
	s.getJSON("/api/v1/networks?enabled=false", &filtered)
	if filtered.Pagination.Total != 1 || len(filtered.Data) != 1 || filtered.Data[0].Name != "Guest" {
		t.Errorf("enabled=false filter = %+v, want only Guest", filtered.Data)
	}

	// 4. Sorting & pagination
	s.getJSON("/api/v1/networks?sort=vlan_id&order=desc", &filtered)
	if len(filtered.Data) != 2 || filtered.Data[0].Name != "Guest" || filtered.Data[1].Name != "Default" {
		t.Errorf("sort vlan_id desc = [%s, %s], want [Guest, Default]", filtered.Data[0].Name, filtered.Data[1].Name)
	}

	s.getJSON("/api/v1/networks?page=1&page_size=1", &filtered)
	if filtered.Pagination.Page != 1 || filtered.Pagination.PageSize != 1 ||
		filtered.Pagination.Total != 2 || filtered.Pagination.TotalPages != 2 || len(filtered.Data) != 1 {
		t.Errorf("paged networks = %+v, want page 1 of 2 with 1 item", filtered.Pagination)
	}

	// 5. Validation errors
	code, badBody, err := s.do(http.MethodGet, "/api/v1/networks?vlan_id=4095", nil)
	if err != nil || code != http.StatusBadRequest {
		t.Fatalf("vlan_id=4095 code = %d, want 400", code)
	}
	s.assertContract("Error", badBody)

	code, badBody, err = s.do(http.MethodGet, "/api/v1/networks?vlan_id=-1", nil)
	if err != nil || code != http.StatusBadRequest {
		t.Fatalf("vlan_id=-1 code = %d, want 400", code)
	}
	s.assertContract("Error", badBody)

	code, badBody, err = s.do(http.MethodGet, "/api/v1/networks?sort=unsupported", nil)
	if err != nil || code != http.StatusBadRequest {
		t.Fatalf("sort=unsupported code = %d, want 400", code)
	}
	s.assertContract("Error", badBody)
}

func TestNetworksEmptyOnCloudSurface(t *testing.T) {
	// The cloud surface does not expose networks (ADR-001):
	// the adapter returns ErrNotFound, which the worker treats as an unsupported
	// collection and gracefully skips. The networks table stays empty.
	s := startStack(t, stackOptions{
		unifiEnabled: true,
		extraEnv:     map[string]string{"UNIFI_MODE": "cloud"},
	})
	s.waitForSynced(e2eDeviceTotal)

	var list networkList
	s.getJSON("/api/v1/networks", &list)
	if list.Pagination.Total != 0 || len(list.Data) != 0 {
		t.Fatalf("cloud surface networks = %+v, want empty list", list)
	}
}
