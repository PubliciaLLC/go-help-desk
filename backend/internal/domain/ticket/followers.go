package ticket

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Following a ticket (#441, part D of #464).
//
// A follower hears about a ticket's activity without holding it. Only a person
// can follow: a staff member or administrator signed in as themselves. An API
// key, an OAuth client and a service user cannot, and that is enforced twice:
// the HTTP route refuses a machine credential, and Follow refuses any account
// that is not an active, non-service staff or admin account, so a second
// caller (MCP, a future importer) cannot get round the route.

// Audit actions written for following. The per-ticket feed and the admin-wide
// view withhold them from staff unless the administrator allows it, so they
// are named here for both to share.
const (
	AuditActionFollowed   = "followed"
	AuditActionUnfollowed = "unfollowed"
)

// FollowAuditActions is every audit action that records following.
func FollowAuditActions() []string {
	return []string{AuditActionFollowed, AuditActionUnfollowed}
}

// IsFollowAuditAction reports whether action records following or unfollowing.
func IsFollowAuditAction(action string) bool {
	return action == AuditActionFollowed || action == AuditActionUnfollowed
}

// Follower is someone following a ticket, as the followers list shows them.
type Follower struct {
	UserID      uuid.UUID `json:"user_id"`
	DisplayName string    `json:"display_name"`
	CreatedAt   time.Time `json:"created_at"`
}

// ErrFollowerNotEligible is an attempt to add somebody who cannot follow: an
// account that is deleted, disabled, a service account, not staff or an
// administrator, or not there at all. One error for all of them, so the answer
// does not tell the caller which (#695).
var ErrFollowerNotEligible = errors.New("that account cannot follow a ticket: it must be an active staff member or administrator")

// Follow makes the actor a follower of the ticket. Following twice is not an
// error and writes nothing the second time.
//
// The audit entry commits with the row (ticket.Atomic): an entry kept apart
// from the change it describes is not an audit trail.
func (s *Service) Follow(ctx context.Context, ticketID uuid.UUID, actor Actor) error {
	if actor.UserID == nil {
		return ErrForbidden
	}
	return s.setFollowing(ctx, ticketID, *actor.UserID, actor, true)
}

// Unfollow removes the actor from the ticket's followers. Not following is not
// an error.
func (s *Service) Unfollow(ctx context.Context, ticketID uuid.UUID, actor Actor) error {
	if actor.UserID == nil {
		return ErrForbidden
	}
	return s.setFollowing(ctx, ticketID, *actor.UserID, actor, false)
}

// AddFollower makes target a follower of the ticket, on the actor's say-so
// (#695). The actor must be a person (an active, non-service staff or admin
// account) and so must the target, else ErrFollowerNotEligible. Adding
// yourself is Follow. Adding somebody who already follows is not an error and
// writes nothing. Nobody is mailed that they were added.
//
// Whether the target can see the ticket is not decided here: the domain does
// not know ticket scope. The caller checks it first, as StillEntitled does.
func (s *Service) AddFollower(ctx context.Context, ticketID, target uuid.UUID, actor Actor) error {
	return s.setFollowing(ctx, ticketID, target, actor, true)
}

// CanFollow reports whether an account is somebody who can follow a ticket: an
// active staff or admin account that is not a service account. AddFollower asks
// the same question inside its transaction; the HTTP handler asks it first, so
// it can look at the person's role before it checks what they may see.
func (s *Service) CanFollow(ctx context.Context, userID uuid.UUID) (bool, error) {
	return s.store.IsAssignableUser(ctx, userID)
}

// RemoveFollower removes target from the ticket's followers, whoever added
// them. Not following is not an error.
func (s *Service) RemoveFollower(ctx context.Context, ticketID, target uuid.UUID, actor Actor) error {
	return s.setFollowing(ctx, ticketID, target, actor, false)
}

// setFollowing adds or removes target as the actor. When they are the same
// person it is exactly what it always was. When they are not, the actor must
// be a person who may choose (staff or admin, not a service account), and the
// audit entry also names whose follow it was (follower_user_id), because the
// actor is no longer the subject.
func (s *Service) setFollowing(ctx context.Context, ticketID, target uuid.UUID, actor Actor, follow bool) error {
	if actor.UserID == nil {
		// The scheduler and other system actors are not people.
		return ErrForbidden
	}
	other := target != *actor.UserID
	if other && actor.Role != user.RoleStaff && actor.Role != user.RoleAdmin {
		return ErrForbidden
	}
	return s.atomic.InTx(ctx, func(st Store, au audit.Store, ch *notification.Changes) error {
		t, err := st.GetByID(ctx, ticketID)
		if err != nil {
			return err
		}
		// IsAssignableUser is "an active staff or admin account that is not a
		// service account", which is exactly who may follow, and who may
		// choose followers. Removing needs it of the actor only when it is
		// somebody else's follow: leaving on your own is always allowed.
		if follow || other {
			ok, perr := st.IsAssignableUser(ctx, *actor.UserID)
			if perr != nil {
				return perr
			}
			if !ok {
				return ErrForbidden
			}
		}
		var (
			changed bool
			action  string
			before  map[string]any
			after   map[string]any
			kind    notification.EventType
		)
		if follow {
			if other {
				ok, perr := st.IsAssignableUser(ctx, target)
				if perr != nil {
					return perr
				}
				if !ok {
					return ErrFollowerNotEligible
				}
			}
			changed, err = st.AddFollower(ctx, ticketID, target)
			action, before, after = AuditActionFollowed, map[string]any{"following": false}, map[string]any{"following": true}
			if other {
				after["follower_user_id"] = target.String()
			}
			kind = notification.EventTicketFollowed
		} else {
			changed, err = st.RemoveFollower(ctx, ticketID, target)
			action, before, after = AuditActionUnfollowed, map[string]any{"following": true}, map[string]any{"following": false}
			if other {
				before["follower_user_id"] = target.String()
			}
			kind = notification.EventTicketUnfollowed
		}
		if err != nil {
			return fmt.Errorf("updating followers: %w", err)
		}
		if !changed {
			return nil
		}
		if err := au.Create(ctx, auditEntry(actor.UserID, "ticket", ticketID, action, before, after)); err != nil {
			return fmt.Errorf("auditing %s: %w", action, err)
		}
		raiseFollow(ch, t, actor.UserID, target, kind)
		return nil
	})
}

// raiseFollow announces a follow or an unfollow to the webhooks that name it
// (#630, D40: opt-in, see notification.OptInWebhookEvents). The payload is the
// person's id and nothing else: a name would put in a webhook what the follow
// audit keeps from staff by default, and the id is what a receiver can look up.
// No mail: following is not something the requester or the assignee is told.
func raiseFollow(ch *notification.Changes, t Ticket, actorID *uuid.UUID, person uuid.UUID, kind notification.EventType) {
	ev := raiseOn(t, Actor{UserID: actorID}, time.Now())
	ev.Kinds = []notification.EventType{kind}
	ev.Payload = map[string]any{"user_id": person}
	ch.Raise(ev)
}

// dropAssigneeFollow removes the follow of the person a ticket was just taken
// from, in the reassignment's own transaction, and audits it as unfollowed
// with the reassigning actor as the actor (Erik, 2026-10-14: they click Follow
// again to keep the mail). No follow row, no entry. The entry names whose
// follow went, and who holds the ticket now, in the keys the audit view
// already allow-lists and resolves to names.
//
// The unfollow is announced like any other (ticket.unfollowed, opt-in, #630),
// as one more kind of the reassignment's own event. t is the ticket as the
// reassignment leaves it.
func dropAssigneeFollow(ctx context.Context, st Store, au audit.Store, ch *notification.Changes, t Ticket, previous uuid.UUID, actorID, now *uuid.UUID) error {
	ticketID := t.ID
	changed, err := st.RemoveFollower(ctx, ticketID, previous)
	if err != nil {
		return fmt.Errorf("removing the old assignee's follow: %w", err)
	}
	if !changed {
		return nil
	}
	before := map[string]any{"following": true, "assignee_user_id": previous.String()}
	after := map[string]any{"following": false, "assignee_user_id": nil}
	if now != nil {
		after["assignee_user_id"] = now.String()
	}
	if err := au.Create(ctx, auditEntry(actorID, "ticket", ticketID, AuditActionUnfollowed, before, after)); err != nil {
		return fmt.Errorf("auditing the old assignee's unfollow: %w", err)
	}
	raiseFollow(ch, t, actorID, previous, notification.EventTicketUnfollowed)
	return nil
}

// ListAuditFeed is a ticket's activity feed, oldest first, with follow and
// unfollow entries (includeFollow) or without.
//
// Each feed is capped at the newest ticketAuditCap entries. Reply and note
// entries and follow entries are kept out of the cap on the ticket's real
// history in the query, and fetched on their own with a cap of their own, then
// merged by time: otherwise a long conversation, or a few hundred follow
// toggles, would push the ticket's real history off its feed. Replies and notes
// are always included; follows only when the viewer may see them.
func (s *Service) ListAuditFeed(ctx context.Context, ticketID uuid.UUID, includeFollow bool) ([]audit.Entry, error) {
	real, err := s.auditStore.ListByEntityActions(ctx, "ticket", ticketID,
		audit.ActionFilter{Exclude: append(FollowAuditActions(), AuditActionReplyAdded, AuditActionNoteAdded)}, ticketAuditCap, 0)
	if err != nil {
		return nil, fmt.Errorf("listing audit entries: %w", err)
	}
	// Reply and note entries are capped on their own, like follows: a long
	// conversation must not push the ticket's real history off the feed.
	replies, err := s.auditStore.ListByEntityActions(ctx, "ticket", ticketID,
		audit.ActionFilter{Only: []string{AuditActionReplyAdded, AuditActionNoteAdded}}, ticketAuditCap, 0)
	if err != nil {
		return nil, fmt.Errorf("listing reply entries: %w", err)
	}
	entries := append(real, replies...)
	if includeFollow {
		follows, err := s.auditStore.ListByEntityActions(ctx, "ticket", ticketID,
			audit.ActionFilter{Only: FollowAuditActions()}, ticketAuditCap, 0)
		if err != nil {
			return nil, fmt.Errorf("listing follow entries: %w", err)
		}
		entries = append(entries, follows...)
	}
	// Both lists are newest first; one stable sort puts the whole feed oldest
	// first, ties staying in the order the store gave them.
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].CreatedAt.After(entries[j].CreatedAt) })
	slices.Reverse(entries)
	return entries, nil
}

// ListFollowers returns who follows the ticket, oldest first.
func (s *Service) ListFollowers(ctx context.Context, ticketID uuid.UUID) ([]Follower, error) {
	return s.store.ListFollowers(ctx, ticketID)
}

// ListMailableFollowerIDs returns the followers staff email can go to. It
// implements the follower half of notify.StaffFollowers.
func (s *Service) ListMailableFollowerIDs(ctx context.Context, ticketID uuid.UUID) ([]uuid.UUID, error) {
	return s.store.ListMailableFollowerIDs(ctx, ticketID)
}
