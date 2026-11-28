package testutil

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

type failingSink struct{ err error }

func (s failingSink) Dispatch(context.Context, notification.Event) error { return s.err }

// The production runner writes each event to the outbox inside the transaction,
// so an outbox that cannot take the row fails the change. The stand-in used to
// drop that error, which let every HTTP test pass over an announcement that had
// failed, including the staff mail the test was about.
func TestJoiningTxRunner_ASinkErrorFailsTheChange(t *testing.T) {
	boom := errors.New("sink is down")
	r := NewJoiningTxRunner(nil, failingSink{boom})

	err := r.InTx(context.Background(), func(_ ticket.Store, _ audit.Store, ch *notification.Changes) error {
		ch.Raise(notification.Event{Type: notification.EventTicketCreated, TicketID: uuid.New()})
		return nil
	})

	if !errors.Is(err, boom) {
		t.Fatalf("InTx = %v, want the sink's error", err)
	}
}

func TestJoiningTxRunner_NoSinkAndNoEventsAreNotErrors(t *testing.T) {
	raise := func(_ ticket.Store, _ audit.Store, ch *notification.Changes) error {
		ch.Raise(notification.Event{Type: notification.EventTicketCreated, TicketID: uuid.New()})
		return nil
	}
	if err := NewJoiningTxRunner(nil, nil).InTx(context.Background(), raise); err != nil {
		t.Fatalf("no sink: %v", err)
	}
	none := func(ticket.Store, audit.Store, *notification.Changes) error { return nil }
	if err := NewJoiningTxRunner(nil, failingSink{errors.New("never called")}).InTx(context.Background(), none); err != nil {
		t.Fatalf("no events: %v", err)
	}
}
