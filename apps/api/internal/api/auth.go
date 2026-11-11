package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Sham607/network-orchestrator/apps/api/internal/audit"
	"github.com/Sham607/network-orchestrator/apps/api/internal/identity"
)

const (
	sessionCookieName = "session"
	csrfCookieName    = "csrf_token"
	csrfHeaderName    = "X-CSRF-Token"
	authTimeout       = 5 * time.Second
)

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type userResponse struct {
	ID    string        `json:"id"`
	Email string        `json:"email"`
	Role  identity.Role `json:"role"`
}

type loginResponse struct {
	User userResponse `json:"user"`
}

func clientIP(r *http.Request) string {
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		parts := strings.Split(xff, ",")
		if ip := strings.TrimSpace(parts[0]); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func isSecureCookie(deps Dependencies, r *http.Request) bool {
	if deps.Config.AppEnv == "production" {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") || r.TLS != nil
}

// sessionMiddleware resolves the operator session cookie when present and
// enforces authentication when deps.Config.AuthEnabled is true.
func sessionMiddleware(deps Dependencies) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			isPublic := path == "/api/v1/health" ||
				path == "/api/v1/ready" ||
				path == "/api/v1/auth/login"

			var sess identity.Session
			var hasSession bool

			if deps.Identity != nil {
				if c, err := r.Cookie(sessionCookieName); err == nil && strings.TrimSpace(c.Value) != "" {
					ctx, cancel := context.WithTimeout(r.Context(), authTimeout)
					validated, err := deps.Identity.ValidateSession(ctx, c.Value)
					cancel()
					if err == nil {
						sess = validated
						hasSession = true
						r = r.WithContext(identity.WithSession(r.Context(), sess))
					}
				}
			}

			if deps.Config.AuthEnabled && !isPublic && !hasSession {
				writeError(w, r, CodeUnauthorized, "authentication required", nil)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func handleLogin(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req loginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, r, CodeValidationFailed, "cannot parse JSON body", nil)
			return
		}

		fields := fieldErrors{}
		email := strings.ToLower(strings.TrimSpace(req.Email))
		if email == "" || !strings.Contains(email, "@") {
			fields["email"] = "must be a valid email address"
		}
		if len(req.Password) < 8 {
			fields["password"] = "must be at least 8 characters"
		}
		if fields.invalid() {
			writeValidationFailed(w, r, fields)
			return
		}

		if deps.Identity == nil {
			writeError(w, r, CodeServiceUnavailable, "identity service is not configured", map[string]any{
				"reason": "auth_unavailable",
			})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), authTimeout)
		defer cancel()

		ip := clientIP(r)
		rawToken, sess, err := deps.Identity.Authenticate(ctx, email, req.Password, ip, r.UserAgent())
		if err != nil {
			if errors.Is(err, identity.ErrInvalidCredentials) {
				if deps.Audit != nil {
					_ = deps.Audit.Record(ctx, audit.Entry{
						ActorType:  "user",
						ActorLabel: email,
						Action:     "auth.login.failed",
						TargetType: "session",
						RequestID:  requestIDFrom(r.Context()),
						IP:         ip,
						Detail:     map[string]any{"email": email},
					})
				}
				writeError(w, r, CodeUnauthorized, "invalid email or password", nil)
				return
			}
			writeError(w, r, CodeInternalError, "authentication failed", nil)
			return
		}

		secure := isSecureCookie(deps, r)
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    rawToken,
			Path:     "/",
			Expires:  sess.ExpiresAt,
			MaxAge:   int(time.Until(sess.ExpiresAt).Seconds()),
			HttpOnly: true,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
		})
		http.SetCookie(w, &http.Cookie{
			Name:     csrfCookieName,
			Value:    sess.CSRFToken,
			Path:     "/",
			Expires:  sess.ExpiresAt,
			MaxAge:   int(time.Until(sess.ExpiresAt).Seconds()),
			HttpOnly: false,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
		})

		if deps.Audit != nil {
			userID := sess.User.ID
			_ = deps.Audit.Record(ctx, audit.Entry{
				ActorUserID: &userID,
				ActorType:   "user",
				ActorLabel:  sess.User.Email,
				Action:      "auth.login",
				TargetType:  "session",
				TargetID:    sess.ID,
				RequestID:   requestIDFrom(r.Context()),
				IP:          ip,
				Detail: map[string]any{
					"role": string(sess.User.Role),
				},
			})
		}

		w.Header().Set(csrfHeaderName, sess.CSRFToken)
		writeJSON(w, http.StatusOK, loginResponse{
			User: userResponse{
				ID:    sess.User.ID,
				Email: sess.User.Email,
				Role:  sess.User.Role,
			},
		})
	}
}

func handleLogout(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), authTimeout)
		defer cancel()

		if c, err := r.Cookie(sessionCookieName); err == nil && strings.TrimSpace(c.Value) != "" && deps.Identity != nil {
			_ = deps.Identity.RevokeSession(ctx, c.Value)
		}

		if sess, ok := identity.SessionFromContext(r.Context()); ok && deps.Audit != nil {
			userID := sess.User.ID
			_ = deps.Audit.Record(ctx, audit.Entry{
				ActorUserID: &userID,
				ActorType:   "user",
				ActorLabel:  sess.User.Email,
				Action:      "auth.logout",
				TargetType:  "session",
				TargetID:    sess.ID,
				RequestID:   requestIDFrom(r.Context()),
				IP:          clientIP(r),
			})
		}

		secure := isSecureCookie(deps, r)
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
		})
		http.SetCookie(w, &http.Cookie{
			Name:     csrfCookieName,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: false,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
		})

		w.WriteHeader(http.StatusNoContent)
	}
}

// defaultLocalOperator is returned by GET /me when AUTH_ENABLED=false and no
// explicit session cookie is active, allowing the SPA to display a consistent
// operator badge in unauthenticated dev/CI environments.
var defaultLocalOperator = userResponse{
	ID:    "00000000-0000-4000-8000-000000000001",
	Email: "operator@local.noc",
	Role:  identity.RoleAdmin,
}

func handleMe(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if sess, ok := identity.SessionFromContext(r.Context()); ok {
			w.Header().Set(csrfHeaderName, sess.CSRFToken)
			writeJSON(w, http.StatusOK, userResponse{
				ID:    sess.User.ID,
				Email: sess.User.Email,
				Role:  sess.User.Role,
			})
			return
		}
		if !deps.Config.AuthEnabled {
			writeJSON(w, http.StatusOK, defaultLocalOperator)
			return
		}
		writeError(w, r, CodeUnauthorized, "authentication required", nil)
	}
}
