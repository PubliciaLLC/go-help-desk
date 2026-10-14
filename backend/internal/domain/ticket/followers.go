package ticket

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
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

// Follow makes the actor a follower of the ticket. Following twice is not an
// error and writes nothing the second time.
//
// The audit entry commits with the row (ticket.Atomic): an entry kept apart
// from the change it describes is not an audit trail.
func (s *Service) Follow(ctx context.Context, ticketID uuid.UUID, actor Actor) error {
	return s.setFollowing(ctx, ticketID, actor, true)
}

// Unfollow removes the actor from the ticket's followers. Not following is not
// an error.
func (s *Service) Unfollow(ctx context.Context, ticketID uuid.UUID, actor Actor) error {
	return s.setFollowing(ctx, ticketID, actor, false)
}

func (s *Service) setFollowing(ctx context.Context, ticketID uuid.UUID, actor Actor, follow bool) error {
	if actor.UserID == nil {
		// The scheduler and other system actors are not people.
		return ErrForbidden
	}
	uid := *actor.UserID
	return s.atomic.InTx(ctx, func(st Store, au audit.Store) error {
		if _, err := st.GetByID(ctx, ticketID); err != nil {
			return err
		}
		var (
			changed bool
			err     error
			action  string
			before  map[string]any
			after   map[string]any
		)
		if follow {
			// IsAssignableUser is "an active staff or admin account that is not
			// a service account", which is exactly who may follow.
			ok, perr := st.IsAssignableUser(ctx, uid)
			if perr != nil {
				return perr
			}
			if !ok {
				return ErrForbidden
			}
			changed, err = st.AddFollower(ctx, ticketID, uid)
			action, before, after = AuditActionFollowed, map[string]any{"following": false}, map[string]any{"following": true}
		} else {
			changed, err = st.RemoveFollower(ctx, ticketID, uid)
			action, before, after = AuditActionUnfollowed, map[string]any{"following": true}, map[string]any{"following": false}
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
		return nil
	})
}

// dropAssigneeFollow removes the follow of the person a ticket was just taken
// from, in the reassignment's own transaction, and audits it as unfollowed
// with the reassigning actor as the actor (Erik, 2026-10-14: they click Follow
// again to keep the mail). No follow row, no entry. The entry names whose
// follow went, and who holds the ticket now, in the keys the audit view
// already allow-lists and resolves to names.
func dropAssigneeFollow(ctx context.Context, st Store, au audit.Store, ticketID, previous uuid.UUID, actorID, now *uuid.UUID) error {
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
	return nil
}

// ListAuditFeed is a ticket's activity feed, oldest first, with follow and
// unfollow entries (includeFollow) or without.
//
// The feed is capped at the newest ticketAuditCap entries. Follow entries are
// kept out of that count in the query, and fetched on their own with a cap of
// their own when the viewer may see them, then merged by time: otherwise a few
// hundred toggles would push the ticket's real history off its feed.
func (s *Service) ListAuditFeed(ctx context.Context, ticketID uuid.UUID, includeFollow bool) ([]audit.Entry, error) {
	real, err := s.auditStore.ListByEntityActions(ctx, "ticket", ticketID,
		audit.ActionFilter{Exclude: FollowAuditActions()}, ticketAuditCap, 0)
	if err != nil {
		return nil, fmt.Errorf("listing audit entries: %w", err)
	}
	entries := real
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
