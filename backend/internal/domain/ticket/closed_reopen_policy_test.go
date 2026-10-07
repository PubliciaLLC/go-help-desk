package ticket_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// The closed_reopen_policy setting (#349, refining "Closed is terminal"):
//
//	off         (default) nobody leaves Closed. closed_terminal_test.go runs
//	            under this default and is the "off" half of the matrix.
//	admin       an administrator may force-reopen.
//	staff_admin staff and administrators may.
//
// Requesters — reporting users, guests, and API keys or OAuth clients acting as
// one — never may, whatever the setting. The follow-up is available in every
// mode. One predicate, ticket.CanForceReopen, is asked by every route that can
// move a ticket out of Closed; the policy is read when it is used and decided
// on the locked row.

func TestCanForceReopen(t *testing.T) {
	cases := []struct {
		policy string
		role   user.Role
		want   bool
	}{
		{ticket.ReopenPolicyOff, user.RoleAdmin, false},
		{ticket.ReopenPolicyOff, user.RoleStaff, false},
		{ticket.ReopenPolicyAdmin, user.RoleAdmin, true},
		{ticket.ReopenPolicyAdmin, user.RoleStaff, false},
		{ticket.ReopenPolicyStaffAdmin, user.RoleAdmin, true},
		{ticket.ReopenPolicyStaffAdmin, user.RoleStaff, true},
		// Requesters never, in any mode.
		{ticket.ReopenPolicyOff, user.RoleUser, false},
		{ticket.ReopenPolicyAdmin, user.RoleUser, false},
		{ticket.ReopenPolicyStaffAdmin, user.RoleUser, false},
		// An unset or unrecognised value is off, never the permissive option.
		{"", user.RoleAdmin, false},
		{"Admin", user.RoleAdmin, false},
		{"everyone", user.RoleStaff, false},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, ticket.CanForceReopen(tc.policy, tc.role), "%q / %s", tc.policy, tc.role)
	}
	require.True(t, ticket.ValidClosedReopenPolicy("off"))
	require.True(t, ticket.ValidClosedReopenPolicy("admin"))
	require.True(t, ticket.ValidClosedReopenPolicy("staff_admin"))
	for _, bad := range []string{"", "Off", "staff", "all", "true"} {
		require.False(t, ticket.ValidClosedReopenPolicy(bad), bad)
	}
}

// Every route out of Closed, under every policy, for every role.
func TestForceReopen_Matrix(t *testing.T) {
	id := uuid.New()
	actors := map[string]ticket.Actor{
		"staff": {UserID: &id, Role: user.RoleStaff},
		"admin": {UserID: &id, Role: user.RoleAdmin},
		"user":  {UserID: &id, Role: user.RoleUser},
	}
	routes := map[string]func(h *harness, tk ticket.Ticket, a ticket.Actor) error{
		"Reopen": func(h *harness, tk ticket.Ticket, a ticket.Actor) error {
			_, err := h.svc.Reopen(context.Background(), tk.ID, h.newStatus.ID, a)
			return err
		},
		"status change to New": func(h *harness, tk ticket.Ticket, a ticket.Actor) error {
			_, err := h.svc.UpdateStatus(context.Background(), tk.ID, h.newStatus.ID, a)
			return err
		},
		"status change to Resolved": func(h *harness, tk ticket.Ticket, a ticket.Actor) error {
			_, err := h.svc.UpdateStatus(context.Background(), tk.ID, h.resolvedStatus.ID, a)
			return err
		},
		"Resolve": func(h *harness, tk ticket.Ticket, a ticket.Actor) error {
			_, err := h.svc.Resolve(context.Background(), tk.ID, "again", a)
			return err
		},
		"ResolveAsDuplicate": func(h *harness, tk ticket.Ticket, a ticket.Actor) error {
			other := h.seedOpen()
			_, err := h.svc.ResolveAsDuplicate(context.Background(), tk.ID, other.ID, "dup", a)
			return err
		},
	}

	for _, policy := range []string{"", ticket.ReopenPolicyOff, ticket.ReopenPolicyAdmin, ticket.ReopenPolicyStaffAdmin, "bogus"} {
		for aname, actor := range actors {
			for rname, route := range routes {
				t.Run(policy+"/"+aname+"/"+rname, func(t *testing.T) {
					h := newHarness(t)
					h.policy = policy
					tk := h.seedClosed()

					err := route(h, tk, actor)

					stored, getErr := h.store.GetByID(context.Background(), tk.ID)
					require.NoError(t, getErr)
					if actor.Role == user.RoleUser {
						// Never, whatever the setting, and with the refusal a
						// requester has always had: nothing about the policy.
						require.ErrorIs(t, err, ticket.ErrForbidden)
						require.Equal(t, h.closedStatus.ID, stored.StatusID)
						return
					}
					if ticket.CanForceReopen(policy, actor.Role) {
						require.NoError(t, err)
						require.NotEqual(t, h.closedStatus.ID, stored.StatusID, "it was reopened")
						require.Nil(t, stored.ClosedAt)
						return
					}
					require.ErrorIs(t, err, ticket.ErrClosed, "refused as a closed ticket is")
					var refused *ticket.ReopenRefusedError
					require.ErrorAs(t, err, &refused)
					require.Contains(t, err.Error(), "reopening closed tickets is")
					require.Contains(t, err.Error(), "follow-up", "and where to go instead")
					require.Equal(t, h.closedStatus.ID, stored.StatusID)
					require.NotNil(t, stored.ClosedAt)
					require.Zero(t, h.store.updates, "nothing may be written")
				})
			}
		}
	}
}

// What a staff member is told depends on why: disabled, or restricted to
// administrators. A requester is never told either.
func TestForceReopen_RefusalSaysWhy(t *testing.T) {
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}

	h := newHarness(t)
	tk := h.seedClosed()
	_, err := h.svc.Reopen(context.Background(), tk.ID, h.newStatus.ID, staff)
	require.ErrorContains(t, err, "disabled")

	h.policy = ticket.ReopenPolicyAdmin
	_, err = h.svc.Reopen(context.Background(), tk.ID, h.newStatus.ID, staff)
	require.ErrorContains(t, err, "restricted to administrators")

	reporter := ticket.Actor{UserID: tk.ReporterUserID, Role: user.RoleUser}
	_, err = h.svc.Reopen(context.Background(), tk.ID, h.newStatus.ID, reporter)
	require.ErrorIs(t, err, ticket.ErrForbidden)
	require.NotContains(t, err.Error(), "reopening closed tickets")
}

// A service nobody wired a policy into is off: the default, not an open door.
func TestForceReopen_AnUnwiredPolicyIsOff(t *testing.T) {
	h := newHarness(t)
	h.svc.SetClosedReopenPolicy(nil)
	admin := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleAdmin}
	tk := h.seedClosed()

	_, err := h.svc.Reopen(context.Background(), tk.ID, h.newStatus.ID, admin)
	require.ErrorIs(t, err, ticket.ErrClosed)
	_, err = h.svc.UpdateStatus(context.Background(), tk.ID, h.newStatus.ID, admin)
	require.ErrorIs(t, err, ticket.ErrClosed)
}

// The policy is read when it is used. No cache: flipping the setting changes
// the very next call.
func TestForceReopen_TakesEffectOnTheNextRequest(t *testing.T) {
	h := newHarness(t)
	admin := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleAdmin}
	tk := h.seedClosed()

	_, err := h.svc.Reopen(context.Background(), tk.ID, h.newStatus.ID, admin)
	require.ErrorIs(t, err, ticket.ErrClosed, "default off")

	h.policy = ticket.ReopenPolicyAdmin
	_, err = h.svc.Reopen(context.Background(), tk.ID, h.newStatus.ID, admin)
	require.NoError(t, err, "on, for the next request")

	require.NoError(t, h.svc.Close(context.Background(), tk.ID, admin))
	h.policy = ticket.ReopenPolicyOff
	_, err = h.svc.Reopen(context.Background(), tk.ID, h.newStatus.ID, admin)
	require.ErrorIs(t, err, ticket.ErrClosed, "and off again")
}

// The race: the policy is read AFTER the ticket's row lock is taken, so a
// setting flipped to off between the first, unlocked read and the lock is seen.
// The store hook flips it at the moment the lock is requested.
func TestForceReopen_APolicyFlippedToOffBeforeTheLockIsRefused(t *testing.T) {
	admin := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleAdmin}
	routes := map[string]func(h *harness, id uuid.UUID) error{
		"Reopen": func(h *harness, id uuid.UUID) error {
			_, err := h.svc.Reopen(context.Background(), id, h.newStatus.ID, admin)
			return err
		},
		"UpdateStatus": func(h *harness, id uuid.UUID) error {
			_, err := h.svc.UpdateStatus(context.Background(), id, h.newStatus.ID, admin)
			return err
		},
		"Resolve": func(h *harness, id uuid.UUID) error {
			_, err := h.svc.Resolve(context.Background(), id, "n", admin)
			return err
		},
	}
	for name, route := range routes {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.policy = ticket.ReopenPolicyStaffAdmin
			tk := h.seedClosed()
			h.store.forUpdateReads = 0
			h.store.onRead = func(x ticket.Ticket) ticket.Ticket {
				if h.store.forUpdateReads > 0 {
					h.policy = ticket.ReopenPolicyOff // flipped as the lock is taken
				}
				return x
			}

			err := route(h, tk.ID)

			require.ErrorIs(t, err, ticket.ErrClosed)
			stored, _ := h.store.GetByID(context.Background(), tk.ID)
			require.Equal(t, h.closedStatus.ID, stored.StatusID)
			require.Zero(t, h.store.updates)
		})
	}
}

// A force-reopen is recorded as one: a status-history row, and an audit entry
// that says so and names the policy in force. Its side effects are the old
// Reopen's and nothing more: the reopen notification, the guest's link issued
// at send time, SLA state carried by the shared timestamp rule.
func TestForceReopen_IsRecordedAsForced(t *testing.T) {
	h := newHarness(t)
	h.policy = ticket.ReopenPolicyStaffAdmin
	staffID := uuid.New()
	staff := ticket.Actor{UserID: &staffID, Role: user.RoleStaff}
	tk, held := guestTicket(t, h)
	require.NoError(t, h.svc.Close(context.Background(), tk.ID, staff))
	require.Equal(t, 1, h.store.guestTokenCount(tk.ID), "closing left the guest's link alone")
	h.dispatcher.events = nil
	auditBefore := len(h.auditStore.entries)

	got, err := h.svc.Reopen(context.Background(), tk.ID, h.newStatus.ID, staff)
	require.NoError(t, err)
	require.Equal(t, h.newStatus.ID, got.StatusID)
	require.Nil(t, got.ClosedAt)

	hist := historyFor(h, tk.ID)
	last := hist[len(hist)-1]
	require.Equal(t, &h.closedStatus.ID, last.FromStatusID)
	require.Equal(t, h.newStatus.ID, last.ToStatusID)
	require.Equal(t, &staffID, last.ChangedByUserID)

	require.Greater(t, len(h.auditStore.entries), auditBefore)
	e := h.auditStore.entries[len(h.auditStore.entries)-1]
	require.Equal(t, "reopened", e.Action)
	require.Equal(t, &staffID, e.ActorID)
	require.Equal(t, true, e.After["forced_reopen"])
	require.Equal(t, ticket.ReopenPolicyStaffAdmin, e.After["closed_reopen_policy"])

	ev := lastEventOfType(t, h, notification.EventTicketReopened)
	require.Equal(t, "guest@example.test", ev.Recipient)
	require.NotEmpty(t, ev.GuestToken, "the reopen mail carries a link, as Reopen always did")

	// And it is an ordinary open ticket again: the guest writes.
	_, err = h.svc.TicketForGuestWrite(context.Background(), ev.GuestToken)
	require.NoError(t, err)
	_, err = h.svc.AddGuestReply(context.Background(), tk.ID, "thanks", 7, h.newStatus.ID)
	require.NoError(t, err)
	_ = held
}

// The forced flag rides on the other two doors' audit entries too.
func TestForceReopen_StatusChangeAndResolveAreAuditedAsForced(t *testing.T) {
	admin := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleAdmin}
	for name, route := range map[string]func(h *harness, id uuid.UUID) error{
		"UpdateStatus": func(h *harness, id uuid.UUID) error {
			_, err := h.svc.UpdateStatus(context.Background(), id, h.newStatus.ID, admin)
			return err
		},
		"Resolve": func(h *harness, id uuid.UUID) error {
			_, err := h.svc.Resolve(context.Background(), id, "n", admin)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.policy = ticket.ReopenPolicyAdmin
			tk := h.seedClosed()
			require.NoError(t, route(h, tk.ID))
			e := h.auditStore.entries[len(h.auditStore.entries)-1]
			require.Equal(t, true, e.After["forced_reopen"])
			require.Equal(t, ticket.ReopenPolicyAdmin, e.After["closed_reopen_policy"])
		})
	}

	t.Run("an ordinary status change is not flagged", func(t *testing.T) {
		h := newHarness(t)
		tk := h.seedOpen()
		_, err := h.svc.UpdateStatus(context.Background(), tk.ID, h.resolvedStatus.ID, admin)
		require.NoError(t, err)
		e := h.auditStore.entries[len(h.auditStore.entries)-1]
		require.NotContains(t, e.After, "forced_reopen")
	})
}

func TestForceReopen_Preconditions(t *testing.T) {
	admin := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleAdmin}
	ctx := context.Background()

	t.Run("only a closed ticket", func(t *testing.T) {
		h := newHarness(t)
		h.policy = ticket.ReopenPolicyStaffAdmin
		for _, tk := range []ticket.Ticket{h.seedOpen(), h.seedResolved(uuid.New())} {
			_, err := h.svc.Reopen(ctx, tk.ID, h.newStatus.ID, admin)
			require.ErrorIs(t, err, ticket.ErrNotClosed)
		}
		require.Zero(t, h.store.updates)
	})
	t.Run("an unusable target is refused before writing", func(t *testing.T) {
		h := newHarness(t)
		h.policy = ticket.ReopenPolicyStaffAdmin
		tk := h.seedClosed()
		_, err := h.svc.Reopen(ctx, tk.ID, uuid.Nil, admin)
		require.ErrorIs(t, err, ticket.ErrValidation)
		require.Zero(t, h.store.updates)
	})
	t.Run("a missing ticket", func(t *testing.T) {
		h := newHarness(t)
		h.policy = ticket.ReopenPolicyStaffAdmin
		_, err := h.svc.Reopen(ctx, uuid.New(), h.newStatus.ID, admin)
		require.ErrorIs(t, err, errNotFound)
	})
	t.Run("a ticket closed by a concurrent writer's reopen is not closed any more", func(t *testing.T) {
		h := newHarness(t)
		h.policy = ticket.ReopenPolicyStaffAdmin
		tk := h.seedClosed()
		h.store.forUpdateReads = 0
		h.store.onRead = func(x ticket.Ticket) ticket.Ticket {
			if x.ID == tk.ID && h.store.forUpdateReads > 0 {
				x.StatusID = h.newStatus.ID
			}
			return x
		}
		_, err := h.svc.Reopen(ctx, tk.ID, h.newStatus.ID, admin)
		require.ErrorIs(t, err, ticket.ErrNotClosed)
		require.Zero(t, h.store.updates)
	})
}

// The follow-up is available whatever the policy: it is the way forward when
// reopening is off, and does not stop being one when it is on.
func TestForceReopen_FollowUpIsAvailableInEveryMode(t *testing.T) {
	for _, policy := range []string{"", ticket.ReopenPolicyOff, ticket.ReopenPolicyAdmin, ticket.ReopenPolicyStaffAdmin} {
		h := newHarness(t)
		h.policy = policy
		orig := h.seedClosed()
		for _, role := range []user.Role{user.RoleStaff, user.RoleAdmin} {
			_, err := h.svc.CreateFollowUp(context.Background(), orig.ID, "GHD", ticket.Actor{UserID: ptr(uuid.New()), Role: role})
			require.NoError(t, err, "%q / %s", policy, role)
		}
	}
}

// A requester's own paths are untouched by the setting: still read-only on a
// closed ticket in every mode.
func TestForceReopen_RequestersAreReadOnlyInEveryMode(t *testing.T) {
	for _, policy := range []string{"", ticket.ReopenPolicyAdmin, ticket.ReopenPolicyStaffAdmin} {
		h := newHarness(t)
		h.policy = policy
		tk := h.seedClosed()
		reporter := ticket.Actor{UserID: tk.ReporterUserID, Role: user.RoleUser}

		_, err := h.svc.AddReply(context.Background(), tk.ID, "x", false, false, "", reporter, 7, h.newStatus.ID)
		require.ErrorIs(t, err, ticket.ErrClosed, policy)
		require.ErrorIs(t, h.svc.CanRequesterWrite(context.Background(), tk.ID, reporter), ticket.ErrClosed, policy)
		require.ErrorIs(t, h.svc.CreateAttachment(context.Background(),
			ticket.Attachment{ID: uuid.New(), TicketID: tk.ID}, reporter), ticket.ErrClosed, policy)
	}
}
