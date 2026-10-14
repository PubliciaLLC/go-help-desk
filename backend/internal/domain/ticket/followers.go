package ticket

import (
	"context"
	"fmt"
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

// ListFollowers returns who follows the ticket, oldest first.
func (s *Service) ListFollowers(ctx context.Context, ticketID uuid.UUID) ([]Follower, error) {
	return s.store.ListFollowers(ctx, ticketID)
}

// ListMailableFollowerIDs returns the followers staff email can go to. It
// implements the follower half of notify.StaffFollowers.
func (s *Service) ListMailableFollowerIDs(ctx context.Context, ticketID uuid.UUID) ([]uuid.UUID, error) {
	return s.store.ListMailableFollowerIDs(ctx, ticketID)
}
