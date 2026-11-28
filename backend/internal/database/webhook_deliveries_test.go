package database_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/server/notify"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

func newHistoryHook(t *testing.T, as *authstore.Store, url string) authstore.WebhookConfig {
	t.Helper()
	wh := authstore.WebhookConfig{
		ID: uuid.New(), URL: url, Events: []string{"*"}, Enabled: true,
		CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
	require.NoError(t, as.CreateWebhook(context.Background(), wh))
	return wh
}

func attempt(hook authstore.WebhookConfig, id uuid.UUID, at time.Time, status int, class string, retrying bool) authstore.WebhookAttempt {
	return authstore.WebhookAttempt{
		DeliveryID: id, HookID: hook.ID, EventType: "ticket.created",
		Event:    []byte(`{"type":"ticket.created"}`),
		Result:   authstore.WebhookDelivery{At: at, Status: status, Error: class},
		Retrying: retrying,
	}
}

// A delivery is one row however many attempts it took, and shows the latest.
func TestWebhookHistory_RetriesUpdateOneRow(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()
	as := authstore.New(q)
	hook := newHistoryHook(t, as, "https://example.com/h")

	id := uuid.New()
	t0 := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, as.RecordWebhookAttempt(ctx, attempt(hook, id, t0, 503, notify.DeliveryHTTPStatus, true)))
	require.NoError(t, as.RecordWebhookAttempt(ctx, attempt(hook, id, t0.Add(time.Minute), 200, "", false)))

	got, err := as.ListWebhookDeliveries(ctx, hook.ID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, id, got[0].ID)
	require.Equal(t, 2, got[0].Attempts)
	require.Equal(t, 200, got[0].Status)
	require.Equal(t, "ok", got[0].Result)
	require.False(t, got[0].Retrying)
	require.True(t, got[0].StartedAt.Equal(t0), "started_at is the first attempt")
	require.True(t, got[0].LastAttemptAt.Equal(t0.Add(time.Minute)))
	require.Equal(t, "ticket.created", got[0].Event)
}

// 25 per hook, newest kept, and one busy hook cannot push another's out.
func TestWebhookHistory_KeepsTheNewest25PerHook(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()
	as := authstore.New(q)
	busy := newHistoryHook(t, as, "https://example.com/busy")
	quiet := newHistoryHook(t, as, "https://example.com/quiet")

	quietID := uuid.New()
	base := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, as.RecordWebhookAttempt(ctx, attempt(quiet, quietID, base, 200, "", false)))

	ids := make([]uuid.UUID, 30)
	for i := range ids {
		ids[i] = uuid.New()
		require.NoError(t, as.RecordWebhookAttempt(ctx,
			attempt(busy, ids[i], base.Add(time.Duration(i+1)*time.Second), 200, "", false)))
	}

	got, err := as.ListWebhookDeliveries(ctx, busy.ID)
	require.NoError(t, err)
	require.Len(t, got, authstore.WebhookHistoryLimit)
	require.Equal(t, ids[29], got[0].ID, "newest first")
	require.Equal(t, ids[5], got[24].ID, "the 5 oldest are gone")

	other, err := as.ListWebhookDeliveries(ctx, quiet.ID)
	require.NoError(t, err)
	require.Len(t, other, 1)
	require.Equal(t, quietID, other[0].ID)
}

// History holds a class and a status, never text: the CHECK refuses a message
// even if a caller tried to store one.
func TestWebhookHistory_RefusesFreeText(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()
	as := authstore.New(q)
	hook := newHistoryHook(t, as, "https://example.com/h")

	err := as.RecordWebhookAttempt(ctx,
		attempt(hook, uuid.New(), time.Now().UTC(), 0, "dial tcp 10.0.0.1: https://example.com/h?token=abc", false))
	require.Error(t, err)
}

// The event is stored for Redeliver and reachable only by the hook that owns
// the delivery.
func TestWebhookHistory_EventIsScopedToItsHook(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()
	as := authstore.New(q)
	a := newHistoryHook(t, as, "https://example.com/a")
	b := newHistoryHook(t, as, "https://example.com/b")

	id := uuid.New()
	require.NoError(t, as.RecordWebhookAttempt(ctx, attempt(a, id, time.Now().UTC(), 200, "", false)))

	ev, err := as.GetWebhookDeliveryEvent(ctx, a.ID, id)
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"ticket.created"}`, string(ev))

	_, err = as.GetWebhookDeliveryEvent(ctx, b.ID, id)
	require.True(t, errors.Is(err, sql.ErrNoRows), "another hook's delivery must not be readable: %v", err)
}

func TestWebhookHistory_DeletedWithTheHook(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()
	as := authstore.New(q)
	hook := newHistoryHook(t, as, "https://example.com/h")
	require.NoError(t, as.RecordWebhookAttempt(ctx, attempt(hook, uuid.New(), time.Now().UTC(), 200, "", false)))

	require.NoError(t, as.DeleteWebhook(ctx, hook.ID))

	got, err := as.ListWebhookDeliveries(ctx, hook.ID)
	require.NoError(t, err)
	require.Empty(t, got)
}

// Like the latest result, history described a different target once the URL
// changes, so it goes; changing anything else keeps it.
func TestWebhookHistory_ClearedWhenTheURLChanges(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()
	as := authstore.New(q)
	hook := newHistoryHook(t, as, "https://example.com/h")
	require.NoError(t, as.RecordWebhookAttempt(ctx, attempt(hook, uuid.New(), time.Now().UTC(), 200, "", false)))

	hook.Enabled = false
	require.NoError(t, as.UpdateWebhook(ctx, hook))
	got, err := as.ListWebhookDeliveries(ctx, hook.ID)
	require.NoError(t, err)
	require.Len(t, got, 1, "a non-URL edit keeps history")

	hook.URL = "https://example.com/other"
	require.NoError(t, as.UpdateWebhook(ctx, hook))
	got, err = as.ListWebhookDeliveries(ctx, hook.ID)
	require.NoError(t, err)
	require.Empty(t, got)
}

// The result classes the table accepts are exactly the delivery classes plus
// "ok", so a new class cannot be added in Go and refused by the database.
func TestWebhookHistory_AcceptsEveryDeliveryClass(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()
	as := authstore.New(q)
	hook := newHistoryHook(t, as, "https://example.com/h")

	for _, class := range append([]string{""}, notify.DeliveryErrors...) {
		require.NoError(t, as.RecordWebhookAttempt(ctx,
			attempt(hook, uuid.New(), time.Now().UTC(), 0, class, false)), class)
	}
}

// A retry that is dropped turns its entry from "retrying" into "dropped" with
// the reason (#499). A delivery that is not retrying is left alone: a finished
// result is a fact, and a drop with no earlier entry has nothing to mark.
func TestWebhookHistory_MarkDroppedOnlyChangesARetryingEntry(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()
	as := authstore.New(q)
	hook := newHistoryHook(t, as, "https://example.com/h")
	other := newHistoryHook(t, as, "https://example.com/other")
	t0 := time.Now().UTC().Truncate(time.Millisecond)

	retrying, delivered := uuid.New(), uuid.New()
	require.NoError(t, as.RecordWebhookAttempt(ctx, attempt(hook, retrying, t0, 503, notify.DeliveryHTTPStatus, true)))
	require.NoError(t, as.RecordWebhookAttempt(ctx, attempt(hook, delivered, t0, 200, "", false)))

	// Another hook's id, an id with no entry, and a delivered one: none changes.
	require.NoError(t, as.MarkWebhookDeliveryDropped(ctx, other.ID, retrying, notify.DropDisabled))
	require.NoError(t, as.MarkWebhookDeliveryDropped(ctx, hook.ID, uuid.New(), notify.DropDisabled))
	require.NoError(t, as.MarkWebhookDeliveryDropped(ctx, hook.ID, delivered, notify.DropDisabled))
	require.NoError(t, as.MarkWebhookDeliveryDropped(ctx, hook.ID, retrying, notify.DropURLChanged))

	got, err := as.ListWebhookDeliveries(ctx, hook.ID)
	require.NoError(t, err)
	by := map[uuid.UUID]authstore.WebhookDeliveryRecord{}
	for _, r := range got {
		by[r.ID] = r
	}
	require.Len(t, by, 2, "nothing was added")
	require.Equal(t, "dropped", by[retrying].Result)
	require.Equal(t, notify.DropURLChanged, by[retrying].DropReason)
	require.False(t, by[retrying].Retrying)
	require.Equal(t, 503, by[retrying].Status, "the last status it saw stays")
	require.Equal(t, 1, by[retrying].Attempts, "a drop is not an attempt")
	require.Equal(t, "ok", by[delivered].Result)
	require.Empty(t, by[delivered].DropReason)

	// Dropping again changes nothing: it is no longer retrying.
	require.NoError(t, as.MarkWebhookDeliveryDropped(ctx, hook.ID, retrying, notify.DropFilter))
	got, err = as.ListWebhookDeliveries(ctx, hook.ID)
	require.NoError(t, err)
	for _, r := range got {
		if r.ID == retrying {
			require.Equal(t, notify.DropURLChanged, r.DropReason)
		}
	}
}

// The reasons the table accepts are exactly the ones Go sends, and free text
// is refused, as for the result class.
func TestWebhookHistory_DropReasonsMatchTheCheck(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()
	as := authstore.New(q)
	hook := newHistoryHook(t, as, "https://example.com/h")

	for _, reason := range notify.DropReasons {
		id := uuid.New()
		require.NoError(t, as.RecordWebhookAttempt(ctx, attempt(hook, id, time.Now().UTC(), 503, notify.DeliveryHTTPStatus, true)))
		require.NoError(t, as.MarkWebhookDeliveryDropped(ctx, hook.ID, id, reason), reason)
	}
	id := uuid.New()
	require.NoError(t, as.RecordWebhookAttempt(ctx, attempt(hook, id, time.Now().UTC(), 503, notify.DeliveryHTTPStatus, true)))
	require.Error(t, as.MarkWebhookDeliveryDropped(ctx, hook.ID, id, "https://example.com/?token=abc"))
}
