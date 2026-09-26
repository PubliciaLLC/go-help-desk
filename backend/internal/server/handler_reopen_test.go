package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/stretchr/testify/require"
)

// TestReopenTicket_New_ReturnsTicketNotClosed pins #277: reopening a ticket
// that was never resolved (still New) used to reach ticket.Service.Reopen's
// bare "ticket is not closed" error, which handleError could not recognize,
// and returned a 500. The domain check now wraps ticket.ErrNotClosed, mapped
// to 409 ticket_not_closed.
func TestReopenTicket_New_ReturnsTicketNotClosed(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	tkID := createTicketForSLA(t, h, "Fresh ticket")

	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets/"+tkID+"/reopen", nil)
	require.Equal(t, http.StatusConflict, resp.StatusCode)

	var errBody struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeJSON(t, resp, &errBody)
	require.Equal(t, "ticket_not_closed", errBody.Error.Code)
}

// TestReopenTicket_Resolved_ReturnsTicketNotClosed pins #277's central
// decision: Reopen accepts only a Closed ticket, deliberately, even though
// this button was shown on a Resolved ticket's header too (it 500'd there
// until #292 wrapped ticket.ErrNotClosed and mapped it to 409; the frontend
// stopped showing the button there in this same PR). A Resolved ticket is
// moved by the status selector (UpdateStatus) or by the reporter's own
// reply — not by this endpoint.
func TestReopenTicket_Resolved_ReturnsTicketNotClosed(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	tkID := createTicketForSLA(t, h, "Resolved ticket")
	resolveResp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets/"+tkID+"/resolve", map[string]any{})
	require.Equal(t, http.StatusOK, resolveResp.StatusCode)

	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets/"+tkID+"/reopen", nil)
	require.Equal(t, http.StatusConflict, resp.StatusCode)

	var errBody struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeJSON(t, resp, &errBody)
	require.Equal(t, "ticket_not_closed", errBody.Error.Code)
}

// TestReopenTicket_Closed_Returns200 is the positive control: a Closed
// ticket reopens successfully, landing on the configured (here, default New)
// target status.
func TestReopenTicket_Closed_Returns200(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	tkID := createTicketForSLA(t, h, "Closed ticket")
	resolveResp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets/"+tkID+"/resolve", map[string]any{})
	require.Equal(t, http.StatusOK, resolveResp.StatusCode)
	closeResp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets/"+tkID+"/close", map[string]any{})
	require.Equal(t, http.StatusOK, closeResp.StatusCode)

	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets/"+tkID+"/reopen", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got struct {
		StatusID string `json:"status_id"`
	}
	decodeJSON(t, resp, &got)
	require.Equal(t, statusIDNamed(t, h, "New").String(), got.StatusID)
}

// TestReopenTicket_MisconfiguredTarget_FallsBackToNew pins the misconfigured-
// target half of #277: adminSvc.ReopenTargetStatusName returning a name that
// matches no status used to make handleReopenTicket pass uuid.Nil straight
// to Reopen, which refused it with ticket.ErrValidation -- so an
// administrator's typo in a settings field turned every manual reopen into
// a 500, since handleReopenTicket had its own inline ListStatuses loop
// instead of the shared fallback the reply paths already used. This fix is
// that shared helper; the ErrValidation -> 400 mapping itself came from the
// v1.3.0-beta merge, not from this branch. handleReopenTicket now goes
// through the same reopenTargetStatusID helper the reply paths use, which
// falls back to New.
func TestReopenTicket_MisconfiguredTarget_FallsBackToNew(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	require.NoError(t, h.adminSvc.SetString(context.Background(), admin.KeyReopenTargetStatusName, "No Such Status"))

	tkID := createTicketForSLA(t, h, "Closed ticket, bad reopen target")
	resolveResp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets/"+tkID+"/resolve", map[string]any{})
	require.Equal(t, http.StatusOK, resolveResp.StatusCode)
	closeResp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets/"+tkID+"/close", map[string]any{})
	require.Equal(t, http.StatusOK, closeResp.StatusCode)

	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets/"+tkID+"/reopen", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got struct {
		StatusID string `json:"status_id"`
	}
	decodeJSON(t, resp, &got)
	require.Equal(t, statusIDNamed(t, h, "New").String(), got.StatusID)
}
