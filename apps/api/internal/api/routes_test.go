package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestRouterRegistersOnlyReadRoutes inspects the router's actual route table:
// seven GET read routes plus two POST stubs for gated action endpoints (ZENI-19).
// No other non-GET routes may be registered.
func TestRouterRegistersOnlyReadRoutes(t *testing.T) {
	router := NewRouter(Dependencies{})

	walkable, ok := router.(chi.Routes)
	if !ok {
		t.Fatal("the API handler does not expose a chi route table")
	}

	got := map[string]bool{}
	err := chi.Walk(walkable, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		got[method+" "+route] = true
		if method != http.MethodGet && !(method == http.MethodPost && (route == "/api/v1/devices/{id}/actions" || route == "/api/v1/clients/{id}/actions")) {
			t.Errorf("unauthorized non-GET route registered: %s %s", method, route)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk routes: %v", err)
	}

	want := []string{
		"GET /api/v1/clients",
		"GET /api/v1/clients/{id}",
		"GET /api/v1/devices",
		"GET /api/v1/devices/{id}",
		"GET /api/v1/health",
		"GET /api/v1/networks",
		"GET /api/v1/networks/{id}",
		"GET /api/v1/ready",
		"GET /api/v1/sites",
		"GET /api/v1/sync/status",
		"POST /api/v1/clients/{id}/actions",
		"POST /api/v1/devices/{id}/actions",
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

func TestWriteActionStubsRejectWith403(t *testing.T) {
	router := NewRouter(Dependencies{})

	for _, path := range []string{
		"/api/v1/devices/dev-123/actions",
		"/api/v1/clients/client-456/actions",
	} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Fatalf("POST %s: status = %d, want 403", path, rr.Code)
		}
		var body ErrorBody
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body.Code != CodeForbidden {
			t.Errorf("code = %q, want forbidden", body.Code)
		}
		if body.Message != "write operations are not enabled" {
			t.Errorf("message = %q, want 'write operations are not enabled'", body.Message)
		}
		if body.Details["reason"] != "write_disabled" {
			t.Errorf("details.reason = %v, want write_disabled", body.Details["reason"])
		}

		// Other methods should be rejected (405 Method Not Allowed)
		for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
			badReq := httptest.NewRequest(method, path, nil)
			badRR := httptest.NewRecorder()
			router.ServeHTTP(badRR, badReq)
			if badRR.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s: status = %d, want 405", method, path, badRR.Code)
			}
		}
	}
}
