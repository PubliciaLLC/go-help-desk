package notify

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Staff email (#441), in two halves around the outbox.
//
//   - StaffFanout runs when the worker first picks up a change's email row
//     (#506), not on the request. It turns "this ticket was assigned", "was
//     reopened" or "the requester replied" into one staff.* event per person
//     to be mailed, applying the three switches, the administrator's override,
//     and the rule that nobody is mailed about their own action.
//   - StaffMailDispatcher runs when the worker sends one of those rows. It
//     looks the person up again and fills in their address.
//
// Each person gets their own outbox row, so one person's failing mail never
// resends anyone else's, and none of it touches the requester's mail, which
// travels as a row of its own.
//
// It runs in the worker because the change's events are written inside its
// transaction, where the reads below would not see the transaction's own
// writes: Assign drops the previous assignee's follow in the same transaction,
// so a read there would still mail them as a follower.

// StaffUsers is the part of the user service staff mail reads.
type StaffUsers interface {
	GetByIDs(ctx context.Context, ids []uuid.UUID) ([]user.User, error)
}

// StaffGroups is the part of the group service staff mail reads.
type StaffGroups interface {
	// ListMailableMembers, not ListMembers: the people staff email can go to.
	ListMailableMembers(ctx context.Context, groupID uuid.UUID) ([]uuid.UUID, error)
}

// StaffFollowers is the part of the ticket service staff mail reads to find a
// ticket's followers (#441).
type StaffFollowers interface {
	// ListMailableFollowerIDs: followers who are active staff or admin and not
	// a service account. Whether each can still see the ticket is the
	// send-time check's question (StaffAccess), not this one's.
	ListMailableFollowerIDs(ctx context.Context, ticketID uuid.UUID) ([]uuid.UUID, error)
}

// StaffFanout splits one change's email event into the mail it calls for.
type StaffFanout struct {
	next      notification.Dispatcher
	users     StaffUsers
	groups    StaffGroups
	followers StaffFollowers
	overrides func(context.Context) notification.StaffOverrides
	log       *slog.Logger
}

// NewStaffFanout returns a Dispatcher that hands next the requester's mail (one
// event, when the change has one) and then one staff.* event for each person to
// be mailed. overrides is read on every event, so a change applies to the next
// email.
func NewStaffFanout(next notification.Dispatcher, users StaffUsers, groups StaffGroups, followers StaffFollowers,
	overrides func(context.Context) notification.StaffOverrides, log *slog.Logger) *StaffFanout {
	return &StaffFanout{next: next, users: users, groups: groups, followers: followers, overrides: overrides, log: log}
}

// requesterTemplates are the kinds that have a mail for the requester, in the
// order the first of them present decides the template (#506).
var requesterTemplates = []notification.EventType{
	notification.EventTicketCreated,
	notification.EventTicketReopened,
	notification.EventTicketResolved,
	notification.EventTicketReplied,
	notification.EventTicketStatusChanged,
}

// requesterMail is the event the requester's mail is sent from: the change with
// its Type set to the first kind that has a template, and no Kinds, so it is
// sent as it is and not split again. ok is false when the change has nobody to
// mail or no kind with a template. Who is mailed was decided when the change was
// raised (the reporter for a reply with "notify customer" on, a guest for a
// creation, a status change other than closing, a resolution and a force-reopen,
// nobody for a close).
func requesterMail(ev notification.Event) (notification.Event, bool) {
	if ev.Recipient == "" && !ev.GuestLink {
		return notification.Event{}, false
	}
	kinds := ev.AllKinds()
	for _, k := range requesterTemplates {
		if slices.Contains(kinds, k) {
			ev.Type, ev.Kinds = k, nil
			return ev, true
		}
	}
	return notification.Event{}, false
}

func (f *StaffFanout) Dispatch(ctx context.Context, ev notification.Event) error {
	// The requester's mail first and unchanged: a problem finding staff to mail
	// must not cost them anything.
	var err error
	if req, ok := requesterMail(ev); ok {
		err = f.next.Dispatch(ctx, req)
	}

	cands, ok := f.candidates(ctx, ev)
	if !ok {
		return err
	}
	ids, lerr := f.recipients(ctx, ev, cands)
	if lerr != nil {
		f.log.ErrorContext(ctx, "staff email: could not work out who to notify",
			"event", ev.Type, "ticket_id", ev.TicketID, "error", lerr)
		if err == nil {
			err = lerr
		}
		return err
	}
	for _, r := range ids {
		staff := notification.Event{
			Type: r.Mail, TicketID: ev.TicketID, ActorID: ev.ActorID,
			TrackingNumber: ev.TrackingNumber, OccurredAt: ev.OccurredAt,
			StaffUserID: &r.UserID, StaffReason: r.Reason,
		}
		if derr := f.next.Dispatch(ctx, staff); derr != nil && err == nil {
			err = derr
		}
	}
	return err
}

// candidate is a person with a reason to hear about an event, before their
// account is read. mail is the staff.* event they would be mailed with.
type candidate struct {
	id     uuid.UUID
	reason notification.StaffSwitch
	mail   notification.EventType
}

// holders is who holds the ticket as of the change, read off the ticket state
// the event carries (the commit's, not a later read).
func holders(ev notification.Event) (user, group *uuid.UUID) {
	if ev.Ticket == nil {
		return nil, nil
	}
	return ev.Ticket.AssigneeUserID, ev.Ticket.AssigneeGroupID
}

// candidates says who might get staff mail for a change, across every kind it
// carries. A person who qualifies for more than one kind is one entry in the
// result of recipients, with the mail of the first kind (in the order assigned,
// reopened, requester replied) whose switch is on. ok is false for a change that
// calls for none; an empty slice with ok true means it calls for some but nobody
// holds the ticket.
func (f *StaffFanout) candidates(ctx context.Context, ev notification.Event) ([]candidate, bool) {
	kinds := ev.AllKinds()
	assignee, _ := holders(ev)
	var cands []candidate
	called := false

	if slices.Contains(kinds, notification.EventTicketAssigned) {
		called = true
		if assignee != nil {
			cands = append(cands, candidate{*assignee, notification.SwitchAssigned, notification.EventStaffAssigned})
		}
		cands = append(cands, f.groupMembers(ctx, ev, notification.SwitchGroup, notification.EventStaffAssigned)...)
		// An assignment mails the assigned group, so its members are left out
		// of follower mail.
		cands = append(cands, f.followerCandidates(ctx, ev, true, notification.EventStaffAssigned)...)
	}

	if slices.Contains(kinds, notification.EventTicketReopened) {
		called = true
		// The reply rule (Erik, 2026-10-14): the assignee hears about a reopen,
		// and the group only while nobody personally holds the ticket. The
		// assignee's mail answers to the same switch as a requester's reply
		// ("requester replied on my tickets"). Followers hear about it whoever
		// holds the ticket; a group member who follows is mailed as a follower
		// only when the group itself is not mailed.
		cands = append(cands, f.replyRule(ctx, ev, notification.EventStaffReopened)...)
	}

	if slices.Contains(kinds, notification.EventTicketReplied) && ev.FromRequester {
		called = true
		// The assignee hears about it. A group hears about it only while
		// nobody personally holds the ticket: a group member whose colleague
		// has the ticket would otherwise get mail for every reply on work
		// that is not theirs.
		//
		// Followers hear about every reply, whoever holds the ticket.
		cands = append(cands, f.replyRule(ctx, ev, notification.EventStaffRequesterReplied)...)
	}
	return cands, called
}

// replyRule is who is a candidate for a requester's reply and for a reopen,
// which follow one rule.
func (f *StaffFanout) replyRule(ctx context.Context, ev notification.Event, mail notification.EventType) []candidate {
	assignee, _ := holders(ev)
	var cands []candidate
	if assignee != nil {
		cands = append(cands, candidate{*assignee, notification.SwitchActivity, mail})
	} else {
		cands = append(cands, f.groupMembers(ctx, ev, notification.SwitchGroup, mail)...)
	}
	// The group is mailed only while nobody holds the ticket.
	return append(cands, f.followerCandidates(ctx, ev, assignee == nil, mail)...)
}

// followerCandidates are the ticket's followers, except the assignee always,
// and the assigned group's members when the group itself is mailed for this
// event (groupMailed: an assignment, or a reply on a ticket nobody holds).
// Those people are told through their own switches (Erik, 2026-10-14), and
// following on top must not bring back mail they turned off. When the group is
// not mailed (a reply while somebody holds the ticket) a member's follow
// counts, so a member who follows hears about it. A lookup that fails costs
// the followers their mail and nothing else: the people already found are
// still mailed.
func (f *StaffFanout) followerCandidates(ctx context.Context, ev notification.Event, groupMailed bool, mail notification.EventType) []candidate {
	ids, err := f.followers.ListMailableFollowerIDs(ctx, ev.TicketID)
	if err != nil {
		f.log.ErrorContext(ctx, "staff email: could not list the ticket's followers",
			"event", ev.Type, "ticket_id", ev.TicketID, "error", err)
		return nil
	}
	left := map[uuid.UUID]bool{}
	if assignee, _ := holders(ev); assignee != nil {
		left[*assignee] = true
	}
	if groupMailed {
		for _, m := range f.groupMembers(ctx, ev, notification.SwitchGroup, mail) {
			left[m.id] = true
		}
	}
	out := make([]candidate, 0, len(ids))
	for _, id := range ids {
		if !left[id] {
			out = append(out, candidate{id, notification.SwitchFollow, mail})
		}
	}
	return out
}

func (f *StaffFanout) groupMembers(ctx context.Context, ev notification.Event, reason notification.StaffSwitch, mail notification.EventType) []candidate {
	_, group := holders(ev)
	if group == nil {
		return nil
	}
	members, err := f.groups.ListMailableMembers(ctx, *group)
	if err != nil {
		f.log.ErrorContext(ctx, "staff email: could not list the group's members",
			"event", ev.Type, "ticket_id", ev.TicketID, "error", err)
		return nil
	}
	out := make([]candidate, len(members))
	for i, m := range members {
		out[i] = candidate{m, reason, mail}
	}
	return out
}

// recipients reads the candidates' accounts and applies the rule.
func (f *StaffFanout) recipients(ctx context.Context, ev notification.Event, cands []candidate) ([]notification.StaffRecipient, error) {
	if len(cands) == 0 {
		return nil, nil
	}
	ids := make([]uuid.UUID, len(cands))
	for i, c := range cands {
		ids[i] = c.id
	}
	users, err := f.users.GetByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("reading staff accounts: %w", err)
	}
	byID := make(map[uuid.UUID]user.User, len(users))
	for _, u := range users {
		byID[u.ID] = u
	}
	var eligible []notification.StaffCandidate
	for _, c := range cands {
		u, ok := byID[c.id]
		if !ok || !u.CanReceiveStaffMail() {
			continue
		}
		eligible = append(eligible, notification.StaffCandidate{
			UserID: u.ID, Reason: c.reason, Mail: c.mail,
			Prefs: notification.StaffPrefs{Assigned: u.EmailNotifyAssigned, Activity: u.EmailNotifyActivity, Group: u.EmailNotifyGroup},
		})
	}
	// "Nobody is mailed about their own action" does not apply to an assignment
	// routing made inside a create: the creator did not choose it (as in 1.2.x,
	// when routing was a second, system-made write).
	actor := ev.ActorID
	if ev.RoutedAssignment {
		actor = nil
	}
	return notification.StaffRecipients(eligible, actor, f.overrides(ctx)), nil
}

// StaffAccess is the send-time check that a person should still hear about a
// ticket. Mail can wait in the queue and be retried for an hour, and the
// ticket moves meanwhile.
type StaffAccess interface {
	// StillEntitled reports whether u still holds the ticket (or is still in
	// the group that does), or, for a follower, can still see it under the
	// ticket-scope rule (#391). A ticket that no longer exists is not an error:
	// there is nothing to tell anyone about.
	StillEntitled(ctx context.Context, u user.User, ticketID uuid.UUID, reason notification.StaffSwitch) (bool, error)
}

// StaffMailDispatcher is the send-time half, wrapped around the email channel.
type StaffMailDispatcher struct {
	next   notification.Dispatcher
	users  StaffUsers
	access StaffAccess
}

// NewStaffMailDispatcher fills in a staff.* event's recipient from the account
// as it is when the mail is sent, then hands it to next.
func NewStaffMailDispatcher(next notification.Dispatcher, users StaffUsers, access StaffAccess) *StaffMailDispatcher {
	return &StaffMailDispatcher{next: next, users: users, access: access}
}

func (d *StaffMailDispatcher) Dispatch(ctx context.Context, ev notification.Event) error {
	if ev.StaffUserID == nil {
		return d.next.Dispatch(ctx, ev)
	}
	us, err := d.users.GetByIDs(ctx, []uuid.UUID{*ev.StaffUserID})
	if err != nil {
		return fmt.Errorf("reading staff account: %w", err) // retried by the worker
	}
	// Queued mail for an account that has since been disabled, deleted or
	// made a requester is dropped: there is nobody to send it to, and retrying
	// would not change that. Mail already queued also stops the moment the
	// account does, which is what disabling someone should mean.
	if len(us) != 1 || !us[0].CanReceiveStaffMail() {
		return nil
	}
	// And mail for a ticket the person no longer holds or can no longer see:
	// reassigned, taken out of the group, or moved out of their scope since.
	ok, err := d.access.StillEntitled(ctx, us[0], ev.TicketID, ev.StaffReason)
	if err != nil {
		return fmt.Errorf("checking access to the ticket: %w", err) // retried by the worker
	}
	if !ok {
		return nil
	}
	ev.Recipient = us[0].Email
	return d.next.Dispatch(ctx, ev)
}
