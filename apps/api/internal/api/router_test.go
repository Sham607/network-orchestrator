package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Sham607/network-orchestrator/apps/api/internal/config"
)

type fakeDB struct {
	err error
}

func (f fakeDB) Ping(context.Context) error { return f.err }

func testRouter(t *testing.T, db DB) http.Handler {
	t.Helper()
	return NewRouter(Dependencies{
		Config: config.Config{
			UnifiEnabled:   false,
			UnifiMode:      config.UnifiModeCloud,
			SyncStaleAfter: 180000000000, // 3m, in nanoseconds
		},
		Logger: slog.New(slog.NewTextHandler(discard{}, nil)),
		DB:     db,
	})
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func TestHealthIsLivenessOnly(t *testing.T) {
	// Even with a failing database, liveness must stay 200.
	rr := httptest.NewRecorder()
	testRouter(t, fakeDB{err: errors.New("boom")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("health status = %d, want %d", rr.Code, http.StatusOK)
	}
	if got := rr.Header().Get(requestIDHeader); got == "" {
		t.Fatal("expected X-Request-Id response header")
	}
}

func TestReadyFailsWhenDatabaseIsDown(t *testing.T) {
	rr := httptest.NewRecorder()
	testRouter(t, fakeDB{err: errors.New("connection refused")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/ready", nil))

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("ready status = %d, want %d", rr.Code, http.StatusInternalServerError)
	}
	var body ErrorBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Code != CodeInternalError {
		t.Fatalf("code = %q, want %q", body.Code, CodeInternalError)
	}
	if body.Details["request_id"] == "" {
		t.Fatal("expected request_id in error details")
	}
}

func TestReadySucceedsWhenDatabaseIsUp(t *testing.T) {
	rr := httptest.NewRecorder()
	testRouter(t, fakeDB{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/ready", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("ready status = %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestErrorStatusMapping(t *testing.T) {
	cases := map[Code]int{
		CodeValidationFailed: http.StatusBadRequest,
		CodeUnauthorized:     http.StatusUnauthorized,
		CodeForbidden:        http.StatusForbidden,
		CodeNotFound:         http.StatusNotFound,
		CodeRateLimited:      http.StatusTooManyRequests,
		CodeUnifiUnreachable: http.StatusServiceUnavailable,
		CodeUnifiAuthFailed:  http.StatusBadGateway,
		CodeInternalError:    http.StatusInternalServerError,
	}
	for code, want := range cases {
		if got := httpStatus(code); got != want {
			t.Errorf("httpStatus(%q) = %d, want %d", code, got, want)
		}
	}
}
