package unifi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func consoleTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-KEY") != testAPIKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/info":
			_, _ = w.Write(fixture(t, "console_info.json"))
		case "/sites":
			_, _ = w.Write(fixture(t, "console_sites.json"))
		case "/sites/csite-1/devices":
			_, _ = w.Write(fixture(t, "console_devices.json"))
		case "/sites/csite-1/clients":
			// Two pages, split on the offset the adapter sends.
			if r.URL.Query().Get("offset") == "0" {
				_, _ = w.Write(fixture(t, "console_clients_page0.json"))
			} else {
				_, _ = w.Write(fixture(t, "console_clients_page1.json"))
			}
		case "/sites/csite-1/networks":
			_, _ = w.Write(fixture(t, "console_networks.json"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestConsoleHealthSitesDevicesNetworks(t *testing.T) {
	c := newTestConsole(t, consoleTestServer(t))
	ctx := context.Background()

	if err := c.Health(ctx); err != nil {
		t.Fatalf("Health: %v", err)
	}

	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatalf("ListSites: %v", err)
	}
	if len(sites) != 2 || sites[0].ID != "csite-1" || sites[0].Desc != "default" {
		t.Fatalf("unexpected sites: %+v", sites)
	}

	devices, err := c.ListDevices(ctx, "csite-1")
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(devices) != 3 {
		t.Fatalf("got %d devices, want 3", len(devices))
	}
	if devices[0].Type != "ap" || devices[1].Type != "gateway" || devices[2].Type != "switch" {
		t.Fatalf("unexpected device types: %q %q %q", devices[0].Type, devices[1].Type, devices[2].Type)
	}
	if devices[0].Status != "ONLINE" || devices[2].Status != "OFFLINE" {
		t.Fatalf("unexpected statuses: %q %q", devices[0].Status, devices[2].Status)
	}
	if devices[0].SiteID != "csite-1" {
		t.Fatalf("device SiteID = %q, want csite-1", devices[0].SiteID)
	}

	networks, err := c.ListNetworks(ctx, "csite-1")
	if err != nil {
		t.Fatalf("ListNetworks: %v", err)
	}
	if len(networks) != 2 || networks[0].VLAN != 1 || !networks[0].Enabled || networks[1].Name != "Guest" {
		t.Fatalf("unexpected networks: %+v", networks)
	}
	if networks[0].Management != "GATEWAY" || !networks[0].IsDefault || networks[0].Origin != "SYSTEM_DEFINED" ||
		networks[0].Configurable == nil || !*networks[0].Configurable {
		t.Fatalf("unexpected network[0] completeness fields: %+v", networks[0])
	}
	if networks[1].Management != "SWITCH" || networks[1].IsDefault || networks[1].Origin != "USER_DEFINED" ||
		networks[1].Configurable == nil || !*networks[1].Configurable {
		t.Fatalf("unexpected network[1] completeness fields: %+v", networks[1])
	}
}

func TestConsoleClientsPagination(t *testing.T) {
	c := newTestConsole(t, consoleTestServer(t))

	clients, err := c.ListClients(context.Background(), "csite-1")
	if err != nil {
		t.Fatalf("ListClients: %v", err)
	}
	if len(clients) != 3 {
		t.Fatalf("got %d clients, want 3 (two pages)", len(clients))
	}
	if clients[0].ID != "client-1" || clients[2].ID != "client-3" {
		t.Fatalf("pagination lost or reordered clients: %+v", clients)
	}
	if clients[0].Hostname != "laptop-1" || clients[0].APID != "cdev-1" {
		t.Fatalf("unexpected client[0]: %+v", clients[0])
	}
	if clients[0].MAC != "aa:bb:cc:00:00:10" || clients[0].Type != "WIRELESS" || !clients[0].Connected {
		t.Fatalf("unexpected client[0] normalized fields: MAC=%q Type=%q Connected=%v", clients[0].MAC, clients[0].Type, clients[0].Connected)
	}
	if clients[1].MAC != "aa:bb:cc:00:00:11" || clients[1].Type != "WIRED" || !clients[1].Connected {
		t.Fatalf("unexpected client[1] normalized fields: MAC=%q Type=%q Connected=%v", clients[1].MAC, clients[1].Type, clients[1].Connected)
	}
	// v1 does not expose these; stay at the zero value rather than inventing data.
	if clients[0].SSID != "" || clients[0].SignalDBm != 0 {
		t.Fatalf("SSID/SignalDBm must stay unset: %+v", clients[0])
	}
}

func TestConsoleMissingIdIsSchemaDrift(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "broken_console_devices_missing_id.json"))
	}))
	t.Cleanup(srv.Close)
	c := newTestConsole(t, srv)

	_, err := c.ListDevices(context.Background(), "csite-1")
	if !errors.Is(err, &Error{Kind: ErrSchemaDrift}) {
		t.Fatalf("err = %v, want ErrSchemaDrift", err)
	}
}

func TestConsoleMissingDataArrayIsSchemaDrift(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"offset":0,"totalCount":1}`))
	}))
	t.Cleanup(srv.Close)
	c := newTestConsole(t, srv)

	_, err := c.ListSites(context.Background())
	if !errors.Is(err, &Error{Kind: ErrSchemaDrift}) {
		t.Fatalf("err = %v, want ErrSchemaDrift", err)
	}
}

func TestConsoleInfoMissingVersionIsSchemaDrift(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	c := newTestConsole(t, srv)

	if err := c.Health(context.Background()); !errors.Is(err, &Error{Kind: ErrSchemaDrift}) {
		t.Fatalf("err = %v, want ErrSchemaDrift", err)
	}
}

func TestConsoleRequiresConsoleID(t *testing.T) {
	if _, err := NewConsoleClient(Config{APIKey: testAPIKey}); err == nil {
		t.Fatal("expected an error when ConsoleID is missing")
	}
}

func TestConsoleBaseURLDerivedFromCloud(t *testing.T) {
	srv := consoleTestServer(t)
	c := newTestConsole(t, srv) // sets ConsoleBaseURL directly
	if err := c.Health(context.Background()); err != nil {
		t.Fatalf("Health with explicit ConsoleBaseURL: %v", err)
	}
}

func TestConsoleClientMissingIdIsSchemaDrift(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"offset":0,"limit":2,"count":1,"totalCount":1,"data":[{"name":"test"}]}`))
	}))
	t.Cleanup(srv.Close)
	c := newTestConsole(t, srv)

	_, err := c.ListClients(context.Background(), "csite-1")
	if !errors.Is(err, &Error{Kind: ErrSchemaDrift}) {
		t.Fatalf("err = %v, want ErrSchemaDrift", err)
	}
}

func TestNormalizeMAC(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"AA:BB:CC:00:00:10", "aa:bb:cc:00:00:10"},
		{"aa:bb:cc:00:00:10", "aa:bb:cc:00:00:10"},
		{"AA-BB-CC-00-00-10", "aa:bb:cc:00:00:10"},
		{"aabbcc000010", "aa:bb:cc:00:00:10"},
		{"AABBCC000010", "aa:bb:cc:00:00:10"},
		{"aabb.cc00.0010", "aa:bb:cc:00:00:10"},
		{"", ""},
		{"   ", ""},
		{"custom-mac", "custom-mac"},
	}
	for _, tc := range cases {
		got := normalizeMAC(tc.input)
		if got != tc.want {
			t.Errorf("normalizeMAC(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestConsoleListNetworksParsesADR001Payload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Three rows cover the shapes the console is known to send: canonical
		// metadata, no metadata at all, and metadata that omits configurable.
		// The first row also arrives with the casing and padding variance the
		// controller has been seen to produce, which the adapter normalises so
		// that a management filter matches exactly one value.
		_, _ = w.Write([]byte(`{
			"offset": 0,
			"limit": 200,
			"count": 3,
			"totalCount": 3,
			"data": [
				{
					"management": " gateway ",
					"id": "net-adr-1",
					"name": "Corporate",
					"enabled": true,
					"vlanId": 10,
					"metadata": {"origin": "system_defined", "configurable": true},
					"default": true
				},
				{
					"management": "SWITCH",
					"id": "net-adr-2",
					"name": "IoT",
					"enabled": false,
					"vlanId": 30,
					"default": false
				},
				{
					"management": "UNMANAGED",
					"id": "net-adr-3",
					"name": "Lab",
					"enabled": true,
					"vlanId": 40,
					"metadata": {"origin": "USER_DEFINED"},
					"default": false
				}
			]
		}`))
	}))
	t.Cleanup(srv.Close)
	c := newTestConsole(t, srv)

	networks, err := c.ListNetworks(context.Background(), "csite-1")
	if err != nil {
		t.Fatalf("ListNetworks: %v", err)
	}
	if len(networks) != 3 {
		t.Fatalf("got %d networks, want 3", len(networks))
	}

	n1 := networks[0]
	if n1.ID != "net-adr-1" || n1.Name != "Corporate" || n1.VLAN != 10 || !n1.Enabled ||
		n1.Management != "GATEWAY" || !n1.IsDefault || n1.Origin != "SYSTEM_DEFINED" ||
		n1.Configurable == nil || !*n1.Configurable {
		t.Fatalf("unexpected n1 (casing and padding should be normalised): %+v", n1)
	}

	n2 := networks[1]
	if n2.ID != "net-adr-2" || n2.Name != "IoT" || n2.VLAN != 30 || n2.Enabled ||
		n2.Management != "SWITCH" || n2.IsDefault || n2.Origin != "" || n2.Configurable != nil {
		t.Fatalf("unexpected n2 (metadata absent must leave configurable null): %+v", n2)
	}

	n3 := networks[2]
	if n3.Management != "UNMANAGED" || n3.Origin != "USER_DEFINED" || n3.Configurable != nil {
		t.Fatalf("unexpected n3 (configurable omitted must stay null): %+v", n3)
	}
}
