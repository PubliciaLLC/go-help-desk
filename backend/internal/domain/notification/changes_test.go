package notification

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func ptr[T any](v T) *T { return &v }

// One transaction that changes one ticket is one event, whatever number of
// pieces raised it (#506, ruling 8).
func TestChanges_MergesRaisesForOneTicketIntoOneEvent(t *testing.T) {
	tid := uuid.New()
	alice, bob := uuid.New(), uuid.New()
	t0 := time.Date(2026, 10, 14, 15, 0, 0, 0, time.UTC)

	c := &Changes{}
	c.Raise(Event{
		Kinds: []EventType{EventTicketStatusChanged}, TicketID: tid, OccurredAt: t0,
		Payload: map[string]any{"new_status_id": "s1"},
		Changes: []Change{ValueChange("status_id", "a", "b")},
		Ticket:  &TicketState{Priority: "medium"},
	})
	c.Raise(Event{
		Kinds: []EventType{EventTicketAssigned}, TicketID: tid, OccurredAt: t0.Add(time.Second),
		Changes: []Change{IDChange("assignee_user_id", &alice, &bob)},
		Ticket:  &TicketState{Priority: "medium", AssigneeUserID: &bob},
	})
	c.Raise(Event{
		Kinds: []EventType{EventTicketPriorityChanged}, TicketID: tid,
		Changes: []Change{ValueChange("priority", "medium", "high")},
		Ticket:  &TicketState{Priority: "high", AssigneeUserID: &bob},
	})
	// The same field again: first "from", last "to".
	c.Raise(Event{
		Kinds: []EventType{EventTicketPriorityChanged}, TicketID: tid,
		Changes: []Change{ValueChange("priority", "high", "critical")},
		Ticket:  &TicketState{Priority: "critical", AssigneeUserID: &bob},
	})

	evs := c.Events()
	require.Len(t, evs, 1)
	ev := evs[0]
	require.Equal(t, []EventType{EventTicketStatusChanged, EventTicketAssigned, EventTicketPriorityChanged}, ev.Kinds)
	require.Equal(t, EventTicketStatusChanged, ev.Type, "Type is the first kind")
	require.Equal(t, t0, ev.OccurredAt, "the change began at the first raise")
	require.Equal(t, "s1", ev.Payload["new_status_id"])
	require.Equal(t, "critical", ev.Ticket.Priority, "the ticket is as of the last raise")
	require.Equal(t, []Change{
		ValueChange("status_id", "a", "b"),
		IDChange("assignee_user_id", &alice, &bob),
		ValueChange("priority", "medium", "critical"),
	}, ev.Changes)
}

// The kinds come out in the fixed order of importance, not the order raised.
func TestChanges_KindsAreInTheFixedOrder(t *testing.T) {
	tid := uuid.New()
	tests := []struct {
		name   string
		raised [][]EventType
		want   []EventType
	}{
		{"reply that reopens",
			[][]EventType{{EventTicketReplied}, {EventTicketStatusChanged, EventTicketReopened}},
			[]EventType{EventTicketReopened, EventTicketStatusChanged, EventTicketReplied}},
		{"create then assign",
			[][]EventType{{EventTicketAssigned}, {EventTicketCreated}},
			[]EventType{EventTicketCreated, EventTicketAssigned}},
		{"a kind raised twice is listed once",
			[][]EventType{{EventTicketLinked}, {EventTicketLinked}, {EventTicketResolved}},
			[]EventType{EventTicketResolved, EventTicketLinked}},
		{"every kind",
			[][]EventType{{EventTicketLinked, EventTicketNoteAdded, EventTicketReplied, EventTicketClassified,
				EventTicketPriorityChanged, EventTicketAssigned, EventTicketStatusChanged, EventTicketClosed,
				EventTicketResolved, EventTicketReopened, EventTicketCreated}},
			KindOrder},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &Changes{}
			for _, k := range tc.raised {
				c.Raise(Event{Kinds: k, TicketID: tid})
			}
			require.Equal(t, tc.want, c.Events()[0].Kinds)
		})
	}
}

// A change that touches two tickets is two events, one per ticket (ruling 8).
func TestChanges_OneEventPerTicket(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	c := &Changes{}
	c.Raise(Event{Kinds: []EventType{EventTicketCreated}, TicketID: a})
	c.Raise(Event{Kinds: []EventType{EventTicketLinked}, TicketID: b})
	c.Raise(Event{Kinds: []EventType{EventTicketAssigned}, TicketID: a})

	evs := c.Events()
	require.Len(t, evs, 2)
	require.Equal(t, a, evs[0].TicketID, "events keep the order tickets were first raised in")
	require.Equal(t, []EventType{EventTicketCreated, EventTicketAssigned}, evs[0].Kinds)
	require.Equal(t, b, evs[1].TicketID)
	require.Equal(t, []EventType{EventTicketLinked}, evs[1].Kinds)
}

func TestChanges_EmptyCollectorHasNoEvents(t *testing.T) {
	require.Empty(t, (&Changes{}).Events())
	var nilChanges *Changes
	require.Empty(t, nilChanges.Events())
}

// The scalar fields: the guest link and "from the requester" are true if any
// piece said so; the strings are the last piece that had one; the actor is
// the first piece's.
func TestChanges_MergesScalars(t *testing.T) {
	tid := uuid.New()
	actor, other := uuid.New(), uuid.New()
	c := &Changes{}
	c.Raise(Event{Kinds: []EventType{EventTicketCreated}, TicketID: tid, ActorID: &actor,
		TrackingNumber: "GHD-1", Subject: "s", Recipient: "g@example.com"})
	c.Raise(Event{Kinds: []EventType{EventTicketAssigned}, TicketID: tid, ActorID: &other, GuestLink: true, FromRequester: true})
	ev := c.Events()[0]
	require.Equal(t, &actor, ev.ActorID)
	require.Equal(t, "GHD-1", ev.TrackingNumber)
	require.Equal(t, "s", ev.Subject)
	require.Equal(t, "g@example.com", ev.Recipient)
	require.True(t, ev.GuestLink)
	require.True(t, ev.FromRequester)
}

func TestChange_Constructors(t *testing.T) {
	id := uuid.New()
	require.Equal(t, Change{Field: "x", From: nil, To: ptr(id.String())}, IDChange("x", nil, &id))
	require.Equal(t, Change{Field: "p", From: ptr("a"), To: ptr("b")}, ValueChange("p", "a", "b"))
}

// An event with no kinds says nothing; it is ignored, not a panic.
func TestChanges_AnEventWithNoKindsIsIgnored(t *testing.T) {
	c := &Changes{}
	require.NotPanics(t, func() { c.Raise(Event{TicketID: uuid.New()}) })
	require.Empty(t, c.Events())
}
