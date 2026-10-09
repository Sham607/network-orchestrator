package api

import (
	"net/http"
	"reflect"
	"sort"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestRouterRegistersOnlyReadRoutes inspects the router's actual route table
// instead of assuming phase 1 is read-only (ZENI-16). A single non-GET handler
// mounted anywhere fails this test.
func TestRouterRegistersOnlyReadRoutes(t *testing.T) {
	router := NewRouter(Dependencies{})

	walkable, ok := router.(chi.Routes)
	if !ok {
		t.Fatal("the API handler does not expose a chi route table")
	}

	got := map[string]bool{}
	err := chi.Walk(walkable, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		got[method+" "+route] = true
		if method != http.MethodGet {
			t.Errorf("non-GET route registered: %s %s (phase 1 is read-only)", method, route)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk routes: %v", err)
	}

	want := []string{
		"GET /api/v1/devices",
		"GET /api/v1/health",
		"GET /api/v1/ready",
		"GET /api/v1/sites",
		"GET /api/v1/sync/status",
	}
	routes := make([]string, 0, len(got))
	for route := range got {
		routes = append(routes, route)
	}
	sort.Strings(routes)

	if !reflect.DeepEqual(routes, want) {
		t.Errorf("route table = %v, want exactly %v", routes, want)
	}
}
