package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// closed_reopen_policy (#349): off (the default) | admin | staff_admin.
// Whether a Closed ticket can be force-reopened at all and by whom. Requesters
// never can. The rule is one predicate in the ticket service; these tests drive
// it over the real HTTP and MCP surfaces and the real database.

func setReopenPolicy(t *testing.T, h *harness, v string) {
	t.Helper()
	require.NoError(t, h.adminSvc.SetString(context.Background(), admin.KeyClosedReopenPolicy, v))
}

func policyRoutes(h *harness) map[string]func(t *testing.T, do func(*testing.T, string, string, any) *http.Response, id string) *http.Response {
	return map[string]func(t *testing.T, do func(*testing.T, string, string, any) *http.Response, id string) *http.Response{
		"POST /reopen": func(t *testing.T, do func(*testing.T, string, string, any) *http.Response, id string) *http.Response {
			return do(t, http.MethodPost, "/api/v1/tickets/"+id+"/reopen", nil)
		},
		"PATCH status": func(t *testing.T, do func(*testing.T, string, string, any) *http.Response, id string) *http.Response {
			return do(t, http.MethodPatch, "/api/v1/tickets/"+id,
				map[string]any{"status_id": statusIDNamed(t, h, ticket.StatusNameNew).String()})
		},
		"POST /resolve": func(t *testing.T, do func(*testing.T, string, string, any) *http.Response, id string) *http.Response {
			return do(t, http.MethodPost, "/api/v1/tickets/"+id+"/resolve", map[string]any{"notes": "again"})
		},
		"duplicate-of with auto-resolve": func(t *testing.T, do func(*testing.T, string, string, any) *http.Response, id string) *http.Response {
			target, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
				Subject: "Original", Description: "x", CategoryID: h.catID, ReporterUserID: &h.userID,
			})
			require.NoError(t, err)
			return do(t, http.MethodPost, "/api/v1/tickets/"+id+"/links", map[string]any{
				"target_id": target.ID, "link_type": "duplicate_of", "resolve_as_duplicate": true,
			})
		},
	}
}

// Every route out of Closed, every mode, every kind of caller (an API key per
// role: the same handlers a session takes, and what an OAuth client acting as
// a role meets).
func TestClosedReopenPolicy_OverREST(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	callers := map[string]func(*testing.T, string, string, any) *http.Response{
		"staff": h.do, "admin": h.doAsAdmin, "reporter": h.doAsUser,
	}
	allowed := map[string]map[string]bool{
		"":                            {},
		ticket.ReopenPolicyOff:        {},
		ticket.ReopenPolicyAdmin:      {"admin": true},
		ticket.ReopenPolicyStaffAdmin: {"admin": true, "staff": true},
	}

	for policy, who := range allowed {
		setReopenPolicy(t, h, policy)
		for rname, route := range policyRoutes(h) {
			for cname, do := range callers {
				t.Run(policy+"/"+cname+"/"+rname, func(t *testing.T) {
					tk := reporterClosedTicket(t, h)

					res := route(t, do, tk.ID.String())
					body := readBody(t, res)

					if cname == "reporter" {
						require.Equal(t, http.StatusForbidden, res.StatusCode,
							"a requester never, whatever the setting; %s", body)
						require.NotContains(t, strings.ToLower(body), "disabled")
						require.Equal(t, ticket.StatusNameClosed, statusOf(t, h, tk.ID))
						return
					}
					if who[cname] {
						require.Equal(t, http.StatusOK, res.StatusCode, body)
						require.NotEqual(t, ticket.StatusNameClosed, statusOf(t, h, tk.ID))
						return
					}
					require.Equal(t, http.StatusConflict, res.StatusCode, body)
					require.Contains(t, body, "ticket_closed")
					if policy == ticket.ReopenPolicyAdmin {
						require.Contains(t, body, "restricted to administrators")
					} else {
						require.Contains(t, body, "disabled")
					}
					require.Contains(t, body, "follow-up", "and says where to go instead")
					require.Equal(t, ticket.StatusNameClosed, statusOf(t, h, tk.ID))
				})
			}
		}
	}
}

// The setting is read when a request is decided, not at start-up.
func TestClosedReopenPolicy_TakesEffectOnTheNextRequest(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := reporterClosedTicket(t, h)
	path := "/api/v1/tickets/" + tk.ID.String() + "/reopen"

	res := h.doAsAdmin(t, http.MethodPost, path, nil)
	res.Body.Close()
	require.Equal(t, http.StatusConflict, res.StatusCode, "default: off")

	setReopenPolicy(t, h, ticket.ReopenPolicyAdmin)
	res = h.do(t, http.MethodPost, path, nil)
	res.Body.Close()
	require.Equal(t, http.StatusConflict, res.StatusCode, "staff under admin-only")

	res = h.doAsAdmin(t, http.MethodPost, path, nil)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode, "the very next request sees the new value")
}

// The default is off with nothing stored: an upgraded instance is exactly the
// terminal behaviour it had.
func TestClosedReopenPolicy_DefaultsToOffAndAnUnrecognisedStoredValueIsOff(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.Equal(t, "off", h.adminSvc.ClosedReopenPolicy(ctx))

	for _, junk := range []string{"", "everyone", "Admin", "true"} {
		setReopenPolicy(t, h, junk)
		require.Equal(t, "off", h.adminSvc.ClosedReopenPolicy(ctx), junk)
		tk := reporterClosedTicket(t, h)
		res := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/reopen", nil)
		res.Body.Close()
		require.Equal(t, http.StatusConflict, res.StatusCode, junk)
	}
}

// Validated at save time, session-gated like the other settings that widen who
// may do what.
func TestClosedReopenPolicy_SettingIsValidatedAndSessionGated(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	require.Contains(t, admin.AuthCriticalKeys(), admin.KeyClosedReopenPolicy)

	// An API key is refused before validation is reached.
	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{admin.KeyClosedReopenPolicy: "staff_admin"})
	res.Body.Close()
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "off", h.adminSvc.ClosedReopenPolicy(context.Background()))

	sess := &session{h: h}
	res, body := sess.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "admin@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "admin login; body: %s", body)

	for _, bad := range []any{"Admin", "staff", "everyone", "", true, 1, nil} {
		res, raw := sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
			map[string]any{admin.KeyClosedReopenPolicy: bad})
		require.Equal(t, http.StatusBadRequest, res.StatusCode, "%v; body: %s", bad, raw)
	}
	var e struct {
		Error struct{ Code string } `json:"error"`
	}
	res, raw := sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{admin.KeyClosedReopenPolicy: "everyone"})
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
	require.NoError(t, json.Unmarshal(raw, &e))
	require.Equal(t, "invalid_closed_reopen_policy", e.Error.Code)
	require.Equal(t, "off", h.adminSvc.ClosedReopenPolicy(context.Background()), "a refused write stores nothing")

	for _, ok := range []string{"off", "admin", "staff_admin"} {
		res, raw := sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
			map[string]any{admin.KeyClosedReopenPolicy: ok})
		require.Equal(t, http.StatusNoContent, res.StatusCode, "%q; body: %s", ok, raw)
		require.Equal(t, ok, h.adminSvc.ClosedReopenPolicy(context.Background()))
	}

	// And a session of a lesser role cannot change it at all.
	res = h.doAsUser(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{admin.KeyClosedReopenPolicy: "staff_admin"})
	res.Body.Close()
	require.NotEqual(t, http.StatusNoContent, res.StatusCode)
	res = h.do(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{admin.KeyClosedReopenPolicy: "staff_admin"})
	res.Body.Close()
	require.NotEqual(t, http.StatusNoContent, res.StatusCode)
}

// The ticket page asks the ticket it already loads whether the Reopen button
// is for this viewer.
func TestClosedReopenPolicy_CanReopenOnTheTicketResponse(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	closed := reporterClosedTicket(t, h)
	open, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "Open one", Description: "x", CategoryID: h.catID, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)

	canReopen := func(do func(*testing.T, string, string, any) *http.Response, id string) bool {
		res := do(t, http.MethodGet, "/api/v1/tickets/"+id, nil)
		var v struct {
			CanReopen *bool `json:"can_reopen"`
		}
		require.NoError(t, json.Unmarshal([]byte(readBody(t, res)), &v))
		require.NotNil(t, v.CanReopen, "the field is always present")
		return *v.CanReopen
	}
	for policy, want := range map[string]map[string]bool{
		ticket.ReopenPolicyOff:        {"staff": false, "admin": false, "reporter": false},
		ticket.ReopenPolicyAdmin:      {"staff": false, "admin": true, "reporter": false},
		ticket.ReopenPolicyStaffAdmin: {"staff": true, "admin": true, "reporter": false},
	} {
		setReopenPolicy(t, h, policy)
		for who, do := range map[string]func(*testing.T, string, string, any) *http.Response{
			"staff": h.do, "admin": h.doAsAdmin, "reporter": h.doAsUser,
		} {
			require.Equal(t, want[who], canReopen(do, closed.ID.String()), "%s / %s", policy, who)
			require.False(t, canReopen(do, open.ID.String()), "an open ticket is never reopenable: %s / %s", policy, who)
		}
	}
}

// After a force-reopen it is an ordinary open ticket: the guest's link writes
// again, and nothing about the close lingered on it.
func TestClosedReopenPolicy_AReopenedGuestTicketIsWritableAgain(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	setReopenPolicy(t, h, ticket.ReopenPolicyStaffAdmin)
	tk, held := seedGuestTicket(t, h)

	res := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/close", nil)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	res = h.doGuest(t, http.MethodPost, "/api/v1/guest/replies", held, map[string]any{"body": "x"})
	res.Body.Close()
	require.Equal(t, http.StatusNotFound, res.StatusCode, "closed: read-only")

	res = h.do(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/reopen", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, readBody(t, res))
	res.Body.Close()
	require.NotEqual(t, ticket.StatusNameClosed, statusOf(t, h, tk.ID))

	// The reopen mail carried a fresh link (issued at send time, as Reopen
	// always did); a link issued now writes.
	token, err := h.ticketSvc.IssueGuestToken(ctx, tk.ID)
	require.NoError(t, err)
	res = h.doGuest(t, http.MethodPost, "/api/v1/guest/replies", token, map[string]any{"body": "back again"})
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)
}

// A guest has no way to reopen in any mode.
func TestClosedReopenPolicy_GuestsNeverReopen(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	for _, policy := range []string{ticket.ReopenPolicyOff, ticket.ReopenPolicyAdmin, ticket.ReopenPolicyStaffAdmin} {
		setReopenPolicy(t, h, policy)
		tk, token := seedGuestTicket(t, h)
		require.NoError(t, h.ticketSvc.Close(context.Background(), tk.ID,
			ticket.Actor{UserID: &h.adminID, Role: "admin"}))
		res := h.doGuest(t, http.MethodPost, "/api/v1/guest/replies", token, map[string]any{"body": "please reopen"})
		res.Body.Close()
		require.Equal(t, http.StatusNotFound, res.StatusCode, policy)
		require.Equal(t, ticket.StatusNameClosed, statusOf(t, h, tk.ID), policy)
	}
}

// MCP: update_ticket_status out of Closed goes through the same service rule.
// There was no reopen tool before #349 and there is none now.
func TestClosedReopenPolicy_OverMCP(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	newID := statusIDNamed(t, h, ticket.StatusNameNew).String()

	staff := openMCP(t, h, h.apiKey)
	defer staff.closeBody()
	adm := openMCP(t, h, h.adminKey)
	defer adm.closeBody()
	rep := openMCP(t, h, h.userKey)
	defer rep.closeBody()

	move := func(c *mcpConn, id string) string {
		return c.call(t, "update_ticket_status", map[string]any{"ticket_id": id, "status_id": newID})
	}

	t.Run("off: nobody leaves Closed", func(t *testing.T) {
		setReopenPolicy(t, h, ticket.ReopenPolicyOff)
		for name, c := range map[string]*mcpConn{"staff": staff, "admin": adm} {
			tk := reporterClosedTicket(t, h)
			got := move(c, tk.ID.String())
			require.Contains(t, got, `"isError":true`, "%s: %s", name, got)
			require.Contains(t, got, "disabled", got)
			require.Equal(t, ticket.StatusNameClosed, statusOf(t, h, tk.ID))
		}
	})

	t.Run("admin: only an admin, and the change is seen by the same connection", func(t *testing.T) {
		setReopenPolicy(t, h, ticket.ReopenPolicyAdmin)
		tk := reporterClosedTicket(t, h)
		got := move(staff, tk.ID.String())
		require.Contains(t, got, `"isError":true`, got)
		require.Contains(t, got, "restricted to administrators", got)
		require.Equal(t, ticket.StatusNameClosed, statusOf(t, h, tk.ID))

		got = move(adm, tk.ID.String())
		require.NotContains(t, got, `"isError":true`, got)
		require.NotEqual(t, ticket.StatusNameClosed, statusOf(t, h, tk.ID))
	})

	t.Run("staff_admin: both", func(t *testing.T) {
		setReopenPolicy(t, h, ticket.ReopenPolicyStaffAdmin)
		for name, c := range map[string]*mcpConn{"staff": staff, "admin": adm} {
			tk := reporterClosedTicket(t, h)
			got := move(c, tk.ID.String())
			require.NotContains(t, got, `"isError":true`, "%s: %s", name, got)
			require.NotEqual(t, ticket.StatusNameClosed, statusOf(t, h, tk.ID))
		}
	})

	t.Run("a reporting user never, in any mode", func(t *testing.T) {
		for _, policy := range []string{ticket.ReopenPolicyOff, ticket.ReopenPolicyAdmin, ticket.ReopenPolicyStaffAdmin} {
			setReopenPolicy(t, h, policy)
			tk := reporterClosedTicket(t, h)
			got := move(rep, tk.ID.String())
			require.Contains(t, got, `"isError":true`, got)
			require.Contains(t, got, "staff", got)
			require.Equal(t, ticket.StatusNameClosed, statusOf(t, h, tk.ID), policy)
		}
	})

	t.Run("no reopen tool exists", func(t *testing.T) {
		got := staff.call(t, "reopen_ticket", map[string]any{"ticket_id": reporterClosedTicket(t, h).ID.String()})
		require.Contains(t, got, `"error"`)
	})
}
