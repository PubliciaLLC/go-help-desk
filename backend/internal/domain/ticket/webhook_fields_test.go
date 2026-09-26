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

// Subject and StatusName are json:"-" fields on notification.Event, read only
// by the webhook chat/ITSM renderers (internal/server/notify) — never
// serialised, so populating them cannot move the raw wire shape (pinned in
// notification_test.go). This file is the service-side half: it asserts the
// eight lifecycle doors actually set them, using the real service against the
// in-memory fakes, exactly as notification_source_test.go and
// internal_note_event_test.go already do for the fields those pin.
func eventOfType(t *testing.T, events []notification.Event, ty notification.EventType) notification.Event {
	t.Helper()
	for _, e := range events {
		if e.Type == ty {
			return e
		}
	}
	t.Fatalf("no %s event dispatched", ty)
	return notification.Event{}
}

func TestUpdateStatus_DispatchesSubjectAndStatusName(t *testing.T) {
	h := newHarness(t)
	seeded := h.seedOpen()
	agent := uuid.New()

	_, err := h.svc.UpdateStatus(context.Background(), seeded.ID, h.inProgressStatus.ID,
		ticket.Actor{UserID: &agent, Role: user.RoleStaff})
	require.NoError(t, err)

	ev := eventOfType(t, h.dispatcher.events, notification.EventTicketStatusChanged)
	require.Equal(t, seeded.Subject, ev.Subject)
	require.Equal(t, h.inProgressStatus.Name, ev.StatusName)
}

func TestAssign_DispatchesSubjectAndTrackingNumber(t *testing.T) {
	h := newHarness(t)
	seeded := h.seedOpen()
	agent := uuid.New()
	assignee := uuid.New()

	_, err := h.svc.Assign(context.Background(), seeded.ID, &assignee, nil,
		ticket.Actor{UserID: &agent, Role: user.RoleStaff})
	require.NoError(t, err)

	ev := eventOfType(t, h.dispatcher.events, notification.EventTicketAssigned)
	require.Equal(t, seeded.Subject, ev.Subject)
	require.Equal(t, string(seeded.TrackingNumber), ev.TrackingNumber,
		"Assign was one of the three sites that dispatched no tracking number at all")
}

func TestAddReply_DispatchesSubject(t *testing.T) {
	h := newHarness(t)
	seeded := h.seedOpen()
	agent := uuid.New()

	_, err := h.svc.AddReply(context.Background(), seeded.ID, "the reply", false, false,
		"reporter@example.com",
		ticket.Actor{UserID: &agent, Role: user.RoleStaff},
		30, h.newStatus.ID)
	require.NoError(t, err)

	ev := eventOfType(t, h.dispatcher.events, notification.EventTicketReplied)
	require.Equal(t, seeded.Subject, ev.Subject)
}

func TestResolve_DispatchesSubject(t *testing.T) {
	h := newHarness(t)
	seeded := h.seedOpen()
	agent := uuid.New()

	_, err := h.svc.Resolve(context.Background(), seeded.ID, "fixed",
		ticket.Actor{UserID: &agent, Role: user.RoleStaff})
	require.NoError(t, err)

	ev := eventOfType(t, h.dispatcher.events, notification.EventTicketResolved)
	require.Equal(t, seeded.Subject, ev.Subject)
}

func TestClose_DispatchesSubjectAndTrackingNumber(t *testing.T) {
	h := newHarness(t)
	seeded := h.seedOpen()
	agent := uuid.New()

	err := h.svc.Close(context.Background(), seeded.ID, ticket.Actor{UserID: &agent, Role: user.RoleAdmin})
	require.NoError(t, err)

	ev := eventOfType(t, h.dispatcher.events, notification.EventTicketClosed)
	require.Equal(t, seeded.Subject, ev.Subject)
	require.Equal(t, string(seeded.TrackingNumber), ev.TrackingNumber,
		"Close was one of the three sites that dispatched no tracking number at all")
}

func TestReopen_DispatchesSubject(t *testing.T) {
	h := newHarness(t)
	seeded := h.seedOpen()
	agent := uuid.New()
	require.NoError(t, h.svc.Close(context.Background(), seeded.ID, ticket.Actor{UserID: &agent, Role: user.RoleAdmin}))
	h.dispatcher.events = nil

	_, err := h.svc.Reopen(context.Background(), seeded.ID, h.newStatus.ID,
		ticket.Actor{UserID: &agent, Role: user.RoleAdmin})
	require.NoError(t, err)

	ev := eventOfType(t, h.dispatcher.events, notification.EventTicketReopened)
	require.Equal(t, seeded.Subject, ev.Subject)
}

func TestAddLink_DispatchesSubjectAndTrackingNumber(t *testing.T) {
	h := newHarness(t)
	source := h.seedOpen()
	target := h.seedOpen()
	agent := uuid.New()

	err := h.svc.AddLink(context.Background(), source.ID, target.ID, ticket.LinkRelatedTo,
		ticket.Actor{UserID: &agent, Role: user.RoleStaff})
	require.NoError(t, err)

	ev := eventOfType(t, h.dispatcher.events, notification.EventTicketLinked)
	require.Equal(t, source.Subject, ev.Subject)
	require.Equal(t, string(source.TrackingNumber), ev.TrackingNumber,
		"Link was one of the three sites that dispatched no tracking number at all")
}
