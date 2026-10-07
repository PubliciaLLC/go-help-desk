package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// #349: the REQUESTER may open a follow-up of their own closed ticket too.
//
//   - an account holder: POST /tickets/{id}/follow-up on a ticket they reported
//     (an API key acting as one takes the same route);
//   - a guest: POST /guest/follow-up with the ticket's link — a deliberate
//     exception to "every guest write route refuses a closed ticket", which
//     resolves the link for a READ and then requires Closed, and otherwise
//     refuses with the generic 404.
//
// The principle: it must not produce a ticket the requester could not have
// created directly, so it copies only what a requester may set, and is held to
// the creation limits a guest ticket already has.

// raisedClosedTicket is the reporter's ticket as staff left it: priority
// raised, filed under a type and item, assigned, with a reply, then closed.
func raisedClosedTicket(t *testing.T, h *harness) ticket.Ticket {
	t.Helper()
	ctx := context.Background()
	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Laptop will not boot", Description: "black screen", CategoryID: h.catID,
		Priority: ticket.PriorityCritical, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	_, err = h.ticketSvc.Assign(ctx, tk.ID, &h.staffID, nil, ticket.Actor{UserID: &h.adminID, Role: user.RoleAdmin})
	require.NoError(t, err)
	_, err = h.ticketSvc.AddReply(ctx, tk.ID, "replaced the board", false, false, "",
		ticket.Actor{UserID: &h.staffID, Role: user.RoleStaff}, 7, statusIDNamed(t, h, ticket.StatusNameNew))
	require.NoError(t, err)
	require.NoError(t, h.ticketSvc.Close(ctx, tk.ID, ticket.Actor{UserID: &h.adminID, Role: user.RoleAdmin}))
	return tk
}

func decodeTicket(t *testing.T, res *http.Response) ticket.Ticket {
	t.Helper()
	body := readBody(t, res)
	require.Equal(t, http.StatusCreated, res.StatusCode, body)
	var got ticket.Ticket
	require.NoError(t, json.Unmarshal([]byte(body), &got))
	return got
}

// ── account holders ─────────────────────────────────────────────────────────

func TestRequesterFollowUp_AnAccountHolderOpensOneFromTheirClosedTicket(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	orig := raisedClosedTicket(t, h)
	before, err := h.ticketSvc.GetByID(ctx, orig.ID)
	require.NoError(t, err)
	repliesBefore, _ := h.ticketSvc.ListReplies(ctx, orig.ID)

	got := decodeTicket(t, h.doAsUser(t, http.MethodPost, "/api/v1/tickets/"+orig.ID.String()+"/follow-up", nil))

	require.NotEqual(t, orig.ID, got.ID)
	require.Equal(t, orig.Subject, got.Subject)
	require.Equal(t, orig.Description, got.Description)
	require.Equal(t, &h.userID, got.ReporterUserID, "owned by them: a normal requester-created ticket")
	require.Equal(t, ticket.PriorityMedium, got.Priority,
		"the critical priority staff gave the original is not theirs to carry over")
	require.Equal(t, "New", statusOf(t, h, got.ID))

	// Linked, parent = the closed ticket.
	links, err := h.ticketSvc.ListLinks(ctx, orig.ID)
	require.NoError(t, err)
	require.Equal(t, []ticket.TicketLink{{
		SourceTicketID: orig.ID, TargetTicketID: got.ID, LinkType: ticket.LinkParentChild,
	}}, links)

	// The original is untouched.
	after, err := h.ticketSvc.GetByID(ctx, orig.ID)
	require.NoError(t, err)
	require.Equal(t, before, after)
	repliesAfter, _ := h.ticketSvc.ListReplies(ctx, orig.ID)
	require.Equal(t, len(repliesBefore), len(repliesAfter))

	// And they can open their new ticket.
	res := h.doAsUser(t, http.MethodGet, "/api/v1/tickets/"+got.ID.String(), nil)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func TestRequesterFollowUp_AnAccountHolderIsHeldToTheCreationRules(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	orig := raisedClosedTicket(t, h)

	// Anything in the body is ignored: there is no field a requester can set.
	res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets/"+orig.ID.String()+"/follow-up",
		map[string]any{"priority": "critical", "assignee_user_id": h.staffID.String(), "subject": "hijacked"})
	got := decodeTicket(t, res)
	require.Equal(t, ticket.PriorityMedium, got.Priority)
	require.Nil(t, got.AssigneeUserID)
	require.Equal(t, orig.Subject, got.Subject)
	_ = ctx

	// An archived category is closed to them, as for a direct create.
	t.Run("an archived category", func(t *testing.T) {
		cat, err := h.categorySvc.CreateCategory(ctx, "Retired later", 98)
		require.NoError(t, err)
		other, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
			Subject: "Filed under it", Description: "x", CategoryID: cat.ID, ReporterUserID: &h.userID,
		})
		require.NoError(t, err)
		require.NoError(t, h.ticketSvc.Close(ctx, other.ID, ticket.Actor{UserID: &h.adminID, Role: user.RoleAdmin}))
		cat.Active = false
		require.NoError(t, h.categorySvc.UpdateCategory(ctx, cat))

		res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets/"+other.ID.String()+"/follow-up", nil)
		body := readBody(t, res)
		require.Equal(t, http.StatusBadRequest, res.StatusCode, body)
		links, err := h.ticketSvc.ListLinks(ctx, other.ID)
		require.NoError(t, err)
		require.Empty(t, links, "nothing was created")
	})
}

func TestRequesterFollowUp_AnAccountHolderRefusals(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	t.Run("someone else's closed ticket is not found", func(t *testing.T) {
		foreign, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
			Subject: "Not theirs", Description: "x", CategoryID: h.catID, ReporterUserID: &h.adminID,
		})
		require.NoError(t, err)
		require.NoError(t, h.ticketSvc.Close(ctx, foreign.ID, ticket.Actor{UserID: &h.adminID, Role: user.RoleAdmin}))
		res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets/"+foreign.ID.String()+"/follow-up", nil)
		res.Body.Close()
		require.Equal(t, http.StatusNotFound, res.StatusCode, "not-found first: no oracle for tickets they cannot see")
		links, _ := h.ticketSvc.ListLinks(ctx, foreign.ID)
		require.Empty(t, links)
	})

	for name, setup := range map[string]func(id uuid.UUID){
		"an open ticket": func(uuid.UUID) {},
		"a resolved ticket": func(id uuid.UUID) {
			_, err := h.ticketSvc.Resolve(ctx, id, "done", ticket.Actor{UserID: &h.staffID, Role: user.RoleStaff})
			require.NoError(t, err)
		},
	} {
		t.Run(name+" is refused", func(t *testing.T) {
			tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
				Subject: "Still going", Description: "x", CategoryID: h.catID, ReporterUserID: &h.userID,
			})
			require.NoError(t, err)
			setup(tk.ID)
			res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/follow-up", nil)
			body := readBody(t, res)
			require.Equal(t, http.StatusConflict, res.StatusCode, body)
			require.Contains(t, body, "ticket_not_closed")
		})
	}

	t.Run("only one", func(t *testing.T) {
		tk := reporterClosedTicket(t, h)
		decodeTicket(t, h.doAsUser(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/follow-up", nil))
		res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/follow-up", nil)
		body := readBody(t, res)
		require.Equal(t, http.StatusConflict, res.StatusCode, body)
		require.Contains(t, body, "follow_up_exists")
		links, _ := h.ticketSvc.ListLinks(ctx, tk.ID)
		require.Len(t, links, 1)
	})

	t.Run("one that staff opened counts, and staff are not limited", func(t *testing.T) {
		tk := reporterClosedTicket(t, h)
		for i := 0; i < 2; i++ {
			decodeTicket(t, h.do(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/follow-up", nil))
		}
		res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/follow-up", nil)
		body := readBody(t, res)
		require.Equal(t, http.StatusConflict, res.StatusCode, body)
		require.Contains(t, body, "follow_up_exists")
	})
}

// Staff keep the broader copy set: what staff raised stays raised.
func TestRequesterFollowUp_StaffBehaviourIsUnchanged(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	orig := raisedClosedTicket(t, h)
	got := decodeTicket(t, h.do(t, http.MethodPost, "/api/v1/tickets/"+orig.ID.String()+"/follow-up", nil))
	require.Equal(t, ticket.PriorityCritical, got.Priority, "staff copy the priority")
}

// ── guests ──────────────────────────────────────────────────────────────────

// seedGuestTicketFor is seedGuestTicket for another guest address.
func seedGuestTicketFor(t *testing.T, h *harness, email string) (ticket.Ticket, string) {
	t.Helper()
	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "Printer jammed", Description: "again", CategoryID: h.catID,
		GuestEmail: &email, GuestName: "Ada",
	})
	require.NoError(t, err)
	token, err := h.ticketSvc.IssueGuestToken(context.Background(), tk.ID)
	require.NoError(t, err)
	return tk, token
}

func closeGuestTicket(t *testing.T, h *harness, tk ticket.Ticket) {
	t.Helper()
	require.NoError(t, h.ticketSvc.Close(context.Background(), tk.ID,
		ticket.Actor{UserID: &h.adminID, Role: user.RoleAdmin}))
}

func enableGuests(t *testing.T, h *harness) {
	t.Helper()
	require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeyGuestSubmissionEnabled, true))
}

func TestGuestFollowUp_ACloseTicketsLinkOpensANewGuestTicket(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	enableGuests(t, h)
	orig, held := seedGuestTicket(t, h)
	closeGuestTicket(t, h, orig)
	origBefore, _ := h.ticketSvc.GetByID(ctx, orig.ID)
	h.dispatcher.record = true // queue only: what the outbox would hold

	res := h.doGuest(t, http.MethodPost, "/api/v1/guest/follow-up", held,
		map[string]any{"priority": "critical", "category_id": uuid.NewString(), "guest_email": "attacker@evil.test"})
	body := readBody(t, res)

	require.Equal(t, http.StatusCreated, res.StatusCode, body)
	// The tracking number of their new ticket, as a normal guest submission
	// answers, and nothing else: in particular no link.
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &out))
	require.Len(t, out, 1, body)
	tn, _ := out["tracking_number"].(string)
	require.NotEmpty(t, tn)
	require.NotEqual(t, string(orig.TrackingNumber), tn)

	created, err := h.ticketSvc.GetByTrackingNumber(ctx, ticket.TrackingNumber(tn))
	require.NoError(t, err)
	require.Nil(t, created.ReporterUserID)
	require.Equal(t, "guest@test.local", *created.GuestEmail, "the same guest, whatever the body said")
	require.Equal(t, "Ada", created.GuestName)
	require.Equal(t, orig.CategoryID, created.CategoryID)
	require.Equal(t, ticket.PriorityMedium, created.Priority)
	require.Nil(t, created.TypeID)
	require.Nil(t, created.ItemID)
	require.Equal(t, "New", statusOf(t, h, created.ID))

	links, err := h.ticketSvc.ListLinks(ctx, orig.ID)
	require.NoError(t, err)
	require.Equal(t, []ticket.TicketLink{{
		SourceTicketID: orig.ID, TargetTicketID: created.ID, LinkType: ticket.LinkParentChild,
	}}, links)

	// Its link goes by the ordinary guest-ticket-created mail, to the guest's
	// address, and never in the response.
	var ev *notification.Event
	for i := range h.dispatcher.queued {
		if h.dispatcher.queued[i].Type == notification.EventTicketCreated {
			ev = &h.dispatcher.queued[i]
		}
	}
	require.NotNil(t, ev)
	require.Equal(t, created.ID, ev.TicketID)
	require.True(t, ev.GuestLink)
	sent, ok, err := h.srv.PrepareGuestLink(ctx, *ev)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotContains(t, body, sent.GuestToken)
	r := h.doGuest(t, http.MethodPost, "/api/v1/guest/replies", sent.GuestToken, map[string]any{"body": "thanks"})
	r.Body.Close()
	require.Equal(t, http.StatusCreated, r.StatusCode, "the follow-up is a live guest ticket")

	// The original is untouched, and the guest's link still reads it.
	origAfter, _ := h.ticketSvc.GetByID(ctx, orig.ID)
	require.Equal(t, origBefore, origAfter)
	r = h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", held, nil)
	r.Body.Close()
	require.Equal(t, http.StatusOK, r.StatusCode)
}

// Every refusal is the generic 404 of the other guest write routes, byte for
// byte: the route must not tell a visitor which links are real, or what state a
// ticket is in.
func TestGuestFollowUp_EveryRefusalIsTheGenericNotFound(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	enableGuests(t, h)

	openTk, openToken := seedGuestTicket(t, h)
	_ = openTk
	closedTk, closedToken := seedGuestTicketFor(t, h, "closed@test.local")
	closeGuestTicket(t, h, closedTk)
	secondTk, secondToken := seedGuestTicketFor(t, h, "second@test.local")
	closeGuestTicket(t, h, secondTk)
	res := h.doGuest(t, http.MethodPost, "/api/v1/guest/follow-up", secondToken, nil)
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode, "precondition: the first one is allowed")

	// The reference: what a guest write route says to a link that is no good.
	ref := h.doGuest(t, http.MethodPost, "/api/v1/guest/replies", nearMiss(openToken), map[string]any{"body": "x"})
	want := readBody(t, ref)
	require.Equal(t, http.StatusNotFound, ref.StatusCode)

	cases := map[string]string{
		"no token":              "",
		"a token never issued":  "0000000000000000000000000000000000000000000000000000000000000000",
		"a near miss":           nearMiss(closedToken),
		"an open ticket's link": openToken,
		"a second follow-up":    secondToken,
	}
	for name, tok := range cases {
		t.Run(name, func(t *testing.T) {
			res := h.doGuest(t, http.MethodPost, "/api/v1/guest/follow-up", tok, map[string]any{})
			require.Equal(t, http.StatusNotFound, res.StatusCode)
			require.Equal(t, "application/json", res.Header.Get("Content-Type"))
			require.Equal(t, want, readBody(t, res), "must be byte-identical to a bad link's refusal")
		})
	}
	links, _ := h.ticketSvc.ListLinks(ctx, openTk.ID)
	require.Empty(t, links, "nothing was created for an open ticket")

	t.Run("an account holder's ticket is not reachable by a guest link", func(t *testing.T) {
		// A guest token can only exist for a guest ticket; this is the service's
		// own answer if one were somehow presented.
		reporterTk := reporterClosedTicket(t, h)
		_, err := h.ticketSvc.CreateRequesterFollowUp(ctx, reporterTk.ID, "GHD", ticket.Actor{Role: user.RoleUser}, nil)
		require.ErrorIs(t, err, ticket.ErrForbidden)
	})
}

// With guest submission off, an instance does not take new guest tickets, and a
// follow-up is one: the same generic 404, before anything else is said.
func TestGuestFollowUp_NeedsGuestSubmissionOn(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk, token := seedGuestTicket(t, h)
	closeGuestTicket(t, h, tk)
	require.False(t, h.adminSvc.GuestSubmissionEnabled(context.Background()), "precondition")

	res := h.doGuest(t, http.MethodPost, "/api/v1/guest/follow-up", token, nil)
	body := readBody(t, res)
	require.Equal(t, http.StatusNotFound, res.StatusCode)
	require.Equal(t, `{"error":{"code":"not_found","message":"not found"}}`+"\n", body)
	links, _ := h.ticketSvc.ListLinks(context.Background(), tk.ID)
	require.Empty(t, links)
}

// The follow-up is a guest ticket creation, so it draws on the same per-address
// budget as POST /guest/tickets: it is not a way around it. And a visitor who
// has run it down learns nothing about links: bad links and open tickets still
// answer the generic 404, not a 429.
func TestGuestFollowUp_SharesTheGuestSubmissionBudget(t *testing.T) {
	h, cleanup := newHarnessWithRateLimit(t, 2)
	defer cleanup()
	ctx := context.Background()
	enableGuests(t, h)
	catID := h.catID.String()
	submit := func() int {
		res := h.doGuest(t, http.MethodPost, "/api/v1/guest/tickets", "", map[string]any{
			"subject": "x", "description": "y", "category_id": catID,
			"guest_email": "someone@test.local", "guest_name": "Some One",
		})
		res.Body.Close()
		return res.StatusCode
	}

	tk, token := seedGuestTicket(t, h)
	closeGuestTicket(t, h, tk)
	require.Equal(t, http.StatusCreated, submit(), "a direct submission spends the budget...")
	res := h.doGuest(t, http.MethodPost, "/api/v1/guest/follow-up", token, nil)
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode, "...the follow-up draws on it...")

	tk2, token2 := seedGuestTicketFor(t, h, "other@test.local")
	closeGuestTicket(t, h, tk2)
	res = h.doGuest(t, http.MethodPost, "/api/v1/guest/follow-up", token2, nil)
	require.Equal(t, http.StatusTooManyRequests, res.StatusCode, "...and is refused once it is spent")
	res.Body.Close()
	links, _ := h.ticketSvc.ListLinks(ctx, tk2.ID)
	require.Empty(t, links)
	require.Equal(t, http.StatusTooManyRequests, submit(), "the same bucket as a direct submission")

	// No oracle: with the budget spent, a bad link and an open ticket's link
	// still get the generic 404 rather than a 429.
	_, openToken := seedGuestTicketFor(t, h, "open@test.local")
	for name, tok := range map[string]string{"a bad link": nearMiss(token2), "an open ticket": openToken} {
		res := h.doGuest(t, http.MethodPost, "/api/v1/guest/follow-up", tok, nil)
		res.Body.Close()
		require.Equal(t, http.StatusNotFound, res.StatusCode, name)
	}
}

// A guest write that is not a follow-up is unchanged: replies on a closed
// ticket are still the generic 404 (the exception is this one route).
func TestGuestFollowUp_TheOtherWriteRoutesStillRefuseAClosedTicket(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	enableGuests(t, h)
	tk, token := seedGuestTicket(t, h)
	closeGuestTicket(t, h, tk)
	res := h.doGuest(t, http.MethodPost, "/api/v1/guest/follow-up", token, nil)
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	res = h.doGuest(t, http.MethodPost, "/api/v1/guest/replies", token, map[string]any{"body": "x"})
	res.Body.Close()
	require.Equal(t, http.StatusNotFound, res.StatusCode)
	res = uploadGuestAttachment(t, h, token, "p.txt", []byte("x"))
	res.Body.Close()
	require.Equal(t, http.StatusNotFound, res.StatusCode)
}

// The one refusal on the guest route that is not the generic 404 (documented,
// not an oversight): the category was archived after the ticket was filed, and a
// direct submission of it is a 400 too. The holder has a good link to a closed
// ticket and knows its category, so nothing is revealed.
func TestGuestFollowUp_AnArchivedCategoryIsA400WithTheReason(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	enableGuests(t, h)
	cat, err := h.categorySvc.CreateCategory(ctx, "Retired later", 97)
	require.NoError(t, err)
	email := "retired@test.local"
	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Filed under it", Description: "x", CategoryID: cat.ID,
		GuestEmail: &email, GuestName: "Ada",
	})
	require.NoError(t, err)
	token, err := h.ticketSvc.IssueGuestToken(ctx, tk.ID)
	require.NoError(t, err)
	closeGuestTicket(t, h, tk)
	cat.Active = false
	require.NoError(t, h.categorySvc.UpdateCategory(ctx, cat))

	res := h.doGuest(t, http.MethodPost, "/api/v1/guest/follow-up", token, nil)
	body := readBody(t, res)

	require.Equal(t, http.StatusBadRequest, res.StatusCode, body)
	require.Contains(t, body, "not an active category")
	links, _ := h.ticketSvc.ListLinks(ctx, tk.ID)
	require.Empty(t, links)
}
