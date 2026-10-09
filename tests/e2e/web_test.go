//go:build e2e

package e2e

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestWebDashboardSlice drives the dashboard in a real browser (Playwright,
// Chromium) against the same stack the API suite uses: the real API binary, a
// disposable Postgres schema and a mocked UniFi controller. Nothing about the
// web path can reach a live controller — the browser only talks to the Vite
// dev server, whose /api proxy forwards to the API.
//
// It is gated on E2E_WEB=1 so the plain Go integration job stays free of a
// browser dependency. `make e2e-web` sets the flag and installs what it needs.
func TestWebDashboardSlice(t *testing.T) {
	if os.Getenv("E2E_WEB") != "1" {
		t.Skip("set E2E_WEB=1 (or run `make e2e-web`) to run the Playwright dashboard suite")
	}

	root, err := repoRoot()
	if err != nil {
		t.Fatalf("locate the repository root: %v", err)
	}
	webDir := filepath.Join(root, "apps", "web")

	playwright := filepath.Join(webDir, "node_modules", ".bin", "playwright")
	if _, err := os.Stat(playwright); err != nil {
		t.Fatalf("Playwright is not installed in apps/web: run `npm ci` and `npx playwright install chromium` (%v)", err)
	}

	// The sync must have completed before the browser reads the cache, otherwise
	// the first assertions would race the worker.
	s := startStack(t, stackOptions{unifiEnabled: true})
	s.waitForSynced(e2eDeviceTotal)

	baseURL := startViteDevServer(t, s, webDir)

	cmd := exec.Command(playwright, "test")
	cmd.Dir = webDir
	// The base URL is appended last so it always wins over an inherited value.
	cmd.Env = append(os.Environ(), "PLAYWRIGHT_BASE_URL="+baseURL)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err = cmd.Run()
	t.Logf("playwright output:\n%s", out.String())
	if err != nil {
		t.Fatalf("the Playwright dashboard suite failed: %v", err)
	}
}

// startViteDevServer serves apps/web with its /api proxy pointed at the stack's
// API, and returns the origin the browser should use.
func startViteDevServer(t *testing.T, s *stack, webDir string) string {
	t.Helper()

	vite := filepath.Join(webDir, "node_modules", ".bin", "vite")
	if _, err := os.Stat(vite); err != nil {
		t.Fatalf("Vite is not installed in apps/web: run `npm ci` (%v)", err)
	}

	port := freePort(t)
	url := fmt.Sprintf("http://127.0.0.1:%d", port)

	logs := &bytes.Buffer{}
	cmd := exec.Command(vite, "--host", "127.0.0.1", "--port", strconv.Itoa(port), "--strictPort")
	cmd.Dir = webDir
	cmd.Env = append(os.Environ(), "VITE_API_TARGET="+s.baseURL)
	cmd.Stdout, cmd.Stderr = logs, logs
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the Vite dev server: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url + "/")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				t.Logf("Vite dev server ready at %s (API at %s)", url, s.baseURL)
				return url
			}
		}
		time.Sleep(250 * time.Millisecond)
	}

	t.Fatalf("the Vite dev server never answered at %s\nvite logs:\n%s", url, logs.String())
	return ""
}
