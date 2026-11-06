package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// doRawBody sends the body exactly as given, so a test can send what the
// encoding helpers never produce: nothing at all, or text that is not JSON.
func (h *harness) doRawBody(t *testing.T, method, path, apiKey, body string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if apiKey != "" {
		req.Header.Set("Authorization", "ApiKey "+apiKey)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	h.srv.ServeHTTP(rr, req)
	return rr.Result()
}

// POST /tickets/{id}/resolve: a body that is not JSON used to be ignored and
// the ticket resolved with no notes, so a client that mangled its notes lost
// them without being told. An empty body stays valid (#421).
func TestResolve_Body(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{"empty body resolves with no notes", "", http.StatusOK},
		{"empty object resolves", `{}`, http.StatusOK},
		{"notes resolve", `{"notes":"done"}`, http.StatusOK},
		{"malformed JSON is refused", `{"notes":`, http.StatusBadRequest},
		{"not JSON is refused", `garbage`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			id := createTicketIDAsAdmin(t, h)

			resp := h.doRawBody(t, http.MethodPost, "/api/v1/tickets/"+id+"/resolve", h.apiKey, tc.body)
			resp.Body.Close()
			require.Equal(t, tc.wantStatus, resp.StatusCode)

			// A refusal must leave the ticket alone.
			var got struct {
				StatusID string `json:"status_id"`
			}
			get := h.do(t, http.MethodGet, "/api/v1/tickets/"+id, nil)
			decodeJSON(t, get, &got)
			resolved := got.StatusID == statusIDNamed(t, h, "Resolved").String()
			require.Equal(t, tc.wantStatus == http.StatusOK, resolved)
		})
	}
}

func createTicketIDAsAdmin(t *testing.T, h *harness) string {
	t.Helper()
	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "s", "description": "d", "category_id": h.catID.String(),
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created struct {
		ID string `json:"id"`
	}
	decodeJSON(t, resp, &created)
	return created.ID
}

// POST /guest/resend: a client that has nothing to say sends no body. That is
// the same as {}, and still 202 like every other answer here (#421). Text that
// is not JSON stays 400.
func TestGuestResend_Body(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{"empty body is accepted", "", http.StatusAccepted},
		{"empty object is accepted", `{}`, http.StatusAccepted},
		{"garbage is refused", `garbage`, http.StatusBadRequest},
		{"truncated JSON is refused", `{"email":`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeyGuestSubmissionEnabled, true))

			resp := h.doRawBody(t, http.MethodPost, "/api/v1/guest/resend", "", tc.body)
			resp.Body.Close()
			require.Equal(t, tc.wantStatus, resp.StatusCode)
		})
	}
}
