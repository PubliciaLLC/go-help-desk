package server_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Follow entries must never crowd a ticket's real history off its feed. The
// feed holds the newest 500 entries, so enough follow/unfollow toggles would
// push "created" and "assigned" out, for administrators as much as staff.
func TestTicketFeed_FollowTogglesNeverCrowdOutRealHistory(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk := newStaffTicket(t, h)
	_, err := h.ticketSvc.Assign(ctx, tk.ID, &h.staffID, nil, adminActor(h))
	require.NoError(t, err)

	me := ticket.Actor{UserID: &h.staffID, Role: user.RoleStaff}
	for i := 0; i < 260; i++ { // 520 entries, past the 500 cap
		require.NoError(t, h.ticketSvc.Follow(ctx, tk.ID, me))
		require.NoError(t, h.ticketSvc.Unfollow(ctx, tk.ID, me))
	}

	adm, staff := h.session(t, asAdmin), h.session(t, asStaff)

	for name, cookie := range map[string]string{"admin": adm, "staff, setting off": staff} {
		acts := ticketAuditActions(t, h, cookie, tk.ID)
		require.Contains(t, acts, "created", name)
		require.Contains(t, acts, "assigned", name)
		if name == "staff, setting off" {
			require.Empty(t, onlyFollow(acts), name)
		}
	}

	// Real history first, then the follow entries, still oldest first overall.
	acts := ticketAuditActions(t, h, adm, tk.ID)
	require.Equal(t, "created", acts[0])
	require.LessOrEqual(t, len(onlyFollow(acts)), 500, "follow entries have a cap of their own")
	require.NotEmpty(t, onlyFollow(acts))

	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyStaffCanViewFollowAudit, true))
	acts = ticketAuditActions(t, h, staff, tk.ID)
	require.Contains(t, acts, "created")
	require.Contains(t, acts, "assigned")
	require.NotEmpty(t, onlyFollow(acts))

	// Order is by time across both kinds: the assignment came before every toggle.
	firstFollow := -1
	assigned := -1
	for i, a := range acts {
		if a == "assigned" {
			assigned = i
		}
		if firstFollow < 0 && ticket.IsFollowAuditAction(a) {
			firstFollow = i
		}
	}
	require.Less(t, assigned, firstFollow)
}
