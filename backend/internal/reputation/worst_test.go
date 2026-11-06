package reputation_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
)

// The worst-verdict ordering, which is what a staff member reads inline on an
// attachment row before they expand anything (#168).
//
//	detected  >  unseen  >  unscanned  >  clean  >  known
//
// Two of those are not obvious and each has its own test below, because each
// is a decision somebody will later read as a bug and "fix".

// Every pair, both ways round, so the answer cannot depend on the order the
// providers happened to be asked in.
func TestWorst_TheOrdering(t *testing.T) {
	order := []reputation.State{
		reputation.Detected,
		reputation.Unseen,
		reputation.Unscanned,
		reputation.Clean,
		reputation.Known,
	}

	for i, worse := range order {
		for j, better := range order[i+1:] {
			_ = j
			t.Run(string(worse)+" beats "+string(better), func(t *testing.T) {
				require.Equal(t, 0, reputation.Worst([]reputation.State{worse, better}),
					"%s must outrank %s", worse, better)
				require.Equal(t, 1, reputation.Worst([]reputation.State{better, worse}),
					"%s must outrank %s whichever order they arrive in", worse, better)
			})
		}
	}
}

// unseen outranks clean, and this is the one an unhurried reader will want to
// invert.
//
// Only quarantined files are looked up, so every hash reaching this ordering
// is one the local scanner has already called malicious. A file no reputation
// service has ever seen is a novel sample — more concerning than one seventy
// engines have examined and passed, not less. Ordered the other way round it
// would bury exactly the case worth a second look under a reassuring word.
func TestWorst_UnseenOutranksClean(t *testing.T) {
	require.Equal(t, 0, reputation.Worst([]reputation.State{reputation.Unseen, reputation.Clean}))
	require.Equal(t, 1, reputation.Worst([]reputation.State{reputation.Clean, reputation.Unseen}))
}

// unavailable is not in the ordering at all: it is a failed lookup, not a
// verdict, and it must never displace a real answer.
//
// VirusTotal timing out while CIRCL says "known" shows "known" — including
// when "known" is the least serious verdict there is, which is the case that
// makes this a real rule rather than an accident of the ranking.
func TestWorst_UnavailableNeverDisplacesAVerdict(t *testing.T) {
	for _, verdict := range []reputation.State{
		reputation.Known, reputation.Clean, reputation.Unscanned,
		reputation.Unseen, reputation.Detected,
	} {
		t.Run(string(verdict), func(t *testing.T) {
			require.Equal(t, 1, reputation.Worst(
				[]reputation.State{reputation.Unavailable, verdict}),
				"a failed lookup must not outrank %s", verdict)
			require.Equal(t, 0, reputation.Worst(
				[]reputation.State{verdict, reputation.Unavailable}))
		})
	}
}

// And when nothing answered there is no index to give, which is how the caller
// tells "every provider failed" from "one of them said known".
func TestWorst_NothingAnswered(t *testing.T) {
	require.Equal(t, -1, reputation.Worst(nil))
	require.Equal(t, -1, reputation.Worst([]reputation.State{}))
	require.Equal(t, -1, reputation.Worst([]reputation.State{
		reputation.Unavailable, reputation.Unavailable,
	}))
	require.Equal(t, -1, reputation.Worst([]reputation.State{""}),
		"the zero State is not a verdict either")
}

// Ties go to the earliest entry, so a caller passing providers in
// ProviderNames order gets the same summary on every render rather than one
// that depends on which lookup returned first.
func TestWorst_TiesGoToTheFirst(t *testing.T) {
	require.Equal(t, 0, reputation.Worst([]reputation.State{
		reputation.Detected, reputation.Detected, reputation.Detected,
	}))
	require.Equal(t, 1, reputation.Worst([]reputation.State{
		reputation.Unavailable, reputation.Clean, reputation.Clean,
	}))
}

// ProviderNames is the canonical order, and it has to hold every provider this
// build can talk to — a provider missing from it is one no setting reaches and
// no lookup runs.
func TestProviderNames_HoldsEveryValidProvider(t *testing.T) {
	names := reputation.ProviderNames()
	require.Equal(t, []string{"virustotal", "metadefender", "polyswarm", "circl"}, names)
	for _, n := range names {
		require.True(t, reputation.ValidProvider(n), "%q is not a provider this build accepts", n)
		require.NotEmpty(t, reputation.DisplayName(n), "%q has no name to attribute a claim to", n)
	}
}

// Only CIRCL needs no key, and that is the whole reason it has no key setting.
func TestNeedsKey(t *testing.T) {
	require.True(t, reputation.NeedsKey("virustotal"))
	require.True(t, reputation.NeedsKey("metadefender"))
	require.True(t, reputation.NeedsKey("polyswarm"))
	require.False(t, reputation.NeedsKey("circl"))
	require.True(t, reputation.NeedsKey("nonsense"),
		"a provider this build cannot talk to is not turned on by supplying nothing")
}

// Recheckable is what arms the per-provider Check again control, and it has to
// agree with what Refresh would actually do — a control that is armed and then
// refused is worse than one that is greyed out with a date on it.
func TestRecheckable(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	old := now.Add(-30 * 24 * time.Hour)
	recent := now.Add(-24 * time.Hour)

	cases := []struct {
		name      string
		state     reputation.State
		fetchedAt time.Time
		want      bool
	}{
		{"a month-old clean verdict", reputation.Clean, old, true},
		{"a month-old unseen", reputation.Unseen, old, true},
		{"a month-old unscanned", reputation.Unscanned, old, true},
		{"checked yesterday", reputation.Clean, recent, false},
		{"a detection never changes", reputation.Detected, old, false},
		{"a catalogue entry never decays", reputation.Known, old, false},
		{"a failed lookup has nothing cached to re-check", reputation.Unavailable, old, false},
		{"the zero state", reputation.State(""), old, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, reputation.Recheckable(tc.state, tc.fetchedAt, now))
		})
	}
}

// The hash link is a string and nothing else: no key, no request, no
// dependence on which providers are enabled.
func TestHashLink(t *testing.T) {
	const sha = "275a021bbfb6489e54d471899f7db9d1663fc695ec2fe2a2c4538aabf651fd0f"
	require.Equal(t, "https://www.virustotal.com/gui/file/"+sha, reputation.HashLink(sha))
	require.Empty(t, reputation.HashLink(""), "no hash, no link")
}
