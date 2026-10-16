package notify

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/config"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// ── fakes ────────────────────────────────────────────────────────────────────

type fakeStaffUsers struct {
	byID map[uuid.UUID]user.User
	err  error
	// failFirst makes the first n lookups fail, then succeed: a database that
	// blips once.
	failFirst int
}

func newFakeStaffUsers(us ...user.User) *fakeStaffUsers {
	f := &fakeStaffUsers{byID: map[uuid.UUID]user.User{}}
	for _, u := range us {
		f.byID[u.ID] = u
	}
	return f
}

func (f *fakeStaffUsers) GetByIDs(_ context.Context, ids []uuid.UUID) ([]user.User, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.failFirst > 0 {
		f.failFirst--
		return nil, errors.New("database blipped")
	}
	var out []user.User
	for _, id := range ids {
		if u, ok := f.byID[id]; ok {
			out = append(out, u)
		}
	}
	return out, nil
}

type fakeStaffGroups map[uuid.UUID][]uuid.UUID

func (f fakeStaffGroups) ListMailableMembers(_ context.Context, id uuid.UUID) ([]uuid.UUID, error) {
	return f[id], nil
}

// fakeStaffFollowers maps a ticket to the followers the store would return as
// mailable. err, when set, is returned for every ticket.
type fakeStaffFollowers map[uuid.UUID][]uuid.UUID

func (f fakeStaffFollowers) ListMailableFollowerIDs(_ context.Context, id uuid.UUID) ([]uuid.UUID, error) {
	return f[id], nil
}

type failingStaffFollowers struct{}

func (failingStaffFollowers) ListMailableFollowerIDs(context.Context, uuid.UUID) ([]uuid.UUID, error) {
	return nil, errors.New("database down")
}

// fakeStaffAccess stands in for the send-time check that the person is still
// entitled to hear about the ticket.
type fakeStaffAccess struct {
	ok   bool
	err  error
	seen []notification.StaffSwitch
}

func (f *fakeStaffAccess) StillEntitled(_ context.Context, _ user.User, _ uuid.UUID, reason notification.StaffSwitch) (bool, error) {
	f.seen = append(f.seen, reason)
	return f.ok, f.err
}

func staffUser(name string) user.User {
	return user.User{
		ID: uuid.New(), Email: name + "@example.com", DisplayName: name, Role: user.RoleStaff,
		EmailNotifyAssigned: true, EmailNotifyActivity: true, EmailNotifyGroup: true,
	}
}

func recipientsOf(r *recorder) []uuid.UUID {
	var out []uuid.UUID
	for _, ev := range r.got {
		if ev.StaffUserID != nil {
			out = append(out, *ev.StaffUserID)
		}
	}
	return out
}

func noOverrides(context.Context) notification.StaffOverrides { return notification.StaffOverrides{} }

func newFanout(next notification.Dispatcher, users StaffUsers, groups StaffGroups) *StaffFanout {
	return NewStaffFanout(next, users, groups, fakeStaffFollowers{}, noOverrides, quietLog())
}

func assignedEvent(actor *uuid.UUID, assignee, group *uuid.UUID) notification.Event {
	return notification.Event{
		Type: notification.EventTicketAssigned, Kinds: []notification.EventType{notification.EventTicketAssigned},
		TicketID: uuid.New(), ActorID: actor,
		TrackingNumber: "GHD-2026-000007", OccurredAt: time.Now(),
		Ticket: &notification.TicketState{AssigneeUserID: assignee, AssigneeGroupID: group},
	}
}

func requesterReply(assignee, group *uuid.UUID) notification.Event {
	return notification.Event{
		Type: notification.EventTicketReplied, Kinds: []notification.EventType{notification.EventTicketReplied},
		TicketID: uuid.New(), FromRequester: true,
		TrackingNumber: "GHD-2026-000007", OccurredAt: time.Now(),
		Ticket: &notification.TicketState{AssigneeUserID: assignee, AssigneeGroupID: group},
	}
}

// ── fan-out: assignment ──────────────────────────────────────────────────────

// The fan-out runs in the worker on a change's email row (#506). It hands on
// the requester's mail when the change has one, and staff mail beside it; the
// webhook channel has a row of its own and is not its business. A change with
// nobody to mail as a requester hands on staff mail only.
func TestStaffFanout_HandsOnTheRequestersMailFirstWhenThereIsOne(t *testing.T) {
	a := staffUser("assignee")
	next := &recorder{}
	ev := assignedEvent(nil, &a.ID, nil)
	ev.Kinds = []notification.EventType{notification.EventTicketStatusChanged, notification.EventTicketAssigned}
	ev.Type = notification.EventTicketStatusChanged
	ev.Recipient = "guest@example.com"

	require.NoError(t, newFanout(next, newFakeStaffUsers(a), fakeStaffGroups{}).Dispatch(context.Background(), ev))

	require.Len(t, next.got, 2)
	require.Equal(t, notification.EventTicketStatusChanged, next.got[0].Type)
	require.Empty(t, next.got[0].Kinds, "a single mail: it is sent as it is, not split again")
	require.Equal(t, "guest@example.com", next.got[0].Recipient)
	require.Equal(t, notification.EventStaffAssigned, next.got[1].Type)
}

func TestStaffFanout_NoRequesterMailWhenThereIsNoRecipient(t *testing.T) {
	a := staffUser("assignee")
	next := &recorder{}
	ev := assignedEvent(nil, &a.ID, nil)

	require.NoError(t, newFanout(next, newFakeStaffUsers(a), fakeStaffGroups{}).Dispatch(context.Background(), ev))

	require.Len(t, next.got, 1)
	require.Equal(t, notification.EventStaffAssigned, next.got[0].Type)
}

func TestStaffFanout_AssignmentMailsTheAssigneeAndTheGroup(t *testing.T) {
	assignee, m1, m2 := staffUser("assignee"), staffUser("m1"), staffUser("m2")
	g := uuid.New()
	next := &recorder{}
	f := newFanout(next, newFakeStaffUsers(assignee, m1, m2), fakeStaffGroups{g: {m1.ID, m2.ID}})
	ev := assignedEvent(nil, &assignee.ID, &g)

	require.NoError(t, f.Dispatch(context.Background(), ev))

	require.Equal(t, []uuid.UUID{assignee.ID, m1.ID, m2.ID}, recipientsOf(next))
	for _, got := range next.got {
		require.Equal(t, notification.EventStaffAssigned, got.Type)
		require.Equal(t, ev.TicketID, got.TicketID)
		require.Equal(t, "GHD-2026-000007", got.TrackingNumber)
		require.Empty(t, got.Recipient, "the address is read at send time, not queued")
	}
}

// The reason decides the wording of the email, and what is checked again at
// send time: a group member is told it is their group's ticket.
func TestStaffFanout_EachRowSaysWhyThePersonIsBeingMailed(t *testing.T) {
	assignee, member := staffUser("assignee"), staffUser("member")
	g := uuid.New()
	next := &recorder{}
	f := newFanout(next, newFakeStaffUsers(assignee, member), fakeStaffGroups{g: {assignee.ID, member.ID}})

	require.NoError(t, f.Dispatch(context.Background(), assignedEvent(nil, &assignee.ID, &g)))

	reasons := map[uuid.UUID]notification.StaffSwitch{}
	for _, ev := range next.got {
		reasons[*ev.StaffUserID] = ev.StaffReason
	}
	require.Equal(t, map[uuid.UUID]notification.StaffSwitch{
		assignee.ID: notification.SwitchAssigned, // holding it directly wins over being in the group
		member.ID:   notification.SwitchGroup,
	}, reasons)
}

func TestStaffFanout_OneEmailPerPersonEvenInTwoRoles(t *testing.T) {
	assignee, other := staffUser("assignee"), staffUser("other")
	g := uuid.New()
	next := &recorder{}
	f := newFanout(next, newFakeStaffUsers(assignee, other), fakeStaffGroups{g: {assignee.ID, other.ID, other.ID}})

	require.NoError(t, f.Dispatch(context.Background(), assignedEvent(nil, &assignee.ID, &g)))

	require.Equal(t, []uuid.UUID{assignee.ID, other.ID}, recipientsOf(next))
}

func TestStaffFanout_NobodyIsMailedAboutTheirOwnAction(t *testing.T) {
	actor, other := staffUser("actor"), staffUser("other")
	g := uuid.New()
	next := &recorder{}
	f := newFanout(next, newFakeStaffUsers(actor, other), fakeStaffGroups{g: {actor.ID, other.ID}})

	// Assigning a ticket to yourself and to your own group.
	require.NoError(t, f.Dispatch(context.Background(), assignedEvent(&actor.ID, &actor.ID, &g)))

	require.Equal(t, []uuid.UUID{other.ID}, recipientsOf(next))
}

func TestStaffFanout_EachSwitchIsHonoured(t *testing.T) {
	g := uuid.New()
	cases := []struct {
		name   string
		off    func(*user.User)
		event  func(a, g *uuid.UUID) notification.Event
		wantTo bool
	}{
		{"assigned off", func(u *user.User) { u.EmailNotifyAssigned = false },
			func(a, g *uuid.UUID) notification.Event { return assignedEvent(nil, a, nil) }, false},
		{"assigned off does not silence the group switch", func(u *user.User) { u.EmailNotifyAssigned = false },
			func(a, gr *uuid.UUID) notification.Event { return assignedEvent(nil, nil, gr) }, true},
		{"group off", func(u *user.User) { u.EmailNotifyGroup = false },
			func(a, gr *uuid.UUID) notification.Event { return assignedEvent(nil, nil, gr) }, false},
		{"activity off", func(u *user.User) { u.EmailNotifyActivity = false },
			func(a, gr *uuid.UUID) notification.Event { return requesterReply(a, nil) }, false},
		{"activity off does not silence assignment", func(u *user.User) { u.EmailNotifyActivity = false },
			func(a, gr *uuid.UUID) notification.Event { return assignedEvent(nil, a, nil) }, true},
		{"all on", func(*user.User) {},
			func(a, gr *uuid.UUID) notification.Event { return requesterReply(a, nil) }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := staffUser("u")
			tc.off(&u)
			next := &recorder{}
			f := newFanout(next, newFakeStaffUsers(u), fakeStaffGroups{g: {u.ID}})
			require.NoError(t, f.Dispatch(context.Background(), tc.event(&u.ID, &g)))
			if tc.wantTo {
				require.Equal(t, []uuid.UUID{u.ID}, recipientsOf(next))
			} else {
				require.Empty(t, recipientsOf(next))
			}
		})
	}
}

func TestStaffFanout_AdminOverrideForcesASwitchForEveryone(t *testing.T) {
	quiet, loud := staffUser("quiet"), staffUser("loud")
	quiet.EmailNotifyAssigned = false
	g := uuid.New()

	run := func(ov notification.StaffOverrides, ev notification.Event) []uuid.UUID {
		next := &recorder{}
		f := NewStaffFanout(next, newFakeStaffUsers(quiet, loud), fakeStaffGroups{g: {quiet.ID, loud.ID}}, fakeStaffFollowers{},
			func(context.Context) notification.StaffOverrides { return ov }, quietLog())
		require.NoError(t, f.Dispatch(context.Background(), ev))
		return recipientsOf(next)
	}

	// On: the person who turned it off is mailed too.
	require.ElementsMatch(t, []uuid.UUID{quiet.ID, loud.ID},
		run(notification.StaffOverrides{Group: notification.OverrideOn}, assignedEvent(nil, nil, &g)))
	// Off: the person who left it on is not.
	require.Empty(t, run(notification.StaffOverrides{Group: notification.OverrideOff}, assignedEvent(nil, nil, &g)))
	// The override is per switch.
	require.Equal(t, []uuid.UUID{loud.ID},
		run(notification.StaffOverrides{Assigned: notification.OverrideUser}, assignedEvent(nil, &loud.ID, nil)))
	require.Equal(t, []uuid.UUID{quiet.ID},
		run(notification.StaffOverrides{Assigned: notification.OverrideOn}, assignedEvent(nil, &quiet.ID, nil)))
	// A forced-on switch still never mails the person who acted.
	require.Empty(t, run(notification.StaffOverrides{Assigned: notification.OverrideOn}, assignedEvent(&quiet.ID, &quiet.ID, nil)))
}

func TestStaffFanout_OnlyPeopleWhoCanReceiveStaffMail(t *testing.T) {
	ok := staffUser("ok")
	disabled, deleted, requester := staffUser("disabled"), staffUser("deleted"), staffUser("requester")
	disabled.Disabled = true
	now := time.Now()
	deleted.DeletedAt = &now
	requester.Role = user.RoleUser
	g := uuid.New()
	next := &recorder{}
	f := newFanout(next, newFakeStaffUsers(ok, disabled, deleted, requester),
		fakeStaffGroups{g: {ok.ID, disabled.ID, deleted.ID, requester.ID, uuid.New()}})

	require.NoError(t, f.Dispatch(context.Background(), assignedEvent(nil, nil, &g)))

	require.Equal(t, []uuid.UUID{ok.ID}, recipientsOf(next))
}

// A service account (an OAuth client's identity, #490) is never mailed, as a
// group member or as the assignee.
func TestStaffFanout_ServiceAccountsAreNeverMailed(t *testing.T) {
	human, bot := staffUser("human"), staffUser("bot")
	bot.ServiceAccount = true
	g := uuid.New()

	t.Run("as a member of the group", func(t *testing.T) {
		next := &recorder{}
		f := newFanout(next, newFakeStaffUsers(human, bot), fakeStaffGroups{g: {human.ID, bot.ID}})
		require.NoError(t, f.Dispatch(context.Background(), requesterReply(nil, &g)))
		require.Equal(t, []uuid.UUID{human.ID}, recipientsOf(next))
	})
	t.Run("as the assignee", func(t *testing.T) {
		next := &recorder{}
		f := newFanout(next, newFakeStaffUsers(bot), fakeStaffGroups{})
		require.NoError(t, f.Dispatch(context.Background(), assignedEvent(nil, &bot.ID, nil)))
		require.Empty(t, recipientsOf(next))
	})
	t.Run("and a row queued before the account became one is dropped at send time", func(t *testing.T) {
		next := &recorder{}
		d := NewStaffMailDispatcher(next, newFakeStaffUsers(bot), &fakeStaffAccess{ok: true})
		require.NoError(t, d.Dispatch(context.Background(), staffEvent(bot.ID)))
		require.Empty(t, next.got)
	})
}

func TestStaffFanout_UnassigningMailsNobody(t *testing.T) {
	next := &recorder{}
	require.NoError(t, newFanout(next, newFakeStaffUsers(), fakeStaffGroups{}).Dispatch(context.Background(), assignedEvent(nil, nil, nil)))
	require.Empty(t, next.got, "nobody to mail")
}

// ── fan-out: requester replies ───────────────────────────────────────────────

func TestStaffFanout_RequesterReplyMailsTheAssignee(t *testing.T) {
	assignee, m := staffUser("assignee"), staffUser("m")
	g := uuid.New()
	next := &recorder{}
	f := newFanout(next, newFakeStaffUsers(assignee, m), fakeStaffGroups{g: {m.ID}})

	require.NoError(t, f.Dispatch(context.Background(), requesterReply(&assignee.ID, &g)))

	// The group is not also mailed when somebody personally holds the ticket.
	require.Equal(t, []uuid.UUID{assignee.ID}, recipientsOf(next))
	require.Equal(t, notification.EventStaffRequesterReplied, next.got[0].Type)
}

func TestStaffFanout_RequesterReplyOnAGroupTicketMailsTheGroup(t *testing.T) {
	m1, m2 := staffUser("m1"), staffUser("m2")
	g := uuid.New()
	next := &recorder{}
	f := newFanout(next, newFakeStaffUsers(m1, m2), fakeStaffGroups{g: {m1.ID, m2.ID}})

	require.NoError(t, f.Dispatch(context.Background(), requesterReply(nil, &g)))

	require.Equal(t, []uuid.UUID{m1.ID, m2.ID}, recipientsOf(next))
}

func TestStaffFanout_RequesterReplyOnAnUnassignedTicketMailsNobody(t *testing.T) {
	next := &recorder{}
	require.NoError(t, newFanout(next, newFakeStaffUsers(), fakeStaffGroups{}).Dispatch(context.Background(), requesterReply(nil, nil)))
	require.Empty(t, next.got)
}

// Only a requester's public reply is a reason to mail staff.
func TestStaffFanout_StaffRepliesAndOtherEventsMailNobody(t *testing.T) {
	a := staffUser("a")
	staffReply := requesterReply(&a.ID, nil)
	staffReply.FromRequester = false
	closed := assignedEvent(nil, &a.ID, nil)
	closed.Type, closed.Kinds = notification.EventTicketClosed, []notification.EventType{notification.EventTicketClosed}

	for name, ev := range map[string]notification.Event{"a staff reply or internal note": staffReply, "a ticket closing": closed} {
		t.Run(name, func(t *testing.T) {
			next := &recorder{}
			require.NoError(t, newFanout(next, newFakeStaffUsers(a), fakeStaffGroups{}).Dispatch(context.Background(), ev))
			require.Empty(t, next.got)
		})
	}
}

// ── fan-out: followers (#441) ────────────────────────────────────────────────

func fanoutWithFollowers(next notification.Dispatcher, users StaffUsers, groups StaffGroups, fol StaffFollowers) *StaffFanout {
	return NewStaffFanout(next, users, groups, fol, noOverrides, quietLog())
}

// A follower gets the same two events as the person who holds the ticket, under
// the activity switch, and is told it is a ticket they follow.
func TestStaffFanout_FollowersAreMailedForAssignmentAndRequesterReplies(t *testing.T) {
	assignee, follower := staffUser("assignee"), staffUser("follower")
	for name, ev := range map[string]notification.Event{
		"assignment":      assignedEvent(nil, &assignee.ID, nil),
		"requester reply": requesterReply(&assignee.ID, nil),
	} {
		t.Run(name, func(t *testing.T) {
			next := &recorder{}
			f := fanoutWithFollowers(next, newFakeStaffUsers(assignee, follower), fakeStaffGroups{},
				fakeStaffFollowers{ev.TicketID: {follower.ID}})

			require.NoError(t, f.Dispatch(context.Background(), ev))

			require.Equal(t, []uuid.UUID{assignee.ID, follower.ID}, recipientsOf(next))
			last := next.got[len(next.got)-1]
			require.Equal(t, notification.SwitchFollow, last.StaffReason)
		})
	}
}

func TestStaffFanout_FollowersAreMailedOnATicketNobodyHolds(t *testing.T) {
	follower := staffUser("follower")
	ev := requesterReply(nil, nil)
	next := &recorder{}
	f := fanoutWithFollowers(next, newFakeStaffUsers(follower), fakeStaffGroups{}, fakeStaffFollowers{ev.TicketID: {follower.ID}})

	require.NoError(t, f.Dispatch(context.Background(), ev))

	require.Equal(t, []uuid.UUID{follower.ID}, recipientsOf(next))
}

// One email per person per event across the three roles: the assignee who also
// follows, the group member who also follows, and a follower in both.
func TestStaffFanout_OneEmailPerPersonAcrossAssigneeGroupAndFollower(t *testing.T) {
	assignee, member, both, plain := staffUser("assignee"), staffUser("member"), staffUser("both"), staffUser("plain")
	g := uuid.New()
	ev := assignedEvent(nil, &assignee.ID, &g)
	next := &recorder{}
	f := fanoutWithFollowers(next,
		newFakeStaffUsers(assignee, member, both, plain),
		fakeStaffGroups{g: {member.ID, both.ID}},
		fakeStaffFollowers{ev.TicketID: {assignee.ID, member.ID, both.ID, plain.ID}})

	require.NoError(t, f.Dispatch(context.Background(), ev))

	require.Equal(t, []uuid.UUID{assignee.ID, member.ID, both.ID, plain.ID}, recipientsOf(next), "each exactly once")
	reasons := map[uuid.UUID]notification.StaffSwitch{}
	for _, e := range next.got {
		reasons[*e.StaffUserID] = e.StaffReason
	}
	require.Equal(t, notification.SwitchAssigned, reasons[assignee.ID], "holding it wins")
	require.Equal(t, notification.SwitchGroup, reasons[member.ID])
	require.Equal(t, notification.SwitchGroup, reasons[both.ID])
	require.Equal(t, notification.SwitchFollow, reasons[plain.ID])
}

// Erik, 2026-10-14: the assignee always, and the assigned group's members when
// the group itself is mailed for the event (an assignment, or a reply on a
// ticket nobody holds), get no follower mail. Their own switches decide, and
// following on top does not bring back what they turned off.
func TestStaffFanout_TheAssigneeAndAMailedGroupNeverGetFollowerMail(t *testing.T) {
	assignee, member := staffUser("assignee"), staffUser("member")
	assignee.EmailNotifyAssigned, assignee.EmailNotifyActivity = false, false
	member.EmailNotifyGroup = false
	g := uuid.New()
	for name, ev := range map[string]notification.Event{
		"assignment":                assignedEvent(nil, &assignee.ID, &g),
		"reply on a group's ticket": requesterReply(nil, &g),
		"assignment to the group":   assignedEvent(nil, nil, &g),
	} {
		t.Run(name, func(t *testing.T) {
			next := &recorder{}
			f := fanoutWithFollowers(next, newFakeStaffUsers(assignee, member), fakeStaffGroups{g: {member.ID}},
				fakeStaffFollowers{ev.TicketID: {assignee.ID, member.ID}})

			require.NoError(t, f.Dispatch(context.Background(), ev))

			require.Empty(t, recipientsOf(next))
		})
	}
}

// Ann holds a ticket in group G. Mia is in G and follows it. The group is not
// mailed for a reply while Ann holds the ticket, so Mia's follow counts.
func TestStaffFanout_AGroupMemberWhoFollowsAHeldTicketIsMailedOnAReply(t *testing.T) {
	ann, mia := staffUser("ann"), staffUser("mia")
	mia.EmailNotifyGroup = false // irrelevant: this is not a group mail
	g := uuid.New()
	ev := requesterReply(&ann.ID, &g)
	next := &recorder{}
	f := fanoutWithFollowers(next, newFakeStaffUsers(ann, mia), fakeStaffGroups{g: {ann.ID, mia.ID}},
		fakeStaffFollowers{ev.TicketID: {ann.ID, mia.ID}})

	require.NoError(t, f.Dispatch(context.Background(), ev))

	require.Equal(t, []uuid.UUID{ann.ID, mia.ID}, recipientsOf(next), "Ann as the assignee, Mia as a follower, once each")
	require.Equal(t, notification.SwitchFollow, next.got[len(next.got)-1].StaffReason)
}

// The group was mailed for an assignment, so a member's own group switch
// decides, whether or not they follow.
func TestStaffFanout_AGroupMemberWithTheSwitchOffGetsNothingOnAnAssignment(t *testing.T) {
	member := staffUser("member")
	member.EmailNotifyGroup = false
	g := uuid.New()
	ev := assignedEvent(nil, nil, &g)
	next := &recorder{}
	f := fanoutWithFollowers(next, newFakeStaffUsers(member), fakeStaffGroups{g: {member.ID}},
		fakeStaffFollowers{ev.TicketID: {member.ID}})

	require.NoError(t, f.Dispatch(context.Background(), ev))

	require.Empty(t, recipientsOf(next))
}

// A ticket nobody holds is mailed to its group on a reply; a member who also
// follows it is mailed once, under the group.
func TestStaffFanout_AGroupMemberFollowingAnUnheldTicketIsMailedOnceOnAReply(t *testing.T) {
	member := staffUser("member")
	g := uuid.New()
	ev := requesterReply(nil, &g)
	next := &recorder{}
	f := fanoutWithFollowers(next, newFakeStaffUsers(member), fakeStaffGroups{g: {member.ID}},
		fakeStaffFollowers{ev.TicketID: {member.ID}})

	require.NoError(t, f.Dispatch(context.Background(), ev))

	require.Equal(t, []uuid.UUID{member.ID}, recipientsOf(next))
	require.Equal(t, notification.SwitchGroup, next.got[0].StaffReason)
}

// Never mail the person who made the change, even though they follow.
func TestStaffFanout_ASelfFollowingActorIsNotMailed(t *testing.T) {
	actor, other := staffUser("actor"), staffUser("other")
	ev := assignedEvent(&actor.ID, &other.ID, nil)
	next := &recorder{}
	f := fanoutWithFollowers(next, newFakeStaffUsers(actor, other), fakeStaffGroups{}, fakeStaffFollowers{ev.TicketID: {actor.ID}})

	require.NoError(t, f.Dispatch(context.Background(), ev))

	require.Equal(t, []uuid.UUID{other.ID}, recipientsOf(next))
}

// Followers answer to the "activity" switch and to the administrator's
// override of it, not to the assignment switch.
func TestStaffFanout_FollowersAnswerToTheActivitySwitchAndItsOverride(t *testing.T) {
	quiet, loud := staffUser("quiet"), staffUser("loud")
	quiet.EmailNotifyActivity = false
	ev := requesterReply(nil, nil)

	run := func(ov notification.StaffOverrides) []uuid.UUID {
		next := &recorder{}
		f := NewStaffFanout(next, newFakeStaffUsers(quiet, loud), fakeStaffGroups{}, fakeStaffFollowers{ev.TicketID: {quiet.ID, loud.ID}},
			func(context.Context) notification.StaffOverrides { return ov }, quietLog())
		require.NoError(t, f.Dispatch(context.Background(), ev))
		return recipientsOf(next)
	}

	require.Equal(t, []uuid.UUID{loud.ID}, run(notification.StaffOverrides{}))
	require.ElementsMatch(t, []uuid.UUID{quiet.ID, loud.ID}, run(notification.StaffOverrides{Activity: notification.OverrideOn}))
	require.Empty(t, run(notification.StaffOverrides{Activity: notification.OverrideOff}))
	// The assignment switch is not the one a follower is gated by.
	require.Equal(t, []uuid.UUID{quiet.ID, loud.ID}, run(notification.StaffOverrides{Assigned: notification.OverrideOff, Activity: notification.OverrideOn}))
}

func TestStaffFanout_FollowersWhoCannotReceiveStaffMailAreSkipped(t *testing.T) {
	ok, bot, disabled, requester := staffUser("ok"), staffUser("bot"), staffUser("disabled"), staffUser("requester")
	bot.ServiceAccount = true
	disabled.Disabled = true
	requester.Role = user.RoleUser
	ev := requesterReply(nil, nil)
	next := &recorder{}
	f := fanoutWithFollowers(next, newFakeStaffUsers(ok, bot, disabled, requester), fakeStaffGroups{},
		fakeStaffFollowers{ev.TicketID: {ok.ID, bot.ID, disabled.ID, requester.ID}})

	require.NoError(t, f.Dispatch(context.Background(), ev))

	require.Equal(t, []uuid.UUID{ok.ID}, recipientsOf(next))
}

// Not being able to list followers costs the followers their mail and nobody
// else theirs.
func TestStaffFanout_AFollowerLookupFailureStillMailsTheAssignee(t *testing.T) {
	assignee := staffUser("assignee")
	next := &recorder{}
	f := fanoutWithFollowers(next, newFakeStaffUsers(assignee), fakeStaffGroups{}, failingStaffFollowers{})

	require.NoError(t, f.Dispatch(context.Background(), requesterReply(&assignee.ID, nil)))

	require.Equal(t, []uuid.UUID{assignee.ID}, recipientsOf(next))
}

func TestEventToEmail_FollowersAreToldItIsATicketTheyFollow(t *testing.T) {
	d := &EmailDispatcher{cfg: &config.Config{BaseURL: "https://help.example.com"}}
	for _, tc := range []struct {
		typ      notification.EventType
		template string
		subject  string
	}{
		{notification.EventStaffAssigned, "staff_assigned_follow.tmpl", "A ticket you follow is assigned: [GHD-2026-000007]"},
		{notification.EventStaffRequesterReplied, "staff_requester_replied_follow.tmpl", "The requester replied on a ticket you follow: [GHD-2026-000007]"},
	} {
		t.Run(string(tc.typ), func(t *testing.T) {
			name, subject, _, data, ok := d.eventToEmail(notification.Event{
				Type: tc.typ, TicketID: uuid.New(), TrackingNumber: "GHD-2026-000007",
				Recipient: "staff@example.com", StaffReason: notification.SwitchFollow,
			})
			require.True(t, ok)
			require.Equal(t, tc.template, name)
			require.Equal(t, tc.subject, subject)
			require.Len(t, data, 2, "still only the tracking number and a link")
		})
	}
}

// ── fan-out: failures ────────────────────────────────────────────────────────

// Not being able to find out who to mail queues NOTHING and is reported: the
// worker then tries the change again from a clean start. Handing the requester's
// mail on first would let it be sent and deleted before the retry, which would
// queue it a second time (its id is free once the row is gone).
func TestStaffFanout_ALookupFailureQueuesNothing(t *testing.T) {
	a := staffUser("a")
	users := newFakeStaffUsers(a)
	users.err = errors.New("database down")
	next := &recorder{}
	ev := assignedEvent(nil, &a.ID, nil)
	ev.Kinds = []notification.EventType{notification.EventTicketStatusChanged, notification.EventTicketAssigned}
	ev.Recipient = "guest@example.com"

	err := newFanout(next, users, fakeStaffGroups{}).Dispatch(context.Background(), ev)

	require.Error(t, err)
	require.Empty(t, next.got)
}

func TestStaffFanout_AFailureToHandOnIsReturned(t *testing.T) {
	a := staffUser("a")
	next := &recorder{fail: errors.New("queue full")}
	err := newFanout(next, newFakeStaffUsers(a), fakeStaffGroups{}).Dispatch(context.Background(), assignedEvent(nil, &a.ID, nil))
	require.ErrorContains(t, err, "queue full")
}

// ── send time ────────────────────────────────────────────────────────────────

func staffEvent(id uuid.UUID) notification.Event {
	return notification.Event{
		Type: notification.EventStaffAssigned, TicketID: uuid.New(), TrackingNumber: "GHD-2026-000007", StaffUserID: &id,
	}
}

func TestStaffMailDispatcher_ReadsTheAddressAtSendTime(t *testing.T) {
	u := staffUser("u")
	next := &recorder{}
	d := NewStaffMailDispatcher(next, newFakeStaffUsers(u), &fakeStaffAccess{ok: true})

	require.NoError(t, d.Dispatch(context.Background(), staffEvent(u.ID)))

	require.Len(t, next.got, 1)
	require.Equal(t, "u@example.com", next.got[0].Recipient)
}

// Mail queued before the person was disabled, deleted or moved to a requester
// role is dropped, not sent, and is not an error to retry.
func TestStaffMailDispatcher_SkipsAnAccountThatCannotReceiveItAnymore(t *testing.T) {
	disabled, requester := staffUser("d"), staffUser("r")
	disabled.Disabled = true
	requester.Role = user.RoleUser
	next := &recorder{}
	d := NewStaffMailDispatcher(next, newFakeStaffUsers(disabled, requester), &fakeStaffAccess{ok: true})

	for _, id := range []uuid.UUID{disabled.ID, requester.ID, uuid.New()} {
		require.NoError(t, d.Dispatch(context.Background(), staffEvent(id)))
	}
	require.Empty(t, next.got)
}

// A lookup that fails is retried by the worker.
func TestStaffMailDispatcher_ALookupFailureIsRetried(t *testing.T) {
	u := staffUser("u")
	users := newFakeStaffUsers(u)
	users.err = errors.New("database down")
	require.Error(t, NewStaffMailDispatcher(&recorder{}, users, &fakeStaffAccess{ok: true}).Dispatch(context.Background(), staffEvent(u.ID)))
}

// Mail queued for somebody who has since lost the ticket (reassigned, taken
// out of the group, or no longer able to see it) is dropped, not sent.
func TestStaffMailDispatcher_SkipsAPersonWhoIsNoLongerEntitled(t *testing.T) {
	u := staffUser("u")
	next := &recorder{}
	access := &fakeStaffAccess{ok: false}
	d := NewStaffMailDispatcher(next, newFakeStaffUsers(u), access)

	ev := staffEvent(u.ID)
	ev.StaffReason = notification.SwitchGroup
	require.NoError(t, d.Dispatch(context.Background(), ev))

	require.Empty(t, next.got)
	require.Equal(t, []notification.StaffSwitch{notification.SwitchGroup}, access.seen, "the check is told why the mail was queued")
}

// A check that cannot be made is retried, not treated as a no.
func TestStaffMailDispatcher_AFailedEntitlementCheckIsRetried(t *testing.T) {
	u := staffUser("u")
	next := &recorder{}
	d := NewStaffMailDispatcher(next, newFakeStaffUsers(u), &fakeStaffAccess{err: errors.New("database down")})
	require.Error(t, d.Dispatch(context.Background(), staffEvent(u.ID)))
	require.Empty(t, next.got)
}

// Everything else is the requester's mail or a webhook and is not touched.
func TestStaffMailDispatcher_PassesOtherEventsStraightThrough(t *testing.T) {
	next := &recorder{}
	ev := sampleEvent()
	require.NoError(t, NewStaffMailDispatcher(next, newFakeStaffUsers(), &fakeStaffAccess{ok: true}).Dispatch(context.Background(), ev))
	require.Equal(t, []notification.Event{ev}, next.got)
}

// ── the mail itself ──────────────────────────────────────────────────────────

func TestEventToEmail_StaffMailCarriesOnlyTheTrackingNumberAndALink(t *testing.T) {
	d := &EmailDispatcher{cfg: &config.Config{BaseURL: "https://help.example.com"}}
	id := uuid.New()
	for _, tc := range []struct {
		typ      notification.EventType
		template string
		subject  string
	}{
		{notification.EventStaffAssigned, "staff_assigned.tmpl", "A ticket is assigned to you: [GHD-2026-000007]"},
		{notification.EventStaffRequesterReplied, "staff_requester_replied.tmpl", "The requester replied on [GHD-2026-000007]"},
	} {
		t.Run(string(tc.typ), func(t *testing.T) {
			name, subject, to, data, ok := d.eventToEmail(notification.Event{
				Type: tc.typ, TicketID: id, TrackingNumber: "GHD-2026-000007", Recipient: "staff@example.com",
				Subject: "request text", GuestToken: "must-not-appear",
				Payload: map[string]any{"ReplyBody": "request text", "reporter_email": "attacker@evil.test"},
			})
			require.True(t, ok)
			require.Equal(t, tc.template, name)
			require.Equal(t, tc.subject, subject)
			require.Equal(t, "staff@example.com", to)
			require.Equal(t, map[string]string{
				"TrackingNumber": "GHD-2026-000007",
				// Staff sign in; a staff link is never a guest link.
				"TicketURL": "https://help.example.com/tickets/" + id.String(),
			}, data)
		})
	}
}

// Somebody mailed because of their group is told it is their group's ticket,
// not that it was assigned to them.
func TestEventToEmail_GroupMembersAreToldItIsTheirGroupsTicket(t *testing.T) {
	d := &EmailDispatcher{cfg: &config.Config{BaseURL: "https://help.example.com"}}
	for _, tc := range []struct {
		typ      notification.EventType
		template string
		subject  string
	}{
		{notification.EventStaffAssigned, "staff_assigned_group.tmpl", "A ticket is assigned to your group: [GHD-2026-000007]"},
		{notification.EventStaffRequesterReplied, "staff_requester_replied_group.tmpl", "The requester replied on a ticket assigned to your group: [GHD-2026-000007]"},
	} {
		t.Run(string(tc.typ), func(t *testing.T) {
			name, subject, _, _, ok := d.eventToEmail(notification.Event{
				Type: tc.typ, TicketID: uuid.New(), TrackingNumber: "GHD-2026-000007",
				Recipient: "staff@example.com", StaffReason: notification.SwitchGroup,
			})
			require.True(t, ok)
			require.Equal(t, tc.template, name)
			require.Equal(t, tc.subject, subject)
		})
	}
}

// Both templates parse and render with the view the dispatcher builds.
func TestStaffTemplates_Render(t *testing.T) {
	d, err := NewEmailDispatcher(&config.Config{SMTPHost: "smtp.example.com", SMTPFrom: "desk@example.com", BaseURL: "https://help.example.com"})
	require.NoError(t, err)
	for _, name := range []string{"staff_assigned.tmpl", "staff_requester_replied.tmpl", "staff_assigned_group.tmpl", "staff_requester_replied_group.tmpl", "staff_assigned_follow.tmpl", "staff_requester_replied_follow.tmpl"} {
		var b strings.Builder
		require.NoError(t, d.templates.ExecuteTemplate(&b, name, map[string]string{
			"TrackingNumber": "GHD-2026-000007", "TicketURL": "https://help.example.com/tickets/x",
		}), name)
		require.Contains(t, b.String(), "GHD-2026-000007")
		require.Contains(t, b.String(), "https://help.example.com/tickets/x")
	}
}

// ── the outbox ───────────────────────────────────────────────────────────────

func TestOutbox_StaffMailIsEmailOnly(t *testing.T) {
	ev := staffEvent(uuid.New())
	require.True(t, notification.Carries("email", ev), "no Recipient is set yet: it is read at send time")
	require.False(t, notification.Carries("webhook", ev), "a webhook subscriber already has the event this came from")
	require.False(t, notification.Carries("verification", ev))
}

func TestOutboxRecord_RoundTripsTheStaffFields(t *testing.T) {
	staff, a, g := uuid.New(), uuid.New(), uuid.New()
	ev := notification.Event{
		Type: notification.EventStaffRequesterReplied, TicketID: uuid.New(), OccurredAt: time.Now().UTC().Truncate(time.Microsecond),
		StaffUserID: &staff, Ticket: &notification.TicketState{AssigneeUserID: &a, AssigneeGroupID: &g}, FromRequester: true, StaffReason: notification.SwitchGroup,
	}
	b, err := encodeEvent(ev)
	require.NoError(t, err)
	got, err := decodeEvent(b)
	require.NoError(t, err)
	require.Equal(t, &staff, got.StaffUserID)
	require.Equal(t, &a, got.Ticket.AssigneeUserID)
	require.Equal(t, &g, got.Ticket.AssigneeGroupID)
	require.True(t, got.FromRequester)
	require.Equal(t, notification.SwitchGroup, got.StaffReason)
}
