package notify

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/config"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// Staff mail on a reopen (#464, Erik's Q7): the assignee, the assigned group's
// members and the ticket's followers each get one mail, nobody gets two, and
// the person who reopened it gets none.

func reopenedEventFor(actor, assignee, group *uuid.UUID) notification.Event {
	return notification.Event{
		Type: notification.EventTicketReopened, Kinds: []notification.EventType{notification.EventTicketReopened},
		TicketID: uuid.New(), ActorID: actor,
		TrackingNumber: "GHD-2026-000007", OccurredAt: time.Now(),
		Ticket: &notification.TicketState{AssigneeUserID: assignee, AssigneeGroupID: group},
	}
}

func reasonsOf(r *recorder) map[uuid.UUID]notification.StaffSwitch {
	out := map[uuid.UUID]notification.StaffSwitch{}
	for _, e := range r.got {
		if e.StaffUserID != nil {
			out[*e.StaffUserID] = e.StaffReason
		}
	}
	return out
}

// A held ticket: the assignee and a follower are mailed, once each. The group
// is not mailed (Erik, 2026-10-14: the reply rule), so a group member who does
// not follow hears nothing.
func TestStaffFanout_ReopenOfAHeldTicketMailsTheAssigneeAndFollowersNotTheGroup(t *testing.T) {
	assignee, member, follower := staffUser("assignee"), staffUser("member"), staffUser("follower")
	g := uuid.New()
	ev := reopenedEventFor(nil, &assignee.ID, &g)
	next := &recorder{}
	f := fanoutWithFollowers(next, newFakeStaffUsers(assignee, member, follower), fakeStaffGroups{g: {member.ID}},
		fakeStaffFollowers{ev.TicketID: {follower.ID}})

	require.NoError(t, f.Dispatch(context.Background(), ev))

	require.Equal(t, []uuid.UUID{assignee.ID, follower.ID}, recipientsOf(next))
	require.Equal(t, map[uuid.UUID]notification.StaffSwitch{
		assignee.ID: notification.SwitchActivity, // under the "requester replied" switch, per Erik
		follower.ID: notification.SwitchFollow,
	}, reasonsOf(next))
	for _, e := range next.got {
		require.Equal(t, notification.EventStaffReopened, e.Type)
		require.Equal(t, ev.TicketID, e.TicketID)
		require.Equal(t, "GHD-2026-000007", e.TrackingNumber)
	}
}

// A held ticket: a group member who follows it is mailed as a follower, under
// the activity switch, whatever their group switch says (#503's rule: the group
// is not mailed for this event, so their follow counts).
func TestStaffFanout_ReopenOfAHeldTicketMailsAGroupMemberWhoFollows(t *testing.T) {
	assignee, member := staffUser("assignee"), staffUser("member")
	member.EmailNotifyGroup = false // irrelevant: this is not a group mail
	g := uuid.New()
	ev := reopenedEventFor(nil, &assignee.ID, &g)
	next := &recorder{}
	f := fanoutWithFollowers(next, newFakeStaffUsers(assignee, member), fakeStaffGroups{g: {member.ID}},
		fakeStaffFollowers{ev.TicketID: {member.ID}})

	require.NoError(t, f.Dispatch(context.Background(), ev))

	require.Equal(t, []uuid.UUID{assignee.ID, member.ID}, recipientsOf(next))
	require.Equal(t, notification.SwitchFollow, reasonsOf(next)[member.ID])
}

// An unheld ticket: the group is mailed, and a member who also follows gets
// exactly one mail, as a group member. A follower who is not in the group is
// mailed as a follower.
func TestStaffFanout_ReopenOfAnUnheldTicketMailsTheGroupOnce(t *testing.T) {
	member, both, plain := staffUser("member"), staffUser("both"), staffUser("plain")
	g := uuid.New()
	ev := reopenedEventFor(nil, nil, &g)
	next := &recorder{}
	f := fanoutWithFollowers(next, newFakeStaffUsers(member, both, plain), fakeStaffGroups{g: {member.ID, both.ID}},
		fakeStaffFollowers{ev.TicketID: {both.ID, plain.ID}})

	require.NoError(t, f.Dispatch(context.Background(), ev))

	require.Equal(t, []uuid.UUID{member.ID, both.ID, plain.ID}, recipientsOf(next), "each exactly once")
	require.Equal(t, map[uuid.UUID]notification.StaffSwitch{
		member.ID: notification.SwitchGroup,
		both.ID:   notification.SwitchGroup,
		plain.ID:  notification.SwitchFollow,
	}, reasonsOf(next))
}

// The assignee never gets follower mail; on an unheld ticket a member's group
// switch decides and following does not bring back what they turned off.
func TestStaffFanout_ReopenFollowingDoesNotOverrideTheAssigneeOrGroupSwitch(t *testing.T) {
	assignee, member := staffUser("assignee"), staffUser("member")
	assignee.EmailNotifyActivity = false
	member.EmailNotifyGroup = false
	g := uuid.New()

	held := reopenedEventFor(nil, &assignee.ID, nil)
	next := &recorder{}
	require.NoError(t, fanoutWithFollowers(next, newFakeStaffUsers(assignee), fakeStaffGroups{},
		fakeStaffFollowers{held.TicketID: {assignee.ID}}).Dispatch(context.Background(), held))
	require.Empty(t, recipientsOf(next), "the assignee who follows is still the assignee")

	unheld := reopenedEventFor(nil, nil, &g)
	next = &recorder{}
	require.NoError(t, fanoutWithFollowers(next, newFakeStaffUsers(member), fakeStaffGroups{g: {member.ID}},
		fakeStaffFollowers{unheld.TicketID: {member.ID}}).Dispatch(context.Background(), unheld))
	require.Empty(t, recipientsOf(next))
}

// Nobody is mailed about their own action, in any of the three roles. Others
// still are.
func TestStaffFanout_ReopenSkipsWhoeverReopenedIt(t *testing.T) {
	actor, other := staffUser("actor"), staffUser("other")
	g := uuid.New()
	for name, tc := range map[string]struct {
		assignee, group *uuid.UUID
		followers       []uuid.UUID
		members         []uuid.UUID
		want            []uuid.UUID
	}{
		"the assignee reopens":                 {assignee: &actor.ID, followers: []uuid.UUID{other.ID}, want: []uuid.UUID{other.ID}},
		"a group member reopens":               {group: &g, members: []uuid.UUID{actor.ID, other.ID}, want: []uuid.UUID{other.ID}},
		"a follower reopens":                   {assignee: &other.ID, followers: []uuid.UUID{actor.ID}, want: []uuid.UUID{other.ID}},
		"a requester reopens (no staff actor)": {assignee: &other.ID, want: []uuid.UUID{other.ID}},
	} {
		t.Run(name, func(t *testing.T) {
			var actorID *uuid.UUID
			if !strings.Contains(name, "requester") {
				actorID = &actor.ID
			}
			ev := reopenedEventFor(actorID, tc.assignee, tc.group)
			next := &recorder{}
			f := fanoutWithFollowers(next, newFakeStaffUsers(actor, other), fakeStaffGroups{g: tc.members},
				fakeStaffFollowers{ev.TicketID: tc.followers})

			require.NoError(t, f.Dispatch(context.Background(), ev))

			require.Equal(t, tc.want, recipientsOf(next))
		})
	}
}

// The assignee's mail sits under the "requester replied" (activity) switch, the
// group's under the group switch and a follower's under the activity switch,
// and the administrator's override of each applies.
func TestStaffFanout_ReopenHonoursTheSwitchesAndOverrides(t *testing.T) {
	g := uuid.New()
	run := func(u func(*notification.StaffPrefs), ov notification.StaffOverrides, role string) []uuid.UUID {
		person := staffUser("p")
		person.EmailNotifyAssigned, person.EmailNotifyActivity, person.EmailNotifyGroup = true, true, true
		p := notification.StaffPrefs{Assigned: true, Activity: true, Group: true}
		u(&p)
		person.EmailNotifyAssigned, person.EmailNotifyActivity, person.EmailNotifyGroup = p.Assigned, p.Activity, p.Group
		ev := reopenedEventFor(nil, nil, nil)
		fol := fakeStaffFollowers{}
		members := fakeStaffGroups{}
		switch role {
		case "assignee":
			ev.Ticket.AssigneeUserID = &person.ID
		case "group":
			ev.Ticket.AssigneeGroupID = &g
			members[g] = []uuid.UUID{person.ID}
		case "follower":
			fol[ev.TicketID] = []uuid.UUID{person.ID}
		}
		next := &recorder{}
		f := NewStaffFanout(next, newFakeStaffUsers(person), members, fol,
			func(context.Context) notification.StaffOverrides { return ov }, quietLog())
		require.NoError(t, f.Dispatch(context.Background(), ev))
		return recipientsOf(next)
	}
	none := notification.StaffOverrides{}

	require.Empty(t, run(func(p *notification.StaffPrefs) { p.Activity = false }, none, "assignee"), "the assignee's mail is the requester-replied switch")
	require.NotEmpty(t, run(func(p *notification.StaffPrefs) { p.Assigned = false }, none, "assignee"), "not the assigned-to-me switch")
	require.Empty(t, run(func(p *notification.StaffPrefs) { p.Group = false }, none, "group"))
	require.NotEmpty(t, run(func(p *notification.StaffPrefs) { p.Activity = false }, none, "group"))
	require.Empty(t, run(func(p *notification.StaffPrefs) { p.Activity = false }, none, "follower"))

	off := notification.StaffOverrides{Activity: notification.OverrideOff, Group: notification.OverrideOff}
	on := notification.StaffOverrides{Activity: notification.OverrideOn, Group: notification.OverrideOn}
	allOff := func(p *notification.StaffPrefs) { *p = notification.StaffPrefs{} }
	for _, role := range []string{"assignee", "group", "follower"} {
		require.Empty(t, run(func(*notification.StaffPrefs) {}, off, role), role+": forced off")
		require.NotEmpty(t, run(allOff, on, role), role+": forced on")
	}
}

// A ticket nobody holds or follows mails nobody; one more lookup failing costs
// only that lookup.
func TestStaffFanout_ReopenOfAnUnheldTicketMailsNobody(t *testing.T) {
	next := &recorder{}
	require.NoError(t, newFanout(next, newFakeStaffUsers(), fakeStaffGroups{}).Dispatch(context.Background(), reopenedEventFor(nil, nil, nil)))
	require.Empty(t, next.got, "nobody to mail")
}

func TestStaffFanout_ReopenFollowerLookupFailureStillMailsTheAssignee(t *testing.T) {
	assignee := staffUser("assignee")
	next := &recorder{}
	f := fanoutWithFollowers(next, newFakeStaffUsers(assignee), fakeStaffGroups{}, failingStaffFollowers{})

	require.NoError(t, f.Dispatch(context.Background(), reopenedEventFor(nil, &assignee.ID, nil)))

	require.Equal(t, []uuid.UUID{assignee.ID}, recipientsOf(next))
}

// The mail carries the tracking number and a link, nothing a requester wrote,
// and each reason is worded for the person reading it.
func TestEventToEmail_ReopenStaffMail(t *testing.T) {
	d := &EmailDispatcher{cfg: &config.Config{BaseURL: "https://help.example.com"}}
	id := uuid.New()
	for _, tc := range []struct {
		reason   notification.StaffSwitch
		template string
		subject  string
	}{
		{notification.SwitchActivity, "staff_reopened.tmpl", "A ticket assigned to you was reopened: [GHD-2026-000007]"},
		{notification.SwitchGroup, "staff_reopened_group.tmpl", "A ticket assigned to your group was reopened: [GHD-2026-000007]"},
		{notification.SwitchFollow, "staff_reopened_follow.tmpl", "A ticket you follow was reopened: [GHD-2026-000007]"},
	} {
		t.Run(string(tc.reason), func(t *testing.T) {
			name, subject, to, data, ok := d.eventToEmail(notification.Event{
				Type: notification.EventStaffReopened, TicketID: id, TrackingNumber: "GHD-2026-000007",
				Recipient: "staff@example.com", StaffReason: tc.reason,
				Subject: "request text", GuestToken: "must-not-appear",
			})
			require.True(t, ok)
			require.Equal(t, tc.template, name)
			require.Equal(t, tc.subject, subject)
			require.Equal(t, "staff@example.com", to)
			require.Equal(t, map[string]string{
				"TrackingNumber": "GHD-2026-000007",
				"TicketURL":      "https://help.example.com/tickets/" + id.String(),
			}, data, "a staff link is never a guest link")
		})
	}
}

func TestStaffTemplates_ReopenRender(t *testing.T) {
	d, err := NewEmailDispatcher(&config.Config{SMTPHost: "smtp.example.com", SMTPFrom: "desk@example.com", BaseURL: "https://help.example.com"})
	require.NoError(t, err)
	for _, name := range []string{"staff_reopened.tmpl", "staff_reopened_group.tmpl", "staff_reopened_follow.tmpl"} {
		var b strings.Builder
		require.NoError(t, d.templates.ExecuteTemplate(&b, name, map[string]string{
			"TrackingNumber": "GHD-2026-000007", "TicketURL": "https://help.example.com/tickets/x",
		}), name)
		require.Contains(t, b.String(), "GHD-2026-000007")
		require.Contains(t, b.String(), "https://help.example.com/tickets/x")
		require.Contains(t, b.String(), "reopened")
	}
}

// A staff.reopened row is email only and needs no address in it: it is read at
// send time, like every staff row.
func TestOutbox_ReopenStaffMailIsEmailOnly(t *testing.T) {
	id := uuid.New()
	ev := notification.Event{Type: notification.EventStaffReopened, TicketID: uuid.New(), StaffUserID: &id}
	require.True(t, notification.Carries("email", ev))
	require.False(t, notification.Carries("webhook", ev), "a webhook subscriber already has ticket.reopened")
}

// The reopen event itself, without a Recipient (a status change or a requester's
// own reply), makes no mail for the requester; it only feeds the staff fan-out.
func TestEventToEmail_AReopenWithoutARecipientMailsNobody(t *testing.T) {
	d := &EmailDispatcher{cfg: &config.Config{BaseURL: "https://help.example.com"}}
	_, _, _, _, ok := d.eventToEmail(notification.Event{Type: notification.EventTicketReopened, TicketID: uuid.New()})
	require.False(t, ok)
}
