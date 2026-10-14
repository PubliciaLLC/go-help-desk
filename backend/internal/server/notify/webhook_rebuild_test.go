package notify

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// fakeSource is the ticket as it is now. A ticket missing from facts is gone.
type fakeSource struct {
	mu      sync.Mutex
	facts   map[uuid.UUID]TicketFacts
	replies map[uuid.UUID]fakeReply // by reply id
	err     error
	asked   []uuid.UUID // reply ids whose text was asked for
}

type fakeReply struct {
	ticket   uuid.UUID
	body     string
	internal bool
	deleted  bool
}

func newFakeSource() *fakeSource {
	return &fakeSource{facts: map[uuid.UUID]TicketFacts{}, replies: map[uuid.UUID]fakeReply{}}
}

func (f *fakeSource) TicketFacts(_ context.Context, id uuid.UUID) (TicketFacts, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return TicketFacts{}, false, f.err
	}
	t, ok := f.facts[id]
	return t, ok, nil
}

func (f *fakeSource) PublicReplyBody(_ context.Context, ticketID, replyID uuid.UUID) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, replyID)
	r, ok := f.replies[replyID]
	if !ok || r.deleted || r.internal || r.ticket != ticketID {
		return "", false, nil
	}
	return r.body, true, nil
}

// rebuildRig is a Slack hook that has delivered contentEvent once, with a
// source that now knows the ticket under different, newer values.
type rebuildRig struct {
	*queueRig
	hook    authstore.WebhookConfig
	src     *fakeSource
	ev      notification.Event
	bodies  func() []string
	firstID uuid.UUID
}

func newRebuildRig(t *testing.T, format string, ev notification.Event) *rebuildRig {
	t.Helper()
	rc := newReceiver(t, 200)
	bodies := capture(rc)
	hook := hookFor(rc.srv.URL)
	hook.PayloadFormat = format
	rig := newQueueRig(t, rc.srv.Client(), hook)
	src := newFakeSource()
	rig.disp.queue.Source = src
	require.NoError(t, rig.front.Dispatch(context.Background(), ev))
	rig.drain(t)
	return &rebuildRig{queueRig: rig, hook: hook, src: src, ev: ev, bodies: bodies,
		firstID: rig.hooks.last().DeliveryID}
}

func (r *rebuildRig) redeliver(t *testing.T) string {
	t.Helper()
	before := len(r.bodies())
	require.NoError(t, r.disp.Redeliver(context.Background(), r.hook.ID, r.firstID))
	r.drain(t)
	got := r.bodies()
	require.Len(t, got, before+1)
	return got[before]
}

func (r *rebuildRig) setTicket(f TicketFacts) { r.src.facts[r.ev.TicketID] = f }

func (r *rebuildRig) setReply(body string, internal, deleted bool) {
	r.src.replies[contentReplyID] = fakeReply{ticket: r.ev.TicketID, body: body, internal: internal, deleted: deleted}
}

func replyEvent() notification.Event {
	ev := contentEvent()
	ev.Type = notification.EventTicketReplied
	return ev
}

// ── Chat redeliver rebuilds from the ticket as it is now ─────────────────────

func TestRedeliver_ChatMessageIsRebuiltFromTheTicketNow(t *testing.T) {
	r := newRebuildRig(t, "slack", replyEvent())
	r.setTicket(TicketFacts{Subject: "NEW-SUBJECT", TrackingNumber: "TRK-NOW-9", StatusName: "Open"})
	r.setReply("NOW-PUBLIC-REPLY", false, false)

	msg := r.redeliver(t)

	require.Contains(t, msg, "NEW-SUBJECT", "the current subject, not the original")
	require.Contains(t, msg, "TRK-NOW-9")
	require.Contains(t, msg, "NOW-PUBLIC-REPLY", "the reply as it reads now")
	require.NotContains(t, msg, "SECRET-SUBJECT")
	require.NotContains(t, msg, "customer@example.com")
}

func TestRedeliver_NeverSendsAReplyThatIsGoneOrInternal(t *testing.T) {
	for _, tc := range []struct {
		name     string
		internal bool
		deleted  bool
		missing  bool
	}{
		{"deleted reply", false, true, false},
		{"reply since made internal", true, false, false},
		{"reply id no longer exists", false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRebuildRig(t, "slack", replyEvent())
			r.setTicket(TicketFacts{Subject: "NEW-SUBJECT", TrackingNumber: "TRK-NOW-9"})
			if !tc.missing {
				r.setReply("MUST-NOT-APPEAR", tc.internal, tc.deleted)
			}

			msg := r.redeliver(t)

			require.NotContains(t, msg, "MUST-NOT-APPEAR")
			require.Contains(t, msg, "NEW-SUBJECT", "the rest of the message still goes")
			require.Contains(t, msg, "New reply")
		})
	}
}

func TestRedeliver_ANoteNeverCarriesText(t *testing.T) {
	ev := replyEvent()
	ev.Type = notification.EventTicketNoteAdded
	r := newRebuildRig(t, "slack", ev)
	r.setTicket(TicketFacts{Subject: "NEW-SUBJECT", TrackingNumber: "TRK-NOW-9"})
	r.setReply("NOTE-TEXT-MUST-NOT-APPEAR", false, false) // even if the lookup would answer

	msg := r.redeliver(t)

	require.NotContains(t, msg, "NOTE-TEXT-MUST-NOT-APPEAR")
	require.Contains(t, msg, "Internal note added")
	require.Empty(t, r.src.asked, "a note's text is not even asked for")
}

func TestRedeliver_ReplyFromAnotherTicketIsNotUsed(t *testing.T) {
	r := newRebuildRig(t, "slack", replyEvent())
	r.setTicket(TicketFacts{Subject: "NEW-SUBJECT"})
	r.src.replies[contentReplyID] = fakeReply{ticket: uuid.New(), body: "OTHER-TICKETS-TEXT"}

	require.NotContains(t, r.redeliver(t), "OTHER-TICKETS-TEXT")
}

func TestRedeliver_TicketGoneSendsTheHeadlineOnly(t *testing.T) {
	r := newRebuildRig(t, "slack", replyEvent())
	// no setTicket: the ticket is gone

	msg := r.redeliver(t)

	require.Contains(t, msg, "New reply")
	for _, leak := range contentStrings {
		require.NotContains(t, msg, leak)
	}
	require.Empty(t, r.src.asked)
}

func TestRedeliver_StatusChangeUsesTheCurrentStatusName(t *testing.T) {
	ev := notification.Event{Type: notification.EventTicketStatusChanged, TicketID: uuid.New(),
		Payload: map[string]any{"new_status_id": uuid.New()}, Subject: "OLD", StatusName: "OLD-STATUS"}
	r := newRebuildRig(t, "slack", ev)
	r.setTicket(TicketFacts{Subject: "NEW-SUBJECT", TrackingNumber: "TRK-NOW-9", StatusName: "Closed"})

	msg := r.redeliver(t)

	require.Contains(t, msg, "Status changed to Closed")
	require.NotContains(t, msg, "OLD-STATUS")
}

// With nothing to put after "to", the headline does not end in a dangling
// "Status changed to ".
func TestHeadline_StatusChangeWithoutAName(t *testing.T) {
	require.Equal(t, "Status changed", headline(notification.EventTicketStatusChanged, "", false, "", ""))
	require.Equal(t, "Status changed to Open", headline(notification.EventTicketStatusChanged, "Open", false, "", ""))
}

// JIRA's tracking_number held the ticket's UUID whenever a redelivered event
// had no tracking number. Rebuilt, it holds the tracking number.
func TestRedeliver_JiraTrackingNumberIsTheTrackingNumber(t *testing.T) {
	r := newRebuildRig(t, "jira", replyEvent())
	r.setTicket(TicketFacts{Subject: "NEW-SUBJECT", TrackingNumber: "TRK-NOW-9"})

	msg := r.redeliver(t)

	require.Contains(t, msg, `"tracking_number":"TRK-NOW-9"`)
	require.NotContains(t, msg, `"tracking_number":"`+r.ev.TicketID.String())
}

// Gone ticket: JIRA says nothing rather than putting an id where a tracking
// number belongs.
func TestRenderJIRA_NoTrackingNumberIsEmptyNotTheUUID(t *testing.T) {
	ev := notification.Event{Type: notification.EventTicketClosed, TicketID: uuid.New(), OccurredAt: fixtureOccurred}
	got, err := renderJIRA(summarize(ev, fixtureBaseURL))
	require.NoError(t, err)
	require.Contains(t, string(got), `"tracking_number":""`)
	require.Contains(t, string(got), ev.TicketID.String(), "the id is still there, as ticket_id")
}

func TestRedeliver_SourceFailureQueuesNothing(t *testing.T) {
	r := newRebuildRig(t, "slack", replyEvent())
	r.src.err = errors.New("database is down")
	before := len(r.store.order)

	require.Error(t, r.disp.Redeliver(context.Background(), r.hook.ID, r.firstID))
	require.Equal(t, before, len(r.store.order))
}

func TestRedeliver_RawHookNeverConsultsTheTicket(t *testing.T) {
	r := newRebuildRig(t, "raw", replyEvent())
	r.setTicket(TicketFacts{Subject: "NEW-SUBJECT"})
	r.setReply("NOW-PUBLIC-REPLY", false, false)

	msg := r.redeliver(t)

	require.NotContains(t, msg, "NEW-SUBJECT")
	require.NotContains(t, msg, "NOW-PUBLIC-REPLY")
	require.Empty(t, r.src.asked)
}

// The rebuilt content sits in the queued row only until it is sent, and the
// copy kept for the next Redeliver is still ids only.
func TestRedeliver_RebuiltContentIsNotKept(t *testing.T) {
	r := newRebuildRig(t, "slack", replyEvent())
	r.setTicket(TicketFacts{Subject: "NEW-SUBJECT", TrackingNumber: "TRK-NOW-9"})
	r.setReply("NOW-PUBLIC-REPLY", false, false)
	_ = r.redeliver(t)

	kept := string(r.hooks.last().Event)
	for _, leak := range []string{"NEW-SUBJECT", "TRK-NOW-9", "NOW-PUBLIC-REPLY"} {
		require.NotContains(t, kept, leak)
	}
}

// ── A delivery that finally fails leaves no content behind ───────────────────

func TestQueue_FinalFailureScrubsTheStoredRow(t *testing.T) {
	rc := newReceiver(t, 500)
	bodies := capture(rc)
	hook := hookFor(rc.srv.URL)
	hook.PayloadFormat = "slack"
	rig := newQueueRig(t, rc.srv.Client(), hook)
	ev := replyEvent()

	require.NoError(t, rig.front.Dispatch(context.Background(), ev))
	rig.drain(t)

	require.Equal(t, 8, rc.count())
	for _, b := range bodies() {
		require.Contains(t, b, "SECRET-REPLY-TEXT", "every attempt, the last included, sends the real message")
	}
	require.Len(t, rig.store.failed, 1)
	var kept string
	for id := range rig.store.failed {
		kept = string(rig.store.rows[id].event)
	}
	for _, leak := range contentStrings {
		require.NotContains(t, kept, leak, "the row kept for 30 days")
	}
	require.Contains(t, kept, ev.TicketID.String(), "ids stay")
	require.Contains(t, kept, hook.ID.String())
}

// Until the last attempt the row still has to build the message.
func TestQueue_RetryingRowKeepsItsContent(t *testing.T) {
	rc := newReceiver(t, 500, 200)
	bodies := capture(rc)
	hook := hookFor(rc.srv.URL)
	hook.PayloadFormat = "slack"
	rig := newQueueRig(t, rc.srv.Client(), hook)

	require.NoError(t, rig.front.Dispatch(context.Background(), replyEvent()))
	rig.drain(t)

	got := bodies()
	require.Len(t, got, 2)
	require.True(t, strings.Contains(got[1], "SECRET-REPLY-TEXT"), "the retry is the same message")
}
