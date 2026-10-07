package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// Delivering on the request made every caller wait on the mail server, and
// made guest resend, which answers identically for a match and a miss, leak
// the answer through its timing (#164). Requests now enqueue; Worker sends.

// ErrRawGuestToken refuses to queue an event carrying a guest's raw access
// token. The guest token table holds only hashes; a copy in the outbox would
// undo that. Guest links are created by the worker at send time instead.
var ErrRawGuestToken = errors.New("refusing to queue a raw guest token")

// outboxRecord is how an event is stored. Spelled out field by field rather
// than json.Marshal(event), because Event's own JSON is the webhook body and
// deliberately omits the email-only fields this needs.
//
// GuestToken is absent on purpose. TestOutboxRecord_CoversEveryEventField
// fails when a field is added to Event without a decision here.
type outboxRecord struct {
	Type           notification.EventType `json:"type"`
	TicketID       uuid.UUID              `json:"ticket_id"`
	ActorID        *uuid.UUID             `json:"actor_id,omitempty"`
	Payload        map[string]any         `json:"payload,omitempty"`
	OccurredAt     time.Time              `json:"occurred_at"`
	TrackingNumber string                 `json:"tracking_number,omitempty"`
	Recipient      string                 `json:"recipient,omitempty"`
	Subject        string                 `json:"subject,omitempty"`
	StatusName     string                 `json:"status_name,omitempty"`
}

func encodeEvent(ev notification.Event) ([]byte, error) {
	if ev.GuestToken != "" {
		return nil, ErrRawGuestToken
	}
	return json.Marshal(outboxRecord{
		Type: ev.Type, TicketID: ev.TicketID, ActorID: ev.ActorID, Payload: ev.Payload,
		OccurredAt: ev.OccurredAt, TrackingNumber: ev.TrackingNumber, Recipient: ev.Recipient,
		Subject: ev.Subject, StatusName: ev.StatusName,
	})
}

func decodeEvent(b []byte) (notification.Event, error) {
	var r outboxRecord
	if err := json.Unmarshal(b, &r); err != nil {
		return notification.Event{}, err
	}
	return notification.Event{
		Type: r.Type, TicketID: r.TicketID, ActorID: r.ActorID, Payload: r.Payload,
		OccurredAt: r.OccurredAt, TrackingNumber: r.TrackingNumber, Recipient: r.Recipient,
		Subject: r.Subject, StatusName: r.StatusName,
	}, nil
}

// OutboxDispatcher is the Dispatcher requests call: it queues one row per
// channel and returns without contacting any of them.
type OutboxDispatcher struct {
	store    notification.Outbox
	channels []string
	wake     func()
}

// NewOutboxDispatcher queues events for the named channels. wake, if not nil,
// is called after queueing so a worker in this process need not wait for its
// next poll.
func NewOutboxDispatcher(store notification.Outbox, channels []string, wake func()) *OutboxDispatcher {
	return &OutboxDispatcher{store: store, channels: channels, wake: wake}
}

func (d *OutboxDispatcher) Dispatch(ctx context.Context, ev notification.Event) error {
	body, err := encodeEvent(ev)
	if err != nil {
		return err
	}
	// Detached: the change this announces has already committed, so a client
	// hanging up must not lose the notification.
	ctx = context.WithoutCancel(ctx)
	var first error
	for _, ch := range d.channels {
		if err := d.store.Enqueue(ctx, uuid.New(), ch, body); err != nil && first == nil {
			first = err
		}
	}
	if d.wake != nil {
		d.wake()
	}
	return first
}

// Worker delivers queued rows to the channel each names.
type Worker struct {
	store    notification.Outbox
	channels map[string]notification.Dispatcher
	log      *slog.Logger

	Poll        time.Duration // how often to look when nobody wakes it
	Batch       int           // rows per claim
	Lease       time.Duration // how long a claimed row is this worker's
	SendTimeout time.Duration // per delivery
	MaxAttempts int           // then the row is failed
	Backoff     func(attempt int) time.Duration
	KeepFailed  time.Duration // failed rows are deleted after this

	wakeCh chan struct{}
}

// NewWorker returns a Worker with the defaults the server runs with.
func NewWorker(store notification.Outbox, channels map[string]notification.Dispatcher, log *slog.Logger) *Worker {
	return &Worker{
		store: store, channels: channels, log: log,
		Poll:        5 * time.Second,
		Batch:       20,
		Lease:       5 * time.Minute,
		SendTimeout: time.Minute,
		MaxAttempts: 8,
		Backoff:     defaultBackoff,
		KeepFailed:  30 * 24 * time.Hour,
		wakeCh:      make(chan struct{}, 1),
	}
}

// defaultBackoff doubles from 30 seconds, capped at an hour: eight attempts
// span a little over an hour, long enough to ride out a mail server restart.
func defaultBackoff(attempt int) time.Duration {
	d := 30 * time.Second
	for i := 1; i < attempt && d < time.Hour; i++ {
		d *= 2
	}
	return min(d, time.Hour)
}

// Wake asks the worker to look now. It never blocks.
func (w *Worker) Wake() {
	select {
	case w.wakeCh <- struct{}{}:
	default:
	}
}

// Run delivers until ctx is cancelled. Rows still queued stay queued.
func (w *Worker) Run(ctx context.Context) {
	poll := time.NewTicker(w.Poll)
	defer poll.Stop()
	cleanup := time.NewTicker(time.Hour)
	defer cleanup.Stop()
	for {
		for {
			n, err := w.RunOnce(ctx)
			if err != nil {
				w.log.ErrorContext(ctx, "notification outbox: claim failed", "error", err)
				break
			}
			if n < w.Batch {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-w.wakeCh:
		case <-poll.C:
		case <-cleanup.C:
			if n, err := w.store.DeleteFailedBefore(ctx, time.Now().Add(-w.KeepFailed)); err != nil {
				w.log.ErrorContext(ctx, "notification outbox: cleanup failed", "error", err)
			} else if n > 0 {
				w.log.InfoContext(ctx, "notification outbox: deleted old failed rows", "count", n)
			}
		}
	}
}

// RunOnce claims one batch and settles every row in it. It reports how many
// rows it claimed.
func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	rows, err := w.store.Claim(ctx, w.Batch, w.Lease)
	if err != nil {
		return 0, err
	}
	for _, row := range rows {
		w.deliver(ctx, row)
	}
	return len(rows), nil
}

func (w *Worker) deliver(ctx context.Context, row notification.OutboxRow) {
	// Settling runs detached: a shutdown mid-batch must not leave a delivered
	// row unsettled, which would send it again after the lease.
	settle := context.WithoutCancel(ctx)

	d, ok := w.channels[row.Channel]
	if !ok {
		w.fail(settle, row, fmt.Sprintf("unknown channel %q", row.Channel))
		return
	}
	ev, err := decodeEvent(row.Event)
	if err != nil {
		w.fail(settle, row, "undecodable event: "+err.Error())
		return
	}

	sendCtx, cancel := context.WithTimeout(settle, w.SendTimeout)
	err = d.Dispatch(sendCtx, ev)
	cancel()
	if err == nil {
		if err := w.store.Delete(settle, row.ID); err != nil {
			w.log.ErrorContext(ctx, "notification outbox: delivered but not settled; it may be sent again",
				"id", row.ID, "channel", row.Channel, "error", err)
		}
		return
	}
	if row.Attempts >= w.MaxAttempts {
		w.fail(settle, row, err.Error())
		return
	}
	if rerr := w.store.Retry(settle, row.ID, time.Now().Add(w.Backoff(row.Attempts)), err.Error()); rerr != nil {
		w.log.ErrorContext(ctx, "notification outbox: could not reschedule", "id", row.ID, "error", rerr)
	}
}

func (w *Worker) fail(ctx context.Context, row notification.OutboxRow, reason string) {
	w.log.ErrorContext(ctx, "notification outbox: giving up on a notification",
		"id", row.ID, "channel", row.Channel, "attempts", row.Attempts, "reason", reason)
	if err := w.store.Fail(ctx, row.ID, reason); err != nil {
		w.log.ErrorContext(ctx, "notification outbox: could not mark failed", "id", row.ID, "error", err)
	}
}
