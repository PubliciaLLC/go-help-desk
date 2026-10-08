package audit_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
)

// Secrets are hidden and the fields the view may show are shown. Uses the
// keys real writers use (status_id, not "status": since #362 the view is an
// allow-list, and a key nobody writes is not on it).
func TestRedact_HidesSecretsAndShowsAllowListedFields(t *testing.T) {
	before := map[string]any{"status_id": "open", "password_hash": "$2a$...", "priority": "low"}
	after := map[string]any{"status_id": "resolved", "api_key": "sk-live-...", "priority": "low"}

	gotBefore, gotAfter := audit.Redact(before, after)

	require.Equal(t, "open", gotBefore["status_id"])
	require.Equal(t, "low", gotBefore["priority"])
	require.Equal(t, "[redacted]", gotBefore["password_hash"])

	require.Equal(t, "resolved", gotAfter["status_id"])
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
		"smtp_pass", "attachment_reputation_virustotal_key",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			got, _ := audit.Redact(map[string]any{name: "sensitive-value"}, nil)
			require.Equal(t, "[redacted]", got[name])
		})
	}
}

// The keys real writers put in Before/After that are safe to show: ids,
// statuses, flags and counts from ticketMap, UnassignForUser, ResetMFA and
// cmd/server/resetfactors.go. They are shown as written.
func TestRedact_ShowsTheAllowListedFields(t *testing.T) {
	m := map[string]any{
		"id": "1", "status_id": "2", "priority": "low",
		"assignee_user_id": "u", "assignee_group_id": "g", "follow_up_of": "t",
		"forced_reopen": true, "closed_reopen_policy": "staff_only",
		"os_user": "bob", "host": "h", "source": "cli",
		"passkeys_removed": 2, "totp_cleared": true, "sessions_revoked": true,
	}
	got, _ := audit.Redact(m, nil)
	require.Equal(t, m, got)
}

// #362, Erik: protected or sensitive data — PII, FERPA, HIPAA, SOX, PCI-DSS —
// is never visible in the audit view, to anyone. The ticket subject is free
// text a requester typed and can hold any of those, so it is not shown. Nor
// is any field nobody has decided is safe: the view is an allow-list, so a
// writer that adds a field later shows "[redacted]" until it is added here on
// purpose. This replaces the old expectation that the subject passed through
// and that unknown fields (nested ones included) were shown.
func TestRedact_FreeTextAndUndecidedFieldsAreNeverShown(t *testing.T) {
	before := map[string]any{
		"subject":      "Student 4471's IEP accommodations, DOB 2009-03-14",
		"description":  "card 4111 1111 1111 1111",
		"guest_email":  "parent@example.com",
		"display_name": "Ada Lovelace",
		"new_field":    "anything",
		"config":       map[string]any{"name": "smtp", "ok": "fine"},
		"status_id":    "2",
	}
	got, _ := audit.Redact(before, nil)
	for _, k := range []string{"subject", "description", "guest_email", "display_name", "new_field", "config"} {
		require.Equal(t, "[redacted]", got[k], "%q was shown", k)
	}
	require.Equal(t, "2", got["status_id"])
}

// An allow-listed name holding a nested value is still redacted: the allow
// list vouches for a scalar, not for whatever a writer nests under it.
func TestRedact_AnAllowListedKeyWithANestedValueIsRedacted(t *testing.T) {
	got, _ := audit.Redact(map[string]any{"priority": map[string]any{"note": "free text"}}, nil)
	require.Equal(t, "[redacted]", got["priority"])
}
