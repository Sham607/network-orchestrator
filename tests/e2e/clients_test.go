//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// The client-presence vertical slice of FR-3: the mocked console reports active
// clients, the sync worker writes them into Postgres, and GET /clients plus
// GET /clients/{id} serve that cache. `client_events` is internal — no endpoint
// exposes it — so the transition test asserts the append-only rows straight
// from Postgres, which is where the worker writes them.

type clientRow struct {
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

type clientList struct {
	Data       []clientRow `json:"data"`
	Pagination pagination  `json:"pagination"`
}

// identity renders the fields that identify a client and its connection state,
// so two reads can be compared without Go pointer identity deciding the answer.
// last_seen and synced_at are left out on purpose: a background cycle moves
// them, and the assertion is about the row, not about the clock.
func (c clientRow) identity() string {
	return strings.Join([]string{
		c.ID, c.SiteID, deref(c.MAC), deref(c.Hostname), deref(c.IPAddress),
		deref(c.ClientType), deref(c.AccessType), deref(c.UplinkDeviceID),
		deref(c.SSID), deref(c.ConnectedAt), deref(c.DisconnectedAt),
		fmt.Sprintf("%t/%s", c.IsConnected, derefInt(c.SignalDBm)),
		c.FirstSeen,
	}, "|")
}

func derefInt(v *int) string {
	if v == nil {
		return "<nil>"
	}
	return fmt.Sprint(*v)
}

// onlySiteID returns the single cached site's local id, the value the client
// filters take.
func (s *stack) onlySiteID() string {
	s.t.Helper()
	var sites siteList
	s.getJSON("/api/v1/sites", &sites)
	if len(sites.Data) != 1 {
		s.t.Fatalf("sites = %d rows, want the single fixture site", len(sites.Data))
	}
	return sites.Data[0].ID
}

// clientByHostname finds a fixture client in the page the API serves.
func (s *stack) clientByHostname(hostname string) clientRow {
	s.t.Helper()
	var list clientList
	s.getJSON("/api/v1/clients?page_size=200", &list)
	for _, c := range list.Data {
		if c.Hostname != nil && *c.Hostname == hostname {
			return c
		}
	}
	s.t.Fatalf("no %q client in the cache: %+v", hostname, list.Data)
	return clientRow{}
}

// clientByID reads one client through the API. Polling loops use this: a failed
// call means "not yet", not a test failure.
func (s *stack) clientByID(id string) (clientRow, bool) {
	code, body, err := s.do(http.MethodGet, "/api/v1/clients/"+id, nil)
	if err != nil || code != http.StatusOK {
		return clientRow{}, false
	}
	var row clientRow
	if err := json.Unmarshal(body, &row); err != nil {
		return clientRow{}, false
	}
	return row, true
}

// cachedClientRow is one row of the `clients` table as the worker wrote it.
type cachedClientRow struct {
	SiteID         string
	IsConnected    bool
	Source         string
	ConnectedAt    *time.Time
	DisconnectedAt *time.Time
	FirstSeen      time.Time
	LastSeen       time.Time
	SyncedAt       time.Time
}

// cachedClients reads the clients table itself, keyed by the UniFi client id.
// The API only ever reads this cache, so proving the worker wrote Postgres
// means looking at the table.
func (s *stack) cachedClients() (map[string]cachedClientRow, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rows, err := s.db.Query(ctx, `
SELECT unifi_client_id, site_id::text, is_connected, source, connected_at,
       disconnected_at, first_seen, last_seen, synced_at
FROM clients`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cached := make(map[string]cachedClientRow)
	for rows.Next() {
		var unifiID string
		var row cachedClientRow
		if err := rows.Scan(&unifiID, &row.SiteID, &row.IsConnected, &row.Source,
			&row.ConnectedAt, &row.DisconnectedAt, &row.FirstSeen, &row.LastSeen,
			&row.SyncedAt); err != nil {
			return nil, err
		}
		cached[unifiID] = row
	}
	return cached, rows.Err()
}

// clientEvents returns one client's transition log, oldest first. The order is
// the identity column's, not occurred_at's: a connect event carries the time
// the console reported, which can predate the row that records it.
func (s *stack) clientEvents(clientID string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rows, err := s.db.Query(ctx,
		`SELECT event_type FROM client_events WHERE client_id = $1::uuid ORDER BY id`, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []string
	for rows.Next() {
		var eventType string
		if err := rows.Scan(&eventType); err != nil {
			return nil, err
		}
		events = append(events, eventType)
	}
	return events, rows.Err()
}

// successfulSyncs counts the cycles that refreshed the cache. A test that needs
// "the worker ran again" waits for this to move instead of sleeping.
func (s *stack) successfulSyncs() (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var n int
	err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM sync_runs WHERE status IN ('success', 'partial')`).Scan(&n)
	return n, err
}

// waitForCycles waits until n more successful cycles have completed.
func (s *stack) waitForCycles(n int) {
	s.t.Helper()
	base, err := s.successfulSyncs()
	if err != nil {
		s.t.Fatalf("read the sync run log: %v", err)
	}
	s.waitFor(60*time.Second, fmt.Sprintf("%d more successful sync cycles", n), func() bool {
		got, err := s.successfulSyncs()
		return err == nil && got >= base+n
	})
}

// waitForEvents waits until a client's transition log is exactly want, oldest
// first.
func (s *stack) waitForEvents(clientID string, want []string) {
	s.t.Helper()
	s.waitFor(60*time.Second, fmt.Sprintf("client %s to have the event log %v", clientID, want), func() bool {
		got, err := s.clientEvents(clientID)
		return err == nil && slices.Equal(got, want)
	})
}

// TestClientPresenceSlice is the read path end to end: mock console -> sync
// worker -> Postgres -> GET /clients and GET /clients/{id}. Both halves of the
// boundary are asserted — the cache table the worker writes, and the API that
// reads it — so neither a fixture that never reached Postgres nor a handler
// answering from somewhere else can pass.
func TestClientPresenceSlice(t *testing.T) {
	s := startStack(t, stackOptions{unifiEnabled: true})
	s.waitForSynced(e2eDeviceTotal)

	siteID := s.onlySiteID()

	cached, err := s.cachedClients()
	if err != nil {
		t.Fatalf("read the clients table: %v", err)
	}
	if len(cached) != e2eClientTotal {
		t.Fatalf("clients table holds %d rows, want %d", len(cached), e2eClientTotal)
	}
	for unifiID, row := range cached {
		if row.Source != "console" {
			t.Errorf("client %s source = %q, want console", unifiID, row.Source)
		}
		if !row.IsConnected {
			t.Errorf("client %s is_connected = false, want true while the console reports it active", unifiID)
		}
		if row.SiteID != siteID {
			t.Errorf("client %s site_id = %s, want %s", unifiID, row.SiteID, siteID)
		}
		if row.DisconnectedAt != nil {
			t.Errorf("client %s has disconnected_at set while active: %s", unifiID, row.DisconnectedAt)
		}
		if row.FirstSeen.IsZero() || row.LastSeen.IsZero() || row.SyncedAt.IsZero() {
			t.Errorf("client %s has an unset timestamp: %+v", unifiID, row)
		}
	}

	var clients clientList
	s.getJSON("/api/v1/clients", &clients)
	if clients.Pagination.Total != e2eClientTotal || len(clients.Data) != e2eClientTotal {
		t.Fatalf("clients total = %d (%d rows), want %d", clients.Pagination.Total, len(clients.Data), e2eClientTotal)
	}
	for _, c := range clients.Data {
		if !uuidRe.MatchString(c.ID) {
			t.Errorf("client id %q is not a UUID", c.ID)
		}
		if c.SiteID != siteID {
			t.Errorf("client %s site_id = %s, want %s", c.ID, c.SiteID, siteID)
		}
		if !c.IsConnected {
			t.Errorf("client %s is_connected = false, want true", c.ID)
		}
		if c.Hostname == nil || c.MAC == nil || c.IPAddress == nil || c.ConnectedAt == nil {
			t.Errorf("client %s is missing a field the console reported: %+v", c.ID, c)
		}
		// Phase-1 nulls: Integration API v1 exposes neither SSID nor signal, and
		// the contract forbids inventing one (docs/data-model.md §4.5).
		if c.SSID != nil {
			t.Errorf("client %s ssid = %q, want null: the adapter never has one in phase 1", c.ID, *c.SSID)
		}
		if c.SignalDBm != nil {
			t.Errorf("client %s signal_dbm = %d, want null in phase 1", c.ID, *c.SignalDBm)
		}
		for name, value := range map[string]string{
			"first_seen": c.FirstSeen, "last_seen": c.LastSeen, "synced_at": c.SyncedAt,
		} {
			if _, err := time.Parse(time.RFC3339, value); err != nil {
				t.Errorf("client %s %s = %q is not RFC3339: %v", c.ID, name, value, err)
			}
		}
	}

	// The console's normalized fields survive the whole path.
	wireless := s.clientByHostname(e2eWirelessHost)
	if wireless.MAC == nil || *wireless.MAC != e2eWirelessMAC {
		t.Errorf("wireless client mac = %v, want the normalized lower-case colon form %s", wireless.MAC, e2eWirelessMAC)
	}
	if wireless.IPAddress == nil || *wireless.IPAddress != e2eWirelessIP {
		t.Errorf("wireless client ip_address = %v, want %s", wireless.IPAddress, e2eWirelessIP)
	}
	if wireless.ClientType == nil || *wireless.ClientType != "WIRELESS" {
		t.Errorf("wireless client client_type = %v, want WIRELESS", wireless.ClientType)
	}
	if wireless.AccessType == nil || *wireless.AccessType != "DEFAULT" {
		t.Errorf("wireless client access_type = %v, want DEFAULT", wireless.AccessType)
	}
	wired := s.clientByHostname(e2eWiredHost)
	if wired.ClientType == nil || *wired.ClientType != "WIRED" {
		t.Errorf("wired client client_type = %v, want WIRED", wired.ClientType)
	}

	// GET /clients/{id} serves the row the list returned.
	var detail clientRow
	s.getJSON("/api/v1/clients/"+wireless.ID, &detail)
	if detail.identity() != wireless.identity() {
		t.Errorf("GET /clients/%s = %+v, want the list row %+v", wireless.ID, detail, wireless)
	}

	// The id is the control plane's own, and an unknown one is a 404 — not a 200
	// with an empty body, and not a 500.
	code, body := s.get("/api/v1/clients/2a3b4c5d-6e7f-4a8b-9c0d-1e2f3a4b5c6d")
	if code != http.StatusNotFound {
		t.Fatalf("unknown client status = %d, want 404\nbody: %s", code, body)
	}
	var notFound errorBody
	if err := json.Unmarshal(body, &notFound); err != nil {
		t.Fatalf("decode the 404 body: %v\nbody: %s", err, body)
	}
	if notFound.Code != "not_found" {
		t.Errorf("unknown client code = %q, want not_found", notFound.Code)
	}
	s.assertContract("Error", body)
}

// TestClientFiltersAndPagination is the query matrix of GET /clients: every
// contract parameter narrows the cache in the database, and the two corners the
// contract itself calls out behave the way it describes.
func TestClientFiltersAndPagination(t *testing.T) {
	s := startStack(t, stackOptions{unifiEnabled: true})
	s.waitForSynced(e2eDeviceTotal)

	siteID := s.onlySiteID()
	wireless := s.clientByHostname(e2eWirelessHost)

	var connected clientList
	s.getJSON("/api/v1/clients?connected=true", &connected)
	if connected.Pagination.Total != e2eClientTotal {
		t.Errorf("connected=true total = %d, want %d", connected.Pagination.Total, e2eClientTotal)
	}
	for _, c := range connected.Data {
		if !c.IsConnected {
			t.Errorf("connected=true returned %s with is_connected=false", deref(c.Hostname))
		}
	}
	// Integration API v1 reports only active clients, so the historical half of
	// the filter is empty until a client actually leaves the active list.
	var offline clientList
	s.getJSON("/api/v1/clients?connected=false", &offline)
	if offline.Pagination.Total != 0 || len(offline.Data) != 0 {
		t.Errorf("connected=false = %d rows (total %d), want none", len(offline.Data), offline.Pagination.Total)
	}

	var byType clientList
	s.getJSON("/api/v1/clients?client_type=WIRELESS", &byType)
	if byType.Pagination.Total != 1 || len(byType.Data) != 1 {
		t.Fatalf("client_type=WIRELESS = %d rows, want 1", len(byType.Data))
	}
	if deref(byType.Data[0].Hostname) != e2eWirelessHost {
		t.Errorf("client_type=WIRELESS returned %s, want %s", deref(byType.Data[0].Hostname), e2eWirelessHost)
	}

	var bySite clientList
	s.getJSON("/api/v1/clients?site_id="+siteID, &bySite)
	if bySite.Pagination.Total != e2eClientTotal {
		t.Errorf("site_id=%s total = %d, want %d", siteID, bySite.Pagination.Total, e2eClientTotal)
	}
	var otherSite clientList
	s.getJSON("/api/v1/clients?site_id=2a3b4c5d-6e7f-4a8b-9c0d-1e2f3a4b5c6d", &otherSite)
	if otherSite.Pagination.Total != 0 {
		t.Errorf("site_id for an unknown site total = %d, want 0", otherSite.Pagination.Total)
	}

	// `mac` is an exact match, and the handler normalizes the caller's value, so
	// an upper-case query finds the lower-case row the cache stores.
	var byMAC clientList
	s.getJSON("/api/v1/clients?mac="+strings.ToUpper(e2eWirelessMAC), &byMAC)
	if byMAC.Pagination.Total != 1 || len(byMAC.Data) != 1 || byMAC.Data[0].ID != wireless.ID {
		t.Errorf("mac=<upper-case> = %+v, want the one normalized row", byMAC)
	}

	// `ssid` is published but inert — the column is always null in phase 1 — so
	// it answers zero rows rather than being rejected or ignored.
	var bySSID clientList
	s.getJSON("/api/v1/clients?ssid=office", &bySSID)
	if bySSID.Pagination.Total != 0 {
		t.Errorf("ssid=office total = %d, want 0 (the contract documents the filter as inert)", bySSID.Pagination.Total)
	}

	// `uplink_device_id` is validated as a UUID, but the column holds the UniFi
	// device id the console reported (the contract's Client description says
	// "UniFi id", and the adapter copies `uplinkDeviceId` through), while the
	// filter's own description says "local device id". This fixture's device ids
	// are not UUID-shaped, so all that can be shown here is that the filter
	// matches nothing; whether it works against a real console depends on the
	// shape of UniFi's ids (ADR-001's sanitized client sample renders them
	// `<device-uuid>`). Pinned here, and raised on the issue rather than
	// resolved in a test.
	var byUplink clientList
	s.getJSON("/api/v1/clients?uplink_device_id=2a3b4c5d-6e7f-4a8b-9c0d-1e2f3a4b5c6d", &byUplink)
	if byUplink.Pagination.Total != 0 {
		t.Errorf("uplink_device_id=<uuid> total = %d, want 0 while the column holds UniFi ids", byUplink.Pagination.Total)
	}
	if wireless.UplinkDeviceID == nil || uuidRe.MatchString(*wireless.UplinkDeviceID) {
		t.Errorf("uplink_device_id = %v, want the console's own non-UUID device id", wireless.UplinkDeviceID)
	}

	var first clientList
	s.getJSON("/api/v1/clients?page_size=1", &first)
	if len(first.Data) != 1 || first.Pagination.Total != e2eClientTotal || first.Pagination.TotalPages != e2eClientTotal {
		t.Fatalf("page_size=1 = %d rows, total %d, total_pages %d; want 1/%d/%d",
			len(first.Data), first.Pagination.Total, first.Pagination.TotalPages, e2eClientTotal, e2eClientTotal)
	}
	var second clientList
	s.getJSON("/api/v1/clients?page=2&page_size=1", &second)
	if len(second.Data) != 1 || second.Pagination.Page != 2 {
		t.Fatalf("page=2&page_size=1 = %d rows on page %d, want 1 row on page 2", len(second.Data), second.Pagination.Page)
	}
	if first.Data[0].ID == second.Data[0].ID {
		t.Errorf("pages 1 and 2 returned the same client %s: paging is not stable", first.Data[0].ID)
	}

	var sorted clientList
	s.getJSON("/api/v1/clients?sort=hostname&order=asc", &sorted)
	if len(sorted.Data) != e2eClientTotal {
		t.Fatalf("sort=hostname = %d rows, want %d", len(sorted.Data), e2eClientTotal)
	}
	if deref(sorted.Data[0].Hostname) != e2eWirelessHost || deref(sorted.Data[1].Hostname) != e2eWiredHost {
		t.Errorf("sort=hostname&order=asc = %s, %s; want %s, %s",
			deref(sorted.Data[0].Hostname), deref(sorted.Data[1].Hostname), e2eWirelessHost, e2eWiredHost)
	}
	var reversed clientList
	s.getJSON("/api/v1/clients?sort=hostname&order=desc", &reversed)
	if deref(reversed.Data[0].Hostname) != e2eWiredHost {
		t.Errorf("sort=hostname&order=desc starts with %s, want %s", deref(reversed.Data[0].Hostname), e2eWiredHost)
	}
}

// TestClientValidationErrors is the negative matrix of the client routes: every
// contract violation is a 400 validation_failed naming the offending field, and
// nothing is silently clamped or ignored.
func TestClientValidationErrors(t *testing.T) {
	s := startStack(t, stackOptions{unifiEnabled: true})
	s.waitForSynced(e2eDeviceTotal)

	cases := []struct {
		name  string
		path  string
		field string
	}{
		{"list page_size above max", "/api/v1/clients?page_size=201", "page_size"},
		{"list page below one", "/api/v1/clients?page=0", "page"},
		{"list sort unknown", "/api/v1/clients?sort=bogus", "sort"},
		{"list order unknown", "/api/v1/clients?order=bogus", "order"},
		{"list site_id not a uuid", "/api/v1/clients?site_id=not-a-uuid", "site_id"},
		{"list connected not a boolean", "/api/v1/clients?connected=yes", "connected"},
		{"list client_type unknown", "/api/v1/clients?client_type=MOBILE", "client_type"},
		{"list mac malformed", "/api/v1/clients?mac=zz:zz", "mac"},
		{"list uplink_device_id not a uuid", "/api/v1/clients?uplink_device_id=e2e-dev-1", "uplink_device_id"},
		{"list unknown query param", "/api/v1/clients?bogus_param=1", "bogus_param"},
		{"detail id not a uuid", "/api/v1/clients/not-a-uuid", "id"},
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

// TestClientDisconnectReconnectTransitions drives the state machine
// `client_events` exists for: the console stops reporting a client, the worker
// marks it disconnected and appends one event; the client comes back and the
// worker appends exactly one more. The log is read from Postgres — it is
// internal, and the API deliberately does not expose it.
func TestClientDisconnectReconnectTransitions(t *testing.T) {
	// A one-second interval is what makes the transitions observable inside a
	// test; the stale window still has to exceed it.
	s := startStack(t, stackOptions{unifiEnabled: true, interval: time.Second, staleAfter: 30 * time.Second})
	s.waitForSynced(e2eDeviceTotal)

	wireless := s.clientByHostname(e2eWirelessHost)

	// First observation is the client's first 'connected'. Two more cycles
	// change nothing, so the log must not grow: only a state transition writes
	// a row.
	s.waitForCycles(2)
	s.waitForEvents(wireless.ID, []string{"connected"})

	// The console stops reporting the client. Integration API v1 has no
	// per-client state, so "disconnected" is the absence of a row in the active
	// list, and the worker has to notice that absence.
	s.mock.control("clients/" + e2eWirelessUnifiID + "/absent")
	defer s.mock.reset()

	var offline clientRow
	s.waitFor(60*time.Second, "the worker to mark the client disconnected", func() bool {
		row, ok := s.clientByID(wireless.ID)
		if !ok || row.IsConnected {
			return false
		}
		offline = row
		return true
	})

	if offline.DisconnectedAt == nil {
		t.Error("disconnected client has no disconnected_at")
	} else if _, err := time.Parse(time.RFC3339, *offline.DisconnectedAt); err != nil {
		t.Errorf("disconnected_at %q is not RFC3339: %v", *offline.DisconnectedAt, err)
	}
	// The row keeps its history: the disconnect does not rewrite when the client
	// connected or when it was first seen.
	if deref(offline.ConnectedAt) != deref(wireless.ConnectedAt) {
		t.Errorf("connected_at = %s after the disconnect, want the original %s",
			deref(offline.ConnectedAt), deref(wireless.ConnectedAt))
	}
	if offline.FirstSeen != wireless.FirstSeen {
		t.Errorf("first_seen = %s after the disconnect, want the original %s", offline.FirstSeen, wireless.FirstSeen)
	}

	// The historical row is what connected=false is for, and it is served. The
	// other client never left the active list, so the diff is per client.
	var history clientList
	s.getJSON("/api/v1/clients?connected=false", &history)
	if history.Pagination.Total != 1 || len(history.Data) != 1 || history.Data[0].ID != wireless.ID {
		t.Fatalf("connected=false = %+v, want only the disconnected client", history)
	}
	var stillActive clientList
	s.getJSON("/api/v1/clients?connected=true", &stillActive)
	if stillActive.Pagination.Total != 1 || len(stillActive.Data) != 1 || deref(stillActive.Data[0].Hostname) != e2eWiredHost {
		t.Fatalf("connected=true = %+v, want only %s", stillActive, e2eWiredHost)
	}

	s.waitForEvents(wireless.ID, []string{"connected", "disconnected"})

	// The client comes back, and the console reports a fresh connect time.
	// Truncated to a second because that is the precision the fixture reports.
	reconnectedAt := time.Now().UTC().Truncate(time.Second)
	s.mock.control("clients/" + e2eWirelessUnifiID + "/present")

	var back clientRow
	s.waitFor(60*time.Second, "the worker to mark the client connected again", func() bool {
		row, ok := s.clientByID(wireless.ID)
		if !ok || !row.IsConnected {
			return false
		}
		back = row
		return true
	})

	if back.DisconnectedAt != nil {
		t.Errorf("disconnected_at = %s after the reconnect, want null", *back.DisconnectedAt)
	}
	if back.ConnectedAt == nil {
		t.Fatal("reconnected client has no connected_at")
	}
	reported, err := time.Parse(time.RFC3339, *back.ConnectedAt)
	if err != nil {
		t.Fatalf("connected_at %q is not RFC3339: %v", *back.ConnectedAt, err)
	}
	if reported.Before(reconnectedAt) {
		t.Errorf("connected_at = %s, want the reconnect time the console reported (%s or later)",
			reported, reconnectedAt)
	}
	if back.FirstSeen != wireless.FirstSeen {
		t.Errorf("first_seen = %s after the reconnect, want the original %s", back.FirstSeen, wireless.FirstSeen)
	}

	s.waitForEvents(wireless.ID, []string{"connected", "disconnected", "connected"})

	// Both clients are active again, and the historical filter is empty.
	var active clientList
	s.getJSON("/api/v1/clients?connected=true", &active)
	if active.Pagination.Total != e2eClientTotal {
		t.Errorf("connected=true total = %d after the reconnect, want %d", active.Pagination.Total, e2eClientTotal)
	}
	var historical clientList
	s.getJSON("/api/v1/clients?connected=false", &historical)
	if historical.Pagination.Total != 0 {
		t.Errorf("connected=false total = %d after the reconnect, want 0", historical.Pagination.Total)
	}

	// The reconnect settles: further cycles must not append a 'connected' every
	// time they see the client.
	s.waitForCycles(2)
	events, err := s.clientEvents(wireless.ID)
	if err != nil {
		t.Fatalf("read client_events: %v", err)
	}
	if want := []string{"connected", "disconnected", "connected"}; !slices.Equal(events, want) {
		t.Errorf("client_events = %v after two more cycles, want %v", events, want)
	}

	// The cache row agrees with the API, and it is still the console's source.
	cached, err := s.cachedClients()
	if err != nil {
		t.Fatalf("read the clients table: %v", err)
	}
	row, ok := cached[e2eWirelessUnifiID]
	if !ok {
		t.Fatalf("client %s is missing from the clients table", e2eWirelessUnifiID)
	}
	if !row.IsConnected || row.DisconnectedAt != nil {
		t.Errorf("cached client = %+v, want connected with no disconnected_at", row)
	}
	if row.Source != "console" {
		t.Errorf("cached client source = %q, want console", row.Source)
	}
}
