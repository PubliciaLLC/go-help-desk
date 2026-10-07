package audit_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
)

func TestRedact_ReplacesOnlyTheDenylistedKeys(t *testing.T) {
	before := map[string]any{"status": "open", "password_hash": "$2a$...", "priority": "low"}
	after := map[string]any{"status": "resolved", "api_key": "sk-live-...", "priority": "low"}

	gotBefore, gotAfter := audit.Redact(before, after)

	require.Equal(t, "open", gotBefore["status"])
	require.Equal(t, "low", gotBefore["priority"])
	require.Equal(t, "[redacted]", gotBefore["password_hash"])

	require.Equal(t, "resolved", gotAfter["status"])
	require.Equal(t, "[redacted]", gotAfter["api_key"])
}

func TestRedact_NilInNilOut(t *testing.T) {
	gotBefore, gotAfter := audit.Redact(nil, map[string]any{"status": "closed"})
	require.Nil(t, gotBefore, "a create/delete action's absent side must stay nil, not become an empty map")
	require.NotNil(t, gotAfter)
}

func TestRedact_DoesNotMutateTheCallersMap(t *testing.T) {
	before := map[string]any{"secret": "do-not-leak"}
	audit.Redact(before, nil)
	require.Equal(t, "do-not-leak", before["secret"], "Redact must return a copy, not edit the caller's map in place")
}

func TestRedact_EveryDenylistedNameIsActuallyCaught(t *testing.T) {
	names := []string{"password", "password_hash", "mfa_secret", "totp_secret", "secret", "api_key", "client_secret", "token", "hash"}
	m := make(map[string]any, len(names))
	for _, n := range names {
		m[n] = "sensitive-value"
	}

	got, _ := audit.Redact(m, nil)

	for _, n := range names {
		require.Equal(t, "[redacted]", got[n], "field %q must be redacted", n)
	}
}

// #329: a guard that only knows exact lower-case spellings is one a future
// writer passes by naming a field apiKey instead of api_key.
func TestRedact_MatchesRegardlessOfCaseAndSeparators(t *testing.T) {
	cases := []string{
		"passwordHash", "PasswordHash", "PASSWORD_HASH", "password-hash", "user.password",
		"apiKey", "ApiKey", "api-key", "API_KEY",
		"access_token", "refresh_token", "accessToken",
		"private_key", "privateKey", "key_pem", "keyPem",
		"recovery_codes", "recoveryCodes", "backup_codes", "BackupCodes",
		"clientSecret", "mfaSecret", "totp_secret",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			got, _ := audit.Redact(map[string]any{name: "sensitive-value"}, nil)
			require.Equal(t, "[redacted]", got[name])
		})
	}
}

func TestRedact_LeavesOrdinaryFieldsAlone(t *testing.T) {
	m := map[string]any{"id": "1", "status_id": "2", "priority": "low", "subject": "x", "os_user": "bob", "host": "h", "assignee_id": "u"}
	got, _ := audit.Redact(m, nil)
	require.Equal(t, m, got)
}

func TestRedact_WalksNestedMapsAndSlices(t *testing.T) {
	before := map[string]any{
		"config": map[string]any{
			"name":  "smtp",
			"creds": map[string]any{"accessToken": "t0"},
			"list":  []any{map[string]any{"client_secret": "s0", "ok": "fine"}, "plain"},
		},
		"token": map[string]any{"nested": "whole subtree hidden"},
	}

	got, _ := audit.Redact(before, nil)

	cfg := got["config"].(map[string]any)
	require.Equal(t, "smtp", cfg["name"])
	require.Equal(t, map[string]any{"accessToken": "[redacted]"}, cfg["creds"])
	item := cfg["list"].([]any)[0].(map[string]any)
	require.Equal(t, "[redacted]", item["client_secret"])
	require.Equal(t, "fine", item["ok"])
	require.Equal(t, "plain", cfg["list"].([]any)[1])
	require.Equal(t, "[redacted]", got["token"], "a sensitive key hides its whole value, whatever shape it has")

	// The caller's nested structure is untouched.
	orig := before["config"].(map[string]any)["creds"].(map[string]any)
	require.Equal(t, "t0", orig["accessToken"])
}
