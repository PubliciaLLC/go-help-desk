package ticket

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// pausedSecondsAt is the pause total a restarted SLA record carries as its base
// (#443b): the ticket's closed Pending intervals, plus the part of an open one
// already elapsed. It must agree with sla.Elapsed's clipping, so an interval
// that opens after `at` contributes nothing.
func TestPausedSecondsAt(t *testing.T) {
	base := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	since := func(d time.Duration) *time.Time { v := base.Add(d); return &v }

	cases := []struct {
		name string
		t    Ticket
		at   time.Time
		want int64
	}{
		{name: "not pending, nothing paused", t: Ticket{}, at: base, want: 0},
		{name: "not pending, closed intervals only", t: Ticket{SLAPausedSeconds: 300}, at: base, want: 300},
		{
			name: "pending since T, at T+90s",
			t:    Ticket{SLAPausedSeconds: 300, PendingSince: since(0)},
			at:   base.Add(90 * time.Second),
			want: 390,
		},
		{
			name: "pending with nothing closed before",
			t:    Ticket{PendingSince: since(0)},
			at:   base.Add(time.Hour),
			want: 3600,
		},
		{
			name: "pending that opens after at is clipped away",
			t:    Ticket{SLAPausedSeconds: 300, PendingSince: since(time.Minute)},
			at:   base,
			want: 300,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, pausedSecondsAt(tc.t, tc.at))
		})
	}
}
