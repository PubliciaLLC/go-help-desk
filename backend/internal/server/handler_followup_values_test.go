package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
)

// #625 item 8 (D24): a follow-up carries its original's custom field values.
// Staff: every field that resolves on the new ticket. A requester: only the
// fields the new-ticket form shows. Not atomic with the follow-up, which is
// already committed when the copy runs.

type followUpValues struct {
	shown, hidden customfield.FieldDef
	orig          uuid.UUID
}

// valuesFixture is a shown field and one the form hides, both on the harness
// category, with a value for each on a closed ticket.
func newFollowUpValues(t *testing.T, h *harness, closed func() uuid.UUID) followUpValues {
	t.Helper()
	ctx := context.Background()
	f := followUpValues{
		shown:  h.mkField(t, "Serial", customfield.FieldTypeText, nil, 1),
		hidden: h.mkField(t, "Internal code", customfield.FieldTypeText, nil, 2),
	}
	_, err := h.customFieldSvc.Attach(ctx, customfield.Assignment{FieldDefID: f.shown.ID, ScopeType: customfield.CategoryScope, ScopeID: h.catID, VisibleOnNew: true}, nil)
	require.NoError(t, err)
	_, err = h.customFieldSvc.Attach(ctx, customfield.Assignment{FieldDefID: f.hidden.ID, ScopeType: customfield.CategoryScope, ScopeID: h.catID}, nil)
	require.NoError(t, err)
	f.orig = closed()
	require.NoError(t, h.customFieldSvc.SetValues(ctx, f.orig, map[uuid.UUID]string{f.shown.ID: "SN-1", f.hidden.ID: "P1-secret"}))
	return f
}

func followUpID(t *testing.T, res *http.Response) string {
	t.Helper()
	var out struct {
		ID string `json:"id"`
	}
	body := readAll(t, res)
	require.Equal(t, http.StatusCreated, res.StatusCode, string(body))
	require.NoError(t, json.Unmarshal(body, &out))
	return out.ID
}

func TestFollowUp_StaffFollowUpCarriesEveryValue(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newFollowUpValues(t, h, func() uuid.UUID { return reporterClosedTicket(t, h).ID })

	id := followUpID(t, h.do(t, http.MethodPost, "/api/v1/tickets/"+f.orig.String()+"/follow-up", nil))
	require.Equal(t, map[string]string{f.shown.ID.String(): "SN-1", f.hidden.ID.String(): "P1-secret"}, h.storedValues(t, id))
}

func TestFollowUp_ARequesterCarriesOnlyWhatTheFormShows(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newFollowUpValues(t, h, func() uuid.UUID { return reporterClosedTicket(t, h).ID })

	id := followUpID(t, h.doAsUser(t, http.MethodPost, "/api/v1/tickets/"+f.orig.String()+"/follow-up", nil))
	require.Equal(t, map[string]string{f.shown.ID.String(): "SN-1"}, h.storedValues(t, id),
		"the field the form hides is not carried to a requester's ticket")
}

func TestFollowUp_AGuestLinkCarriesOnlyWhatTheFormShows(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	var token string
	f := newFollowUpValues(t, h, func() uuid.UUID {
		tk, tok := seedGuestTicket(t, h)
		closeGuestTicket(t, h, tk)
		token = tok
		return tk.ID
	})

	res := h.doGuest(t, http.MethodPost, "/api/v1/guest/follow-up", token, map[string]any{})
	body := readAll(t, res)
	require.Equal(t, http.StatusCreated, res.StatusCode, string(body))
	var out map[string]string
	require.NoError(t, json.Unmarshal(body, &out))
	var newID uuid.UUID
	require.NoError(t, h.tx.QueryRowContext(context.Background(), `SELECT id FROM tickets WHERE tracking_number = $1`, out["tracking_number"]).Scan(&newID))
	require.Equal(t, map[string]string{f.shown.ID.String(): "SN-1"}, h.storedValues(t, newID.String()))
}

func TestFollowUp_MCPCarriesEveryValue(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	s := mcpFieldSession(t, h)
	f := newFollowUpValues(t, h, func() uuid.UUID { return reporterClosedTicket(t, h).ID })

	text, isErr := s.tool("create_follow_up", map[string]any{"ticket_id": f.orig.String()})
	require.False(t, isErr, text)
	var out struct{ ID string }
	require.NoError(t, json.Unmarshal([]byte(text), &out))
	require.Equal(t, map[string]string{f.shown.ID.String(): "SN-1", f.hidden.ID.String(): "P1-secret"}, h.storedValues(t, out.ID))
}

// Values the field no longer accepts are left out, and the follow-up is filed
// with the rest.
func TestFollowUp_AValueTheFieldNoLongerAcceptsIsLeftOut(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newFollowUpValues(t, h, func() uuid.UUID { return reporterClosedTicket(t, h).ID })
	code, body := h.patchField(t, f.shown.ID, map[string]any{"max_length": 3}) // "SN-1" is four characters
	require.Equal(t, http.StatusOK, code, string(body))

	id := followUpID(t, h.do(t, http.MethodPost, "/api/v1/tickets/"+f.orig.String()+"/follow-up", nil))
	require.Equal(t, map[string]string{f.hidden.ID.String(): "P1-secret"}, h.storedValues(t, id))
}

// Not atomic: the follow-up is committed when the copy runs, so a copy that
// fails is logged and the follow-up stands.
func TestFollowUp_AFailedCopyIsLoggedAndTheFollowUpStands(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newFollowUpValues(t, h, func() uuid.UUID { return reporterClosedTicket(t, h).ID })
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(prev)
	h.failValueWrites(t)

	res := h.do(t, http.MethodPost, "/api/v1/tickets/"+f.orig.String()+"/follow-up", nil)
	body := readAll(t, res)
	require.Equal(t, http.StatusCreated, res.StatusCode, "the follow-up is filed: %s", body)
	require.Contains(t, logs.String(), "follow-up")
	require.Contains(t, logs.String(), "values")
}
