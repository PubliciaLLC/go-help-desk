package ticket_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

func TestService_VolumeByDay(t *testing.T) {
	boom := errors.New("boom")
	now := time.Date(2026, 11, 2, 15, 0, 0, 0, time.UTC)
	days := []ticket.DayVolume{{Day: "2026-11-01", Created: 2, Resolved: 1}, {Day: "2026-11-02"}}

	cases := []struct {
		name    string
		days    []ticket.DayVolume
		err     error
		wantErr error
	}{
		{name: "the store's days", days: days},
		{name: "an unknown zone is the sentinel", err: ticket.ErrUnknownTimeZone, wantErr: ticket.ErrUnknownTimeZone},
		{name: "another store error is returned", err: boom, wantErr: boom},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.store.volumeDays, h.store.errVolume = tc.days, tc.err
			f := ticket.Filter{ActorID: uuid.New(), Visibility: ticket.VisibilityScoped}

			got, err := h.svc.VolumeByDay(context.Background(), f, "Europe/London", now)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.days, got)
			require.Equal(t, []volumeCall{{f, "Europe/London", now}}, h.store.volumeCalls)
		})
	}
}

// DESIGN.md "Dashboard" gives a volume day as {"date", "created", "resolved"};
// the type is marshalled as it stands, so its tags are the wire contract.
func TestDayVolume_JSONShape(t *testing.T) {
	b, err := json.Marshal(ticket.DayVolume{Day: "2026-10-26", Created: 3, Resolved: 2})
	require.NoError(t, err)
	require.JSONEq(t, `{"date":"2026-10-26","created":3,"resolved":2}`, string(b))
}
