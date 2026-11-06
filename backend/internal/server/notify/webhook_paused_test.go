package notify

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// A hook whose API key, or that key's owner, is no longer active is paused (#450): nothing is
// sent, and nothing is recorded as a failure, as for a disabled hook. The store
// says so when it hands the hook over; this is the dispatcher acting on it.

func TestQueue_PausedHookGetsNothing(t *testing.T) {
	rc := newReceiver(t, 200)
	hook := hookFor(rc.srv.URL)
	rig := newQueueRig(t, rc.srv.Client(), hook)

	setPaused := func(p bool) {
		rig.hooks.mu.Lock()
		defer rig.hooks.mu.Unlock()
		h := rig.hooks.hooks[hook.ID]
		h.Paused = p
		rig.hooks.hooks[hook.ID] = h
	}

	t.Run("paused before the event: no row is queued", func(t *testing.T) {
		setPaused(true)
		require.NoError(t, rig.front.Dispatch(context.Background(), fixtureReplyEvent(false)))
		_, err := rig.worker.RunOnce(context.Background()) // fan-out: the channel's one row becomes one per hook
		require.NoError(t, err)
		// Draining alone would pass if a row were queued and then settled at
		// send time; this is the check that none was queued.
		require.Empty(t, rig.store.channelsQueued(), "a paused hook is not fanned out to")
		rig.drain(t)
		require.Equal(t, 0, rc.count())
		require.Empty(t, rig.store.failed)
	})

	t.Run("paused between queueing and sending: settled, not failed", func(t *testing.T) {
		setPaused(false)
		require.NoError(t, rig.front.Dispatch(context.Background(), fixtureReplyEvent(false)))
		_, err := rig.worker.RunOnce(context.Background()) // fan-out: one row for the hook
		require.NoError(t, err)
		setPaused(true)
		rig.drain(t)
		require.Equal(t, 0, rc.count(), "its key died after the event was queued")
		require.Empty(t, rig.store.failed, "a paused hook settles the row; it is not a failure")
	})

	t.Run("re-approved: it fires again", func(t *testing.T) {
		setPaused(false)
		require.NoError(t, rig.front.Dispatch(context.Background(), fixtureReplyEvent(false)))
		rig.drain(t)
		require.Equal(t, 1, rc.count())
	})

	t.Run("a replay of an earlier delivery is refused while paused", func(t *testing.T) {
		rig.hooks.mu.Lock()
		id := rig.hooks.attempts[0].DeliveryID
		rig.hooks.mu.Unlock()
		setPaused(true)
		require.ErrorIs(t, rig.disp.Redeliver(context.Background(), hook.ID, id), ErrWebhookDisabled)
	})
}

func TestDispatch_WithoutAQueueSkipsAPausedHook(t *testing.T) {
	rc := newReceiver(t, 200)
	live, paused := hookFor(rc.srv.URL), hookFor(rc.srv.URL)
	paused.Paused = true
	hs := newQueueStore(live, paused)
	disp := NewWebhookDispatcher(hs, fixtureBaseURL, quietLog())
	disp.client = rc.srv.Client()

	require.NoError(t, disp.Dispatch(context.Background(), fixtureReplyEvent(false)))
	require.Eventually(t, func() bool { return rc.count() >= 1 }, 2e9, 10e6)
	require.Equal(t, 1, rc.count(), "only the hook that is not paused is sent to")
}
