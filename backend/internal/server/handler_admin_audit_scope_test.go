package server_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
	"github.com/publiciallc/go-help-desk/backend/internal/server"
)

// Staff scope is applied by the audit query itself (#330), not by a walk in Go.
// There are two rules for "may this actor see this ticket" in this codebase —
// ticket.CanView, and the SQL predicate the listing and the audit query share —
// and the only thing that keeps them one rule is a test that runs both over the
// same data. This file is that test. If the SQL drifts from CanView, whichever
// way, TestAdminAudit_ScopeParity names the actor and the entry.

// parityRow is one planted audit entry and what the test needs to know to
// predict whether an actor sees it.
type parityRow struct {
	id         uuid.UUID
	entityType string
	entityID   uuid.UUID
	ticket     *ticket.Ticket // nil: no such ticket
	actorID    uuid.UUID
	at         time.Time
}

type parityWorld struct {
	rows []parityRow

	// One staff member for each way CanView can say yes, so no branch of it is
	// carried by an actor who would have been admitted by another.
	categoryScoped uuid.UUID // group scope over catA, every type
	typeScoped     uuid.UUID // group scope over one type of catB only
	bothScoped     uuid.UUID // both of the above, through two groups
	groupOnly      uuid.UUID // in a group with no scope rule at all
	noGroups       uuid.UUID // the harness staff user
	otherStaff     uuid.UUID // appears only as an assignee/reporter of tickets
	reporting      uuid.UUID
	admin          uuid.UUID
	keys           map[uuid.UUID]string
}

func (w *parityWorld) actors() map[string]uuid.UUID {
	return map[string]uuid.UUID{
		"category-scoped": w.categoryScoped, "type-scoped": w.typeScoped, "both": w.bothScoped,
		"group-only": w.groupOnly, "no-groups": w.noGroups, "other-staff": w.otherStaff,
	}
}

// keyFor mints an API key for an existing user, so a request can be made as them.
func keyFor(t *testing.T, h *harness, uid uuid.UUID) string {
	t.Helper()
	raw, hashed, err := auth.GenerateToken()
	require.NoError(t, err)
	require.NoError(t, h.authStore.CreateAPIKey(context.Background(), auth.APIKey{
		ID: uuid.New(), Name: "parity", HashedToken: hashed, UserID: uid,
		Scopes: allScopes(), CreatedAt: time.Now(),
	}))
	return raw
}

func (h *harness) doWithKey(t *testing.T, key, path string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "ApiKey "+key)
	rr := httptest.NewRecorder()
	h.srv.ServeHTTP(rr, req)
	return rr.Result()
}

const parityAction = "parity_fixture"

// buildParityWorld plants a matrix of tickets, groups, scope rules and audit
// entries covering every way ticket.CanView says yes or no.
func buildParityWorld(t *testing.T, h *harness) *parityWorld {
	t.Helper()
	ctx := context.Background()
	w := &parityWorld{keys: map[uuid.UUID]string{}}

	mkStaff := func(name string) uuid.UUID {
		u, err := h.userSvc.Create(ctx, user.CreateUserInput{
			Email: name + "@parity.local", DisplayName: name, Role: user.RoleStaff, Password: "password",
		})
		require.NoError(t, err)
		return u.ID
	}
	w.categoryScoped = mkStaff("category-scoped")
	w.typeScoped = mkStaff("type-scoped")
	w.bothScoped = mkStaff("both")
	w.groupOnly = mkStaff("group-only")
	w.otherStaff = mkStaff("other-staff")
	w.noGroups = h.staffID
	w.reporting = h.userID
	w.admin = h.adminID

	// Categories and types. Three categories so one can be covered by nobody.
	catA := h.catID
	catB, err := h.categorySvc.CreateCategory(ctx, "ParityB", 2)
	require.NoError(t, err)
	catC, err := h.categorySvc.CreateCategory(ctx, "ParityC", 3)
	require.NoError(t, err)
	mkType := func(cat uuid.UUID, name string) *uuid.UUID {
		tp, err := h.categorySvc.CreateType(ctx, cat, name, 1)
		require.NoError(t, err)
		return &tp.ID
	}
	typeA1, typeA2 := mkType(catA, "A1"), mkType(catA, "A2")
	typeB1, typeB2 := mkType(catB.ID, "B1"), mkType(catB.ID, "B2")

	// Groups and the rules each holds.
	mkGroup := func(name string, members ...uuid.UUID) uuid.UUID {
		g, err := h.groupSvc.Create(ctx, name, "")
		require.NoError(t, err)
		for _, m := range members {
			require.NoError(t, h.groupSvc.AddMember(ctx, g.ID, m))
		}
		return g.ID
	}
	gCat := mkGroup("parity-cat", w.categoryScoped, w.bothScoped)
	require.NoError(t, h.groupSvc.AddScope(ctx, group.GroupScope{GroupID: gCat, CategoryID: catA}))
	gType := mkGroup("parity-type", w.typeScoped, w.bothScoped)
	require.NoError(t, h.groupSvc.AddScope(ctx, group.GroupScope{GroupID: gType, CategoryID: catB.ID, TypeID: typeB1}))
	gBare := mkGroup("parity-bare", w.groupOnly) // a group, and no rule
	gNobody := mkGroup("parity-nobody")          // a group no staff member is in

	newSt, err := h.ticketStore.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)

	seq := int64(7000 + time.Now().UnixNano()%1000)
	mkTicket := func(cat uuid.UUID, typ *uuid.UUID, mod func(*ticket.Ticket)) *ticket.Ticket {
		seq++
		now := time.Now().UTC().Truncate(time.Millisecond)
		tk := ticket.Ticket{
			ID:             uuid.New(),
			TrackingNumber: ticket.GenerateTrackingNumber(ticket.DefaultTrackingPrefix, 2026, seq),
			Subject:        "parity", CategoryID: cat, TypeID: typ, Priority: ticket.PriorityLow,
			StatusID: newSt.ID, CreatedAt: now, UpdatedAt: now,
		}
		guest := "guest@parity.local"
		tk.GuestEmail = &guest // nobody's own ticket unless mod says so
		if mod != nil {
			mod(&tk)
		}
		require.NoError(t, h.ticketStore.Create(ctx, tk))
		return &tk
	}
	by := func(u uuid.UUID) func(*ticket.Ticket) {
		return func(tk *ticket.Ticket) { tk.ReporterUserID = &u }
	}
	to := func(u uuid.UUID) func(*ticket.Ticket) {
		return func(tk *ticket.Ticket) { tk.AssigneeUserID = &u }
	}
	toGroup := func(g uuid.UUID) func(*ticket.Ticket) {
		return func(tk *ticket.Ticket) { tk.AssigneeGroupID = &g }
	}

	var tickets []*ticket.Ticket
	// The scope rules, against every (category, type) shape they can meet.
	for _, c := range []struct {
		cat uuid.UUID
		typ *uuid.UUID
	}{
		{catA, nil}, {catA, typeA1}, {catA, typeA2},
		{catB.ID, nil}, {catB.ID, typeB1}, {catB.ID, typeB2},
		{catC.ID, nil},
	} {
		tickets = append(tickets, mkTicket(c.cat, c.typ, nil))
	}
	// Everything CanView allows besides a scope rule, in a category nobody
	// covers, so the relationship alone is what decides.
	for _, mod := range []func(*ticket.Ticket){
		by(w.noGroups), by(w.reporting), by(w.otherStaff),
		to(w.noGroups), to(w.categoryScoped), to(w.otherStaff),
		toGroup(gBare), toGroup(gCat), toGroup(gNobody),
	} {
		tickets = append(tickets, mkTicket(catC.ID, nil, mod))
	}

	// Entries: two per ticket, so a page boundary can fall between them; and
	// two sharing one microsecond, so the tiebreaker is exercised.
	base := time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)
	n := 0
	plant := func(entityType string, entityID uuid.UUID, tk *ticket.Ticket, at time.Time, actor uuid.UUID) {
		r := parityRow{id: uuid.New(), entityType: entityType, entityID: entityID, ticket: tk, actorID: actor, at: at}
		require.NoError(t, h.q.CreateAuditEntry(ctx, dbgen.CreateAuditEntryParams{
			ID: r.id, ActorID: uuid.NullUUID{UUID: actor, Valid: true},
			EntityType: entityType, EntityID: entityID, Action: parityAction, CreatedAt: at,
		}))
		w.rows = append(w.rows, r)
		n++
	}
	actorOf := func(i int) uuid.UUID {
		if i%2 == 0 {
			return w.admin
		}
		return w.noGroups
	}
	for _, tk := range tickets {
		at := base.Add(time.Duration(n) * time.Second)
		plant("ticket", tk.ID, tk, at, actorOf(n))
		plant("ticket", tk.ID, tk, at, actorOf(n)) // the same instant
	}
	// Entries for tickets that do not exist, and for another entity type whose
	// id happens to be a ticket's: neither is a ticket entry on a ticket.
	for i := 0; i < 3; i++ {
		plant("ticket", uuid.New(), nil, base.Add(time.Duration(n)*time.Second), actorOf(n))
	}
	for _, tk := range tickets {
		plant("user", tk.ID, nil, base.Add(time.Duration(n)*time.Second), actorOf(n))
	}

	for _, u := range []uuid.UUID{w.categoryScoped, w.typeScoped, w.bothScoped, w.groupOnly, w.noGroups, w.otherStaff, w.admin} {
		if u == h.staffID {
			w.keys[u] = h.apiKey
			continue
		}
		if u == h.adminID {
			w.keys[u] = h.adminKey
			continue
		}
		w.keys[u] = keyFor(t, h, u)
	}
	return w
}

// expected is the entries an actor should be shown, in order, derived ONLY
// from ticket.CanView (through the same Server method every other surface
// uses) — never from the query under test.
//
// Two rules sit outside CanView and are part of the endpoint's contract:
// staff never see a non-ticket entity; and an entry whose ticket does not
// exist is shown to nobody under scope enforcement, which is
// indistinguishable from "not yours" by design, and shown to everyone allowed
// the view when enforcement is off, where there is nothing to scope.
func (w *parityWorld) expected(t *testing.T, h *harness, actor uuid.UUID, role user.Role, enforced bool, keep func(parityRow) bool) []parityRow {
	t.Helper()
	a := &authmw.Actor{UserID: actor, Role: role}
	var out []parityRow
	for _, r := range w.rows {
		if keep != nil && !keep(r) {
			continue
		}
		visible := false
		switch {
		case role == user.RoleAdmin:
			visible = true
		case r.entityType != "ticket":
			visible = false
		case r.ticket == nil:
			visible = !enforced
		default:
			ok, err := h.srv.CanViewTicket(context.Background(), a, *r.ticket)
			require.NoError(t, err)
			visible = ok
		}
		if visible {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].at.Equal(out[j].at) {
			return out[i].at.After(out[j].at)
		}
		return out[i].id.String() > out[j].id.String()
	})
	return out
}

type parityPage struct {
	Entries []struct {
		ID string `json:"id"`
	} `json:"entries"`
	Total   *int `json:"total"`
	HasMore bool `json:"has_more"`
}

func ids(rows []parityRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.id.String()
	}
	return out
}

// The staff audit view is exactly what ticket.CanView admits, whatever the
// scope rules, the relationship to the ticket, the filters, or the page size.
func TestAdminAudit_ScopeParity(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	w := buildParityWorld(t, h)

	midpoint := w.rows[len(w.rows)/2].at
	filters := []struct {
		name  string
		query string
		keep  func(parityRow) bool
	}{
		{"action only", "", nil},
		{"actor", "&actor_id=" + w.admin.String(), func(r parityRow) bool { return r.actorID == w.admin }},
		{"from", "&from=" + midpoint.Format(time.RFC3339Nano), func(r parityRow) bool { return !r.at.Before(midpoint) }},
		{"to", "&to=" + midpoint.Format(time.RFC3339Nano), func(r parityRow) bool { return !r.at.After(midpoint) }},
		{"q on entity type", "&q=TICK", func(r parityRow) bool { return r.entityType == "ticket" }},
		// Staff are forced to ticket entries whatever they ask for.
		{"entity_type=user", "&entity_type=user", nil},
	}

	for _, enforced := range []bool{false, true} {
		if enforced {
			enableScope(t, h)
		}
		for name, actor := range w.actors() {
			for _, f := range filters {
				t.Run(fmt.Sprintf("enforced=%v/%s/%s", enforced, name, f.name), func(t *testing.T) {
					want := w.expected(t, h, actor, user.RoleStaff, enforced, f.keep)
					base := "/api/v1/admin/audit?action=" + parityAction + f.query

					// One request, whole answer.
					resp := h.doWithKey(t, w.keys[actor], base+"&limit=500")
					require.Equal(t, http.StatusOK, resp.StatusCode)
					var all parityPage
					decodeJSON(t, resp, &all)
					resp.Body.Close()
					got := make([]string, len(all.Entries))
					for i, e := range all.Entries {
						got[i] = e.ID
					}
					require.Equal(t, ids(want), got, "the staff view is not the set ticket.CanView admits")
					require.NotNil(t, all.Total, "staff are given the database's own count")
					require.Equal(t, len(want), *all.Total, "total disagrees with what the actor can see")
					require.False(t, all.HasMore)

					// The same answer assembled page by page: the pager follows
					// the same sequence the single read showed.
					paged := []string{}
					for offset := 0; offset < 100; offset += 3 {
						resp := h.doWithKey(t, w.keys[actor], fmt.Sprintf("%s&limit=3&offset=%d", base, offset))
						var p parityPage
						decodeJSON(t, resp, &p)
						resp.Body.Close()
						require.Equal(t, len(want), *p.Total)
						for _, e := range p.Entries {
							paged = append(paged, e.ID)
						}
						require.Equal(t, offset+len(p.Entries) < len(want), p.HasMore, "has_more at offset %d", offset)
						if !p.HasMore {
							break
						}
					}
					require.Equal(t, ids(want), paged)
				})
			}
		}
	}

	// Sanity: the matrix is not vacuous. Every actor must see something and
	// none may see everything, or the assertions above could pass on a world
	// where scope decides nothing.
	enableScope(t, h)
	var onTickets int
	for _, r := range w.rows {
		if r.entityType == "ticket" && r.ticket != nil {
			onTickets++
		}
	}
	for name, actor := range w.actors() {
		seen := len(w.expected(t, h, actor, user.RoleStaff, true, nil))
		require.Greater(t, seen, 0, "%s sees nothing, so nothing about them is being tested", name)
		require.Less(t, seen, onTickets, "%s sees everything, so scope decides nothing for them", name)
	}
}

// Administrators are never scoped, and see every entity type; a reporting user
// has no route at all.
func TestAdminAudit_ScopeParity_AdminAndReportingUser(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	w := buildParityWorld(t, h)

	for _, enforced := range []bool{false, true} {
		if enforced {
			enableScope(t, h)
		}
		want := w.expected(t, h, w.admin, user.RoleAdmin, enforced, nil)
		require.Greater(t, len(want), len(w.expected(t, h, w.noGroups, user.RoleStaff, enforced, nil)),
			"the fixture must give an administrator more than a staff member to prove admins are unscoped")

		resp := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/audit?action="+parityAction+"&limit=500", nil)
		var got parityPage
		decodeJSON(t, resp, &got)
		resp.Body.Close()
		out := make([]string, len(got.Entries))
		for i, e := range got.Entries {
			out[i] = e.ID
		}
		require.Equal(t, ids(want), out, "enforced=%v", enforced)
		require.Equal(t, len(want), *got.Total)

		resp = h.doAsUser(t, http.MethodGet, "/api/v1/admin/audit?action="+parityAction, nil)
		resp.Body.Close()
		require.Equal(t, http.StatusForbidden, resp.StatusCode, "enforced=%v", enforced)
	}
}

// A staff request is answered by one read of the audit log, however many
// tickets the entries span. #330 was a lookup per ticket; this is the shape of
// the fix, held in place.
func TestAdminAudit_StaffRequestIsOneRead(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	w := buildParityWorld(t, h)
	enableScope(t, h)

	counting := &countingAuditStore{Store: h.auditStore}
	withAuditStoreForTest(h, counting)

	resp := h.doWithKey(t, w.keys[w.bothScoped], "/api/v1/admin/audit?action="+parityAction+"&limit=500")
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, 1, counting.searches, "a staff request read the audit log more than once")
}

// countingAuditStore counts how many times the audit log is searched.
type countingAuditStore struct {
	audit.Store
	searches int
}

func (c *countingAuditStore) Search(ctx context.Context, f audit.Filter, limit, offset int) ([]audit.Entry, int, error) {
	c.searches++
	return c.Store.Search(ctx, f, limit, offset)
}

func withAuditStoreForTest(h *harness, s audit.Store) { server.WithAuditStore(s)(h.srv) }
