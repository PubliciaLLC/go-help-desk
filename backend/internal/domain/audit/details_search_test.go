package audit_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
)

func TestFilter_DetailsSearch(t *testing.T) {
	now := time.Now()
	day := 24 * time.Hour
	at := func(d time.Duration) *time.Time { t := now.Add(d); return &t }
	max := time.Duration(audit.DetailsSearchMaxDays) * day
	limit := audit.DetailsSearchMaxSpan

	for _, tc := range []struct {
		name         string
		f            audit.Filter
		wantSearch   bool
		wantReason   string
		wantTooLong  bool
		wantReversed bool
	}{
		{"no text needs no range", audit.Filter{}, false, "", false, false},
		{"text, no range", audit.Filter{Q: "x"}, false, audit.DetailsSkippedNoRange, false, false},
		{"text, only from", audit.Filter{Q: "x", From: at(-day)}, false, audit.DetailsSkippedNoRange, false, false},
		{"text, only to", audit.Filter{Q: "x", To: at(0)}, false, audit.DetailsSkippedNoRange, false, false},
		{"text, a day", audit.Filter{Q: "x", From: at(-day), To: at(0)}, true, "", false, false},
		{"text, exactly 90 days", audit.Filter{Q: "x", From: at(-max), To: at(0)}, true, "", false, false},
		// A 90-calendar-day range picked in local time is 90 days and an hour across
		// the clock change that gives a day 25 hours.
		{"text, 90 days and an hour (a clock change)", audit.Filter{Q: "x", From: at(-max - time.Hour), To: at(0)}, true, "", false, false},
		{"text, a second over that", audit.Filter{Q: "x", From: at(-limit - time.Second), To: at(0)}, false, "", true, false},

		{"no text, over the limit is just a date filter", audit.Filter{From: at(-limit - time.Second), To: at(0)}, false, "", false, false},
		{"text, a range that ends before it starts", audit.Filter{Q: "x", From: at(0), To: at(-day)}, true, "", false, true},
		{"no text, a range that ends before it starts is a date filter that holds nothing", audit.Filter{From: at(0), To: at(-day)}, false, "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.wantSearch, tc.f.SearchesDetails())
			require.Equal(t, tc.wantReason, tc.f.DetailsSkippedReason())
			require.Equal(t, tc.wantTooLong, tc.f.DateRangeTooLong())
			require.Equal(t, tc.wantReversed, tc.f.DateRangeReversed())
		})
	}
}

// The other tests read DetailsSearchMaxSpan, so they would follow it if the slack
// grew. This one writes the numbers out: 90 days and an hour is the most a clock
// change can add to 90 calendar days, and anything past it is refused.
func TestDetailsSearchMaxSpan_IsNinetyDaysAndAnHour(t *testing.T) {
	require.Equal(t, 90, audit.DetailsSearchMaxDays)
	require.Equal(t, 90*24*time.Hour+time.Hour, audit.DetailsSearchMaxSpan)

	to := time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)
	within := to.Add(-(90*24*time.Hour + time.Hour))
	over := to.Add(-(90*24*time.Hour + time.Hour + time.Second))
	require.False(t, audit.Filter{Q: "x", From: &within, To: &to}.DateRangeTooLong())
	require.True(t, audit.Filter{Q: "x", From: &within, To: &to}.SearchesDetails())
	require.True(t, audit.Filter{Q: "x", From: &over, To: &to}.DateRangeTooLong(), "90 days, 1 hour and 1 second is refused")
	require.False(t, audit.Filter{Q: "x", From: &over, To: &to}.SearchesDetails())
}
