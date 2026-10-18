package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// The archived-node rule (#421, decided on #435): NOBODY may newly pick an
// archived category, type or item. Not when creating a ticket, not when
// reclassifying into one, whatever their role and whatever credential they
// hold. A ticket that is already in an archived node keeps it.
//
// This deliberately reverses the old staff exception, where staff and admins
// were allowed to file under archived nodes "because an old ticket belongs
// there". The rule is enforced once, in the ticket service, so REST, MCP and
// the guest form all obey it; these tests drive each of those doors.
//
// OAuth clients: they cannot create or reclassify tickets at all yet (the
// identity check refuses a credential with no user, before the classification
// is looked at), so there is nothing to refuse for them here. Add their rows
// when the OAuth service user (#434) lands.

// archivedTree is a catalogue with one retired node at each level. It is built
// with everything active so a test can file "old" tickets the way they really
// got there, then retire the nodes with archive().
type archivedTree struct {
	catArchived   uuid.UUID // an archived category, with an archived type and items beneath it
	typeUnderArc  uuid.UUID
	itemUnderArc  uuid.UUID
	itemUnderArc2 uuid.UUID // a second archived item under the same archived type

	catOpen     uuid.UUID // an active category
	typeOpen    uuid.UUID // an active type under it
	typeArch    uuid.UUID // an archived type under it
	itemOpen    uuid.UUID // an active item under typeOpen
	itemArch    uuid.UUID // an archived item under typeOpen
	itemUnderTA uuid.UUID // an active item under the archived type
}

func buildArchivedTree(t *testing.T, h *harness) archivedTree {
	t.Helper()
	ctx := context.Background()
	var a archivedTree

	c1, err := h.categorySvc.CreateCategory(ctx, "Retired cat", 80)
	require.NoError(t, err)
	t1, err := h.categorySvc.CreateType(ctx, c1.ID, "Type under retired cat", 1)
	require.NoError(t, err)
	i1, err := h.categorySvc.CreateItem(ctx, t1.ID, "Item under retired cat", 1)
	require.NoError(t, err)
	i1b, err := h.categorySvc.CreateItem(ctx, t1.ID, "Second item under retired cat", 2)
	require.NoError(t, err)
	a.catArchived, a.typeUnderArc, a.itemUnderArc, a.itemUnderArc2 = c1.ID, t1.ID, i1.ID, i1b.ID

	c2, err := h.categorySvc.CreateCategory(ctx, "Open cat", 81)
	require.NoError(t, err)
	tOpen, err := h.categorySvc.CreateType(ctx, c2.ID, "Open type", 1)
	require.NoError(t, err)
	tArch, err := h.categorySvc.CreateType(ctx, c2.ID, "Retired type", 2)
	require.NoError(t, err)
	iOpen, err := h.categorySvc.CreateItem(ctx, tOpen.ID, "Open item", 1)
	require.NoError(t, err)
	iArch, err := h.categorySvc.CreateItem(ctx, tOpen.ID, "Retired item", 2)
	require.NoError(t, err)
	iUnderTA, err := h.categorySvc.CreateItem(ctx, tArch.ID, "Item under retired type", 1)
	require.NoError(t, err)
	a.catOpen, a.typeOpen, a.typeArch = c2.ID, tOpen.ID, tArch.ID
	a.itemOpen, a.itemArch, a.itemUnderTA = iOpen.ID, iArch.ID, iUnderTA.ID
	return a
}

// archive retires the nodes the tree names as archived.
func (a archivedTree) archive(t *testing.T, h *harness) {
	t.Helper()
	ctx := context.Background()
	for _, id := range []uuid.UUID{a.itemUnderArc, a.itemUnderArc2, a.itemArch} {
		it, err := h.categorySvc.GetItem(ctx, id)
		require.NoError(t, err)
		it.Active = false
		require.NoError(t, h.categorySvc.UpdateItem(ctx, it))
	}
	for _, id := range []uuid.UUID{a.typeUnderArc, a.typeArch} {
		ty, err := h.categorySvc.GetType(ctx, id)
		require.NoError(t, err)
		ty.Active = false
		require.NoError(t, h.categorySvc.UpdateType(ctx, ty))
	}
	c, err := h.categorySvc.GetCategory(ctx, a.catArchived)
	require.NoError(t, err)
	c.Active = false
	require.NoError(t, h.categorySvc.UpdateCategory(ctx, c))
}

// classification is the body fields of a create or a reclassify.
type classification struct {
	level string // what is archived, for the failure message
	cat   uuid.UUID
	typ   *uuid.UUID
	item  *uuid.UUID
	want  string // the field the refusal must name
}

func (c classification) fields() map[string]any {
	m := map[string]any{"category_id": c.cat.String()}
	if c.typ != nil {
		m["type_id"] = c.typ.String()
	}
	if c.item != nil {
		m["item_id"] = c.item.String()
	}
	return m
}

func (a archivedTree) archivedPicks() []classification {
	return []classification{
		{"category", a.catArchived, nil, nil, "category_id"},
		{"type", a.catOpen, &a.typeArch, nil, "type_id"},
		{"item", a.catOpen, &a.typeOpen, &a.itemArch, "item_id"},
	}
}

// credential sends a request as one kind of caller.
type credential struct {
	name string
	send func(t *testing.T, method, path string, body any) (int, string)
}

func readStatusBody(t *testing.T, res *http.Response) (int, string) {
	t.Helper()
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return res.StatusCode, string(b)
}

func staffAndAdminCredentials(t *testing.T, h *harness) []credential {
	t.Helper()
	login := func(email string) *session {
		s := &session{h: h}
		res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/login",
			map[string]any{"email": email, "password": "password"})
		require.Equal(t, http.StatusOK, res.StatusCode, "login %s; body: %s", email, body)
		return s
	}
	viaSession := func(s *session) func(*testing.T, string, string, any) (int, string) {
		return func(t *testing.T, method, path string, body any) (int, string) {
			res, b := s.send(t, method, path, body)
			return res.StatusCode, string(b)
		}
	}
	return []credential{
		{"staff API key", func(t *testing.T, m, p string, b any) (int, string) {
			return readStatusBody(t, h.do(t, m, p, b))
		}},
		{"admin API key", func(t *testing.T, m, p string, b any) (int, string) {
			return readStatusBody(t, h.doAsAdmin(t, m, p, b))
		}},
		{"staff session", viaSession(login("staff@test.local"))},
		{"admin session", viaSession(login("admin@test.local"))},
	}
}

func TestArchivedRule_NobodyCanFileIntoAnArchivedNode(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tree := buildArchivedTree(t, h)
	tree.archive(t, h)

	for _, cred := range staffAndAdminCredentials(t, h) {
		for _, pick := range tree.archivedPicks() {
			t.Run(cred.name+" creating under an archived "+pick.level, func(t *testing.T) {
				body := pick.fields()
				body["subject"] = "Into the archive"
				body["description"] = "x"
				status, resp := cred.send(t, http.MethodPost, "/api/v1/tickets", body)
				require.Equal(t, http.StatusBadRequest, status, resp)
				require.Contains(t, resp, pick.want, "the refusal must say which id is the problem")
				require.Contains(t, resp, "not an active")
			})
		}
	}

	// And an active classification still works for every one of them, or the
	// rule would be satisfied by refusing everything.
	for _, cred := range staffAndAdminCredentials(t, h) {
		t.Run(cred.name+" creating under an active tree", func(t *testing.T) {
			status, resp := cred.send(t, http.MethodPost, "/api/v1/tickets", map[string]any{
				"subject": "Fine", "description": "x", "category_id": tree.catOpen.String(),
				"type_id": tree.typeOpen.String(), "item_id": tree.itemOpen.String(),
			})
			require.Equal(t, http.StatusCreated, status, resp)
		})
	}
}

func TestArchivedRule_NobodyCanReclassifyIntoAnArchivedNode(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tree := buildArchivedTree(t, h)
	tree.archive(t, h)

	for _, cred := range staffAndAdminCredentials(t, h) {
		for _, pick := range tree.archivedPicks() {
			t.Run(cred.name+" reclassifying into an archived "+pick.level, func(t *testing.T) {
				tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
					Subject: "Fine where it is", Description: "x", CategoryID: tree.catOpen,
					ReporterUserID: &h.userID,
				})
				require.NoError(t, err)

				status, resp := cred.send(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), pick.fields())
				require.Equal(t, http.StatusBadRequest, status, resp)
				require.Contains(t, resp, pick.want)
				require.Contains(t, resp, "not an active")

				after, err := h.ticketSvc.GetByID(ctx, tk.ID)
				require.NoError(t, err)
				require.Equal(t, tree.catOpen, after.CategoryID, "a refused reclassify must change nothing")
				require.Nil(t, after.TypeID)
				require.Nil(t, after.ItemID)
			})
		}
	}
}

// A ticket that is already in an archived node keeps it. Filing it there was
// legitimate when it happened; the rule is about newly picking one.
func TestArchivedRule_ATicketAlreadyThereKeepsIt(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tree := buildArchivedTree(t, h)
	// Filed while the nodes were still active, retired afterwards: how an old
	// ticket really ends up in an archived node.
	old, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Old ticket", Description: "x", CategoryID: tree.catArchived,
		TypeID: &tree.typeUnderArc, ItemID: &tree.itemUnderArc, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	tree.archive(t, h)

	path := "/api/v1/tickets/" + old.ID.String()
	for _, cred := range staffAndAdminCredentials(t, h) {
		t.Run(cred.name+" an unrelated edit", func(t *testing.T) {
			status, resp := cred.send(t, http.MethodPatch, path,
				map[string]any{"assignee_user_id": h.staffID.String()})
			require.Equal(t, http.StatusOK, status, resp)
			after, err := h.ticketSvc.GetByID(ctx, old.ID)
			require.NoError(t, err)
			require.Equal(t, old.CategoryID, after.CategoryID)
			require.Equal(t, old.TypeID, after.TypeID)
			require.Equal(t, old.ItemID, after.ItemID)
		})

		t.Run(cred.name+" a PATCH that repeats the same classification", func(t *testing.T) {
			status, resp := cred.send(t, http.MethodPatch, path, map[string]any{
				"category_id": old.CategoryID.String(), "type_id": old.TypeID.String(), "item_id": old.ItemID.String(),
			})
			require.Equal(t, http.StatusOK, status,
				"nothing new was picked, so nothing new may be refused; body: %s", resp)
		})
	}

	t.Run("a different archived item under the same archived type is a new pick", func(t *testing.T) {
		status, resp := readStatusBody(t, h.do(t, http.MethodPatch, path, map[string]any{
			"category_id": tree.catArchived.String(), "type_id": tree.typeUnderArc.String(),
			"item_id": tree.itemUnderArc2.String(),
		}))
		require.Equal(t, http.StatusBadRequest, status, resp)
		require.Contains(t, resp, "item_id")
	})

	t.Run("moving out of the archive into an active node works", func(t *testing.T) {
		status, resp := readStatusBody(t, h.do(t, http.MethodPatch, path, map[string]any{
			"category_id": tree.catOpen.String(), "type_id": tree.typeOpen.String(), "item_id": tree.itemOpen.String(),
		}))
		require.Equal(t, http.StatusOK, status, resp)
	})
}

// Only the level that changed is held to the rule: keeping an archived type
// while picking a new, active item under it is not a new pick of the type.
func TestArchivedRule_OnlyTheLevelThatChangedIsChecked(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tree := buildArchivedTree(t, h)
	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "In a type that was later retired", Description: "x", CategoryID: tree.catOpen,
		TypeID: &tree.typeArch, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	tree.archive(t, h)

	status, resp := readStatusBody(t, h.do(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), map[string]any{
		"category_id": tree.catOpen.String(), "type_id": tree.typeArch.String(),
		"item_id": tree.itemUnderTA.String(),
	}))
	require.Equal(t, http.StatusOK, status, resp)
}

// Follow-ups copy the classification of a closed ticket, so a follow-up of a
// ticket in an archived node still works, for staff over REST and over MCP.
func TestArchivedRule_AFollowUpCopiesTheArchivedClassification(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tree := buildArchivedTree(t, h)
	old, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Old ticket", Description: "x", CategoryID: tree.catArchived,
		TypeID: &tree.typeUnderArc, ItemID: &tree.itemUnderArc, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	require.NoError(t, h.ticketSvc.Close(ctx, old.ID, ticket.Actor{UserID: &h.adminID, Role: user.RoleAdmin}))
	tree.archive(t, h)

	t.Run("over REST", func(t *testing.T) {
		status, body := readStatusBody(t, h.do(t, http.MethodPost,
			"/api/v1/tickets/"+old.ID.String()+"/follow-up", map[string]any{}))
		require.Equal(t, http.StatusCreated, status, body)
		var created ticket.Ticket
		require.NoError(t, json.Unmarshal([]byte(body), &created))
		require.Equal(t, old.CategoryID, created.CategoryID)
		require.Equal(t, old.TypeID, created.TypeID)
		require.Equal(t, old.ItemID, created.ItemID)
	})

	t.Run("over MCP", func(t *testing.T) {
		// One follow-up per closed ticket is a requester rule; staff may open
		// several, so a second one from MCP is legitimate.
		c := openMCP(t, h, h.apiKey)
		defer c.closeBody()
		got := c.call(t, "create_follow_up", map[string]any{"ticket_id": old.ID.String()})
		require.NotContains(t, got, `"isError":true`, got)
		require.Contains(t, got, old.CategoryID.String())
	})
}

// MCP create_ticket and the guest form obey the same rule, from the same place.
func TestArchivedRule_MCPAndGuestAreRefusedToo(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tree := buildArchivedTree(t, h)
	tree.archive(t, h)

	c := openMCP(t, h, h.apiKey)
	defer c.closeBody()
	for _, pick := range tree.archivedPicks() {
		t.Run("MCP create_ticket, archived "+pick.level, func(t *testing.T) {
			args := pick.fields()
			args["subject"] = "Into the archive"
			got := c.call(t, "create_ticket", args)
			require.Contains(t, got, `"isError":true`, got)
			require.Contains(t, got, "not an active")
		})
	}

	t.Run("MCP create_ticket, active tree still works", func(t *testing.T) {
		got := c.call(t, "create_ticket", map[string]any{
			"subject": "Fine", "category_id": tree.catOpen.String(),
			"type_id": tree.typeOpen.String(), "item_id": tree.itemOpen.String(),
		})
		require.NotContains(t, got, `"isError":true`, got)
	})

	t.Run("guest form", func(t *testing.T) {
		enableGuests(t, h)
		res := h.doGuest(t, http.MethodPost, "/api/v1/guest/tickets", "", map[string]any{
			"subject": "Into the archive", "description": "x", "category_id": tree.catArchived.String(),
			"guest_email": "g@test.local", "guest_name": "Gus",
		})
		status, body := readStatusBody(t, res)
		require.Equal(t, http.StatusBadRequest, status, body)
	})

	t.Run("a reporting user", func(t *testing.T) {
		status, body := readStatusBody(t, h.doAsUser(t, http.MethodPost, "/api/v1/tickets", map[string]any{
			"subject": "Into the archive", "description": "x", "category_id": tree.catArchived.String(),
		}))
		require.Equal(t, http.StatusBadRequest, status, body)
	})
}

// The service itself, without HTTP: this is where the rule lives, so a caller
// that is neither REST nor MCP nor the guest form (the auto-close scheduler,
// a plugin, a future route) gets it too.
func TestArchivedRule_TheServiceEnforcesItForEveryCaller(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tree := buildArchivedTree(t, h)
	tree.archive(t, h)

	for _, pick := range tree.archivedPicks() {
		t.Run("Create, archived "+pick.level, func(t *testing.T) {
			_, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
				Subject: "x", Description: "x", CategoryID: pick.cat, TypeID: pick.typ, ItemID: pick.item,
				ReporterUserID: &h.staffID,
			})
			require.ErrorIs(t, err, ticket.ErrValidation)
			require.ErrorContains(t, err, pick.want)
		})
		t.Run("UpdateCTI, archived "+pick.level, func(t *testing.T) {
			tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
				Subject: "x", Description: "x", CategoryID: tree.catOpen, ReporterUserID: &h.staffID,
			})
			require.NoError(t, err)
			_, err = h.ticketSvc.UpdateCTI(ctx, tk.ID, pick.cat, pick.typ, pick.item, ticket.SystemActor)
			require.ErrorIs(t, err, ticket.ErrValidation)
			require.ErrorContains(t, err, pick.want)
		})
	}
}

// Staff still SEE archived rows (greyed out in the UI): the catalogue GETs are
// unchanged by this rule, only picking is.
func TestArchivedRule_StaffStillSeeArchivedRows(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tree := buildArchivedTree(t, h)
	tree.archive(t, h)
	for _, cred := range staffAndAdminCredentials(t, h) {
		t.Run(cred.name, func(t *testing.T) {
			status, body := cred.send(t, http.MethodGet, "/api/v1/categories", nil)
			require.Equal(t, http.StatusOK, status)
			require.Contains(t, body, tree.catArchived.String())
		})
	}
}

// A PATCH that carries a refused classification must change NOTHING. The
// assignee and status used to be applied first, so the caller got a 400 for a
// request that had half happened. The classification is validated before any
// of it is applied.
func TestArchivedRule_ARefusedPatchChangesNothing(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tree := buildArchivedTree(t, h)
	tree.archive(t, h)

	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Untouched", Description: "x", CategoryID: tree.catOpen, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)

	body := map[string]any{
		"assignee_user_id": h.staffID.String(),
		"status_id":        h.resolvedStatusID(t).String(),
		"category_id":      tree.catArchived.String(),
	}
	status, resp := readStatusBody(t, h.do(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), body))
	require.Equal(t, http.StatusBadRequest, status, resp)

	after, err := h.ticketSvc.GetByID(ctx, tk.ID)
	require.NoError(t, err)
	require.Nil(t, after.AssigneeUserID, "the assignment was applied although the request was refused")
	require.Equal(t, tk.StatusID, after.StatusID, "the status was changed although the request was refused")
	require.Equal(t, tree.catOpen, after.CategoryID)
}
