// Package observability holds cross-cutting logging, metrics and health
// helpers. It depends on nothing else in the codebase so every module can
// import it (see docs/adr/003-architecture.md §6).
package observability

import (
	"log/slog"
	"os"
)

// NewLogger returns a structured JSON logger writing to stdout.
//
// Field discipline per ADR-003 §6: callers pass fixed keys (module, event,
// request_id, site_id, result, duration_ms). The logger itself never receives
// raw request/response payloads or headers, which is what keeps credentials
// out of logs.
func NewLogger(level slog.Level) *slog.Logger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	})
	return slog.New(handler)
}

// RedactHeaderValue masks a sensitive header value for diagnostic logging.
// It returns a fixed marker so no length or content of the secret leaks.
func RedactHeaderValue(string) string {
	return "***redacted***"
}
