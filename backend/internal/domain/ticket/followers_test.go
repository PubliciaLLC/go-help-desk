package ticket_test

import (
	"context"
	"slices"
	"sort"
	"time"

	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// ── the fake's follower table ────────────────────────────────────────────────

func (f *fakeStore) AddFollower(_ context.Context, ticketID, userID uuid.UUID) (bool, error) {
	if f.followers == nil {
		f.followers = map[uuid.UUID]map[uuid.UUID]time.Time{}
	}
	if f.followers[ticketID] == nil {
		f.followers[ticketID] = map[uuid.UUID]time.Time{}
	}
	if _, dup := f.followers[ticketID][userID]; dup {
		return false, nil
	}
	f.followers[ticketID][userID] = time.Now()
	return true, nil
}

func (f *fakeStore) RemoveFollower(_ context.Context, ticketID, userID uuid.UUID) (bool, error) {
	if _, ok := f.followers[ticketID][userID]; !ok {
		return false, nil
	}
	delete(f.followers[ticketID], userID)
	return true, nil
}

func (f *fakeStore) ListFollowers(_ context.Context, ticketID uuid.UUID) ([]ticket.Follower, error) {
	var out []ticket.Follower
	for u, at := range f.followers[ticketID] {
		out = append(out, ticket.Follower{UserID: u, CreatedAt: at})
	}
	return out, nil
}

func (f *fakeStore) ListMailableFollowerIDs(_ context.Context, ticketID uuid.UUID) ([]uuid.UUID, error) {
	var out []uuid.UUID
	for u := range f.followers[ticketID] {
		out = append(out, u)
	}
	return out, nil
}

// ListByEntityActions mirrors the real query: narrowed by action before the
// limit applies, newest first.
func (f *fakeAuditStore) ListByEntityActions(_ context.Context, entityType string, entityID uuid.UUID, flt audit.ActionFilter, limit, offset int) ([]audit.Entry, error) {
	var matched []audit.Entry
	for _, e := range f.entries {
		if e.EntityType != entityType || e.EntityID != entityID {
			continue
		}
		if len(flt.Only) > 0 && !slices.Contains(flt.Only, e.Action) {
			continue
		}
		if slices.Contains(flt.Exclude, e.Action) {
			continue
		}
		matched = append(matched, e)
	}
	// created_at DESC, id DESC: the real query's total order.
	sort.Slice(matched, func(i, j int) bool {
		if !matched[i].CreatedAt.Equal(matched[j].CreatedAt) {
			return matched[i].CreatedAt.After(matched[j].CreatedAt)
		}
		return matched[i].ID.String() > matched[j].ID.String()
	})
	if offset >= len(matched) {
		return nil, nil
	}
	matched = matched[offset:]
	if limit > 0 && limit < len(matched) {
		matched = matched[:limit]
	}
	return matched, nil
}

// ── Follow and Unfollow ──────────────────────────────────────────────────────

func followActions(h *harness) []string {
	var out []string
	for _, e := range h.auditStore.entries {
		if ticket.IsFollowAuditAction(e.Action) {
			out = append(out, e.Action)
		}
	}
	return out
}

func TestFollow_RecordsTheFollowerAndAuditsItWithTheActor(t *testing.T) {
	h := newHarness(t)
	tk := h.seedResolved(uuid.New())
	me := uuid.New()
	actor := ticket.Actor{UserID: &me, Role: user.RoleStaff}

	require.NoError(t, h.svc.Follow(context.Background(), tk.ID, actor))

	ids, err := h.svc.ListMailableFollowerIDs(context.Background(), tk.ID)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{me}, ids)
	require.Equal(t, []string{"followed"}, followActions(h))
	e := h.auditStore.entries[len(h.auditStore.entries)-1]
	require.Equal(t, &me, e.ActorID, "the audit entry names the person who followed")
	require.Equal(t, tk.ID, e.EntityID)
	require.Equal(t, "ticket", e.EntityType)
}

func TestFollow_IsIdempotentAndAuditsOnlyTheChange(t *testing.T) {
	h := newHarness(t)
	tk := h.seedResolved(uuid.New())
	me := uuid.New()
	actor := ticket.Actor{UserID: &me, Role: user.RoleStaff}
	ctx := context.Background()

	require.NoError(t, h.svc.Follow(ctx, tk.ID, actor))
	require.NoError(t, h.svc.Follow(ctx, tk.ID, actor))
	require.Equal(t, []string{"followed"}, followActions(h), "a repeat is not a second entry")

	require.NoError(t, h.svc.Unfollow(ctx, tk.ID, actor))
	require.NoError(t, h.svc.Unfollow(ctx, tk.ID, actor))
	require.Equal(t, []string{"followed", "unfollowed"}, followActions(h))

	ids, _ := h.svc.ListMailableFollowerIDs(ctx, tk.ID)
	require.Empty(t, ids)
}

func TestFollow_PeopleOnly(t *testing.T) {
	h := newHarness(t)
	tk := h.seedResolved(uuid.New())
	serviceUser := uuid.New()
	h.store.notPeople = map[uuid.UUID]bool{serviceUser: true}

	tests := []struct {
		name  string
		actor ticket.Actor
	}{
		{"no person behind it (the scheduler)", ticket.SystemActor},
		{"a service user", ticket.Actor{UserID: &serviceUser, Role: user.RoleStaff}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := h.svc.Follow(context.Background(), tk.ID, tc.actor)
			require.ErrorIs(t, err, ticket.ErrForbidden)
			ids, _ := h.svc.ListMailableFollowerIDs(context.Background(), tk.ID)
			require.Empty(t, ids)
			require.Empty(t, followActions(h))
		})
	}
}

func TestFollow_AnUnknownTicketIsNotFound(t *testing.T) {
	h := newHarness(t)
	me := uuid.New()
	err := h.svc.Follow(context.Background(), uuid.New(), ticket.Actor{UserID: &me, Role: user.RoleStaff})
	require.ErrorIs(t, err, errNotFound)
}

// The feed's cap counts real history only: follow entries are narrowed out in
// the query, so any number of them cannot push "created" off the feed.
func TestListAuditFeed_FollowEntriesNeverCountTowardTheCap(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	id := uuid.New()
	base := time.Now().Add(-24 * time.Hour)
	h.auditStore.entries = append(h.auditStore.entries,
		audit.Entry{ID: uuid.New(), EntityType: "ticket", EntityID: id, Action: "created", CreatedAt: base},
		audit.Entry{ID: uuid.New(), EntityType: "ticket", EntityID: id, Action: "resolved", CreatedAt: base.Add(time.Hour)})
	for i := 0; i < 700; i++ {
		a := "followed"
		if i%2 == 1 {
			a = "unfollowed"
		}
		h.auditStore.entries = append(h.auditStore.entries,
			audit.Entry{ID: uuid.New(), EntityType: "ticket", EntityID: id, Action: a, CreatedAt: base.Add(2*time.Hour + time.Duration(i)*time.Second)})
	}

	without, err := h.svc.ListAuditFeed(ctx, id, false)
	require.NoError(t, err)
	require.Len(t, without, 2)
	require.Equal(t, "created", without[0].Action)

	with, err := h.svc.ListAuditFeed(ctx, id, true)
	require.NoError(t, err)
	require.Equal(t, "created", with[0].Action, "oldest first")
	require.Equal(t, "resolved", with[1].Action)
	require.Len(t, with, 2+500, "follow entries are capped on their own, newest kept")
	require.True(t, sort.SliceIsSorted(with, func(i, j int) bool { return with[i].CreatedAt.Before(with[j].CreatedAt) }))
}

func TestFollowAuditActions_AreExactlyTheTwoWeWrite(t *testing.T) {
	require.True(t, ticket.IsFollowAuditAction("followed"))
	require.True(t, ticket.IsFollowAuditAction("unfollowed"))
	require.False(t, ticket.IsFollowAuditAction("assigned"))
	got := ticket.FollowAuditActions()
	slices.Sort(got)
	require.Equal(t, []string{"followed", "unfollowed"}, got)
}
