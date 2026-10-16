package outboxstore_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/outboxstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/txrunner"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/server/notify"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// End to end against real Postgres (#506): a real ticket.Service on the real
// transaction runner, then the real worker with the change splitter. It commits
// for real, so it lives with the outbox store's tests (see txrunner_outbox_test.go).

type mailRecorder struct{ got []notification.Event }

func (m *mailRecorder) Dispatch(_ context.Context, ev notification.Event) error {
	m.got = append(m.got, ev)
	return nil
}

type noGroups struct{}

func (noGroups) ListMailableMembers(context.Context, uuid.UUID) ([]uuid.UUID, error) { return nil, nil }

type outboxRow struct {
	channel string
	event   notification.Event
}

func rowsFor(t *testing.T, db *testutil.DB, ticketID uuid.UUID) []outboxRow {
	t.Helper()
	rows, err := db.SQL.QueryContext(context.Background(),
		`SELECT channel, event FROM notification_outbox WHERE event->>'ticket_id' = $1 ORDER BY created_at, channel`, ticketID.String())
	require.NoError(t, err)
	defer rows.Close()
	var out []outboxRow
	for rows.Next() {
		var ch string
		var raw []byte
		require.NoError(t, rows.Scan(&ch, &raw))
		ev, err := notification.DecodeEvent(raw)
		require.NoError(t, err)
		out = append(out, outboxRow{ch, ev})
	}
	require.NoError(t, rows.Err())
	return out
}

func channelsOf(rows []outboxRow) map[string]int {
	out := map[string]int{}
	for _, r := range rows {
		out[r.channel]++
	}
	return out
}

func TestEndToEnd_AChangeIsAnnouncedOnceAndMailedOncePerPerson(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()
	q := db.Queries
	ts, us := ticketstore.New(q), userstore.New(q)
	now := time.Now().UTC().Truncate(time.Millisecond)

	mkStaff := func(name string) user.User {
		u := user.User{
			ID: uuid.New(), Email: name + "-" + uuid.NewString() + "@test.local", DisplayName: name, Role: user.RoleStaff,
			EmailNotifyAssigned: true, EmailNotifyActivity: true, EmailNotifyGroup: true, CreatedAt: now, UpdatedAt: now,
		}
		require.NoError(t, us.Create(ctx, u))
		return u
	}
	routedTo, other, actor := mkStaff("routed"), mkStaff("other"), mkStaff("actor")
	cat := category.Category{ID: uuid.New(), Name: "e2e " + uuid.NewString()[:8], SortOrder: 1, Active: true}
	require.NoError(t, categorystore.New(q).CreateCategory(ctx, cat))

	svc := ticket.NewService(ts, ts, notification.Noop{}, auditstore.New(q),
		txrunner.New(db.SQL, []string{"email", "webhook"}, nil), nil)
	require.NoError(t, svc.LoadSystemStatuses(ctx))

	guest := "guest-" + uuid.NewString() + "@test.local"
	var ticketID uuid.UUID
	defer func() {
		forgetOutbox(db, ticketID)
		_, _ = db.SQL.Exec(`DELETE FROM tickets WHERE id = $1`, ticketID)
		_, _ = db.SQL.Exec(`DELETE FROM audit_log WHERE entity_id = $1`, ticketID)
		_, _ = db.SQL.Exec(`DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{routedTo.ID, other.ID, actor.ID})
		_, _ = db.SQL.Exec(`DELETE FROM categories WHERE id = $1`, cat.ID)
	}()

	// Staff mail is read by the worker, with the real follower and account reads.
	recorder := &mailRecorder{}
	splitter := notify.NewChangeSplitter(recorder, outboxstore.New(q), nil, us, noGroups{}, ts,
		func(context.Context) notification.StaffOverrides { return notification.StaffOverrides{} },
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	worker := notify.NewWorker(outboxstore.New(q), map[string]notification.Dispatcher{
		"email": splitter, "webhook": notification.Noop{}, "verification": notification.Noop{},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	drain := func() {
		for range 20 {
			n, err := worker.RunOnce(ctx)
			require.NoError(t, err)
			if n == 0 {
				return
			}
		}
		t.Fatal("the worker never ran out of rows")
	}
	staffMailTo := func() map[uuid.UUID]int {
		out := map[uuid.UUID]int{}
		for _, e := range recorder.got {
			if e.StaffUserID != nil {
				out[*e.StaffUserID]++
			}
		}
		return out
	}
	requesterMails := func() []notification.Event {
		var out []notification.Event
		for _, e := range recorder.got {
			if e.StaffUserID == nil {
				out = append(out, e)
			}
		}
		return out
	}

	// (a) A guest files a ticket and routing gives it to a staff member.
	created, err := svc.Create(ctx, ticket.CreateInput{
		Subject: "e2e", Description: "d", CategoryID: cat.ID, GuestEmail: &guest, GuestName: "Ada",
		AutoAssign: []ticket.Assignment{{UserID: &routedTo.ID}},
	})
	require.NoError(t, err)
	ticketID = created.ID

	// The rows are there the moment Create returns, before any worker has run.
	rows := rowsFor(t, db, ticketID)
	require.Equal(t, map[string]int{"email": 1, "webhook": 1}, channelsOf(rows))
	for _, r := range rows {
		if r.channel == "email" {
			require.Equal(t, []notification.EventType{notification.EventTicketCreated, notification.EventTicketAssigned}, r.event.Kinds)
			require.Equal(t, guest, r.event.Recipient)
		}
	}
	drain()
	require.Len(t, requesterMails(), 1)
	require.Equal(t, notification.EventTicketCreated, requesterMails()[0].Type)
	require.Equal(t, map[uuid.UUID]int{routedTo.ID: 1}, staffMailTo(), "routing picked them, so they are mailed once")

	// (b) The ticket is resolved, then an agent moves it back out and assigns it.
	_, err = svc.Resolve(ctx, ticketID, "fixed", ticket.Actor{UserID: &actor.ID, Role: user.RoleStaff})
	require.NoError(t, err)
	drain()
	forgetOutbox(db, ticketID)
	recorder.got = nil

	newStatus, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)
	_, err = svc.Update(ctx, ticketID, ticket.Patch{
		StatusID: &newStatus.ID, Assign: &ticket.Assignment{UserID: &other.ID},
	}, ticket.Actor{UserID: &actor.ID, Role: user.RoleStaff})
	require.NoError(t, err)

	rows = rowsFor(t, db, ticketID)
	require.Equal(t, map[string]int{"email": 1, "webhook": 1}, channelsOf(rows), "one event for the whole PATCH")
	require.Equal(t, []notification.EventType{
		notification.EventTicketReopened, notification.EventTicketStatusChanged,
		notification.EventTicketAssigned, notification.EventTicketPriorityChanged,
	}, rows[0].event.Kinds)

	drain()
	req := requesterMails()
	require.Len(t, req, 1, "one mail to the guest")
	require.Equal(t, notification.EventTicketReopened, req[0].Type, "the reopened template")
	require.Equal(t, guest, req[0].Recipient)
	require.Equal(t, map[uuid.UUID]int{other.ID: 1}, staffMailTo(),
		"the new assignee once; the previous one and the person who acted not at all")
	require.Empty(t, rowsFor(t, db, ticketID), "every row settled")
}
