package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/database/adminstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/groupstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/slastore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
	"github.com/stretchr/testify/require"
)

// ── User store ───────────────────────────────────────────────────────────────

func TestUserStore_CreateAndGet(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	s := userstore.New(q)
	ctx := context.Background()

	u := user.User{
		ID:          uuid.New(),
		Email:       "alice@example.com",
		DisplayName: "Alice",
		Role:        user.RoleUser,
		CreatedAt:   time.Now().UTC().Truncate(time.Millisecond),
		UpdatedAt:   time.Now().UTC().Truncate(time.Millisecond),
	}
	require.NoError(t, s.Create(ctx, u))

	got, err := s.GetByID(ctx, u.ID)
	require.NoError(t, err)
	require.Equal(t, u.Email, got.Email)
	require.Equal(t, u.Role, got.Role)
}

func TestUserStore_GetByEmail(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	s := userstore.New(q)
	ctx := context.Background()

	u := user.User{
		ID:          uuid.New(),
		Email:       "bob@example.com",
		DisplayName: "Bob",
		Role:        user.RoleStaff,
		CreatedAt:   time.Now().UTC().Truncate(time.Millisecond),
		UpdatedAt:   time.Now().UTC().Truncate(time.Millisecond),
	}
	require.NoError(t, s.Create(ctx, u))

	got, err := s.GetByEmail(ctx, u.Email)
	require.NoError(t, err)
	require.Equal(t, u.ID, got.ID)
}

func TestUserStore_NotFound(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	s := userstore.New(q)
	_, err := s.GetByID(context.Background(), uuid.New())
	require.ErrorIs(t, err, userstore.ErrNotFound)
}

func TestUserStore_Update(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	s := userstore.New(q)
	ctx := context.Background()

	u := user.User{
		ID:          uuid.New(),
		Email:       "carol@example.com",
		DisplayName: "Carol",
		Role:        user.RoleUser,
		CreatedAt:   time.Now().UTC().Truncate(time.Millisecond),
		UpdatedAt:   time.Now().UTC().Truncate(time.Millisecond),
	}
	require.NoError(t, s.Create(ctx, u))

	u.DisplayName = "Carol Updated"
	u.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, s.Update(ctx, u))

	got, err := s.GetByID(ctx, u.ID)
	require.NoError(t, err)
	require.Equal(t, "Carol Updated", got.DisplayName)
}

// ── Category store ───────────────────────────────────────────────────────────

func TestCategoryStore_CreateAndList(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	s := categorystore.New(q)
	ctx := context.Background()

	cat := category.Category{
		ID:        uuid.New(),
		Name:      "Hardware",
		SortOrder: 1,
		Active:    true,
	}
	require.NoError(t, s.CreateCategory(ctx, cat))

	list, err := s.ListCategories(ctx, true)
	require.NoError(t, err)

	var found bool
	for _, c := range list {
		if c.ID == cat.ID {
			found = true
			require.Equal(t, "Hardware", c.Name)
		}
	}
	require.True(t, found, "created category not found in list")
}

func TestCategoryStore_TypeAndItem(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	s := categorystore.New(q)
	ctx := context.Background()

	cat := category.Category{ID: uuid.New(), Name: "Software", SortOrder: 1, Active: true}
	require.NoError(t, s.CreateCategory(ctx, cat))

	tp := category.Type{ID: uuid.New(), CategoryID: cat.ID, Name: "OS", SortOrder: 1, Active: true}
	require.NoError(t, s.CreateType(ctx, tp))

	it := category.Item{ID: uuid.New(), TypeID: tp.ID, Name: "Windows", SortOrder: 1, Active: true}
	require.NoError(t, s.CreateItem(ctx, it))

	types, err := s.ListTypes(ctx, cat.ID, true)
	require.NoError(t, err)
	require.Len(t, types, 1)

	items, err := s.ListItems(ctx, tp.ID, true)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "Windows", items[0].Name)
}

// ── Ticket store ─────────────────────────────────────────────────────────────

func TestTicketStore_CreateAndGet(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	ctx := context.Background()
	us := userstore.New(q)
	cs := categorystore.New(q)
	ts := ticketstore.New(q)

	// Prerequisite: a user and a category.
	reporter := user.User{
		ID:          uuid.New(),
		Email:       "reporter@example.com",
		DisplayName: "Reporter",
		Role:        user.RoleUser,
		CreatedAt:   time.Now().UTC().Truncate(time.Millisecond),
		UpdatedAt:   time.Now().UTC().Truncate(time.Millisecond),
	}
	require.NoError(t, us.Create(ctx, reporter))

	cat := category.Category{ID: uuid.New(), Name: "General", SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))

	// Look up the seeded "New" status ID.
	newSt, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)

	now := time.Now().UTC().Truncate(time.Millisecond)
	tk := ticket.Ticket{
		ID:             uuid.New(),
		TrackingNumber: ticket.GenerateTrackingNumber(2024, 1),
		Subject:        "Printer broken",
		Description:    "It won't print",
		CategoryID:     cat.ID,
		Priority:       ticket.PriorityMedium,
		StatusID:       newSt.ID,
		ReporterUserID: &reporter.ID,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	require.NoError(t, ts.Create(ctx, tk))

	got, err := ts.GetByID(ctx, tk.ID)
	require.NoError(t, err)
	require.Equal(t, tk.Subject, got.Subject)
	require.Equal(t, tk.TrackingNumber, got.TrackingNumber)

	gotByTN, err := ts.GetByTrackingNumber(ctx, tk.TrackingNumber)
	require.NoError(t, err)
	require.Equal(t, tk.ID, gotByTN.ID)
}

func TestTicketStore_Reply(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	ctx := context.Background()
	us := userstore.New(q)
	cs := categorystore.New(q)
	ts := ticketstore.New(q)

	reporter := user.User{
		ID: uuid.New(), Email: "rep2@example.com", DisplayName: "Rep",
		Role: user.RoleUser, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, reporter))

	cat := category.Category{ID: uuid.New(), Name: "Net", SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))

	newSt, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)

	now := time.Now().UTC().Truncate(time.Millisecond)
	tk := ticket.Ticket{
		ID:             uuid.New(),
		TrackingNumber: ticket.GenerateTrackingNumber(2024, 2),
		Subject:        "No internet",
		Description:    "Cannot connect",
		CategoryID:     cat.ID,
		Priority:       ticket.PriorityHigh,
		StatusID:       newSt.ID,
		ReporterUserID: &reporter.ID,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	require.NoError(t, ts.Create(ctx, tk))

	reply := ticket.Reply{
		ID:        uuid.New(),
		TicketID:  tk.ID,
		AuthorID:  &reporter.ID,
		Body:      "Have you tried turning it off and on again?",
		Internal:  false,
		CreatedAt: now,
	}
	require.NoError(t, ts.CreateReply(ctx, reply))

	replies, err := ts.ListReplies(ctx, tk.ID)
	require.NoError(t, err)
	require.Len(t, replies, 1)
	require.Equal(t, reply.Body, replies[0].Body)
}

func TestTicketStore_NotFound(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	ts := ticketstore.New(q)
	_, err := ts.GetByID(context.Background(), uuid.New())
	require.ErrorIs(t, err, ticketstore.ErrNotFound)
}

// TestTicketStore_ScopedListsAndCounts covers the admin-scope list helpers
// (ListAll / ListUnassigned and their Search variants) and the scoped status
// counts used to drive per-role dashboard numbers.
func TestTicketStore_ScopedListsAndCounts(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	ctx := context.Background()
	us := userstore.New(q)
	cs := categorystore.New(q)
	gs := groupstore.New(q)
	ts := ticketstore.New(q)

	now := time.Now().UTC().Truncate(time.Millisecond)

	reporter := user.User{ID: uuid.New(), Email: "r@ex.com", DisplayName: "R", Role: user.RoleUser, CreatedAt: now, UpdatedAt: now}
	staff := user.User{ID: uuid.New(), Email: "s@ex.com", DisplayName: "S", Role: user.RoleStaff, CreatedAt: now, UpdatedAt: now}
	require.NoError(t, us.Create(ctx, reporter))
	require.NoError(t, us.Create(ctx, staff))

	cat := category.Category{ID: uuid.New(), Name: "Gen", SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))

	grp := group.Group{ID: uuid.New(), Name: "Infra"}
	require.NoError(t, gs.Create(ctx, grp))

	newSt, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)

	mkTicket := func(subject string, assigneeUser *uuid.UUID, assigneeGroup *uuid.UUID) ticket.Ticket {
		seq, err := ts.NextSeq(ctx)
		require.NoError(t, err)
		tk := ticket.Ticket{
			ID:              uuid.New(),
			TrackingNumber:  ticket.GenerateTrackingNumber(2024, seq),
			Subject:         subject,
			Description:     subject + " body",
			CategoryID:      cat.ID,
			Priority:        ticket.PriorityMedium,
			StatusID:        newSt.ID,
			ReporterUserID:  &reporter.ID,
			AssigneeUserID:  assigneeUser,
			AssigneeGroupID: assigneeGroup,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		require.NoError(t, ts.Create(ctx, tk))
		return tk
	}

	mine := mkTicket("assigned to staff", &staff.ID, nil)
	grpTicket := mkTicket("assigned to group", nil, &grp.ID)
	_ = grpTicket
	unassigned1 := mkTicket("unassigned alpha", nil, nil)
	unassigned2 := mkTicket("unassigned beta", nil, nil)

	t.Run("ListAll returns every ticket", func(t *testing.T) {
		all, err := ts.ListAll(ctx, 100, 0)
		require.NoError(t, err)
		require.Len(t, all, 4)
	})

	t.Run("ListUnassigned returns only unassigned tickets", func(t *testing.T) {
		got, err := ts.ListUnassigned(ctx, 100, 0)
		require.NoError(t, err)
		require.Len(t, got, 2)
		ids := map[uuid.UUID]bool{}
		for _, tk := range got {
			ids[tk.ID] = true
		}
		require.True(t, ids[unassigned1.ID])
		require.True(t, ids[unassigned2.ID])
	})

	t.Run("SearchAll filters by subject", func(t *testing.T) {
		got, err := ts.SearchAll(ctx, "alpha", 100, 0)
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.Equal(t, unassigned1.ID, got[0].ID)
	})

	t.Run("SearchUnassigned filters and excludes assigned", func(t *testing.T) {
		got, err := ts.SearchUnassigned(ctx, "assigned", 100, 0)
		require.NoError(t, err)
		require.Len(t, got, 0)
	})

	t.Run("CountByStatusForAssignee counts user+group tickets", func(t *testing.T) {
		count, err := ts.CountByStatusForAssignee(ctx, newSt.ID, staff.ID, []uuid.UUID{grp.ID})
		require.NoError(t, err)
		require.Equal(t, int64(2), count)

		// Without the group: only direct assignment.
		count, err = ts.CountByStatusForAssignee(ctx, newSt.ID, staff.ID, nil)
		require.NoError(t, err)
		require.Equal(t, int64(1), count)

		// A user with nothing assigned sees zero.
		count, err = ts.CountByStatusForAssignee(ctx, newSt.ID, uuid.New(), nil)
		require.NoError(t, err)
		require.Equal(t, int64(0), count)
	})

	t.Run("CountByStatusForReporter counts only reported tickets", func(t *testing.T) {
		count, err := ts.CountByStatusForReporter(ctx, newSt.ID, reporter.ID)
		require.NoError(t, err)
		require.Equal(t, int64(4), count)

		count, err = ts.CountByStatusForReporter(ctx, newSt.ID, staff.ID)
		require.NoError(t, err)
		require.Equal(t, int64(0), count)
	})

	// Sanity: unscoped global count still reflects every ticket.
	total, err := ts.CountByStatus(ctx, newSt.ID)
	require.NoError(t, err)
	require.Equal(t, int64(4), total)
	_ = mine
}

// ── Group store ──────────────────────────────────────────────────────────────

func TestGroupStore(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	ctx := context.Background()
	gs := groupstore.New(q)
	cs := categorystore.New(q)

	// Create a group.
	g := group.Group{ID: uuid.New(), Name: "Infra", Description: "Infrastructure team"}
	require.NoError(t, gs.Create(ctx, g))

	got, err := gs.GetByID(ctx, g.ID)
	require.NoError(t, err)
	require.Equal(t, "Infra", got.Name)

	// Update the group.
	g.Description = "Updated desc"
	require.NoError(t, gs.Update(ctx, g))
	got, err = gs.GetByID(ctx, g.ID)
	require.NoError(t, err)
	require.Equal(t, "Updated desc", got.Description)

	// List.
	list, err := gs.List(ctx)
	require.NoError(t, err)
	var found bool
	for _, gr := range list {
		if gr.ID == g.ID {
			found = true
		}
	}
	require.True(t, found)

	// Members.
	us := userstore.New(q)
	member := user.User{
		ID: uuid.New(), Email: "member@example.com", DisplayName: "Member",
		Role: user.RoleStaff, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, member))

	require.NoError(t, gs.AddMember(ctx, g.ID, member.ID))
	members, err := gs.ListMembers(ctx, g.ID)
	require.NoError(t, err)
	require.Contains(t, members, member.ID)

	require.NoError(t, gs.RemoveMember(ctx, g.ID, member.ID))
	members, err = gs.ListMembers(ctx, g.ID)
	require.NoError(t, err)
	require.NotContains(t, members, member.ID)

	// Scopes (needs a real category due to FK constraint).
	cat := category.Category{ID: uuid.New(), Name: "ScopeTest", SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))

	sc := group.GroupScope{GroupID: g.ID, CategoryID: cat.ID, TypeID: nil}
	require.NoError(t, gs.AddScope(ctx, sc))

	scopes, err := gs.ListScopes(ctx, g.ID)
	require.NoError(t, err)
	require.Len(t, scopes, 1)
	require.Equal(t, cat.ID, scopes[0].CategoryID)

	inScope, err := gs.ListGroupsInScope(ctx, cat.ID, nil)
	require.NoError(t, err)
	var inScopeFound bool
	for _, gr := range inScope {
		if gr.ID == g.ID {
			inScopeFound = true
		}
	}
	require.True(t, inScopeFound)

	require.NoError(t, gs.RemoveScope(ctx, g.ID, cat.ID, nil))
	scopes, err = gs.ListScopes(ctx, g.ID)
	require.NoError(t, err)
	require.Empty(t, scopes)

	// Delete.
	require.NoError(t, gs.Delete(ctx, g.ID))
	_, err = gs.GetByID(ctx, g.ID)
	require.Error(t, err)
}

// ── SLA store ────────────────────────────────────────────────────────────────

func TestSLAStore_Policies(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	ctx := context.Background()
	ss := slastore.New(q)

	p := sla.Policy{
		ID:                  uuid.New(),
		Name:                "Standard",
		Priority:            ticket.PriorityMedium,
		ResponseTargetMin:   60,
		ResolutionTargetMin: 480,
	}
	require.NoError(t, ss.CreatePolicy(ctx, p))

	got, err := ss.GetPolicy(ctx, p.ID)
	require.NoError(t, err)
	require.Equal(t, "Standard", got.Name)

	p.Name = "Updated"
	require.NoError(t, ss.UpdatePolicy(ctx, p))
	got, err = ss.GetPolicy(ctx, p.ID)
	require.NoError(t, err)
	require.Equal(t, "Updated", got.Name)

	list, err := ss.ListPolicies(ctx)
	require.NoError(t, err)
	var found bool
	for _, pol := range list {
		if pol.ID == p.ID {
			found = true
		}
	}
	require.True(t, found)

	// FindPolicy — no match.
	match, err := ss.FindPolicy(ctx, ticket.PriorityLow, uuid.New())
	require.NoError(t, err)
	require.Nil(t, match)

	// FindPolicy — global match (nil CategoryID).
	got2, err := ss.FindPolicy(ctx, ticket.PriorityMedium, uuid.New())
	require.NoError(t, err)
	// May or may not match depending on priority — just verify no error.
	_ = got2

	require.NoError(t, ss.DeletePolicy(ctx, p.ID))
	_, err = ss.GetPolicy(ctx, p.ID)
	require.Error(t, err)
}

func TestSLAStore_Records(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	ctx := context.Background()
	ss := slastore.New(q)
	us := userstore.New(q)
	cs := categorystore.New(q)
	ts := ticketstore.New(q)

	// Seed user + category + ticket.
	u := user.User{
		ID: uuid.New(), Email: "sla@example.com", DisplayName: "SLA User",
		Role: user.RoleUser, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, u))

	cat := category.Category{ID: uuid.New(), Name: "SLACat", SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))

	newSt, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)

	now := time.Now().UTC().Truncate(time.Millisecond)
	tk := ticket.Ticket{
		ID:             uuid.New(),
		TrackingNumber: ticket.GenerateTrackingNumber(2025, 99),
		Subject:        "SLA test ticket",
		CategoryID:     cat.ID,
		Priority:       ticket.PriorityHigh,
		StatusID:       newSt.ID,
		ReporterUserID: &u.ID,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	require.NoError(t, ts.Create(ctx, tk))

	// Create a policy.
	p := sla.Policy{
		ID:                  uuid.New(),
		Name:                "SLATest",
		Priority:            ticket.PriorityHigh,
		ResponseTargetMin:   30,
		ResolutionTargetMin: 240,
	}
	require.NoError(t, ss.CreatePolicy(ctx, p))

	// Create record.
	rec := sla.Record{TicketID: tk.ID, PolicyID: p.ID}
	require.NoError(t, ss.CreateRecord(ctx, rec))

	got, err := ss.GetRecord(ctx, tk.ID)
	require.NoError(t, err)
	require.Equal(t, p.ID, got.PolicyID)
	require.Nil(t, got.FirstResponseAt)

	// Update record.
	firstResp := time.Now().UTC().Truncate(time.Millisecond)
	got.FirstResponseAt = &firstResp
	require.NoError(t, ss.UpdateRecord(ctx, got))

	updated, err := ss.GetRecord(ctx, tk.ID)
	require.NoError(t, err)
	require.NotNil(t, updated.FirstResponseAt)
}

// ── Admin store ──────────────────────────────────────────────────────────────

func TestAdminStore(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	ctx := context.Background()
	as := adminstore.New(q)

	// Set and get a value.
	require.NoError(t, as.Set(ctx, "test_key", []byte(`"hello"`)))
	got, err := as.Get(ctx, "test_key")
	require.NoError(t, err)
	require.Equal(t, []byte(`"hello"`), got)

	// Upsert (overwrite).
	require.NoError(t, as.Set(ctx, "test_key", []byte(`"world"`)))
	got, err = as.Get(ctx, "test_key")
	require.NoError(t, err)
	require.Equal(t, []byte(`"world"`), got)

	// Missing key returns error.
	_, err = as.Get(ctx, "nonexistent_key")
	require.Error(t, err)

	// List.
	all, err := as.List(ctx)
	require.NoError(t, err)
	require.Contains(t, all, "test_key")
}

// ── Auth store ───────────────────────────────────────────────────────────────

func TestAuthStore_APIKeys(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	ctx := context.Background()
	as := authstore.New(q)
	us := userstore.New(q)

	// Seed a user (FK).
	u := user.User{
		ID: uuid.New(), Email: "apikey@example.com", DisplayName: "APIKey User",
		Role: user.RoleStaff, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, u))

	raw, hashed, err := auth.GenerateToken()
	require.NoError(t, err)
	_ = raw

	k := auth.APIKey{
		ID:          uuid.New(),
		Name:        "test-key",
		HashedToken: hashed,
		UserID:      u.ID,
		Scopes:      []string{"*"},
		CreatedAt:   time.Now().UTC().Truncate(time.Millisecond),
	}
	require.NoError(t, as.CreateAPIKey(ctx, k))

	got, err := as.GetByHash(ctx, hashed)
	require.NoError(t, err)
	require.Equal(t, k.ID, got.ID)
	require.Equal(t, k.Name, got.Name)

	// UpdateLastUsed.
	now := time.Now().UTC()
	require.NoError(t, as.UpdateLastUsed(ctx, k.ID, now))

	list, err := as.ListByUser(ctx, u.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, k.ID, list[0].ID)

	require.NoError(t, as.Delete(ctx, k.ID))
	_, err = as.GetByHash(ctx, hashed)
	require.Error(t, err)
}

func TestAuthStore_OAuthClients(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	ctx := context.Background()
	as := authstore.New(q)

	_, secret, err := auth.GenerateToken()
	require.NoError(t, err)

	c := auth.OAuthClient{
		ID:           uuid.New(),
		ClientID:     "client-test-001",
		HashedSecret: secret,
		Name:         "Test App",
		Scopes:       []string{"tickets:read"},
		CreatedAt:    time.Now().UTC().Truncate(time.Millisecond),
	}
	require.NoError(t, as.CreateOAuthClient(ctx, c))

	got, err := as.GetByClientID(ctx, c.ClientID)
	require.NoError(t, err)
	require.Equal(t, c.ID, got.ID)

	list, err := as.ListOAuthClients(ctx)
	require.NoError(t, err)
	var found bool
	for _, cl := range list {
		if cl.ID == c.ID {
			found = true
		}
	}
	require.True(t, found)

	require.NoError(t, as.DeleteOAuthClient(ctx, c.ID))
	_, err = as.GetByClientID(ctx, c.ClientID)
	require.Error(t, err)
}

func TestAuthStore_Webhooks(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	ctx := context.Background()
	as := authstore.New(q)

	wh := authstore.WebhookConfig{
		ID:        uuid.New(),
		URL:       "https://example.com/hook",
		Events:    []string{"ticket.created"},
		Secret:    "hmac-secret",
		Enabled:   true,
		CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
	require.NoError(t, as.CreateWebhook(ctx, wh))

	got, err := as.GetWebhook(ctx, wh.ID)
	require.NoError(t, err)
	require.Equal(t, wh.URL, got.URL)

	wh.URL = "https://example.com/hook2"
	require.NoError(t, as.UpdateWebhook(ctx, wh))
	got, err = as.GetWebhook(ctx, wh.ID)
	require.NoError(t, err)
	require.Equal(t, "https://example.com/hook2", got.URL)

	list, err := as.ListEnabledWebhooks(ctx)
	require.NoError(t, err)
	var found bool
	for _, w := range list {
		if w.ID == wh.ID {
			found = true
		}
	}
	require.True(t, found)

	require.NoError(t, as.DeleteWebhook(ctx, wh.ID))
	_, err = as.GetWebhook(ctx, wh.ID)
	require.Error(t, err)
}

// ── Audit store ──────────────────────────────────────────────────────────────

func TestAuditStore(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	ctx := context.Background()
	aus := auditstore.New(q)

	entityID := uuid.New()
	e := audit.Entry{
		ID:         uuid.New(),
		ActorID:    nil,
		EntityType: "ticket",
		EntityID:   entityID,
		Action:     "created",
		After:      map[string]any{"subject": "Test"},
	}
	require.NoError(t, aus.Create(ctx, e))

	// Second entry for pagination test.
	e2 := audit.Entry{
		ID:         uuid.New(),
		ActorID:    nil,
		EntityType: "ticket",
		EntityID:   entityID,
		Action:     "status_changed",
	}
	require.NoError(t, aus.Create(ctx, e2))

	entries, err := aus.ListByEntity(ctx, "ticket", entityID, 10, 0)
	require.NoError(t, err)
	require.Len(t, entries, 2)

	// Pagination: offset 1 returns only 1 entry.
	entries, err = aus.ListByEntity(ctx, "ticket", entityID, 10, 1)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}
