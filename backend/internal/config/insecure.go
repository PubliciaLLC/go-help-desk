package config

import "strings"

// Secrets that ship in docker/.env.example. An operator who copies that file
// and forgets to edit it gets a working instance whose signing keys are public
// knowledge — anyone can forge a session cookie or a bearer token for it.
//
// The length check on SESSION_SECRET (32 minimum, see auth.DeriveSessionKeys)
// does not catch this: the example value is 47 characters. It is long enough
// to start and entirely insecure, which is the worst combination — a secret
// that is too short at least refuses to boot.
var exampleSecrets = []string{
	"change-me-in-production-this-is-really-insecure",
	"change-me-in-production",
	"dev-session-secret-change-me-32c",
	"dev-jwt-secret-change-me",
}

// InsecureSecret reports whether a secret is one this project ships as an
// example, or is obviously still a placeholder.
//
// The substring check is deliberately broad. It will occasionally flag a
// genuine secret that happens to contain "change-me" or "insecure", and that
// is the right way to be wrong: a false warning costs an operator one glance,
// a missed one leaves a forgeable instance.
func InsecureSecret(secret string) bool {
	s := strings.ToLower(strings.TrimSpace(secret))
	if s == "" {
		return true
	}
	for _, example := range exampleSecrets {
		if s == strings.ToLower(example) {
			return true
		}
	}
	for _, marker := range []string{"change-me", "changeme", "insecure", "example", "placeholder"} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

// IsExampleSecret reports whether a secret is exactly one of the values this
// project publishes (ignoring case and surrounding space). Narrower than
// InsecureSecret on purpose: this decides that a value is thrown away, so it
// must not match a real secret that merely contains the word "example".
func IsExampleSecret(secret string) bool {
	s := strings.ToLower(strings.TrimSpace(secret))
	for _, example := range exampleSecrets {
		if s == strings.ToLower(example) {
			return true
		}
	}
	return false
}

// InsecureSecrets names the configured secrets that are examples or
// placeholders. Empty when everything is set properly.
//
// The names are returned, never the values: this feeds a startup warning and
// an admin-visible banner, and neither should print a signing key.
func (c *Config) InsecureSecrets() []string {
	var names []string
	if InsecureSecret(c.SessionSecret) {
		names = append(names, "SESSION_SECRET")
	}
	// JWT_SECRET: an exact published example is discarded at startup
	// (cmd/server resolveJWTSecret), and a value under 32 bytes is replaced by
	// a generated key, so neither is the key in use. What is left is a long
	// placeholder that is not an exact example: it wins over the generated key,
	// so it must be flagged.
	if len(c.JWTSecret) >= 32 && !IsExampleSecret(c.JWTSecret) && InsecureSecret(c.JWTSecret) {
		names = append(names, "JWT_SECRET")
	}
	return names
}
