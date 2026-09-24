package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// The per-provider reputation settings over the API (#168).
//
// Seven settings replaced two: a toggle per provider, and a key for the three
// commercial ones. The rule that makes the set coherent is that ENABLING A
// PROVIDER REQUIRES ITS KEY — which is the same rule as everywhere else in
// this handler, a setting accepted and then ignored being worse than a
// refusal, and which disposes of the old "enabled but silently doing nothing"
// state by making it unreachable.

// Turning a commercial provider on with no key is refused, and the message
// names which provider is missing one.
//
// A refusal that does not say which of three keys is missing sends an operator
// to check all three.
func TestSettings_RefusesEnablingAProviderWithNoKey(t *testing.T) {
	cases := []struct {
		provider   string
		enabledKey string
		wantNamed  string
	}{
		{"virustotal", admin.KeyAttachmentReputationVirusTotalEnabled, "VirusTotal"},
		{"metadefender", admin.KeyAttachmentReputationMetaDefenderEnabled, "MetaDefender"},
		{"polyswarm", admin.KeyAttachmentReputationPolySwarmEnabled, "PolySwarm"},
	}

	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			s := adminSession(t, h)

			res, body := s.send(t, http.MethodPatch, "/api/v1/admin/settings",
				map[string]any{tc.enabledKey: true})
			require.Equal(t, http.StatusBadRequest, res.StatusCode,
				"%s was enabled with no key: %s", tc.provider, body)
			require.Contains(t, string(body), "invalid_reputation_config")
			require.Contains(t, string(body), tc.wantNamed,
				"the refusal has to say which of three keys is missing")

			require.False(t, h.adminSvc.ReputationEnabled(context.Background(), tc.provider),
				"a refused write must not land")
		})
	}
}

// The key and the toggle in one request is the ordinary way an operator turns
// a provider on, and it has to work.
//
// A validator that looked only at what is already stored would refuse this,
// and one that looked only at the body would refuse enabling a provider whose
// key was pasted last week. It has to read the write laid over the settings.
func TestSettings_AKeyAndAToggleInOneWriteIsAccepted(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	s := adminSession(t, h)
	ctx := context.Background()

	res, body := s.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		admin.KeyAttachmentReputationVirusTotalKey:     "vt-key-0123456789",
		admin.KeyAttachmentReputationVirusTotalEnabled: true,
	})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "%s", body)
	require.True(t, h.adminSvc.ReputationEnabled(ctx, "virustotal"))
	require.Equal(t, "vt-key-0123456789", h.adminSvc.ReputationKey(ctx, "virustotal"))

	// And the toggle on its own, once the key is already stored.
	res, body = s.send(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{admin.KeyAttachmentReputationMetaDefenderKey: "md-key-0123456789"})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "%s", body)

	res, body = s.send(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{admin.KeyAttachmentReputationMetaDefenderEnabled: true})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "%s", body)
	require.Equal(t, []string{"virustotal", "metadefender"},
		h.adminSvc.EnabledReputationProviders(ctx))
}

// Clearing the key of a provider that is still enabled is the same invalid
// state arrived at from the other side, and is refused the same way.
func TestSettings_RefusesClearingTheKeyOfAnEnabledProvider(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	s := adminSession(t, h)
	ctx := context.Background()

	res, body := s.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		admin.KeyAttachmentReputationPolySwarmKey:     "ps-key-0123456789",
		admin.KeyAttachmentReputationPolySwarmEnabled: true,
	})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "%s", body)

	for _, blank := range []string{"", "   "} {
		res, body = s.send(t, http.MethodPatch, "/api/v1/admin/settings",
			map[string]any{admin.KeyAttachmentReputationPolySwarmKey: blank})
		require.Equal(t, http.StatusBadRequest, res.StatusCode, "key %q: %s", blank, body)
		require.Contains(t, string(body), "invalid_reputation_config")
	}

	require.Equal(t, "ps-key-0123456789", h.adminSvc.ReputationKey(ctx, "polyswarm"),
		"a refused write must not have cleared the key either")

	// Switching the provider off in the same write makes it legal again: the
	// invalid state is enabled-with-no-key, not an empty key.
	res, body = s.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		admin.KeyAttachmentReputationPolySwarmKey:     "",
		admin.KeyAttachmentReputationPolySwarmEnabled: false,
	})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "%s", body)
	require.False(t, h.adminSvc.ReputationEnabled(ctx, "polyswarm"))
}

// CIRCL needs no key, ever, so enabling it on its own is a complete
// configuration.
//
// It has no key setting at all: an empty box an operator feels obliged to fill
// is worse than no box, and a rule that demanded a key here would leave the
// one keyless provider permanently dead.
func TestSettings_CIRCLNeedsNoKey(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	s := adminSession(t, h)

	res, body := s.send(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{admin.KeyAttachmentReputationCIRCLEnabled: true})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "%s", body)
	require.True(t, h.adminSvc.ReputationEnabled(context.Background(), "circl"))
}

// Turning everything off is a supported configuration, not a broken one, and
// needs no key anywhere.
func TestSettings_EverythingOffIsAccepted(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	s := adminSession(t, h)

	body := map[string]any{}
	for _, p := range admin.ReputationProviders() {
		enabledKey, _, _ := admin.ReputationSettingKeys(p)
		body[enabledKey] = false
	}
	res, got := s.send(t, http.MethodPatch, "/api/v1/admin/settings", body)
	require.Equal(t, http.StatusNoContent, res.StatusCode, "%s", got)
	require.Empty(t, h.adminSvc.EnabledReputationProviders(context.Background()))
}

// A refused reputation write takes the rest of the request down with it, or an
// operator changing two things gets one of them.
func TestSettings_ABadReputationConfigRejectsTheWholeWrite(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	s := adminSession(t, h)
	ctx := context.Background()

	res, _ := s.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		"site_name": "Changed By A Rejected Write",
		admin.KeyAttachmentReputationVirusTotalEnabled: true,
	})
	require.Equal(t, http.StatusBadRequest, res.StatusCode)

	name, _ := h.adminSvc.GetString(ctx, admin.KeySiteName)
	require.NotEqual(t, "Changed By A Rejected Write", name,
		"the valid half of a refused write must not land")
}

// Every provider key is write-only: accepted on PATCH, never echoed by the
// settings dump. An admin session that can read one back can exfiltrate the
// operator's key to whatever the provider's terms attach to it.
func TestSettings_EveryProviderKeyIsWriteOnly(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	s := adminSession(t, h)

	const secret = "a-real-looking-key-0123456789"
	keys := []string{
		admin.KeyAttachmentReputationVirusTotalKey,
		admin.KeyAttachmentReputationMetaDefenderKey,
		admin.KeyAttachmentReputationPolySwarmKey,
	}

	for _, k := range keys {
		res, body := s.send(t, http.MethodPatch, "/api/v1/admin/settings",
			map[string]any{k: secret})
		require.Equal(t, http.StatusNoContent, res.StatusCode, "%s", body)
	}

	res, dump := s.send(t, http.MethodGet, "/api/v1/admin/settings", nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.NotContains(t, string(dump), secret, "a key echoed back is not write-only")
	for _, k := range keys {
		require.NotContains(t, string(dump), `"`+k+`"`)
		require.Contains(t, string(dump), `"`+k+`_set":true`,
			"the UI still has to be able to tell a configured key from an absent one")
	}
}

// None of the seven may be changed by a machine credential.
//
// A toggle decides where customers' file hashes go; a key decides whether they
// go anywhere. Neither is a decision a leaked API key may make, for the same
// reason it cannot repoint the identity provider.
func TestSettings_TheReputationSettingsNeedASession(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	for _, p := range admin.ReputationProviders() {
		enabledKey, apiKeyKey, ok := admin.ReputationSettingKeys(p)
		require.True(t, ok)

		res := h.do(t, http.MethodPatch, "/api/v1/admin/settings",
			map[string]any{enabledKey: true})
		res.Body.Close()
		require.Equal(t, http.StatusForbidden, res.StatusCode,
			"%s was writable with an API key", enabledKey)

		if apiKeyKey != "" {
			res = h.do(t, http.MethodPatch, "/api/v1/admin/settings",
				map[string]any{apiKeyKey: "stolen"})
			res.Body.Close()
			require.Equal(t, http.StatusForbidden, res.StatusCode,
				"%s was writable with an API key", apiKeyKey)
		}
	}
}
