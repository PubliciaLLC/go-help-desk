package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
	"github.com/publiciallc/go-help-desk/backend/internal/mcp"
)

// Value limits (owner, 2026-11-11): 255 characters for single-line fields, 10,000
// for long text, counted in characters, refused with 400 by every door before
// anything is created. 10,000 four-byte characters is 40 KB, which the index
// under the values used to refuse.

// variedRunes is n characters from lo to hi, the same every run: text that does
// not compress, which is what an index row limit measures.
func variedRunes(n int, lo, hi rune) string {
	var b strings.Builder
	x := uint32(7)
	for i := 0; i < n; i++ {
		x = x*1664525 + 1013904223
		b.WriteRune(lo + rune(x>>8)%(hi-lo+1))
	}
	return b.String()
}

func emoji(n int) string           { return variedRunes(n, 0x1F300, 0x1F64F) }
func cjk(n int) string             { return variedRunes(n, 0x4E00, 0x9FFF) }
func ascii(n int) string           { return strings.Repeat("a", n) }
func exactly(s string, n int) bool { return len([]rune(s)) == n }

// limitFixture is the harness category with one single-line and one long-text
// field, shown on the form and optional.
type limitFixture struct{ line, long customfield.FieldDef }

func newLimitFixture(t *testing.T, h *harness) limitFixture {
	t.Helper()
	f := limitFixture{
		line: h.mkField(t, "Subject line", customfield.FieldTypeText, nil, 1),
		long: h.mkField(t, "Error log", customfield.FieldTypeTextarea, nil, 2),
	}
	h.attach(t, f.line, customfield.CategoryScope, h.catID, 1)
	h.attach(t, f.long, customfield.CategoryScope, h.catID, 2)
	return f
}

func TestCreateTicket_ValueLimits(t *testing.T) {
	for _, caller := range []string{"staff", "admin", "user"} {
		t.Run(caller, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			f := newLimitFixture(t, h)
			before := ticketCount(t, h)

			for name, fields := range map[string]map[string]string{
				"single-line, 256 characters":     {f.line.ID.String(): ascii(256)},
				"single-line, 256 two-byte":       {f.line.ID.String(): strings.Repeat("é", 256)},
				"long text, 10,001 four-byte":     {f.long.ID.String(): emoji(10_001)},
				"long text, 10,001 ascii":         {f.long.ID.String(): ascii(10_001)},
				"one good value and one too long": {f.line.ID.String(): "fine", f.long.ID.String(): cjk(10_001)},
			} {
				code, body := h.postTicket(t, caller, fields)
				require.Equal(t, http.StatusBadRequest, code, "%s: %.200s", name, body)
				require.Contains(t, string(body), "too long", name)
				require.Less(t, len(body), 1000, "%s: the refusal does not echo the value", name)
			}
			require.Equal(t, before, ticketCount(t, h), "a refused request writes no ticket")

			good := map[string]string{f.line.ID.String(): strings.Repeat("😀", 255), f.long.ID.String(): emoji(10_000)}
			code, body := h.postTicket(t, caller, good)
			require.Equal(t, http.StatusCreated, code, "exactly the limit, in characters: %.200s", body)
			var created createdBody
			require.NoError(t, json.Unmarshal(body, &created))
			require.Empty(t, created.Warnings)
			stored := h.storedValues(t, created.ID)
			require.Equal(t, good[f.long.ID.String()], stored[f.long.ID.String()], "read back exactly")
			require.True(t, exactly(stored[f.line.ID.String()], 255))
		})
	}
}

func TestPutCustomFields_ValueLimits(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newLimitFixture(t, h)
	tk := h.newTicket(t, nil, nil)
	path := "/api/v1/tickets/" + tk.ID.String() + "/custom-fields"

	res := h.do(t, http.MethodPut, path, map[string]string{f.long.ID.String(): cjk(10_001)})
	require.Equal(t, http.StatusBadRequest, res.StatusCode, string(readAll(t, res)))
	res = h.do(t, http.MethodPut, path, map[string]string{f.line.ID.String(): "ok", f.long.ID.String(): cjk(10_001)})
	require.Equal(t, http.StatusBadRequest, res.StatusCode, "all or none")
	res.Body.Close()
	require.Empty(t, h.storedValues(t, tk.ID.String()))

	res = h.do(t, http.MethodPut, path, map[string]string{f.long.ID.String(): cjk(10_000)})
	require.Equal(t, http.StatusNoContent, res.StatusCode, string(readAll(t, res)))
	require.Equal(t, cjk(10_000), h.storedValues(t, tk.ID.String())[f.long.ID.String()])
}

// A value stored before the limits, over one, stays as it is and shows; a save
// that does not send it is unaffected, and sending it back unchanged is not checked again.
func TestPutCustomFields_AValueAlreadyOverTheLimitStaysAndIsNotRecheckedUnchanged(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newLimitFixture(t, h)
	tk := h.newTicket(t, nil, nil)
	old := ascii(400)
	_, err := h.tx.ExecContext(context.Background(),
		`INSERT INTO ticket_custom_field_values (ticket_id, field_def_id, value) VALUES ($1, $2, $3)`, tk.ID, f.line.ID, old)
	require.NoError(t, err)
	path := "/api/v1/tickets/" + tk.ID.String() + "/custom-fields"

	res := h.do(t, http.MethodPut, path, map[string]string{f.long.ID.String(): "another field changed"})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "an edit that leaves the old value out is allowed")
	require.Equal(t, old, h.storedValues(t, tk.ID.String())[f.line.ID.String()], "and the value is left as it was")

	res = h.do(t, http.MethodPut, path, map[string]string{f.line.ID.String(): old})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "sending it back unchanged is not checked again")
	res.Body.Close()
	require.Equal(t, old, h.storedValues(t, tk.ID.String())[f.line.ID.String()])

	res = h.do(t, http.MethodPut, path, map[string]string{f.line.ID.String(): ascii(401)})
	require.Equal(t, http.StatusBadRequest, res.StatusCode, "a different value is held to the limit")
	res.Body.Close()
}

func TestGuestCreate_ValueLimits(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	f := newGuestFixture(t, h)
	long := h.mkField(t, "Error log", customfield.FieldTypeTextarea, nil, 5)
	_, err := h.customFieldSvc.Attach(ctx, customfield.Assignment{
		FieldDefID: long.ID, ScopeType: customfield.CategoryScope, ScopeID: h.catID, VisibleOnNew: true, VisibleToGuests: true,
	}, nil)
	require.NoError(t, err)
	before := ticketCount(t, h)

	for name, fields := range map[string]map[string]string{
		"a required single-line, 256": {f.serial.ID.String(): ascii(256)},
		"long text, 10,001":           {f.serial.ID.String(): "SN", long.ID.String(): emoji(10_001)},
	} {
		code, body := h.guestCreate(t, fields)
		require.Equal(t, http.StatusBadRequest, code, "%s: %.200s", name, body)
		require.Contains(t, string(body), "too long", name)
	}
	require.Equal(t, before, ticketCount(t, h))

	code, body := h.guestCreate(t, map[string]string{f.serial.ID.String(): "SN", long.ID.String(): emoji(10_000)})
	require.Equal(t, http.StatusCreated, code, string(body))
	var created map[string]string
	require.NoError(t, json.Unmarshal(body, &created))
	var ticketID uuid.UUID
	require.NoError(t, h.tx.QueryRowContext(ctx, `SELECT id FROM tickets WHERE tracking_number = $1`, created["tracking_number"]).Scan(&ticketID))
	require.Equal(t, emoji(10_000), h.storedValues(t, ticketID.String())[long.ID.String()])
}

func TestMCPCreateTicket_ValueLimits(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	s := mcpFieldSession(t, h)
	f := newLimitFixture(t, h)
	before := ticketCount(t, h)

	text, isErr := h.mcpCreate(t, s, map[string]any{f.long.ID.String(): emoji(10_001)})
	require.True(t, isErr, text)
	require.Contains(t, text, "too long")
	text, isErr = h.mcpCreate(t, s, map[string]any{f.line.ID.String(): ascii(256)})
	require.True(t, isErr, text)
	require.Equal(t, before, ticketCount(t, h))

	text, isErr = h.mcpCreate(t, s, map[string]any{f.long.ID.String(): emoji(10_000)})
	require.False(t, isErr, text)
	var created struct{ ID string }
	require.NoError(t, json.Unmarshal([]byte(text), &created))
	require.Equal(t, emoji(10_000), h.storedValues(t, created.ID)[f.long.ID.String()])
}

// Options longer than a select value may be could never be chosen.
func TestFieldDefs_RefuseAnOptionOver255Characters(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	code, body := h.cf(t, http.MethodPost, cfAdmin, map[string]any{"name": "Pick", "field_type": "select", "options": []string{ascii(256)}})
	require.Equal(t, http.StatusBadRequest, code, string(body))
	code, body = h.cf(t, http.MethodPost, cfAdmin, map[string]any{"name": "Pick", "field_type": "select", "options": []string{ascii(255)}})
	require.Equal(t, http.StatusCreated, code, string(body))
}

// ── A failed write of the values ─────────────────────────────────────────────

// failValueWrites makes every insert into the values table fail, as a database
// fault would. The harness transaction is aborted by it, so a test that uses it
// looks only at the answer.
func (h *harness) failValueWrites(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	_, err := h.tx.ExecContext(ctx, `CREATE FUNCTION g6_refuse_values() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'values refused'; END $$`)
	require.NoError(t, err)
	_, err = h.tx.ExecContext(ctx, `CREATE TRIGGER g6_refuse_values BEFORE INSERT ON ticket_custom_field_values FOR EACH ROW EXECUTE FUNCTION g6_refuse_values()`)
	require.NoError(t, err)
}

// When a guest's values cannot be saved after the ticket was created, the guest
// is told the truth in a success-like answer (owner, 2026-11-11: "Say it was
// filed, check email"): received, some details not saved, check the email for the
// link and add them there. Not a 500, which invites a retry that files the ticket
// twice; the tracking number is given as it is on success.
func TestGuestCreate_AFailedValueWriteSaysTheTicketWasReceivedAndToCheckEmail(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newGuestFixture(t, h)
	h.failValueWrites(t)

	code, body := h.guestCreate(t, map[string]string{f.serial.ID.String(): "SN-1"})
	require.Equal(t, http.StatusCreated, code, "the ticket exists: %s", body)
	var got map[string]any
	require.NoError(t, json.Unmarshal(body, &got))
	require.NotEmpty(t, got["tracking_number"], "shown as on success")
	require.Equal(t, false, got["custom_fields_saved"])
	notice, _ := got["notice"].(string)
	require.Contains(t, notice, "received")
	require.Contains(t, notice, "could not be saved")
	require.Contains(t, notice, "email")
	require.NotContains(t, strings.ToLower(notice), "try again", "no retry: that would file it twice")
	require.NotContains(t, strings.ToLower(notice), "retry")
	require.NotContains(t, string(body), "values refused", "and nothing of the database's words")
}

// A success where the values were saved is as it was: the tracking number alone.
func TestGuestCreate_ASavedWriteIsStillTheTrackingNumberAlone(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newGuestFixture(t, h)

	code, body := h.guestCreate(t, map[string]string{f.serial.ID.String(): "SN-1"})
	require.Equal(t, http.StatusCreated, code, string(body))
	var got map[string]any
	require.NoError(t, json.Unmarshal(body, &got))
	require.Len(t, got, 1)
	require.Contains(t, got, "tracking_number")
}

func TestCreateTicket_AFailedValueWriteFailsTheRequestWhenAFieldIsRequired(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	serial := h.requireField(t, "Serial", customfield.FieldTypeText, nil)
	h.failValueWrites(t)

	code, body := h.postTicket(t, "staff", map[string]string{serial.ID.String(): "SN-1"})
	require.Equal(t, http.StatusInternalServerError, code, string(body))
	require.NotContains(t, string(body), "values refused")
}

// Where nothing is required the values stay best-effort, as they always were: a
// 201 that says what was not saved.
func TestCreateTicket_AFailedValueWriteIsStillAWarningWhenNothingIsRequired(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newLimitFixture(t, h)
	h.failValueWrites(t)

	code, body := h.postTicket(t, "staff", map[string]string{f.line.ID.String(): "x"})
	require.Equal(t, http.StatusCreated, code, string(body))
	var created createdBody
	require.NoError(t, json.Unmarshal(body, &created))
	require.Equal(t, []customfield.Warning{{FieldID: f.line.ID.String(), Reason: customfield.ReasonNotSaved}}, created.Warnings)
}

func TestMCPCreateTicket_AFailedValueWriteFailsTheCallWhenAFieldIsRequired(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	s := mcpFieldSession(t, h)
	serial := h.requireField(t, "Serial", customfield.FieldTypeText, nil)
	h.failValueWrites(t)

	text, isErr := h.mcpCreate(t, s, map[string]any{serial.ID.String(): "SN-1"})
	require.True(t, isErr, text)
	require.NotContains(t, text, "values refused")
}

// ── Follow-ups are exempt from the required rule ─────────────────────────────

// A follow-up copies its original's classification and no values, and its route
// takes none, so it could never satisfy a required field. It is filed without
// one, by every door.
func TestFollowUp_IsNotHeldToRequiredFields(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	h.requireField(t, "Serial", customfield.FieldTypeText, nil)

	t.Run("staff", func(t *testing.T) {
		tk := reporterClosedTicket(t, h)
		res := h.do(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/follow-up", nil)
		require.Equal(t, http.StatusCreated, res.StatusCode, readBody(t, res))
	})
	t.Run("requester", func(t *testing.T) {
		tk := reporterClosedTicket(t, h)
		res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/follow-up", nil)
		require.Equal(t, http.StatusCreated, res.StatusCode, readBody(t, res))
	})
	t.Run("guest link", func(t *testing.T) {
		tk, token := seedGuestTicket(t, h)
		closeGuestTicket(t, h, tk)
		res := h.doGuest(t, http.MethodPost, "/api/v1/guest/follow-up", token, map[string]any{})
		require.Equal(t, http.StatusCreated, res.StatusCode, readBody(t, res))
	})
	t.Run("MCP", func(t *testing.T) {
		tk := reporterClosedTicket(t, h)
		s := mcpFieldSession(t, h)
		text, isErr := s.tool("create_follow_up", map[string]any{"ticket_id": tk.ID.String()})
		require.False(t, isErr, text)
	})
	t.Run("and a new ticket still is", func(t *testing.T) {
		code, _ := h.postTicket(t, "staff", nil)
		require.Equal(t, http.StatusBadRequest, code)
	})
}

// ── A guest cannot tell a retired option from one that never existed ─────────

func TestGuestCreate_ARetiredOptionAnswersLikeAnUnknownOne(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newGuestFixture(t, h)

	_, retired := h.guestCreate(t, map[string]string{f.serial.ID.String(): "SN", f.colour.ID.String(): "blue"})
	code, unknown := h.guestCreate(t, map[string]string{f.serial.ID.String(): "SN", f.colour.ID.String(): "purple"})
	require.Equal(t, http.StatusBadRequest, code)
	require.Equal(t, strings.ReplaceAll(string(unknown), "purple", "blue"), string(retired))
	require.NotContains(t, string(retired), "retired")
}

// ── MCP get_ticket values are for staff, and the lookup is not even made ─────

type spyFields struct {
	*customfield.Service
	valueReads int
}

func (s *spyFields) ListValuesForTicket(ctx context.Context, id uuid.UUID) ([]customfield.TicketFieldValue, error) {
	s.valueReads++
	return s.Service.ListValuesForTicket(ctx, id)
}

var _ mcp.CustomFields = (*spyFields)(nil)

func TestMCPGetTicket_CustomFieldValuesAreStaffOnly(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	field := h.mkField(t, "Internal code", customfield.FieldTypeText, nil, 1)
	h.attach(t, field, customfield.CategoryScope, h.catID, 1)
	tk := reporterClosedTicket(t, h) // the requester's own ticket
	require.NoError(t, h.customFieldSvc.SetValues(ctx, tk.ID, map[uuid.UUID]string{field.ID: "P1-secret"}))

	for _, tc := range []struct {
		name      string
		key       string
		wantReads int
		wantValue bool
	}{
		{"a requester key", h.userKey, 0, false},
		{"a staff key", h.apiKey, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spy := &spyFields{Service: h.customFieldSvc}
			m := mcp.New(h.ticketSvc, nil, h.srv, h.categorySvc, h.srv, h.srv).WithCustomFields(spy)
			c := openMCPOn(t, h, tc.key, m)
			t.Cleanup(c.closeBody)

			got := c.call(t, "get_ticket", map[string]any{"id": tk.ID.String()})
			require.Contains(t, got, tk.TrackingNumber, "the ticket itself is shown: %s", got)
			require.Equal(t, tc.wantValue, strings.Contains(got, "P1-secret"), got)
			require.Equal(t, tc.wantValue, strings.Contains(got, "custom_fields"), got)
			require.Equal(t, tc.wantReads, spy.valueReads,
				"a requester's lookup is not made at all, so removing the gate is not hidden by the response filter")
		})
	}
}
