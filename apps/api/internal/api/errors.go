package api

import (
	"encoding/json"
	"net/http"
)

// Code is the stable, machine-readable error code. Clients branch on this,
// never on the HTTP status or message (docs/adr/003-architecture.md §5).
type Code string

const (
	CodeNotFound           Code = "not_found"
	CodeValidationFailed   Code = "validation_failed"
	CodeUnauthorized       Code = "unauthorized"
	CodeForbidden          Code = "forbidden"
	CodeRateLimited        Code = "rate_limited"
	CodeUnifiRateLimited   Code = "unifi_rate_limited"
	CodeUnifiUnreachable   Code = "unifi_unreachable"
	CodeUnifiSchemaDrift   Code = "unifi_schema_drift"
	CodeUnifiAuthFailed    Code = "unifi_unauthorized"
	CodeUnifiForbidden     Code = "unifi_forbidden"
	CodeServiceUnavailable Code = "service_unavailable"
	CodeInternalError      Code = "internal_error"
)

// ErrorBody is the single error shape returned by every endpoint.
type ErrorBody struct {
	Code    Code           `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// httpStatus maps a stable code onto its HTTP status (ADR-003 §5).
func httpStatus(code Code) int {
	switch code {
	case CodeValidationFailed:
		return http.StatusBadRequest
	case CodeUnauthorized:
		return http.StatusUnauthorized
	case CodeForbidden:
		return http.StatusForbidden
	case CodeNotFound:
		return http.StatusNotFound
	case CodeRateLimited, CodeUnifiRateLimited:
		return http.StatusTooManyRequests
	case CodeUnifiUnreachable, CodeUnifiSchemaDrift:
		return http.StatusServiceUnavailable
	case CodeServiceUnavailable:
		// A dependency the API itself needs is down (ADR-004). Distinct from
		// internal_error: expected and recoverable, not a bug in our code.
		return http.StatusServiceUnavailable
	case CodeUnifiAuthFailed, CodeUnifiForbidden:
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

// writeError renders the standard error body. Messages must already be
// sanitized by the caller: no internal paths, no credentials.
func writeError(w http.ResponseWriter, r *http.Request, code Code, message string, details map[string]any) {
	if details == nil {
		details = map[string]any{}
	}
	if requestID := requestIDFrom(r.Context()); requestID != "" {
		details["request_id"] = requestID
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(httpStatus(code))
	_ = json.NewEncoder(w).Encode(ErrorBody{Code: code, Message: message, Details: details})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
