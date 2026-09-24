package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// The fourth value of attachment_reputation_provider (#168).
//
// Same rule as every other setting here: a value that is accepted and then
// ignored is worse than one that is refused. "circl" has to be accepted AND
// take effect, or an operator who chose it has been told nothing at all.
func TestSettings_AcceptsCIRCL(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	s := adminSession(t, h)

	for _, good := range []string{"circl", "virustotal"} {
		res, body := s.send(t, http.MethodPatch, "/api/v1/admin/settings",
			map[string]any{admin.KeyAttachmentReputationProvider: good})
		require.Equal(t, http.StatusNoContent, res.StatusCode, "provider %q: %s", good, body)
		require.Equal(t, good, h.adminSvc.ReputationProvider(context.Background()),
			"provider %q was accepted and then ignored", good)
	}
}

// And the refusal names it, because a message listing three of four choices is
// how an operator concludes the fourth is not available.
func TestSettings_TheProviderRefusalNamesEveryChoice(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	s := adminSession(t, h)

	res, body := s.send(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{admin.KeyAttachmentReputationProvider: "hashlookup"})
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
	for _, choice := range []string{"virustotal", "metadefender", "polyswarm", "circl"} {
		require.Contains(t, string(body), choice)
	}
}
