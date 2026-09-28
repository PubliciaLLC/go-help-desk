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
