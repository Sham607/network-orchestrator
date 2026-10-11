//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestWriteActionRoutesRejectWrites proves the read-only boundary over HTTP.
//
// The contract (openapi/openapi.yaml, `x-status: proposed-gated`) promises that
// while writes are gated the two action routes answer `403 forbidden` with
// `details.reason: write_disabled`. Both routes are registered with stubs that
// enforce this boundary (ZENI-19).
func TestWriteActionRoutesRejectWrites(t *testing.T) {
	s := startStack(t, stackOptions{unifiEnabled: false})

	target := "2a3b4c5d-6e7f-4a8b-9c0d-1e2f3a4b5c6d"
	for _, path := range []string{
		"/api/v1/devices/" + target + "/actions",
		"/api/v1/clients/" + target + "/actions",
	} {
		code, body, err := s.do(http.MethodPost, path, []byte(`{"action":"RESTART"}`))
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		if code >= 200 && code < 400 {
			t.Fatalf("POST %s accepted a write (status %d): the read-only boundary is broken\nbody: %s", path, code, body)
		}
		if code != http.StatusForbidden {
			t.Errorf("POST %s status = %d, want 403 write_disabled\nbody: %s", path, code, body)
		}
		var errResp errorBody
		if err := json.Unmarshal(body, &errResp); err != nil {
			t.Fatalf("decode: %v\nbody: %s", err, body)
		}
		if errResp.Code != "forbidden" {
			t.Errorf("code = %q, want forbidden", errResp.Code)
		}
		if errResp.Details.Reason != "write_disabled" {
			t.Errorf("details.reason = %v, want write_disabled", errResp.Details.Reason)
		}
	}
}

// TestNoWriteMethodIsAccepted sweeps the write verbs over the read routes and
// the action routes, so a future handler cannot quietly accept one.
func TestNoWriteMethodIsAccepted(t *testing.T) {
	s := startStack(t, stackOptions{unifiEnabled: false})

	paths := []string{
		"/api/v1/sites",
		"/api/v1/devices",
		"/api/v1/health",
		"/api/v1/devices/2a3b4c5d-6e7f-4a8b-9c0d-1e2f3a4b5c6d/actions",
		"/api/v1/clients/2a3b4c5d-6e7f-4a8b-9c0d-1e2f3a4b5c6d/actions",
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		for _, path := range paths {
			code, body, err := s.do(method, path, []byte(`{}`))
			if err != nil {
				t.Fatalf("%s %s: %v", method, path, err)
			}
			if code >= 200 && code < 400 {
				t.Errorf("%s %s accepted a write (status %d)\nbody: %s", method, path, code, body)
			}
		}
	}
}
