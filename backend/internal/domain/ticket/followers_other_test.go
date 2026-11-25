package ticket_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Adding and removing someone else (#695). The domain owns who may be a
// follower and what is audited and announced; whether the person added can
// see the ticket under scope is the handler's (the domain cannot know scope).

func lastFollowEntry(h *harness) map[string]any {
	e := h.auditStore.entries[len(h.auditStore.entries)-1]
	return map[string]any{"action": e.Action, "actor": e.ActorID, "before": e.Before, "after": e.After}
}

func TestAddFollower_AuditsTheAdderAndNamesWhoWasAdded(t *testing.T) {
	cases := []struct {
		name       string
		adderRole  user.Role
		targetSelf bool
	}{
		{"staff adds a colleague", user.RoleStaff, false},
		{"admin adds a colleague", user.RoleAdmin, false},
		{"adding yourself", user.RoleStaff, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			tk := h.seedResolved(uuid.New())
			adder, other := uuid.New(), uuid.New()
			target := other
			if tc.targetSelf {
				target = adder
			}
			actor := ticket.Actor{UserID: &adder, Role: tc.adderRole}

			require.NoError(t, h.svc.AddFollower(context.Background(), tk.ID, target, actor))

			ids, err := h.svc.ListMailableFollowerIDs(context.Background(), tk.ID)
			require.NoError(t, err)
			require.Equal(t, []uuid.UUID{target}, ids)
			require.Equal(t, []string{"followed"}, followActions(h))
			e := lastFollowEntry(h)
			require.Equal(t, &adder, e["actor"], "the actor is whoever added")
			if tc.targetSelf {
				require.Equal(t, map[string]any{"following": false}, e["before"])
				require.Equal(t, map[string]any{"following": true}, e["after"], "a self follow has no follower_user_id")
			} else {
				require.Equal(t, map[string]any{"following": true, "follower_user_id": other.String()}, e["after"])
			}
		})
	}
}

func TestAddFollower_SelfIsTheSameEntryAsFollow(t *testing.T) {
	h1, h2 := newHarness(t), newHarness(t)
	me := uuid.New()
	actor := ticket.Actor{UserID: &me, Role: user.RoleStaff}
	t1, t2 := h1.seedResolved(uuid.New()), h2.seedResolved(uuid.New())

	require.NoError(t, h1.svc.Follow(context.Background(), t1.ID, actor))
	require.NoError(t, h2.svc.AddFollower(context.Background(), t2.ID, me, actor))

	a, b := lastFollowEntry(h1), lastFollowEntry(h2)
	require.Equal(t, a["before"], b["before"])
	require.Equal(t, a["after"], b["after"])
}

func TestAddFollower_IsIdempotent(t *testing.T) {
	h := newHarness(t)
	tk := h.seedResolved(uuid.New())
	adder, other := uuid.New(), uuid.New()
	actor := ticket.Actor{UserID: &adder, Role: user.RoleStaff}
	ctx := context.Background()

	require.NoError(t, h.svc.AddFollower(ctx, tk.ID, other, actor))
	h.dispatcher.events = nil
	require.NoError(t, h.svc.AddFollower(ctx, tk.ID, other, actor))

	require.Equal(t, []string{"followed"}, followActions(h), "a repeat writes no second entry")
	require.Empty(t, h.dispatcher.events, "and announces nothing")
}

func TestAddFollower_RefusesWhoCannotFollowOrCannotChoose(t *testing.T) {
	notPerson, requester := uuid.New(), uuid.New()
	adder, other := uuid.New(), uuid.New()
	cases := []struct {
		name    string
		actor   ticket.Actor
		target  uuid.UUID
		wantErr error
	}{
		{"the target is not a person who can follow", ticket.Actor{UserID: &adder, Role: user.RoleStaff}, notPerson, ticket.ErrFollowerNotEligible},
		{"a requester cannot add anyone", ticket.Actor{UserID: &requester, Role: user.RoleUser}, other, ticket.ErrForbidden},
		{"a system actor is not a person", ticket.SystemActor, other, ticket.ErrForbidden},
		{"an actor that is not a person (a service user) cannot add", ticket.Actor{UserID: &notPerson, Role: user.RoleStaff}, other, ticket.ErrForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			tk := h.seedResolved(uuid.New())
			h.store.notPeople = map[uuid.UUID]bool{notPerson: true}

			err := h.svc.AddFollower(context.Background(), tk.ID, tc.target, tc.actor)

			require.ErrorIs(t, err, tc.wantErr)
			ids, _ := h.svc.ListMailableFollowerIDs(context.Background(), tk.ID)
			require.Empty(t, ids)
			require.Empty(t, followActions(h))
			require.Empty(t, h.dispatcher.events)
		})
	}
}

func TestAddFollower_AnUnknownTicketIsNotFound(t *testing.T) {
	h := newHarness(t)
	me, other := uuid.New(), uuid.New()
	err := h.svc.AddFollower(context.Background(), uuid.New(), other, ticket.Actor{UserID: &me, Role: user.RoleStaff})
	require.ErrorIs(t, err, errNotFound)
}

func TestAddFollower_AnnouncesTheAdderAsActorAndTheAddedAsUserWithoutMail(t *testing.T) {
	h := newHarness(t)
	tk := h.seedOpen()
	adder, other := uuid.New(), uuid.New()

	require.NoError(t, h.svc.AddFollower(context.Background(), tk.ID, other, ticket.Actor{UserID: &adder, Role: user.RoleAdmin}))

	ev := only(t, h)
	require.Equal(t, notification.EventTicketFollowed, ev.Type)
	require.Equal(t, &adder, ev.ActorID)
	require.Equal(t, map[string]any{"user_id": other}, ev.Payload)
	requireNoMail(t, ev)
}

func TestRemoveFollower(t *testing.T) {
	adder, other, bystander := uuid.New(), uuid.New(), uuid.New()
	ctx := context.Background()

	t.Run("removes any follower, whoever added them, and audits the remover", func(t *testing.T) {
		h := newHarness(t)
		tk := h.seedResolved(uuid.New())
		require.NoError(t, h.svc.AddFollower(ctx, tk.ID, other, ticket.Actor{UserID: &bystander, Role: user.RoleStaff}))
		h.dispatcher.events = nil

		require.NoError(t, h.svc.RemoveFollower(ctx, tk.ID, other, ticket.Actor{UserID: &adder, Role: user.RoleStaff}))

		ids, _ := h.svc.ListMailableFollowerIDs(ctx, tk.ID)
		require.Empty(t, ids)
		require.Equal(t, []string{"followed", "unfollowed"}, followActions(h))
		e := lastFollowEntry(h)
		require.Equal(t, &adder, e["actor"])
		require.Equal(t, map[string]any{"following": true, "follower_user_id": other.String()}, e["before"])
		require.Equal(t, map[string]any{"following": false}, e["after"])
		ev := only(t, h)
		require.Equal(t, notification.EventTicketUnfollowed, ev.Type)
		require.Equal(t, &adder, ev.ActorID)
		require.Equal(t, map[string]any{"user_id": other}, ev.Payload)
		requireNoMail(t, ev)
	})

	t.Run("removing yourself is today's unfollow", func(t *testing.T) {
		h := newHarness(t)
		tk := h.seedResolved(uuid.New())
		actor := ticket.Actor{UserID: &adder, Role: user.RoleStaff}
		require.NoError(t, h.svc.Follow(ctx, tk.ID, actor))
		require.NoError(t, h.svc.RemoveFollower(ctx, tk.ID, adder, actor))
		e := lastFollowEntry(h)
		require.Equal(t, map[string]any{"following": true}, e["before"])
		require.Equal(t, map[string]any{"following": false}, e["after"])
	})

	t.Run("someone who does not follow is a no-op", func(t *testing.T) {
		h := newHarness(t)
		tk := h.seedResolved(uuid.New())
		require.NoError(t, h.svc.RemoveFollower(ctx, tk.ID, other, ticket.Actor{UserID: &adder, Role: user.RoleStaff}))
		require.Empty(t, followActions(h))
		require.Empty(t, h.dispatcher.events)
	})

	t.Run("a requester, a system actor and a service user cannot remove another", func(t *testing.T) {
		h := newHarness(t)
		tk := h.seedResolved(uuid.New())
		bot := uuid.New()
		h.store.notPeople = map[uuid.UUID]bool{bot: true}
		require.NoError(t, h.svc.AddFollower(ctx, tk.ID, other, ticket.Actor{UserID: &adder, Role: user.RoleStaff}))
		for name, a := range map[string]ticket.Actor{
			"requester": {UserID: &bystander, Role: user.RoleUser},
			"system":    ticket.SystemActor,
			"service":   {UserID: &bot, Role: user.RoleStaff},
		} {
			require.ErrorIs(t, h.svc.RemoveFollower(ctx, tk.ID, other, a), ticket.ErrForbidden, name)
		}
		ids, _ := h.svc.ListMailableFollowerIDs(ctx, tk.ID)
		require.Equal(t, []uuid.UUID{other}, ids)
	})
}
