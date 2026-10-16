package notify

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// ChangeSplitter is the first stop of the email channel in the worker (#506).
//
// A ticket change is queued as ONE email row, written in the change's own
// transaction. Who is mailed is decided here, when the worker first takes that
// row: the requester's mail and each staff member's become rows of their own,
// and the change's row is settled. Each person's mail then retries on its own.
//
// Rows that do not carry Kinds are not changes in the new shape: a row queued
// before #506 has already been split on the request, and a row this splitter
// made is a single mail. They go straight on to send.
type ChangeSplitter struct {
	send notification.Dispatcher // the rest of the email channel
	fan  *StaffFanout
}

// NewChangeSplitter splits changes into rows queued on outbox and hands every
// other row to send. wake, if not nil, asks the worker to look at the new rows.
func NewChangeSplitter(send notification.Dispatcher, outbox notification.Outbox, wake func(),
	users StaffUsers, groups StaffGroups, followers StaffFollowers,
	overrides func(context.Context) notification.StaffOverrides, log *slog.Logger) *ChangeSplitter {
	queue := &childQueue{outbox: outbox, wake: wake}
	return &ChangeSplitter{send: send, fan: NewStaffFanout(queue, users, groups, followers, overrides, log)}
}

func (d *ChangeSplitter) Dispatch(ctx context.Context, ev notification.Event) error {
	if len(ev.Kinds) == 0 {
		return d.send.Dispatch(ctx, ev)
	}
	return d.fan.Dispatch(ctx, ev)
}

// childQueue queues the rows a change is split into.
//
// Their ids derive from the change's row, and the outbox ignores a repeated id,
// so a crash between queueing and settling the change's row, which has the
// worker split it again, does not queue anyone twice. A mail already sent and
// deleted would be queued again, as for the webhook fan-out (#448): a rare,
// accepted duplicate, and email has no delivery id to drop it by.
type childQueue struct {
	outbox notification.Outbox
	wake   func()
}

func (q *childQueue) Dispatch(ctx context.Context, ev notification.Event) error {
	body, err := encodeEvent(ev)
	if err != nil {
		return fmt.Errorf("encoding mail: %w", err)
	}
	id := uuid.New()
	if parent := RowID(ctx); parent != uuid.Nil {
		id = uuid.NewSHA1(parent, childKey(ev))
	}
	if err := q.outbox.Enqueue(ctx, id, "email", body); err != nil {
		return err
	}
	if q.wake != nil {
		q.wake()
	}
	return nil
}

// childKey names a child within its parent: one requester mail, and one mail
// per staff member.
func childKey(ev notification.Event) []byte {
	if ev.StaffUserID != nil {
		return append([]byte("staff:"), ev.StaffUserID[:]...)
	}
	return []byte("requester")
}
