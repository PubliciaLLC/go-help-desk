package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Staff and administrators add other people as followers, and remove any
// follower (#695). Real Postgres: who can see a ticket under scope is a SQL
// predicate, and a fake would only agree with itself.

func followerPath(ticketID any, userID uuid.UUID) string {
	return fmt.Sprintf("/api/v1/tickets/%v/followers/%s", ticketID, userID)
}

func (h *harness) sessionOf(t *testing.T, u user.User) string {
	t.Helper()
	return h.sessionCookieFor(t, auth.SessionData{UserID: u.ID, Role: u.Role, MFAPassed: true})
}

type apiErr struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// refusal decodes the JSON error body of a response and closes it.
func refusal(t *testing.T, res *http.Response) (int, apiErr) {
	t.Helper()
	defer res.Body.Close()
	var e apiErr
	_ = json.NewDecoder(res.Body).Decode(&e)
	return res.StatusCode, e
}

type followAuditEntry struct {
	Action    string                           `json:"action"`
	ActorID   string                           `json:"actor_id"`
	ActorName string                           `json:"actor_name"`
	Before    map[string]any                   `json:"before"`
	After     map[string]any                   `json:"after"`
	Names     map[string]struct{ Name string } `json:"names"`
}

func followEntries(t *testing.T, h *harness, cookie string, id uuid.UUID) []followAuditEntry {
	t.Helper()
	res := h.doSession(t, cookie, http.MethodGet, fmt.Sprintf("/api/v1/tickets/%s/audit", id), nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	var all []followAuditEntry
	require.NoError(t, json.NewDecoder(res.Body).Decode(&all))
	var out []followAuditEntry
	for _, e := range all {
		if e.Action == "followed" || e.Action == "unfollowed" {
			out = append(out, e)
		}
	}
	return out
}

// ── who may add whom ─────────────────────────────────────────────────────────

func TestAddFollower_WhoMayAddWhom(t *testing.T) {
	cases := []struct {
		name   string
		caller who
		target func(h *harness) user.User
	}{
		{"staff adds staff", asStaff, func(h *harness) user.User { return seedStaffMember(t, h, "colleague") }},
		{"admin adds staff", asAdmin, func(h *harness) user.User { return seedStaffMember(t, h, "colleague") }},
		{"staff adds an administrator", asStaff, func(h *harness) user.User {
			u, err := h.userSvc.GetByID(context.Background(), h.adminID)
			require.NoError(t, err)
			return u
		}},
		{"admin adds an administrator", asAdmin, func(h *harness) user.User {
			u, err := h.userSvc.Create(context.Background(), user.CreateUserInput{
				Email: "admin2@test.local", DisplayName: "Admin Two", Role: user.RoleAdmin, Password: "password"})
			require.NoError(t, err)
			return u
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			tk := newStaffTicket(t, h)
			target := tc.target(h)
			cookie := h.session(t, tc.caller)

			require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, cookie, http.MethodPut, followerPath(tk.ID, target.ID), nil)))

			require.Equal(t, []string{target.ID.String()}, h.followerIDs(t, cookie, tk.ID))
		})
	}
}

func TestAddFollower_AcceptsATrackingNumberAndIsIdempotent(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := newStaffTicket(t, h)
	other := seedStaffMember(t, h, "colleague")
	cookie := h.session(t, asStaff)

	for i := 0; i < 2; i++ {
		require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, cookie, http.MethodPut, followerPath(tk.TrackingNumber, other.ID), nil)))
	}

	require.Equal(t, []string{other.ID.String()}, h.followerIDs(t, cookie, tk.ID))
	require.Len(t, followEntries(t, h, h.session(t, asAdmin), tk.ID), 1, "the repeat is not a second entry")
}

func TestAddFollower_ARequesterCannotAddOrRemove(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := newStaffTicket(t, h) // the reporting user's own ticket
	other := seedStaffMember(t, h, "colleague")
	require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, h.session(t, asStaff), http.MethodPut, followerPath(tk.ID, other.ID), nil)))
	cookie := h.session(t, asReporter)

	require.Equal(t, http.StatusForbidden, status(t, h.doSession(t, cookie, http.MethodPut, followerPath(tk.ID, h.staffID), nil)))
	require.Equal(t, http.StatusForbidden, status(t, h.doSession(t, cookie, http.MethodDelete, followerPath(tk.ID, other.ID), nil)))
	require.Equal(t, []string{other.ID.String()}, h.followerIDs(t, h.session(t, asStaff), tk.ID))
}

// People only (D20): an API key and an OAuth client are refused, an
// administrator's key included, and nothing is stored.
func TestAddFollower_PeopleOnly(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := newStaffTicket(t, h)
	other := seedStaffMember(t, h, "colleague")
	c := h.newServiceClient(t, "Nagios", "tickets:read", "tickets:write")
	require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, h.session(t, asStaff), http.MethodPut, followerPath(tk.ID, h.adminID), nil)))

	for name, do := range map[string]func(method string) *http.Response{
		"an API key":         func(m string) *http.Response { return h.do(t, m, followerPath(tk.ID, other.ID), nil) },
		"an admin's API key": func(m string) *http.Response { return h.doAsAdmin(t, m, followerPath(tk.ID, h.adminID), nil) },
		"an OAuth client":    func(m string) *http.Response { return h.doBearer(t, c.Token, m, followerPath(tk.ID, other.ID), nil) },
	} {
		for _, m := range []string{http.MethodPut, http.MethodDelete} {
			code, e := refusal(t, do(m))
			require.Equal(t, http.StatusForbidden, code, name+" "+m)
			require.Equal(t, "session_required", e.Error.Code, name+" "+m)
		}
	}
	require.Equal(t, []string{h.adminID.String()}, h.followerIDs(t, h.session(t, asStaff), tk.ID), "nothing was added or removed")
}

// ── who may be added ─────────────────────────────────────────────────────────

func TestAddFollower_TheTargetMustBeAnActiveStaffOrAdmin(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk := newStaffTicket(t, h)
	c := h.newServiceClient(t, "Nagios", "tickets:read")
	disabled := seedStaffMember(t, h, "disabled")
	require.NoError(t, h.userSvc.Disable(ctx, disabled.ID))
	deleted := seedStaffMember(t, h, "deleted")
	require.NoError(t, h.userSvc.SoftDelete(ctx, deleted.ID))
	requester, err := h.userSvc.Create(ctx, user.CreateUserInput{Email: "req2@test.local", DisplayName: "Req Two", Role: user.RoleUser, Password: "password"})
	require.NoError(t, err)
	cookie := h.session(t, asStaff)

	cases := map[string]uuid.UUID{
		"a requester":                requester.ID,
		"the ticket's own requester": h.userID,
		"a disabled account":         disabled.ID,
		"a deleted account":          deleted.ID,
		"a service account":          c.UserID,
		"an id that is nobody":       uuid.New(),
	}
	for name, id := range cases {
		t.Run(name, func(t *testing.T) {
			code, e := refusal(t, h.doSession(t, cookie, http.MethodPut, followerPath(tk.ID, id), nil))
			require.Equal(t, http.StatusUnprocessableEntity, code)
			require.Equal(t, "follower_not_eligible", e.Error.Code)
		})
	}
	require.Empty(t, h.followerIDs(t, cookie, tk.ID))
	require.Empty(t, followEntries(t, h, h.session(t, asAdmin), tk.ID), "a refusal writes no audit entry")
}

func TestAddFollower_ABadUserIDIsABadRequest(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := newStaffTicket(t, h)
	res := h.doSession(t, h.session(t, asStaff), http.MethodPut, fmt.Sprintf("/api/v1/tickets/%s/followers/not-a-uuid", tk.ID), nil)
	code, _ := refusal(t, res)
	require.Equal(t, http.StatusBadRequest, code)
}

// The person added must be able to see the ticket under scope (#391); the
// caller is checked by the route gate as for everything under /tickets/{id}.
func TestAddFollower_TheTargetMustBeAbleToSeeTheTicketUnderScope(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk := newStaffTicket(t, h)
	other := seedStaffMember(t, h, "colleague") // in no group
	enableScope(t, h)
	adm := h.session(t, asAdmin) // sees everything, so the gate passes

	code, e := refusal(t, h.doSession(t, adm, http.MethodPut, followerPath(tk.ID, other.ID), nil))
	require.Equal(t, http.StatusUnprocessableEntity, code)
	require.Equal(t, "follower_cannot_view_ticket", e.Error.Code)
	require.Empty(t, h.followerIDs(t, adm, tk.ID))

	// Admins are never scoped.
	require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, adm, http.MethodPut, followerPath(tk.ID, h.adminID), nil)))

	// A group covering the ticket's category brings the colleague into scope.
	g, err := h.groupSvc.Create(ctx, "Tier 1", "")
	require.NoError(t, err)
	require.NoError(t, h.groupSvc.AddMember(ctx, g.ID, other.ID))
	require.NoError(t, h.groupSvc.AddScope(ctx, group.GroupScope{GroupID: g.ID, CategoryID: h.catID}))
	require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, adm, http.MethodPut, followerPath(tk.ID, other.ID), nil)))
	require.ElementsMatch(t, []string{h.adminID.String(), other.ID.String()}, h.followerIDs(t, adm, tk.ID))
}

// Staff in scope who add a colleague out of scope are refused; the staff
// caller's own gate (404 for a ticket they cannot see) is unchanged.
func TestAddFollower_UnderScopeStaffCannotAddSomeoneWhoCannotSee(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk := newStaffTicket(t, h)
	other := seedStaffMember(t, h, "colleague")
	enableScope(t, h)
	staff := h.session(t, asStaff)

	// The caller cannot see the ticket: 404, whatever the target.
	require.Equal(t, http.StatusNotFound, status(t, h.doSession(t, staff, http.MethodPut, followerPath(tk.ID, other.ID), nil)))
	require.Equal(t, http.StatusNotFound, status(t, h.doSession(t, staff, http.MethodDelete, followerPath(tk.ID, other.ID), nil)))

	// Now the caller can (assigned), the colleague still cannot.
	_, err := h.ticketSvc.Assign(ctx, tk.ID, &h.staffID, nil, adminActor(h))
	require.NoError(t, err)
	code, e := refusal(t, h.doSession(t, staff, http.MethodPut, followerPath(tk.ID, other.ID), nil))
	require.Equal(t, http.StatusUnprocessableEntity, code)
	require.Equal(t, "follower_cannot_view_ticket", e.Error.Code)
}

// ── removing ─────────────────────────────────────────────────────────────────

func TestRemoveFollower_AnyFollowerByAnyStaffAndIdempotent(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := newStaffTicket(t, h)
	a, b := seedStaffMember(t, h, "ann"), seedStaffMember(t, h, "bob")
	staff, adm := h.session(t, asStaff), h.session(t, asAdmin)
	// Ann followed herself; Bob was added by the admin.
	require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, h.sessionOf(t, a), http.MethodPut, followPath(tk.ID), nil)))
	require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, adm, http.MethodPut, followerPath(tk.ID, b.ID), nil)))

	// Staff remove both, though they added neither.
	require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, staff, http.MethodDelete, followerPath(tk.ID, a.ID), nil)))
	require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, staff, http.MethodDelete, followerPath(tk.ID, b.ID), nil)))
	require.Empty(t, h.followerIDs(t, staff, tk.ID))

	// Someone who does not follow, and an id that is nobody: still 204.
	require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, staff, http.MethodDelete, followerPath(tk.ID, b.ID), nil)))
	require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, staff, http.MethodDelete, followerPath(tk.ID, uuid.New()), nil)))
	// Removing yourself by this route is the same as DELETE /follow.
	require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, staff, http.MethodDelete, followerPath(tk.ID, h.staffID), nil)))

	var actions []string
	for _, e := range followEntries(t, h, adm, tk.ID) {
		actions = append(actions, e.Action)
	}
	require.Equal(t, []string{"followed", "followed", "unfollowed", "unfollowed"}, actions, "only real changes are audited")
}

// ── audit ────────────────────────────────────────────────────────────────────

func TestAddFollower_AuditNamesTheAdderAndTheAdded(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk := newStaffTicket(t, h)
	other := seedStaffMember(t, h, "colleague")
	staff, adm := h.session(t, asStaff), h.session(t, asAdmin)
	require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, staff, http.MethodPut, followerPath(tk.ID, other.ID), nil)))
	// Admin takes them off again; and staff follows for themselves.
	require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, adm, http.MethodDelete, followerPath(tk.ID, other.ID), nil)))
	require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, staff, http.MethodPut, followPath(tk.ID), nil)))

	es := followEntries(t, h, adm, tk.ID)
	require.Len(t, es, 3)

	added := es[0]
	require.Equal(t, "followed", added.Action)
	require.Equal(t, h.staffID.String(), added.ActorID, "the adder is the actor")
	require.Equal(t, other.ID.String(), added.After["follower_user_id"])
	require.Equal(t, "colleague", added.Names[other.ID.String()].Name, "the added person is named, not just an id")

	removed := es[1]
	require.Equal(t, "unfollowed", removed.Action)
	require.Equal(t, h.adminID.String(), removed.ActorID, "the remover is the actor")
	require.Equal(t, other.ID.String(), removed.Before["follower_user_id"])

	self := es[2]
	require.Equal(t, "followed", self.Action)
	require.NotContains(t, self.After, "follower_user_id", "a self follow keeps today's shape")

	// Staff who may see follow entries but not field-level history are told
	// whom the entry was about: it is an id on the allow-list, not content.
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyStaffCanViewFollowAudit, true))
	require.False(t, h.adminSvc.StaffCanViewTicketChangeHistory(ctx))
	got := followEntries(t, h, staff, tk.ID)
	require.Len(t, got, 3)
	require.Equal(t, other.ID.String(), got[0].After["follower_user_id"])
	require.Equal(t, "colleague", got[0].Names[other.ID.String()].Name)
	require.NotContains(t, got[0].After, "subject")

	// Staff without the setting see no follow entries at all, as before.
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyStaffCanViewFollowAudit, false))
	require.Empty(t, followEntries(t, h, staff, tk.ID))
}

// ── mail and events ──────────────────────────────────────────────────────────

func TestAddFollower_NoMailOnBeingAdded_ThenOrdinaryFollowerMail(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := newStaffTicket(t, h)
	other := seedStaffMember(t, h, "colleague")
	m := wireStaffMail(t, h)

	require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, h.session(t, asStaff), http.MethodPut, followerPath(tk.ID, other.ID), nil)))
	require.Empty(t, m.recipients(t), "nobody is mailed that somebody was added")

	requesterReplies(t, h, tk)
	require.Equal(t, []string{"colleague@test.local"}, m.recipients(t), "after that they hear as any follower does")

	// The adder is not a follower by adding someone else.
	ids, err := h.ticketSvc.ListMailableFollowerIDs(context.Background(), tk.ID)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{other.ID}, ids)
}

func TestAddFollower_WebhookEventCarriesTheAdderAsActorAndTheAddedAsUserID(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := g4Ticket(t, h, "Followed")
	other := seedStaffMember(t, h, "colleague")
	h.dispatcher.record = true

	res := h.doSession(t, h.session(t, asAdmin), http.MethodPut, followerPath(tk.ID, other.ID), nil)
	require.Equal(t, http.StatusNoContent, status(t, res))

	evs := g4Queued(h, notification.EventTicketFollowed)
	require.Len(t, evs, 1)
	require.Equal(t, &h.adminID, evs[0].ActorID)
	require.Equal(t, other.ID, evs[0].Payload["user_id"])
	require.False(t, evs[0].MailsStaff())

	res = h.doSession(t, h.session(t, asStaff), http.MethodDelete, followerPath(tk.ID, other.ID), nil)
	require.Equal(t, http.StatusNoContent, status(t, res))
	evs = g4Queued(h, notification.EventTicketUnfollowed)
	require.Len(t, evs, 1)
	require.Equal(t, &h.staffID, evs[0].ActorID)
	require.Equal(t, other.ID, evs[0].Payload["user_id"])
}

// ── the list ─────────────────────────────────────────────────────────────────

// The list a staff member reads is what the panel draws: who, and when. It
// says nothing about who added them (that is in the audit).
func TestFollowers_ListShapeIsUnchanged(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := newStaffTicket(t, h)
	other := seedStaffMember(t, h, "colleague")
	cookie := h.session(t, asStaff)
	require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, cookie, http.MethodPut, followerPath(tk.ID, other.ID), nil)))

	res := h.doSession(t, cookie, http.MethodGet, fmt.Sprintf("/api/v1/tickets/%s/followers", tk.ID), nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	var raw []map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&raw))
	require.Len(t, raw, 1)
	keys := []string{}
	for k := range raw[0] {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	require.Equal(t, []string{"created_at", "display_name", "user_id"}, keys)
	require.Equal(t, "colleague", raw[0]["display_name"])
}
