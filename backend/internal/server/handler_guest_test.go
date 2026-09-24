package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
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

// nearMiss returns a token differing from the real one in its last character.
//
// Appending a fixed digit does not: the token is hex, so one time in sixteen
// the "near miss" IS the real token and the case asserts a 404 against a
// request that correctly answers 200. It passed locally and failed in CI, which
// is the only reason it was caught rather than shipped.
func nearMiss(token string) string {
	last := token[len(token)-1]
	replacement := "0"
	if last == '0' {
		replacement = "1"
	}
	return token[:len(token)-1] + replacement
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
		{"a near miss on a real token", nearMiss(token)},
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
}

// Expiry and closure are decided inside the query, not in Go. This exercises
// that against the real database: the domain fake asserts the same rules, and
// a fake that were more permissive than Postgres would make every other guest
// test optimistic.
func TestGuest_ClosingTheTicketStopsTheLinkAtTheQuery(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk, token := seedGuestTicket(t, h)

	res := h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", token, nil)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode, "precondition: the link works")

	staff := ticket.Actor{UserID: &h.staffID, Role: user.RoleStaff}
	require.NoError(t, h.ticketSvc.Close(ctx, tk.ID, staff))

	res = h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", token, nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusNotFound, res.StatusCode,
		"closing revokes, and the query refuses even if a row somehow survived")

	// And replying is refused too, not merely reading.
	reply := h.doGuest(t, http.MethodPost, "/api/v1/guest/replies", token,
		map[string]any{"body": "hello?"})
	defer reply.Body.Close()
	require.Equal(t, http.StatusNotFound, reply.StatusCode)
}

// The closure clause in GetTicketByGuestToken, exercised where only it can
// answer.
//
// The earlier version of this closed the ticket, which deletes the row — so it
// passed with the clause removed, and was decorative for the thing it claimed
// to test. Here the row is left in place and the ticket closed underneath it,
// which is the state the clause exists for: a token that outlived its DELETE,
// or a close that revoked nothing because the delete failed.
func TestGuest_AQueryRefusesATokenOnAClosedTicketEvenIfTheRowSurvives(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk, token := seedGuestTicket(t, h)

	res := h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", token, nil)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode, "precondition")

	// Close the ticket without going through Close(), so the token row stays.
	full, err := h.ticketSvc.GetByID(ctx, tk.ID)
	require.NoError(t, err)
	now := time.Now()
	full.ClosedAt = &now
	full.StatusID = statusIDNamed(t, h, ticket.StatusNameClosed)
	require.NoError(t, h.ticketStore.Update(ctx, full))

	res = h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", token, nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusNotFound, res.StatusCode,
		"the query must refuse a live row against a closed ticket")
}

// The same clause on the re-request lookup, for the same reason: a resend must
// not resurrect access to a closed ticket.
func TestGuest_ResendFindsNothingForAClosedTicket(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyGuestSubmissionEnabled, true))
	tk, _ := seedGuestTicket(t, h)

	_, err := h.ticketSvc.GuestTicketIDFor(ctx, tk.TrackingNumber, "guest@test.local")
	require.NoError(t, err, "precondition: it resolves while open")

	full, err := h.ticketSvc.GetByID(ctx, tk.ID)
	require.NoError(t, err)
	now := time.Now()
	full.ClosedAt = &now
	require.NoError(t, h.ticketStore.Update(ctx, full))

	_, err = h.ticketSvc.GuestTicketIDFor(ctx, tk.TrackingNumber, "guest@test.local")
	require.ErrorIs(t, err, ticket.ErrGuestTokenNotFound,
		"a closed ticket must not be reachable by re-request")
}

// from_you is how a guest tells their own words from support's. Asserting only
// that their own reply is flagged passes when the field is hardcoded true.
func TestGuest_FromYouDistinguishesTheTwoSides(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk, token := seedGuestTicket(t, h)

	staff := ticket.Actor{UserID: &h.staffID, Role: user.RoleStaff}
	_, err := h.ticketSvc.AddReply(ctx, tk.ID, "We are on it", false, true,
		"guest@test.local", staff, 7, statusIDNamed(t, h, ticket.StatusNameNew))
	require.NoError(t, err)
	_, err = h.ticketSvc.AddGuestReply(ctx, tk.ID, "thank you", 7, statusIDNamed(t, h, ticket.StatusNameNew))
	require.NoError(t, err)

	// The staff reply rotated the link, so use a current one.
	token, err = h.ticketSvc.IssueGuestToken(ctx, tk.ID)
	require.NoError(t, err)

	res := h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", token, nil)
	defer res.Body.Close()
	var out struct {
		Replies []struct {
			Body    string `json:"body"`
			FromYou bool   `json:"from_you"`
		} `json:"replies"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	require.Len(t, out.Replies, 2)

	byBody := map[string]bool{}
	for _, r := range out.Replies {
		byBody[r.Body] = r.FromYou
	}
	require.False(t, byBody["We are on it"], "support's reply is not the guest's")
	require.True(t, byBody["thank you"], "the guest's own reply is")
}

// Closing by status change is pinned in the domain suite
// (TestGuestToken_StatusChangeToClosedMintsNothing), where the dispatched
// event is visible. A server-level version lived here and was decorative: both
// its assertions hold whether the branch revokes or rotates, because
// closed_at gates the query either way.

// The per-ticket resend budget lives in the handler, and the budget is the
// RateLimiter's contract, so it is asserted where it can actually be observed:
// the limiter directly, and the endpoint answering identically either way.
//
// Not by counting rows. The harness runs inside an uncommitted transaction, so
// a query outside it sees nothing — a test that "passed" by finding zero rows
// would be measuring the harness, not the budget.
func TestGuest_ResendBudgetIsOnePerTicketPerFiveMinutes(t *testing.T) {
	limiter := authmw.NewRateLimiter(1, 5*time.Minute)
	id := uuid.New().String()

	require.True(t, limiter.Allow(id), "the customer who lost their link gets one")
	require.False(t, limiter.Allow(id), "a second inside the window must not rotate")
	require.True(t, limiter.Allow(uuid.New().String()),
		"the budget is per ticket, so another ticket is unaffected")
}

// However often it is called, the answer does not change. That is what stops
// the endpoint being an oracle, and it has to hold while the budget is
// refusing as well as while it is allowing.
func TestGuest_ResendAnswersIdenticallyWhenTheBudgetIsExhausted(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyGuestSubmissionEnabled, true))
	tk, _ := seedGuestTicket(t, h)

	body := map[string]any{
		"tracking_number": string(tk.TrackingNumber),
		"email":           "guest@test.local",
	}

	var codes []int
	var bodies []string
	for i := 0; i < 4; i++ {
		res := h.doGuest(t, http.MethodPost, "/api/v1/guest/resend", "", body)
		raw, _ := io.ReadAll(res.Body)
		res.Body.Close()
		codes = append(codes, res.StatusCode)
		bodies = append(bodies, string(raw))
	}
	for i := range codes {
		require.Equal(t, http.StatusAccepted, codes[i], "call %d", i)
		require.Equal(t, bodies[0], bodies[i], "call %d answered differently", i)
	}
}

// The category a guest names is checked against the active list before
// anything is written. Without it an unknown id reached a foreign key after
// NextSeq had already taken a tracking number.
func TestGuest_CategoryMustBeOneOpenToGuests(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyGuestSubmissionEnabled, true))

	for _, tc := range []struct{ name, categoryID string }{
		{"an id that is not a category", uuid.New().String()},
		{"the nil uuid", uuid.Nil.String()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := h.doGuest(t, http.MethodPost, "/api/v1/guest/tickets", "", map[string]any{
				"subject": "Door", "description": "sticks", "category_id": tc.categoryID,
				"guest_email": "walkin@test.local", "guest_name": "Sam",
			})
			defer res.Body.Close()
			require.Equal(t, http.StatusBadRequest, res.StatusCode,
				"refused before the write, not by a foreign key after it")
		})
	}
}

// guest_name is required, which DESIGN.md said and the route did not enforce.
func TestGuest_NameIsRequired(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeyGuestSubmissionEnabled, true))

	res := h.doGuest(t, http.MethodPost, "/api/v1/guest/tickets", "", map[string]any{
		"subject": "Door", "description": "sticks", "category_id": h.catID.String(),
		"guest_email": "walkin@test.local",
	})
	defer res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
}

// expires_at > clock_timestamp() lives in the query, and nothing exercised it
// against Postgres — the domain fake decided expiry itself, which is only
// trustworthy if the database agrees.
//
// Inserted directly with a past expiry, because nothing in the application
// will mint one: the point is that a row which has aged out is refused by the
// query rather than by the code that wrote it.
func TestGuest_AnExpiredRowIsRefusedByTheQuery(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk, _ := seedGuestTicket(t, h)

	raw, hashed, err := auth.GenerateToken()
	require.NoError(t, err)
	// An hour, not a second. The row's expiry comes from this process's clock
	// and the query compares it against Postgres's, so a one-second margin is
	// a race between two machines' idea of now — it passes almost always and
	// fails in CI for reasons nobody can reproduce. An hour is as expired as a
	// second for what this test asks, and it is not a race.
	require.NoError(t, h.ticketStore.CreateGuestToken(ctx, uuid.New(), tk.ID, hashed,
		time.Now().Add(-time.Hour)))

	res := h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", raw, nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusNotFound, res.StatusCode,
		"an aged-out row must be as dead as one that was never written")

	// And a live row beside it still works, so the clause is discriminating
	// rather than refusing everything.
	live, liveHash, err := auth.GenerateToken()
	require.NoError(t, err)
	require.NoError(t, h.ticketStore.CreateGuestToken(ctx, uuid.New(), tk.ID, liveHash,
		time.Now().Add(time.Hour)))

	ok := h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", live, nil)
	defer ok.Body.Close()
	require.Equal(t, http.StatusOK, ok.StatusCode)
}

// reopenTargetStatusID returning its ListStatuses error rather than swallowing
// it into uuid.Nil is a real fix — the domain reads uuid.Nil as a misconfigured
// setting, so an outage surfaced as a 400 blaming an administrator's typo — but
// it is NOT pinned by a test here, and a test that closed the pool was
// decorative: with the database dead every other query fails too, so the
// handler answers 500 whether the error is returned or swallowed. Isolating it
// needs a store that fails only ListStatuses, which the harness cannot express.

// The budget has to be checked at the call site, not merely exist.
//
// Observing it needs a token whose fate is visible: mint one directly, then
// resend. A resend that runs rotates and kills it; a resend the budget refuses
// leaves it alone. Removing the check makes the second case fail.
func TestGuest_ResendBudgetIsActuallyConsulted(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyGuestSubmissionEnabled, true))
	tk, _ := seedGuestTicket(t, h)

	body := map[string]any{
		"tracking_number": string(tk.TrackingNumber),
		"email":           "guest@test.local",
	}

	// First resend: inside the budget, so it rotates and the token we hold dies.
	mine, err := h.ticketSvc.IssueGuestToken(ctx, tk.ID)
	require.NoError(t, err)
	res := h.doGuest(t, http.MethodPost, "/api/v1/guest/resend", "", body)
	res.Body.Close()
	require.Equal(t, http.StatusAccepted, res.StatusCode)

	gone := h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", mine, nil)
	gone.Body.Close()
	require.Equal(t, http.StatusNotFound, gone.StatusCode,
		"precondition: a resend inside the budget does rotate")

	// Second resend: the budget refuses, so this one must leave the link alone.
	mine, err = h.ticketSvc.IssueGuestToken(ctx, tk.ID)
	require.NoError(t, err)
	res = h.doGuest(t, http.MethodPost, "/api/v1/guest/resend", "", body)
	res.Body.Close()
	require.Equal(t, http.StatusAccepted, res.StatusCode, "the answer never changes")

	still := h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", mine, nil)
	defer still.Body.Close()
	require.Equal(t, http.StatusOK, still.StatusCode,
		"a resend the budget refused must not have rotated anything")
}

// The middleware's 404 and a handler's 404 have to be the same bytes, or the
// difference is itself the signal the identical body exists to remove.
func TestGuest_TheTwo404sAreByteIdentical(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	fromMiddleware := h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket",
		"0000000000000000000000000000000000000000000000000000000000000000", nil)
	mw, _ := io.ReadAll(fromMiddleware.Body)
	fromMiddleware.Body.Close()

	// The submission route answers 404 through Error() when the toggle is off.
	fromHandler := h.doGuest(t, http.MethodPost, "/api/v1/guest/tickets", "", map[string]any{})
	hd, _ := io.ReadAll(fromHandler.Body)
	fromHandler.Body.Close()

	require.Equal(t, http.StatusNotFound, fromMiddleware.StatusCode)
	require.Equal(t, http.StatusNotFound, fromHandler.StatusCode)
	require.Equal(t, string(hd), string(mw),
		"a refusal that differs by even a byte is a refusal that says which kind it was")
}

// Re-request is gated on the same toggle as submission: an instance that does
// not offer guest tickets does not offer a way to get back into one.
func TestGuest_ResendIsRefusedUntilEnabled(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := h.doGuest(t, http.MethodPost, "/api/v1/guest/resend", "",
		map[string]any{"tracking_number": "GHD-2026-000001", "email": "a@b.test"})
	defer res.Body.Close()
	require.Equal(t, http.StatusNotFound, res.StatusCode)
}

// The per-ticket budget is checked after the lookup, so a miss spends nothing.
// Reversing that would let someone burn a stranger's budget by guessing, and
// would make the budget itself tell them which tickets exist.
func TestGuest_AMissDoesNotSpendTheTicketsBudget(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyGuestSubmissionEnabled, true))
	tk, _ := seedGuestTicket(t, h)

	// Three requests that match nothing.
	for _, body := range []map[string]any{
		{"tracking_number": string(tk.TrackingNumber), "email": "wrong@test.local"},
		{"tracking_number": "GHD-2026-999999", "email": "guest@test.local"},
		{"tracking_number": string(tk.TrackingNumber), "email": "also-wrong@test.local"},
	} {
		res := h.doGuest(t, http.MethodPost, "/api/v1/guest/resend", "", body)
		res.Body.Close()
		require.Equal(t, http.StatusAccepted, res.StatusCode)
	}

	// The real customer's one call must still rotate.
	mine, err := h.ticketSvc.IssueGuestToken(ctx, tk.ID)
	require.NoError(t, err)
	res := h.doGuest(t, http.MethodPost, "/api/v1/guest/resend", "",
		map[string]any{"tracking_number": string(tk.TrackingNumber), "email": "guest@test.local"})
	res.Body.Close()

	gone := h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", mine, nil)
	defer gone.Body.Close()
	require.Equal(t, http.StatusNotFound, gone.StatusCode,
		"misses must not have spent the budget the customer needs")
}
