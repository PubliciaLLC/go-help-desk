package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// Per-provider toggles over HTTP, through the real router and the real auth
// chain (#168).

// With no provider enabled a ticket carrying a quarantined attachment renders
// exactly as it does without the feature at all.
//
// This is the state every instance upgrades into, so it is the one that must
// not break, and "nothing configured" is the easiest thing to render as though
// something were wrong. Everything a staff member needs is still there — the
// scanner's detection name, the SHA-256, the hash link — because none of it
// came from a reputation provider. No warning, no banner, and specifically no
// "not checked yet": that phrase belongs to a lookup that was attempted and
// did not finish, and nothing was attempted.
func TestListAttachments_NoProviderEnabledRendersTheTicketNormally(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	rig := newVerdictRig(t, h, func(http.ResponseWriter, *http.Request) {
		t.Error("a lookup was attempted with every provider disabled")
	})
	// A key left over from a provider the operator has since switched off is
	// not an instruction to keep asking.
	require.NoError(t, h.adminSvc.SetRaw(ctx,
		admin.KeyAttachmentReputationVirusTotalKey, []byte(`"vt-key"`)))
	for _, p := range admin.ReputationProviders() {
		enabledKey, _, _ := admin.ReputationSettingKeys(p)
		require.NoError(t, h.adminSvc.SetRaw(ctx, enabledKey, []byte(`false`)))
	}

	tk := rig.quarantinedTicket(t, "invoice.exe.zip")

	got := rig.list(t, h.apiKey, tk)
	require.Len(t, got, 1)
	require.Equal(t, int64(0), rig.hits.Load())

	require.NotNil(t, got[0].VirusName, "the scanner's verdict stands on its own")
	require.Equal(t, "Eicar-Test-Signature", *got[0].VirusName)
	require.NotNil(t, got[0].SHA256)
	require.NotNil(t, got[0].ReputationURL, "a link is not a lookup")
	require.Nil(t, got[0].Reputation,
		"nothing was attempted, so the block is absent rather than a failure")
}

// Two providers enabled means two answers on the wire, each attributed, with
// the worst of them summarised for the row.
func TestListAttachments_TwoProvidersBothAnswer(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	rig := newVerdictRig(t, h, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case hasPrefix(r.URL.Path, "/api/v3/files/"):
			_, _ = w.Write([]byte(`{"data":{"attributes":{
				"last_analysis_stats":{"malicious":0,"suspicious":0,"undetected":70,"harmless":0,
					"timeout":0,"confirmed-timeout":0,"failure":0,"type-unsupported":0},
				"last_analysis_date":1758565200}}}`))
		default:
			_, _ = w.Write([]byte(`{"SHA-256":"x","db":"nsrl_modern_rds","source":"NSRL"}`))
		}
	})
	rig.setKey(t, "vt-key")
	require.NoError(t, h.adminSvc.SetRaw(ctx,
		admin.KeyAttachmentReputationCIRCLEnabled, []byte(`true`)))

	tk := rig.quarantinedTicket(t, "invoice.exe.zip")

	got := rig.list(t, h.apiKey, tk)
	require.Len(t, got, 1)
	require.NotNil(t, got[0].Reputation)
	require.Equal(t, int64(2), rig.hits.Load())

	require.Equal(t, "clean", got[0].Reputation.State, "clean outranks known")
	require.Equal(t, "virustotal", got[0].Reputation.ProviderKey)
	require.Len(t, got[0].Reputation.Providers, 2)
	require.Equal(t, "known", providerLine(t, got[0].Reputation, "circl").State)
}

// The re-check names the provider whose Check again control was clicked,
// because each verdict has its own expiry clock and its own control.
func TestRecheckReputation_NamesOneProvider(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	rig := newRecheckRig(t, h, repRespond200(vtDetectedRecheckBody))
	rig.setKey(t, "vt-key")

	tk, attID := rig.quarantined(t, "invoice.exe.zip")
	rig.seed(t, cleanRecheckVerdict(), 30*recheckDay)

	res, body := rig.recheckProvider(t, h.apiKey, tk, attID, "virustotal")
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", body)
	require.Equal(t, int64(1), rig.hits.Load())

	var att ticket.Attachment
	require.NoError(t, json.Unmarshal(body, &att))
	require.NotNil(t, att.Reputation)
	require.Equal(t, "detected", att.Reputation.State)
}

// A provider that is not enabled has no lookup to re-check, whether the name
// was misspelled or switched off since the page was rendered.
func TestRecheckReputation_AProviderThatIsNotEnabledIsRefused(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	rig := newRecheckRig(t, h, func(http.ResponseWriter, *http.Request) {
		t.Error("a disabled provider was asked")
	})
	rig.setKey(t, "vt-key") // VirusTotal only

	tk, attID := rig.quarantined(t, "invoice.exe.zip")
	rig.seed(t, cleanRecheckVerdict(), 30*recheckDay)

	for _, name := range []string{"circl", "hybridanalysis"} {
		res, body := rig.recheckProvider(t, h.apiKey, tk, attID, name)
		require.Equal(t, http.StatusBadRequest, res.StatusCode, "%s: %s", name, body)
		require.Contains(t, string(body), "invalid_provider")
	}
	require.Equal(t, int64(0), rig.hits.Load())
}

func hasPrefix(s, prefix string) bool { return len(s) >= len(prefix) && s[:len(prefix)] == prefix }

func providerLine(t *testing.T, rep *ticket.AttachmentReputation, key string) ticket.AttachmentProviderVerdict {
	t.Helper()
	for _, p := range rep.Providers {
		if p.ProviderKey == key {
			return p
		}
	}
	t.Fatalf("no entry for %q", key)
	return ticket.AttachmentProviderVerdict{}
}
