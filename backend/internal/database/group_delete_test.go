package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/groupstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/txrunner"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// groupFixture is committed rows, because the tests below need separate
// connections to see each other (see ticket_concurrency_test.go).
type groupFixture struct {
	db       *testutil.DB
	ts       *ticketstore.Store
	group    group.Group
	ticket   ticket.Ticket
	spare    ticket.Ticket // not assigned to the group; the one a late assignment targets
	reporter user.User
}

func newGroupFixture(t *testing.T) *groupFixture {
	t.Helper()
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB)
	ctx := context.Background()
	q := db.Queries
	us, cs, ts, gs := userstore.New(q), categorystore.New(q), ticketstore.New(q), groupstore.New(q)

	f := &groupFixture{db: db, ts: ts}
	f.reporter = user.User{
		ID: uuid.New(), Email: "groupdel-" + uuid.NewString() + "@test.local",
		DisplayName: "Reporter", Role: user.RoleUser,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, f.reporter))
	cat := category.Category{ID: uuid.New(), Name: "GroupDel " + uuid.NewString()[:8], SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))
	f.group = group.Group{ID: uuid.New(), Name: "ghd-groupdel-" + uuid.NewString()[:8]}
	require.NoError(t, gs.Create(ctx, f.group))

	newSt, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)
	f.ticket = ticket.Ticket{DescriptionFormat: ticket.TextFormatPlain,
		ID: uuid.New(), TrackingNumber: ticket.TrackingNumber("GDEL-" + uuid.NewString()[:8]),
		Subject: "Held by a group", Description: "x", CategoryID: cat.ID,
		Priority: ticket.PriorityMedium, StatusID: newSt.ID, ReporterUserID: &f.reporter.ID,
		AssigneeGroupID: &f.group.ID,
		CreatedAt:       time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, ts.Create(ctx, f.ticket))
	f.spare = f.ticket
	f.spare.ID = uuid.New()
	f.spare.TrackingNumber = ticket.TrackingNumber("GDEL-" + uuid.NewString()[:8])
	f.spare.AssigneeGroupID = nil
	require.NoError(t, ts.Create(ctx, f.spare))

	t.Cleanup(func() {
		_, _ = db.SQL.Exec(`DELETE FROM audit_log WHERE entity_id = $1`, f.ticket.ID)
		_, _ = db.SQL.Exec(`DELETE FROM tickets WHERE id = ANY($1)`, []uuid.UUID{f.ticket.ID, f.spare.ID})
		_, _ = db.SQL.Exec(`DELETE FROM groups WHERE id = $1`, f.group.ID)
		_, _ = db.SQL.Exec(`DELETE FROM users WHERE id = $1`, f.reporter.ID)
		_, _ = db.SQL.Exec(`DELETE FROM categories WHERE id = $1`, cat.ID)
	})
	return f
}

func (f *groupFixture) heldGroup(t *testing.T) *uuid.UUID {
	t.Helper()
	got, err := f.ts.GetByID(context.Background(), f.ticket.ID)
	require.NoError(t, err)
	return got.AssigneeGroupID
}

func (f *groupFixture) service(t *testing.T) *ticket.Service {
	t.Helper()
	svc := ticket.NewService(f.ts, f.ts, notification.Noop{}, auditstore.New(f.db.Queries),
		txrunner.New(f.db.SQL, nil, nil), nil)
	require.NoError(t, svc.LoadSystemStatuses(context.Background()))
	return svc
}

// While a group delete is open, assigning a ticket to that group waits, and
// fails once the delete commits. The group row is locked first; without it the
// assignment slips in after the unassignment, and the foreign key clears it at
// the delete with no audit entry, which is the gap the delete exists to close.
//
// Staged on two connections rather than raced: a race passes with or without
// the lock. Verified by removing the LockGroup call, which makes this fail.
func TestDeleteGroup_AnAssignmentDuringTheDeleteWaitsThenFails(t *testing.T) {
	f := newGroupFixture(t)
	ctx := context.Background()

	tx1, err := f.db.SQL.Begin()
	require.NoError(t, err)
	defer func() { _ = tx1.Rollback() }()
	st1 := ticketstore.New(dbgen.New(tx1))
	_, err = st1.UnassignForGroup(ctx, f.group.ID) // takes the group lock
	require.NoError(t, err)

	assigned := make(chan error, 1)
	go func() {
		_, err := f.db.SQL.ExecContext(ctx,
			`UPDATE tickets SET assignee_group_id = $1 WHERE id = $2`, f.group.ID, f.spare.ID)
		assigned <- err
	}()

	select {
	case err := <-assigned:
		t.Fatalf("an assignment to the group went through while its delete was open (err=%v); "+
			"it would be cleared by the foreign key with no audit entry", err)
	case <-time.After(400 * time.Millisecond):
	}

	require.NoError(t, st1.DeleteGroup(ctx, f.group.ID))
	require.NoError(t, tx1.Commit())

	select {
	case err := <-assigned:
		require.Error(t, err, "assigning to a group that was just deleted must fail, not be silently cleared")
	case <-time.After(5 * time.Second):
		t.Fatal("the waiting assignment never returned after the delete committed")
	}
	require.Nil(t, f.heldGroup(t))
	spare, err := f.ts.GetByID(ctx, f.spare.ID)
	require.NoError(t, err)
	require.Nil(t, spare.AssigneeGroupID)
}

// A failure at the last step leaves the tickets' group and the audit trail as
// they were. The failure is a trigger that refuses this one group's delete; it
// names the group so other packages running against the same database are not
// affected while it exists.
func TestDeleteGroup_AFailedDeleteRollsBackTheUnassignmentAndItsAudit(t *testing.T) {
	f := newGroupFixture(t)
	ctx := context.Background()

	fn := "ghd_test_rgd_" + f.group.ID.String()[:8]
	_, err := f.db.SQL.Exec(`
		CREATE FUNCTION ` + fn + `() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF OLD.name = '` + f.group.Name + `' THEN RAISE EXCEPTION 'refused for the test'; END IF;
			RETURN OLD;
		END $$`)
	require.NoError(t, err)
	_, err = f.db.SQL.Exec(`
		CREATE TRIGGER ` + fn + ` BEFORE DELETE ON groups
		FOR EACH ROW EXECUTE FUNCTION ` + fn + `()`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = f.db.SQL.Exec(`DROP TRIGGER IF EXISTS ` + fn + ` ON groups`)
		_, _ = f.db.SQL.Exec(`DROP FUNCTION IF EXISTS ` + fn + `()`)
	})

	_, err = f.service(t).DeleteGroup(ctx, nil, f.group.ID)
	require.Error(t, err)

	held := f.heldGroup(t)
	require.NotNil(t, held, "the ticket lost its group although the delete failed")
	require.Equal(t, f.group.ID, *held)
	entries, err := auditstore.New(f.db.Queries).ListByEntity(ctx, "ticket", f.ticket.ID, 10, 0)
	require.NoError(t, err)
	require.Empty(t, entries, "an audit entry survived the rolled-back delete")
	_, err = groupstore.New(f.db.Queries).GetByID(ctx, f.group.ID)
	require.NoError(t, err, "the group is gone")
}

// And the same path, succeeding, for the real service on a real transaction.
func TestDeleteGroup_CommitsTheUnassignmentTheAuditAndTheDelete(t *testing.T) {
	f := newGroupFixture(t)
	ctx := context.Background()

	moved, err := f.service(t).DeleteGroup(ctx, nil, f.group.ID)
	require.NoError(t, err)
	require.Equal(t, 1, moved)

	require.Nil(t, f.heldGroup(t))
	entries, err := auditstore.New(f.db.Queries).ListByEntity(ctx, "ticket", f.ticket.ID, 10, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "unassigned", entries[0].Action)
	_, err = groupstore.New(f.db.Queries).GetByID(ctx, f.group.ID)
	require.ErrorIs(t, err, group.ErrNotFound)
}
