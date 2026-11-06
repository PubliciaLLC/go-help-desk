package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// UpdateWebhook does two jobs since #448 and #413: it writes the filter, and
// it clears the history when the URL changes. Neither may cost the other.
func TestAuthStore_UpdateWebhook_FilterAndHistoryTogether(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()
	as := authstore.New(q)
	hook := newHistoryHook(t, as, "https://example.com/h")
	require.NoError(t, as.RecordWebhookAttempt(ctx, attempt(hook, uuid.New(), time.Now().UTC(), 200, "", false)))

	// A filter-only edit stores the filter and keeps the history: the hook is
	// still the same target.
	hook.Filter = fullFilter()
	require.NoError(t, as.UpdateWebhook(ctx, hook))
	got, err := as.GetWebhook(ctx, hook.ID)
	require.NoError(t, err)
	require.Equal(t, fullFilter(), got.Filter)
	hist, err := as.ListWebhookDeliveries(ctx, hook.ID)
	require.NoError(t, err)
	require.Len(t, hist, 1, "a filter edit keeps history")

	// A new URL with the filter kept clears the history and still stores the
	// filter sent with it.
	hook.URL = "https://example.com/other"
	require.NoError(t, as.UpdateWebhook(ctx, hook))
	got, err = as.GetWebhook(ctx, hook.ID)
	require.NoError(t, err)
	require.Equal(t, fullFilter(), got.Filter)
	hist, err = as.ListWebhookDeliveries(ctx, hook.ID)
	require.NoError(t, err)
	require.Empty(t, hist)
}
