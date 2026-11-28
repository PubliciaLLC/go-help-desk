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
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Staff mail for sla.breached and sla.at_risk (#670): who is mailed, under which
// switch, with which template, and what the mail says. The reply rule's people,
// the fourth switch alone deciding, one of eight server-built status strings.

const (
	resp = notification.SLATargetResponse
	reso = notification.SLATargetResolution
)

// slaChange is the event as the worker finds it: the sweep's, with no actor, a
// ticket as read in the transaction, and a payload of ids.
func slaChange(typ notification.EventType, alert notification.SLAAlert, assignee, group *uuid.UUID) notification.Event {
	return notification.Event{
		Type: typ, Kinds: []notification.EventType{typ}, TicketID: uuid.New(),
		TrackingNumber: "GHD-2026-000007", OccurredAt: time.Now(),
		Payload: map[string]any{"targets": []string{"response"}, "policy_id": uuid.New(), "record_id": uuid.New()},
		Ticket:  &notification.TicketState{AssigneeUserID: assignee, AssigneeGroupID: group},
		SLA:     &alert,
	}
}

var slaBoth = []struct {
	typ  notification.EventType
	mail notification.EventType
	a    notification.SLAAlert
}{
	{notification.EventSLABreached, notification.EventStaffSLABreached, notification.SLAAlert{Breached: []notification.SLATarget{resp}, AtRisk: []notification.SLATarget{reso}}},
	{notification.EventSLAAtRisk, notification.EventStaffSLAAtRisk, notification.SLAAlert{AtRisk: []notification.SLATarget{resp, reso}}},
}

// ── the eight strings ────────────────────────────────────────────────────────

func TestSLAStatus_EightStrings(t *testing.T) {
	cases := []struct {
		name  string
		alert notification.SLAAlert
		want  string
	}{
		{"response breached", notification.SLAAlert{Breached: []notification.SLATarget{resp}}, "Response breached"},
		{"resolution breached", notification.SLAAlert{Breached: []notification.SLATarget{reso}}, "Resolution breached"},
		{"both breached", notification.SLAAlert{Breached: []notification.SLATarget{resp, reso}}, "Response and resolution breached"},
		{"response breached, resolution at risk", notification.SLAAlert{Breached: []notification.SLATarget{resp}, AtRisk: []notification.SLATarget{reso}}, "Response breached; resolution at risk"},
		{"resolution breached, response at risk", notification.SLAAlert{Breached: []notification.SLATarget{reso}, AtRisk: []notification.SLATarget{resp}}, "Resolution breached; response at risk"},
		{"response at risk", notification.SLAAlert{AtRisk: []notification.SLATarget{resp}}, "Response at risk"},
		{"resolution at risk", notification.SLAAlert{AtRisk: []notification.SLATarget{reso}}, "Resolution at risk"},
		{"both at risk", notification.SLAAlert{AtRisk: []notification.SLATarget{resp, reso}}, "Response and resolution at risk"},
	}
	seen := map[string]bool{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := slaStatus(&tc.alert)
			require.True(t, ok)
			require.Equal(t, tc.want, got)
			seen[got] = true
		})
	}
	require.Len(t, seen, 8, "eight distinct strings")
}

// Anything the sweep cannot raise has no status, so no mail goes out saying
// nothing, or something else.
func TestSLAStatus_NothingElseIsAStatus(t *testing.T) {
	cases := []struct {
		name  string
		alert *notification.SLAAlert
	}{
		{"no alert", nil},
		{"no targets", &notification.SLAAlert{}},
		{"a target in both lists", &notification.SLAAlert{Breached: []notification.SLATarget{resp}, AtRisk: []notification.SLATarget{resp}}},
		{"an unknown target", &notification.SLAAlert{Breached: []notification.SLATarget{"first_touch"}}},
		{"an unknown target beside a known one", &notification.SLAAlert{Breached: []notification.SLATarget{resp, "<b>x</b>"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := slaStatus(tc.alert)
			require.False(t, ok)
			require.Empty(t, got)
		})
	}
}

// ── recipients ───────────────────────────────────────────────────────────────

func TestStaffFanout_SLARecipientsFollowTheReplyRule(t *testing.T) {
	cases := []struct {
		name      string
		assignee  string // a name from the people below, or ""
		group     bool
		members   []string
		followers []string
		want      map[string]notification.StaffSwitch
	}{
		{"the assignee and followers; the group's members only while nobody holds it",
			"a", true, []string{"m1", "m2"}, []string{"f"},
			map[string]notification.StaffSwitch{"a": notification.SwitchActivity, "f": notification.SwitchFollow}},
		{"nobody holds it: the group's members and followers",
			"", true, []string{"m1", "m2"}, []string{"f"},
			map[string]notification.StaffSwitch{"m1": notification.SwitchGroup, "m2": notification.SwitchGroup, "f": notification.SwitchFollow}},
		{"a member who follows an unheld ticket is mailed once, as the group's",
			"", true, []string{"m1"}, []string{"m1", "f"},
			map[string]notification.StaffSwitch{"m1": notification.SwitchGroup, "f": notification.SwitchFollow}},
		{"the assignee who follows is mailed once, as the assignee",
			"a", false, nil, []string{"a", "f"},
			map[string]notification.StaffSwitch{"a": notification.SwitchActivity, "f": notification.SwitchFollow}},
		{"a member who follows, while somebody holds the ticket, is a follower",
			"a", true, []string{"m1"}, []string{"m1"},
			map[string]notification.StaffSwitch{"a": notification.SwitchActivity, "m1": notification.SwitchFollow}},
		{"followers of an unheld, ungrouped ticket",
			"", false, nil, []string{"f"},
			map[string]notification.StaffSwitch{"f": notification.SwitchFollow}},
		{"nobody holds it and nobody follows it",
			"", false, nil, nil,
			map[string]notification.StaffSwitch{}},
	}
	for _, tc := range cases {
		for _, k := range slaBoth {
			t.Run(tc.name+" ("+string(k.typ)+")", func(t *testing.T) {
				people := map[string]user.User{}
				var all []user.User
				for _, n := range []string{"a", "m1", "m2", "f"} {
					people[n] = staffUser(n)
					all = append(all, people[n])
				}
				var assignee, group *uuid.UUID
				if tc.assignee != "" {
					id := people[tc.assignee].ID
					assignee = &id
				}
				groups := fakeStaffGroups{}
				if tc.group {
					g := uuid.New()
					group = &g
					for _, m := range tc.members {
						groups[g] = append(groups[g], people[m].ID)
					}
				}
				ev := slaChange(k.typ, k.a, assignee, group)
				followers := fakeStaffFollowers{}
				for _, f := range tc.followers {
					followers[ev.TicketID] = append(followers[ev.TicketID], people[f].ID)
				}

				next := &recorder{}
				f := NewStaffFanout(next, newFakeStaffUsers(all...), groups, followers, noOverrides, quietLog())
				require.NoError(t, f.Dispatch(context.Background(), ev))

				got := map[string]notification.StaffSwitch{}
				for _, row := range next.got {
					require.NotNil(t, row.StaffUserID)
					require.Equal(t, k.mail, row.Type, "the staff mail of this event")
					require.Nil(t, row.ActorID, "the system acts; nobody is skipped as the actor")
					require.Equal(t, ev.TicketID, row.TicketID)
					require.Equal(t, "GHD-2026-000007", row.TrackingNumber)
					require.Equal(t, &k.a, row.SLA, "the typed field rides on every row")
					require.Empty(t, row.Payload, "email never reads Payload, and the row does not carry it")
					for n, p := range people {
						if p.ID == *row.StaffUserID {
							got[n] = row.StaffReason
						}
					}
				}
				require.Len(t, next.got, len(tc.want), "one mail per person")
				require.Equal(t, tc.want, got)
			})
		}
	}
}

// ── the switch ───────────────────────────────────────────────────────────────

// The SLA switch alone decides for these two events. The three older switches
// do not apply to them, in either direction.
func TestStaffFanout_SLAAnswersOnlyToTheSLASwitch(t *testing.T) {
	type setup struct {
		role      string
		assignee  bool
		group     bool
		follower  bool
		wantsFrom string
	}
	roles := []setup{
		{role: "assignee", assignee: true},
		{role: "group member", group: true},
		{role: "follower", follower: true},
	}
	allOld := func(p *user.User) {
		p.EmailNotifyAssigned, p.EmailNotifyActivity, p.EmailNotifyGroup = true, true, true
	}
	noOld := func(p *user.User) {
		p.EmailNotifyAssigned, p.EmailNotifyActivity, p.EmailNotifyGroup = false, false, false
	}
	cases := []struct {
		name string
		mod  func(*user.User)
		ov   notification.StaffOverrides
		want bool
	}{
		{"SLA off, the other three on: nothing", func(p *user.User) { allOld(p); p.EmailNotifySLA = false }, notification.StaffOverrides{}, false},
		{"SLA on, the other three off: mailed", func(p *user.User) { noOld(p); p.EmailNotifySLA = true }, notification.StaffOverrides{}, true},
		{"all on: mailed", func(p *user.User) { allOld(p); p.EmailNotifySLA = true }, notification.StaffOverrides{}, true},
		{"SLA on, the SLA override off: nothing", func(p *user.User) { p.EmailNotifySLA = true }, notification.StaffOverrides{SLA: notification.OverrideOff}, false},
		{"SLA off, the SLA override on: mailed", func(p *user.User) { noOld(p); p.EmailNotifySLA = false }, notification.StaffOverrides{SLA: notification.OverrideOn}, true},
		{"SLA on, the Activity and Group overrides off: mailed", func(p *user.User) { p.EmailNotifySLA = true },
			notification.StaffOverrides{Activity: notification.OverrideOff, Group: notification.OverrideOff, Assigned: notification.OverrideOff}, true},
		{"SLA off, the Activity and Group overrides on: nothing", func(p *user.User) { p.EmailNotifySLA = false },
			notification.StaffOverrides{Activity: notification.OverrideOn, Group: notification.OverrideOn, Assigned: notification.OverrideOn}, false},
	}
	for _, r := range roles {
		for _, tc := range cases {
			for _, k := range slaBoth {
				t.Run(r.role+": "+tc.name+" ("+string(k.typ)+")", func(t *testing.T) {
					p := staffUser("person")
					tc.mod(&p)
					var assignee, group *uuid.UUID
					groups := fakeStaffGroups{}
					switch {
					case r.assignee:
						assignee = &p.ID
					case r.group:
						g := uuid.New()
						group = &g
						groups[g] = []uuid.UUID{p.ID}
					}
					ev := slaChange(k.typ, k.a, assignee, group)
					followers := fakeStaffFollowers{}
					if r.follower {
						followers[ev.TicketID] = []uuid.UUID{p.ID}
					}
					next := &recorder{}
					f := NewStaffFanout(next, newFakeStaffUsers(p), groups, followers,
						func(context.Context) notification.StaffOverrides { return tc.ov }, quietLog())
					require.NoError(t, f.Dispatch(context.Background(), ev))
					if tc.want {
						require.Equal(t, []uuid.UUID{p.ID}, recipientsOf(next))
					} else {
						require.Empty(t, next.got)
					}
				})
			}
		}
	}
}

// The existing events are untouched by the fourth switch: a requester's reply
// still answers to Activity whatever the SLA switch says.
func TestStaffFanout_TheSLASwitchDoesNotDecideTheOlderEvents(t *testing.T) {
	p := staffUser("assignee")
	p.EmailNotifySLA = false
	next := &recorder{}
	require.NoError(t, newFanout(next, newFakeStaffUsers(p), fakeStaffGroups{}).Dispatch(context.Background(), requesterReply(&p.ID, nil)))
	require.Equal(t, []uuid.UUID{p.ID}, recipientsOf(next))

	p.EmailNotifySLA, p.EmailNotifyActivity = true, false
	next = &recorder{}
	require.NoError(t, newFanout(next, newFakeStaffUsers(p), fakeStaffGroups{}).Dispatch(context.Background(), requesterReply(&p.ID, nil)))
	require.Empty(t, next.got)
}

// A suppressed change mails no staff, an SLA one included; and a change that
// is not an SLA event raises no SLA mail.
func TestStaffFanout_SLAMailIsStaffOnlyAndSuppressible(t *testing.T) {
	p := staffUser("assignee")
	ev := slaChange(notification.EventSLABreached, slaBoth[0].a, &p.ID, nil)
	ev.SuppressStaffMail = true
	next := &recorder{}
	require.NoError(t, newFanout(next, newFakeStaffUsers(p), fakeStaffGroups{}).Dispatch(context.Background(), ev))
	require.Empty(t, next.got)

	// The requester's mail is not part of it: there is none for an SLA event.
	ev.SuppressStaffMail = false
	ev.Recipient = "someone@example.com"
	next = &recorder{}
	require.NoError(t, newFanout(next, newFakeStaffUsers(p), fakeStaffGroups{}).Dispatch(context.Background(), ev))
	require.Len(t, next.got, 1)
	require.NotNil(t, next.got[0].StaffUserID)
}

// A failed lookup retries the whole change, as for every staff mail.
func TestStaffFanout_SLAFollowerLookupFailureIsAnError(t *testing.T) {
	a := staffUser("assignee")
	next := &recorder{}
	f := fanoutWithFollowers(next, newFakeStaffUsers(a), fakeStaffGroups{}, failingStaffFollowers{})
	require.Error(t, f.Dispatch(context.Background(), slaChange(notification.EventSLAAtRisk, slaBoth[1].a, &a.ID, nil)))
	require.Empty(t, next.got)
}

// ── send time ────────────────────────────────────────────────────────────────

// The send-time check is the one every staff mail gets, and it is asked with the
// reason, not with the switch: an assignee must still hold the ticket, a group
// member still be in the group, a follower still follow and see it.
func TestStaffMailDispatcher_SLAMailIsCheckedByReason(t *testing.T) {
	for _, reason := range []notification.StaffSwitch{notification.SwitchActivity, notification.SwitchGroup, notification.SwitchFollow} {
		for _, k := range slaBoth {
			t.Run(string(reason)+" "+string(k.mail), func(t *testing.T) {
				p := staffUser("person")
				row := notification.Event{Type: k.mail, TicketID: uuid.New(), StaffUserID: &p.ID, StaffReason: reason, SLA: &k.a}

				access := &fakeStaffAccess{ok: false}
				next := &recorder{}
				require.NoError(t, NewStaffMailDispatcher(next, newFakeStaffUsers(p), access).Dispatch(context.Background(), row))
				require.Empty(t, next.got, "no longer entitled: dropped")
				require.Equal(t, []notification.StaffSwitch{reason}, access.seen)

				access = &fakeStaffAccess{ok: true}
				next = &recorder{}
				require.NoError(t, NewStaffMailDispatcher(next, newFakeStaffUsers(p), access).Dispatch(context.Background(), row))
				require.Len(t, next.got, 1)
				require.Equal(t, p.Email, next.got[0].Recipient)
				require.Equal(t, &k.a, next.got[0].SLA, "the alert survives to the template")
			})
		}
	}
}

// ── the mail ─────────────────────────────────────────────────────────────────

func TestEventToEmail_SLAStaffMail(t *testing.T) {
	d := &EmailDispatcher{cfg: &config.Config{BaseURL: "https://help.example.com"}}
	id := uuid.New()
	mixed := notification.SLAAlert{Breached: []notification.SLATarget{resp}, AtRisk: []notification.SLATarget{reso}}
	both := notification.SLAAlert{AtRisk: []notification.SLATarget{resp, reso}}
	cases := []struct {
		mail     notification.EventType
		reason   notification.StaffSwitch
		alert    notification.SLAAlert
		template string
		subject  string
	}{
		{notification.EventStaffSLABreached, notification.SwitchActivity, mixed, "staff_sla_breached.tmpl",
			"SLA alert on a ticket assigned to you: Response breached; resolution at risk [GHD-2026-000007]"},
		{notification.EventStaffSLABreached, notification.SwitchGroup, mixed, "staff_sla_breached_group.tmpl",
			"SLA alert on a ticket assigned to your group: Response breached; resolution at risk [GHD-2026-000007]"},
		{notification.EventStaffSLABreached, notification.SwitchFollow, mixed, "staff_sla_breached_follow.tmpl",
			"SLA alert on a ticket you follow: Response breached; resolution at risk [GHD-2026-000007]"},
		{notification.EventStaffSLAAtRisk, notification.SwitchAssigned, both, "staff_sla_at_risk.tmpl",
			"SLA alert on a ticket assigned to you: Response and resolution at risk [GHD-2026-000007]"},
		{notification.EventStaffSLAAtRisk, notification.SwitchGroup, both, "staff_sla_at_risk_group.tmpl",
			"SLA alert on a ticket assigned to your group: Response and resolution at risk [GHD-2026-000007]"},
		{notification.EventStaffSLAAtRisk, notification.SwitchFollow, both, "staff_sla_at_risk_follow.tmpl",
			"SLA alert on a ticket you follow: Response and resolution at risk [GHD-2026-000007]"},
	}
	for _, tc := range cases {
		t.Run(tc.template, func(t *testing.T) {
			alert := tc.alert
			status, _ := slaStatus(&alert)
			name, subject, to, data, ok := d.eventToEmail(notification.Event{
				Type: tc.mail, TicketID: id, TrackingNumber: "GHD-2026-000007",
				Recipient: "staff@example.com", StaffReason: tc.reason, SLA: &alert,
				// None of these may reach the mail.
				Subject: "request text", StatusName: "status text", GuestToken: "must-not-appear",
				Payload: map[string]any{"SLAStatus": "FORGED", "targets": []string{"FORGED"}, "ReplyBody": "request text"},
			})
			require.True(t, ok)
			require.Equal(t, tc.template, name)
			require.Equal(t, tc.subject, subject)
			require.Equal(t, "staff@example.com", to)
			require.Equal(t, map[string]string{
				"TrackingNumber": "GHD-2026-000007",
				"TicketURL":      "https://help.example.com/tickets/" + id.String(),
				"SLAStatus":      status,
			}, data, "a staff link is never a guest link, and the status is the server's, never from Payload")
		})
	}
}

func TestEventToEmail_SLAStaffMailWithoutATrackingNumberLeavesTheBracketsOut(t *testing.T) {
	d := &EmailDispatcher{cfg: &config.Config{BaseURL: "https://help.example.com"}}
	_, subject, _, _, ok := d.eventToEmail(notification.Event{
		Type: notification.EventStaffSLABreached, TicketID: uuid.New(), Recipient: "s@example.com", StaffReason: notification.SwitchActivity,
		SLA: &notification.SLAAlert{Breached: []notification.SLATarget{resp}},
	})
	require.True(t, ok)
	require.Equal(t, "SLA alert on a ticket assigned to you: Response breached", subject)
}

// An SLA staff row with no usable alert says nothing rather than something
// made up.
func TestEventToEmail_SLAStaffMailNeedsAnAlert(t *testing.T) {
	d := &EmailDispatcher{cfg: &config.Config{BaseURL: "https://help.example.com"}}
	for _, a := range []*notification.SLAAlert{nil, {}} {
		_, _, _, _, ok := d.eventToEmail(notification.Event{
			Type: notification.EventStaffSLAAtRisk, TicketID: uuid.New(), Recipient: "s@example.com", SLA: a,
		})
		require.False(t, ok)
	}
}

func TestStaffTemplates_SLARender(t *testing.T) {
	d, err := NewEmailDispatcher(&config.Config{SMTPHost: "smtp.example.com", SMTPFrom: "desk@example.com", BaseURL: "https://help.example.com"})
	require.NoError(t, err)
	view := map[string]string{
		"TrackingNumber": "GHD-2026-000007", "TicketURL": "https://help.example.com/tickets/x",
		"SLAStatus": "Response breached; resolution at risk",
	}
	const tail = "\n\nResponse breached; resolution at risk.\n\nTicket: GHD-2026-000007\n\nRead it here:\nhttps://help.example.com/tickets/x\n\nYou can choose which of these emails you get under your profile.\n"
	for name, first := range map[string]string{
		"staff_sla_breached":        "An SLA target on a ticket assigned to you has been breached.",
		"staff_sla_breached_group":  "An SLA target on a ticket assigned to a group you belong to, which nobody has picked up, has been breached.",
		"staff_sla_breached_follow": "An SLA target on a ticket you follow has been breached.",
		"staff_sla_at_risk":         "An SLA target on a ticket assigned to you is at risk: 80% of its time has been used.",
		"staff_sla_at_risk_group":   "An SLA target on a ticket assigned to a group you belong to, which nobody has picked up, is at risk: 80% of its time has been used.",
		"staff_sla_at_risk_follow":  "An SLA target on a ticket you follow is at risk: 80% of its time has been used.",
	} {
		var b strings.Builder
		require.NoError(t, d.templates.ExecuteTemplate(&b, name+".tmpl", view), name)
		require.Equal(t, first+tail, b.String(), name)
	}
}

// ── the editor ───────────────────────────────────────────────────────────────

var slaTemplateNames = []string{
	"staff_sla_breached", "staff_sla_breached_group", "staff_sla_breached_follow",
	"staff_sla_at_risk", "staff_sla_at_risk_group", "staff_sla_at_risk_follow",
}

// The six are in the registry, beside the other staff mails, editable like the
// rest, and print TicketURL (SLAStatus is optional wording).
func TestRegistry_SLATemplates(t *testing.T) {
	for _, n := range slaTemplateNames {
		def := mustDef(t, n)
		require.Equal(t, GroupStaff, def.Group, n)
		require.False(t, def.Shell, n)
		require.Equal(t, "TicketURL", def.Required, n)
		require.ElementsMatch(t, []string{"TrackingNumber", "TicketURL", "SLAStatus"}, def.VarNames(), n)
		require.NotEmpty(t, def.Label, n)
		require.Empty(t, ValidateTemplate(def, def.DefaultSubject, def.DefaultBody), n)
	}
}

// Validation refuses a variable the template is not given, so the vars entry is
// what lets an administrator use SLAStatus in these six and nowhere else.
func TestValidate_SLAStatusIsOnlyForTheSLATemplates(t *testing.T) {
	for _, n := range slaTemplateNames {
		def := mustDef(t, n)
		require.Empty(t, ValidateTemplate(def, "Alert: {{.SLAStatus}}", "{{.SLAStatus}}\n{{.TicketURL}}"), n)
		require.Empty(t, ValidateTemplate(def, "Alert", "{{if .SLAStatus}}{{.SLAStatus}}{{end}}\n{{.TicketURL}}"), n)
		errs := ValidateTemplate(def, "Alert", "{{.SLAStatus}}")
		require.Len(t, errs, 1, n)
		require.Contains(t, errs[0].Message, "TicketURL", "TicketURL is still the one that must be printed")
	}
	for _, n := range []string{"staff_assigned", "staff_reopened_group", "ticket_created", "guest_link"} {
		errs := ValidateTemplate(mustDef(t, n), "Alert: {{.SLAStatus}}", "{{.TicketURL}}")
		require.Len(t, errs, 1, n)
		require.Equal(t, "subject", errs[0].Field)
		require.Contains(t, errs[0].Message, ".SLAStatus is not available")
		errs = ValidateTemplate(mustDef(t, n), "Alert", "{{.SLAStatus}}\n{{.TicketURL}}")
		require.Len(t, errs, 1, n)
		require.Equal(t, "body", errs[0].Field)
	}
}

// A sample (preview, test send) renders SLAStatus as one of the eight.
func TestSampleVars_SLAStatusIsOneOfTheEight(t *testing.T) {
	for _, n := range slaTemplateNames {
		v := SampleVars(mustDef(t, n), "https://help.example.com", "GHD")
		require.Contains(t, []string{
			"Response breached", "Resolution breached", "Response and resolution breached",
			"Response breached; resolution at risk", "Resolution breached; response at risk",
			"Response at risk", "Resolution at risk", "Response and resolution at risk",
		}, v["SLAStatus"], n)
	}
	require.NotContains(t, SampleVars(mustDef(t, "staff_assigned"), "https://help.example.com", "GHD"), "SLAStatus")
}

// An edited template renders with the status like the default does.
func TestRender_AnEditedSLATemplateGetsTheStatus(t *testing.T) {
	def := mustDef(t, "staff_sla_breached")
	subject, body, err := RenderTemplate(def, "Heads up ({{.SLAStatus}})", "Status: {{.SLAStatus}}\n{{.TicketURL}}",
		map[string]string{"TrackingNumber": "GHD-1", "TicketURL": "https://x/t", "SLAStatus": "Response breached"})
	require.NoError(t, err)
	require.Equal(t, "Heads up (Response breached)", subject)
	require.Equal(t, "Status: Response breached\nhttps://x/t", body)
}
