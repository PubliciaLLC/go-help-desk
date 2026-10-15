package notify

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Staff email (#441), in two halves around the outbox.
//
//   - StaffFanout runs when a request queues an event. It turns "this ticket
//     was assigned" or "the requester replied" into one staff.* event per
//     person to be mailed, applying the three switches, the administrator's
//     override, and the rule that nobody is mailed about their own action.
//   - StaffMailDispatcher runs when the worker sends one of those rows. It
//     looks the person up again and fills in their address.
//
// Each person gets their own outbox row, so one person's failing mail never
// resends anyone else's, and none of it touches the requester's mail, which
// still travels on the original event.

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

// StaffFanout wraps the dispatcher requests use.
type StaffFanout struct {
	next      notification.Dispatcher
	users     StaffUsers
	groups    StaffGroups
	followers StaffFollowers
	overrides func(context.Context) notification.StaffOverrides
	log       *slog.Logger
}

// NewStaffFanout returns a Dispatcher that forwards every event to next and
// then queues staff mail for the events that call for it. overrides is read on
// every event, so a change applies to the next email.
func NewStaffFanout(next notification.Dispatcher, users StaffUsers, groups StaffGroups, followers StaffFollowers,
	overrides func(context.Context) notification.StaffOverrides, log *slog.Logger) *StaffFanout {
	return &StaffFanout{next: next, users: users, groups: groups, followers: followers, overrides: overrides, log: log}
}

func (f *StaffFanout) Dispatch(ctx context.Context, ev notification.Event) error {
	// The original event first and unchanged: the webhook channel and the
	// requester's own mail depend on it, and a problem finding staff to mail
	// must not cost them anything.
	err := f.next.Dispatch(ctx, ev)

	staffType, cands, ok := f.candidates(ctx, ev)
	if !ok {
		return err
	}
	ids, lerr := f.recipients(ctx, ev, cands)
	if lerr != nil {
		// Callers discard this; the log is how "why did nobody get the email"
		// gets an answer.
		f.log.ErrorContext(ctx, "staff email: could not work out who to notify",
			"event", ev.Type, "ticket_id", ev.TicketID, "error", lerr)
		if err == nil {
			err = lerr
		}
		return err
	}
	for _, r := range ids {
		staff := notification.Event{
			Type: staffType, TicketID: ev.TicketID, ActorID: ev.ActorID,
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
// account is read.
type candidate struct {
	id     uuid.UUID
	reason notification.StaffSwitch
}

// candidates says which staff mail an event calls for and who might get it.
// ok is false for an event that calls for none; an empty slice with ok true
// means it calls for some but nobody holds the ticket.
func (f *StaffFanout) candidates(ctx context.Context, ev notification.Event) (notification.EventType, []candidate, bool) {
	switch {
	case ev.Type == notification.EventTicketAssigned:
		cands := []candidate{}
		if ev.AssigneeUserID != nil {
			cands = append(cands, candidate{*ev.AssigneeUserID, notification.SwitchAssigned})
		}
		cands = append(cands, f.groupMembers(ctx, ev, notification.SwitchGroup)...)
		// An assignment mails the assigned group, so its members are left out
		// of follower mail.
		cands = append(cands, f.followerCandidates(ctx, ev, true)...)
		return notification.EventStaffAssigned, cands, true

	case ev.Type == notification.EventTicketReplied && ev.FromRequester:
		// The assignee hears about it. A group hears about it only while
		// nobody personally holds the ticket: a group member whose colleague
		// has the ticket would otherwise get mail for every reply on work
		// that is not theirs.
		//
		// Followers hear about every reply, whoever holds the ticket.
		cands := []candidate{}
		if ev.AssigneeUserID != nil {
			cands = append(cands, candidate{*ev.AssigneeUserID, notification.SwitchActivity})
		} else {
			cands = append(cands, f.groupMembers(ctx, ev, notification.SwitchGroup)...)
		}
		// The group is mailed only while nobody holds the ticket.
		cands = append(cands, f.followerCandidates(ctx, ev, ev.AssigneeUserID == nil)...)
		return notification.EventStaffRequesterReplied, cands, true
	}
	return "", nil, false
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
func (f *StaffFanout) followerCandidates(ctx context.Context, ev notification.Event, groupMailed bool) []candidate {
	ids, err := f.followers.ListMailableFollowerIDs(ctx, ev.TicketID)
	if err != nil {
		f.log.ErrorContext(ctx, "staff email: could not list the ticket's followers",
			"event", ev.Type, "ticket_id", ev.TicketID, "error", err)
		return nil
	}
	left := map[uuid.UUID]bool{}
	if ev.AssigneeUserID != nil {
		left[*ev.AssigneeUserID] = true
	}
	if groupMailed {
		for _, m := range f.groupMembers(ctx, ev, notification.SwitchGroup) {
			left[m.id] = true
		}
	}
	out := make([]candidate, 0, len(ids))
	for _, id := range ids {
		if !left[id] {
			out = append(out, candidate{id, notification.SwitchFollow})
		}
	}
	return out
}

func (f *StaffFanout) groupMembers(ctx context.Context, ev notification.Event, reason notification.StaffSwitch) []candidate {
	if ev.AssigneeGroupID == nil {
		return nil
	}
	members, err := f.groups.ListMailableMembers(ctx, *ev.AssigneeGroupID)
	if err != nil {
		f.log.ErrorContext(ctx, "staff email: could not list the group's members",
			"event", ev.Type, "ticket_id", ev.TicketID, "error", err)
		return nil
	}
	out := make([]candidate, len(members))
	for i, m := range members {
		out[i] = candidate{m, reason}
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
			UserID: u.ID, Reason: c.reason,
			Prefs: notification.StaffPrefs{Assigned: u.EmailNotifyAssigned, Activity: u.EmailNotifyActivity, Group: u.EmailNotifyGroup},
		})
	}
	return notification.StaffRecipients(eligible, ev.ActorID, f.overrides(ctx)), nil
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
