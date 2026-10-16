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

// An assignment that changes nothing announces nothing, as a same-status or
// same-priority change does (#506 ruling 13). A real change still announces.
//
// The audit entry is still written for a no-op, as it is for a same-status
// move; only the announcement (and so the mail and the webhooks) is dropped.
func TestAssign_OnlyAChangeAnnounces(t *testing.T) {
	user1, user2 := uuid.New(), uuid.New()
	group1, group2 := uuid.New(), uuid.New()
	cases := []struct {
		name             string
		heldUser, heldGp *uuid.UUID
		toUser, toGp     *uuid.UUID
		announces        bool
	}{
		{"same assignee", &user1, nil, &user1, nil, false},
		{"same assignee and group", &user1, &group1, &user1, &group1, false},
		{"same group", nil, &group1, nil, &group1, false},
		{"clear on an unassigned ticket", nil, nil, nil, nil, false},
		{"new assignee", &user1, nil, &user2, nil, true},
		{"first assignee", nil, nil, &user1, nil, true},
		{"new group", nil, &group1, nil, &group2, true},
		{"group added", &user1, nil, &user1, &group1, true},
		{"clear on an assigned ticket", &user1, nil, nil, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			seeded := h.seedOpen()
			seeded.AssigneeUserID, seeded.AssigneeGroupID = tc.heldUser, tc.heldGp
			h.store.seed(seeded)
			actor := uuid.New()

			_, err := h.svc.Assign(context.Background(), seeded.ID, tc.toUser, tc.toGp,
				ticket.Actor{UserID: &actor, Role: user.RoleStaff})
			require.NoError(t, err)

			if tc.announces {
				require.Contains(t, h.dispatcher.types(), notification.EventTicketAssigned)
			} else {
				require.Empty(t, h.dispatcher.events, "nothing changed, so nothing is announced")
			}
			require.Len(t, h.auditStore.entries, 1, "the request is still audited, as a same-status move is")
		})
	}
}
