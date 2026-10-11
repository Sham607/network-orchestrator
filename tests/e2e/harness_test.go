//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// e2eUnifiKey is a throwaway fixture credential. It is deliberately short so it
// cannot be mistaken for (or flagged as) a real secret by scripts/check-secrets.sh,
// and it is never the production `unifi_apikey`.
const e2eUnifiKey = "fixture-key"

const (
	e2eSiteID       = "e2e-site-1"
	e2eSiteName     = "E2E HQ"
	e2eDeviceTotal  = 3
	e2eOfflineCount = 1 // devices whose state is OFFLINE in the fixture
)

// stackOptions controls one API-under-test instance.
type stackOptions struct {
	unifiEnabled bool
	// unifiURL overrides the mock. Empty means "start a fresh mock" when
	// unifiEnabled, otherwise unused.
	unifiURL    string
	databaseURL string
	interval    time.Duration
	staleAfter  time.Duration
	// extraEnv overrides the settings above, for tests that need a knob the
	// harness does not otherwise expose (e.g. CLIENT_EVENTS_RETENTION). The
	// entries are applied on top of the defaults, not appended next to them.
	extraEnv map[string]string
}

// syncBuffer collects a child process's output and can be read while that
// process is still writing to it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// stack owns a running API process plus its mock controller.
type stack struct {
	t       *testing.T
	baseURL string
	mock    *mockUnifi
	cmd     *exec.Cmd
	logs    *syncBuffer
	client  *http.Client
}

func startStack(t *testing.T, opts stackOptions) *stack {
	t.Helper()
	if opts.databaseURL == "" {
		opts.databaseURL = databaseURL
	}
	if opts.interval == 0 {
		opts.interval = 60 * time.Second
	}
	if opts.staleAfter == 0 {
		opts.staleAfter = 180 * time.Second
	}
	if opts.staleAfter <= opts.interval {
		t.Fatalf("invalid stack options: staleAfter (%s) must exceed interval (%s)", opts.staleAfter, opts.interval)
	}

	// Each stack starts from an empty cache, so the fixture counts are exact no
	// matter what an earlier test left behind. A stack pointed at a different
	// (deliberately dead) database must not touch the shared one.
	if opts.databaseURL == databaseURL {
		root, err := repoRoot()
		if err != nil {
			t.Fatalf("locate the repo root: %v", err)
		}
		if err := resetDatabase(context.Background(), root); err != nil {
			t.Fatalf("reset the database: %v", err)
		}
	}

	var mock *mockUnifi
	unifiURL := opts.unifiURL
	if opts.unifiEnabled && unifiURL == "" {
		mock = newMockUnifi(t)
		unifiURL = mock.URL()
	}

	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	settings := map[string]string{
		"APP_ENV":          "e2e",
		"HTTP_ADDR":        addr,
		"DATABASE_URL":     opts.databaseURL,
		"SYNC_INTERVAL":    opts.interval.String(),
		"SYNC_STALE_AFTER": opts.staleAfter.String(),
		"LOG_LEVEL":        "warn",
		"UNIFI_ENABLED":    "false",
	}
	if opts.unifiEnabled {
		settings["UNIFI_ENABLED"] = "true"
		settings["UNIFI_MODE"] = "local"
		settings["UNIFI_LOCAL_URL"] = unifiURL
		settings["UNIFI_LOCAL_API_KEY"] = e2eUnifiKey
		settings["UNIFI_CONSOLE_ID"] = "console-e2e"
	}
	// Overrides go into the map, not next to it: the process reads the first
	// matching entry from its environment, so a duplicate name would silently
	// keep the default.
	for name, value := range opts.extraEnv {
		settings[name] = value
	}

	env := cleanEnv()
	for name, value := range settings {
		env = append(env, name+"="+value)
	}

	logs := &syncBuffer{}
	cmd := exec.Command(apiBinary)
	cmd.Env = env
	cmd.Stdout = logs
	cmd.Stderr = logs
	if err := cmd.Start(); err != nil {
		t.Fatalf("start api: %v", err)
	}

	s := &stack{
		t:       t,
		baseURL: "http://" + addr,
		mock:    mock,
		cmd:     cmd,
		logs:    logs,
		client:  &http.Client{Timeout: 15 * time.Second},
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		if mock != nil {
			mock.Close()
		}
	})

	s.waitForStatus("/api/v1/health", http.StatusOK, 20*time.Second)
	return s
}

// cleanEnv strips application configuration and every UNIFI_* variable inherited
// from the developer's shell, so the harness can never reach a live controller
// or depend on a real key by accident. Only the values this file adds are used.
func cleanEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		name := strings.SplitN(kv, "=", 2)[0]
		switch {
		case strings.HasPrefix(name, "UNIFI_"),
			strings.HasPrefix(name, "CLIENT_EVENTS_"),
			name == "DATABASE_URL", name == "HTTP_ADDR", name == "APP_ENV",
			name == "LOG_LEVEL", strings.HasPrefix(name, "SYNC_"):
			continue
		}
		out = append(out, kv)
	}
	return out
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// do performs one request and returns status + raw body.
func (s *stack) do(method, path string, body []byte) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, s.baseURL+path, reader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, raw, nil
}

func (s *stack) get(path string) (int, []byte) {
	s.t.Helper()
	code, body, err := s.do(http.MethodGet, path, nil)
	if err != nil {
		s.t.Fatalf("GET %s: %v", path, err)
	}
	return code, body
}

// getJSON fails the test unless the endpoint answers 200 and decodes into out.
func (s *stack) getJSON(path string, out any) {
	s.t.Helper()
	code, body := s.get(path)
	if code != http.StatusOK {
		s.t.Fatalf("GET %s: status %d, want 200\nbody: %s", path, code, body)
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			s.t.Fatalf("GET %s: decode: %v\nbody: %s", path, err, body)
		}
	}
	s.assertContract(schemaForPath(path), body)
}

// waitFor polls cond until it holds or the deadline passes. Polling a fact is
// how the harness synchronises; there is no fixed sleep standing in for a
// condition.
func (s *stack) waitFor(timeout time.Duration, desc string, cond func() bool) {
	s.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	s.t.Fatalf("timed out after %s waiting for %s\napi logs:\n%s", timeout, desc, s.logs.String())
}

func (s *stack) waitForStatus(path string, want int, timeout time.Duration) {
	s.t.Helper()
	s.waitFor(timeout, fmt.Sprintf("%s to answer %d", path, want), func() bool {
		code, _, err := s.do(http.MethodGet, path, nil)
		return err == nil && code == want
	})
}

// waitForSynced waits until the worker has finished a successful sync and the
// cache holds wantDevices devices.
//
// Both halves are required, and the second one is the point. The worker upserts
// a site's rows inside the cycle, then records the run as successful when the
// cycle ends, so the row count alone says "a cycle is in progress", not "a
// cycle happened". In the gap between the two, /ready still answers
// 503 cache_stale with last_successful_sync_at null, and a test that then
// asserted a healthy cache raced that gap: on CI the merge commit for ZENI-16
// failed TestControllerGoingOfflineMakesTheCacheStale at degradation_test.go:107
// ("GET /api/v1/ready: status 503, want 200") on bytes identical to the PR run
// that passed.
func (s *stack) waitForSynced(wantDevices int) {
	s.t.Helper()
	s.waitFor(30*time.Second, "the sync worker to finish a sync and populate the device cache", func() bool {
		code, body, err := s.do(http.MethodGet, "/api/v1/devices", nil)
		if err != nil || code != http.StatusOK {
			return false
		}
		var page struct {
			Pagination struct {
				Total int `json:"total"`
			} `json:"pagination"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return false
		}
		if page.Pagination.Total != wantDevices {
			return false
		}

		// The rows are in. Now require what /ready and the freshness gate
		// actually read: a cycle that completed, on a cache that is still fresh.
		code, body, err = s.do(http.MethodGet, "/api/v1/sync/status", nil)
		if err != nil || code != http.StatusOK {
			return false
		}
		var status struct {
			Stale   bool `json:"stale"`
			LastRun *struct {
				Status string `json:"status"`
			} `json:"last_run"`
		}
		if err := json.Unmarshal(body, &status); err != nil {
			return false
		}
		return !status.Stale && status.LastRun != nil && status.LastRun.Status == "success"
	})
}

// --- contract validation ----------------------------------------------------

var (
	contractOnce sync.Once
	contractDoc  any
	contractErr  error
)

// assertContract validates a real response body against the components schema
// of openapi/openapi.yaml. `redocly lint` only checks the document against
// itself, so this is the only place the contract meets the shipped bytes.
func (s *stack) assertContract(schemaName string, body []byte) {
	s.t.Helper()
	if schemaName == "" {
		return
	}
	contractOnce.Do(func() {
		root, err := repoRoot()
		if err != nil {
			contractErr = err
			return
		}
		raw, err := os.ReadFile(filepath.Join(root, "openapi", "openapi.yaml"))
		if err != nil {
			contractErr = err
			return
		}
		var doc any
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			contractErr = err
			return
		}
		contractDoc = doc
	})
	if contractErr != nil {
		s.t.Fatalf("load the OpenAPI contract: %v", contractErr)
	}

	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	if err := compiler.AddResource("openapi.yaml", contractDoc); err != nil {
		s.t.Fatalf("register the contract: %v", err)
	}
	schema, err := compiler.Compile("openapi.yaml#/components/schemas/" + schemaName)
	if err != nil {
		s.t.Fatalf("compile components.schemas.%s: %v", schemaName, err)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(body))
	if err != nil {
		s.t.Fatalf("decode %s body: %v\nbody: %s", schemaName, err, body)
	}
	if err := schema.Validate(instance); err != nil {
		s.t.Fatalf("response violates components.schemas.%s: %v\nbody: %s", schemaName, err, body)
	}
}

// schemaForPath maps a read endpoint to its contract schema, so getJSON can
// validate every 200 without the caller repeating the name.
func schemaForPath(path string) string {
	base := path
	if i := strings.IndexByte(base, '?'); i >= 0 {
		base = base[:i]
	}
	switch base {
	case "/api/v1/sites":
		return "SiteList"
	case "/api/v1/devices":
		return "DeviceList"
	case "/api/v1/health":
		return "Health"
	case "/api/v1/ready":
		return "Readiness"
	case "/api/v1/sync/status":
		return "SyncStatus"
	}
	return ""
}

// --- mock UniFi controller --------------------------------------------------

// mockUnifi is a recorded-fixture stand-in for the console Integration API. It
// can be told to fail or to slow down so the harness can exercise the degraded
// paths without ever touching a real controller.
type mockUnifi struct {
	server *httptest.Server

	mu       sync.Mutex
	offline  bool
	delay    time.Duration
	requests int
}

func newMockUnifi(t *testing.T) *mockUnifi {
	t.Helper()
	m := &mockUnifi{}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /info", func(w http.ResponseWriter, r *http.Request) {
		m.respond(w, map[string]any{"applicationVersion": "10.6.106"})
	})
	mux.HandleFunc("GET /sites", func(w http.ResponseWriter, r *http.Request) {
		m.respond(w, consolePage(mockSites()))
	})
	mux.HandleFunc("GET /sites/{siteID}/devices", func(w http.ResponseWriter, r *http.Request) {
		m.respond(w, consolePage(mockDevices()))
	})
	mux.HandleFunc("GET /sites/{siteID}/clients", func(w http.ResponseWriter, r *http.Request) {
		m.respond(w, consolePage(mockClients(r.PathValue("siteID"))))
	})
	mux.HandleFunc("GET /sites/{siteID}/networks", func(w http.ResponseWriter, r *http.Request) {
		m.respond(w, consolePage(mockNetworks(r.PathValue("siteID"))))
	})

	mux.HandleFunc("POST /__control/offline", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		m.offline = true
		m.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /__control/online", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		m.offline = false
		m.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /__control/slow", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		m.delay = 3 * time.Second
		m.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /__control/reset", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		m.offline, m.delay = false, 0
		m.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})

	m.server = httptest.NewServer(mux)
	return m
}

func (m *mockUnifi) URL() string { return m.server.URL }
func (m *mockUnifi) Close()      { m.server.Close() }
func (m *mockUnifi) reset()      { m.control("reset") }

// requestCount counts fixture calls only, so the control endpoints never
// distort the "the read path did not call UniFi" assertion.
func (m *mockUnifi) requestCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.requests
}

func (m *mockUnifi) control(action string) {
	req, _ := http.NewRequest(http.MethodPost, m.server.URL+"/__control/"+action, nil)
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

// respond applies the current mock state and writes a JSON body.
func (m *mockUnifi) respond(w http.ResponseWriter, body any) {
	m.mu.Lock()
	m.requests++
	offline, delay := m.offline, m.delay
	m.mu.Unlock()

	if delay > 0 {
		time.Sleep(delay)
	}
	if offline {
		http.Error(w, `{"code":"unifi_unreachable"}`, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

// consolePage renders the Integration API's offset/limit envelope. The adapter
// trusts totalCount, so reporting every row in one page ends pagination.
func consolePage(data any) map[string]any {
	n := 0
	switch v := data.(type) {
	case []map[string]any:
		n = len(v)
	}
	return map[string]any{
		"offset":     0,
		"limit":      200,
		"count":      n,
		"totalCount": n,
		"data":       data,
	}
}

func mockSites() []map[string]any {
	return []map[string]any{
		{"id": e2eSiteID, "internalReference": "default", "name": e2eSiteName},
	}
}

func mockDevices() []map[string]any {
	return []map[string]any{
		{"id": "e2e-dev-1", "macAddress": "aa:bb:cc:00:00:01", "ipAddress": "10.0.0.11",
			"name": "AP Lobby", "model": "U6 Pro", "state": "ONLINE", "supported": true,
			"firmwareVersion": "6.8.2", "features": []string{"accessPoint"}},
		{"id": "e2e-dev-2", "macAddress": "aa:bb:cc:00:00:02", "ipAddress": "10.0.0.1",
			"name": "UDM HQ", "model": "UDM Pro", "state": "ONLINE", "supported": true,
			"firmwareVersion": "5.1.33", "features": []string{"switching"}},
		{"id": "e2e-dev-3", "macAddress": "aa:bb:cc:00:00:03",
			"name": "Switch Branch", "model": "USW-24", "state": "OFFLINE", "supported": true,
			"firmwareVersion": "7.0.0"},
	}
}

func mockClients(siteID string) []map[string]any {
	return []map[string]any{
		{"type": "WIRELESS", "id": "e2e-client-1", "name": "laptop", "connectedAt": "2026-10-06T15:00:00Z",
			"ipAddress": "10.0.0.50", "macAddress": "aa:bb:cc:00:00:50", "uplinkDeviceId": "e2e-dev-1"},
		{"type": "WIRED", "id": "e2e-client-2", "name": "workstation", "connectedAt": "2026-10-06T15:05:00Z",
			"ipAddress": "10.0.0.51", "macAddress": "aa:bb:cc:00:00:51", "uplinkDeviceId": "e2e-dev-2"},
	}
}

func mockNetworks(siteID string) []map[string]any {
	return []map[string]any{
		{"management": "GATEWAY", "id": "e2e-net-1", "name": "Default", "enabled": true,
			"vlanId": 1, "default": true},
	}
}
