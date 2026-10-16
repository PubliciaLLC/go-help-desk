package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// The SLA status and the Reopen affordance are for staff and administrators.
// Any other role, one this code does not recognise included, gets neither: the
// view is built without touching the settings or the SLA service at all, so a
// Server with neither (nil here, which would panic if asked) is enough to prove
// they were never consulted.
func TestTicketViews_OnlyStaffAndAdminGetSLAAndCanReopen(t *testing.T) {
	closed := time.Now()
	ts := []ticket.Ticket{{ClosedAt: &closed}}
	s := &Server{}

	for _, role := range []user.Role{user.RoleUser, user.Role("auditor"), user.Role("")} {
		t.Run(string(role), func(t *testing.T) {
			views, err := s.ticketViews(context.Background(), ts, &authmw.Actor{Role: role})
			require.NoError(t, err)
			require.Len(t, views, 1)
			require.Nil(t, views[0].SLA)
			require.False(t, views[0].CanReopen)
		})
	}
	t.Run("no actor", func(t *testing.T) {
		views, err := s.ticketViews(context.Background(), ts, nil)
		require.NoError(t, err)
		require.Nil(t, views[0].SLA)
		require.False(t, views[0].CanReopen)
	})
}
