package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/customfieldstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// PUT /tickets/{id}/custom-fields is all-or-nothing: every entry is checked
// before any is written. The body is a JSON object and Go ranges over a map in
// random order, so the old per-entry loop wrote whichever valid entries came
// before the invalid one, and a different set on the next try.

// atomicFixture is a ticket whose category carries a text field, a number
// field and a select field.
type atomicFixture struct {
	ticketID           string
	text, number, pick customfield.FieldDef
}

func newAtomicFixture(t *testing.T, h *harness) atomicFixture {
	t.Helper()
	ctx := context.Background()
	mk := func(name string, ft customfield.FieldType, opts []string, order int) customfield.FieldDef {
		def, err := h.customFieldSvc.CreateFieldDef(ctx, name, ft, opts, order, nil)
		require.NoError(t, err)
		_, err = h.customFieldSvc.CreateAssignment(ctx, def.ID, customfield.CategoryScope, h.catID, order, true, false, nil)
		require.NoError(t, err)
		return def
	}
	f := atomicFixture{
		text:   mk("Serial", customfield.FieldTypeText, nil, 1),
		number: mk("Seats", customfield.FieldTypeNumber, nil, 2),
		pick:   mk("Colour", customfield.FieldTypeSelect, []string{"red", "blue"}, 3),
	}
	res := h.do(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "Has fields", "description": "x", "category_id": h.catID.String(),
	})
	require.Equal(t, http.StatusCreated, res.StatusCode)
	var created struct {
		ID string `json:"id"`
	}
	decodeJSON(t, res, &created)
	f.ticketID = created.ID
	return f
}

func (f atomicFixture) put(t *testing.T, h *harness, body map[string]string) int {
	t.Helper()
	res := h.do(t, http.MethodPut, "/api/v1/tickets/"+f.ticketID+"/custom-fields", body)
	res.Body.Close()
	return res.StatusCode
}

func (f atomicFixture) stored(t *testing.T, h *harness) map[string]string {
	t.Helper()
	res := h.do(t, http.MethodGet, "/api/v1/tickets/"+f.ticketID+"/custom-fields", nil)
	defer res.Body.Close()
	var got []customfield.TicketFieldValue
	decodeJSON(t, res, &got)
	out := map[string]string{}
	for _, v := range got {
		out[v.FieldDefID.String()] = v.Value
	}
	return out
}

func TestPutCustomFields_ARefusedEntryWritesNothing(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newAtomicFixture(t, h)
	// A real field that no scope assigns, so this ticket does not have it.
	detached, err := h.customFieldSvc.CreateFieldDef(context.Background(), "Elsewhere", customfield.FieldTypeText, nil, 9, nil)
	require.NoError(t, err)

	for name, bad := range map[string]map[string]string{
		"option outside the list": {f.pick.ID.String(): "green"},
		"field not on the ticket": {detached.ID.String(): "x"},
	} {
		t.Run(name, func(t *testing.T) {
			// Several rounds: with the old loop the valid entries landed or
			// not depending on map order, so one pass could pass by luck.
			for range 20 {
				body := map[string]string{f.text.ID.String(): "SN-1", f.number.ID.String(): "4"}
				for k, v := range bad {
					body[k] = v
				}
				require.Equal(t, http.StatusBadRequest, f.put(t, h, body))
				require.Empty(t, f.stored(t, h), "the valid entries must not land when another is refused")
			}
		})
	}
}

func TestPutCustomFields_ADeactivatedEntryWritesNothing(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newAtomicFixture(t, h)
	f.number.Active = false
	require.NoError(t, h.customFieldSvc.UpdateFieldDef(context.Background(), f.number, nil))

	for range 20 {
		require.Equal(t, http.StatusBadRequest, f.put(t, h, map[string]string{
			f.text.ID.String(): "SN-1", f.number.ID.String(): "4",
		}))
		require.Empty(t, f.stored(t, h))
	}
}

func TestPutCustomFields_AllEntriesWrittenTogether(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newAtomicFixture(t, h)
	require.Equal(t, http.StatusNoContent, f.put(t, h, map[string]string{f.number.ID.String(): "9"}))

	// A set, a change and a clear in one save.
	require.Equal(t, http.StatusNoContent, f.put(t, h, map[string]string{
		f.text.ID.String():   "SN-1",
		f.pick.ID.String():   "blue",
		f.number.ID.String(): "",
	}))
	require.Equal(t, map[string]string{
		f.text.ID.String(): "SN-1",
		f.pick.ID.String(): "blue",
	}, f.stored(t, h))
}

func TestPutCustomFields_OnlyTheNamedFieldsAreTouched(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newAtomicFixture(t, h)
	require.Equal(t, http.StatusNoContent, f.put(t, h, map[string]string{
		f.text.ID.String(): "SN-1", f.pick.ID.String(): "red",
	}))

	// The card sends only what changed, so a field it leaves out must keep its value.
	require.Equal(t, http.StatusNoContent, f.put(t, h, map[string]string{f.text.ID.String(): "SN-2"}))
	require.Equal(t, map[string]string{
		f.text.ID.String(): "SN-2",
		f.pick.ID.String(): "red",
	}, f.stored(t, h))
}

// The harness joins one rolled-back transaction, so it cannot show that the
// store's own transaction commits or rolls back. This does, against a database
// where the writes really commit.
func TestSetValues_RealTransaction_CommitsAllOrRollsBackAll(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	ctx := context.Background()
	store := customfieldstore.New(db.Queries, db.SQL)
	svc := customfield.NewService(store)

	cat := category.Category{ID: uuid.New(), Name: "Atomic", SortOrder: 1, Active: true}
	require.NoError(t, categorystore.New(db.Queries).CreateCategory(ctx, cat))
	var ticketID uuid.UUID
	require.NoError(t, db.SQL.QueryRowContext(ctx, `
		INSERT INTO tickets (tracking_number, subject, category_id, status_id, guest_email, description_format, source_channel)
		VALUES ('ATOMIC-1', 'x', $1, (SELECT id FROM statuses LIMIT 1), 'g@example.test', 'plain', 'web')
		RETURNING id`, cat.ID).Scan(&ticketID))

	// Two fields, ordered so the one the trigger below refuses is written
	// second: the first write has happened by the time the second fails.
	a, b := uuid.New(), uuid.New()
	if a.String() > b.String() {
		a, b = b, a
	}
	for _, id := range []uuid.UUID{a, b} {
		_, err := store.CreateFieldDef(ctx, customfield.FieldDef{
			ID: id, Name: "F-" + id.String()[:8], FieldType: customfield.FieldTypeText, Active: true,
		}, nil)
		require.NoError(t, err)
	}

	require.NoError(t, svc.SetValues(ctx, ticketID, map[uuid.UUID]string{a: "one", b: "two"}))
	vals, err := svc.ListValuesForTicket(ctx, ticketID)
	require.NoError(t, err)
	require.Len(t, vals, 2, "committed: visible from another connection")

	_, err = db.SQL.ExecContext(ctx, `
		CREATE FUNCTION refuse_boom() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.value = 'boom' THEN RAISE EXCEPTION 'boom'; END IF; RETURN NEW; END $$;
		CREATE TRIGGER refuse_boom BEFORE INSERT OR UPDATE ON ticket_custom_field_values
		FOR EACH ROW EXECUTE FUNCTION refuse_boom();`)
	require.NoError(t, err)

	require.Error(t, svc.SetValues(ctx, ticketID, map[uuid.UUID]string{a: "changed", b: "boom"}))
	vals, err = svc.ListValuesForTicket(ctx, ticketID)
	require.NoError(t, err)
	got := map[uuid.UUID]string{}
	for _, v := range vals {
		got[v.FieldDefID] = v.Value
	}
	require.Equal(t, map[uuid.UUID]string{a: "one", b: "two"}, got, "the write before the failure was rolled back")
}
