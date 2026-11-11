package identity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/argon2"
)

// Role defines the operator RBAC tier (ADR-002 §6).
type Role string

const (
	RoleViewer   Role = "viewer"
	RoleOperator Role = "operator"
	RoleAdmin    Role = "admin"
)

// CanOperate reports whether the role is permitted to run operational maintenance
// actions (e.g. AP restart or radio tuning).
func (r Role) CanOperate() bool {
	return r == RoleOperator || r == RoleAdmin
}

// IsAdmin reports whether the role has full administrative privileges.
func (r Role) IsAdmin() bool {
	return r == RoleAdmin
}

// Valid reports whether r is one of the three contract-declared roles.
func (r Role) Valid() bool {
	return r == RoleViewer || r == RoleOperator || r == RoleAdmin
}

// User is an operator account as exposed by the identity boundary.
type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Role  Role   `json:"role"`
}

// Session represents a validated server-side operator session.
type Session struct {
	ID        string
	User      User
	CSRFToken string
	ExpiresAt time.Time
	IP        string
}

var (
	// ErrInvalidCredentials is returned when email or password does not match
	// an active operator account.
	ErrInvalidCredentials = errors.New("identity: invalid email or password")
	// ErrSessionExpired is returned when a session token does not exist, was
	// revoked, or is past its expiry.
	ErrSessionExpired = errors.New("identity: session not found or expired")
)

type ctxKey struct{}

// WithSession attaches a validated operator session to ctx.
func WithSession(ctx context.Context, s Session) context.Context {
	return context.WithValue(ctx, ctxKey{}, s)
}

// SessionFromContext extracts the operator session from ctx, if present.
func SessionFromContext(ctx context.Context) (Session, bool) {
	s, ok := ctx.Value(ctxKey{}).(Session)
	return s, ok
}

// Service defines the operator identity and session operations consumed by the
// HTTP transport.
type Service interface {
	Authenticate(ctx context.Context, email, password, ip, userAgent string) (rawToken string, sess Session, err error)
	ValidateSession(ctx context.Context, rawToken string) (Session, error)
	RevokeSession(ctx context.Context, rawToken string) error
	EnsureBootstrapAdmin(ctx context.Context, email, password string) error
}

// Argon2id parameters following OWASP minimum recommendations.
const (
	argonTime    uint32 = 1
	argonMemory  uint32 = 64 * 1024 // 64 MiB
	argonThreads uint8  = 4
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

// HashPassword encodes a plaintext password using argon2id in standard PHC string format.
func HashPassword(password string) (string, error) {
	if len(password) < 8 {
		return "", errors.New("identity: password must be at least 8 characters")
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("identity: generate salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Hash := base64.RawStdEncoding.EncodeToString(hash)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64Salt, b64Hash), nil
}

// VerifyPassword checks password against an argon2id PHC string in constant time.
func VerifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var memory, timeCost uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &timeCost, &threads); err != nil {
		return false
	}
	if memory == 0 || timeCost == 0 || threads == 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return false
	}
	wantHash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(wantHash) == 0 {
		return false
	}
	gotHash := argon2.IDKey([]byte(password), salt, timeCost, memory, threads, uint32(len(wantHash)))
	return subtle.ConstantTimeCompare(gotHash, wantHash) == 1
}

// GenerateSessionToken produces a 32-byte cryptographically random URL-safe token
// and its SHA-256 digest (stored in sessions.token_hash).
func GenerateSessionToken() (rawToken string, tokenHash []byte, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", nil, fmt.Errorf("identity: generate token: %w", err)
	}
	rawToken = base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(rawToken))
	return rawToken, sum[:], nil
}

// HashToken computes the SHA-256 digest of a raw session cookie value.
func HashToken(rawToken string) []byte {
	sum := sha256.Sum256([]byte(rawToken))
	return sum[:]
}

// DeriveCSRFToken deterministically derives a per-session CSRF token from the
// server-side token hash so write requests can be verified in constant time
// without an extra database column.
func DeriveCSRFToken(tokenHash []byte, sessionID string) string {
	mac := hmac.New(sha256.New, tokenHash)
	_, _ = mac.Write([]byte("csrf:" + sessionID))
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyCSRF compares a provided CSRF header against the session's expected token.
func VerifyCSRF(expected, provided string) bool {
	if expected == "" || provided == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) == 1
}

// PostgresService implements Service backed by PostgreSQL `users` and `sessions`.
type PostgresService struct {
	pool       *pgxpool.Pool
	sessionTTL time.Duration
}

// NewPostgresService creates a Postgres-backed identity service.
func NewPostgresService(pool *pgxpool.Pool, sessionTTL time.Duration) *PostgresService {
	if sessionTTL <= 0 {
		sessionTTL = 24 * time.Hour
	}
	return &PostgresService{
		pool:       pool,
		sessionTTL: sessionTTL,
	}
}

const selectUserByEmailSQL = `
SELECT id::text, email, password_hash, role, is_active
FROM users
WHERE email = lower(trim($1))`

const insertSessionSQL = `
INSERT INTO sessions (user_id, token_hash, expires_at, last_seen_at, ip, user_agent)
VALUES ($1::uuid, $2, $3, now(), $4, NULLIF($5, ''))
RETURNING id::text`

const updateLastLoginSQL = `
UPDATE users
SET last_login_at = now(), updated_at = now()
WHERE id = $1::uuid`

// Authenticate verifies operator credentials and creates a new server-side session row.
func (s *PostgresService) Authenticate(ctx context.Context, email, password, ip, userAgent string) (string, Session, error) {
	cleanEmail := strings.ToLower(strings.TrimSpace(email))
	if cleanEmail == "" || password == "" {
		return "", Session{}, ErrInvalidCredentials
	}

	var (
		userID       string
		storedEmail  string
		passwordHash string
		roleStr      string
		isActive     bool
	)
	err := s.pool.QueryRow(ctx, selectUserByEmailSQL, cleanEmail).Scan(
		&userID, &storedEmail, &passwordHash, &roleStr, &isActive,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", Session{}, ErrInvalidCredentials
		}
		return "", Session{}, fmt.Errorf("identity: query user: %w", err)
	}
	if !isActive || !VerifyPassword(password, passwordHash) {
		return "", Session{}, ErrInvalidCredentials
	}

	rawToken, tokenHash, err := GenerateSessionToken()
	if err != nil {
		return "", Session{}, err
	}

	expiresAt := time.Now().UTC().Add(s.sessionTTL)
	var parsedIP any
	if addr, err := netip.ParseAddr(strings.TrimSpace(ip)); err == nil {
		parsedIP = addr.String()
	}

	var sessionID string
	if err := s.pool.QueryRow(ctx, insertSessionSQL,
		userID, tokenHash, expiresAt, parsedIP, strings.TrimSpace(userAgent),
	).Scan(&sessionID); err != nil {
		return "", Session{}, fmt.Errorf("identity: create session: %w", err)
	}

	_, _ = s.pool.Exec(ctx, updateLastLoginSQL, userID)

	sess := Session{
		ID: sessionID,
		User: User{
			ID:    userID,
			Email: storedEmail,
			Role:  Role(roleStr),
		},
		CSRFToken: DeriveCSRFToken(tokenHash, sessionID),
		ExpiresAt: expiresAt,
		IP:        strings.TrimSpace(ip),
	}
	return rawToken, sess, nil
}

const validateSessionSQL = `
SELECT s.id::text, s.expires_at, COALESCE(host(s.ip), ''),
       u.id::text, u.email, u.role, u.is_active
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1
  AND s.revoked_at IS NULL
  AND s.expires_at > now()`

const touchSessionSQL = `
UPDATE sessions
SET last_seen_at = now()
WHERE id = $1::uuid`

// ValidateSession looks up an active, non-expired session by its raw cookie token.
func (s *PostgresService) ValidateSession(ctx context.Context, rawToken string) (Session, error) {
	if strings.TrimSpace(rawToken) == "" {
		return Session{}, ErrSessionExpired
	}
	tokenHash := HashToken(rawToken)

	var (
		sessionID string
		expiresAt time.Time
		ip        string
		userID    string
		email     string
		roleStr   string
		isActive  bool
	)
	err := s.pool.QueryRow(ctx, validateSessionSQL, tokenHash).Scan(
		&sessionID, &expiresAt, &ip, &userID, &email, &roleStr, &isActive,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Session{}, ErrSessionExpired
		}
		return Session{}, fmt.Errorf("identity: validate session: %w", err)
	}
	if !isActive {
		return Session{}, ErrSessionExpired
	}

	_, _ = s.pool.Exec(ctx, touchSessionSQL, sessionID)

	return Session{
		ID: sessionID,
		User: User{
			ID:    userID,
			Email: email,
			Role:  Role(roleStr),
		},
		CSRFToken: DeriveCSRFToken(tokenHash, sessionID),
		ExpiresAt: expiresAt,
		IP:        ip,
	}, nil
}

const revokeSessionSQL = `
UPDATE sessions
SET revoked_at = now()
WHERE token_hash = $1 AND revoked_at IS NULL`

// RevokeSession marks the session identified by rawToken as revoked. Idempotent.
func (s *PostgresService) RevokeSession(ctx context.Context, rawToken string) error {
	if strings.TrimSpace(rawToken) == "" {
		return nil
	}
	tokenHash := HashToken(rawToken)
	if _, err := s.pool.Exec(ctx, revokeSessionSQL, tokenHash); err != nil {
		return fmt.Errorf("identity: revoke session: %w", err)
	}
	return nil
}

const upsertBootstrapAdminSQL = `
INSERT INTO users (email, password_hash, role, is_active)
VALUES (lower(trim($1)), $2, 'admin', true)
ON CONFLICT (email) DO UPDATE SET
    password_hash = EXCLUDED.password_hash,
    role          = 'admin',
    is_active     = true,
    updated_at    = now()`

// EnsureBootstrapAdmin seeds or refreshes the initial admin account when
// ADMIN_BOOTSTRAP_PASSWORD is provided at startup (ADR-002 §6).
func (s *PostgresService) EnsureBootstrapAdmin(ctx context.Context, email, password string) error {
	if strings.TrimSpace(password) == "" {
		return nil
	}
	cleanEmail := strings.ToLower(strings.TrimSpace(email))
	if cleanEmail == "" {
		cleanEmail = "admin@kantor.local"
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, upsertBootstrapAdminSQL, cleanEmail, hash); err != nil {
		return fmt.Errorf("identity: bootstrap admin: %w", err)
	}
	return nil
}
