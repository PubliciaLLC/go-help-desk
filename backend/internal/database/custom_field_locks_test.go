package database_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/customfieldstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// The locks that make an admin change to a custom field and a ticket's value
// write safe against each other (#603), and the transaction the audit entries
// share with the change. These need writes that really commit on separate
// connections, which the rolled-back harness cannot give, so each test has a
// database of its own (testutil.NewIsolatedDB).
//
// A value write (SetValues) holds the definition's row FOR SHARE while it checks
// the definition and writes. An admin change (UpdateFieldDef, ReorderFieldDefs)
// holds it FOR UPDATE while it checks what tickets hold and writes. So one
// finishes before the other starts, and each sees the other's result.
//
// Staged on two connections rather than only raced: a race passes with or
// without the lock, so each order is forced, by holding one transaction open
// inside its own check while the other starts. Verified by removing the lock
// from LockCustomFieldDefForUpdate (and again from LockCustomFieldDefsForShare),
// which makes the staged tests fail.

type lockFixture struct {
	t        *testing.T
	db       *testutil.DB
	store    *customfieldstore.Store
	svc      *customfield.Service
	ticketID uuid.UUID
}

func newLockFixture(t *testing.T) *lockFixture {
	t.Helper()
	db, closeDB := testutil.NewIsolatedDB(t)
	t.Cleanup(closeDB)
	ctx := context.Background()
	cat := category.Category{ID: uuid.New(), Name: "Locks", SortOrder: 1, Active: true}
	require.NoError(t, categorystore.New(db.Queries).CreateCategory(ctx, cat))
	var ticketID uuid.UUID
	require.NoError(t, db.SQL.QueryRowContext(ctx, `
		INSERT INTO tickets (tracking_number, subject, category_id, status_id, guest_email, description_format, source_channel)
		VALUES ('LOCKS-1', 'x', $1, (SELECT id FROM statuses LIMIT 1), 'g@example.test', 'plain', 'web')
		RETURNING id`, cat.ID).Scan(&ticketID))
	store := customfieldstore.New(db.Queries, db.SQL)
	return &lockFixture{t: t, db: db, store: store, svc: customfield.NewService(store), ticketID: ticketID}
}

func (f *lockFixture) field(name string, ft customfield.FieldType, opts ...string) customfield.FieldDef {
	f.t.Helper()
	d, err := f.svc.CreateFieldDef(context.Background(), name, ft, opts, 1, nil)
	require.NoError(f.t, err)
	return d
}

func (f *lockFixture) stored(id uuid.UUID) (string, bool) {
	f.t.Helper()
	var v string
	err := f.db.SQL.QueryRow(`SELECT value FROM ticket_custom_field_values WHERE ticket_id = $1 AND field_def_id = $2`, f.ticketID, id).Scan(&v)
	if err != nil {
		return "", false
	}
	return v, true
}

// A value write that has locked the definition and checked it, held open at the
// point where it would write. release lets it write and commit.
type heldWrite struct {
	locked  chan struct{}
	release chan struct{}
	done    chan error
}

func (f *lockFixture) holdWrite(id uuid.UUID, value string) *heldWrite {
	w := &heldWrite{locked: make(chan struct{}), release: make(chan struct{}), done: make(chan error, 1)}
	go func() {
		w.done <- f.store.SetValues(context.Background(), f.ticketID, map[uuid.UUID]string{id: value},
			func(defs map[uuid.UUID]customfield.FieldDef) (map[uuid.UUID]string, error) {
				// The definition is locked FOR SHARE from here until this returns;
				// hold it open.
				select {
				case <-w.locked:
				default:
					close(w.locked)
				}
				<-w.release
				return map[uuid.UUID]string{id: value}, nil
			})
	}()
	select {
	case <-w.locked:
	case err := <-w.done:
		f.t.Fatalf("the held write ended before it held the lock: %v", err)
	case <-time.After(5 * time.Second):
		f.t.Fatal("the held write never took its lock")
	}
	return w
}

// A definition change that has locked the definition and run its checks, held
// open at the point where it would write. proceed lets it write and commit.
//
// Every wait is bounded. A check that failed, or a test that gave up early,
// must end the stage in seconds: an unbounded receive here turns any failure
// into a hang until `go test`'s own 10-minute timeout, with the failure message
// buried under a goroutine dump.
type heldChange struct {
	proceedOnce sync.Once
	release     chan struct{}
	done        chan error
}

func (c *heldChange) proceed() { c.proceedOnce.Do(func() { close(c.release) }) }

// holdChange starts UpdateFieldDef on id and returns once it holds the
// definition. inspect, when not nil, runs on the locked row before the hold;
// what it returns is how the change ends, so a failed check ends the stage
// rather than leaving the test waiting on a lock that was never reported.
func (f *lockFixture) holdChange(id uuid.UUID, inspect func(customfield.Held) error, apply func(*customfield.FieldDef)) *heldChange {
	f.t.Helper()
	locked := make(chan struct{})
	c := &heldChange{release: make(chan struct{}), done: make(chan error, 1)}
	// A test that fails before proceed would leave the change holding its lock.
	f.t.Cleanup(c.proceed)
	go func() {
		_, err := f.store.UpdateFieldDef(context.Background(), id, func(cur customfield.FieldDef, held customfield.Held) (customfield.FieldDef, []audit.Entry, error) {
			if inspect != nil {
				if err := inspect(held); err != nil {
					return cur, nil, err
				}
			}
			close(locked)
			select {
			case <-c.release:
			case <-time.After(30 * time.Second):
				return cur, nil, errors.New("the held change was never released")
			}
			apply(&cur)
			return cur, nil, nil
		})
		c.done <- err
	}()
	select {
	case <-locked:
	case err := <-c.done:
		f.t.Fatalf("the held change ended before it held the lock: %v", err)
	case <-time.After(5 * time.Second):
		f.t.Fatal("the held change never took its lock")
	}
	return c
}

// stillWaiting reports that ch has not been written to for a while. The wait is
// what shows a lock holding; it is a negative, so it is as long as it can
// afford to be.
func stillWaiting(ch <-chan error) bool {
	select {
	case <-ch:
		return false
	case <-time.After(400 * time.Millisecond):
		return true
	}
}

func within(t *testing.T, ch <-chan error, what string) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(10 * time.Second):
		t.Fatalf("%s never returned", what)
		return nil
	}
}

// ── Deleting an option ───────────────────────────────────────────────────────

func TestDeleteOption_ConcurrentValueWriteIsSerialised(t *testing.T) {
	t.Run("the write got there first: the delete waits for it, then sees the value and is refused", func(t *testing.T) {
		f := newLockFixture(t)
		sel := f.field("Brand", customfield.FieldTypeSelect, "Dell", "HP")
		w := f.holdWrite(sel.ID, "HP")

		deleted := make(chan error, 1)
		go func() {
			_, err := f.svc.PatchFieldDef(context.Background(), sel.ID, customfield.FieldDefPatch{Options: []string{"Dell"}}, nil)
			deleted <- err
		}()
		require.True(t, stillWaiting(deleted), "the delete ran while a write held the definition")

		close(w.release)
		require.NoError(t, within(t, w.done, "the write"))
		require.ErrorIs(t, within(t, deleted, "the delete"), customfield.ErrConflict, "the delete checks after the write and finds its row")
		v, ok := f.stored(sel.ID)
		require.True(t, ok)
		require.Equal(t, "HP", v)
		got, err := f.svc.GetFieldDef(context.Background(), sel.ID)
		require.NoError(t, err)
		require.Equal(t, []string{"Dell", "HP"}, got.Options, "a refused delete changes nothing")
	})

	t.Run("the delete got there first: the write waits for it, then is refused", func(t *testing.T) {
		f := newLockFixture(t)
		sel := f.field("Brand", customfield.FieldTypeSelect, "Dell", "HP")

		deletion := f.holdChange(sel.ID, func(held customfield.Held) error {
			any, err := held.Any()
			if err != nil {
				return err
			}
			if any {
				return errors.New("a value is held; this stage needs none")
			}
			return nil
		}, func(cur *customfield.FieldDef) { cur.Options = []string{"Dell"} })

		wrote := make(chan error, 1)
		go func() {
			wrote <- f.svc.SetValues(context.Background(), f.ticketID, map[uuid.UUID]string{sel.ID: "HP"})
		}()
		require.True(t, stillWaiting(wrote), "a value write ran while the delete held the definition")

		deletion.proceed()
		require.NoError(t, within(t, deletion.done, "the delete"))
		require.ErrorIs(t, within(t, wrote, "the write"), customfield.ErrValidation, "the write sees the new lists and refuses the deleted value")
		_, ok := f.stored(sel.ID)
		require.False(t, ok, "the database never holds a value the field no longer offers")
	})

	// Unstaged, many times over: whichever wins, exactly one succeeds and the
	// database never ends up holding a deleted value.
	t.Run("raced", func(t *testing.T) {
		f := newLockFixture(t)
		for i := range 40 {
			sel := f.field("Raced "+uuid.NewString()[:8], customfield.FieldTypeSelect, "keep", "X")
			setDone, delDone := make(chan error, 1), make(chan error, 1)
			go func() {
				setDone <- f.svc.SetValues(context.Background(), f.ticketID, map[uuid.UUID]string{sel.ID: "X"})
			}()
			go func() {
				_, err := f.svc.PatchFieldDef(context.Background(), sel.ID, customfield.FieldDefPatch{Options: []string{"keep"}}, nil)
				delDone <- err
			}()
			setErr, delErr := within(t, setDone, "the write"), within(t, delDone, "the delete")

			require.Equal(t, 1, boolToInt(setErr == nil)+boolToInt(delErr == nil), "round %d: exactly one succeeds (set: %v, delete: %v)", i, setErr, delErr)
			v, held := f.stored(sel.ID)
			def, err := f.svc.GetFieldDef(context.Background(), sel.ID)
			require.NoError(t, err)
			if held {
				require.Equal(t, "X", v)
				require.Contains(t, def.Options, "X", "round %d: the value is held, so the option must still be there", i)
				require.ErrorIs(t, delErr, customfield.ErrConflict)
			} else {
				require.NotContains(t, def.Options, "X")
				require.ErrorIs(t, setErr, customfield.ErrValidation)
			}
		}
	})
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ── The type lock ────────────────────────────────────────────────────────────

func TestTypeLock_ConcurrentValueWriteIsSerialised(t *testing.T) {
	number := customfield.FieldTypeNumber

	t.Run("the write got there first: the type change is refused", func(t *testing.T) {
		f := newLockFixture(t)
		def := f.field("Cost", customfield.FieldTypeText)
		w := f.holdWrite(def.ID, "12")

		changed := make(chan error, 1)
		go func() {
			_, err := f.svc.PatchFieldDef(context.Background(), def.ID, customfield.FieldDefPatch{FieldType: &number}, nil)
			changed <- err
		}()
		require.True(t, stillWaiting(changed))

		close(w.release)
		require.NoError(t, within(t, w.done, "the write"))
		require.ErrorIs(t, within(t, changed, "the type change"), customfield.ErrConflict)
		got, err := f.svc.GetFieldDef(context.Background(), def.ID)
		require.NoError(t, err)
		require.Equal(t, customfield.FieldTypeText, got.FieldType)
	})

	t.Run("the type change got there first: the write is held to the new type", func(t *testing.T) {
		f := newLockFixture(t)
		def := f.field("Cost", customfield.FieldTypeText)

		change := f.holdChange(def.ID, nil, func(cur *customfield.FieldDef) { cur.FieldType = customfield.FieldTypeNumber })

		wrote := make(chan error, 1)
		go func() {
			wrote <- f.svc.SetValues(context.Background(), f.ticketID, map[uuid.UUID]string{def.ID: "not a number"})
		}()
		require.True(t, stillWaiting(wrote))

		change.proceed()
		require.NoError(t, within(t, change.done, "the type change"))
		require.ErrorIs(t, within(t, wrote, "the write"), customfield.ErrValidation, "text that was fine for a text field is not a number")
		_, ok := f.stored(def.ID)
		require.False(t, ok, "no text sits in a number field")
	})
}

// ── Deactivating ─────────────────────────────────────────────────────────────

func TestDeactivate_ConcurrentValueWriteIsSerialised(t *testing.T) {
	off := false

	t.Run("the write got there first: it lands, before the deactivation", func(t *testing.T) {
		f := newLockFixture(t)
		def := f.field("Serial", customfield.FieldTypeText)
		w := f.holdWrite(def.ID, "SN-1")

		deactivated := make(chan error, 1)
		go func() {
			_, err := f.svc.PatchFieldDef(context.Background(), def.ID, customfield.FieldDefPatch{Active: &off}, nil)
			deactivated <- err
		}()
		require.True(t, stillWaiting(deactivated), "the deactivation did not wait for a write in flight")

		close(w.release)
		require.NoError(t, within(t, w.done, "the write"))
		require.NoError(t, within(t, deactivated, "the deactivation"))
		v, ok := f.stored(def.ID)
		require.True(t, ok)
		require.Equal(t, "SN-1", v, "the value is from before the deactivation committed")
	})

	t.Run("the deactivation got there first: a write that starts after it is refused", func(t *testing.T) {
		f := newLockFixture(t)
		def := f.field("Serial", customfield.FieldTypeText)

		deactivation := f.holdChange(def.ID, nil, func(cur *customfield.FieldDef) { cur.Active = false })

		wrote := make(chan error, 1)
		go func() {
			wrote <- f.svc.SetValues(context.Background(), f.ticketID, map[uuid.UUID]string{def.ID: "SN-2"})
		}()
		require.True(t, stillWaiting(wrote))

		deactivation.proceed()
		require.NoError(t, within(t, deactivation.done, "the deactivation"))
		require.ErrorIs(t, within(t, wrote, "the write"), customfield.ErrValidation)
		_, ok := f.stored(def.ID)
		require.False(t, ok, "the database never holds a value written after the deactivation committed")
	})
}

// ── Audit shares the change's transaction ────────────────────────────────────

// An audit insert that fails (an actor that is no user: the foreign key refuses
// it) takes the change with it, for every kind of change.
func TestCustomFieldAudit_RolledBackWithTheChange(t *testing.T) {
	f := newLockFixture(t)
	ctx := context.Background()
	ghostID := uuid.New() // no such user: the audit entry's foreign key refuses it
	ghost := &ghostID
	def := f.field("Dept", customfield.FieldTypeSelect, "Sales")
	other := f.field("Other", customfield.FieldTypeText)
	count := func(q string, args ...any) (n int) {
		require.NoError(t, f.db.SQL.QueryRow(q, args...).Scan(&n))
		return n
	}

	t.Run("a change to a definition", func(t *testing.T) {
		name := "Department"
		_, err := f.svc.PatchFieldDef(ctx, def.ID, customfield.FieldDefPatch{Name: &name, Options: []string{"Sales", "EMEA"}}, ghost)
		require.Error(t, err)
		got, err := f.svc.GetFieldDef(ctx, def.ID)
		require.NoError(t, err)
		require.Equal(t, "Dept", got.Name, "the definition must not change without its audit entry")
		require.Equal(t, []string{"Sales"}, got.Options)
	})
	t.Run("a new definition", func(t *testing.T) {
		_, err := f.svc.CreateFieldDef(ctx, "Brand new", customfield.FieldTypeText, nil, 1, ghost)
		require.Error(t, err)
		require.Zero(t, count(`SELECT count(*) FROM custom_field_defs WHERE name = 'Brand new'`), "the field must not exist without its audit entry")
	})
	t.Run("an attachment", func(t *testing.T) {
		_, err := f.svc.CreateAssignment(ctx, def.ID, customfield.CategoryScope, f.categoryID(), 1, true, false, ghost)
		require.Error(t, err)
		require.Zero(t, count(`SELECT count(*) FROM custom_field_assignments`))
	})
	t.Run("a reorder", func(t *testing.T) {
		err := f.svc.ReorderFieldDefs(ctx, []uuid.UUID{other.ID, def.ID}, ghost)
		require.Error(t, err)
		got, err := f.svc.GetFieldDef(ctx, other.ID)
		require.NoError(t, err)
		require.Equal(t, 1, got.SortOrder)
	})
	t.Run("and with no actor, the system's change commits with its entries", func(t *testing.T) {
		name := "Department"
		_, err := f.svc.PatchFieldDef(ctx, def.ID, customfield.FieldDefPatch{Name: &name}, nil)
		require.NoError(t, err)
		es, err := auditstore.New(f.db.Queries).ListByEntity(ctx, "custom_field", def.ID, 20, 0)
		require.NoError(t, err)
		var actions []string
		for _, e := range es {
			actions = append(actions, e.Action)
		}
		require.Contains(t, actions, "custom_field_renamed")
		require.Nil(t, es[0].ActorID)
	})
}

func (f *lockFixture) categoryID() uuid.UUID {
	f.t.Helper()
	var id uuid.UUID
	require.NoError(f.t, f.db.SQL.QueryRow(`SELECT id FROM categories LIMIT 1`).Scan(&id))
	return id
}

// ── Creation is all or nothing ───────────────────────────────────────────────

// When the write of one value fails, none of the ticket's values is stored and
// each non-empty entry is reported as not_saved. Needs a real transaction: in the
// rolled-back harness nothing could be read back after the failure.
func TestSetCreationValues_AFailedWriteStoresNothing(t *testing.T) {
	f := newLockFixture(t)
	ctx := context.Background()
	catID := f.categoryID()
	first := f.field("Serial", customfield.FieldTypeText)
	last := f.field("Colour", customfield.FieldTypeText)
	// Values are written in id order: the failing entry must come second, so the
	// valid one has been written when it fails.
	if first.ID.String() > last.ID.String() {
		first, last = last, first
	}
	for _, d := range []customfield.FieldDef{first, last} {
		_, err := f.svc.CreateAssignment(ctx, d.ID, customfield.CategoryScope, catID, 1, true, false, nil)
		require.NoError(t, err)
	}
	// The trigger refuses one value, the way a full disk or a lost connection
	// would refuse a write.
	_, err := f.db.SQL.ExecContext(ctx, `
		CREATE FUNCTION refuse_boom() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.value = 'boom' THEN RAISE EXCEPTION 'boom'; END IF; RETURN NEW; END $$;
		CREATE TRIGGER refuse_boom BEFORE INSERT OR UPDATE ON ticket_custom_field_values
		FOR EACH ROW EXECUTE FUNCTION refuse_boom();`)
	require.NoError(t, err)

	warnings, err := f.svc.SetCreationValues(ctx, f.ticketID, catID, nil, nil, map[string]string{
		first.ID.String(): "7", last.ID.String(): "boom",
	})
	require.Error(t, err, "the failure is returned for the caller to log")
	require.Len(t, warnings, 2)
	for _, w := range warnings {
		require.Equal(t, customfield.ReasonNotSaved, w.Reason)
	}
	var n int
	require.NoError(t, f.db.SQL.QueryRow(`SELECT count(*) FROM ticket_custom_field_values WHERE ticket_id = $1`, f.ticketID).Scan(&n))
	require.Zero(t, n, "the valid entry must not be stored when another failed")

	// And the same entries without the failure are stored together.
	warnings, err = f.svc.SetCreationValues(ctx, f.ticketID, catID, nil, nil, map[string]string{
		first.ID.String(): "7", last.ID.String(): "fine",
	})
	require.NoError(t, err)
	require.Empty(t, warnings)
	require.NoError(t, f.db.SQL.QueryRow(`SELECT count(*) FROM ticket_custom_field_values WHERE ticket_id = $1`, f.ticketID).Scan(&n))
	require.Equal(t, 2, n)
}
