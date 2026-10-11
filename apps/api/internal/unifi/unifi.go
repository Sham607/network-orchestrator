// Package unifi is the only boundary between the orchestrator and a UniFi
// controller. No other package may know UniFi paths, headers, credentials or
// raw response shapes (docs/adr/003-architecture.md §2).
//
// This package contains the interface, the neutral domain types and the typed
// error taxonomy, plus the read-only HTTP adapter behind that interface: the
// Site Manager cloud transport (cloud.go) and the per-console Integration API
// transport reached through the Connector Proxy (console.go), sharing one
// bounded retry/timeout policy in client.go. Read-only in phase 1; write
// methods will be added as a separate Writer interface only once the API key
// scope is approved.
package unifi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Client is the read-only surface consumed by inventory.
type Client interface {
	// Health verifies the controller is reachable and the credential is valid.
	Health(ctx context.Context) error
	ListSites(ctx context.Context) ([]Site, error)
	ListDevices(ctx context.Context, siteID string) ([]Device, error)
	ListClients(ctx context.Context, siteID string) ([]ClientInfo, error)
	ListNetworks(ctx context.Context, siteID string) ([]Network, error)
}

// Domain types are neutral: they are not the raw UniFi response shape. Raw
// keeps the original payload for audit and schema-drift forensics only.
type Site struct {
	ID   string
	Name string
	Desc string
	// ConsoleID is the host (console) that owns the site: the Site Manager
	// hostId on the cloud transport, the configured console id on the console
	// transport. It is empty only when the surface does not expose it.
	ConsoleID string
	Raw       json.RawMessage
}

type Device struct {
	ID        string
	SiteID    string
	MAC       string
	Name      string
	Model     string
	Type      string
	Status    string
	Firmware  string
	IP        string
	UptimeSec int64
	AdoptedAt time.Time
	Raw       json.RawMessage
}

type ClientInfo struct {
	ID          string
	SiteID      string
	MAC         string
	Hostname    string
	IP          string
	Type        string // WIRELESS | WIRED
	AccessType  string
	APID        string
	SSID        string
	SignalDBm   int
	ConnectedAt time.Time
	Connected   bool
	Raw         json.RawMessage
}

type Network struct {
	ID           string
	SiteID       string
	Name         string
	VLAN         int
	Management   string
	Enabled      bool
	IsDefault    bool
	Origin       string
	Configurable bool
	Raw          json.RawMessage
}

// ErrKind is a stable error classification. The API layer maps it onto HTTP
// status codes and metrics label on it without parsing error strings.
type ErrKind string

const (
	ErrUnreachable  ErrKind = "unifi_unreachable"
	ErrUnauthorized ErrKind = "unifi_unauthorized"
	ErrForbidden    ErrKind = "unifi_forbidden"
	ErrRateLimited  ErrKind = "unifi_rate_limited"
	ErrNotFound     ErrKind = "unifi_not_found"
	ErrSchemaDrift  ErrKind = "unifi_schema_drift"
	ErrServer       ErrKind = "unifi_server_error"
)

// Error is the typed adapter error. Message is always sanitized: it must never
// carry an API key, a header value or a raw payload.
type Error struct {
	Kind       ErrKind
	HTTPStatus int
	RetryAfter time.Duration
	Message    string
	Cause      error
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Kind, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Message)
}

func (e *Error) Unwrap() error { return e.Cause }

// Is lets callers classify with errors.Is(err, &unifi.Error{Kind: ...}).
func (e *Error) Is(target error) bool {
	var t *Error
	if !errors.As(target, &t) {
		return false
	}
	return e.Kind == t.Kind
}

// Transient reports whether a retry could plausibly succeed. Credential and
// schema errors are never retried: retrying cannot heal them and only loads the
// controller (docs/adr/003-architecture.md §3, §7).
func (k ErrKind) Transient() bool {
	switch k {
	case ErrUnreachable, ErrRateLimited, ErrServer:
		return true
	default:
		return false
	}
}
