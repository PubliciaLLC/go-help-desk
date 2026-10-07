package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// #349: Closed is archived read-only for every requester, and terminal for
// every role. This file is the contract over the real HTTP surface and the real
// database; the domain rules are pinned in internal/domain/ticket.

// reporterClosedTicket files a ticket for the seeded reporting user and closes
// it.
func reporterClosedTicket(t *testing.T, h *harness) ticket.Ticket {
	t.Helper()
	ctx := context.Background()
	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Laptop will not boot", Description: "black screen", CategoryID: h.catID,
		ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	require.NoError(t, h.ticketSvc.Close(ctx, tk.ID, ticket.Actor{UserID: &h.adminID, Role: user.RoleAdmin}))
	return tk
}

func readBody(t *testing.T, res *http.Response) string {
	t.Helper()
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return string(b)
}

func statusOf(t *testing.T, h *harness, id uuid.UUID) string {
	t.Helper()
	tk, err := h.ticketSvc.GetByID(context.Background(), id)
	require.NoError(t, err)
	sts, err := h.ticketSvc.ListStatuses(context.Background())
	require.NoError(t, err)
	for _, s := range sts {
		if s.ID == tk.StatusID {
			return s.Name
		}
	}
	t.Fatal("ticket has no status")
	return ""
}

// ── guests ──────────────────────────────────────────────────────────────────

// A closed ticket is readable by its guest link, and every guest write route
// refuses it with the byte-identical 404 a bad link gets: same status, same
// headers that matter, same body. A refusal that differs by a byte says which
// kind it was, and so that the ticket exists.
func TestGuest_EveryWriteRouteRefusesAClosedTicketWithTheGenericNotFound(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk, token := seedGuestTicket(t, h)
	require.NoError(t, h.ticketSvc.Close(ctx, tk.ID, ticket.Actor{UserID: &h.adminID, Role: user.RoleAdmin}))

	read := h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", token, nil)
	require.Equal(t, http.StatusOK, read.StatusCode, "a closed ticket is readable by its link")
	read.Body.Close()

	type refusal struct {
		code        int
		contentType string
		body        string
	}
	capture := func(res *http.Response) refusal {
		return refusal{res.StatusCode, res.Header.Get("Content-Type"), readBody(t, res)}
	}

	routes := map[string]func(token string) *http.Response{
		"POST /guest/replies": func(tok string) *http.Response {
			return h.doGuest(t, http.MethodPost, "/api/v1/guest/replies", tok, map[string]any{"body": "hello?"})
		},
		// An invalid request is refused for the ticket before it is refused for
		// its body: the handler would answer 400 "body is required" first, and
		// that 400 says the link resolved to a ticket. Only the middleware
		// group, ahead of the handler, makes this a 404.
		"POST /guest/replies (empty body)": func(tok string) *http.Response {
			return h.doGuest(t, http.MethodPost, "/api/v1/guest/replies", tok, map[string]any{"body": ""})
		},
		"POST /guest/attachments": func(tok string) *http.Response {
			return uploadGuestAttachment(t, h, tok, "photo.txt", []byte("x"))
		},
	}
	for name, call := range routes {
		t.Run(name, func(t *testing.T) {
			closed := capture(call(token))
			neverIssued := capture(call("0000000000000000000000000000000000000000000000000000000000000000"))
			require.Equal(t, http.StatusNotFound, closed.code)
			require.Equal(t, neverIssued, closed,
				"a closed ticket must be refused exactly as a link that never existed is")
		})
	}

	replies, err := h.ticketSvc.ListReplies(ctx, tk.ID)
	require.NoError(t, err)
	require.Empty(t, replies, "nothing was written")
	atts, err := h.ticketSvc.ListAttachments(ctx, tk.ID)
	require.NoError(t, err)
	require.Empty(t, atts)
	require.Equal(t, ticket.StatusNameClosed, statusOf(t, h, tk.ID), "and nothing reopened it")
}

// The service refuses a guest write to a closed ticket too, for the request
// that raced the close past the middleware (guestWriteError maps it to the
// same 404).
func TestGuest_AWriteRacingTheCloseIsStillRefusedByTheService(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk, _ := seedGuestTicket(t, h)
	require.NoError(t, h.ticketSvc.Close(context.Background(), tk.ID,
		ticket.Actor{UserID: &h.adminID, Role: user.RoleAdmin}))

	// Past the middleware, straight at the service: what the handler maps.
	_, err := h.ticketSvc.AddGuestReply(context.Background(), tk.ID, "late", 7, uuid.New())
	require.ErrorIs(t, err, ticket.ErrClosed, "the service still refuses, as defence in depth")
}

// Auto-close is the third door into Closed and keeps the link readable.
func TestGuest_AutoCloseKeepsTheLinkReadable(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk, _ := seedGuestTicket(t, h)
	staff := ticket.Actor{UserID: &h.staffID, Role: user.RoleStaff}
	_, err := h.ticketSvc.Resolve(ctx, tk.ID, "done", staff)
	require.NoError(t, err)
	// The resolution rotated the link; this is the one the guest was mailed.
	token, err := h.ticketSvc.IssueGuestToken(ctx, tk.ID)
	require.NoError(t, err)

	// 0 days: the next sweep closes it. The SetInt matches the setting the
	// scheduler reads in cmd/server.
	require.NoError(t, h.adminSvc.SetInt(ctx, admin.KeyReopenWindowDays, 0))
	n, err := h.ticketSvc.AutoClose(ctx, h.adminSvc.ReopenWindowDays(ctx), 10)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, ticket.StatusNameClosed, statusOf(t, h, tk.ID))

	res := h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", token, nil)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode, "auto-close must not revoke the link")
	res = h.doGuest(t, http.MethodPost, "/api/v1/guest/replies", token, map[string]any{"body": "x"})
	res.Body.Close()
	require.Equal(t, http.StatusNotFound, res.StatusCode)
}

// The headline of #349 through the real send-time step: staff reply, then the
// ticket is closed before the mail goes. The mail still goes, and its link
// opens the thread.
func TestGuest_ReplyThenCloseDeliversAWorkingLink(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk, _ := seedGuestTicket(t, h)
	h.dispatcher.record = true // queue only; the send comes after the close

	res := h.do(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/replies",
		map[string]any{"body": "Replaced the drum, you are good to go."})
	require.Equal(t, http.StatusCreated, res.StatusCode, readBody(t, res))
	res.Body.Close()
	res = h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/close", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, readBody(t, res))
	res.Body.Close()

	var reply *notification.Event
	for i := range h.dispatcher.queued {
		if h.dispatcher.queued[i].Type == notification.EventTicketReplied {
			reply = &h.dispatcher.queued[i]
		}
	}
	require.NotNil(t, reply, "the reply queued its mail")
	sent, ok, err := h.srv.PrepareGuestLink(ctx, *reply)
	require.NoError(t, err)
	require.True(t, ok, "#346 dropped this mail")

	got := h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", sent.GuestToken, nil)
	body := readBody(t, got)
	require.Equal(t, http.StatusOK, got.StatusCode)
	require.Contains(t, body, "Replaced the drum", "the link in the mail reads the answer")
}

// ── account holders ─────────────────────────────────────────────────────────

// Every write a reporting user (here an API key acting as one, which takes the
// same handlers as a session) has on their own closed ticket is refused, and
// the ticket is unchanged. One table, so a new write route that is not here is
// the thing to notice in review.
func TestReporter_EveryWriteIsRefusedOnTheirClosedTicket(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk := reporterClosedTicket(t, h)
	other, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Another of mine", Description: "x", CategoryID: h.catID, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	base := "/api/v1/tickets/" + tk.ID.String()

	cases := []struct {
		name   string
		method string
		path   string
		body   any
		want   int
	}{
		// The refusals a reporter is told about their own ticket: 409 and a
		// clear code. They can already read it, so this says nothing new.
		{"reply", http.MethodPost, base + "/replies", map[string]any{"body": "still broken"}, http.StatusConflict},
		{"link from the closed ticket", http.MethodPost, base + "/links",
			map[string]any{"target_id": other.ID, "link_type": "related_to"}, http.StatusConflict},
		{"link to the closed ticket", http.MethodPost, "/api/v1/tickets/" + other.ID.String() + "/links",
			map[string]any{"target_id": tk.ID, "link_type": "related_to"}, http.StatusConflict},
		{"custom fields", http.MethodPut, base + "/custom-fields", map[string]any{}, http.StatusConflict},
		// Refused for any state, by role, and still refused here.
		{"status change", http.MethodPatch, base, map[string]any{"status_id": statusIDNamed(t, h, ticket.StatusNameNew).String()}, http.StatusForbidden},
		{"reassign", http.MethodPatch, base, map[string]any{"assignee_user_id": h.staffID.String()}, http.StatusForbidden},
		{"reclassify", http.MethodPatch, base, map[string]any{"category_id": h.catID.String()}, http.StatusForbidden},
		{"resolve", http.MethodPost, base + "/resolve", map[string]any{}, http.StatusForbidden},
		{"close", http.MethodPost, base + "/close", map[string]any{}, http.StatusForbidden},
		{"follow-up", http.MethodPost, base + "/follow-up", map[string]any{}, http.StatusForbidden},
		{"tag", http.MethodPost, base + "/tags", map[string]any{"name": "x"}, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := h.doAsUser(t, tc.method, tc.path, tc.body)
			body := readBody(t, res)
			require.Equal(t, tc.want, res.StatusCode, "body %s", body)
		})
	}

	t.Run("upload", func(t *testing.T) {
		res := uploadAttachmentAs(t, h, h.userKey, tk.ID.String(), "shot.txt", []byte("log"))
		res.Body.Close()
		require.Equal(t, http.StatusConflict, res.StatusCode)
	})

	t.Run("the 409 says closed, and is the same for every write that gets one", func(t *testing.T) {
		res := h.doAsUser(t, http.MethodPost, base+"/replies", map[string]any{"body": "x"})
		var e struct {
			Error struct{ Code string } `json:"error"`
		}
		require.NoError(t, json.Unmarshal([]byte(readBody(t, res)), &e))
		require.Equal(t, "ticket_closed", e.Error.Code)
	})

	// And nothing moved.
	replies, err := h.ticketSvc.ListReplies(ctx, tk.ID)
	require.NoError(t, err)
	require.Empty(t, replies)
	atts, err := h.ticketSvc.ListAttachments(ctx, tk.ID)
	require.NoError(t, err)
	require.Empty(t, atts)
	links, err := h.ticketSvc.ListLinks(ctx, tk.ID)
	require.NoError(t, err)
	require.Empty(t, links)
	require.Equal(t, ticket.StatusNameClosed, statusOf(t, h, tk.ID))
}

// The refusal for a ticket the reporter cannot see stays the not-found one:
// the closed-ticket 409 must not become an existence oracle (#174).
func TestReporter_ClosedRefusalIsNotAnOracleForTicketsTheyCannotSee(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	foreign, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Not theirs", Description: "x", CategoryID: h.catID, ReporterUserID: &h.adminID,
	})
	require.NoError(t, err)
	require.NoError(t, h.ticketSvc.Close(ctx, foreign.ID, ticket.Actor{UserID: &h.adminID, Role: user.RoleAdmin}))

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/tickets/" + foreign.ID.String() + "/replies"},
		{http.MethodPut, "/api/v1/tickets/" + foreign.ID.String() + "/custom-fields"},
		{http.MethodPost, "/api/v1/tickets/" + foreign.ID.String() + "/links"},
	} {
		res := h.doAsUser(t, tc.method, tc.path, map[string]any{"body": "x"})
		res.Body.Close()
		require.Equal(t, http.StatusNotFound, res.StatusCode, tc.method+" "+tc.path)
	}
}

// A Resolved ticket still reopens on a requester's reply inside the window and
// not outside it; 0 days means no reopening at all. (Unchanged by #349 — pinned
// here because the whole rule is "Resolved reopens, Closed never does".)
func TestReporter_ResolvedReopensInsideTheWindowAndNotOutsideIt(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	staff := ticket.Actor{UserID: &h.staffID, Role: user.RoleStaff}

	newResolved := func() ticket.Ticket {
		tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
			Subject: "Resolved one", Description: "x", CategoryID: h.catID, ReporterUserID: &h.userID,
		})
		require.NoError(t, err)
		_, err = h.ticketSvc.Resolve(ctx, tk.ID, "done", staff)
		require.NoError(t, err)
		return tk
	}

	t.Run("inside the window", func(t *testing.T) {
		require.NoError(t, h.adminSvc.SetInt(ctx, admin.KeyReopenWindowDays, 7))
		tk := newResolved()
		res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/replies", map[string]any{"body": "not fixed"})
		require.Equal(t, http.StatusCreated, res.StatusCode, readBody(t, res))
		res.Body.Close()
		require.NotEqual(t, ticket.StatusNameResolved, statusOf(t, h, tk.ID))
		require.NotEqual(t, ticket.StatusNameClosed, statusOf(t, h, tk.ID))
	})

	t.Run("0 days is no reopening", func(t *testing.T) {
		require.NoError(t, h.adminSvc.SetInt(ctx, admin.KeyReopenWindowDays, 0))
		tk := newResolved()
		res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/replies", map[string]any{"body": "not fixed"})
		require.Equal(t, http.StatusConflict, res.StatusCode, readBody(t, res))
		res.Body.Close()
		require.Equal(t, ticket.StatusNameResolved, statusOf(t, h, tk.ID))

		// ...and the next sweep closes it, read-only.
		n, err := h.ticketSvc.AutoClose(ctx, h.adminSvc.ReopenWindowDays(ctx), 50)
		require.NoError(t, err)
		require.GreaterOrEqual(t, n, 1)
		require.Equal(t, ticket.StatusNameClosed, statusOf(t, h, tk.ID))
	})
}

// ── staff and admin: terminal ───────────────────────────────────────────────

// By DEFAULT (closed_reopen_policy off) nobody reopens a Closed ticket: not a
// status change, not Resolve, not the reopen endpoint, for staff and admin
// alike. The policy-on cases are in handler_closed_reopen_policy_test.go.
func TestClosedIsTerminalOverREST(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := reporterClosedTicket(t, h)
	base := "/api/v1/tickets/" + tk.ID.String()
	newStatus := statusIDNamed(t, h, ticket.StatusNameNew).String()
	resolved := statusIDNamed(t, h, ticket.StatusNameResolved).String()

	for who, do := range map[string]func(t *testing.T, method, path string, body any) *http.Response{
		"staff": h.do, "admin": h.doAsAdmin, "reporter": h.doAsUser,
	} {
		for _, tc := range []struct {
			name, method, path string
			body               any
			wantStaff          int
		}{
			{"status to New", http.MethodPatch, base, map[string]any{"status_id": newStatus}, http.StatusConflict},
			{"status to Resolved", http.MethodPatch, base, map[string]any{"status_id": resolved}, http.StatusConflict},
			{"resolve", http.MethodPost, base + "/resolve", map[string]any{"notes": "again"}, http.StatusConflict},
		} {
			t.Run(who+" "+tc.name, func(t *testing.T) {
				res := do(t, tc.method, tc.path, tc.body)
				body := readBody(t, res)
				if who == "reporter" {
					require.Equal(t, http.StatusForbidden, res.StatusCode, body)
				} else {
					require.Equal(t, tc.wantStaff, res.StatusCode, body)
					require.Contains(t, body, "ticket_closed")
				}
				require.Equal(t, ticket.StatusNameClosed, statusOf(t, h, tk.ID))
			})
		}

		t.Run(who+" reopen endpoint, policy off", func(t *testing.T) {
			res := do(t, http.MethodPost, base+"/reopen", map[string]any{})
			body := readBody(t, res)
			if who == "reporter" {
				require.Equal(t, http.StatusForbidden, res.StatusCode, body)
			} else {
				require.Equal(t, http.StatusConflict, res.StatusCode, body)
				require.Contains(t, body, "ticket_closed")
			}
			require.Equal(t, ticket.StatusNameClosed, statusOf(t, h, tk.ID))
		})
	}

	t.Run("duplicate-of with auto-resolve cannot take a closed ticket out of Closed", func(t *testing.T) {
		target, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
			Subject: "Original", Description: "x", CategoryID: h.catID, ReporterUserID: &h.userID,
		})
		require.NoError(t, err)
		res := h.doAsAdmin(t, http.MethodPost, base+"/links", map[string]any{
			"target_id": target.ID, "link_type": "duplicate_of", "resolve_as_duplicate": true,
		})
		require.Equal(t, http.StatusConflict, res.StatusCode, readBody(t, res))
		res.Body.Close()
		require.Equal(t, ticket.StatusNameClosed, statusOf(t, h, tk.ID))
	})

	// Decision (DESIGN.md → Closing): only leaving Closed is removed for staff
	// and admin. They still reply, annotate, assign and link.
	t.Run("staff keep their other actions", func(t *testing.T) {
		res := h.do(t, http.MethodPost, base+"/replies", map[string]any{"body": "for the record", "notify_customer": false})
		require.Equal(t, http.StatusCreated, res.StatusCode, readBody(t, res))
		res.Body.Close()
		res = h.do(t, http.MethodPatch, base, map[string]any{"assignee_user_id": h.staffID.String()})
		require.Equal(t, http.StatusOK, res.StatusCode, readBody(t, res))
		res.Body.Close()
		require.Equal(t, ticket.StatusNameClosed, statusOf(t, h, tk.ID), "and none of it reopens")
	})
}

// ── follow-up ───────────────────────────────────────────────────────────────

func TestFollowUp_StaffAndAdminOpenALinkedTicketFromAClosedOne(t *testing.T) {
	for _, who := range []string{"staff", "admin"} {
		t.Run(who, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			ctx := context.Background()
			orig := reporterClosedTicket(t, h)
			before, err := h.ticketSvc.GetByID(ctx, orig.ID)
			require.NoError(t, err)
			repliesBefore, _ := h.ticketSvc.ListReplies(ctx, orig.ID)

			call := h.do
			if who == "admin" {
				call = h.doAsAdmin
			}
			res := call(t, http.MethodPost, "/api/v1/tickets/"+orig.ID.String()+"/follow-up", nil)
			body := readBody(t, res)
			require.Equal(t, http.StatusCreated, res.StatusCode, body)
			var got ticket.Ticket
			require.NoError(t, json.Unmarshal([]byte(body), &got))

			require.NotEqual(t, orig.ID, got.ID)
			require.NotEqual(t, orig.TrackingNumber, got.TrackingNumber)
			require.Equal(t, orig.Subject, got.Subject)
			require.Equal(t, orig.Description, got.Description)
			require.Equal(t, orig.CategoryID, got.CategoryID)
			require.Equal(t, &h.userID, got.ReporterUserID, "the requester is carried over")
			require.Equal(t, "New", statusOf(t, h, got.ID), "a follow-up starts open")

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
			require.Equal(t, ticket.StatusNameClosed, statusOf(t, h, orig.ID))

			// And the requester sees their new ticket in their own queue.
			res = h.doAsUser(t, http.MethodGet, "/api/v1/tickets/"+got.ID.String(), nil)
			res.Body.Close()
			require.Equal(t, http.StatusOK, res.StatusCode)
		})
	}
}

func TestFollowUp_Refusals(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	closed := reporterClosedTicket(t, h)
	open, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Still open", Description: "x", CategoryID: h.catID, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)

	t.Run("a reporter, on their own closed ticket", func(t *testing.T) {
		res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets/"+closed.ID.String()+"/follow-up", nil)
		res.Body.Close()
		require.Equal(t, http.StatusForbidden, res.StatusCode)
	})
	// Decision: a follow-up continues a CLOSED ticket; an open one is refused.
	t.Run("a ticket that is not closed", func(t *testing.T) {
		res := h.do(t, http.MethodPost, "/api/v1/tickets/"+open.ID.String()+"/follow-up", nil)
		body := readBody(t, res)
		require.Equal(t, http.StatusConflict, res.StatusCode, body)
		require.Contains(t, body, "ticket_not_closed")
	})
	t.Run("a ticket that does not exist", func(t *testing.T) {
		res := h.do(t, http.MethodPost, "/api/v1/tickets/"+uuid.NewString()+"/follow-up", nil)
		res.Body.Close()
		require.Equal(t, http.StatusNotFound, res.StatusCode)
	})
	t.Run("no credentials", func(t *testing.T) {
		res := h.doUnauth(t, http.MethodPost, "/api/v1/tickets/"+closed.ID.String()+"/follow-up", nil)
		res.Body.Close()
		require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	})

	links, err := h.ticketSvc.ListLinks(ctx, closed.ID)
	require.NoError(t, err)
	require.Empty(t, links, "no refused request left a link behind")
}

// A guest's closed ticket: the follow-up copies the guest and mails them a
// link to the NEW ticket, through the ordinary creation path.
func TestFollowUp_OfAGuestTicketCopiesTheGuestAndMailsALink(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk, _ := seedGuestTicket(t, h)
	require.NoError(t, h.ticketSvc.Close(ctx, tk.ID, ticket.Actor{UserID: &h.adminID, Role: user.RoleAdmin}))
	h.dispatcher.record = true

	res := h.do(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/follow-up", nil)
	body := readBody(t, res)
	require.Equal(t, http.StatusCreated, res.StatusCode, body)
	var got ticket.Ticket
	require.NoError(t, json.Unmarshal([]byte(body), &got))
	require.NotNil(t, got.GuestEmail)
	require.Equal(t, "guest@test.local", *got.GuestEmail)
	require.Equal(t, "Ada", got.GuestName)

	var created *notification.Event
	for i := range h.dispatcher.queued {
		if h.dispatcher.queued[i].Type == notification.EventTicketCreated {
			created = &h.dispatcher.queued[i]
		}
	}
	require.NotNil(t, created)
	require.Equal(t, got.ID, created.TicketID)
	require.True(t, created.GuestLink)
	sent, ok, err := h.srv.PrepareGuestLink(ctx, *created)
	require.NoError(t, err)
	require.True(t, ok)
	r := h.doGuest(t, http.MethodPost, "/api/v1/guest/replies", sent.GuestToken, map[string]any{"body": "thanks"})
	r.Body.Close()
	require.Equal(t, http.StatusCreated, r.StatusCode, "the follow-up is a live ticket: its guest can reply")
}
