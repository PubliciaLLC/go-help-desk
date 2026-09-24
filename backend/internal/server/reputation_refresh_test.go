package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
)

// Expiry and the named provider, at the seam where the setting meets the
// lookup (#168).
//
// White-box for the same reason as reputation_test.go beside it: what is under
// test is the wiring. A refresh interval that is read correctly, converted
// correctly and then never passed to the service is a feature that is
// configured, documented and does nothing.
//
// Nothing here sleeps. Every "how old is this verdict" is set up by writing a
// verdict into the cache with the fetch time it would have had, which is what
// the database hands back.

const repDay = 24 * time.Hour

// seedVerdict puts a verdict in the cache as though it had been fetched ago
// ago, the way the store returns one.
func (r *repRig) seedVerdict(t *testing.T, sha string, rep reputation.Reputation, ago time.Duration) {
	t.Helper()
	rep.FetchedAt = time.Now().Add(-ago)
	provider := reputation.ProviderVirusTotal
	if enabled := r.admin.EnabledReputationProviders(context.Background()); len(enabled) > 0 {
		provider = enabled[0]
	}
	require.NoError(t, r.store.Put(context.Background(), sha, provider, rep))
}

func repCleanVerdict() reputation.Reputation {
	at := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	return reputation.Reputation{State: reputation.Clean, Detected: 0, Total: 70, AnalysedAt: &at}
}

// A verdict past the configured interval is re-checked on the next render, and
// the new answer is what staff see.
//
// The default is a fortnight, and this row is three weeks old: the file was
// clean when we asked and is a detection now, which is exactly the change the
// setting exists to catch.
func TestAddReputation_ReChecksAVerdictPastTheInterval(t *testing.T) {
	rig := newRepRig(t, repRespond(200, vtDetectedBody))
	rig.setKey(t, repTestKey)
	rig.seedVerdict(t, repTestHash, repCleanVerdict(), 21*repDay)

	att := quarantinedAttachment(repTestHash)
	rig.srv.addReputation(context.Background(), &att)

	require.Equal(t, int64(1), rig.hits.Load(), "a three week old verdict must be re-checked")
	require.NotNil(t, att.Reputation)
	require.Equal(t, "detected", att.Reputation.State, "the new answer, not the one on file")
}

// The interval is the operator's, not a constant: "never" switches automatic
// re-checking off and a year-old verdict still renders untouched.
func TestAddReputation_FollowsTheRefreshSetting(t *testing.T) {
	cases := []struct {
		setting  string
		age      time.Duration
		wantHits int64
	}{
		{admin.ReputationRefreshNever, 400 * repDay, 0},
		{admin.ReputationRefreshQuarterly, 60 * repDay, 0},
		{admin.ReputationRefreshQuarterly, 100 * repDay, 1},
		{admin.ReputationRefreshWeekly, 8 * repDay, 1},
		{admin.ReputationRefreshWeekly, 6 * repDay, 0},
	}
	for _, tc := range cases {
		t.Run(tc.setting+"/"+tc.age.String(), func(t *testing.T) {
			rig := newRepRig(t, repRespond(200, vtCleanBody))
			rig.setKey(t, repTestKey)
			require.NoError(t, rig.admin.SetRaw(context.Background(),
				admin.KeyAttachmentReputationRefresh, []byte(`"`+tc.setting+`"`)))
			rig.seedVerdict(t, repTestHash, repCleanVerdict(), tc.age)

			att := quarantinedAttachment(repTestHash)
			rig.srv.addReputation(context.Background(), &att)

			require.Equal(t, tc.wantHits, rig.hits.Load())
			require.NotNil(t, att.Reputation, "either way the reader sees a verdict")
		})
	}
}

// A detection never expires, whatever the setting says. Re-confirming known
// malware is the one lookup guaranteed to tell nobody anything.
func TestAddReputation_ADetectionIsNotReChecked(t *testing.T) {
	rig := newRepRig(t, repRespond(200, vtCleanBody))
	rig.setKey(t, repTestKey)
	require.NoError(t, rig.admin.SetRaw(context.Background(),
		admin.KeyAttachmentReputationRefresh, []byte(`"weekly"`)))
	rig.seedVerdict(t, repTestHash, reputation.Reputation{
		State: reputation.Detected, Detected: 62, Total: 81,
	}, 400*repDay)

	att := quarantinedAttachment(repTestHash)
	rig.srv.addReputation(context.Background(), &att)

	require.Equal(t, int64(0), rig.hits.Load())
	require.NotNil(t, att.Reputation)
	require.Equal(t, "detected", att.Reputation.State)
}

// A re-check that cannot be made leaves the stale verdict on the page rather
// than replacing it with nothing.
//
// The allowance is spent here, which is the ordinary way this happens on a
// busy afternoon. Dropping the verdict would make a quarantined file's answer
// blink out the moment it passed its interval, for the rest of the day.
func TestAddReputation_AStaleVerdictSurvivesAFailedReCheck(t *testing.T) {
	rig := newRepRig(t, repRespond(200, vtDetectedBody))
	rig.setKey(t, repTestKey)
	rig.seedVerdict(t, repTestHash, repCleanVerdict(), 30*repDay)

	// The whole minute's allowance, gone before the render.
	for i := 0; i < 4; i++ {
		require.NoError(t, rig.srv.repBudget.Spend(reputation.ProviderVirusTotal))
	}

	att := quarantinedAttachment(repTestHash)
	rig.srv.addReputation(context.Background(), &att)

	require.Equal(t, int64(0), rig.hits.Load())
	require.NotNil(t, att.Reputation, "a stale verdict is still a verdict")
	require.Equal(t, "clean", att.Reputation.State)
}

// The verdict names the service that gave it.
//
// The UI has to be able to write "VirusTotal has never seen this file" rather
// than "the reputation service has never seen this file", and it cannot work
// the name out for itself: the provider is a session-gated setting staff
// cannot read, which is why the server already sends a finished link. A claim
// with a source is worth more than a claim from nowhere.
func TestAddReputation_NamesTheProvider(t *testing.T) {
	cases := []struct {
		setting string
		want    string
	}{
		{reputation.ProviderVirusTotal, "VirusTotal"},
		{reputation.ProviderMetaDefender, "MetaDefender"},
	}
	for _, tc := range cases {
		t.Run(tc.setting, func(t *testing.T) {
			rig := newRepRig(t, repRespond(200, vtCleanBody))
			rig.setProvider(t, tc.setting)
			rig.setKey(t, repTestKey)
			rig.seedVerdict(t, repTestHash, repCleanVerdict(), time.Hour)

			att := quarantinedAttachment(repTestHash)
			rig.srv.addReputation(context.Background(), &att)

			require.NotNil(t, att.Reputation)
			require.Equal(t, tc.want, att.Reputation.Provider,
				"the payload must carry the provider's name as a person reads it")
		})
	}
}

// And it says when we last asked, which is what the "check again" control is
// enabled or disabled on.
//
// Two cases, because they arrive by different paths: a cached verdict carries
// the row's fetch time, and one fetched by this very call carries none — the
// provider does not know when we asked. Sending null for the second would
// leave the control looking un-armed a second after a lookup.
func TestAddReputation_SendsWhenWeLastAsked(t *testing.T) {
	t.Run("a cached verdict carries the row's fetch time", func(t *testing.T) {
		rig := newRepRig(t, repRespond(200, vtCleanBody))
		rig.setKey(t, repTestKey)
		fetched := time.Now().Add(-3 * repDay)
		rig.seedVerdict(t, repTestHash, repCleanVerdict(), 3*repDay)

		att := quarantinedAttachment(repTestHash)
		rig.srv.addReputation(context.Background(), &att)

		require.NotNil(t, att.Reputation)
		require.NotNil(t, att.Reputation.FetchedAt)
		require.WithinDuration(t, fetched, *att.Reputation.FetchedAt, time.Minute)
	})

	t.Run("a verdict fetched right now says so", func(t *testing.T) {
		rig := newRepRig(t, repRespond(200, vtCleanBody))
		rig.setKey(t, repTestKey)

		att := quarantinedAttachment(repTestHash)
		rig.srv.addReputation(context.Background(), &att)

		require.NotNil(t, att.Reputation)
		require.NotNil(t, att.Reputation.FetchedAt,
			"a fresh lookup happened now; null would re-arm the control immediately")
		require.WithinDuration(t, time.Now(), *att.Reputation.FetchedAt, time.Minute)
	})
}
