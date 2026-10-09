package inventory

import (
	"context"
	"testing"
	"time"

	"github.com/Sham607/network-orchestrator/apps/api/internal/unifi"
)

// seedReadCache writes three sites and five devices through the production
// write path, so the read tests page over exactly the rows a sync cycle leaves
// behind. Uses a real Postgres: see testPool.
func seedReadCache(t *testing.T, repo *PostgresRepository) {
	t.Helper()
	ctx := context.Background()
	base := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)

	snapshots := []SiteSnapshot{
		{
			Site:      unifi.Site{ID: "site-alpha", Name: "Alpha HQ", Desc: "Head office"},
			ConsoleID: "host-a",
			Source:    SourceCloud,
			SyncedAt:  base,
			Devices: []unifi.Device{
				{ID: "dev-1", SiteID: "site-alpha", MAC: "aa:bb:cc:00:00:01", Name: "U6 Pro", Model: "U6 Pro", Type: "ap", Status: "ONLINE", Firmware: "6.6.55", IP: "10.0.0.11"},
				{ID: "dev-2", SiteID: "site-alpha", MAC: "aa:bb:cc:00:00:02", Name: "USW-24", Model: "USW-24", Type: "switch", Status: "OFFLINE", IP: "10.0.0.12"},
			},
		},
		{
			Site:      unifi.Site{ID: "site-beta", Name: "Beta Branch"},
			ConsoleID: "host-a",
			Source:    SourceCloud,
			SyncedAt:  base.Add(time.Minute),
			Devices: []unifi.Device{
				{ID: "dev-3", SiteID: "site-beta", MAC: "aa:bb:cc:00:00:03", Name: "UDM Pro", Model: "UDM Pro", Type: "gateway", Status: "ONLINE"},
				{ID: "dev-4", SiteID: "site-beta", MAC: "aa:bb:cc:00:00:04", Name: "U6 Lite", Model: "U6 Lite", Type: "ap", Status: "ADOPTING"},
			},
		},
		{
			Site:      unifi.Site{ID: "site-gamma", Name: "gamma Site"},
			ConsoleID: "host-b",
			Source:    SourceConsole,
			SyncedAt:  base.Add(2 * time.Minute),
			// UniFi reported neither name, model nor type for this device: the
			// read side must leave those unknown instead of inventing values.
			Devices: []unifi.Device{{ID: "dev-5", SiteID: "site-gamma", MAC: "aa:bb:cc:00:00:05"}},
		},
	}

	for _, snap := range snapshots {
		if err := repo.ApplySite(ctx, snap); err != nil {
			t.Fatalf("seed %s: %v", snap.Site.ID, err)
		}
	}
}

func siteUUID(t *testing.T, repo *PostgresRepository, unifiSiteID string) string {
	t.Helper()
	var id string
	if err := repo.pool.QueryRow(context.Background(),
		`SELECT id::text FROM sites WHERE unifi_site_id = $1`, unifiSiteID).Scan(&id); err != nil {
		t.Fatalf("read uuid of %s: %v", unifiSiteID, err)
	}
	return id
}

func names(page SitePage) []string {
	out := make([]string, 0, len(page.Items))
	for _, site := range page.Items {
		out = append(out, site.Name)
	}
	return out
}

// TestPostgresListSitesPaginatesWithATrueTotal covers the page walk the
// dashboard performs, including a page past the end: the total must still
// describe the whole filtered set, or the UI would report "no data" while rows
// exist.
func TestPostgresListSitesPaginatesWithATrueTotal(t *testing.T) {
	repo := NewPostgresRepository(testPool(t))
	seedReadCache(t, repo)
	ctx := context.Background()

	query := SiteQuery{Page: 1, PageSize: 2, Sort: "name", Order: "asc"}

	first, err := repo.ListSites(ctx, query)
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if got, want := names(first), []string{"Alpha HQ", "Beta Branch"}; !equal(got, want) {
		t.Fatalf("page 1 = %v, want %v", got, want)
	}
	if first.Total != 3 {
		t.Fatalf("page 1 total = %d, want 3", first.Total)
	}

	query.Page = 2
	second, err := repo.ListSites(ctx, query)
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if got, want := names(second), []string{"gamma Site"}; !equal(got, want) {
		t.Fatalf("page 2 = %v, want %v", got, want)
	}
	if second.Total != 3 {
		t.Fatalf("page 2 total = %d, want 3", second.Total)
	}

	query.Page = 9
	empty, err := repo.ListSites(ctx, query)
	if err != nil {
		t.Fatalf("page 9: %v", err)
	}
	if len(empty.Items) != 0 {
		t.Fatalf("page 9 returned %d rows, want none", len(empty.Items))
	}
	if empty.Total != 3 {
		t.Fatalf("page 9 total = %d, want 3 (the total describes the cache, not the page)", empty.Total)
	}
}

func TestPostgresListSitesSortsInBothDirections(t *testing.T) {
	repo := NewPostgresRepository(testPool(t))
	seedReadCache(t, repo)
	ctx := context.Background()

	desc, err := repo.ListSites(ctx, SiteQuery{Page: 1, PageSize: 10, Sort: "synced_at", Order: "desc"})
	if err != nil {
		t.Fatalf("sort desc: %v", err)
	}
	if got, want := names(desc), []string{"gamma Site", "Beta Branch", "Alpha HQ"}; !equal(got, want) {
		t.Fatalf("synced_at desc = %v, want %v", got, want)
	}

	asc, err := repo.ListSites(ctx, SiteQuery{Page: 1, PageSize: 10, Sort: "name", Order: "desc"})
	if err != nil {
		t.Fatalf("sort name desc: %v", err)
	}
	if got, want := names(asc), []string{"gamma Site", "Beta Branch", "Alpha HQ"}; !equal(got, want) {
		t.Fatalf("name desc = %v, want %v", got, want)
	}
}

func TestPostgresListSitesFiltersByNameSubstring(t *testing.T) {
	repo := NewPostgresRepository(testPool(t))
	seedReadCache(t, repo)
	ctx := context.Background()

	cases := map[string][]string{
		"site": {"gamma Site"}, // case-insensitive both ways
		"hq":   {"Alpha HQ"},
		"a":    {"Alpha HQ", "Beta Branch", "gamma Site"},
		"none": {},
	}
	for query, want := range cases {
		page, err := repo.ListSites(ctx, SiteQuery{Page: 1, PageSize: 10, Sort: "name", Order: "asc", Query: query})
		if err != nil {
			t.Fatalf("q=%q: %v", query, err)
		}
		if got := names(page); !equal(got, want) {
			t.Errorf("q=%q = %v, want %v", query, got, want)
		}
		if page.Total != len(want) {
			t.Errorf("q=%q total = %d, want %d", query, page.Total, len(want))
		}
	}
}

func TestPostgresListDevicesFiltersAndCombines(t *testing.T) {
	repo := NewPostgresRepository(testPool(t))
	seedReadCache(t, repo)
	ctx := context.Background()
	alpha := siteUUID(t, repo, "site-alpha")

	cases := []struct {
		name  string
		query DeviceQuery
		want  []string
	}{
		{"unfiltered", DeviceQuery{}, []string{"U6 Lite", "U6 Pro", "UDM Pro", "USW-24", ""}},
		{"site", DeviceQuery{SiteID: alpha}, []string{"U6 Pro", "USW-24"}},
		{"status", DeviceQuery{Status: "ONLINE"}, []string{"U6 Pro", "UDM Pro"}},
		{"device_type", DeviceQuery{DeviceType: "ap"}, []string{"U6 Lite", "U6 Pro"}},
		{"mac", DeviceQuery{MAC: "aa:bb:cc:00:00:03"}, []string{"UDM Pro"}},
		{"site and status", DeviceQuery{SiteID: alpha, Status: "OFFLINE"}, []string{"USW-24"}},
		{"no match", DeviceQuery{Status: "PROVISIONING"}, nil},
	}

	for _, tc := range cases {
		query := tc.query
		query.Page, query.PageSize, query.Sort, query.Order = 1, 10, "name", "asc"
		page, err := repo.ListDevices(ctx, query)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		got := make([]string, 0, len(page.Items))
		for _, device := range page.Items {
			got = append(got, deref(device.Name))
		}
		if !equal(got, tc.want) {
			t.Errorf("%s = %v, want %v", tc.name, got, tc.want)
		}
		if page.Total != len(tc.want) {
			t.Errorf("%s total = %d, want %d", tc.name, page.Total, len(tc.want))
		}
	}
}

// TestPostgresListDevicesLeavesUnknownFieldsNil pins the null contract: the
// transport renders these as explicit nulls, and the dashboard renders null as
// "unknown" instead of a fabricated value (mock-expectations §4).
func TestPostgresListDevicesLeavesUnknownFieldsNil(t *testing.T) {
	repo := NewPostgresRepository(testPool(t))
	seedReadCache(t, repo)

	page, err := repo.ListDevices(context.Background(), DeviceQuery{Page: 1, PageSize: 10, Sort: "name", Order: "asc"})
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if len(page.Items) != 5 {
		t.Fatalf("got %d devices, want 5", len(page.Items))
	}

	// Sorted by name ascending, the unnamed device is last.
	unknown := page.Items[len(page.Items)-1]
	if unknown.Name != nil || unknown.Model != nil || unknown.Status != nil {
		t.Fatalf("unknown device = %+v, want null name/model/status", unknown)
	}
	if unknown.UptimeSec != nil {
		t.Fatalf("uptime_sec = %v, want null (the Integration API exposes no telemetry)", *unknown.UptimeSec)
	}
	// The contract requires a device_type on every row, and the cache stores a
	// controlled vocabulary, so an unknown device is 'other' rather than null.
	if unknown.DeviceType != "other" {
		t.Fatalf("device_type = %q, want %q", unknown.DeviceType, "other")
	}
	if unknown.MAC == nil || *unknown.MAC != "aa:bb:cc:00:00:05" {
		t.Fatalf("mac = %v, want the reported MAC", unknown.MAC)
	}
	if unknown.ID == "" || unknown.SiteID == "" {
		t.Fatalf("id/site_id = %q/%q, want the control-plane uuids", unknown.ID, unknown.SiteID)
	}

	// A device UniFi knows more about keeps its values.
	pro := deviceNamed(page, "U6 Pro")
	if deref(pro.Name) != "U6 Pro" || deref(pro.Status) != "ONLINE" || deref(pro.IPAddress) != "10.0.0.11" {
		t.Fatalf("U6 Pro = %+v, want the synced values", pro)
	}
}

// TestListRejectsASortTheContractDoesNotDeclare keeps user input out of the SQL
// text: the whitelist is the only thing that can reach ORDER BY. A repository
// is not reached without a pool, which proves the check happens first.
func TestListRejectsASortTheContractDoesNotDeclare(t *testing.T) {
	repo := NewPostgresRepository(nil)

	if _, err := repo.ListSites(context.Background(), SiteQuery{Page: 1, PageSize: 10, Sort: "raw", Order: "asc"}); err == nil {
		t.Error("ListSites accepted an undeclared sort field")
	}
	if _, err := repo.ListDevices(context.Background(), DeviceQuery{Page: 1, PageSize: 10, Sort: "name", Order: "sideways"}); err == nil {
		t.Error("ListDevices accepted an undeclared sort order")
	}
}

func equal(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func deviceNamed(page DevicePage, name string) Device {
	for _, device := range page.Items {
		if deref(device.Name) == name {
			return device
		}
	}
	return Device{}
}
