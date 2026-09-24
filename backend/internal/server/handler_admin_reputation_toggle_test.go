package server_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// Every provider's toggle has to be a real boolean, CIRCL's included (#168).
//
// The settings reader is GetBool, which answers false for anything that is not
// a JSON boolean. So a write of "yes" is stored, returns 204, and reads back
// as off: the operator is told their change landed and the provider is never
// asked. That is the "accepted and then ignored" pattern this handler refuses
// everywhere else — the scan policy, the refresh interval, the allowed types —
// and the keyless provider was the one hole in it, because the validator
// skipped any provider it could not demand a key for.
func TestSettings_RefusesANonBooleanProviderToggle(t *testing.T) {
	cases := []struct {
		provider   string
		enabledKey string
		wantNamed  string
	}{
		{"virustotal", admin.KeyAttachmentReputationVirusTotalEnabled, "VirusTotal"},
		{"metadefender", admin.KeyAttachmentReputationMetaDefenderEnabled, "MetaDefender"},
		{"polyswarm", admin.KeyAttachmentReputationPolySwarmEnabled, "PolySwarm"},
		// The one that was silently accepted.
		{"circl", admin.KeyAttachmentReputationCIRCLEnabled, "CIRCL"},
	}

	// Three shapes a hand-written PATCH actually arrives in. None of them is a
	// JSON boolean, so none of them may be stored.
	bad := []any{"yes", "true", 1}

	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			s := adminSession(t, h)
			ctx := context.Background()

			for _, v := range bad {
				t.Run(fmt.Sprintf("%v", v), func(t *testing.T) {
					res, body := s.send(t, http.MethodPatch, "/api/v1/admin/settings",
						map[string]any{tc.enabledKey: v})

					require.Equal(t, http.StatusBadRequest, res.StatusCode,
						"%v is not a boolean, so the write must be refused: %s", v, body)
					require.Contains(t, string(body), "invalid_reputation_config")
					require.Contains(t, string(body), tc.wantNamed,
						"the refusal has to name the provider whose toggle is wrong")

					require.False(t, h.adminSvc.ReputationEnabled(ctx, tc.provider),
						"a refused write must not land")
				})
			}
		})
	}
}

// And the toggle that IS a boolean still works, for the provider that needs no
// key at all.
//
// The rule being added is about the value's type. It must not turn into a
// second key requirement: CIRCL authenticates nobody, and demanding one would
// leave it permanently unusable.
func TestSettings_TheCIRCLToggleIsAcceptedAsABoolean(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	s := adminSession(t, h)
	ctx := context.Background()

	res, body := s.send(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{admin.KeyAttachmentReputationCIRCLEnabled: true})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "%s", body)
	require.True(t, h.adminSvc.ReputationEnabled(ctx, "circl"),
		"no key is needed, so nothing stands between the toggle and the setting")

	res, body = s.send(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{admin.KeyAttachmentReputationCIRCLEnabled: false})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "%s", body)
	require.False(t, h.adminSvc.ReputationEnabled(ctx, "circl"))
}
