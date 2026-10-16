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
)

// #444: staff and administrators file a ticket on behalf of a requester, by
// picking an existing user (reporter_user_id) or typing an address
// (reporter_email). A typed address is filed exactly like a guest ticket. The
// person who filed is the actor; the requester is the reporter.

func (h *harness) postOnBehalf(t *testing.T, as string, extra map[string]any) *http.Response {
	t.Helper()
	body := map[string]any{"subject": "Phone-in", "description": "x", "category_id": h.catID.String()}
	for k, v := range extra {
		body[k] = v
	}
	switch as {
	case "staff":
		return h.do(t, http.MethodPost, "/api/v1/tickets", body)
	case "admin":
		return h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets", body)
	case "user":
		return h.doAsUser(t, http.MethodPost, "/api/v1/tickets", body)
	}
	t.Fatalf("unknown caller %q", as)
	return nil
}

// createdAuditActor is the actor of the ticket's one "created" audit entry.
func createdAuditActor(t *testing.T, h *harness, id uuid.UUID) *uuid.UUID {
	t.Helper()
	entries, err := h.auditStore.ListByEntity(context.Background(), "ticket", id, 50, 0)
	require.NoError(t, err)
	var actors []*uuid.UUID
	for _, e := range entries {
		if e.Action == "created" {
			actors = append(actors, e.ActorID)
		}
	}
	require.Len(t, actors, 1, "exactly one created entry")
	return actors[0]
}

func ticketCount(t *testing.T, h *harness) int {
	t.Helper()
	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets?scope=all&limit=200", nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	var all []json.RawMessage
	require.NoError(t, json.NewDecoder(res.Body).Decode(&all))
	return len(all)
}

func TestCreateTicket_OnBehalf_PickedUser(t *testing.T) {
	for _, caller := range []string{"staff", "admin"} {
		t.Run(caller, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			filer := map[string]uuid.UUID{"staff": h.staffID, "admin": h.adminID}[caller]
			h.dispatcher.record = true

			tk := decodeTicket(t, h.postOnBehalf(t, caller, map[string]any{"reporter_user_id": h.userID.String()}))

			require.NotNil(t, tk.ReporterUserID)
			require.Equal(t, h.userID, *tk.ReporterUserID, "the picked user is the reporter")
			require.Nil(t, tk.GuestEmail)
			actor := createdAuditActor(t, h, tk.ID)
			require.NotNil(t, actor)
			require.Equal(t, filer, *actor, "the audit entry names who filed it, not who it is for")

			var created *notification.Event
			for i := range h.dispatcher.queued {
				if h.dispatcher.queued[i].Type == notification.EventTicketCreated {
					created = &h.dispatcher.queued[i]
				}
			}
			require.NotNil(t, created)
			require.NotNil(t, created.ActorID)
			require.Equal(t, filer, *created.ActorID, "and so does the event")
			require.False(t, created.GuestLink, "an account holder is not sent a guest link")
		})
	}
}

// A typed address with no account behind it is a guest ticket in every way
// that matters: the person is sent the guest link at that address, and the
// link works.
func TestCreateTicket_OnBehalf_TypedAddressIsAGuestTicket(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyGuestSubmissionEnabled, true))
	h.dispatcher.record = true

	// Case and surrounding space are the guest rules' to normalise.
	tk := decodeTicket(t, h.postOnBehalf(t, "staff", map[string]any{
		"reporter_email": "  New.Person@Example.test ", "guest_name": " Ada ",
	}))

	require.Nil(t, tk.ReporterUserID, "no account, so no reporter user")
	require.NotNil(t, tk.GuestEmail)
	require.Equal(t, "new.person@example.test", *tk.GuestEmail)
	require.Equal(t, "Ada", tk.GuestName, "the guest form's optional name is kept, as on a guest ticket")
	actor := createdAuditActor(t, h, tk.ID)
	require.NotNil(t, actor)
	require.Equal(t, h.staffID, *actor)

	var created *notification.Event
	for i := range h.dispatcher.queued {
		if h.dispatcher.queued[i].Type == notification.EventTicketCreated {
			created = &h.dispatcher.queued[i]
		}
	}
	require.NotNil(t, created)
	require.True(t, created.GuestLink, "the created mail carries a guest link")
	require.Equal(t, "new.person@example.test", created.Recipient, "to the typed address")
	require.NotNil(t, created.ActorID)
	require.Equal(t, h.staffID, *created.ActorID)

	sent, ok, err := h.srv.PrepareGuestLink(ctx, *created)
	require.NoError(t, err)
	require.True(t, ok)
	read := h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", sent.GuestToken, nil)
	defer read.Body.Close()
	require.Equal(t, http.StatusOK, read.StatusCode, "the link opens the ticket")
}

// An address that belongs to an account files for that account: the person
// signs in to see it, and a second, guest identity for them would split their
// tickets in two.
func TestCreateTicket_OnBehalf_TypedAddressOfAnAccountFilesForTheAccount(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	h.dispatcher.record = true

	tk := decodeTicket(t, h.postOnBehalf(t, "staff", map[string]any{"reporter_email": "User@Test.local"}))

	require.NotNil(t, tk.ReporterUserID)
	require.Equal(t, h.userID, *tk.ReporterUserID)
	require.Nil(t, tk.GuestEmail, "not a guest ticket")
	for _, ev := range h.dispatcher.queued {
		require.False(t, ev.GuestLink, "and no guest link is sent")
	}
	actor := createdAuditActor(t, h, tk.ID)
	require.NotNil(t, actor)
	require.Equal(t, h.staffID, *actor)
}

// Guest links are suspended while guest submission is off (#424), so a ticket
// for an address with no account would be filed and its person sent a link that
// answers 404. It is refused instead, and nothing is written. An address that
// belongs to an account needs no link and is unaffected.
func TestCreateTicket_OnBehalf_TypedAddressNeedsGuestSubmission(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeyGuestSubmissionEnabled, false))
	before := ticketCount(t, h)

	res := h.postOnBehalf(t, "staff", map[string]any{"reporter_email": "nobody@example.test"})
	body := readBody(t, res)
	require.Equal(t, http.StatusBadRequest, res.StatusCode, body)
	require.Contains(t, body, "guest submission")
	require.Equal(t, before, ticketCount(t, h), "nothing was filed")

	tk := decodeTicket(t, h.postOnBehalf(t, "staff", map[string]any{"reporter_email": "user@test.local"}))
	require.NotNil(t, tk.ReporterUserID, "an account's address still works")
}

// A requester cannot file for anyone else, and is told so rather than having
// the field ignored.
func TestCreateTicket_OnBehalf_RequesterIsRefused(t *testing.T) {
	cases := []struct {
		name  string
		extra map[string]any
	}{
		{"another user", map[string]any{"reporter_user_id": uuid.NewString()}},
		{"an existing user", map[string]any{"reporter_user_id": "STAFF"}},
		{"themselves", map[string]any{"reporter_user_id": "SELF"}},
		{"a typed address", map[string]any{"reporter_email": "someone@example.test"}},
		{"their own address", map[string]any{"reporter_email": "user@test.local"}},
		{"an empty address", map[string]any{"reporter_email": ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeyGuestSubmissionEnabled, true))
			for k, v := range tc.extra {
				switch v {
				case "STAFF":
					tc.extra[k] = h.staffID.String()
				case "SELF":
					tc.extra[k] = h.userID.String()
				}
			}
			before := ticketCount(t, h)
			h.dispatcher.record = true

			res := h.postOnBehalf(t, "user", tc.extra)
			body := readBody(t, res)
			require.Equal(t, http.StatusForbidden, res.StatusCode, body)
			require.Contains(t, body, `"forbidden"`)
			require.Equal(t, before, ticketCount(t, h), "nothing was filed")
			require.Empty(t, h.dispatcher.queued, "and nobody was told")
		})
	}
}

func TestCreateTicket_OnBehalf_Refusals(t *testing.T) {
	cases := []struct {
		name  string
		extra func(h *harness) map[string]any
		want  string // a fragment of the message
	}{
		{"both fields", func(h *harness) map[string]any {
			return map[string]any{"reporter_user_id": h.userID.String(), "reporter_email": "user@test.local"}
		}, "only one of"},
		{"not an address", func(*harness) map[string]any {
			return map[string]any{"reporter_email": "not an address"}
		}, "reporter_email"},
		{"an empty address", func(*harness) map[string]any {
			return map[string]any{"reporter_email": ""}
		}, "reporter_email"},
		{"a display name in the address", func(*harness) map[string]any {
			return map[string]any{"reporter_email": "Eve <eve@example.test>"}
		}, "reporter_email"},
		{"an address that needs quotation marks", func(*harness) map[string]any {
			return map[string]any{"reporter_email": `"john doe"@example.test`}
		}, "reporter_email"},
		{"an id that is not a user", func(*harness) map[string]any {
			return map[string]any{"reporter_user_id": uuid.NewString()}
		}, "reporter_user_id is not a user"},
		{"the nil id", func(*harness) map[string]any {
			return map[string]any{"reporter_user_id": uuid.Nil.String()}
		}, "reporter_user_id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeyGuestSubmissionEnabled, true))
			before := ticketCount(t, h)

			res := h.postOnBehalf(t, "staff", tc.extra(h))
			body := readBody(t, res)
			require.Equal(t, http.StatusBadRequest, res.StatusCode, body)
			require.Contains(t, body, tc.want)
			require.Equal(t, before, ticketCount(t, h), "nothing was filed")
		})
	}

	t.Run("an id that is not a uuid", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		res := h.postOnBehalf(t, "staff", map[string]any{"reporter_user_id": "not-a-uuid"})
		require.Equal(t, http.StatusBadRequest, res.StatusCode)
		res.Body.Close()
	})
}

// Neither field: the caller is the requester and the actor, as before.
func TestCreateTicket_OnBehalf_DefaultIsTheCaller(t *testing.T) {
	for _, caller := range []string{"staff", "admin", "user"} {
		t.Run(caller, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			self := map[string]uuid.UUID{"staff": h.staffID, "admin": h.adminID, "user": h.userID}[caller]

			tk := decodeTicket(t, h.postOnBehalf(t, caller, nil))

			stored, err := h.ticketSvc.GetByID(context.Background(), tk.ID)
			require.NoError(t, err)
			require.NotNil(t, stored.ReporterUserID)
			require.Equal(t, self, *stored.ReporterUserID)
			actor := createdAuditActor(t, h, tk.ID)
			require.NotNil(t, actor)
			require.Equal(t, self, *actor)
		})
	}
}

// Filing for someone is the same write as any other create: a credential
// without tickets:write cannot do it, whoever owns it.
func TestCreateTicket_OnBehalf_NeedsTheTicketsWriteScope(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	readOnly := mintKey(t, h, []string{"tickets:read"})

	res := withKey(t, h, readOnly, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "x", "category_id": h.catID.String(), "reporter_user_id": h.userID.String(),
	})
	defer res.Body.Close()
	require.Equal(t, http.StatusForbidden, res.StatusCode)
}

// createdMail is the "We have received your ticket" event a create queued.
func createdMail(t *testing.T, h *harness) notification.Event {
	t.Helper()
	var got []notification.Event
	for _, ev := range h.dispatcher.queued {
		if ev.Type == notification.EventTicketCreated {
			got = append(got, ev)
		}
	}
	require.Len(t, got, 1, "one created event")
	return got[0]
}

// An existing user who is filed for hears of it with the ordinary mail, whether
// they were picked or their address was typed; a member of staff filing their
// own ticket is not mailed about it, as before.
func TestCreateTicket_OnBehalf_NotifiesTheAccountFiledFor(t *testing.T) {
	cases := []struct {
		name          string
		caller        string
		extra         func(h *harness) map[string]any
		wantRecipient string
	}{
		{"picked from the list", "staff", func(h *harness) map[string]any {
			return map[string]any{"reporter_user_id": h.userID.String()}
		}, "user@test.local"},
		{"picked from the list, by an administrator", "admin", func(h *harness) map[string]any {
			return map[string]any{"reporter_user_id": h.userID.String()}
		}, "user@test.local"},
		{"a typed address that matches an account", "staff", func(*harness) map[string]any {
			return map[string]any{"reporter_email": " User@Test.local "}
		}, "user@test.local"},
		{"staff file their own ticket", "staff", func(*harness) map[string]any { return nil }, ""},
		{"staff pick themselves", "staff", func(h *harness) map[string]any {
			return map[string]any{"reporter_user_id": h.staffID.String()}
		}, ""},
		{"a requester files their own ticket", "user", func(*harness) map[string]any { return nil }, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			h.dispatcher.record = true

			res := h.postOnBehalf(t, tc.caller, tc.extra(h))
			require.Equal(t, http.StatusCreated, res.StatusCode, readBody(t, res))

			ev := createdMail(t, h)
			require.Equal(t, tc.wantRecipient, ev.Recipient)
			require.False(t, ev.GuestLink, "an account holder signs in; no guest link")
		})
	}
}
