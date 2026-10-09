//go:build e2e

// Package e2e is the end-to-end harness for the Network Orchestrator vertical
// slice (ZENI-16). It runs the real API binary against a real Postgres cache and
// a mocked UniFi controller, and asserts the user-visible behaviour of the
// slice: GET /sites -> GET /devices, plus /health, /ready and /sync/status.
//
// Hard constraints (see the issue and ADR-003 §4):
//   - UniFi is never called live. The adapter is pointed at an httptest server
//     that serves recorded fixtures.
//   - No UniFi credential is required. The fixture key is a throwaway value,
//     not a `unifi_apikey`; the test strips any inherited UNIFI_* variables
//     from the child process so a developer's real key can never leak in.
//   - Postgres comes from E2E_DATABASE_URL, which must point at a disposable
//     database: the harness drops the schema and re-applies the migrations.
package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// apiBinary is the compiled API under test. Built once in TestMain so every
// stack starts the same binary the Docker image runs.
var (
	apiBinary   string
	databaseURL string
)

func TestMain(m *testing.M) {
	databaseURL = os.Getenv("E2E_DATABASE_URL")
	if databaseURL == "" {
		fmt.Fprintln(os.Stderr, "e2e: E2E_DATABASE_URL is required (point it at a disposable Postgres)")
		os.Exit(2)
	}

	root, err := repoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e:", err)
		os.Exit(2)
	}

	dir, err := os.MkdirTemp("", "network-orchestrator-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e:", err)
		os.Exit(2)
	}
	defer os.RemoveAll(dir)

	apiBinary = filepath.Join(dir, "api")
	build := exec.Command("go", "build", "-o", apiBinary, "./apps/api/cmd/api")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: go build failed: %v\n%s\n", err, out)
		os.Exit(1)
	}

	if err := resetDatabase(context.Background(), root); err != nil {
		fmt.Fprintln(os.Stderr, "e2e: cannot prepare the database:", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// repoRoot walks up from the test's working directory (tests/e2e) to the module
// root, which holds go.mod and migrations/.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}

// resetDatabase drops every table and re-applies the migrations, so the harness
// starts from zero no matter what a previous run left behind. E2E_DATABASE_URL
// is documented as disposable for exactly this reason.
func resetDatabase(ctx context.Context, root string) error {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// The container may still be starting when the suite is invoked.
	var pingErr error
	for i := 0; i < 60; i++ {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		pingErr = pool.Ping(pingCtx)
		cancel()
		if pingErr == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if pingErr != nil {
		return fmt.Errorf("database never became reachable: %w", pingErr)
	}

	const drop = `DROP TABLE IF EXISTS client_events, networks, clients, devices,
		sync_runs, audit_log, sessions, users, sites CASCADE`
	if _, err := pool.Exec(ctx, drop); err != nil {
		return fmt.Errorf("drop schema: %w", err)
	}

	sql, err := migrationsUp(root)
	if err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, sql); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

// migrationsUp concatenates the Up section of every migration in filename
// order, stopping each file at its `-- +goose Down` marker.
func migrationsUp(root string) (string, error) {
	files, err := filepath.Glob(filepath.Join(root, "migrations", "*.sql"))
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", fmt.Errorf("no migrations found under %s", filepath.Join(root, "migrations"))
	}
	sort.Strings(files)

	var out strings.Builder
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		text := string(raw)
		if i := strings.Index(text, "-- +goose Down"); i >= 0 {
			text = text[:i]
		}
		out.WriteString(text)
		out.WriteString("\n")
	}
	return out.String(), nil
}
