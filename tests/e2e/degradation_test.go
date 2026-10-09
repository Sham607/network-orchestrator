//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// TestReadyFailsWhenDatabaseUnreachable covers ADR-004's core rule: when the
// API's own database is down, /ready is 503 with the generic
// `service_unavailable` code and `details.reason: database_unreachable` — not a
// 500, and not `unifi_unreachable`. /health stays 200 because liveness must not
// depend on the database.
//
// The API is pointed at a database address where nothing listens, which is the
// observable equivalent of Postgres being stopped: the connection is refused.
func TestReadyFailsWhenDatabaseUnreachable(t *testing.T) {
	s := startStack(t, stackOptions{
		unifiEnabled: false,
		databaseURL:  "postgres://nobody:nobody@127.0.0.1:1/nodb?sslmode=disable",
	})

	if code, body := s.get("/api/v1/health"); code != http.StatusOK {
		t.Fatalf("/health status = %d, want 200 while the database is down\nbody: %s", code, body)
	}

	assertError(t, s, "/api/v1/ready", "service_unavailable", "database_unreachable")
	assertError(t, s, "/api/v1/sites", "service_unavailable", "database_unreachable")
	assertError(t, s, "/api/v1/devices", "service_unavailable", "database_unreachable")

	// /sync/status is served without the database too, so the operator still
	// gets a shape rather than an error.
	if code, body := s.get("/api/v1/sync/status"); code != http.StatusOK {
		t.Fatalf("/sync/status status = %d, want 200\nbody: %s", code, body)
	}
}

// TestReadyFailsAsUnifiUnreachableWhenCacheStale covers the other half of
// ADR-004: a stale (here: never-filled) cache with an offline controller is
// `unifi_unreachable`, because the controller is the broken dependency. The
// API is pointed at a closed port, so no sync can ever succeed.
func TestReadyFailsAsUnifiUnreachableWhenCacheStale(t *testing.T) {
	s := startStack(t, stackOptions{
		unifiEnabled: true,
		unifiURL:     "http://127.0.0.1:1",
		interval:     time.Second,
		staleAfter:   3 * time.Second,
	})

	if code, body := s.get("/api/v1/health"); code != http.StatusOK {
		t.Fatalf("/health status = %d, want 200 while the controller is offline\nbody: %s", code, body)
	}

	readyCode, readyBody := s.get("/api/v1/ready")
	if readyCode != http.StatusServiceUnavailable {
		t.Fatalf("/ready status = %d, want 503\nbody: %s", readyCode, readyBody)
	}
	var ready errorBody
	if err := json.Unmarshal(readyBody, &ready); err != nil {
		t.Fatalf("decode /ready: %v\nbody: %s", err, readyBody)
	}
	if ready.Code != "unifi_unreachable" {
		t.Errorf("/ready code = %q, want unifi_unreachable (a dead database is service_unavailable, a dead controller is not)", ready.Code)
	}
	if ready.Details.Reason != "cache_stale" {
		t.Errorf("/ready details.reason = %q, want cache_stale", ready.Details.Reason)
	}
	s.assertContract("Error", readyBody)

	assertError(t, s, "/api/v1/sites", "unifi_unreachable", "cache_stale")
	assertError(t, s, "/api/v1/devices", "unifi_unreachable", "cache_stale")

	var status struct {
		WorkerRunning bool `json:"worker_running"`
		Stale         bool `json:"stale"`
		Unifi         struct {
			Reachable bool `json:"reachable"`
		} `json:"unifi"`
	}
	s.getJSON("/api/v1/sync/status", &status)
	if !status.WorkerRunning || !status.Stale || status.Unifi.Reachable {
		t.Errorf("/sync/status = %+v, want a running worker, stale cache and unreachable controller", status)
	}
}

// TestControllerGoingOfflineMakesTheCacheStale is the transition the readiness
// gate exists for: a healthy sync, then the controller disappears. While the
// cache is still fresh reads keep working; once it ages past SYNC_STALE_AFTER,
// /ready and the list endpoints degrade in the documented way and /health stays
// up.
func TestControllerGoingOfflineMakesTheCacheStale(t *testing.T) {
	s := startStack(t, stackOptions{
		unifiEnabled: true,
		interval:     time.Second,
		staleAfter:   3 * time.Second,
	})
	s.waitForSynced(e2eDeviceTotal)

	// Fresh cache: readiness is 200 and the gate is closed.
	var ready struct {
		Status string `json:"status"`
		Stale  bool   `json:"stale"`
	}
	s.getJSON("/api/v1/ready", &ready)
	if ready.Status != "ready" || ready.Stale {
		t.Fatalf("/ready before the outage = %+v, want ready and fresh", ready)
	}

	s.mock.control("offline")
	defer s.mock.reset()

	// Wait for staleness to be reported; that is the condition under test, not
	// a sleep standing in for it.
	s.waitFor(30*time.Second, "/sync/status to report a stale cache", func() bool {
		code, body, err := s.do(http.MethodGet, "/api/v1/sync/status", nil)
		if err != nil || code != http.StatusOK {
			return false
		}
		var st struct {
			Stale bool `json:"stale"`
		}
		if err := json.Unmarshal(body, &st); err != nil {
			return false
		}
		return st.Stale
	})

	if code, body := s.get("/api/v1/ready"); code != http.StatusServiceUnavailable {
		t.Fatalf("/ready after going stale = %d, want 503\nbody: %s", code, body)
	} else {
		var e errorBody
		if err := json.Unmarshal(body, &e); err != nil {
			t.Fatalf("decode /ready: %v", err)
		}
		if e.Code != "unifi_unreachable" {
			t.Errorf("/ready code = %q, want unifi_unreachable", e.Code)
		}
	}

	if code, body := s.get("/api/v1/health"); code != http.StatusOK {
		t.Fatalf("/health status = %d, want 200 during the outage\nbody: %s", code, body)
	}

	// The controller probe fails on the next cycle; only then do the list
	// endpoints stop serving the (now untrustworthy) cache.
	s.waitFor(45*time.Second, "/sync/status to report the controller unreachable", func() bool {
		code, body, err := s.do(http.MethodGet, "/api/v1/sync/status", nil)
		if err != nil || code != http.StatusOK {
			return false
		}
		var st struct {
			Unifi struct {
				Reachable bool `json:"reachable"`
			} `json:"unifi"`
		}
		if err := json.Unmarshal(body, &st); err != nil {
			return false
		}
		return !st.Unifi.Reachable
	})

	assertError(t, s, "/api/v1/devices", "unifi_unreachable", "cache_stale")
}

// assertError checks one error response against the contract and the expected
// code/reason pair.
func assertError(t *testing.T, s *stack, path, wantCode, wantReason string) {
	t.Helper()
	code, body := s.get(path)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("GET %s: status %d, want 503\nbody: %s", path, code, body)
	}
	var e errorBody
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("GET %s: decode: %v\nbody: %s", path, err, body)
	}
	if e.Code != wantCode {
		t.Errorf("GET %s: code = %q, want %q", path, e.Code, wantCode)
	}
	if e.Details.Reason != wantReason {
		t.Errorf("GET %s: details.reason = %q, want %q", path, e.Details.Reason, wantReason)
	}
	s.assertContract("Error", body)
}
