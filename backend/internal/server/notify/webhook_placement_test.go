package notify

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// createdEvent is what the outbox hands the dispatcher for a new ticket that
// routing placed with a group: the ids as committed, no names.
func placementCreatedEvent() notification.Event {
	g := someGroup
	return notification.Event{
		Type:           notification.EventTicketCreated,
		TicketID:       uuid.New(),
		OccurredAt:     fixtureOccurred,
		TrackingNumber: "GHD-2026-000003",
		Subject:        "Test 3",
		Kinds:          []notification.EventType{notification.EventTicketCreated, notification.EventTicketAssigned},
		Ticket:         &notification.TicketState{CategoryID: uuid.New(), AssigneeGroupID: &g, Priority: "medium"},
	}
}

func itSupport(notification.TicketState) notification.Placement {
	return notification.Placement{Category: "Hardware", Type: "Printer", Group: "IT Support"}
}

type placementRig struct {
	*queueRig
	rc     *receiver
	bodies func() []string
	src    *fakeSource
}

func newPlacementRig(t *testing.T, format string, codes ...int) *placementRig {
	t.Helper()
	rc := newReceiver(t, codes...)
	bodies := capture(rc)
	hook := hookFor(rc.srv.URL)
	hook.PayloadFormat = format
	rig := newQueueRig(t, rc.srv.Client(), hook)
	src := newFakeSource()
	src.placement = itSupport
	rig.disp.queue.Source = src
	return &placementRig{queueRig: rig, rc: rc, bodies: bodies, src: src}
}

func (r *placementRig) send(t *testing.T, ev notification.Event) {
	t.Helper()
	require.NoError(t, r.front.Dispatch(context.Background(), ev))
	r.drain(t)
}

// A Slack or Teams hook is sent the names, read from the ids the event holds.
func TestPlacement_SlackAndTeamsGetTheLines(t *testing.T) {
	for _, format := range []string{"slack", "teams"} {
		t.Run(format, func(t *testing.T) {
			r := newPlacementRig(t, format, 200)
			ev := placementCreatedEvent()
			r.send(t, ev)

			require.Len(t, r.bodies(), 1)
			msg := r.bodies()[0]
			require.True(t, json.Valid([]byte(msg)))
			require.Contains(t, msg, "Hardware")
			require.Contains(t, msg, "Group: IT Support")
			require.Contains(t, msg, "Test 3")
			require.Len(t, r.src.placeAsked, 1)
			require.Equal(t, *ev.Ticket, r.src.placeAsked[0], "the ids of the change, as committed")
		})
	}
}

// Nothing else reads the names: raw, Discord and Jira are not asked for them,
// so they cost no query and carry no line.
func TestPlacement_OtherFormatsAreNeitherAskedNorSent(t *testing.T) {
	for _, format := range []string{"raw", "discord", "jira"} {
		t.Run(format, func(t *testing.T) {
			r := newPlacementRig(t, format, 200)
			r.send(t, placementCreatedEvent())

			require.Len(t, r.bodies(), 1)
			require.NotContains(t, r.bodies()[0], "IT Support")
			require.NotContains(t, r.bodies()[0], "Hardware")
			require.Empty(t, r.src.placeAsked)
		})
	}
}

// Only a creation is asked about, and only when the event holds the ids.
func TestPlacement_OnlyACreationWithItsIdsIsLookedUp(t *testing.T) {
	assigned := placementCreatedEvent()
	assigned.Type = notification.EventTicketAssigned
	assigned.Kinds = []notification.EventType{notification.EventTicketAssigned}
	noState := placementCreatedEvent()
	noState.Ticket = nil

	for name, ev := range map[string]notification.Event{"assigned": assigned, "no ticket state": noState} {
		t.Run(name, func(t *testing.T) {
			r := newPlacementRig(t, "slack", 200)
			r.send(t, ev)

			require.Len(t, r.bodies(), 1)
			require.NotContains(t, r.bodies()[0], "IT Support")
			require.Empty(t, r.src.placeAsked)
		})
	}
}

// The lines are an addition: a database that cannot answer must not hold the
// notification back, or lose it. It is sent without them, once.
func TestPlacement_AFailedLookupSendsTheMessageWithoutTheLines(t *testing.T) {
	r := newPlacementRig(t, "teams", 200)
	r.src.placementErr = errors.New("connection refused")
	r.send(t, placementCreatedEvent())

	require.Equal(t, 1, r.rc.count())
	msg := r.bodies()[0]
	require.Contains(t, msg, "Test 3")
	require.NotContains(t, msg, "IT Support")
	require.NotContains(t, msg, "Unassigned", "a lookup that failed is not a ticket that nobody holds")
}

func TestPlacement_NoSourceSendsTheOldMessage(t *testing.T) {
	r := newPlacementRig(t, "slack", 200)
	r.disp.queue.Source = nil
	r.send(t, placementCreatedEvent())

	require.Len(t, r.bodies(), 1)
	require.NotContains(t, r.bodies()[0], "Hardware")
	require.Contains(t, r.bodies()[0], "Test 3")
}

// A retry reads the names again: a group renamed while the receiver was down is
// named as it is when the message finally goes. That is the behaviour chosen
// (DESIGN.md), and this pins it.
func TestPlacement_ARetryNamesThingsAsTheyAreNow(t *testing.T) {
	r := newPlacementRig(t, "slack", 503, 200)
	calls := 0
	r.src.placement = func(notification.TicketState) notification.Placement {
		calls++
		if calls == 1 {
			return notification.Placement{Category: "Hardware", Group: "Old Name"}
		}
		return notification.Placement{Category: "Hardware", Group: "New Name"}
	}
	r.send(t, placementCreatedEvent())

	require.Len(t, r.bodies(), 2)
	require.Contains(t, r.bodies()[0], "Old Name")
	require.Contains(t, r.bodies()[1], "New Name")
	require.NotContains(t, r.bodies()[1], "Old Name")
}

// The row the outbox keeps holds ids and no names: the names are read when the
// message is built, never stored.
func TestPlacement_NamesAreNeverStoredInTheOutboxRow(t *testing.T) {
	r := newPlacementRig(t, "slack", 503)
	require.NoError(t, r.front.Dispatch(context.Background(), placementCreatedEvent()))
	_, err := r.worker.RunOnce(context.Background()) // fan-out
	require.NoError(t, err)
	_, err = r.worker.RunOnce(context.Background()) // a failed attempt, the row stays
	require.NoError(t, err)

	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	require.NotEmpty(t, r.store.rows, "the failed attempt leaves a row to retry")
	for _, row := range r.store.rows {
		require.NotContains(t, string(row.event), "IT Support")
		require.NotContains(t, string(row.event), "Hardware")
	}
}

// A redelivered created message is rebuilt from the ticket as it is now, names
// included: the ids are the ticket's current ones.
func TestPlacement_RedeliverUsesTheTicketsCurrentIds(t *testing.T) {
	r := newRebuildRig(t, "teams", placementCreatedEvent())
	r.src.placement = itSupport
	other := uuid.New()
	r.setTicket(TicketFacts{Subject: "Now", TrackingNumber: "GHD-2026-000003",
		State: &notification.TicketState{CategoryID: other, Priority: "medium"}})

	msg := r.redeliver(t)

	require.Contains(t, msg, "Hardware")
	require.Equal(t, other, r.src.placeAsked[len(r.src.placeAsked)-1].CategoryID)
}

// A hook that lists only ticket.created is told who holds a ticket that routing
// assigned in the same change, exactly as a "*" hook is (the change's kinds are
// cut to the hook's before the message is built).
func TestPlacement_ACreatedOnlyHookIsToldTheHolder(t *testing.T) {
	for _, format := range []string{"slack", "teams"} {
		t.Run(format, func(t *testing.T) {
			rc := newReceiver(t, 200)
			bodies := capture(rc)
			hook := hookFor(rc.srv.URL)
			hook.PayloadFormat = format
			hook.Events = []string{"ticket.created"}
			rig := newQueueRig(t, rc.srv.Client(), hook)
			src := newFakeSource()
			src.placement = itSupport
			rig.disp.queue.Source = src

			require.NoError(t, rig.front.Dispatch(context.Background(), placementCreatedEvent()))
			rig.drain(t)

			require.Len(t, bodies(), 1)
			require.Contains(t, bodies()[0], "Group: IT Support")
			require.NotContains(t, bodies()[0], "Also:", "the Also line still names only kinds the hook subscribes to")
		})
	}
}
