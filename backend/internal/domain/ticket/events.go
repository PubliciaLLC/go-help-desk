package ticket

import (
	"time"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// What a transaction raises into its notification.Changes (#506). Each piece of
// a change raises its own kinds and field changes, and the collector merges the
// pieces for a ticket into one event. A door that does several things
// therefore needs no code for each combination: it calls the pieces inside one
// transaction.

// stateOf is the ticket as an event describes it: the classification a webhook
// filter tests, who holds it for staff mail, and its status. Taken from the row
// the transaction wrote, at the moment of the raise; the last raise of a
// transaction is the ticket as it commits.
func stateOf(t Ticket) *notification.TicketState {
	return &notification.TicketState{
		StatusID:        t.StatusID,
		AssigneeUserID:  t.AssigneeUserID,
		AssigneeGroupID: t.AssigneeGroupID,
		Priority:        string(t.Priority),
		CategoryID:      t.CategoryID,
		TypeID:          t.TypeID,
		ItemID:          t.ItemID,
		ReopenCount:     t.ReopenCount,
	}
}

// raiseOn is an event's common part for ticket t: who did it, when, and the
// values every consumer reads off the persisted row rather than out of request
// text (see notification.Event.TrackingNumber). The caller adds its kinds,
// changes and payload and raises it.
func raiseOn(t Ticket, actor Actor, at time.Time) notification.Event {
	return notification.Event{
		TicketID:       t.ID,
		ActorID:        actor.UserID,
		OccurredAt:     at,
		TrackingNumber: string(t.TrackingNumber),
		Subject:        t.Subject,
		Ticket:         stateOf(t),
	}
}

// statusKinds are the kinds of a move from one status to another. Every status
// move carries ticket.status_changed; a move into Resolved or Closed also
// carries ticket.resolved or ticket.closed, and a move out of either into an
// open status also carries ticket.reopened. The same for every door, so the
// button that made the change no longer decides what a subscriber is told.
//
// A move to the status the ticket already has is not a move: nothing about
// status is announced (CTO ruling 13), whichever door repeats it. The one
// exception, resolving an already-Resolved ticket with new notes, is the
// Resolve button's own and is decided in resolveInTx.
func (s *Service) statusKinds(oldStatusID, newStatusID uuid.UUID) []notification.EventType {
	if oldStatusID == newStatusID {
		return nil
	}
	kinds := []notification.EventType{notification.EventTicketStatusChanged}
	switch newStatusID {
	case s.sys.resolvedID:
		kinds = append(kinds, notification.EventTicketResolved)
	case s.sys.closedID:
		kinds = append(kinds, notification.EventTicketClosed)
	}
	if s.isReopen(oldStatusID, newStatusID) {
		kinds = append(kinds, notification.EventTicketReopened)
	}
	return kinds
}

// statusChanges is the status_id change of a move, or none for a move to the
// status the ticket already has.
func statusChanges(oldStatusID, newStatusID uuid.UUID) []notification.Change {
	if oldStatusID == newStatusID {
		return nil
	}
	return []notification.Change{notification.ValueChange("status_id", oldStatusID.String(), newStatusID.String())}
}

// optIDChange is a nullable-id field's change, or none when it did not move.
func optIDChange(field string, from, to *uuid.UUID) []notification.Change {
	if sameID(from, to) {
		return nil
	}
	return []notification.Change{notification.IDChange(field, from, to)}
}
