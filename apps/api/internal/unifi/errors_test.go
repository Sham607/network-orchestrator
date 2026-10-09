package unifi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func statusServer(t *testing.T, status int, headers map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":"upstream"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestHTTPStatusMapping(t *testing.T) {
	cases := []struct {
		status int
		want   ErrKind
	}{
		{http.StatusUnauthorized, ErrUnauthorized},
		{http.StatusForbidden, ErrForbidden},
		{http.StatusNotFound, ErrNotFound},
		{http.StatusTooManyRequests, ErrRateLimited},
		{http.StatusInternalServerError, ErrServer},
		{http.StatusBadGateway, ErrServer},
		// A read endpoint should never see these; they mean our contract
		// assumption is wrong, so they degrade to schema drift, not a new code.
		{http.StatusMethodNotAllowed, ErrSchemaDrift},
		{http.StatusBadRequest, ErrSchemaDrift},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			srv := statusServer(t, tc.status, nil)
			c := newTestCloud(t, srv, func(cfg *Config) {
				cfg.MaxRetries = 1
				cfg.RetryBase = time.Millisecond
				cfg.MaxRetryWindow = time.Second
			})
			err := c.Health(context.Background())
			if !errors.Is(err, &Error{Kind: tc.want}) {
				t.Fatalf("status %d -> %v, want %s", tc.status, err, tc.want)
			}
			var typed *Error
			if !errors.As(err, &typed) || typed.HTTPStatus != tc.status {
				t.Fatalf("expected HTTPStatus %d, got %+v", tc.status, typed)
			}
		})
	}
}

func TestRateLimitedCarriesRetryAfter(t *testing.T) {
	srv := statusServer(t, http.StatusTooManyRequests, map[string]string{"Retry-After": "42"})
	// MaxRetryWindow collapses the window so the 42s Retry-After is captured on
	// the error without actually sleeping for it.
	c := newTestCloud(t, srv, func(cfg *Config) {
		cfg.MaxRetries = 1
		cfg.RetryBase = time.Millisecond
		cfg.MaxRetryWindow = time.Nanosecond
	})
	err := c.Health(context.Background())
	var typed *Error
	if !errors.As(err, &typed) || typed.Kind != ErrRateLimited {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
	if typed.RetryAfter <= 0 {
		t.Fatalf("expected a positive RetryAfter, got %s", typed.RetryAfter)
	}
}

func TestParseRetryAfter(t *testing.T) {
	if got := parseRetryAfter("30"); got != 30*time.Second {
		t.Fatalf("seconds form = %s, want 30s", got)
	}
	if got := parseRetryAfter(""); got != 0 {
		t.Fatalf("empty = %s, want 0", got)
	}
	if got := parseRetryAfter("not-a-date"); got != 0 {
		t.Fatalf("garbage = %s, want 0", got)
	}
	if got := parseRetryAfter(time.Now().Add(-time.Minute).UTC().Format(http.TimeFormat)); got != 0 {
		t.Fatalf("past date = %s, want 0", got)
	}
}

func TestTransientFailureIsRetriedThenSucceeds(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "cloud_hosts.json"))
	}))
	t.Cleanup(srv.Close)

	c := newTestCloud(t, srv, func(cfg *Config) {
		cfg.MaxRetries = 2
		cfg.RetryBase = time.Millisecond
		cfg.MaxRetryWindow = time.Second
	})
	if err := c.Health(context.Background()); err != nil {
		t.Fatalf("Health after retries: %v", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

func TestNonTransientFailureIsNotRetried(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	c := newTestCloud(t, srv, func(cfg *Config) {
		cfg.MaxRetries = 3
		cfg.RetryBase = time.Millisecond
	})
	if err := c.Health(context.Background()); !errors.Is(err, &Error{Kind: ErrUnauthorized}) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts = %d, want 1 (credentials are never retried)", got)
	}
}

func TestRetriesAreBounded(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)

	c := newTestCloud(t, srv, func(cfg *Config) {
		cfg.MaxRetries = 2
		cfg.RetryBase = time.Millisecond
		cfg.MaxRetryWindow = time.Second
	})
	start := time.Now()
	if err := c.Health(context.Background()); !errors.Is(err, &Error{Kind: ErrServer}) {
		t.Fatalf("err = %v, want ErrServer", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("attempts = %d, want 3 (bounded, not infinite)", got)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("retries took %s; the window must stay bounded", elapsed)
	}
}

func TestUnreachableTransport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening now

	c, err := NewCloudClient(Config{
		APIKey:         testAPIKey,
		CloudBaseURL:   url,
		MaxRetries:     1,
		RetryBase:      time.Millisecond,
		MaxRetryWindow: time.Second,
	})
	if err != nil {
		t.Fatalf("NewCloudClient: %v", err)
	}
	if err := c.Health(context.Background()); !errors.Is(err, &Error{Kind: ErrUnreachable}) {
		t.Fatalf("err = %v, want ErrUnreachable", err)
	}
}

func TestNonJSONBodyIsSchemaDrift(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>not json</html>"))
	}))
	t.Cleanup(srv.Close)
	c := newTestCloud(t, srv)

	if err := c.Health(context.Background()); !errors.Is(err, &Error{Kind: ErrSchemaDrift}) {
		t.Fatalf("err = %v, want ErrSchemaDrift", err)
	}
}

func TestConstructorRejectsMissingKeyAndBadMode(t *testing.T) {
	if _, err := NewCloudClient(Config{}); err == nil {
		t.Fatal("expected an error for a missing API key")
	}
	if _, err := NewClient(Config{Mode: "bogus", APIKey: testAPIKey}); err == nil {
		t.Fatal("expected an error for an unknown mode")
	}
}

// TestAPIKeyNeverAppearsInErrors walks every failure class and asserts the
// credential cannot surface, including when the test fails.
func TestAPIKeyNeverAppearsInErrors(t *testing.T) {
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := closed.URL
	closed.Close()

	scenarios := []struct {
		name      string
		cfg       Config
		mutate    func(*http.Client)
		callCloud func(Client) error
	}{
		{name: "unreachable", cfg: Config{APIKey: testAPIKey, CloudBaseURL: closedURL, MaxRetries: 1, RetryBase: time.Millisecond}},
		{name: "unauthorized", cfg: Config{APIKey: testAPIKey, CloudBaseURL: statusServer(t, 401, nil).URL}},
		{name: "rate_limited", cfg: Config{APIKey: testAPIKey, CloudBaseURL: statusServer(t, 429, map[string]string{"Retry-After": "0"}).URL, MaxRetries: 1, RetryBase: time.Millisecond}},
		{name: "server_error", cfg: Config{APIKey: testAPIKey, CloudBaseURL: statusServer(t, 500, nil).URL, MaxRetries: 1, RetryBase: time.Millisecond}},
		{name: "schema_drift", cfg: Config{APIKey: testAPIKey, CloudBaseURL: statusServer(t, 405, nil).URL, MaxRetries: 1, RetryBase: time.Millisecond}},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			c, err := NewCloudClient(sc.cfg)
			if err != nil {
				t.Fatalf("NewCloudClient: %v", err)
			}
			err = c.Health(context.Background())
			if err == nil {
				t.Fatal("expected an error")
			}
			assertNoKey(t, err)
		})
	}
}

func assertNoKey(t *testing.T, err error) {
	t.Helper()
	for _, rendered := range []string{err.Error(), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
		if strings.Contains(rendered, testAPIKey) {
			t.Fatalf("API key leaked in error text: %s", rendered)
		}
	}
}

// TestOversizedResponseIsNotSchemaDrift locks review F2: a body past the size
// limit is a transport problem, not a schema change, so the unifi_schema_drift
// alarm stays trustworthy as a "UniFi changed shape" signal.
func TestOversizedResponseIsNotSchemaDrift(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Perfectly valid JSON, padded past the limit: size is the only problem.
		_, _ = w.Write([]byte(`{"httpStatusCode":200,"data":[]}`))
		_, _ = w.Write([]byte(strings.Repeat(" ", maxResponseBytes)))
	}))
	t.Cleanup(srv.Close)

	c := newTestCloud(t, srv, func(cfg *Config) {
		cfg.MaxRetries = 2
		cfg.RetryBase = time.Millisecond
		// Collapse the retry window: one oversized download is enough to prove
		// the classification without a second 8 MB fetch.
		cfg.MaxRetryWindow = time.Nanosecond
	})
	err := c.Health(context.Background())
	if err == nil {
		t.Fatal("expected an error for an oversized response")
	}
	if errors.Is(err, &Error{Kind: ErrSchemaDrift}) {
		t.Fatalf("oversized response must not be reported as schema drift: %v", err)
	}
	if !errors.Is(err, &Error{Kind: ErrUnreachable}) {
		t.Fatalf("err = %v, want ErrUnreachable", err)
	}
	if !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("message should name the size limit, got: %v", err)
	}
	t.Logf("oversized response classified as: %v", err)
	assertNoKey(t, err)
}

func TestSchemaDriftCauseRedacted(t *testing.T) {
	// Decode failures carry the underlying error; it must not include the key.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data": "not-an-array"}`))
	}))
	t.Cleanup(srv.Close)
	c := newTestCloud(t, srv)

	err := c.Health(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	assertNoKey(t, err)
}
