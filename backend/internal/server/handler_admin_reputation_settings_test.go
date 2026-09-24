package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// A reputation provider nobody recognises is refused at the write, not
// silently swapped for the default on the read.
//
// This is the shape #168 was opened about: a setting accepted with a 204 and
// then ignored. The reader falls back to VirusTotal, so a typo is safe — and
// an operator who deliberately chose MetaDefender, saw a 204, and then found
// their staff clicking through to VirusTotal has been told nothing at all.
func TestSettings_RefusesAReputationProviderNobodyRecognises(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	s := adminSession(t, h)

	t.Run("refused, and the message names the choices", func(t *testing.T) {
		for _, bad := range []string{"virus-total", "VirusTotal", "hybridanalysis", "", " virustotal"} {
			res, body := s.send(t, http.MethodPatch, "/api/v1/admin/settings",
				map[string]any{admin.KeyAttachmentReputationProvider: bad})
			require.Equal(t, http.StatusBadRequest, res.StatusCode,
				"provider %q was accepted: %s", bad, body)
			require.Contains(t, string(body), "invalid_reputation_provider")
			require.Contains(t, string(body), "virustotal")
			require.Contains(t, string(body), "metadefender")
		}
	})

	// The companion that stops the fix being "refuse everything", which would
	// pass the half above on its own.
	t.Run("both real providers are accepted and take effect", func(t *testing.T) {
		for _, good := range []string{"metadefender", "virustotal"} {
			res, body := s.send(t, http.MethodPatch, "/api/v1/admin/settings",
				map[string]any{admin.KeyAttachmentReputationProvider: good})
			require.Equal(t, http.StatusNoContent, res.StatusCode, "provider %q: %s", good, body)
			require.Equal(t, good, h.adminSvc.ReputationProvider(context.Background()))
		}
	})

	// A bad value must take the rest of the write down with it, or an operator
	// changing two things gets one of them.
	t.Run("a bad provider rejects the whole write", func(t *testing.T) {
		before := h.adminSvc.ReputationProvider(context.Background())
		res, _ := s.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
			"site_name":                           "Changed By A Rejected Write",
			admin.KeyAttachmentReputationProvider: "nonsense",
		})
		require.Equal(t, http.StatusBadRequest, res.StatusCode)
		require.Equal(t, before, h.adminSvc.ReputationProvider(context.Background()))

		name, _ := h.adminSvc.GetString(context.Background(), admin.KeySiteName)
		require.NotEqual(t, "Changed By A Rejected Write", name,
			"the valid half of a refused write must not land")
	})
}
