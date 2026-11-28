package server_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// The key audit write is detached from the request (#450), as ResetMFA's is
// (#306): the actor who issued a key must not be able to leave no trace by
// hanging up the moment it is written. The context handed in is already
// cancelled, which is what a disconnected client's request looks like.
func TestAPIKeyAudit_IsWrittenEvenIfTheRequestWasCancelled(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	for _, action := range []string{"api_key_issued", "api_key_revoked"} {
		other := uuid.New()
		h.srv.AuditAPIKeyForTest(cancelled, h.adminID, action, other, h.staffID)

		entries, err := h.auditStore.ListByEntity(context.Background(), "api_key", other, 20, 0)
		require.NoError(t, err)
		var seen bool
		for _, e := range entries {
			seen = seen || e.Action == action
		}
		require.True(t, seen, "%s: the entry must be written although the request context is cancelled", action)
	}
}
