package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// Delivering on the request made every caller wait on the mail server, and
// made guest resend, which answers identically for a match and a miss, leak
// the answer through its timing (#164). Requests now enqueue; Worker sends.

// ErrRawGuestToken refuses to queue an event carrying a guest's raw access
// token. The row encoding lives in the notification package (#506), so the
// transaction runner can write rows without importing this one.
var ErrRawGuestToken = notification.ErrRawGuestToken

func encodeEvent(ev notification.Event) ([]byte, error) { return notification.EncodeEvent(ev) }
func decodeEvent(b []byte) (notification.Event, error)  { return notification.DecodeEvent(b) }

type attemptKey struct{}

// Attempt reports which delivery attempt of a queued row this is, starting at
// 1, or 0 outside the worker. A send-time step that should happen once per
// notification rather than once per attempt — charging a budget — checks it.
func Attempt(ctx context.Context) int {
	n, _ := ctx.Value(attemptKey{}).(int)
	return n
}

type (
	rowKey         struct{}
	lastAttemptKey struct{}
)

// RowID reports the id of the queued row being delivered, or uuid.Nil outside
// the worker. It is stable across a row's retries, which is what makes it the
// webhook delivery id (#448).
func RowID(ctx context.Context) uuid.UUID {
	id, _ := ctx.Value(rowKey{}).(uuid.UUID)
	return id
}

// LastAttempt reports whether the worker will give up on the row if this
// attempt fails.
func LastAttempt(ctx context.Context) bool {
	b, _ := ctx.Value(lastAttemptKey{}).(bool)
	return b
}

// OutboxDispatcher queues one row per channel for an event and returns without
// contacting any of them. Ticket changes no longer come through it: they are
// written in the change's own transaction (txrunner, #506). It remains for the
// two events that are not changes to a ticket, the guest link resend and the
// signup verification, which have no transaction to join.
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

// Channels returns the channels Dispatch queues for: a copy, so a caller
// checking the wiring at startup (#361) cannot change what is queued.
func (d *OutboxDispatcher) Channels() []string {
	return slices.Clone(d.channels)
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
		if !notification.Carries(ch, ev) {
			continue
		}
		if err := d.store.Enqueue(ctx, uuid.New(), ch, body); err != nil {
			// Callers mostly discard this error, so it is logged here: an
			// enqueue that fails loses the notification, and "why did the
			// customer never get the email" should have an answer.
			slog.ErrorContext(ctx, "notification outbox: could not queue a notification",
				"event", ev.Type, "ticket_id", ev.TicketID, "channel", ch, "error", err)
			if first == nil {
				first = err
			}
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
	SendTimeout time.Duration // per delivery, for work that honours a context
	// ChannelOverrun is how long a channel can run past SendTimeout because it
	// keeps its own time limits instead of honouring the context.
	ChannelOverrun time.Duration
	MaxAttempts    int // then the row is failed
	Backoff        func(attempt int) time.Duration
	KeepFailed     time.Duration // failed rows are deleted after this

	wakeCh chan struct{}
}

// NewWorker returns a Worker with the defaults the server runs with.
func NewWorker(store notification.Outbox, channels map[string]notification.Dispatcher, log *slog.Logger) *Worker {
	return &Worker{
		store: store, channels: channels, log: log,
		Poll: 5 * time.Second,
		// The lease must outlast the worst a whole batch can take — every
		// row in it running to SendTimeout — or another replica claims rows
		// this worker has not reached yet and sends them twice (#164 round
		// 1: a batch of 20 against a 20-second SMTP timeout was 400 seconds
		// under a 300-second lease). A send is SendTimeout for the work that
		// honours a context (the guest-link step's database work) plus
		// ChannelOverrun for what does not (the email channel's own SMTP
		// bounds: 20 s to dial, 20 s on the connection). 4 × (1 m + 40 s)
		// fits in 10 minutes with room.
		Batch:          4,
		Lease:          10 * time.Minute,
		SendTimeout:    time.Minute,
		ChannelOverrun: 40 * time.Second,
		MaxAttempts:    8,
		Backoff:        defaultBackoff,
		KeepFailed:     30 * 24 * time.Hour,
		wakeCh:         make(chan struct{}, 1),
	}
}

// maxRetryAfter is the longest a receiver's Retry-After can delay a retry:
// the longest wait defaultBackoff ever produces.
const maxRetryAfter = time.Hour

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

// safeBatch is the largest batch whose every row can run to SendTimeout
// inside the lease, so a claimed row is never handed to another worker while
// this one still means to send it.
func (w *Worker) safeBatch() int {
	perSend := w.SendTimeout + w.ChannelOverrun
	if perSend <= 0 {
		return w.Batch
	}
	most := int(w.Lease/perSend) - 1
	return max(1, min(w.Batch, most))
}

// Run delivers until ctx is cancelled. Rows still queued stay queued.
func (w *Worker) Run(ctx context.Context) {
	if b := w.safeBatch(); b != w.Batch {
		w.log.WarnContext(ctx, "notification outbox: batch reduced to fit the lease",
			"configured", w.Batch, "used", b, "lease", w.Lease,
			"send_timeout", w.SendTimeout, "channel_overrun", w.ChannelOverrun)
		w.Batch = b
	}
	poll := time.NewTicker(w.Poll)
	defer poll.Stop()
	cleanup := time.NewTicker(time.Hour)
	defer cleanup.Stop()
	for {
		for {
			n, err := w.RunOnce(ctx)
			if err != nil {
				// A claim cut off by shutdown is not an error worth reporting.
				if ctx.Err() == nil {
					w.log.ErrorContext(ctx, "notification outbox: claim failed", "error", err)
				}
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

	// A row whose send panics is failed, not left to crash the process every
	// time its lease runs out and it is claimed again. Not retried even when
	// the panic came after a guest link was rotated: the old link is then
	// dead with no mail sent, and /resend is the guest's way back. A retry
	// would hit the same panic.
	defer func() {
		if p := recover(); p != nil {
			w.fail(settle, row, fmt.Sprintf("panic: %v", p))
		}
	}()

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

	rowCtx := context.WithValue(settle, attemptKey{}, row.Attempts)
	rowCtx = context.WithValue(rowCtx, rowKey{}, row.ID)
	rowCtx = context.WithValue(rowCtx, lastAttemptKey{}, row.Attempts >= w.MaxAttempts)
	sendCtx, cancel := context.WithTimeout(rowCtx, w.SendTimeout)
	err = d.Dispatch(sendCtx, ev)
	cancel()
	if err == nil {
		if err := w.store.Delete(settle, row.ID); err != nil {
			w.log.ErrorContext(ctx, "notification outbox: delivered but not settled; it may be sent again",
				"id", row.ID, "channel", row.Channel, "error", err)
		}
		return
	}
	// fail redacts the raw error itself, so it is redacted exactly once on
	// either path (redaction is not idempotent; see TestWorker_RedactsTheRawErrorExactlyOnce).
	//
	// A permanent error (a template that will not render) gets no retries:
	// the eighth attempt would fail exactly as the first did, an hour later.
	if errors.Is(err, ErrPermanent) || row.Attempts >= w.MaxAttempts {
		w.fail(settle, row, err.Error())
		return
	}
	delay := w.Backoff(row.Attempts)
	// A receiver that said how long to wait (Retry-After) is obeyed when that
	// is longer than the schedule, but never past the longest wait in it: a
	// header must not be able to park a row for a year.
	var ra *notification.RetryAfterError
	if errors.As(err, &ra) && ra.After > delay {
		delay = min(ra.After, maxRetryAfter)
	}
	if rerr := w.store.Retry(settle, row.ID, time.Now().Add(delay), redactAddresses(err.Error())); rerr != nil {
		w.log.ErrorContext(ctx, "notification outbox: could not reschedule", "id", row.ID, "error", rerr)
	}
}

// emailAddress matches anything shaped like an address in an error string.
// Deliberately loose: it only has to find what to hide, never validate it.
//
// The unquoted local part may contain an apostrophe (o'brien@example.com is an
// ordinary, sendable address, #389), but may not start with one, so an address
// a relay wraps in single quotes keeps both of them: '[address]'. The local
// part may also be quoted ("john doe"@example.com, escapes included):
// user.ValidateEmail refuses one that needs its quotes (#389), but a relay can
// write one itself. The domain may be an IP literal
// (guest@[192.168.1.10]), closed or not: an unterminated "@[10.0.0.1>" is
// hidden up to the ">" (#389).
//
// Linear on relay text, which the relay controls. Go's regexp is linear for one
// search, but redaction searches again after every match, so a branch that can
// scan far ahead and then fail is rescanned once per match: quadratic. That is
// why the quoted local part is bounded to 64 characters (unbounded it took about
// a minute on 96 KB; see TestRedactAddresses_IsLinear). The IP literal cannot
// fail once "@[" is seen, because its "]" is optional, so it has no such cost;
// its 255-character bound, as a domain name has, and its stop at whitespace only
// limit how much of the following text an unclosed one hides. The unquoted
// local part needs no bound: every thread in it reaches the same "@", so the
// earliest one is the match.
var emailAddress = regexp.MustCompile(`(?:"(?:[^"\\]|\\.){0,64}"|[^\s<>()\[\]@,;:"'][^\s<>()\[\]@,;:"]*)@(?:\[[^\]\s<>]{0,255}\]?|[^\s<>()\[\]@,;:"']+)`)

// redactAddresses hides email addresses in a delivery error before it is
// stored in last_error or logged (#350). A mail server's rejection usually
// names the recipient ("550 5.1.1 <guest@example.com>: Recipient address
// rejected"), and before the outbox those errors were thrown away; keeping
// them should not mean keeping a copy of the address for 30 days. The SMTP
// status, which is what an operator needs, is left intact.
func redactAddresses(s string) string {
	return emailAddress.ReplaceAllString(s, "[address]")
}

// fail gives up on a row. reason must be the raw text: fail redacts it, and
// no caller may redact it first, because redaction is applied once (#358).
func (w *Worker) fail(ctx context.Context, row notification.OutboxRow, reason string) {
	reason = redactAddresses(reason)
	w.log.ErrorContext(ctx, "notification outbox: giving up on a notification",
		"id", row.ID, "channel", row.Channel, "attempts", row.Attempts, "reason", reason)
	if err := w.store.Fail(ctx, row.ID, reason); err != nil {
		w.log.ErrorContext(ctx, "notification outbox: could not mark failed", "id", row.ID, "error", err)
	}
}

// GuestLinkDispatcher runs the send-time half of a guest link before the
// channel it wraps (#164). An event marked GuestLink is handed to prepare,
// which decides whether anyone is still to be sent a link and, if so, returns
// the event carrying a freshly created token. Wrap only the email channel:
// nothing else carries a guest link.
type GuestLinkDispatcher struct {
	next    notification.Dispatcher
	prepare func(context.Context, notification.Event) (notification.Event, bool, error)
	log     *slog.Logger
}

// NewGuestLinkDispatcher wraps next with the send-time guest link step.
func NewGuestLinkDispatcher(next notification.Dispatcher,
	prepare func(context.Context, notification.Event) (notification.Event, bool, error)) *GuestLinkDispatcher {
	return &GuestLinkDispatcher{next: next, prepare: prepare, log: slog.Default()}
}

func (d *GuestLinkDispatcher) Dispatch(ctx context.Context, ev notification.Event) error {
	if !ev.GuestLink {
		return d.next.Dispatch(ctx, ev)
	}
	sent, ok, err := d.prepare(ctx, ev)
	if err != nil {
		return err // retried by the worker
	}
	if !ok {
		// Nobody left to send a link to: a resend that matched nothing or
		// was over its budget, or a ticket deleted or without a guest
		// address since the event. Settled, not retried. Logged without the
		// recipient or what the visitor typed. An operator reading the log
		// can tell a resend miss (this line) from a match (a sent mail); the
		// log is not the channel the resend endpoint must keep quiet on.
		attrs := []any{"event", ev.Type}
		if ev.TicketID != uuid.Nil {
			attrs = append(attrs, "ticket_id", ev.TicketID)
		}
		d.log.InfoContext(ctx, "notification outbox: guest-link mail not sent; nobody to send it to", attrs...)
		return nil
	}
	return d.next.Dispatch(ctx, sent)
}

// VerificationDispatcher is the outbox channel for the account email that
// carries a link to follow: signup verification (#348) and password reset
// (#451). A signup event names a pending registration and nothing else; send
// reads the row, decides whether anyone is to be mailed, and mails the address
// and token stored on it. A reset event names only the address that was typed;
// the step wired with WithPasswordReset decides whether that address is to be
// mailed and makes the token.
type VerificationDispatcher struct {
	send  func(context.Context, uuid.UUID) error
	reset func(context.Context, notification.Event) error
}

// NewVerificationDispatcher sends through send, normally
// registration.Service.SendVerification.
func NewVerificationDispatcher(send func(context.Context, uuid.UUID) error) *VerificationDispatcher {
	return &VerificationDispatcher{send: send}
}

// WithPasswordReset sends a password reset request through reset, normally the
// server's SendPasswordReset. Without it a reset request fails rather than
// being settled as sent, so a wiring that forgets it is not silent (#451).
func (d *VerificationDispatcher) WithPasswordReset(reset func(context.Context, notification.Event) error) *VerificationDispatcher {
	d.reset = reset
	return d
}

func (d *VerificationDispatcher) Dispatch(ctx context.Context, ev notification.Event) error {
	switch ev.Type {
	case notification.EventPasswordResetRequested:
		if d.reset == nil {
			return errors.New("password reset event with no step to send it")
		}
		return d.reset(ctx, ev)
	case notification.EventRegistrationVerify:
	default:
		return nil
	}
	raw, _ := ev.Payload["pending_id"].(string)
	id, err := uuid.Parse(raw)
	if err != nil {
		return fmt.Errorf("verification event without a pending registration id: %w", err)
	}
	return d.send(ctx, id)
}
