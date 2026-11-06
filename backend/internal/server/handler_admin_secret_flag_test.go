package server_test

import (
	"context"
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

// The flag is read-only, and a client that PATCHes the dump back must be told
// so rather than quietly creating a settings row named after it.
//
// The dump emits a synthetic <key>_set boolean for every write-only secret.
// The write handler had no allowlist at all, so any client that read the dump,
// edited one field and sent the whole object back wrote real rows called
// oidc_client_secret_set, saml_key_pem_set and so on — rows nothing reads,
// sitting in the settings table looking like configuration. The frontend has
// been doing exactly that since the flags landed.
func TestSettings_RefuseAWriteToASecretPresenceFlag(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	s := adminSession(t, h)

	const flag = admin.KeyOIDCClientSecret + "_set"

	res, body := s.send(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{flag: true})
	require.Equal(t, http.StatusBadRequest, res.StatusCode, "%s", body)

	var errBody struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	require.NoError(t, json.Unmarshal(body, &errBody))
	require.Equal(t, "readonly_setting", errBody.Error.Code)
	require.Contains(t, errBody.Error.Message, flag,
		"the message has to name the key, or the operator cannot find it in what they sent")

	// Nothing was written under that name.
	all, err := h.adminSvc.ListAll(context.Background())
	require.NoError(t, err)
	require.NotContains(t, all, flag, "a refused key must not reach the settings table")

	// And it takes the whole write down with it, the way every other
	// validation failure here does. An operator changing three things and
	// accidentally including a flag must not get two of them.
	res, body = s.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		admin.KeySiteName: "Changed By A Rejected Write",
		flag:              true,
	})
	require.Equal(t, http.StatusBadRequest, res.StatusCode, "%s", body)

	name, _ := h.adminSvc.GetString(context.Background(), admin.KeySiteName)
	require.NotEqual(t, "Changed By A Rejected Write", name,
		"the valid half of a refused write must not land")
}
