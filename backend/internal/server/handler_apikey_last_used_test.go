package server_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The key list shows last_used_at; a key that has made requests must have one.
func TestAPIKey_LastUsedIsRecordedOnUse(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	lastUsed := func() *time.Time {
		keys, err := h.authStore.ListByUser(ctx, h.staffID)
		require.NoError(t, err)
		require.Len(t, keys, 1)
		return keys[0].LastUsedAt
	}
	require.Nil(t, lastUsed(), "precondition: the seeded key has never been used")

	res := h.do(t, http.MethodGet, "/api/v1/me", nil)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	// Written off the response path, so wait for it.
	require.Eventually(t, func() bool { return lastUsed() != nil }, 5*time.Second, 25*time.Millisecond,
		"an authenticated request left last_used_at empty")
	require.WithinDuration(t, time.Now(), *lastUsed(), time.Minute)
}
