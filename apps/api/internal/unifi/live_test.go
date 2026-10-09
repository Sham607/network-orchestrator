//go:build live

// Live read-only verification against a real UniFi deployment.
//
// This test is excluded from the default build (and therefore from CI) by the
// `live` build tag. It runs only through scripts/smoke.sh phase 2, which calls
// it with UNIFI_SM_API_KEY set; it is skipped when that variable is absent.
// It performs GET requests only — no write endpoint is ever touched.
//
//	UNIFI_SM_API_KEY="$unifi_apikey" go test -tags live -run TestLiveReadOnly ./apps/api/internal/unifi/
package unifi

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveReadOnly(t *testing.T) {
	key := strings.TrimSpace(os.Getenv("UNIFI_SM_API_KEY"))
	if key == "" {
		t.Skip("UNIFI_SM_API_KEY not set; skipping live read-only check (expected in CI)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cloud, err := NewCloudClient(Config{APIKey: key, CloudBaseURL: os.Getenv("UNIFI_SM_BASE_URL")})
	if err != nil {
		t.Fatalf("NewCloudClient: %v", err)
	}
	if err := cloud.Health(ctx); err != nil {
		t.Fatalf("cloud Health: %v", err)
	}
	t.Log("cloud Health: OK (Site Manager credential accepted)")

	sites, err := cloud.ListSites(ctx)
	if err != nil {
		t.Fatalf("cloud ListSites: %v", err)
	}
	if len(sites) == 0 {
		t.Fatal("cloud ListSites returned no sites")
	}
	t.Logf("cloud ListSites: %d sites", len(sites))

	devices, err := cloud.ListDevices(ctx, sites[0].ID)
	if err != nil {
		t.Fatalf("cloud ListDevices(%s): %v", sites[0].ID, err)
	}
	t.Logf("cloud ListDevices: %d devices for site %s", len(devices), sites[0].ID)

	consoleID := strings.TrimSpace(os.Getenv("UNIFI_CONSOLE_ID"))
	if consoleID == "" {
		t.Log("UNIFI_CONSOLE_ID not set; skipping console transport check")
		return
	}

	console, err := NewConsoleClient(Config{APIKey: key, ConsoleID: consoleID})
	if err != nil {
		t.Fatalf("NewConsoleClient: %v", err)
	}
	if err := console.Health(ctx); err != nil {
		t.Fatalf("console Health: %v", err)
	}
	t.Log("console Health: OK (Connector Proxy Integration API reachable, version present)")

	consoleSites, err := console.ListSites(ctx)
	if err != nil {
		t.Fatalf("console ListSites: %v", err)
	}
	if len(consoleSites) == 0 {
		t.Fatal("console ListSites returned no sites")
	}
	siteID := consoleSites[0].ID

	cdevices, err := console.ListDevices(ctx, siteID)
	if err != nil {
		t.Fatalf("console ListDevices: %v", err)
	}
	clients, err := console.ListClients(ctx, siteID)
	if err != nil {
		t.Fatalf("console ListClients: %v", err)
	}
	networks, err := console.ListNetworks(ctx, siteID)
	if err != nil {
		t.Fatalf("console ListNetworks: %v", err)
	}
	t.Logf("console detail for site %s: %d devices, %d clients, %d networks", siteID, len(cdevices), len(clients), len(networks))
}
