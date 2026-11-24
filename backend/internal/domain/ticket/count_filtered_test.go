package ticket_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// CountFiltered applies the filter it is given, as ListFiltered does: it does
// not re-derive the actor's authority, and it reports the store's error.
func TestService_CountFiltered(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name    string
		count   int64
		err     error
		wantErr bool
	}{
		{name: "the store's count", count: 42},
		{name: "none", count: 0},
		{name: "a store error is returned", err: boom, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.store.countFiltered, h.store.errCountFiltered = tc.count, tc.err
			f := ticket.Filter{ActorID: uuid.New(), Visibility: ticket.VisibilityScoped, Queue: ticket.QueueMine}

			got, err := h.svc.CountFiltered(context.Background(), f)
			if tc.wantErr {
				require.ErrorIs(t, err, boom)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.count, got)
			require.Equal(t, []ticket.Filter{f}, h.store.countedFilters)
		})
	}
}
