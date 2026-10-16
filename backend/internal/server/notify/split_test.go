package notify

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// Staff mail is decided in the worker, when it first takes the change's email
// row (#506), and each person's mail is a row of its own.

type splitRig struct {
	outbox *fakeOutbox
	sent   *recorder
	worker *Worker
}

func newSplitRig(users StaffUsers, groups StaffGroups, fol StaffFollowers) *splitRig {
	outbox := newFakeOutbox()
	sent := &recorder{}
	split := NewChangeSplitter(sent, outbox, nil, users, groups, fol, noOverrides, quietLog())
	w := NewWorker(outbox, map[string]notification.Dispatcher{"email": split}, quietLog())
	return &splitRig{outbox: outbox, sent: sent, worker: w}
}

func (r *splitRig) queue(t *testing.T, ev notification.Event) {
	t.Helper()
	b, err := encodeEvent(ev)
	require.NoError(t, err)
	require.NoError(t, r.outbox.Enqueue(context.Background(), uuid.New(), "email", b))
}

// drain runs the worker until it has nothing due.
func (r *splitRig) drain(t *testing.T) {
	t.Helper()
	for range 10 {
		r.outbox.makeDue()
		n, err := r.worker.RunOnce(context.Background())
		require.NoError(t, err)
		if n == 0 {
			return
		}
	}
	t.Fatal("the worker never ran out of rows")
}

// changeRow is a change the worker splits: the design's reopen-and-assign.
func changeRow(guest string, assignee, group *uuid.UUID, actor *uuid.UUID) notification.Event {
	return notification.Event{
		Type: notification.EventTicketReopened, TicketID: uuid.New(), ActorID: actor, OccurredAt: time.Now(),
		TrackingNumber: "GHD-2026-000007", Recipient: guest,
		Kinds: []notification.EventType{
			notification.EventTicketReopened, notification.EventTicketStatusChanged, notification.EventTicketAssigned,
		},
		Ticket: &notification.TicketState{AssigneeUserID: assignee, AssigneeGroupID: group},
	}
}

// The change's row becomes the requester's mail and one mail per staff member,
// the change's own row is settled, and the requester's mail uses the template
// of the first kind that has one (reopened, before status_changed).
func TestChangeSplitter_OneRowBecomesTheRequestersMailAndOnePerStaffMember(t *testing.T) {
	assignee, member, follower := staffUser("assignee"), staffUser("member"), staffUser("follower")
	g := uuid.New()
	ev := changeRow("guest@example.com", &assignee.ID, &g, nil)
	rig := newSplitRig(newFakeStaffUsers(assignee, member, follower), fakeStaffGroups{g: {member.ID}},
		fakeStaffFollowers{ev.TicketID: {follower.ID}})
	rig.queue(t, ev)

	rig.drain(t)

	require.Empty(t, liveRows(rig.outbox), "every row, the change's included, was settled")
	var requester []notification.Event
	staff := map[uuid.UUID]notification.EventType{}
	for _, e := range rig.sent.got {
		if e.StaffUserID != nil {
			staff[*e.StaffUserID] = e.Type
			continue
		}
		requester = append(requester, e)
	}
	require.Len(t, requester, 1, "one mail to the requester")
	require.Equal(t, notification.EventTicketReopened, requester[0].Type)
	require.Equal(t, "guest@example.com", requester[0].Recipient)
	require.Empty(t, requester[0].Kinds)
	// The assigned switch is on for everybody here, so the assignee and the
	// group's member hear "assigned" (the first kind in the order assigned,
	// reopened, replied); the follower is not in the group and is mailed as a
	// follower of the assignment too.
	require.Equal(t, map[uuid.UUID]notification.EventType{
		assignee.ID: notification.EventStaffAssigned,
		member.ID:   notification.EventStaffAssigned,
		follower.ID: notification.EventStaffAssigned,
	}, staff)
}

// One mail per person per change: somebody who qualifies for several kinds gets
// the mail of the first whose switch is on. Bob, new assignee, with his Assigned
// switch off, still hears of the reopen through his Activity switch.
func TestChangeSplitter_APersonGetsTheFirstKindWhoseSwitchIsOn(t *testing.T) {
	bob := staffUser("bob")
	bob.EmailNotifyAssigned = false
	ev := changeRow("", &bob.ID, nil, nil)
	rig := newSplitRig(newFakeStaffUsers(bob), fakeStaffGroups{}, fakeStaffFollowers{})
	rig.queue(t, ev)

	rig.drain(t)

	require.Len(t, rig.sent.got, 1, "one mail, not one per kind")
	require.Equal(t, notification.EventStaffReopened, rig.sent.got[0].Type)
	require.Equal(t, notification.SwitchActivity, rig.sent.got[0].StaffReason)
}

// Nobody is mailed about their own action.
func TestChangeSplitter_NobodyIsMailedAboutTheirOwnAction(t *testing.T) {
	actor := staffUser("actor")
	ev := changeRow("", &actor.ID, nil, &actor.ID)
	rig := newSplitRig(newFakeStaffUsers(actor), fakeStaffGroups{}, fakeStaffFollowers{})
	rig.queue(t, ev)

	rig.drain(t)

	require.Empty(t, rig.sent.got)
}

// Who is mailed is decided from the ticket as the change left it, when the
// worker picks the row up: a follow that the same change dropped is not read.
func TestChangeSplitter_ReadsFollowersWhenTheWorkerPicksTheRowUp(t *testing.T) {
	assignee, follower := staffUser("assignee"), staffUser("follower")
	ev := changeRow("", &assignee.ID, nil, nil)
	fol := fakeStaffFollowers{ev.TicketID: {follower.ID}}
	rig := newSplitRig(newFakeStaffUsers(assignee, follower), fakeStaffGroups{}, fol)
	rig.queue(t, ev)
	delete(fol, ev.TicketID) // the follow went with the reassignment, committed with the change

	rig.drain(t)

	require.Equal(t, []uuid.UUID{assignee.ID}, recipientsOf(rig.sent))
}

// A crash between queueing the children and settling the change's row has the
// worker split it again: the children keep their ids, so nobody is queued twice.
func TestChangeSplitter_ASecondSplitOfTheSameRowQueuesNothingNew(t *testing.T) {
	assignee := staffUser("assignee")
	ev := changeRow("guest@example.com", &assignee.ID, nil, nil)
	outbox := newFakeOutbox()
	split := NewChangeSplitter(&recorder{}, outbox, nil, newFakeStaffUsers(assignee), fakeStaffGroups{}, fakeStaffFollowers{}, noOverrides, quietLog())
	parent := uuid.New()
	ctx := context.WithValue(context.Background(), rowKey{}, parent)

	require.NoError(t, split.Dispatch(ctx, ev))
	first := len(outbox.rows)
	require.NoError(t, split.Dispatch(ctx, ev))

	require.Equal(t, 2, first, "the requester's mail and the assignee's")
	require.Equal(t, first, len(outbox.rows))
}

// A row queued before #506 was split on the request already: it is sent as it
// is and not split again.
func TestChangeSplitter_ARowWithoutKindsIsSentAsItIs(t *testing.T) {
	assignee := staffUser("assignee")
	old := notification.Event{Type: notification.EventStaffAssigned, TicketID: uuid.New(), StaffUserID: &assignee.ID}
	rig := newSplitRig(newFakeStaffUsers(assignee), fakeStaffGroups{}, fakeStaffFollowers{})
	rig.queue(t, old)
	oldRequester := notification.Event{Type: notification.EventTicketStatusChanged, TicketID: uuid.New(), Recipient: "g@example.com"}
	rig.queue(t, oldRequester)

	rig.drain(t)

	require.Len(t, rig.sent.got, 2)
	require.Equal(t, notification.EventStaffAssigned, rig.sent.got[0].Type)
	require.Equal(t, notification.EventTicketStatusChanged, rig.sent.got[1].Type)
}

// A lookup that fails has the worker try the change's row again, rather than
// settling it with staff unmailed.
func TestChangeSplitter_AFailedLookupRetriesTheRow(t *testing.T) {
	assignee := staffUser("assignee")
	users := newFakeStaffUsers(assignee)
	users.err = context.DeadlineExceeded
	rig := newSplitRig(users, fakeStaffGroups{}, fakeStaffFollowers{})
	rig.queue(t, changeRow("", &assignee.ID, nil, nil))

	_, err := rig.worker.RunOnce(context.Background())
	require.NoError(t, err)

	require.Len(t, rig.outbox.retries, 1, "rescheduled, not settled")
	users.err = nil
	rig.drain(t)
	require.Equal(t, []uuid.UUID{assignee.ID}, recipientsOf(rig.sent))
}

// A change with nothing to mail settles at once.
func TestChangeSplitter_ANotableChangeWithNobodyToMailIsSettled(t *testing.T) {
	rig := newSplitRig(newFakeStaffUsers(), fakeStaffGroups{}, fakeStaffFollowers{})
	rig.queue(t, changeRow("", nil, nil, nil))

	rig.drain(t)

	require.Empty(t, rig.sent.got)
	require.Empty(t, liveRows(rig.outbox))
}

// Routing a create to its own creator (#506, ruling B): the person who filed the
// ticket did not pick the assignee, routing did, so they hear of it, once. Only
// this case; acting on your own ticket by hand still mails you nothing.
func routedCreate(creator, assignee *uuid.UUID) notification.Event {
	return notification.Event{
		Type: notification.EventTicketCreated, TicketID: uuid.New(), ActorID: creator, OccurredAt: time.Now(),
		TrackingNumber: "GHD-2026-000007", RoutedAssignment: true,
		Kinds:  []notification.EventType{notification.EventTicketCreated, notification.EventTicketAssigned},
		Ticket: &notification.TicketState{AssigneeUserID: assignee},
	}
}

func TestChangeSplitter_ACreatorRoutedToThemselvesIsMailedOnce(t *testing.T) {
	creator := staffUser("creator")
	rig := newSplitRig(newFakeStaffUsers(creator), fakeStaffGroups{}, fakeStaffFollowers{})
	rig.queue(t, routedCreate(&creator.ID, &creator.ID))

	rig.drain(t)

	require.Equal(t, []uuid.UUID{creator.ID}, recipientsOf(rig.sent), "exactly one mail, not none")
	require.Equal(t, notification.EventStaffAssigned, rig.sent.got[0].Type)
}

func TestChangeSplitter_ARoutedCreateMailsTheAssigneeAndNotTheCreator(t *testing.T) {
	creator, assignee := staffUser("creator"), staffUser("assignee")
	rig := newSplitRig(newFakeStaffUsers(creator, assignee), fakeStaffGroups{}, fakeStaffFollowers{})
	rig.queue(t, routedCreate(&creator.ID, &assignee.ID))

	rig.drain(t)

	require.Equal(t, []uuid.UUID{assignee.ID}, recipientsOf(rig.sent))
}

// The same event without the routing mark is the person's own action.
func TestChangeSplitter_AnAssignmentTheActorMadeStillMailsNobody(t *testing.T) {
	actor := staffUser("actor")
	ev := routedCreate(&actor.ID, &actor.ID)
	ev.RoutedAssignment = false
	rig := newSplitRig(newFakeStaffUsers(actor), fakeStaffGroups{}, fakeStaffFollowers{})
	rig.queue(t, ev)

	rig.drain(t)

	require.Empty(t, rig.sent.got)
}
