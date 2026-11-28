package server_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
)

// Number fields take numbers (#603, "Validate now"). The rule itself is
// customfield.IsNumber, tested on its own; this is what the routes do with it.

type numberFixture struct {
	ticketID string
	number   customfield.FieldDef
	other    customfield.FieldDef
}

func newNumberFixture(t *testing.T, h *harness) numberFixture {
	t.Helper()
	f := numberFixture{
		number: h.mkField(t, "Cost", customfield.FieldTypeNumber, nil, 1),
		other:  h.mkField(t, "Serial", customfield.FieldTypeText, nil, 2),
	}
	h.attach(t, f.number, customfield.CategoryScope, h.catID, 1)
	h.attach(t, f.other, customfield.CategoryScope, h.catID, 2)
	f.ticketID = h.newTicket(t, nil, nil).ID.String()
	return f
}

func (f numberFixture) put(t *testing.T, h *harness, body map[string]string) (int, []byte) {
	t.Helper()
	res := h.do(t, http.MethodPut, "/api/v1/tickets/"+f.ticketID+"/custom-fields", body)
	return res.StatusCode, readAll(t, res)
}

func TestPutCustomFields_NumberFieldRefusesText(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newNumberFixture(t, h)

	for _, bad := range []string{"abc", "1,5", "0x10", "NaN", "Inf", "1e400", "1_000", "12 apples"} {
		code, body := f.put(t, h, map[string]string{f.number.ID.String(): bad, f.other.ID.String(): "SN-1"})
		require.Equal(t, http.StatusBadRequest, code, "%q: %s", bad, body)
		require.Equal(t, "bad_request", errCode(t, body))
		require.Contains(t, string(body), "is not a number for field")
		require.Empty(t, h.storedValues(t, f.ticketID), "%q: nothing is written, the text field included", bad)
	}
	code, body := f.put(t, h, map[string]string{f.number.ID.String(): "abc"})
	require.Equal(t, http.StatusBadRequest, code)
	require.Contains(t, string(body), `value \"abc\" is not a number for field \"Cost\"`)
}

func TestPutCustomFields_NumberStoredAsWritten(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newNumberFixture(t, h)

	for in, stored := range map[string]string{" 1.50 ": "1.50", "-3.5": "-3.5", ".5": ".5", "1e3": "1e3", "+1": "+1", "007": "007"} {
		code, body := f.put(t, h, map[string]string{f.number.ID.String(): in})
		require.Equal(t, http.StatusNoContent, code, "%q: %s", in, body)
		require.Equal(t, stored, h.storedValues(t, f.ticketID)[f.number.ID.String()], "%q is stored trimmed and not reformatted", in)
	}

	// Spaces only is a clear.
	code, _ := f.put(t, h, map[string]string{f.number.ID.String(): "   "})
	require.Equal(t, http.StatusNoContent, code)
	require.Empty(t, h.storedValues(t, f.ticketID))
}

// A value stored before 1.3.0 is left as it is. It blocks only a save that
// changes that field, not one that leaves it out.
func TestPutCustomFields_StoredBadNumberDoesNotBlockOtherFields(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newNumberFixture(t, h)
	_, err := h.tx.ExecContext(context.Background(),
		`INSERT INTO ticket_custom_field_values (ticket_id, field_def_id, value) VALUES ($1, $2, 'about ten')`, f.ticketID, f.number.ID)
	require.NoError(t, err)

	code, body := f.put(t, h, map[string]string{f.other.ID.String(): "SN-2"})
	require.Equal(t, http.StatusNoContent, code, string(body))
	require.Equal(t, map[string]string{f.number.ID.String(): "about ten", f.other.ID.String(): "SN-2"}, h.storedValues(t, f.ticketID),
		"the old value is shown as stored")

	code, _ = f.put(t, h, map[string]string{f.number.ID.String(): "about eleven"})
	require.Equal(t, http.StatusBadRequest, code, "changing that field is held to the rule")
	code, _ = f.put(t, h, map[string]string{f.number.ID.String(): ""})
	require.Equal(t, http.StatusNoContent, code, "a clear is always allowed")
}

func TestCreateTicket_NotANumberWarning(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newNumberFixture(t, h)

	code, got, raw := h.createWith(t, h.do, map[string]string{f.number.ID.String(): "abc", f.other.ID.String(): "SN-1"})
	require.Equal(t, http.StatusCreated, code, string(raw))
	require.Equal(t, []customfield.Warning{{FieldID: f.number.ID.String(), Reason: customfield.ReasonNotANumber}}, got.Warnings)
	require.Equal(t, map[string]string{f.other.ID.String(): "SN-1"}, h.storedValues(t, got.ID))

	code, got, raw = h.createWith(t, h.do, map[string]string{f.number.ID.String(): " 2.50 "})
	require.Equal(t, http.StatusCreated, code, string(raw))
	require.Empty(t, got.Warnings)
	require.Equal(t, map[string]string{f.number.ID.String(): "2.50"}, h.storedValues(t, got.ID), "valid, and stored trimmed")
}

// Two keys that spell one UUID differently used to collide in a map, and the
// value that was written was whichever the range met last. Refused now.
func TestPutCustomFields_TheSameFieldTwiceIsRefused(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newNumberFixture(t, h)
	lower := f.other.ID.String()
	upper := strings.ToUpper(lower)

	for range 10 {
		code, body := f.put(t, h, map[string]string{lower: "one", upper: "two"})
		require.Equal(t, http.StatusBadRequest, code, string(body))
		require.Contains(t, string(body), "listed twice")
		require.Empty(t, h.storedValues(t, f.ticketID), "nothing is written")
	}
	code, _ := f.put(t, h, map[string]string{upper: "only"})
	require.Equal(t, http.StatusNoContent, code, "one spelling is fine")
	require.Equal(t, "only", h.storedValues(t, f.ticketID)[lower])
}
