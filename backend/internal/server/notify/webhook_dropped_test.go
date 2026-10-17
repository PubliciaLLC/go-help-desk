package notify

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// A retry that is dropped must not leave its history entry saying "retrying"
// for ever (#499): the delivery's entry is marked dropped, with the reason.
// One case per way deliverQueued drops a queued row.
func TestQueue_DroppedRetryMarksItsHistoryEntry(t *testing.T) {
	cases := []struct {
		name   string
		change func(rig *queueRig, id uuid.UUID)
		reason string // "" : nothing to mark
	}{
		{"disabled", func(rig *queueRig, id uuid.UUID) {
			rig.hooks.mu.Lock()
			defer rig.hooks.mu.Unlock()
			h := rig.hooks.hooks[id]
			h.Enabled = false
			rig.hooks.hooks[id] = h
		}, DropDisabled},
		{"unsubscribed", func(rig *queueRig, id uuid.UUID) {
			rig.hooks.mu.Lock()
			defer rig.hooks.mu.Unlock()
			h := rig.hooks.hooks[id]
			h.Events = []string{"ticket.note_added"}
			rig.hooks.hooks[id] = h
		}, DropUnsubscribed},
		{"url changed", func(rig *queueRig, id uuid.UUID) {
			rig.hooks.mu.Lock()
			defer rig.hooks.mu.Unlock()
			h := rig.hooks.hooks[id]
			h.URL += "/elsewhere"
			rig.hooks.hooks[id] = h
		}, DropURLChanged},
		{"filter no longer admits", func(rig *queueRig, id uuid.UUID) {
			setFilter(rig.hooks, id, notification.WebhookFilter{Priorities: []string{"critical"}})
		}, DropFilter},
		// A body that cannot be built would fail the same way every time.
		{"body cannot be built", func(rig *queueRig, id uuid.UUID) {
			rig.hooks.mu.Lock()
			defer rig.hooks.mu.Unlock()
			h := rig.hooks.hooks[id]
			h.PayloadFormat = "no-such-format"
			rig.hooks.hooks[id] = h
		}, DropUnbuildable},
		// Deleting the hook deletes its history with it (ON DELETE CASCADE), so
		// there is no entry to mark and nothing is asked of the store.
		{"hook deleted", func(rig *queueRig, id uuid.UUID) {
			rig.hooks.mu.Lock()
			defer rig.hooks.mu.Unlock()
			delete(rig.hooks.hooks, id)
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rc := newReceiver(t, 500, 200)
			hook := filtered(hookFor(rc.srv.URL), notification.WebhookFilter{Priorities: []string{"low"}})
			rig := newQueueRig(t, rc.srv.Client(), hook)

			require.NoError(t, rig.front.Dispatch(context.Background(), eventWithPriority("low")))
			rig.store.makeDue()
			_, err := rig.worker.RunOnce(context.Background()) // the fan-out
			require.NoError(t, err)
			rig.store.makeDue()
			_, err = rig.worker.RunOnce(context.Background()) // the first attempt: 500, retrying
			require.NoError(t, err)
			require.Equal(t, 1, rc.count())
			deliveryID := rig.hooks.last().DeliveryID
			require.True(t, rig.hooks.last().Retrying)

			tc.change(rig, hook.ID)
			rig.drain(t)

			require.Equal(t, 1, rc.count(), "the retry is dropped, not sent")
			rig.hooks.mu.Lock()
			defer rig.hooks.mu.Unlock()
			if tc.reason == "" {
				require.Empty(t, rig.hooks.dropped)
				return
			}
			require.Equal(t, []droppedMark{{hook.ID, deliveryID, tc.reason}}, rig.hooks.dropped)
		})
	}
}
