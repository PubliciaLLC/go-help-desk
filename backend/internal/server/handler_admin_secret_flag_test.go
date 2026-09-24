package server_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// The settings dump says whether a secret is stored, without saying what it is.
//
// Dropping a secret from the response and saying nothing else leaves the
// administration UI unable to tell a configured key from an absent one — the
// key is missing either way — so the page can only tell the operator it does
// not know, which is no use to the person deciding whether to paste a new one.
//
// The flag reports presence and nothing more. It cannot be used to confirm a
// guess at the value, which is the thing echoing the secret would allow.
func TestSettings_ReportWhetherASecretIsSetWithoutReturningIt(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	s := adminSession(t, h)

	read := func(t *testing.T) map[string]json.RawMessage {
		t.Helper()
		res, body := s.send(t, http.MethodGet, "/api/v1/admin/settings", nil)
		require.Equal(t, http.StatusOK, res.StatusCode, "%s", body)
		var out map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(body, &out))
		return out
	}

	const flag = admin.KeyAttachmentReputationAPIKey + "_set"

	t.Run("unset reads false", func(t *testing.T) {
		got := read(t)
		require.JSONEq(t, "false", string(got[flag]))
		require.NotContains(t, got, admin.KeyAttachmentReputationAPIKey,
			"the key itself is never returned")
	})

	t.Run("a stored key reads true and is still not returned", func(t *testing.T) {
		res, body := s.send(t, http.MethodPatch, "/api/v1/admin/settings",
			map[string]any{admin.KeyAttachmentReputationAPIKey: "a-real-looking-key-0123456789"})
		require.Equal(t, http.StatusNoContent, res.StatusCode, "%s", body)

		got := read(t)
		require.JSONEq(t, "true", string(got[flag]))
		require.NotContains(t, got, admin.KeyAttachmentReputationAPIKey)
		require.NotContains(t, string(body), "a-real-looking-key",
			"no response may carry the key back")

		// And the whole dump, so a key cannot leak under some other name.
		raw, err := json.Marshal(got)
		require.NoError(t, err)
		require.NotContains(t, string(raw), "a-real-looking-key-0123456789")
	})

	t.Run("whitespace is not a configured key", func(t *testing.T) {
		res, _ := s.send(t, http.MethodPatch, "/api/v1/admin/settings",
			map[string]any{admin.KeyAttachmentReputationAPIKey: "   "})
		require.Equal(t, http.StatusNoContent, res.StatusCode)
		require.JSONEq(t, "false", string(read(t)[flag]),
			"an operator who pasted a stray space has not configured a key, and telling "+
				"them they have sends them looking for a fault somewhere else")
	})

	t.Run("clearing it reads false again", func(t *testing.T) {
		res, _ := s.send(t, http.MethodPatch, "/api/v1/admin/settings",
			map[string]any{admin.KeyAttachmentReputationAPIKey: ""})
		require.Equal(t, http.StatusNoContent, res.StatusCode)
		require.JSONEq(t, "false", string(read(t)[flag]))
	})

	// Every write-only key gets the same treatment, or the next secret added
	// is the one with no flag and the UI silently loses the ability again.
	t.Run("every secret carries a flag", func(t *testing.T) {
		got := read(t)
		for _, k := range []string{admin.KeyOIDCClientSecret, admin.KeySAMLKeyPEM} {
			require.Contains(t, got, k+"_set", "no flag for %s", k)
			require.NotContains(t, got, k)
		}
	})
}
