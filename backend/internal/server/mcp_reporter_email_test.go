package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// #290, Erik's ruling: filing for somebody takes reporter_email. The server
// resolves it to an account if one exists, otherwise the ticket is a guest
// ticket. MCP's create_ticket gets the same rule REST has, from the same code
// (Server.ResolveOnBehalf): same 40-an-hour cap on addresses with no account,
// same created mail, same actor.

// mcpCreate calls create_ticket on the staff API key's MCP session and returns
// the decoded ticket, or the error text.
func mcpCreate(t *testing.T, c mcpCaller, extra map[string]any, h *harness) (tk ticket.Ticket, errText string) {
	t.Helper()
	args := map[string]any{"subject": "Phone-in", "description": "x", "category_id": h.catID.String()}
	for k, v := range extra {
		args[k] = v
	}
	var rpc struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(c.call(t, "create_ticket", args)), &rpc))
	require.NotEmpty(t, rpc.Result.Content)
	text := rpc.Result.Content[0].Text
	if rpc.Result.IsError {
		return tk, text
	}
	require.NoError(t, json.Unmarshal([]byte(text), &tk))
	return tk, ""
}

func newMCPHarness(t *testing.T) (*harness, mcpCaller, func()) {
	t.Helper()
	h, cleanup := newHarness(t)
	require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeyGuestSubmissionEnabled, true))
	h.dispatcher.record = true
	return h, mcpTransports[0].open(t, h, h.apiKey), cleanup
}

func TestMCPCreateTicket_ReporterEmailOfAnAccountFilesForTheAccount(t *testing.T) {
	h, c, cleanup := newMCPHarness(t)
	defer cleanup()

	tk, errText := mcpCreate(t, c, map[string]any{"reporter_email": " User@Test.local ", "guest_name": "Ada"}, h)
	require.Empty(t, errText)
	require.NotNil(t, tk.ReporterUserID)
	require.Equal(t, h.userID, *tk.ReporterUserID)
	require.Nil(t, tk.GuestEmail, "not a guest ticket")
	actor := createdAuditActor(t, h, tk.ID)
	require.NotNil(t, actor)
	require.Equal(t, h.staffID, *actor, "the caller is the actor, not the requester")
	for _, ev := range h.dispatcher.queued {
		require.False(t, ev.GuestLink)
	}
}

func TestMCPCreateTicket_ReporterEmailWithNoAccountIsAGuestTicket(t *testing.T) {
	h, c, cleanup := newMCPHarness(t)
	defer cleanup()

	tk, errText := mcpCreate(t, c, map[string]any{
		"reporter_email": "  New.Person@Example.test ", "guest_name": " Ada ", "guest_phone": "555-0100",
	}, h)
	require.Empty(t, errText)
	require.Nil(t, tk.ReporterUserID)
	require.NotNil(t, tk.GuestEmail)
	require.Equal(t, "new.person@example.test", *tk.GuestEmail)
	require.Equal(t, "Ada", tk.GuestName)
	actor := createdAuditActor(t, h, tk.ID)
	require.NotNil(t, actor)
	require.Equal(t, h.staffID, *actor)

	mail := createdMail(t, h)
	require.True(t, mail.GuestLink, "the guest is sent the link")
	require.Equal(t, "new.person@example.test", mail.Recipient)
	require.NotNil(t, mail.ActorID)
	require.Equal(t, h.staffID, *mail.ActorID)
}

// Same outcome as REST for the same request: same created mail, same actor.
func TestMCPCreateTicket_ReporterEmailMatchesREST(t *testing.T) {
	for name, extra := range map[string]map[string]any{
		"an account":            {"reporter_email": "user@test.local", "guest_name": "Ada"},
		"an address no account": {"reporter_email": "walk-in@example.test", "guest_name": "Ada"},
	} {
		t.Run(name, func(t *testing.T) {
			h, c, cleanup := newMCPHarness(t)
			defer cleanup()

			decodeTicket(t, h.postOnBehalf(t, "staff", extra))
			viaREST := createdMail(t, h)
			h.dispatcher.queued = nil
			_, errText := mcpCreate(t, c, extra, h)
			require.Empty(t, errText)
			viaMCP := createdMail(t, h)

			require.Equal(t, viaREST.Recipient, viaMCP.Recipient)
			require.Equal(t, viaREST.GuestLink, viaMCP.GuestLink)
			require.Equal(t, viaREST.ActorID, viaMCP.ActorID)
		})
	}
}

func TestMCPCreateTicket_ReporterEmailRefusals(t *testing.T) {
	cases := []struct {
		name  string
		extra func(h *harness) map[string]any
		want  string
	}{
		{"not an address", func(*harness) map[string]any { return map[string]any{"reporter_email": "nope"} }, "reporter_email"},
		{"a display name in the address", func(*harness) map[string]any {
			return map[string]any{"reporter_email": "Eve <eve@example.test>"}
		}, "reporter_email"},
		{"no account and no name", func(*harness) map[string]any {
			return map[string]any{"reporter_email": "nobody@example.test"}
		}, "name is required"},
		{"both fields", func(h *harness) map[string]any {
			return map[string]any{"reporter_user_id": h.userID.String(), "reporter_email": "user@test.local"}
		}, "only one of"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, c, cleanup := newMCPHarness(t)
			defer cleanup()
			before := ticketCount(t, h)

			_, errText := mcpCreate(t, c, tc.extra(h), h)
			require.Contains(t, errText, tc.want)
			require.Equal(t, before, ticketCount(t, h), "nothing was filed")
			require.Empty(t, h.dispatcher.queued, "and nobody was told")
		})
	}

	t.Run("guest submission is off", func(t *testing.T) {
		h, c, cleanup := newMCPHarness(t)
		defer cleanup()
		require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeyGuestSubmissionEnabled, false))
		before := ticketCount(t, h)

		_, errText := mcpCreate(t, c, map[string]any{"reporter_email": "nobody@example.test", "guest_name": "Ada"}, h)
		require.Contains(t, errText, "guest submission")
		require.Equal(t, before, ticketCount(t, h))

		tk, errText := mcpCreate(t, c, map[string]any{"reporter_email": "user@test.local", "guest_name": "Ada"}, h)
		require.Empty(t, errText, "an account's address still works")
		require.NotNil(t, tk.ReporterUserID)
	})
}

// The 40-an-hour budget is one per staff member across both surfaces: MCP
// spends REST's, and neither is a way round the other. An address spends a unit
// whether or not it has an account.
func TestMCPCreateTicket_ReporterEmailIsCappedWithRESTsBudget(t *testing.T) {
	h, c, cleanup := newMCPHarness(t)
	defer cleanup()

	for i := 0; i < onBehalfMailBudget-1; i++ {
		_, errText := mcpCreate(t, c, map[string]any{
			"reporter_email": fmt.Sprintf("walk-in-%d@example.test", i), "guest_name": "Ada"}, h)
		require.Empty(t, errText, "typed #%d", i)
	}
	// The last slot goes to REST, for an account...
	res := h.postOnBehalf(t, "staff", map[string]any{"reporter_email": "user@test.local", "guest_name": "Ada"})
	require.Equal(t, http.StatusCreated, res.StatusCode, readBody(t, res))
	before := ticketCount(t, h)

	// ...so MCP is out of budget, and says why.
	_, errText := mcpCreate(t, c, map[string]any{"reporter_email": "one-too-many@example.test", "guest_name": "Ada"}, h)
	require.Contains(t, errText, "too many")
	require.Equal(t, before, ticketCount(t, h), "the refused one created nothing")
	require.Equal(t, http.StatusTooManyRequests, typedNew(t, h, "staff", 1001).StatusCode, "and REST agrees")
	_, errText = mcpCreate(t, c, map[string]any{"reporter_email": "user@test.local", "guest_name": "Ada"}, h)
	require.Contains(t, errText, "too many", "an account's address is held to it too")

	// A picked id is not an address probe.
	_, errText = mcpCreate(t, c, map[string]any{"reporter_user_id": h.userID.String()}, h)
	require.Empty(t, errText)
}

// reporter_user_id keeps working: existing callers send it.
func TestMCPCreateTicket_ReporterUserIDStillWorks(t *testing.T) {
	h, c, cleanup := newMCPHarness(t)
	defer cleanup()

	tk, errText := mcpCreate(t, c, map[string]any{"reporter_user_id": h.userID.String()}, h)
	require.Empty(t, errText)
	require.NotNil(t, tk.ReporterUserID)
	require.Equal(t, h.userID, *tk.ReporterUserID)
}
