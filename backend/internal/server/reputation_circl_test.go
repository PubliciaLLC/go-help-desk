package server

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// CIRCL and "off" at the wiring (#168).
//
// The rule these pin is the one CIRCL forced: a lookup runs when the
// configured provider CAN run. Before it, the lookup was refused on an empty
// key before anything had asked which provider was configured — which is
// correct for three commercial services and leaves a keyless one permanently
// disabled.

// clKnownRecord is one hashlookup record, in the shape the live service
// serves: every scalar a string, ProductCode an object on this one, and a
// KnownMalicious field that is a MalShare membership flag rather than a
// verdict.
const clKnownRecord = `{
  "FileName":"jquery-1.12.4.min.js",
  "FileSize":"97163",
  "KnownMalicious":"malshare.com",
  "ProductCode":{"ProductName":"Global Discovery Vacations"},
  "SHA-256":"275a021bbfb6489e54d471899f7db9d1663fc695ec2fe2a2c4538aabf651fd0f",
  "db":"nsrl_modern_rds",
  "hashlookup:trust":100,
  "source":"NSRL"
}`

// CIRCL looks a hash up on an instance that has configured no key at all.
//
// The whole point of the fourth provider: it needs no key, ever. An operator
// choosing it and finding the feature silently dead would have no way to tell
// why, because the thing they would look for — a key field — is the thing that
// does not apply.
func TestReputationLookup_CIRCLNeedsNoKey(t *testing.T) {
	rig := newRepRig(t, repRespond(http.StatusOK, clKnownRecord))
	rig.setProvider(t, "circl")

	att := quarantinedAttachment(repTestHash)
	rig.srv.addReputation(context.Background(), &att)

	require.Equal(t, int64(1), rig.hits.Load(),
		"a keyless provider was refused for having no key")
	require.NotNil(t, att.Reputation)
	require.Equal(t, "known", att.Reputation.State)
	require.Contains(t, att.Reputation.KnownFeeds, "NSRL")
	require.NotEmpty(t, att.Reputation.Provider, "the claim needs a source")
	require.Nil(t, att.Reputation.Detected,
		"KnownMalicious is not a detection: MalShare's corpus contains jQuery")
}

// And today's behaviour is unchanged for the three commercial providers: no
// key is still a link and no lookup, not a lookup that fails and not silence.
func TestReputationLookup_ACommercialProviderWithNoKeyStillLinks(t *testing.T) {
	rig := newRepRig(t, repRespond(http.StatusOK, vtDetectedBody))
	ctx := context.Background()

	att := quarantinedAttachment(repTestHash)
	rig.srv.addReputation(ctx, &att)
	rig.srv.addReputationURL(ctx, &att)

	require.Equal(t, int64(0), rig.hits.Load(), "no key, no lookup")
	require.Nil(t, att.Reputation)
	require.NotNil(t, att.ReputationURL, "the link needs no key and still works")
	require.Contains(t, *att.ReputationURL, "virustotal.com")
}

// CIRCL is the other way round: a lookup, and no link OF ITS OWN.
//
// There is no per-hash web UI to send anybody to — the root serves a Swagger
// page — so its entry in the expanded view carries no link rather than one
// pointed at a page that cannot answer the question.
//
// The attachment's own VirusTotal link is there regardless, and that is not a
// contradiction: a link is not a lookup. Nothing is sent to VirusTotal by an
// instance that has only CIRCL enabled; the anchor is the analyst's own act in
// their own browser, on a hash that is on the page with a copy control beside
// it either way.
func TestReputationLookup_CIRCLHasNoLinkOfItsOwn(t *testing.T) {
	rig := newRepRig(t, repRespond(http.StatusOK, clKnownRecord))
	rig.setProvider(t, "circl")
	ctx := context.Background()

	att := quarantinedAttachment(repTestHash)
	rig.srv.addReputation(ctx, &att)
	rig.srv.addReputationURL(ctx, &att)

	require.NotNil(t, att.Reputation)
	require.Len(t, att.Reputation.Providers, 1)
	require.Nil(t, att.Reputation.Providers[0].LinkURL,
		"a link to a page with no per-hash view is worse than no link")

	require.NotNil(t, att.ReputationURL,
		"the hash link is the analyst's own act and does not follow the toggles")
	require.Contains(t, *att.ReputationURL, "virustotal.com")
}
