package config_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/config"
)

func TestInsecureSecret(t *testing.T) {
	// The values that actually ship. If docker/.env.example changes and these
	// stop matching, this test is the thing that notices.
	shipped := []string{
		"change-me-in-production-this-is-really-insecure", // .env.example SESSION_SECRET
		"change-me-in-production",                         // .env.example JWT_SECRET
		"dev-session-secret-change-me-32c",                // CONTRIBUTING dev command
		"dev-jwt-secret-change-me",
	}
	for _, s := range shipped {
		require.True(t, config.InsecureSecret(s), "the shipped example %q must be flagged", s)
	}

	// Long enough to pass DeriveSessionKeys' 32-character minimum, so the
	// length check cannot be what protects against these.
	require.Greater(t, len("change-me-in-production-this-is-really-insecure"), 32)
}

func TestInsecureSecret_Placeholders(t *testing.T) {
	for _, s := range []string{
		"",
		"   ",
		"CHANGE-ME-IN-PRODUCTION", // case must not matter
		"my-example-secret-value-that-is-long",
		"placeholder-secret-do-not-ship-this-x",
		"totally-insecure-but-long-enough-value",
	} {
		require.True(t, config.InsecureSecret(s), "%q must be flagged", s)
	}
}

func TestInsecureSecret_AcceptsRealSecrets(t *testing.T) {
	// The shape `openssl rand -base64 32` produces, which is what the docs
	// tell operators to use.
	for _, s := range []string{
		"h8Kq2vZ9mNpR4tXwY6bC1dF3gJ5kL7nQ0sU2wA4eG6i=",
		"aGVsbG8td29ybGQtdGhpcy1pcy1yYW5kb20tZW5vdWdo",
	} {
		require.False(t, config.InsecureSecret(s), "%q is a real secret and must not be flagged", s)
	}
}

func TestConfig_InsecureSecrets_NamesNotValues(t *testing.T) {
	c := &config.Config{
		SessionSecret: "change-me-in-production-this-is-really-insecure",
		JWTSecret:     "h8Kq2vZ9mNpR4tXwY6bC1dF3gJ5kL7nQ0sU2wA4eG6i=",
	}
	got := c.InsecureSecrets()

	require.Equal(t, []string{"SESSION_SECRET"}, got,
		"only the offending secret is named, and only by name")
	require.NotContains(t, got, c.SessionSecret, "a secret's value must never be returned")
}

func TestConfig_InsecureSecrets_CleanConfig(t *testing.T) {
	c := &config.Config{
		SessionSecret: "h8Kq2vZ9mNpR4tXwY6bC1dF3gJ5kL7nQ0sU2wA4eG6i=",
		JWTSecret:     "aGVsbG8td29ybGQtdGhpcy1pcy1yYW5kb20tZW5vdWdo",
	}
	require.Empty(t, c.InsecureSecrets())
}
