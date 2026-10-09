package unifi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testAPIKey is a sentinel credential. Tests assert it never leaks into an
// error, a log line, or a test failure message.
const testAPIKey = "sentinel-api-key-do-not-leak-0123456789abcdef"

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// fixtureServer serves each path from the supplied fixtures and records the
// requests it saw. No real network is touched.
func fixtureServer(t *testing.T, routes map[string][]byte) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.RequestURI())
		if r.Header.Get("X-API-KEY") != testAPIKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func newTestCloud(t *testing.T, srv *httptest.Server, mutate ...func(*Config)) Client {
	t.Helper()
	cfg := Config{Mode: ModeCloud, APIKey: testAPIKey, CloudBaseURL: srv.URL, RetryBase: time.Millisecond}
	for _, m := range mutate {
		m(&cfg)
	}
	c, err := NewCloudClient(cfg)
	if err != nil {
		t.Fatalf("NewCloudClient: %v", err)
	}
	return c
}

func newTestConsole(t *testing.T, srv *httptest.Server, mutate ...func(*Config)) Client {
	t.Helper()
	cfg := Config{Mode: ModeConsole, APIKey: testAPIKey, ConsoleBaseURL: srv.URL, ConsoleID: "console-1", RetryBase: time.Millisecond}
	for _, m := range mutate {
		m(&cfg)
	}
	c, err := NewConsoleClient(cfg)
	if err != nil {
		t.Fatalf("NewConsoleClient: %v", err)
	}
	return c
}

func cloudRoutes(t *testing.T) map[string][]byte {
	t.Helper()
	return map[string][]byte{
		"/v1/hosts":   fixture(t, "cloud_hosts.json"),
		"/v1/sites":   fixture(t, "cloud_sites.json"),
		"/v1/devices": fixture(t, "cloud_devices.json"),
	}
}

func TestCloudHealthListSitesAndDevices(t *testing.T) {
	srv, seen := fixtureServer(t, cloudRoutes(t))
	c := newTestCloud(t, srv)
	ctx := context.Background()

	if err := c.Health(ctx); err != nil {
		t.Fatalf("Health: %v", err)
	}

	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatalf("ListSites: %v", err)
	}
	if len(sites) != 2 {
		t.Fatalf("got %d sites, want 2", len(sites))
	}
	if sites[0].ID != "site-1" || sites[0].Name != "default" || sites[0].Desc != "Default" {
		t.Fatalf("unexpected site[0]: %+v", sites[0])
	}

	devices, err := c.ListDevices(ctx, "site-1")
	if err != nil {
		t.Fatalf("ListDevices(site-1): %v", err)
	}
	if len(devices) != 2 {
		t.Fatalf("got %d devices for site-1, want 2", len(devices))
	}
	if devices[0].ID != "dev-1" || devices[0].Type != "ap" || devices[0].Status != "ONLINE" {
		t.Fatalf("unexpected device[0]: %+v", devices[0])
	}
	if devices[0].AdoptedAt.IsZero() {
		t.Fatal("expected dev-1 adoption time to be parsed")
	}
	if len(devices[0].Raw) == 0 {
		t.Fatal("expected raw payload to be preserved for audit")
	}
	if devices[1].Type != "gateway" {
		t.Fatalf("dev-2 type = %q, want gateway", devices[1].Type)
	}

	other, err := c.ListDevices(ctx, "site-2")
	if err != nil {
		t.Fatalf("ListDevices(site-2): %v", err)
	}
	if len(other) != 1 || other[0].Type != "switch" {
		t.Fatalf("unexpected site-2 devices: %+v", other)
	}

	if len(*seen) == 0 {
		t.Fatal("fixture server saw no requests")
	}
}

func TestCloudListDevicesUnknownSiteIsNotFound(t *testing.T) {
	srv, _ := fixtureServer(t, cloudRoutes(t))
	c := newTestCloud(t, srv)

	_, err := c.ListDevices(context.Background(), "does-not-exist")
	if !errors.Is(err, &Error{Kind: ErrNotFound}) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestCloudHasNoClientOrNetworkSurface(t *testing.T) {
	srv, _ := fixtureServer(t, cloudRoutes(t))
	c := newTestCloud(t, srv)
	ctx := context.Background()

	if _, err := c.ListClients(ctx, "site-1"); !errors.Is(err, &Error{Kind: ErrNotFound}) {
		t.Fatalf("ListClients err = %v, want ErrNotFound", err)
	}
	if _, err := c.ListNetworks(ctx, "site-1"); !errors.Is(err, &Error{Kind: ErrNotFound}) {
		t.Fatalf("ListNetworks err = %v, want ErrNotFound", err)
	}
}

func TestCloudMissingRequiredFieldIsSchemaDrift(t *testing.T) {
	routes := cloudRoutes(t)
	routes["/v1/sites"] = fixture(t, "broken_cloud_sites_missing_siteid.json")
	srv, _ := fixtureServer(t, routes)
	c := newTestCloud(t, srv)

	_, err := c.ListSites(context.Background())
	if !errors.Is(err, &Error{Kind: ErrSchemaDrift}) {
		t.Fatalf("err = %v, want ErrSchemaDrift", err)
	}
}

func TestCloudNullDataIsSchemaDrift(t *testing.T) {
	routes := cloudRoutes(t)
	routes["/v1/sites"] = []byte(`{"httpStatusCode":200,"data":null}`)
	srv, _ := fixtureServer(t, routes)
	c := newTestCloud(t, srv)

	_, err := c.ListSites(context.Background())
	if !errors.Is(err, &Error{Kind: ErrSchemaDrift}) {
		t.Fatalf("err = %v, want ErrSchemaDrift", err)
	}
}

func TestCloudDeviceRawIsValidJSON(t *testing.T) {
	// Raw exists for forensics; it must always be decodable.
	srv, _ := fixtureServer(t, cloudRoutes(t))
	c := newTestCloud(t, srv)

	devices, err := c.ListDevices(context.Background(), "site-1")
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if !json.Valid(devices[0].Raw) {
		t.Fatalf("raw payload is not valid JSON: %s", devices[0].Raw)
	}
}
