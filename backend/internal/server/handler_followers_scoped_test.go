package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// The scoped statements (staff with ticket scope enforced) carry the same
// exclusion of follow entries as the unscoped ones (#441).
func TestFollowAudit_AdminWideViewByRole_ScopeEnforced(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk := newStaffTicket(t, h)
	_, err := h.ticketSvc.Assign(ctx, tk.ID, &h.staffID, nil, adminActor(h))
	require.NoError(t, err)
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyTicketScopeEnforced, true))
	staff := h.session(t, asStaff)
	require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, staff, http.MethodPut, followPath(tk.ID), nil)))

	acts, total := adminAuditActions(t, h, staff)
	require.NotEmpty(t, acts, "the ticket is in scope, so its other entries show")
	require.Empty(t, onlyFollow(acts))
	require.Equal(t, len(acts), total)

	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyStaffCanViewFollowAudit, true))
	acts, total = adminAuditActions(t, h, staff)
	require.Equal(t, []string{"followed"}, onlyFollow(acts))
	require.Equal(t, len(acts), total)
}
