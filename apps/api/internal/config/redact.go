package config

import (
	"net/url"
)

// redactSecret hides a secret entirely, revealing only whether it is set.
// Never return a prefix/suffix of a credential: API key formats are guessable.
func redactSecret(s string) string {
	if s == "" {
		return ""
	}
	return "***redacted***"
}

// redactURL keeps host/db for troubleshooting but drops the password.
func redactURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "***unparseable***"
	}
	if u.User != nil {
		if _, hasPassword := u.User.Password(); hasPassword {
			u.User = url.UserPassword(u.User.Username(), "***redacted***")
		}
	}
	return u.Redacted()
}
