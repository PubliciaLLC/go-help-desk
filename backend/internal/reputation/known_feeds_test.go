package reputation_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
)

// ---------------------------------------------------------------------------
// "known" is licensed by the feed, and cannot outlive it.
//
// known is the one state in this feature that renders as reassurance, and it
// earns that only because a NAMED feed made a positive claim. With no name
// behind it the claim comes from nowhere, which is what the rest of this work
// refuses everywhere else — and it is worse here than elsewhere, because known
// is final: exempt from expiry and from the manual re-check, so once it is
// stored there is no path back.
//
// PolySwarm can produce the shape. KNOWN_GOOD arrives with no assertions and
// no detections, and its known_good entries can carry an empty tool, so a
// state set before the feeds are computed and never reconsidered ships known
// with nothing behind it.
// ---------------------------------------------------------------------------

// psKnownGoodNoFeeds is KNOWN_GOOD with nothing to attribute it to.
const psKnownGoodNoFeeds = `{"result":[{
  "sha256":"275a021bbfb6489e54d471899f7db9d1663fc695ec2fe2a2c4538aabf651fd0f",
  "state":"KNOWN_GOOD",
  "polyscore":0.0,
  "detections":null,
  "assertions":[],
  "known_good":[{"tool":"","tool_metadata":{}}],
  "metadata":[]
}]}`

// psKnownGoodNamed is the same answer with a feed that can be named.
const psKnownGoodNamed = `{"result":[{
  "sha256":"275a021bbfb6489e54d471899f7db9d1663fc695ec2fe2a2c4538aabf651fd0f",
  "state":"KNOWN_GOOD",
  "polyscore":0.0,
  "detections":null,
  "assertions":[],
  "known_good":[{"tool":"microsoft_windows","tool_metadata":{}}],
  "metadata":[]
}]}`

// A KNOWN_GOOD answer nobody can be named for is "unscanned", not "known".
//
// unscanned is exactly what happened: PolySwarm holds the file and gave no
// attributable verdict for it. Not unseen — they have the file. Not known —
// nobody vouched for it by name, and the state that says somebody did must not
// be reachable without one.
func TestKnown_WithNoFeedIsNotKnown(t *testing.T) {
	cases := []struct{ name, feeds string }{
		{"an unnamed feed", `[{"tool":"","tool_metadata":{}}]`},
		{"an empty list", `[]`},
		{"no list at all", `null`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"result":[{"state":"KNOWN_GOOD","detections":null,"assertions":[],` +
				`"known_good":` + tc.feeds + `,"metadata":[]}]}`
			srv := serveCanned(t, http.StatusOK, body)

			got, err := newPolySwarm(t, srv.URL, "k").Lookup(context.Background(), eicarSHA)
			require.NoError(t, err)

			require.NotEqual(t, reputation.Known, got.State,
				"known is reassurance licensed by a named feed; there is no name here")
			require.Equal(t, reputation.Unscanned, got.State,
				"they hold the file and gave no attributable verdict, which is what unscanned means")
			require.Empty(t, got.KnownFeeds)
		})
	}
}

// The same body with a feed that CAN be named is still known, so the fix
// cannot be "never report known".
func TestKnown_WithAFeedIsStillKnown(t *testing.T) {
	srv := serveCanned(t, http.StatusOK, psKnownGoodNamed)

	got, err := newPolySwarm(t, srv.URL, "k").Lookup(context.Background(), eicarSHA)
	require.NoError(t, err)
	require.Equal(t, reputation.Known, got.State)
	require.Equal(t, []string{"microsoft_windows"}, got.KnownFeeds)
}

// And the unnamed one is not cached as final either.
//
// This is what makes the shape worth refusing at the source rather than at the
// renderer: a stored known is exempt from expiry and from the manual re-check,
// so an unattributed one would sit on the ticket for the life of the instance
// with no way for anybody to ask again.
func TestKnown_AnUnattributedAnswerIsNotFinal(t *testing.T) {
	srv := serveCanned(t, http.StatusOK, psKnownGoodNoFeeds)

	got, err := newPolySwarm(t, srv.URL, "k").Lookup(context.Background(), eicarSHA)
	require.NoError(t, err)
	require.NotEqual(t, reputation.Known, got.State,
		"a verdict that never expires must have a source behind it")
}
