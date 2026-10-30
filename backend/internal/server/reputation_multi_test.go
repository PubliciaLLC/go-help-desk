package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
)

// Per-provider toggles: every enabled provider is asked, every answer is kept,
// and the row shows the worst of them (#168).
//
// The single-provider shape these replaced could not express the thing
// operators wanted. The four services answer different questions — VirusTotal
// counts engines, CIRCL says whether a catalogue has the file on record — and
// a sample one calls "detected" while another calls "known" is telling staff
// something either alone would hide.

// repByPath answers as whichever provider asked, so one httptest server can
// stand in for several at once.
//
// Keyed on the path prefix rather than on a counter, because the assertions
// below are about WHICH provider said what, and a responder that answered in
// call order would pass them by accident whenever the loop order happened to
// match.
func repByPath(t *testing.T, bodies map[string]repAnswer) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		for prefix, ans := range bodies {
			if strings.HasPrefix(r.URL.Path, prefix) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(ans.status)
				_, _ = w.Write([]byte(ans.body))
				return
			}
		}
		t.Errorf("no canned answer for %s", r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}
}

type repAnswer struct {
	status int
	body   string
}

const (
	vtPath    = "/api/v3/files/"
	mdPath    = "/v4/hash/"
	circlPath = "/lookup/sha256/"
)

func vtAnswer(body string) repAnswer    { return repAnswer{http.StatusOK, body} }
func circlAnswer(body string) repAnswer { return repAnswer{http.StatusOK, body} }

// providerEntry finds one provider's line in the expanded view.
func providerEntry(t *testing.T, rep *ticket.AttachmentReputation, key string) ticket.AttachmentProviderVerdict {
	t.Helper()
	require.NotNil(t, rep)
	for _, p := range rep.Providers {
		if p.ProviderKey == key {
			return p
		}
	}
	t.Fatalf("no entry for %q; got %d entries", key, len(rep.Providers))
	return ticket.AttachmentProviderVerdict{}
}

// Every enabled provider is asked, and every answer is kept under its own
// provider in the cache.
//
// One row per provider per file is what the composite key was always for. Two
// providers sharing a row would mean the second lookup overwrote the first,
// and the row would then be attributed to whichever service asked last.
func TestAddReputation_AsksEveryEnabledProvider(t *testing.T) {
	rig := newRepRig(t, repByPath(t, map[string]repAnswer{
		vtPath:    vtAnswer(vtCleanBody),
		circlPath: circlAnswer(clKnownRecord),
	}))
	rig.setKey(t, repTestKey)
	rig.enable(t, reputation.ProviderVirusTotal, reputation.ProviderCIRCL)

	att := quarantinedAttachment(repTestHash)
	rig.srv.addReputation(context.Background(), &att)

	require.Equal(t, int64(2), rig.hits.Load(), "both enabled providers are asked")
	require.NotNil(t, att.Reputation)
	require.Len(t, att.Reputation.Providers, 2,
		"the wire carries a list, not the one answer somebody picked")

	require.Equal(t, "clean", providerEntry(t, att.Reputation, "virustotal").State)
	require.Equal(t, "known", providerEntry(t, att.Reputation, "circl").State)

	stored := rig.store.all()
	require.Contains(t, stored, "virustotal:"+repTestHash)
	require.Contains(t, stored, "circl:"+repTestHash)

	// And the second render costs nothing: each provider's row has its own
	// expiry clock, and neither has passed it.
	again := quarantinedAttachment(repTestHash)
	rig.srv.addReputation(context.Background(), &again)
	require.Equal(t, int64(2), rig.hits.Load())
	require.Len(t, again.Reputation.Providers, 2)
}

// The row shows the worst single answer, and the ordering is the one staff
// read before they expand anything.
func TestAddReputation_ShowsTheWorstVerdictInline(t *testing.T) {
	cases := []struct {
		name         string
		vt           repAnswer
		circl        repAnswer
		wantState    string
		wantProvider string
	}{
		{
			name:         "a detection beats a catalogue entry",
			vt:           vtAnswer(vtDetectedBody),
			circl:        circlAnswer(clKnownRecord),
			wantState:    "detected",
			wantProvider: "virustotal",
		},
		{
			// The one worth reading twice. Every hash here is one the local
			// scanner already called malicious, so a file no service has ever
			// seen is a novel sample — more concerning than one seventy
			// engines examined and passed.
			name:         "unseen beats clean",
			vt:           vtAnswer(vtCleanBody),
			circl:        repAnswer{http.StatusNotFound, `{"message":"Non existing SHA-256"}`},
			wantState:    "unseen",
			wantProvider: "circl",
		},
		{
			name:         "clean beats known",
			vt:           vtAnswer(vtCleanBody),
			circl:        circlAnswer(clKnownRecord),
			wantState:    "clean",
			wantProvider: "virustotal",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newRepRig(t, repByPath(t, map[string]repAnswer{
				vtPath: tc.vt, circlPath: tc.circl,
			}))
			rig.setKey(t, repTestKey)
			rig.enable(t, reputation.ProviderVirusTotal, reputation.ProviderCIRCL)

			att := quarantinedAttachment(repTestHash)
			rig.srv.addReputation(context.Background(), &att)

			require.NotNil(t, att.Reputation)
			require.Equal(t, tc.wantState, att.Reputation.State)
			require.Equal(t, tc.wantProvider, att.Reputation.ProviderKey,
				"a summary that names no source is a claim from nowhere")
			require.Equal(t, reputation.DisplayName(tc.wantProvider), att.Reputation.Provider)

			// And exactly one line in the expanded view is marked as the one
			// the summary came from.
			var inline []string
			for _, p := range att.Reputation.Providers {
				if p.Inline {
					inline = append(inline, p.ProviderKey)
				}
			}
			require.Equal(t, []string{tc.wantProvider}, inline)
		})
	}
}

// A failed lookup never displaces a real answer.
//
// It is not a verdict, it is a failed lookup. VirusTotal timing out while
// CIRCL says "known" shows "known" — including when "known" is the least
// serious verdict there is, which is the case that makes this a rule rather
// than an accident of the ranking.
func TestAddReputation_AFailedLookupDoesNotDisplaceARealAnswer(t *testing.T) {
	rig := newRepRig(t, repByPath(t, map[string]repAnswer{
		vtPath:    {http.StatusInternalServerError, `{}`},
		circlPath: circlAnswer(clKnownRecord),
	}))
	rig.setKey(t, repTestKey)
	rig.enable(t, reputation.ProviderVirusTotal, reputation.ProviderCIRCL)

	att := quarantinedAttachment(repTestHash)
	rig.srv.addReputation(context.Background(), &att)

	require.NotNil(t, att.Reputation)
	require.Equal(t, "known", att.Reputation.State,
		"a provider that could not be asked must not hide one that answered")
	require.Equal(t, "circl", att.Reputation.ProviderKey)

	// And the failure is still listed, because an operator has to be able to
	// see their key failing.
	require.Equal(t, "unavailable", providerEntry(t, att.Reputation, "virustotal").State)
	require.NotContains(t, rig.store.all(), "virustotal:"+repTestHash,
		"a transient failure must not be cached")
}

// When nothing answered, "unavailable" is the honest summary — and the block
// is still there, because the operator needs to see four providers failing.
func TestAddReputation_EveryProviderFailingReadsAsUnavailable(t *testing.T) {
	rig := newRepRig(t, repByPath(t, map[string]repAnswer{
		vtPath:    {http.StatusInternalServerError, `{}`},
		circlPath: {http.StatusInternalServerError, `{}`},
	}))
	rig.setKey(t, repTestKey)
	rig.enable(t, reputation.ProviderVirusTotal, reputation.ProviderCIRCL)

	att := quarantinedAttachment(repTestHash)
	rig.srv.addReputation(context.Background(), &att)

	require.NotNil(t, att.Reputation,
		"two providers failing is something an operator has to be able to see")
	require.Equal(t, "unavailable", att.Reputation.State)
	require.Empty(t, att.Reputation.ProviderKey, "nobody answered, so the summary belongs to nobody")
	require.Len(t, att.Reputation.Providers, 2)
	for _, p := range att.Reputation.Providers {
		require.Equal(t, "unavailable", p.State)
		require.False(t, p.Inline)
		require.False(t, p.Recheckable, "there is nothing cached to re-check")
	}
	require.Empty(t, rig.store.all())
}

// Every toggle off is a supported configuration and not a broken one.
//
// It means this instance judges attachments by its own scanner alone, which is
// a complete answer: the detection name, the archive, the SHA-256 and the loud
// treatment all come from ClamAV. No warning, no banner, and specifically no
// "not checked yet" — that phrase belongs to a lookup that was attempted and
// did not finish, and nothing was attempted here.
func TestAddReputation_NothingEnabledIsNotAFailure(t *testing.T) {
	rig := newRepRig(t, func(http.ResponseWriter, *http.Request) {
		t.Error("a lookup was attempted with every provider disabled")
	})
	// A key left over from a provider the operator has since switched off is
	// not an instruction to keep asking.
	rig.setKey(t, repTestKey)
	rig.enable(t)

	att := quarantinedAttachment(repTestHash)
	rig.srv.addReputation(context.Background(), &att)

	require.Equal(t, int64(0), rig.hits.Load())
	require.Nil(t, att.Reputation,
		"nothing was attempted, so the block is absent rather than empty or failed")
	require.Empty(t, rig.store.all())

	// And the payload simply has no reputation key value, which is what the UI
	// renders as "the scanner's verdict stands on its own".
	body, err := json.Marshal(att)
	require.NoError(t, err)
	var out struct {
		Reputation *ticket.AttachmentReputation `json:"reputation"`
	}
	require.NoError(t, json.Unmarshal(body, &out))
	require.Nil(t, out.Reputation)
}

// An exhausted allowance at one provider must not stop another answering.
//
// The budgets are per provider because the allowances are, and this is the
// case that says so with two of them at once: VirusTotal's four-a-minute
// bucket is spent, CIRCL's three hundred an hour is untouched, and the row
// shows CIRCL's answer rather than nothing.
func TestAddReputation_AnExhaustedBudgetDoesNotStopAnotherProvider(t *testing.T) {
	rig := newRepRig(t, repByPath(t, map[string]repAnswer{
		vtPath:    vtAnswer(vtCleanBody),
		circlPath: circlAnswer(clKnownRecord),
	}))
	rig.setKey(t, repTestKey)
	rig.enable(t, reputation.ProviderVirusTotal, reputation.ProviderCIRCL)

	fixed := time.Date(2026, 9, 23, 11, 30, 30, 0, time.UTC)
	rig.srv.repBudget.Now = func() time.Time { return fixed }
	for i := 0; i < 4; i++ {
		require.NoError(t, rig.srv.repBudget.Spend(reputation.ProviderVirusTotal))
	}

	att := quarantinedAttachment(repTestHash)
	rig.srv.addReputation(context.Background(), &att)

	require.Equal(t, int64(1), rig.hits.Load(), "only CIRCL had allowance left")
	require.NotNil(t, att.Reputation)
	require.Equal(t, "known", att.Reputation.State)
	require.Equal(t, "unavailable", providerEntry(t, att.Reputation, "virustotal").State)
	require.Equal(t, "known", providerEntry(t, att.Reputation, "circl").State)
}

// Each provider carries its own clocks and its own Check again eligibility,
// because each verdict is a separate statement with a separate expiry.
//
// A shared "last checked" across providers would be wrong for all but one of
// them, and a Check again control armed from it would be refused on click.
func TestAddReputation_EachProviderCarriesItsOwnTimestamps(t *testing.T) {
	rig := newRepRig(t, repByPath(t, map[string]repAnswer{
		vtPath:    vtAnswer(vtCleanBody),
		circlPath: circlAnswer(clKnownRecord),
	}))
	rig.setKey(t, repTestKey)
	rig.enable(t, reputation.ProviderVirusTotal, reputation.ProviderCIRCL)
	require.NoError(t, rig.admin.SetRaw(context.Background(),
		admin.KeyAttachmentReputationRefresh, []byte(`"never"`)))

	// A clean VirusTotal verdict from a month ago, and nothing for CIRCL.
	old := time.Now().Add(-30 * 24 * time.Hour)
	require.NoError(t, rig.store.Put(context.Background(), repTestHash,
		reputation.ProviderVirusTotal, reputation.Reputation{
			State: reputation.Clean, Detected: 0, Total: 70, FetchedAt: old,
		}))

	att := quarantinedAttachment(repTestHash)
	rig.srv.addReputation(context.Background(), &att)

	require.Equal(t, int64(1), rig.hits.Load(), "only the provider with no verdict is asked")

	vt := providerEntry(t, att.Reputation, "virustotal")
	require.NotNil(t, vt.FetchedAt)
	require.WithinDuration(t, old, *vt.FetchedAt, time.Minute)
	require.True(t, vt.Recheckable, "a month-old clean verdict can still change")

	circl := providerEntry(t, att.Reputation, "circl")
	require.NotNil(t, circl.FetchedAt)
	require.WithinDuration(t, time.Now(), *circl.FetchedAt, time.Minute,
		"a verdict fetched by this very call was fetched now")
	require.False(t, circl.Recheckable,
		"a catalogue entry does not decay, so there is nothing to ask again")
}

// An enabled provider's own link sits beside its own verdict, and a provider
// with no per-hash page carries none.
func TestAddReputation_EachProviderCarriesItsOwnLink(t *testing.T) {
	rig := newRepRig(t, repByPath(t, map[string]repAnswer{
		vtPath:    vtAnswer(vtCleanBody),
		circlPath: circlAnswer(clKnownRecord),
	}))
	rig.setKey(t, repTestKey)
	rig.enable(t, reputation.ProviderVirusTotal, reputation.ProviderCIRCL)

	att := quarantinedAttachment(repTestHash)
	rig.srv.addReputation(context.Background(), &att)

	vt := providerEntry(t, att.Reputation, "virustotal")
	require.NotNil(t, vt.LinkURL)
	require.Equal(t, "https://www.virustotal.com/gui/file/"+repTestHash, *vt.LinkURL)

	require.Nil(t, providerEntry(t, att.Reputation, "circl").LinkURL,
		"CIRCL has no per-hash web UI, and a link that cannot answer is worse than none")
}

// The list is in canonical provider order, so the expanded view does not
// reshuffle itself between renders of the same page.
func TestAddReputation_ListsProvidersInCanonicalOrder(t *testing.T) {
	rig := newRepRig(t, repByPath(t, map[string]repAnswer{
		vtPath:    vtAnswer(vtCleanBody),
		mdPath:    {http.StatusNotFound, `{"error":{"code":404003}}`},
		circlPath: circlAnswer(clKnownRecord),
	}))
	rig.setKey(t, repTestKey)
	rig.enable(t, reputation.ProviderCIRCL, reputation.ProviderVirusTotal, reputation.ProviderMetaDefender)

	att := quarantinedAttachment(repTestHash)
	rig.srv.addReputation(context.Background(), &att)

	require.NotNil(t, att.Reputation)
	var keys []string
	for _, p := range att.Reputation.Providers {
		keys = append(keys, p.ProviderKey)
	}
	require.Equal(t, []string{"virustotal", "metadefender", "circl"}, keys)
}

// A commercial provider enabled with no key cannot be reached, so it is not
// asked and not listed.
//
// The settings endpoint refuses that combination, so this is the state an
// instance can only reach by writing the settings table directly — and the
// lookup has to fail closed rather than send an unauthenticated request to a
// service the operator has an account with.
func TestAddReputation_AnEnabledProviderWithNoKeyIsNotAsked(t *testing.T) {
	rig := newRepRig(t, repByPath(t, map[string]repAnswer{
		circlPath: circlAnswer(clKnownRecord),
	}))
	rig.enable(t, reputation.ProviderVirusTotal, reputation.ProviderCIRCL)

	att := quarantinedAttachment(repTestHash)
	rig.srv.addReputation(context.Background(), &att)

	require.Equal(t, int64(1), rig.hits.Load())
	require.NotNil(t, att.Reputation)
	require.Len(t, att.Reputation.Providers, 1)
	require.Equal(t, "circl", att.Reputation.Providers[0].ProviderKey)
}

// The whole payload survives JSON, which is the only part the UI ever sees.
func TestAddReputation_TheProviderListSurvivesJSON(t *testing.T) {
	rig := newRepRig(t, repByPath(t, map[string]repAnswer{
		vtPath:    vtAnswer(vtDetectedBody),
		circlPath: circlAnswer(clKnownRecord),
	}))
	rig.setKey(t, repTestKey)
	rig.enable(t, reputation.ProviderVirusTotal, reputation.ProviderCIRCL)

	att := quarantinedAttachment(repTestHash)
	rig.srv.addReputation(context.Background(), &att)

	body, err := json.Marshal(att)
	require.NoError(t, err)
	var out struct {
		Reputation *ticket.AttachmentReputation `json:"reputation"`
	}
	require.NoError(t, json.Unmarshal(body, &out))
	require.NotNil(t, out.Reputation)
	require.Equal(t, "detected", out.Reputation.State)
	require.Equal(t, "virustotal", out.Reputation.ProviderKey)
	require.Len(t, out.Reputation.Providers, 2)

	vt := providerEntry(t, out.Reputation, "virustotal")
	require.Equal(t, "VirusTotal", vt.Provider)
	require.NotNil(t, vt.Detected)
	require.Equal(t, 62, *vt.Detected)
	require.True(t, vt.Inline)

	circl := providerEntry(t, out.Reputation, "circl")
	require.Equal(t, "known", circl.State)
	require.Contains(t, circl.KnownFeeds, "NSRL")
	require.Nil(t, circl.Detected, "a catalogued file is never scanned, so no engine ran")
	require.False(t, circl.Inline)
}
