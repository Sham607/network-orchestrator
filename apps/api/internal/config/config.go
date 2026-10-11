// Package config is the single place where environment configuration and
// runtime secrets are read. No other package may read os.Getenv for
// application settings (see docs/adr/003-architecture.md §4).
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"
)

// UnifiMode selects which UniFi transport the adapter will eventually use.
// "cloud" talks to the Site Manager API (read-only, reachable from anywhere);
// "local" talks to a console Integration API and only works from the office LAN
// or VPN (see ZENI-1 reachability findings).
type UnifiMode string

const (
	UnifiModeCloud UnifiMode = "cloud"
	UnifiModeLocal UnifiMode = "local"
)

// Config is the fully validated runtime configuration.
type Config struct {
	AppEnv string
	// HTTPAddr is the listen address of the API, e.g. ":8080".
	HTTPAddr string
	// DatabaseURL is the Postgres DSN. Required: the API will not start without it.
	DatabaseURL string
	LogLevel    slog.Level

	// UnifiEnabled gates the read-only UniFi adapter and the sync worker.
	// It defaults to false so a fresh checkout (and CI) can run the API with
	// no UniFi credentials present. Enabling it requires the credentials for
	// the selected mode to be present, or startup fails fast.
	UnifiEnabled bool
	UnifiMode    UnifiMode

	// UNIFI_SM_API_KEY authenticates against the Site Manager cloud API (read).
	UnifiSMAPIKey string
	// UNIFI_LOCAL_URL and UNIFI_LOCAL_API_KEY are the console Integration API
	// transport (write-capable; office LAN/VPN only).
	UnifiLocalURL    string
	UnifiLocalAPIKey string
	// UNIFI_CONSOLE_ID is optional and only used on the connector-proxy path.
	UnifiConsoleID string
	// UnifiCAFile optionally pins the controller CA instead of disabling TLS
	// verification. Plain paths are fine; the file itself never holds a key.
	UnifiCAFile string

	// SyncInterval is the server-side poll interval; SyncStaleAfter is the
	// readiness threshold for "is the cache fresh enough" (ADR-003 §6).
	SyncInterval   time.Duration
	SyncStaleAfter time.Duration

	// SyncSiteAllowlist optionally limits the sync worker to a subset of sites
	// (by unifi_site_id or site name). Comma-separated. When empty, all sites are synced.
	SyncSiteAllowlist []string

	// ClientEventsRetention is the ADR-005 history window for client_events:
	// rows older than this are deleted by the retention job.
	// ClientEventsPruneInterval is how often that job runs. Both are settings,
	// not constants scattered through the code.
	ClientEventsRetention     time.Duration
	ClientEventsPruneInterval time.Duration

	// AdminBootstrapPassword, when set, is used once to seed the first admin
	// on an empty database. It is a secret and must never be committed.
	AdminBootstrapPassword string
}

// Redacted returns a copy safe to log: every secret field is replaced.
func (c Config) Redacted() map[string]any {
	return map[string]any{
		"app_env":                      c.AppEnv,
		"http_addr":                    c.HTTPAddr,
		"log_level":                    c.LogLevel.String(),
		"unifi_enabled":                c.UnifiEnabled,
		"unifi_mode":                   string(c.UnifiMode),
		"sync_interval":                c.SyncInterval.String(),
		"stale_after":                  c.SyncStaleAfter.String(),
		"sync_site_allowlist":          c.SyncSiteAllowlist,
		"client_events_retention":      c.ClientEventsRetention.String(),
		"client_events_prune_interval": c.ClientEventsPruneInterval.String(),
		"database_url":                 redactURL(c.DatabaseURL),
		"sm_api_key":                   redactSecret(c.UnifiSMAPIKey),
		"local_api_key":                redactSecret(c.UnifiLocalAPIKey),
		"admin_password":               redactSecret(c.AdminBootstrapPassword),
	}
}

// Load reads configuration from the environment and validates it.
func Load() (Config, error) {
	cfg := Config{
		AppEnv:                 getenv("APP_ENV", "development"),
		HTTPAddr:               getenv("HTTP_ADDR", ":8080"),
		DatabaseURL:            os.Getenv("DATABASE_URL"),
		UnifiEnabled:           getenvBool("UNIFI_ENABLED", false),
		UnifiMode:              UnifiMode(getenv("UNIFI_MODE", string(UnifiModeCloud))),
		UnifiSMAPIKey:          os.Getenv("UNIFI_SM_API_KEY"),
		UnifiLocalURL:          os.Getenv("UNIFI_LOCAL_URL"),
		UnifiLocalAPIKey:       os.Getenv("UNIFI_LOCAL_API_KEY"),
		UnifiConsoleID:         os.Getenv("UNIFI_CONSOLE_ID"),
		UnifiCAFile:            os.Getenv("UNIFI_CA_FILE"),
		AdminBootstrapPassword: os.Getenv("ADMIN_BOOTSTRAP_PASSWORD"),
	}

	if rawAllowlist := os.Getenv("SYNC_SITE_ALLOWLIST"); strings.TrimSpace(rawAllowlist) != "" {
		for _, part := range strings.Split(rawAllowlist, ",") {
			if s := strings.TrimSpace(part); s != "" {
				cfg.SyncSiteAllowlist = append(cfg.SyncSiteAllowlist, s)
			}
		}
	}

	var errs []error

	level, err := parseLogLevel(getenv("LOG_LEVEL", "info"))
	if err != nil {
		errs = append(errs, err)
	}
	cfg.LogLevel = level

	if cfg.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	} else if _, err := pgxParse(cfg.DatabaseURL); err != nil {
		errs = append(errs, fmt.Errorf("DATABASE_URL is not a valid Postgres URL: %w", err))
	}

	if cfg.SyncInterval, err = parseDuration("SYNC_INTERVAL", "60s"); err != nil {
		errs = append(errs, err)
	}
	if cfg.SyncStaleAfter, err = parseDuration("SYNC_STALE_AFTER", "180s"); err != nil {
		errs = append(errs, err)
	}
	if cfg.SyncStaleAfter > 0 && cfg.SyncInterval > 0 && cfg.SyncStaleAfter <= cfg.SyncInterval {
		errs = append(errs, fmt.Errorf("SYNC_STALE_AFTER (%s) must be greater than SYNC_INTERVAL (%s)", cfg.SyncStaleAfter, cfg.SyncInterval))
	}

	// ADR-005: the window defaults to 30 days, so a deployment that configures
	// nothing keeps 30 days of history — rather than pruning everything away or
	// keeping it forever.
	if cfg.ClientEventsRetention, err = parseDuration("CLIENT_EVENTS_RETENTION", "720h"); err != nil {
		errs = append(errs, err)
	}
	if cfg.ClientEventsPruneInterval, err = parseDuration("CLIENT_EVENTS_PRUNE_INTERVAL", "1h"); err != nil {
		errs = append(errs, err)
	}

	switch cfg.UnifiMode {
	case UnifiModeCloud:
		if cfg.UnifiEnabled && cfg.UnifiSMAPIKey == "" {
			errs = append(errs, errors.New("UNIFI_MODE=cloud with UNIFI_ENABLED=true requires UNIFI_SM_API_KEY"))
		}
	case UnifiModeLocal:
		if cfg.UnifiLocalURL == "" {
			errs = append(errs, errors.New("UNIFI_MODE=local requires UNIFI_LOCAL_URL"))
		}
		if cfg.UnifiEnabled && cfg.UnifiLocalAPIKey == "" {
			errs = append(errs, errors.New("UNIFI_MODE=local with UNIFI_ENABLED=true requires UNIFI_LOCAL_API_KEY"))
		}
	default:
		errs = append(errs, fmt.Errorf("UNIFI_MODE must be %q or %q, got %q", UnifiModeCloud, UnifiModeLocal, cfg.UnifiMode))
	}

	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}
	return cfg, nil
}

func getenv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func getenvBool(key string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off", "":
		return fallback
	default:
		return fallback
	}
}

func parseDuration(key, fallback string) (time.Duration, error) {
	raw := getenv(key, fallback)
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a Go duration (e.g. 60s), got %q", key, raw)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s must be positive, got %s", key, d)
	}
	return d, nil
}

func parseLogLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("LOG_LEVEL must be one of debug|info|warn|error, got %q", raw)
	}
}

// pgxParse validates the DSN shape without importing pgx into config, keeping
// this package dependency-free and trivially unit-testable.
func pgxParse(dsn string) (*url.URL, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return nil, fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, errors.New("missing host")
	}
	return u, nil
}
