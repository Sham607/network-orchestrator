package config

import (
	"strings"
	"testing"
	"time"
)

func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("expected DATABASE_URL error, got %v", err)
	}
}

func TestLoadDefaults(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL": "postgres://orchestrator:secret@localhost:5432/orchestrator?sslmode=disable",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want :8080", cfg.HTTPAddr)
	}
	if cfg.UnifiEnabled {
		t.Error("UnifiEnabled should default to false so CI runs without secrets")
	}
	if cfg.UnifiMode != UnifiModeCloud {
		t.Errorf("UnifiMode = %q, want cloud", cfg.UnifiMode)
	}
	if cfg.SyncInterval != 60*time.Second {
		t.Errorf("SyncInterval = %s, want 60s", cfg.SyncInterval)
	}
	if cfg.SyncStaleAfter != 180*time.Second {
		t.Errorf("SyncStaleAfter = %s, want 180s", cfg.SyncStaleAfter)
	}
}

func TestCloudModeRequiresKeyWhenEnabled(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL":  "postgres://u:p@localhost:5432/db",
		"UNIFI_ENABLED": "true",
		"UNIFI_MODE":    "cloud",
	})
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "UNIFI_SM_API_KEY") {
		t.Fatalf("expected UNIFI_SM_API_KEY error, got %v", err)
	}
}

func TestLocalModeRequiresURL(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL": "postgres://u:p@localhost:5432/db",
		"UNIFI_MODE":   "local",
	})
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "UNIFI_LOCAL_URL") {
		t.Fatalf("expected UNIFI_LOCAL_URL error, got %v", err)
	}
}

func TestStaleThresholdMustExceedInterval(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL":     "postgres://u:p@localhost:5432/db",
		"SYNC_INTERVAL":    "120s",
		"SYNC_STALE_AFTER": "60s",
	})
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "SYNC_STALE_AFTER") {
		t.Fatalf("expected staleness ordering error, got %v", err)
	}
}

func TestInvalidDatabaseScheme(t *testing.T) {
	setEnv(t, map[string]string{"DATABASE_URL": "mysql://u:p@localhost:3306/db"})
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "scheme") {
		t.Fatalf("expected scheme error, got %v", err)
	}
}

func TestRedactedNeverLeaksSecrets(t *testing.T) {
	cfg := Config{
		DatabaseURL:            "postgres://user:sup3rsecret@db:5432/orchestrator",
		UnifiSMAPIKey:          "sm-key-value",
		UnifiLocalAPIKey:       "local-key-value",
		AdminBootstrapPassword: "admin-password-value",
	}
	logged := cfg.Redacted()
	for key, value := range logged {
		for _, secret := range []string{"sup3rsecret", "sm-key-value", "local-key-value", "admin-password-value"} {
			if s, ok := value.(string); ok && strings.Contains(s, secret) {
				t.Fatalf("Redacted()[%q] leaked %q", key, secret)
			}
		}
	}
}

func TestLoadSyncSiteAllowlist(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL":        "postgres://u:p@localhost:5432/db",
		"SYNC_SITE_ALLOWLIST": "site-1, site-2 ,site-3",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cfg.SyncSiteAllowlist) != 3 {
		t.Fatalf("expected 3 allowlisted sites, got %d", len(cfg.SyncSiteAllowlist))
	}
	if cfg.SyncSiteAllowlist[0] != "site-1" || cfg.SyncSiteAllowlist[1] != "site-2" || cfg.SyncSiteAllowlist[2] != "site-3" {
		t.Errorf("SyncSiteAllowlist = %v", cfg.SyncSiteAllowlist)
	}
}

// ADR-005: the 30-day window is a setting with a safe default, so a deployment
// that configures nothing keeps 30 days rather than pruning everything or
// keeping forever.
func TestClientEventsRetentionDefaultsToThirtyDays(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL": "postgres://u:p@localhost:5432/db",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.ClientEventsRetention != 30*24*time.Hour {
		t.Errorf("ClientEventsRetention = %s, want 720h (30 days)", cfg.ClientEventsRetention)
	}
	if cfg.ClientEventsPruneInterval != time.Hour {
		t.Errorf("ClientEventsPruneInterval = %s, want 1h", cfg.ClientEventsPruneInterval)
	}
}

func TestClientEventsRetentionIsConfigurable(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL":                 "postgres://u:p@localhost:5432/db",
		"CLIENT_EVENTS_RETENTION":      "168h",
		"CLIENT_EVENTS_PRUNE_INTERVAL": "15m",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.ClientEventsRetention != 168*time.Hour {
		t.Errorf("ClientEventsRetention = %s, want 168h", cfg.ClientEventsRetention)
	}
	if cfg.ClientEventsPruneInterval != 15*time.Minute {
		t.Errorf("ClientEventsPruneInterval = %s, want 15m", cfg.ClientEventsPruneInterval)
	}
}

func TestClientEventsRetentionRejectsNonPositiveValues(t *testing.T) {
	for _, raw := range []string{"forever", "0s", "-1h"} {
		t.Run(raw, func(t *testing.T) {
			setEnv(t, map[string]string{
				"DATABASE_URL":            "postgres://u:p@localhost:5432/db",
				"CLIENT_EVENTS_RETENTION": raw,
			})
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), "CLIENT_EVENTS_RETENTION") {
				t.Fatalf("expected a CLIENT_EVENTS_RETENTION error for %q, got %v", raw, err)
			}
		})
	}
}
