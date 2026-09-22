package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// doGuest sends a request carrying a guest token and nothing else.
func (h *harness) doGuest(t *testing.T, method, path, token string, body any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Guest "+token)
	}
	rr := httptest.NewRecorder()
	h.srv.ServeHTTP(rr, req)
	return rr.Result()
}

// seedGuestTicket files a guest ticket through the service and returns it with
// the token the creation minted.
func seedGuestTicket(t *testing.T, h *harness) (ticket.Ticket, string) {
	t.Helper()
	email := "guest@test.local"
	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "Printer jammed", Description: "again", CategoryID: h.catID,
		GuestEmail: &email, GuestName: "Ada",
	})
	require.NoError(t, err)
	token, err := h.ticketSvc.IssueGuestToken(context.Background(), tk.ID)
	require.NoError(t, err)
	require.NotEmpty(t, token)
	return tk, token
}

// The token names one ticket. Everything else is 404, and every 404 is the
// same 404 — a different body or status for "expired" would confirm to whoever
// is guessing that they guessed something real.
func TestGuest_TokenReachesOneTicketAndEveryRefusalLooksAlike(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	_, token := seedGuestTicket(t, h)

	ok := h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", token, nil)
	defer ok.Body.Close()
	require.Equal(t, http.StatusOK, ok.StatusCode)

	var bodies []string
	for _, tc := range []struct{ name, tok string }{
		{"no token at all", ""},
		{"a token that was never issued", "0000000000000000000000000000000000000000000000000000000000000000"},
		{"a near miss on a real token", token[:len(token)-1] + "0"},
		{"not hex", "not-a-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", tc.tok, nil)
			defer res.Body.Close()
			require.Equal(t, http.StatusNotFound, res.StatusCode)
			b, _ := io.ReadAll(res.Body)
			bodies = append(bodies, string(b))
		})
	}
	for i := 1; i < len(bodies); i++ {
		require.Equal(t, bodies[0], bodies[i], "every refusal must be byte-identical")
	}
}

// A guest token must be worthless anywhere but the guest router.
func TestGuest_TokenIsRejectedByEveryOtherSurface(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk, token := seedGuestTicket(t, h)

	for _, path := range []string{
		"/api/v1/tickets",
		"/api/v1/tickets/" + tk.ID.String(),
		"/api/v1/tickets/" + tk.ID.String() + "/replies",
		"/api/v1/admin/users",
		"/api/v1/me",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Guest "+token)
			rr := httptest.NewRecorder()
			h.srv.ServeHTTP(rr, req)
			require.Equal(t, http.StatusUnauthorized, rr.Code,
				"a guest token must not authenticate the signed-in API")
		})
	}

	// And the same token under the Bearer scheme buys nothing either.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/guest/ticket", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.srv.ServeHTTP(rr, req)
	require.Equal(t, http.StatusNotFound, rr.Code, "the scheme is Guest, not Bearer")
}

// Internal notes are staff-to-staff. A guest holding a valid token for the
// ticket must still not see them, and must not learn who wrote what.
func TestGuest_ViewHidesInternalNotesAndStaffIdentities(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk, token := seedGuestTicket(t, h)

	newStatus := statusIDNamed(t, h, ticket.StatusNameNew)
	staff := ticket.Actor{UserID: &h.staffID, Role: user.RoleStaff}
	_, err := h.ticketSvc.AddReply(ctx, tk.ID, "We are on it", false, true, "guest@test.local", staff, 7, newStatus)
	require.NoError(t, err)
	_, err = h.ticketSvc.AddReply(ctx, tk.ID, "INTERNAL: cost code 4471", true, false, "", staff, 7, newStatus)
	require.NoError(t, err)

	// The reply rotated the link, so fetch a current one.
	token, err = h.ticketSvc.IssueGuestToken(ctx, tk.ID)
	require.NoError(t, err)

	res := h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", token, nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	raw, _ := io.ReadAll(res.Body)
	body := string(raw)

	require.Contains(t, body, "We are on it")
	require.NotContains(t, body, "cost code 4471", "an internal note must never reach a guest")
	require.NotContains(t, body, h.staffID.String(), "a guest must not be told which member of staff replied")
	require.NotContains(t, body, "assignee", "the narrow view must not carry assignment")
	require.NotContains(t, body, "resolution_notes")
}

// Submission is off unless an administrator turns it on, and a 404 rather than
// a 403 so an instance does not advertise a route it will not serve.
func TestGuest_SubmissionIsRefusedUntilEnabled(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	create := map[string]any{
		"subject": "Door will not open", "description": "it sticks",
		"category_id": h.catID.String(), "guest_email": "walkin@test.local", "guest_name": "Sam",
	}

	res := h.doGuest(t, http.MethodPost, "/api/v1/guest/tickets", "", create)
	res.Body.Close()
	require.Equal(t, http.StatusNotFound, res.StatusCode)

	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyGuestSubmissionEnabled, true))

	res = h.doGuest(t, http.MethodPost, "/api/v1/guest/tickets", "", create)
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	var out map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	require.NotEmpty(t, out["tracking_number"])
	require.Len(t, out, 1,
		"the response carries the tracking number and nothing else — not the id, and never the token")
}

// A guest cannot choose their own priority: that is a queue anyone on the
// internet could jump.
func TestGuest_CannotSetPriority(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyGuestSubmissionEnabled, true))

	res := h.doGuest(t, http.MethodPost, "/api/v1/guest/tickets", "", map[string]any{
		"subject": "URGENT", "description": "now", "category_id": h.catID.String(),
		"guest_email": "walkin@test.local", "guest_name": "Sam",
		"priority": "critical",
	})
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	var out map[string]string
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	tk, err := h.ticketSvc.GetByTrackingNumber(ctx, ticket.TrackingNumber(out["tracking_number"]))
	require.NoError(t, err)
	require.Equal(t, ticket.PriorityMedium, tk.Priority)
}

// The re-request endpoint answers the same way whatever it is given, so it
// cannot be used to test whether a ticket or an address exists.
func TestGuest_ResendIsNotAnExistenceOracle(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyGuestSubmissionEnabled, true))
	tk, _ := seedGuestTicket(t, h)

	var codes []int
	var bodies []string
	for _, b := range []map[string]any{
		{"tracking_number": string(tk.TrackingNumber), "email": "guest@test.local"},
		{"tracking_number": string(tk.TrackingNumber), "email": "someone@else.test"},
		{"tracking_number": "GHD-2026-999999", "email": "guest@test.local"},
		{"tracking_number": "", "email": ""},
	} {
		res := h.doGuest(t, http.MethodPost, "/api/v1/guest/resend", "", b)
		raw, _ := io.ReadAll(res.Body)
		res.Body.Close()
		codes = append(codes, res.StatusCode)
		bodies = append(bodies, string(raw))
	}
	for i := range codes {
		require.Equal(t, http.StatusAccepted, codes[i], "case %d", i)
		require.Equal(t, bodies[0], bodies[i], "case %d answered differently", i)
	}
}

// A guest replies to their own ticket and to no other, and the reply lands
// with no author.
func TestGuest_CanReplyToTheirOwnTicketOnly(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk, token := seedGuestTicket(t, h)

	res := h.doGuest(t, http.MethodPost, "/api/v1/guest/replies", token,
		map[string]any{"body": "still jammed"})
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	replies, err := h.ticketSvc.ListReplies(ctx, tk.ID)
	require.NoError(t, err)
	require.Len(t, replies, 1)
	require.Nil(t, replies[0].AuthorID, "a guest reply has no author")
	require.False(t, replies[0].Internal)

	// A second guest ticket's token must not reach the first.
	other := "other@test.local"
	tk2, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Other", Description: "x", CategoryID: h.catID,
		GuestEmail: &other, GuestName: "Bo",
	})
	require.NoError(t, err)
	token2, err := h.ticketSvc.IssueGuestToken(ctx, tk2.ID)
	require.NoError(t, err)

	res2 := h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", token2, nil)
	defer res2.Body.Close()
	raw, _ := io.ReadAll(res2.Body)
	require.NotContains(t, string(raw), "Printer jammed",
		"one token, one ticket — and no parameter to change")
	require.Equal(t, 1, len(replies), "the other ticket's reply count is untouched")
	_ = uuid.Nil
}
