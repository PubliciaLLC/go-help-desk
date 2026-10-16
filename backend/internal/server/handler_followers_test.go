package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Ticket followers (#441, part D of #464). Following is a personal choice made
// by a person, so these requests go in on a browser session: an API key and an
// OAuth client are refused, and the harness's usual helpers (do, doAsAdmin)
// are API keys.

type who int

const (
	asStaff who = iota
	asAdmin
	asReporter
)

func (h *harness) session(t *testing.T, w who) string {
	t.Helper()
	switch w {
	case asAdmin:
		return h.sessionCookieFor(t, auth.SessionData{UserID: h.adminID, Role: user.RoleAdmin, MFAPassed: true})
	case asReporter:
		return h.sessionCookieFor(t, auth.SessionData{UserID: h.userID, Role: user.RoleUser, MFAPassed: true})
	}
	return h.sessionCookieFor(t, auth.SessionData{UserID: h.staffID, Role: user.RoleStaff, MFAPassed: true})
}

func (h *harness) doSession(t *testing.T, cookie, method, path string, body any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Cookie", cookie)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	h.srv.ServeHTTP(rr, req)
	return rr.Result()
}

func followPath(id any) string { return fmt.Sprintf("/api/v1/tickets/%v/follow", id) }

func (h *harness) followerIDs(t *testing.T, cookie string, id any) []string {
	t.Helper()
	res := h.doSession(t, cookie, http.MethodGet, fmt.Sprintf("/api/v1/tickets/%v/followers", id), nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	var list []struct {
		UserID      string `json:"user_id"`
		DisplayName string `json:"display_name"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&list))
	out := []string{}
	for _, f := range list {
		require.NotEmpty(t, f.DisplayName)
		out = append(out, f.UserID)
	}
	return out
}

func status(t *testing.T, res *http.Response) int {
	t.Helper()
	res.Body.Close()
	return res.StatusCode
}

// ── who can follow ───────────────────────────────────────────────────────────

func TestFollow_StaffAndAdminCanFollowAndUnfollow(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := newStaffTicket(t, h)

	for name, c := range map[string]struct {
		w  who
		id uuid.UUID
	}{"staff": {asStaff, h.staffID}, "admin": {asAdmin, h.adminID}} {
		t.Run(name, func(t *testing.T) {
			cookie := h.session(t, c.w)

			require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, cookie, http.MethodPut, followPath(tk.ID), nil)))
			require.Contains(t, h.followerIDs(t, cookie, tk.ID), c.id.String())

			// Idempotent both ways.
			require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, cookie, http.MethodPut, followPath(tk.ID), nil)))
			require.Equal(t, 1, countOf(h.followerIDs(t, cookie, tk.ID), c.id.String()))

			require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, cookie, http.MethodDelete, followPath(tk.ID), nil)))
			require.NotContains(t, h.followerIDs(t, cookie, tk.ID), c.id.String())
			require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, cookie, http.MethodDelete, followPath(tk.ID), nil)))
		})
	}
}

func countOf(ss []string, s string) int {
	n := 0
	for _, x := range ss {
		if x == s {
			n++
		}
	}
	return n
}

// A tracking number addresses the ticket everywhere else (#434); following is
// no different.
func TestFollow_AcceptsATrackingNumber(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := newStaffTicket(t, h)
	cookie := h.session(t, asStaff)

	require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, cookie, http.MethodPut, followPath(tk.TrackingNumber), nil)))
	require.Equal(t, []string{h.staffID.String()}, h.followerIDs(t, cookie, tk.TrackingNumber))
	require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, cookie, http.MethodDelete, followPath(tk.TrackingNumber), nil)))
	require.Empty(t, h.followerIDs(t, cookie, tk.ID))
}

// People only (#490): an API key, an OAuth client and a service user cannot
// follow, and nothing is stored when they try.
func TestFollow_PeopleOnly(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := newStaffTicket(t, h)
	c := h.newServiceClient(t, "Nagios", "tickets:read", "tickets:write")

	t.Run("an API key", func(t *testing.T) {
		for _, m := range []string{http.MethodPut, http.MethodDelete} {
			res := h.do(t, m, followPath(tk.ID), nil)
			var body struct {
				Error struct{ Code string }
			}
			require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
			res.Body.Close()
			require.Equal(t, http.StatusForbidden, res.StatusCode, m)
			require.Equal(t, "session_required", body.Error.Code)
		}
	})
	t.Run("an administrator's API key", func(t *testing.T) {
		require.Equal(t, http.StatusForbidden, status(t, h.doAsAdmin(t, http.MethodPut, followPath(tk.ID), nil)))
	})
	t.Run("an OAuth client", func(t *testing.T) {
		require.Equal(t, http.StatusForbidden, status(t, h.doBearer(t, c.Token, http.MethodPut, followPath(tk.ID), nil)))
		require.Equal(t, http.StatusForbidden, status(t, h.doBearer(t, c.Token, http.MethodDelete, followPath(tk.ID), nil)))
	})
	t.Run("a service user, even past the route", func(t *testing.T) {
		// The route is not the only door: the service refuses the account.
		err := h.ticketSvc.Follow(context.Background(), tk.ID, ticket.Actor{UserID: &c.UserID, Role: user.RoleStaff})
		require.ErrorIs(t, err, ticket.ErrForbidden)
	})

	require.Empty(t, h.followerIDs(t, h.session(t, asStaff), tk.ID))
}

func TestFollow_ARequesterCannotFollowOrSeeTheFollowers(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := newStaffTicket(t, h) // the reporting user's own ticket
	cookie := h.session(t, asReporter)

	require.Equal(t, http.StatusForbidden, status(t, h.doSession(t, cookie, http.MethodPut, followPath(tk.ID), nil)))
	require.Equal(t, http.StatusForbidden, status(t, h.doSession(t, cookie, http.MethodDelete, followPath(tk.ID), nil)))
	require.Equal(t, http.StatusForbidden, status(t, h.doSession(t, cookie, http.MethodGet,
		fmt.Sprintf("/api/v1/tickets/%s/followers", tk.ID), nil)))
}

// A follower must be able to see the ticket when following (#391). A ticket
// out of scope answers as one that does not exist.
func TestFollow_ATicketOutOfScopeIsNotFound(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := newStaffTicket(t, h)
	require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeyTicketScopeEnforced, true))
	cookie := h.session(t, asStaff)

	require.Equal(t, http.StatusNotFound, status(t, h.doSession(t, cookie, http.MethodPut, followPath(tk.ID), nil)))
	require.Equal(t, http.StatusNotFound, status(t, h.doSession(t, cookie, http.MethodGet,
		fmt.Sprintf("/api/v1/tickets/%s/followers", tk.ID), nil)))
	require.Equal(t, http.StatusNotFound, status(t, h.doSession(t, cookie, http.MethodPut, followPath(uuid.New()), nil)))
	// The administrator is not narrowed by scope.
	require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, h.session(t, asAdmin), http.MethodPut, followPath(tk.ID), nil)))
}

// Staff always see who is following.
func TestFollowers_StaffSeeTheCurrentList(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := newStaffTicket(t, h)
	staff, admin := h.session(t, asStaff), h.session(t, asAdmin)
	require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, admin, http.MethodPut, followPath(tk.ID), nil)))

	// Staff who do not follow see an administrator who does.
	require.Equal(t, []string{h.adminID.String()}, h.followerIDs(t, staff, tk.ID))
	// A read-only API key may read the list, too.
	res := h.do(t, http.MethodGet, fmt.Sprintf("/api/v1/tickets/%s/followers", tk.ID), nil)
	require.Equal(t, http.StatusOK, status(t, res))
}

// ── audit ────────────────────────────────────────────────────────────────────

func ticketAuditActions(t *testing.T, h *harness, cookie string, id uuid.UUID) []string {
	t.Helper()
	res := h.doSession(t, cookie, http.MethodGet, fmt.Sprintf("/api/v1/tickets/%s/audit", id), nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	var es []struct {
		Action    string `json:"action"`
		ActorName string `json:"actor_name"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&es))
	out := []string{}
	for _, e := range es {
		out = append(out, e.Action)
	}
	return out
}

func adminAuditActions(t *testing.T, h *harness, cookie string) (actions []string, total int) {
	t.Helper()
	res := h.doSession(t, cookie, http.MethodGet, "/api/v1/admin/audit?entity_type=ticket", nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	var body struct {
		Entries []struct {
			Action string `json:"action"`
		} `json:"entries"`
		Total int `json:"total"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	for _, e := range body.Entries {
		actions = append(actions, e.Action)
	}
	return actions, body.Total
}

func TestFollowAudit_IsWrittenAndVisibleByRole(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk := newStaffTicket(t, h)
	staff, adm, reporter := h.session(t, asStaff), h.session(t, asAdmin), h.session(t, asReporter)

	require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, staff, http.MethodPut, followPath(tk.ID), nil)))
	require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, staff, http.MethodPut, followPath(tk.ID), nil)), "a repeat")
	require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, staff, http.MethodDelete, followPath(tk.ID), nil)))

	// Admins see both entries, once each, in order.
	acts := ticketAuditActions(t, h, adm, tk.ID)
	require.Equal(t, []string{"followed", "unfollowed"}, onlyFollow(acts))
	// The entry names the person who followed.
	res := h.doSession(t, adm, http.MethodGet, fmt.Sprintf("/api/v1/tickets/%s/audit", tk.ID), nil)
	var es []struct {
		Action    string `json:"action"`
		ActorName string `json:"actor_name"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&es))
	res.Body.Close()
	for _, e := range es {
		if e.Action == "followed" {
			require.Equal(t, "Staff", e.ActorName)
		}
	}

	// Staff do not, by default: off until an administrator turns it on.
	require.Empty(t, onlyFollow(ticketAuditActions(t, h, staff, tk.ID)))
	// A requester gets no audit feed at all (403, since 2026-10-15), on or off.
	requireAuditRefused(t, h, reporter, tk.ID)

	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyStaffCanViewFollowAudit, true))
	require.Equal(t, []string{"followed", "unfollowed"}, onlyFollow(ticketAuditActions(t, h, staff, tk.ID)))
	requireAuditRefused(t, h, reporter, tk.ID) // the setting is for staff, not requesters
}

func requireAuditRefused(t *testing.T, h *harness, cookie string, id uuid.UUID) {
	t.Helper()
	res := h.doSession(t, cookie, http.MethodGet, fmt.Sprintf("/api/v1/tickets/%s/audit", id), nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusForbidden, res.StatusCode)
}

func onlyFollow(actions []string) []string {
	out := []string{}
	for _, a := range actions {
		if ticket.IsFollowAuditAction(a) {
			out = append(out, a)
		}
	}
	return out
}

// The admin-wide view follows the same rule, and its total agrees with its
// page: the entries are left out by the query, not removed afterwards.
func TestFollowAudit_AdminWideViewByRole(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk := newStaffTicket(t, h)
	staff, adm := h.session(t, asStaff), h.session(t, asAdmin)
	require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, staff, http.MethodPut, followPath(tk.ID), nil)))
	require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, staff, http.MethodDelete, followPath(tk.ID), nil)))

	adminActs, adminTotal := adminAuditActions(t, h, adm)
	require.Equal(t, []string{"followed", "unfollowed"}, sortedFollow(onlyFollow(adminActs)))

	staffActs, staffTotal := adminAuditActions(t, h, staff)
	require.Empty(t, onlyFollow(staffActs))
	require.Equal(t, adminTotal-2, staffTotal, "the count leaves them out too")

	// Asking for them by name does not get round it.
	res := h.doSession(t, staff, http.MethodGet, "/api/v1/admin/audit?action=followed", nil)
	var body struct {
		Entries []struct{} `json:"entries"`
		Total   int        `json:"total"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	res.Body.Close()
	require.Empty(t, body.Entries)
	require.Zero(t, body.Total)

	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyStaffCanViewFollowAudit, true))
	staffActs, staffTotal = adminAuditActions(t, h, staff)
	require.Equal(t, []string{"followed", "unfollowed"}, sortedFollow(onlyFollow(staffActs)))
	require.Equal(t, adminTotal, staffTotal)
}

func sortedFollow(a []string) []string {
	if len(a) == 2 && a[0] == "unfollowed" {
		return []string{a[1], a[0]}
	}
	return a
}

// ── the setting ──────────────────────────────────────────────────────────────

func TestFollowAuditSetting_OffByDefaultKnownAndGuarded(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	require.False(t, h.adminSvc.StaffCanViewFollowAudit(ctx), "off by default")
	require.True(t, admin.IsKnownKey(admin.KeyStaffCanViewFollowAudit))
	require.Contains(t, admin.KnownKeys(), "staff_can_view_follow_audit")

	adm := h.session(t, asAdmin)
	for name, tc := range map[string]struct {
		body map[string]any
		want int
	}{
		"on":        {map[string]any{"staff_can_view_follow_audit": true}, http.StatusNoContent},
		"off":       {map[string]any{"staff_can_view_follow_audit": false}, http.StatusNoContent},
		"null":      {map[string]any{"staff_can_view_follow_audit": nil}, http.StatusBadRequest},
		"as string": {map[string]any{"staff_can_view_follow_audit": "yes"}, http.StatusBadRequest},
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, status(t, h.doSession(t, adm, http.MethodPatch, "/api/v1/admin/settings", tc.body)))
		})
	}

	// It widens what staff see, so a leaked API key may not change it.
	require.Equal(t, http.StatusForbidden, status(t, h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{"staff_can_view_follow_audit": true})))
}

// ── mail ─────────────────────────────────────────────────────────────────────

func followAs(t *testing.T, h *harness, cookie string, tk ticket.Ticket) {
	t.Helper()
	require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, cookie, http.MethodPut, followPath(tk.ID), nil)))
}

func requesterReplies(t *testing.T, h *harness, tk ticket.Ticket) {
	t.Helper()
	res := h.doAsUser(t, http.MethodPost, fmt.Sprintf("/api/v1/tickets/%s/replies", tk.ID), map[string]any{"body": "hello"})
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)
}

func TestFollowers_AreMailedWhenTheRequesterReplies(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := newStaffTicket(t, h) // nobody holds it
	followAs(t, h, h.session(t, asStaff), tk)
	m := wireStaffMail(t, h)

	requesterReplies(t, h, tk)

	require.Equal(t, []string{"staff@test.local"}, m.recipients(t))
}

func TestFollowers_AreMailedWhenTheTicketIsAssigned(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := newStaffTicket(t, h)
	followAs(t, h, h.session(t, asStaff), tk)
	other := seedStaffMember(t, h, "other")
	m := wireStaffMail(t, h)

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), map[string]any{"assignee_user_id": other.ID})
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	require.ElementsMatch(t, []string{"staff@test.local", "other@test.local"}, m.recipients(t))
}

// Nobody is mailed about their own action, even a follower.
func TestFollowers_TheActorIsNotMailedAboutTheirOwnChange(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := newStaffTicket(t, h)
	staff := h.session(t, asStaff)
	followAs(t, h, staff, tk)
	m := wireStaffMail(t, h)

	// The follower assigns the ticket to somebody else; the assignee hears
	// about it, the follower who did it does not.
	other := seedStaffMember(t, h, "other")
	res := h.do(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), map[string]any{"assignee_user_id": other.ID})
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	require.Equal(t, []string{"other@test.local"}, m.recipients(t))
}

// One email per person per event, across the assignee, the group and the
// follower roles.
func TestFollowers_OneEmailPerPersonAcrossAssigneeGroupAndFollower(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk := newStaffTicket(t, h)
	g, err := h.groupSvc.Create(ctx, "Desk", "")
	require.NoError(t, err)
	require.NoError(t, h.groupSvc.AddMember(ctx, g.ID, h.staffID))
	_, err = h.ticketSvc.Assign(ctx, tk.ID, &h.staffID, &g.ID, adminActor(h))
	require.NoError(t, err)
	followAs(t, h, h.session(t, asStaff), tk) // assignee, group member and follower
	m := wireStaffMail(t, h)

	requesterReplies(t, h, tk)
	require.Equal(t, []string{"staff@test.local"}, m.recipients(t))

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(),
		map[string]any{"assignee_user_id": h.staffID, "assignee_group_id": g.ID})
	res.Body.Close()
	require.Equal(t, []string{"staff@test.local"}, m.recipients(t))
}

// Followers answer to the activity switch and the administrator's override.
func TestFollowers_ActivitySwitchAndOverride(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk := newStaffTicket(t, h)
	followAs(t, h, h.session(t, asStaff), tk)
	m := wireStaffMail(t, h)

	setPrefs(t, h, user.EmailPreferencesPatch{Activity: flag(false)})
	requesterReplies(t, h, tk)
	require.Empty(t, m.recipients(t), "the activity switch is off")

	require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyStaffEmailActivityOverride, "on"))
	requesterReplies(t, h, tk)
	require.Equal(t, []string{"staff@test.local"}, m.recipients(t), "forced on")

	setPrefs(t, h, user.EmailPreferencesPatch{Activity: flag(true)})
	require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyStaffEmailActivityOverride, "off"))
	requesterReplies(t, h, tk)
	require.Empty(t, m.recipients(t), "forced off")
}

// Mail is checked again when it is sent. A follower who can no longer see the
// ticket is skipped, and the row stays: it works again when scope returns.
func TestFollowers_ASendTimeScopeChangeSkipsTheMailAndKeepsTheRow(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk := newStaffTicket(t, h)
	staff := h.session(t, asStaff)
	followAs(t, h, staff, tk)
	m := wireStaffMail(t, h)

	requesterReplies(t, h, tk)
	// Scope enforcement is switched on before the worker runs.
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyTicketScopeEnforced, true))
	require.Empty(t, m.recipients(t), "they can no longer see the ticket")

	ids, err := h.ticketSvc.ListMailableFollowerIDs(ctx, tk.ID)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{h.staffID}, ids, "the follow is kept")

	// And it works again once enforcement is off.
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyTicketScopeEnforced, false))
	requesterReplies(t, h, tk)
	require.Equal(t, []string{"staff@test.local"}, m.recipients(t))
}

// A follower row for an account that cannot receive staff mail (a service user
// put there by other means, a disabled account) is never mailed.
func TestFollowers_ServiceUsersAndDisabledAccountsAreNeverMailed(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk := newStaffTicket(t, h)
	bot := uuid.New()
	_, err := h.tx.ExecContext(ctx, `INSERT INTO users (id, email, display_name, role, service_account)
		VALUES ($1, 'oauth-bot@service-account.invalid', 'Bot', 'staff', TRUE)`, bot)
	require.NoError(t, err)
	gone := seedStaffMember(t, h, "gone")
	for _, id := range []uuid.UUID{bot, gone.ID} {
		_, err := h.ticketStore.AddFollower(ctx, tk.ID, id)
		require.NoError(t, err)
	}
	require.NoError(t, h.userSvc.Disable(ctx, gone.ID))
	m := wireStaffMail(t, h)

	requesterReplies(t, h, tk)

	require.Empty(t, m.recipients(t))
}
