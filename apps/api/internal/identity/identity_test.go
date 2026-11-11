package identity

import (
	"context"
	"testing"
)

func TestHashAndVerifyPassword(t *testing.T) {
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("expected error for password shorter than 8 characters")
	}

	pw := "correct-horse-battery-staple"
	hash, err := HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !VerifyPassword(pw, hash) {
		t.Fatal("VerifyPassword returned false for matching password")
	}
	if VerifyPassword("wrong-password-123", hash) {
		t.Fatal("VerifyPassword returned true for wrong password")
	}
	if VerifyPassword(pw, "invalid$format") {
		t.Fatal("VerifyPassword returned true for malformed PHC string")
	}
}

func TestSessionTokenAndCSRF(t *testing.T) {
	raw, hash, err := GenerateSessionToken()
	if err != nil {
		t.Fatalf("GenerateSessionToken: %v", err)
	}
	if raw == "" || len(hash) != 32 {
		t.Fatalf("unexpected token or hash length: raw=%q len(hash)=%d", raw, len(hash))
	}

	csrf := DeriveCSRFToken(hash, "session-123")
	if !VerifyCSRF(csrf, csrf) {
		t.Fatal("VerifyCSRF failed for matching token")
	}
	if VerifyCSRF(csrf, "tampered") {
		t.Fatal("VerifyCSRF accepted tampered token")
	}
	if VerifyCSRF("", "") {
		t.Fatal("VerifyCSRF accepted empty tokens")
	}
}

func TestRolePermissions(t *testing.T) {
	if RoleViewer.CanOperate() || RoleViewer.IsAdmin() {
		t.Fatal("viewer must not have operator or admin permissions")
	}
	if !RoleOperator.CanOperate() || RoleOperator.IsAdmin() {
		t.Fatal("operator must have operate permission but not admin")
	}
	if !RoleAdmin.CanOperate() || !RoleAdmin.IsAdmin() {
		t.Fatal("admin must have both operate and admin permissions")
	}
}

func TestSessionContextRoundTrip(t *testing.T) {
	ctx := context.Background()
	if _, ok := SessionFromContext(ctx); ok {
		t.Fatal("expected no session on empty context")
	}
	want := Session{
		ID:   "sess-1",
		User: User{ID: "u-1", Email: "budi@kantor.com", Role: RoleOperator},
	}
	ctx = WithSession(ctx, want)
	got, ok := SessionFromContext(ctx)
	if !ok || got.ID != want.ID || got.User.Email != want.User.Email {
		t.Fatalf("SessionFromContext = (%+v, %v), want %+v", got, ok, want)
	}
}
