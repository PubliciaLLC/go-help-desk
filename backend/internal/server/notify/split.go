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
	send   notification.Dispatcher // the rest of the email channel
	fan    *StaffFanout
	outbox notification.Outbox
	wake   func()
}

// NewChangeSplitter splits changes into rows queued on outbox and hands every
// other row to send. wake, if not nil, asks the worker to look at the new rows.
func NewChangeSplitter(send notification.Dispatcher, outbox notification.Outbox, wake func(),
	users StaffUsers, groups StaffGroups, followers StaffFollowers,
	overrides func(context.Context) notification.StaffOverrides, log *slog.Logger) *ChangeSplitter {
	d := &ChangeSplitter{send: send, outbox: outbox, wake: wake}
	d.fan = NewStaffFanout(&collector{}, users, groups, followers, overrides, log)
	return d
}

// collector gathers the mails a change is split into, to be queued together.
type collector struct{ got []notification.Event }

func (c *collector) Dispatch(_ context.Context, ev notification.Event) error {
	c.got = append(c.got, ev)
	return nil
}

func (d *ChangeSplitter) Dispatch(ctx context.Context, ev notification.Event) error {
	if len(ev.Kinds) == 0 {
		return d.send.Dispatch(ctx, ev)
	}
	// A collector of its own per change: the splitter serves one worker loop, but
	// nothing about it says it must.
	col := &collector{}
	fan := *d.fan
	fan.next = col
	if err := fan.Dispatch(ctx, ev); err != nil {
		return err
	}
	return d.queue(ctx, col.got)
}

// queue writes all the mails and settles the change's row in one step where the
// outbox can (the real one does, in one transaction), so a failure part way
// leaves nothing queued and the change to be tried again. Their ids derive from
// the change's row as well, so a repeat after a crash between queueing and
// settling does not queue anyone twice while their rows still wait.
func (d *ChangeSplitter) queue(ctx context.Context, mails []notification.Event) error {
	parent := RowID(ctx)
	rows := make([]notification.NewRow, 0, len(mails))
	for _, ev := range mails {
		body, err := encodeEvent(ev)
		if err != nil {
			return fmt.Errorf("encoding mail: %w", err)
		}
		id := uuid.New()
		if parent != uuid.Nil {
			id = uuid.NewSHA1(parent, childKey(ev))
		}
		rows = append(rows, notification.NewRow{ID: id, Channel: "email", Event: body})
	}
	if sp, ok := d.outbox.(notification.Splitter); ok && parent != uuid.Nil {
		if err := sp.Split(ctx, parent, rows); err != nil {
			return err
		}
	} else {
		for _, r := range rows {
			if err := d.outbox.Enqueue(ctx, r.ID, r.Channel, r.Event); err != nil {
				return err
			}
		}
	}
	if d.wake != nil && len(rows) > 0 {
		d.wake()
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
