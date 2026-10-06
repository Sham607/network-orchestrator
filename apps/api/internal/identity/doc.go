// Package identity owns operator accounts, sessions and RBAC
// (viewer/operator/admin). Passwords are hashed with argon2id; sessions are
// server-side rows referenced by an HttpOnly cookie.
//
// Implementation lands with the API/contract work (ZENI-6).
package identity
