package reputation_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
)

// CIRCL's cap is OURS, not theirs.
//
// Sixty sequential and twenty concurrent lookups against the live service drew
// no 429, no rate-limit headers and no published ceiling; their only statement
// is that hashlookup is "free and served as a best-effort basis". So this
// number is a politeness cap we chose, and the test restates it as a literal
// for the same reason the other ceilings are restated: reading the constant
// would assert nothing.
//
// A provider with no meter at all is the failure being prevented. Spend
// refuses one outright, so a fourth provider added to the setting and
// forgotten here cannot spend without limit against a best-effort service
// somebody else pays for.
const circlHourly = 300

func TestBudget_CIRCLIsMeteredEvenThoughNobodyPublishesALimit(t *testing.T) {
	clk := newClock(t, "2026-09-23T10:00:00Z")
	b := clk.budget()

	for i := 0; i < circlHourly; i++ {
		require.NoErrorf(t, b.Spend(reputation.ProviderCIRCL),
			"lookup %d of %d was refused inside our own cap", i+1, circlHourly)
	}

	err := b.Spend(reputation.ProviderCIRCL)
	require.Error(t, err, "an unmetered provider is how somebody's free service gets hammered")
	require.ErrorIs(t, err, reputation.ErrRateLimited,
		"this clears on the hour, so it must not tell a caller to wait for 00:00 UTC")
}

// And it clears on the hour rather than at midnight.
func TestBudget_CIRCLsCapResetsOnTheHour(t *testing.T) {
	clk := newClock(t, "2026-09-23T10:30:00Z")
	b := clk.budget()

	for i := 0; i < circlHourly; i++ {
		require.NoError(t, b.Spend(reputation.ProviderCIRCL))
	}
	require.Error(t, b.Spend(reputation.ProviderCIRCL))

	clk.advance(time.Hour)
	require.NoError(t, b.Spend(reputation.ProviderCIRCL),
		"the next hour is a fresh allowance")
}

// CIRCL spends nobody else's allowance and nobody else spends its.
func TestBudget_CIRCLDoesNotShareAnAllowance(t *testing.T) {
	clk := newClock(t, "2026-09-23T10:00:00Z")
	b := clk.budget()

	for i := 0; i < circlHourly; i++ {
		require.NoError(t, b.Spend(reputation.ProviderCIRCL))
	}
	require.Error(t, b.Spend(reputation.ProviderCIRCL))

	require.NoError(t, b.Spend(reputation.ProviderVirusTotal),
		"a spent CIRCL hour is not a spent VirusTotal day")
	require.NoError(t, b.Spend(reputation.ProviderMetaDefender))
	require.NoError(t, b.Spend(reputation.ProviderPolySwarm))
}
