package admin_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// How often a stored reputation verdict is re-checked.
//
// The setting is a word and the lookup needs a duration, so the mapping is the
// thing under test: a value that reads back as a different number of days than
// the operator chose is a setting that lies, and the only symptom is an API
// allowance draining four times too fast — or a verdict that never refreshes
// at all.

const day = 24 * time.Hour

// An instance that has never touched the setting re-checks fortnightly.
//
// Not "never": every instance that upgrades into this feature inherits this
// value, and a cache that is kept forever is the hole the setting exists to
// close. Not "weekly" either — the default has to be affordable on a free tier
// nobody chose.
func TestAdminService_ReputationRefresh_DefaultsToBiweekly(t *testing.T) {
	svc := admin.NewService(newFakeAdminStore())

	require.Equal(t, admin.ReputationRefreshBiweekly, svc.ReputationRefresh(context.Background()))
	require.Equal(t, 14*day, svc.ReputationRefreshInterval(context.Background()))
}

// Each accepted word is the interval the table in #168 says it is.
func TestAdminService_ReputationRefresh_EachValueIsItsInterval(t *testing.T) {
	cases := []struct {
		value string
		want  time.Duration
	}{
		{admin.ReputationRefreshWeekly, 7 * day},
		{admin.ReputationRefreshBiweekly, 14 * day},
		{admin.ReputationRefreshMonthly, 30 * day},
		{admin.ReputationRefreshQuarterly, 90 * day},
		// Zero is "no automatic re-check", not "re-check immediately". An
		// interval of zero read as an age threshold would re-fetch every
		// verdict on every render, which is the opposite of what the operator
		// asked for and spends their allowance doing it.
		{admin.ReputationRefreshNever, 0},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			svc := admin.NewService(newFakeAdminStore())
			ctx := context.Background()
			require.NoError(t, svc.SetRaw(ctx, admin.KeyAttachmentReputationRefresh, []byte(`"`+tc.value+`"`)))

			require.Equal(t, tc.value, svc.ReputationRefresh(ctx))
			require.Equal(t, tc.want, svc.ReputationRefreshInterval(ctx))
		})
	}
}

// Anything unrecognised reads as the default, the same rule as the scan policy
// and the provider beside it: a typo must land somewhere predictable. The
// settings endpoint refuses the write in the first place; this is what happens
// to a row that got in some other way.
func TestAdminService_ReputationRefresh_UnrecognisedFallsBackToBiweekly(t *testing.T) {
	for _, bad := range []string{"", "fortnightly", "Weekly", "14", "daily", " never"} {
		svc := admin.NewService(newFakeAdminStore())
		ctx := context.Background()
		require.NoError(t, svc.SetRaw(ctx, admin.KeyAttachmentReputationRefresh, []byte(`"`+bad+`"`)))

		require.Equalf(t, admin.ReputationRefreshBiweekly, svc.ReputationRefresh(ctx),
			"%q must fall back to the default", bad)
		require.Equal(t, 14*day, svc.ReputationRefreshInterval(ctx))
	}
}

// A value the store cannot even parse as a string is still the default rather
// than a panic or an empty interval.
func TestAdminService_ReputationRefresh_UnparseableFallsBackToBiweekly(t *testing.T) {
	svc := admin.NewService(newFakeAdminStore())
	ctx := context.Background()
	require.NoError(t, svc.SetRaw(ctx, admin.KeyAttachmentReputationRefresh, []byte(`{"weekly":true}`)))

	require.Equal(t, admin.ReputationRefreshBiweekly, svc.ReputationRefresh(ctx))
}

// ValidReputationRefresh is what the settings endpoint refuses a write with,
// so it has to accept every value the reader understands and nothing else.
func TestAdminService_ValidReputationRefresh(t *testing.T) {
	for _, good := range []string{"weekly", "biweekly", "monthly", "quarterly", "never"} {
		require.Truef(t, admin.ValidReputationRefresh(good), "%q is one of the five", good)
	}
	for _, bad := range []string{"", " ", "Weekly", "fortnightly", "7d", "off", "14"} {
		require.Falsef(t, admin.ValidReputationRefresh(bad), "%q is not a value this reads", bad)
	}
}
