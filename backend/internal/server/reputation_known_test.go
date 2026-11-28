package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// "known" on the wire (#168).
//
// The white-box rig in reputation_test.go is reused wholesale; what is added
// here is the sixth state travelling the whole path — setting, provider,
// cache, payload — because every layer of it has a way to drop a state it was
// not told about, and two of them drop it silently.

// psKnownGoodBody is PolySwarm's answer for a hash a vendor feed carries.
// Their wire vocabulary is KNOWN_GOOD; ours is "known", because how much it is
// worth depends on which feed. No assertions, no detections, a polyscore of
// exactly 0.0: the file was never scanned, because it did not need to be.
const psKnownGoodBody = `{"result":[{
  "sha256":"275a021bbfb6489e54d471899f7db9d1663fc695ec2fe2a2c4538aabf651fd0f",
  "state":"KNOWN_GOOD",
  "polyscore":0.0,
  "detections":null,
  "assertions":[],
  "known_good":[
    {"tool":"nsrl","tool_metadata":{}},
    {"tool":"microsoft_windows","tool_metadata":{}}
  ],
  "metadata":[]
}]}`

// setProvider enables exactly one of the four services and disables the rest.
func (r *repRig) setProvider(t *testing.T, provider string) {
	t.Helper()
	r.enable(t, provider)
}

// A "known" verdict reaches the wire, with the feeds that carry the file and
// with no engine counts.
//
// Two failures are pinned here and both are silent. The payload builder ends
// in a `default` arm that returns nil for any state it does not recognise —
// correct for Unavailable, and for a sixth state it turns the strongest
// positive signal this system can produce into "not checked yet". And the
// counts must stay absent: a file answered out of a catalogue is never
// scanned, so "0 of 0 engines" would be a fabricated analysis attached to the
// one verdict staff are entitled to find reassuring.
func TestAddReputation_PutsAKnownVerdictOnTheWire(t *testing.T) {
	rig := newRepRig(t, repRespond(http.StatusOK, psKnownGoodBody))
	rig.setProvider(t, "polyswarm")
	rig.setKey(t, repTestKey)

	att := quarantinedAttachment(repTestHash)
	rig.srv.addReputation(context.Background(), &att)

	require.Equal(t, int64(1), rig.hits.Load(), "exactly one lookup")
	require.NotNil(t, att.Reputation,
		"a named feed carrying the file is a completed verdict, not an absence")
	require.Equal(t, "known", att.Reputation.State)
	require.Equal(t, []string{"microsoft_windows", "nsrl"}, att.Reputation.KnownFeeds,
		"staff have to see WHICH feed is speaking; the weight of the claim differs by feed")
	require.Equal(t, "PolySwarm", att.Reputation.Provider,
		"attribution is the point: a claim from nowhere is not a claim")

	require.Nil(t, att.Reputation.Detected, "a catalogued file is never scanned, so no engine ran")
	require.Nil(t, att.Reputation.Total)

	// And it survives JSON, which is the only part the UI ever sees.
	body, err := json.Marshal(att)
	require.NoError(t, err)
	var out struct {
		Reputation *ticket.AttachmentReputation `json:"reputation"`
	}
	require.NoError(t, json.Unmarshal(body, &out))
	require.NotNil(t, out.Reputation)
	require.Equal(t, "known", out.Reputation.State)
	require.Equal(t, []string{"microsoft_windows", "nsrl"}, out.Reputation.KnownFeeds)
}

// Every other verdict carries no feeds at all.
//
// An empty list is the honest rendering of "nobody vouched for this file", and
// a renderer keying its reassuring arm on the presence of feeds must never see
// one on a verdict that has no positive claim behind it.
func TestAddReputation_OnlyKnownCarriesFeeds(t *testing.T) {
	rig := newRepRig(t, repRespond(http.StatusOK, vtCleanBody))
	rig.setKey(t, repTestKey)

	att := quarantinedAttachment(repTestHash)
	rig.srv.addReputation(context.Background(), &att)

	require.NotNil(t, att.Reputation)
	require.Equal(t, "clean", att.Reputation.State)
	require.Empty(t, att.Reputation.KnownFeeds,
		"clean means engines found nothing, which is not the same as a feed having it on file")
}

// An enabled provider's own link sits next to its own verdict.
//
// The unconditional VirusTotal link on the attachment is a different thing —
// it is the public report any analyst can read, whatever is enabled. This one
// is the service that actually answered, and a verdict from PolySwarm beside a
// "read the full report" link to somebody else is the mislabelling the
// provider column exists to make impossible.
func TestAddReputation_APolySwarmVerdictCarriesAPolySwarmLink(t *testing.T) {
	rig := newRepRig(t, repRespond(http.StatusOK, psKnownGoodBody))
	rig.setProvider(t, "polyswarm")
	rig.setKey(t, repTestKey)

	att := quarantinedAttachment(repTestHash)
	rig.srv.addReputation(context.Background(), &att)

	require.NotNil(t, att.Reputation)
	require.Len(t, att.Reputation.Providers, 1)
	require.NotNil(t, att.Reputation.Providers[0].LinkURL)
	require.Equal(t,
		"https://polyswarm.network/scan/results/file/"+repTestHash,
		*att.Reputation.Providers[0].LinkURL)
}
