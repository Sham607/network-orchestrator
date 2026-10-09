package unifi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Mode selects which UniFi transport a Client uses.
//
//	cloud   — Site Manager cloud API (https://api.ui.com/v1). Fleet-level:
//	          hosts, sites and devices. Reachable from anywhere, read-only.
//	console — per-console Integration API, reached through the cloud Connector
//	          Proxy. Site-scoped detail: devices, clients, networks.
//
// The two transports are separate capabilities of the same boundary; callers
// pick the one that matches the data they need (docs/adr/001-unifi-api-surface.md).
type Mode string

const (
	ModeCloud   Mode = "cloud"
	ModeConsole Mode = "console"
)

const (
	defaultCloudBaseURL = "https://api.ui.com"
	// consoleAPIPath is the Connector Proxy path from ADR-001, verified live:
	// https://api.ui.com/v1/connector/consoles/{consoleId}/proxy/network/integration/v1
	consoleAPIPath = "/v1/connector/consoles/%s/proxy/network/integration/v1"

	defaultTimeout        = 10 * time.Second
	defaultMaxRetries     = 3
	defaultRetryBase      = 1 * time.Second
	defaultMaxRetryWindow = 30 * time.Second

	// maxResponseBytes bounds how much of a response body we read. UniFi
	// responses are small; this is only a guard against a runaway stream.
	maxResponseBytes = 8 << 20

	// maxPages caps the pagination loop so a misbehaving totalCount can never
	// produce an unbounded loop.
	maxPages = 100
)

// Config configures the read-only UniFi adapter. The API key is the only
// secret; it is held in memory and never logged, returned, or embedded in an
// error (docs/adr/003-architecture.md §4).
type Config struct {
	Mode Mode

	// APIKey is the Site Manager / Integration API key (X-API-KEY). Required.
	APIKey string

	// CloudBaseURL overrides the Site Manager base URL. Defaults to
	// https://api.ui.com. Overridable so tests and self-hosted setups can
	// point at a different host.
	CloudBaseURL string
	// ConsoleBaseURL overrides the full Connector Proxy Integration API base
	// URL. When empty it is derived from CloudBaseURL and ConsoleID.
	ConsoleBaseURL string
	// ConsoleID is the console (host) id used to build the Connector Proxy
	// path. Required by NewConsoleClient.
	ConsoleID string

	// CAFile optionally pins the controller CA from a PEM file instead of
	// relying on the system roots. TLS verification is never disabled.
	CAFile string

	// HTTPClient, when set, is used verbatim (tests inject httptest clients).
	HTTPClient *http.Client

	// Timeout is the per-attempt deadline. Defaults to 10s.
	Timeout time.Duration
	// MaxRetries is the number of retries after the first attempt for
	// transient failures. Defaults to 3.
	MaxRetries int
	// RetryBase is the first backoff step; it doubles per retry. Defaults to 1s.
	RetryBase time.Duration
	// MaxRetryWindow caps total time spent retrying one call. Defaults to 30s.
	MaxRetryWindow time.Duration
}

// NewClient returns the transport selected by cfg.Mode. An empty mode defaults
// to cloud, matching config.UnifiMode's default.
func NewClient(cfg Config) (Client, error) {
	switch cfg.Mode {
	case "", ModeCloud:
		return NewCloudClient(cfg)
	case ModeConsole:
		return NewConsoleClient(cfg)
	default:
		return nil, fmt.Errorf("unifi: unknown mode %q (want %q or %q)", cfg.Mode, ModeCloud, ModeConsole)
	}
}

// NewCloudClient returns a Client backed by the Site Manager cloud API.
// ListClients and ListNetworks are not available on this transport and return
// ErrNotFound; use NewConsoleClient for per-site detail.
func NewCloudClient(cfg Config) (Client, error) {
	t, err := newTransport(cfg)
	if err != nil {
		return nil, err
	}
	base, err := normalizeBaseURL(cfg.CloudBaseURL, defaultCloudBaseURL)
	if err != nil {
		return nil, err
	}
	return &cloudClient{t: t, baseURL: base}, nil
}

// NewConsoleClient returns a Client backed by the per-console Integration API
// through the cloud Connector Proxy.
func NewConsoleClient(cfg Config) (Client, error) {
	consoleID := strings.TrimSpace(cfg.ConsoleID)
	if consoleID == "" {
		return nil, errors.New("unifi: ConsoleID is required for the console transport")
	}
	t, err := newTransport(cfg)
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(cfg.ConsoleBaseURL)
	if raw == "" {
		cloud, err := normalizeBaseURL(cfg.CloudBaseURL, defaultCloudBaseURL)
		if err != nil {
			return nil, err
		}
		raw = cloud + fmt.Sprintf(consoleAPIPath, url.PathEscape(consoleID))
	}
	base, err := normalizeBaseURL(raw, "")
	if err != nil {
		return nil, err
	}
	return &consoleClient{t: t, baseURL: base, consoleID: consoleID}, nil
}

func newTransport(cfg Config) (*transport, error) {
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		return nil, errors.New("unifi: API key is required")
	}
	hc := cfg.HTTPClient
	if hc == nil {
		var err error
		hc, err = defaultHTTPClient(cfg.CAFile)
		if err != nil {
			return nil, err
		}
	}
	t := &transport{
		http:           hc,
		apiKey:         apiKey,
		timeout:        cfg.Timeout,
		maxRetries:     cfg.MaxRetries,
		retryBase:      cfg.RetryBase,
		maxRetryWindow: cfg.MaxRetryWindow,
	}
	if t.timeout <= 0 {
		t.timeout = defaultTimeout
	}
	if t.maxRetries <= 0 {
		t.maxRetries = defaultMaxRetries
	}
	if t.retryBase <= 0 {
		t.retryBase = defaultRetryBase
	}
	if t.maxRetryWindow <= 0 {
		t.maxRetryWindow = defaultMaxRetryWindow
	}
	return t, nil
}

func defaultHTTPClient(caFile string) (*http.Client, error) {
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment}
	if strings.TrimSpace(caFile) != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("unifi: cannot read CA file: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("unifi: CA file contains no usable certificates")
		}
		// Pin the CA instead of disabling verification (ADR-003 failure mode #6).
		tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return &http.Client{Transport: tr}, nil
}

func normalizeBaseURL(raw, fallback string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		raw = fallback
	}
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("unifi: invalid base URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("unifi: base URL scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return "", errors.New("unifi: base URL has no host")
	}
	return raw, nil
}

// transport performs GETs with a bounded retry policy. It is shared by both
// transports so timeout, retry and error mapping behave identically.
type transport struct {
	http           *http.Client
	apiKey         string
	timeout        time.Duration
	maxRetries     int
	retryBase      time.Duration
	maxRetryWindow time.Duration
}

// getJSON issues a GET and decodes a 2xx body into out. Failures are mapped to
// the typed *Error taxonomy; transient kinds are retried with exponential
// backoff, everything else returns immediately.
func (t *transport) getJSON(ctx context.Context, rawURL string, out any) error {
	deadline := time.Now().Add(t.maxRetryWindow)
	for attempt := 0; ; attempt++ {
		body, status, retryAfter, err := t.do(ctx, rawURL)
		if err == nil {
			switch {
			case status < 200 || status >= 300:
				err = classifyStatus(status, retryAfter)
			default:
				if decErr := json.Unmarshal(body, out); decErr != nil {
					return &Error{
						Kind:       ErrSchemaDrift,
						HTTPStatus: status,
						Message:    "response body did not decode as expected JSON",
						Cause:      decErr,
					}
				}
				return nil
			}
		}

		var typed *Error
		if !errors.As(err, &typed) || !typed.Kind.Transient() {
			return err
		}
		if attempt >= t.maxRetries || time.Now().After(deadline) {
			return err
		}
		if waitErr := t.wait(ctx, typed, attempt); waitErr != nil {
			return waitErr
		}
	}
}

func (t *transport) do(ctx context.Context, rawURL string) (body []byte, status int, retryAfter time.Duration, err error) {
	reqCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	req, reqErr := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if reqErr != nil {
		return nil, 0, 0, &Error{Kind: ErrUnreachable, Message: "cannot build request", Cause: reqErr}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-API-KEY", t.apiKey)

	resp, doErr := t.http.Do(req)
	if doErr != nil {
		return nil, 0, 0, &Error{Kind: ErrUnreachable, Message: "request to UniFi failed", Cause: t.sanitize(doErr)}
	}
	defer resp.Body.Close()

	// Read one byte past the limit so an oversized body is detected instead of
	// silently truncated. A truncated body fails to decode and would be
	// reported as unifi_schema_drift, which must stay a trustworthy signal that
	// UniFi changed shape — a size problem is not a schema problem (review F2).
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if readErr != nil {
		return nil, resp.StatusCode, 0, &Error{Kind: ErrUnreachable, Message: "reading UniFi response failed", Cause: t.sanitize(readErr)}
	}
	if len(body) > maxResponseBytes {
		// Transport-level: we could not obtain the response in full, so this is
		// unifi_unreachable, deliberately not unifi_schema_drift.
		return nil, resp.StatusCode, 0, &Error{
			Kind:       ErrUnreachable,
			HTTPStatus: resp.StatusCode,
			Message:    fmt.Sprintf("response body exceeded the %d-byte limit (read %d bytes); this is a size limit, not a schema change", maxResponseBytes, len(body)),
		}
	}
	return body, resp.StatusCode, parseRetryAfter(resp.Header.Get("Retry-After")), nil
}

// sanitize replaces any incidental occurrence of the API key in a cause's text
// with a fixed marker. The key travels only in a header, so this is defense in
// depth: no error path may ever surface it.
func (t *transport) sanitize(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if t.apiKey != "" {
		msg = strings.ReplaceAll(msg, t.apiKey, "***redacted***")
	}
	return errors.New(msg)
}

func (t *transport) wait(ctx context.Context, e *Error, attempt int) error {
	d := e.RetryAfter
	if d <= 0 {
		d = t.retryBase * time.Duration(1<<uint(attempt))
	}
	// Full jitter: spread retries so N sites do not resynchronize onto the
	// controller at the same instant.
	if half := d / 2; half > 0 {
		d += time.Duration(rand.Int63n(int64(half)))
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return &Error{Kind: ErrUnreachable, Message: "retry canceled", Cause: ctx.Err()}
	case <-timer.C:
		return nil
	}
}

func parseRetryAfter(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if secs, err := strconv.Atoi(raw); err == nil {
		if secs < 0 {
			secs = 0
		}
		return time.Duration(secs) * time.Second
	}
	if when, err := http.ParseTime(raw); err == nil {
		d := time.Until(when)
		if d < 0 {
			d = 0
		}
		return d
	}
	return 0
}

// classifyStatus maps an HTTP status onto the fixed error taxonomy. Statuses
// that a read endpoint should never produce (405 on a write-only route, an
// unexpected 4xx) are reported as schema drift: they mean our assumption about
// the upstream contract no longer holds.
func classifyStatus(status int, retryAfter time.Duration) *Error {
	switch status {
	case http.StatusUnauthorized:
		return &Error{Kind: ErrUnauthorized, HTTPStatus: status, Message: "UniFi rejected the credential (401)"}
	case http.StatusForbidden:
		return &Error{Kind: ErrForbidden, HTTPStatus: status, Message: "UniFi credential lacks the required scope (403)"}
	case http.StatusNotFound:
		return &Error{Kind: ErrNotFound, HTTPStatus: status, Message: "UniFi endpoint not found (404)"}
	case http.StatusTooManyRequests:
		return &Error{Kind: ErrRateLimited, HTTPStatus: status, RetryAfter: retryAfter, Message: "UniFi rate limit hit (429)"}
	default:
		if status >= 500 {
			return &Error{Kind: ErrServer, HTTPStatus: status, Message: "UniFi server error"}
		}
		return &Error{Kind: ErrSchemaDrift, HTTPStatus: status, Message: fmt.Sprintf("unexpected HTTP status %d from a read endpoint", status)}
	}
}

// drift builds a schema-drift error. It is used when a 2xx body is valid JSON
// but does not carry the required fields.
func drift(format string, args ...any) *Error {
	return &Error{Kind: ErrSchemaDrift, Message: fmt.Sprintf(format, args...)}
}

// mustRaw re-marshals a decoded item for the domain type's Raw field, which
// exists for audit and drift forensics only. A failure here is impossible for
// values that just decoded, so the error is folded into the drift message.
func mustRaw(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return raw
}

// deviceType classifies a device into the contract's ap|switch|gateway|other.
// The Integration API carries an explicit `features` list; the cloud API does
// not, so we fall back to the console flag and model name. This is a display
// hint, not routing logic.
func deviceType(features []string, model string, isConsole bool) string {
	m := strings.ToLower(model)
	// isConsole and the gateway model families win first: a UDM Pro advertises
	// `switching` as an available feature, yet it is a gateway.
	if isConsole || containsAny(m, "udm", "dream", "gateway", "uxg") {
		return "gateway"
	}
	for _, f := range features {
		switch strings.ToLower(strings.TrimSpace(f)) {
		case "accesspoint":
			return "ap"
		case "switching":
			return "switch"
		case "gateway":
			return "gateway"
		}
	}
	// Flex-family switches are caught by the usw/usl prefixes below, so a bare
	// "flex" only ever reaches the AP branch: a FlexHD access point. Keeping it
	// out of this branch removes the false positive that classified UAP-FlexHD
	// as a switch (review F1).
	switch {
	case containsAny(m, "switch"), strings.HasPrefix(m, "usw"),
		strings.HasPrefix(m, "usl"):
		return "switch"
	case containsAny(m, "uap", "u6", "u7", "flex"), strings.HasPrefix(m, "ac "),
		strings.HasSuffix(m, " ap"), strings.HasPrefix(m, "ap "):
		return "ap"
	default:
		return "other"
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
