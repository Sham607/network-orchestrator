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
