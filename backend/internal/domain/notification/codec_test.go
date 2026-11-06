package notification

import (
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Every field of Event is either stored or deliberately left out. A new field
// added to Event without a decision would otherwise vanish silently on the
// way through the queue. (Moved here from internal/server/notify with the
// encoding it checks, #506.)
func TestOutboxRecord_CoversEveryEventField(t *testing.T) {
	stored := map[string]bool{}
	rt := reflect.TypeOf(outboxRecord{})
	for i := range rt.NumField() {
		stored[rt.Field(i).Name] = true
	}
	notStored := map[string]string{
		"GuestToken": "a raw credential; created by the worker at send time instead",
	}
	et := reflect.TypeOf(Event{})
	for i := range et.NumField() {
		name := et.Field(i).Name
		if stored[name] {
			continue
		}
		_, decided := notStored[name]
		require.True(t, decided, "Event.%s is neither stored in outboxRecord nor listed as deliberately left out", name)
	}
}

func TestEncodeEvent_RefusesARawGuestToken(t *testing.T) {
	_, err := EncodeEvent(Event{Type: EventTicketReplied, GuestToken: "raw"})
	require.ErrorIs(t, err, ErrRawGuestToken)
}

// The new fields survive the queue: the kinds and the ticket are what every
// consumer decides from.
func TestEventCodec_RoundTripsKindsChangesAndTicket(t *testing.T) {
	grp, usr, typ := uuid.New(), uuid.New(), uuid.New()
	ev := Event{
		Type: EventTicketReopened, TicketID: uuid.New(),
		OccurredAt: time.Date(2026, 10, 14, 15, 0, 0, 0, time.UTC),
		Kinds:      []EventType{EventTicketReopened, EventTicketStatusChanged, EventTicketPriorityChanged},
		Changes:    []Change{ValueChange("priority", "medium", "high"), IDChange("assignee_user_id", nil, &usr)},
		Ticket: &TicketState{
			StatusID: uuid.New(), AssigneeUserID: &usr, AssigneeGroupID: &grp, Priority: "high",
			CategoryID: uuid.New(), TypeID: &typ, ReopenCount: 1,
		},
	}
	b, err := EncodeEvent(ev)
	require.NoError(t, err)
	got, err := DecodeEvent(b)
	require.NoError(t, err)
	require.Equal(t, ev.Kinds, got.Kinds)
	require.Equal(t, ev.Changes, got.Changes)
	require.Equal(t, ev.Ticket, got.Ticket)
}

// A row queued before #506 has no kinds and no ticket. It is a one-kind change
// (AllKinds reads [Type]); its ticket state is rebuilt from the attrs and
// assignee keys it did carry. Kinds stays empty, which is how the worker knows
// the row was already split on the request and must not be split again.
func TestDecodeEvent_RowsInTheOldShape(t *testing.T) {
	cat, grp, usr := uuid.New(), uuid.New(), uuid.New()
	tests := []struct {
		name      string
		row       string
		wantKinds []EventType
		want      *TicketState
	}{
		{"no attrs", `{"type":"ticket.created","ticket_id":"11111111-2222-3333-4444-555555555555","occurred_at":"2026-01-01T00:00:00Z"}`,
			[]EventType{EventTicketCreated}, nil},
		{"attrs", `{"type":"ticket.created","ticket_id":"11111111-2222-3333-4444-555555555555","occurred_at":"2026-01-01T00:00:00Z",` +
			`"attrs":{"category_id":"` + cat.String() + `","priority":"low","assignee_group_id":"` + grp.String() + `"}}`,
			[]EventType{EventTicketCreated}, &TicketState{CategoryID: cat, Priority: "low", AssigneeGroupID: &grp}},
		{"assignee keys of a staff-bound event", `{"type":"ticket.assigned","ticket_id":"11111111-2222-3333-4444-555555555555","occurred_at":"2026-01-01T00:00:00Z",` +
			`"assignee_user_id":"` + usr.String() + `","assignee_group_id":"` + grp.String() + `"}`,
			[]EventType{EventTicketAssigned}, &TicketState{AssigneeUserID: &usr, AssigneeGroupID: &grp}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeEvent([]byte(tc.row))
			require.NoError(t, err)
			require.Empty(t, got.Kinds)
			require.Equal(t, tc.wantKinds, got.AllKinds())
			require.Equal(t, tc.want, got.Ticket)
		})
	}
}
