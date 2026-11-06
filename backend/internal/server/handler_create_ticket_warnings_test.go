package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
)

// POST /tickets answers 201 whatever happens to the custom field values sent
// with it, and now says which of them were not written (#603).

type createdBody struct {
	ID       string                `json:"id"`
	Warnings []customfield.Warning `json:"warnings"`
}

// warnFixture is a category with one field of each kind the reasons need.
type warnFixture struct {
	good, inactive, retired, number, selectF, elsewhere customfield.FieldDef
}

func newWarnFixture(t *testing.T, h *harness) warnFixture {
	t.Helper()
	ctx := context.Background()
	mk := func(name string, ft customfield.FieldType, opts []string, order int) customfield.FieldDef {
		d := h.mkField(t, name, ft, opts, order)
		h.attach(t, d, customfield.CategoryScope, h.catID, order)
		return d
	}
	f := warnFixture{
		good:     mk("Serial", customfield.FieldTypeText, nil, 1),
		inactive: mk("Gone", customfield.FieldTypeText, nil, 2),
		number:   mk("Cost", customfield.FieldTypeNumber, nil, 3),
		selectF:  mk("Colour", customfield.FieldTypeSelect, []string{"red", "blue", "green"}, 4),
		// A real field attached to nothing, so this ticket does not have it.
		elsewhere: h.mkField(t, "Elsewhere", customfield.FieldTypeText, nil, 9),
	}
	f.retired = f.selectF
	f.inactive.Active = false
	require.NoError(t, h.customFieldSvc.UpdateFieldDef(ctx, f.inactive, nil))
	code, body := h.patchField(t, f.selectF.ID, map[string]any{"options": []string{"red", "blue"}, "retired_options": []string{"green"}})
	require.Equal(t, http.StatusOK, code, string(body))
	return f
}

func (h *harness) createWith(t *testing.T, as func(t *testing.T, method, path string, body any) *http.Response, fields map[string]string) (int, createdBody, []byte) {
	t.Helper()
	body := map[string]any{"subject": "With fields", "description": "x", "category_id": h.catID.String()}
	if fields != nil {
		body["custom_fields"] = fields
	}
	res := as(t, http.MethodPost, "/api/v1/tickets", body)
	raw := readAll(t, res)
	var out createdBody
	require.NoError(t, json.Unmarshal(raw, &out), string(raw))
	return res.StatusCode, out, raw
}

func (h *harness) storedValues(t *testing.T, ticketID string) map[string]string {
	t.Helper()
	res := h.do(t, http.MethodGet, "/api/v1/tickets/"+ticketID+"/custom-fields", nil)
	var got []customfield.TicketFieldValue
	decodeJSON(t, res, &got)
	res.Body.Close()
	out := map[string]string{}
	for _, v := range got {
		out[v.FieldDefID.String()] = v.Value
	}
	return out
}

func TestCreateTicket_WarningsNameEachDroppedField(t *testing.T) {
	for _, tc := range []struct {
		name   string
		key    func(f warnFixture) string
		value  string
		reason customfield.Reason
	}{
		{"the key is not a UUID", func(warnFixture) string { return "not-a-uuid" }, "x", customfield.ReasonInvalidFieldID},
		{"the field is not on this ticket", func(f warnFixture) string { return f.elsewhere.ID.String() }, "x", customfield.ReasonNotOnTicket},
		{"an id that names no field", func(warnFixture) string { return uuid.NewString() }, "x", customfield.ReasonNotOnTicket},
		{"the field is deactivated", func(f warnFixture) string { return f.inactive.ID.String() }, "x", customfield.ReasonInactive},
		{"a retired option", func(f warnFixture) string { return f.selectF.ID.String() }, "green", customfield.ReasonRetiredOption},
		{"a value that is in neither list", func(f warnFixture) string { return f.selectF.ID.String() }, "purple", customfield.ReasonNotAnOption},
		{"not a number", func(f warnFixture) string { return f.number.ID.String() }, "abc", customfield.ReasonNotANumber},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			f := newWarnFixture(t, h)

			key := tc.key(f)
			code, got, raw := h.createWith(t, h.do, map[string]string{key: tc.value, f.good.ID.String(): "SN-1"})

			require.Equal(t, http.StatusCreated, code, "the ticket is still created: %s", raw)
			require.Equal(t, []customfield.Warning{{FieldID: key, Reason: tc.reason}}, got.Warnings)
			require.Equal(t, map[string]string{f.good.ID.String(): "SN-1"}, h.storedValues(t, got.ID),
				"the valid entry was written and the dropped one was not")
		})
	}
}

// Several at once come back in one stable order, sorted by field_id.
func TestCreateTicket_WarningsAreSortedByFieldID(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newWarnFixture(t, h)
	fields := map[string]string{
		"zzz-not-a-uuid":          "x",
		"aaa-not-a-uuid":          "x",
		f.inactive.ID.String():    "x",
		f.elsewhere.ID.String():   "x",
		f.number.ID.String():      "abc",
		f.good.ID.String():        "fine",
		f.selectF.ID.String():     "purple",
		"mmm-also-not-a-uuid-id1": "x",
	}
	for range 5 { // map order is random; the answer must not be
		code, got, raw := h.createWith(t, h.do, fields)
		require.Equal(t, http.StatusCreated, code, string(raw))
		require.Len(t, got.Warnings, 7)
		ids := make([]string, len(got.Warnings))
		for i, w := range got.Warnings {
			ids[i] = w.FieldID
		}
		require.True(t, sort.StringsAreSorted(ids), "%v", ids)
	}
}

func TestCreateTicket_NoWarningsKeyWhenNothingDropped(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newWarnFixture(t, h)

	keysOf := func(raw []byte) []string {
		var m map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &m))
		var ks []string
		for k := range m {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		return ks
	}
	_, _, without := h.createWith(t, h.do, nil)
	code, _, with := h.createWith(t, h.do, map[string]string{f.good.ID.String(): "SN-1"})
	require.Equal(t, http.StatusCreated, code)

	require.NotContains(t, string(with), "warnings", "the key is absent, not an empty list")
	require.Equal(t, keysOf(without), keysOf(with), "the response is what it was before warnings existed")
}

func TestCreateTicket_EmptyValueIsNotAWarning(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newWarnFixture(t, h)

	code, got, raw := h.createWith(t, h.do, map[string]string{
		f.good.ID.String():     "",
		f.inactive.ID.String(): "", // empty means "no value", even for a field that takes none
		"not-a-uuid":           "",
		f.number.ID.String():   "",
	})
	require.Equal(t, http.StatusCreated, code, string(raw))
	require.Empty(t, got.Warnings)
	require.NotContains(t, string(raw), "warnings")
	require.Empty(t, h.storedValues(t, got.ID))
}

// A requester sees the ids they sent themselves and a reason, nothing else; the
// wrapper filter has to let the key through.
func TestCreateTicket_RequesterReceivesWarnings(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newWarnFixture(t, h)

	code, got, raw := h.createWith(t, h.doAsUser, map[string]string{
		f.inactive.ID.String(): "x",
		f.good.ID.String():     "SN-1",
	})
	require.Equal(t, http.StatusCreated, code, string(raw))
	require.Equal(t, []customfield.Warning{{FieldID: f.inactive.ID.String(), Reason: customfield.ReasonInactive}}, got.Warnings)

	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &m))
	require.NotContains(t, m, "reporter_user_id", "the rest of a requester's view is as it was")
}

// A failure that is none of the above is reported as not_saved for every
// non-empty entry that reached the write, and the ticket is still created. The
// trigger refuses the write the way a full disk or a lost connection would.
//
// This checks the response only. That nothing is stored when a write fails needs
// a real transaction, which this harness does not open; see
// TestSetCreationValues_AFailedWriteStoresNothing.
func TestCreateTicket_AFailedWriteIsReportedAsNotSaved(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newWarnFixture(t, h)
	_, err := h.tx.ExecContext(context.Background(), `
		CREATE FUNCTION ghd_refuse_boom() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.value = 'boom' THEN RAISE EXCEPTION 'boom'; END IF; RETURN NEW; END $$;
		CREATE TRIGGER ghd_refuse_boom BEFORE INSERT OR UPDATE ON ticket_custom_field_values
		FOR EACH ROW EXECUTE FUNCTION ghd_refuse_boom();`)
	require.NoError(t, err)

	code, got, raw := h.createWith(t, h.do, map[string]string{
		f.good.ID.String():     "boom",
		f.number.ID.String():   "7",
		"not-a-uuid":           "x",
		f.inactive.ID.String(): "",
	})
	require.Equal(t, http.StatusCreated, code, string(raw))
	want := []customfield.Warning{
		{FieldID: f.good.ID.String(), Reason: customfield.ReasonNotSaved},
		{FieldID: f.number.ID.String(), Reason: customfield.ReasonNotSaved},
		{FieldID: "not-a-uuid", Reason: customfield.ReasonInvalidFieldID},
	}
	sort.Slice(want, func(i, j int) bool { return want[i].FieldID < want[j].FieldID })
	require.Equal(t, want, got.Warnings, "every entry that reached the write is reported, the valid number included")
}

// PUT refuses two spellings of one field with a 400. Creation never refuses: it
// drops both and says so.
func TestCreateTicket_TwoSpellingsOfOneFieldAreBothReported(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newWarnFixture(t, h)
	lower, upper := f.good.ID.String(), strings.ToUpper(f.good.ID.String())

	code, got, raw := h.createWith(t, h.do, map[string]string{lower: "one", upper: "two", f.number.ID.String(): "5"})
	require.Equal(t, http.StatusCreated, code, string(raw))
	want := []customfield.Warning{
		{FieldID: lower, Reason: customfield.ReasonDuplicateFieldID},
		{FieldID: upper, Reason: customfield.ReasonDuplicateFieldID},
	}
	sort.Slice(want, func(i, j int) bool { return want[i].FieldID < want[j].FieldID })
	require.Equal(t, want, got.Warnings)
	require.Equal(t, map[string]string{f.number.ID.String(): "5"}, h.storedValues(t, got.ID))
}
