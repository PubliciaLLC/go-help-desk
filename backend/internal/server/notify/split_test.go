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

// One mail per recipient address per change, compared without case or
// surrounding space. When the requester's mail and a staff member's resolve to
// the same address, the staff mail is the one sent.
func guestRoutedCreate(guest string, assignee *uuid.UUID) notification.Event {
	ev := routedCreate(nil, assignee)
	ev.Recipient = guest
	return ev
}

func mailsTo(r *recorder) (requester, staff int) {
	for _, e := range r.got {
		if e.StaffUserID != nil {
			staff++
		} else {
			requester++
		}
	}
	return
}

func TestChangeSplitter_ASharedAddressGetsTheStaffMailOnly(t *testing.T) {
	for name, guest := range map[string]string{
		"the same address":           "assignee@example.com",
		"the same address, any case": "  Assignee@Example.COM ",
	} {
		t.Run(name, func(t *testing.T) {
			assignee := staffUser("assignee") // assignee@example.com
			rig := newSplitRig(newFakeStaffUsers(assignee), fakeStaffGroups{}, fakeStaffFollowers{})
			rig.queue(t, guestRoutedCreate(guest, &assignee.ID))

			rig.drain(t)

			req, st := mailsTo(rig.sent)
			require.Equal(t, 1, req+st, "one mail to the address")
			require.Equal(t, 1, st, "and it is the staff mail, with the staff link and the more specific reason")
		})
	}
}

func TestChangeSplitter_ADifferentAddressKeepsBothMails(t *testing.T) {
	assignee := staffUser("assignee")
	rig := newSplitRig(newFakeStaffUsers(assignee), fakeStaffGroups{}, fakeStaffFollowers{})
	rig.queue(t, guestRoutedCreate("guest@example.com", &assignee.ID))

	rig.drain(t)

	req, st := mailsTo(rig.sent)
	require.Equal(t, 1, req)
	require.Equal(t, 1, st)
}

// A staff lookup that fails once must not cost the guest a second mail. The
// requester's mail is only queued once everyone is resolved, so the retry of the
// change starts clean; before, it was queued first, sent and deleted, and then
// queued again by the retry, each time issuing a new guest link.
func TestChangeSplitter_AStaffLookupFailingOnceMailsTheGuestOnce(t *testing.T) {
	assignee := staffUser("assignee")
	users := newFakeStaffUsers(assignee)
	users.failFirst = 1
	outbox := newFakeOutbox()
	sent := &recorder{}
	links := 0
	send := NewGuestLinkDispatcher(sent, func(_ context.Context, ev notification.Event) (notification.Event, bool, error) {
		links++
		return ev, true, nil
	})
	split := NewChangeSplitter(send, outbox, nil, users, fakeStaffGroups{}, fakeStaffFollowers{}, noOverrides, quietLog())
	w := NewWorker(outbox, map[string]notification.Dispatcher{"email": split}, quietLog())
	w.Backoff = func(int) time.Duration { return time.Hour } // the retry waits; the queued mail would not
	ev := guestRoutedCreate("guest@example.com", &assignee.ID)
	ev.GuestLink = true
	b, err := encodeEvent(ev)
	require.NoError(t, err)
	require.NoError(t, outbox.Enqueue(context.Background(), uuid.New(), "email", b))

	// Everything that is due now: the change fails, and whatever it queued is sent.
	for range 5 {
		n, err := w.RunOnce(context.Background())
		require.NoError(t, err)
		if n == 0 {
			break
		}
	}
	// Then the retry comes due.
	for range 5 {
		outbox.makeDue()
		n, err := w.RunOnce(context.Background())
		require.NoError(t, err)
		if n == 0 {
			break
		}
	}

	req, st := mailsTo(sent)
	require.Equal(t, 1, req, "exactly one mail to the guest")
	require.Equal(t, 1, st)
	require.Equal(t, 1, links, "and exactly one guest link issued")
}

// Two staff accounts sharing one address: one mail to it, and it is the one with
// the stronger reason (holding the ticket beats being in its group), so the
// send-time check that drops a mail whose reason no longer holds is least likely
// to leave the address with nothing.
func TestChangeSplitter_TwoStaffSharingAnAddressGetOneMail_TheStrongerReason(t *testing.T) {
	assignee, member := staffUser("shared"), staffUser("shared2")
	member.Email = " SHARED@example.com " // the assignee's address, in another case
	g := uuid.New()
	ev := changeRow("", &assignee.ID, &g, nil)
	// The group member is listed first by the group, ahead of the assignee's
	// own candidacy being ranked.
	rig := newSplitRig(newFakeStaffUsers(assignee, member), fakeStaffGroups{g: {member.ID}}, fakeStaffFollowers{})
	rig.queue(t, ev)

	rig.drain(t)

	require.Equal(t, []uuid.UUID{assignee.ID}, recipientsOf(rig.sent), "one mail, to the person who holds it")
}

// flakyOutbox fails the nth row insert once, like a database that blips mid-split.
// Split is all or nothing, as the real store's transaction is.
type flakyOutbox struct {
	*fakeOutbox
	inserts, failAt int
}

func (f *flakyOutbox) hit() error {
	f.inserts++
	if f.inserts == f.failAt {
		return context.DeadlineExceeded
	}
	return nil
}

func (f *flakyOutbox) Enqueue(ctx context.Context, id uuid.UUID, ch string, ev []byte) error {
	if err := f.hit(); err != nil {
		return err
	}
	return f.fakeOutbox.Enqueue(ctx, id, ch, ev)
}

func (f *flakyOutbox) Split(ctx context.Context, parent uuid.UUID, rows []notification.NewRow) error {
	for range rows {
		if err := f.hit(); err != nil {
			return err // nothing was inserted, and the parent stays
		}
	}
	for _, r := range rows {
		if err := f.fakeOutbox.Enqueue(ctx, r.ID, r.Channel, r.Event); err != nil {
			return err
		}
	}
	return f.fakeOutbox.Delete(ctx, parent)
}

// A child insert that fails after an earlier child was queued must leave nothing
// behind: all of a change's rows are queued, and its row settled, in one step.
// Otherwise the first child is sent, deleted, and queued again by the retry.
func TestChangeSplitter_AFailedInsertLeavesNoChildAndEachMailIsSentOnce(t *testing.T) {
	assignee, follower := staffUser("assignee"), staffUser("follower")
	ev := changeRow("guest@example.com", &assignee.ID, nil, nil)
	outbox := &flakyOutbox{fakeOutbox: newFakeOutbox(), failAt: 2} // the second child
	sent := &recorder{}
	split := NewChangeSplitter(sent, outbox, nil, newFakeStaffUsers(assignee, follower), fakeStaffGroups{},
		fakeStaffFollowers{ev.TicketID: {follower.ID}}, noOverrides, quietLog())
	w := NewWorker(outbox, map[string]notification.Dispatcher{"email": split}, quietLog())
	w.Backoff = func(int) time.Duration { return time.Hour }
	b, err := encodeEvent(ev)
	require.NoError(t, err)
	parent := uuid.New()
	require.NoError(t, outbox.fakeOutbox.Enqueue(context.Background(), parent, "email", b))

	for range 5 { // everything due now: the change fails, and nothing else is queued
		n, err := w.RunOnce(context.Background())
		require.NoError(t, err)
		if n == 0 {
			break
		}
	}
	require.Empty(t, sent.got, "no child was queued, so none was sent")
	require.Len(t, outbox.rows, 1, "only the change's own row, waiting to be retried")

	for range 10 {
		outbox.makeDue()
		n, err := w.RunOnce(context.Background())
		require.NoError(t, err)
		if n == 0 {
			break
		}
	}
	require.Len(t, sent.got, 3, "the guest, the assignee and the follower, once each")
	require.Equal(t, 1, countRequester(sent), "and the guest only once")
}

func countRequester(r *recorder) int {
	n, _ := mailsTo(r)
	return n
}

// An existing user somebody else filed a ticket for (#444) is mailed through the
// same row as a guest would be, with the address on the event and no guest
// link: one "We have received your ticket" mail, to that address, once.
func filedForAccount(addr string, filer, assignee *uuid.UUID) notification.Event {
	ev := routedCreate(filer, assignee)
	ev.Recipient = addr
	if assignee == nil {
		ev.Kinds, ev.RoutedAssignment, ev.Ticket = []notification.EventType{notification.EventTicketCreated}, false, nil
	}
	return ev
}

func TestChangeSplitter_AnAccountFiledForIsMailedOnce(t *testing.T) {
	filer := staffUser("filer")
	rig := newSplitRig(newFakeStaffUsers(filer), fakeStaffGroups{}, fakeStaffFollowers{})
	rig.queue(t, filedForAccount("requester@example.com", &filer.ID, nil))

	rig.drain(t)

	require.Len(t, rig.sent.got, 1)
	got := rig.sent.got[0]
	require.Equal(t, notification.EventTicketCreated, got.Type)
	require.Equal(t, "requester@example.com", got.Recipient)
	require.False(t, got.GuestLink)
	require.Nil(t, got.StaffUserID, "the requester's mail, not a staff one")
}

// The person filed for is also who routing assigned the ticket to: one mail to
// the address, the staff one.
func TestChangeSplitter_AnAccountFiledForWhoIsAlsoTheAssigneeGetsOneMail(t *testing.T) {
	filer, assignee := staffUser("filer"), staffUser("assignee") // assignee@example.com
	rig := newSplitRig(newFakeStaffUsers(filer, assignee), fakeStaffGroups{}, fakeStaffFollowers{})
	rig.queue(t, filedForAccount("assignee@example.com", &filer.ID, &assignee.ID))

	rig.drain(t)

	req, st := mailsTo(rig.sent)
	require.Equal(t, 0, req)
	require.Equal(t, 1, st)
}
