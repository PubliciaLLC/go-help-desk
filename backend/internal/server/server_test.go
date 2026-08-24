package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/sessions"
	"github.com/pquerna/otp/totp"
	"github.com/publiciallc/go-help-desk/backend/internal/config"
	"github.com/publiciallc/go-help-desk/backend/internal/database/adminstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/cannedresponsestore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/customfieldstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/groupstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/slastore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/tagstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/cannedresponse"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/plugin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/tag"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
	"github.com/publiciallc/go-help-desk/backend/internal/server"
	"github.com/publiciallc/go-help-desk/backend/internal/server/notify"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
	"github.com/stretchr/testify/require"
)

// harness is a test server wired against a real (rolled-back) DB transaction.
type harness struct {
	srv             *server.Server
	apiKey          string // raw token for the seeded staff user
	adminKey        string // raw token for the seeded admin user
	userKey         string // raw token for the seeded reporting (RoleUser) user
	staffID         uuid.UUID
	adminID         uuid.UUID
	catID           uuid.UUID
	adminSvc        *admin.Service
	userSvc         *user.Service
	categorySvc     *category.Service
	cannedResponses *cannedresponse.Service
}

func newHarness(t *testing.T) (*harness, func()) {
	t.Helper()
	db, closeDB := testutil.NewDB(t)
	q, rollback := testutil.TxQueries(t, db)

	ctx := context.Background()

	// Stores
	uStore := userstore.New(q)
	tStore := ticketstore.New(q)
	cStore := categorystore.New(q)
	gStore := groupstore.New(q)
	aStore := adminstore.New(q)
	auStore := auditstore.New(q)
	authSt := authstore.New(q)
	cfStore := customfieldstore.New(q)
	tagSt := tagstore.New(q)
	crStore := cannedresponsestore.New(q)

	// Services
	userSvc := user.NewService(uStore)
	categorySvc := category.NewService(cStore)
	groupSvc := group.NewService(gStore)
	adminSvc := admin.NewService(aStore)
	tagSvc := tag.NewService(tagSt)
	customFieldSvc := customfield.NewService(cfStore)
	cannedResponseSvc := cannedresponse.NewService(crStore)
	dispatcher := notify.NewMulti() // no-op in tests
	ticketSvc := ticket.NewService(tStore, tStore, dispatcher, auStore, nil)
	require.NoError(t, ticketSvc.LoadSystemStatuses(ctx))

	// Seed an admin user.
	adminUser, err := userSvc.Create(ctx, user.CreateUserInput{
		Email:       "admin@test.local",
		DisplayName: "Admin",
		Role:        user.RoleAdmin,
		Password:    "password",
	})
	require.NoError(t, err)

	// Seed a staff user + API key.
	staffUser, err := userSvc.Create(ctx, user.CreateUserInput{
		Email:       "staff@test.local",
		DisplayName: "Staff",
		Role:        user.RoleStaff,
		Password:    "password",
	})
	require.NoError(t, err)

	rawToken, hashedToken, err := auth.GenerateToken()
	require.NoError(t, err)
	apiKey := auth.APIKey{
		ID:          uuid.New(),
		Name:        "test-key",
		HashedToken: hashedToken,
		UserID:      staffUser.ID,
		Scopes:      []string{"*"},
		CreatedAt:   time.Now(),
	}
	require.NoError(t, authSt.CreateAPIKey(ctx, apiKey))

	// Seed an admin API key.
	adminRawToken, adminHashedToken, err := auth.GenerateToken()
	require.NoError(t, err)
	adminAPIKey := auth.APIKey{
		ID:          uuid.New(),
		Name:        "admin-test-key",
		HashedToken: adminHashedToken,
		UserID:      adminUser.ID,
		Scopes:      []string{"*"},
		CreatedAt:   time.Now(),
	}
	require.NoError(t, authSt.CreateAPIKey(ctx, adminAPIKey))

	// Seed a reporting (RoleUser) user + API key.
	reportingUser, err := userSvc.Create(ctx, user.CreateUserInput{
		Email:       "user@test.local",
		DisplayName: "Reporting User",
		Role:        user.RoleUser,
		Password:    "password",
	})
	require.NoError(t, err)

	userRawToken, userHashedToken, err := auth.GenerateToken()
	require.NoError(t, err)
	userAPIKey := auth.APIKey{
		ID:          uuid.New(),
		Name:        "user-test-key",
		HashedToken: userHashedToken,
		UserID:      reportingUser.ID,
		Scopes:      []string{"*"},
		CreatedAt:   time.Now(),
	}
	require.NoError(t, authSt.CreateAPIKey(ctx, userAPIKey))

	// Seed a category.
	cat, err := categorySvc.CreateCategory(ctx, "General", 1)
	require.NoError(t, err)

	// API key lookup closure.
	apiKeyLookup := authmw.APIKeyAuthFunc(func(ctx context.Context, hashed string) (auth.APIKey, user.User, error) {
		k, err := authSt.GetByHash(ctx, hashed)
		if err != nil {
			return auth.APIKey{}, user.User{}, err
		}
		u, err := userSvc.GetByID(ctx, k.UserID)
		if err != nil {
			return auth.APIKey{}, user.User{}, err
		}
		return k, u, nil
	})

	cfg := &config.Config{
		SessionSecret: "test-session-secret-32-bytes-long!",
		JWTSecret:     "test-jwt-secret",
	}
	sessionStore := sessions.NewCookieStore([]byte(cfg.SessionSecret))

	srv := server.New(
		cfg,
		sessionStore,
		userSvc,
		ticketSvc,
		categorySvc,
		groupSvc,
		tagSvc,
		adminSvc,
		customFieldSvc,
		sla.NewService(slastore.New(q)),
		plugin.NewRegistry(),
		apiKeyLookup,
		authSt,
		authSt,
		nil, // registration service not needed in integration tests
		cannedResponseSvc,
	)

	h := &harness{
		srv:             srv,
		apiKey:          rawToken,
		adminKey:        adminRawToken,
		userKey:         userRawToken,
		staffID:         staffUser.ID,
		adminID:         adminUser.ID,
		catID:           cat.ID,
		adminSvc:        adminSvc,
		userSvc:         userSvc,
		categorySvc:     categorySvc,
		cannedResponses: cannedResponseSvc,
	}
	cleanup := func() {
		rollback()
		closeDB()
	}
	return h, cleanup
}

// do sends a request to the test server and returns the response.
func (h *harness) do(t *testing.T, method, path string, body any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Authorization", "ApiKey "+h.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	h.srv.ServeHTTP(rr, req)
	return rr.Result()
}

// doAsAdmin sends a request authenticated as the seeded admin user.
func (h *harness) doAsAdmin(t *testing.T, method, path string, body any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Authorization", "ApiKey "+h.adminKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	h.srv.ServeHTTP(rr, req)
	return rr.Result()
}

// doAsUser sends a request authenticated as the seeded reporting (RoleUser) user.
func (h *harness) doAsUser(t *testing.T, method, path string, body any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Authorization", "ApiKey "+h.userKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	h.srv.ServeHTTP(rr, req)
	return rr.Result()
}

// doAs sends a request using a session-style actor injected via a custom header
// (we inject the actor directly by using a special test helper request).
func (h *harness) doUnauth(t *testing.T, method, path string, body any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	h.srv.ServeHTTP(rr, req)
	return rr.Result()
}

func decodeJSON(t *testing.T, r *http.Response, dst any) {
	t.Helper()
	require.NoError(t, json.NewDecoder(r.Body).Decode(dst))
}

// ── Health ───────────────────────────────────────────────────────────────────

func TestHealth(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.do(t, http.MethodGet, "/health", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// ── Tickets ──────────────────────────────────────────────────────────────────

func TestCreateTicket(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.do(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject":     "Printer broken",
		"description": "It won't print",
		"category_id": h.catID.String(),
		"priority":    "high",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var tk ticket.Ticket
	decodeJSON(t, resp, &tk)
	require.Equal(t, "Printer broken", tk.Subject)
	require.NotEqual(t, uuid.Nil, tk.ID)
	require.NotEmpty(t, string(tk.TrackingNumber))
}

func TestCreateTicket_MissingSubject(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.do(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"description": "No subject",
		"category_id": h.catID.String(),
	})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TestCreateTicket_DescriptionTooLong guards against the description
// exceeding tickets.search_vector's underlying tsvector size limit (~1MB) —
// and confirms the resulting validation error surfaces as 400, not 500
// (ticket.ErrValidation must reach the handler's error-mapping check).
func TestCreateTicket_DescriptionTooLong(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.do(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject":     "Printer broken",
		"description": strings.Repeat("a", ticket.MaxDescriptionLength+1),
		"category_id": h.catID.String(),
	})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestCreateTicket_SubjectTooLong(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.do(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject":     strings.Repeat("a", ticket.MaxSubjectLength+1),
		"description": "Body",
		"category_id": h.catID.String(),
	})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestCreateTicket_Unauthenticated(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// Guest submission is off by default — should get 401.
	resp := h.doUnauth(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject":     "Help",
		"description": "Need help",
		"category_id": h.catID.String(),
	})
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestGetTicket(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// Create a ticket first.
	createResp := h.do(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject":     "Get me",
		"description": "Details",
		"category_id": h.catID.String(),
	})
	require.Equal(t, http.StatusCreated, createResp.StatusCode)
	var created ticket.Ticket
	decodeJSON(t, createResp, &created)

	// Fetch by UUID.
	getResp := h.do(t, http.MethodGet, "/api/v1/tickets/"+created.ID.String(), nil)
	require.Equal(t, http.StatusOK, getResp.StatusCode)
	var got ticket.Ticket
	decodeJSON(t, getResp, &got)
	require.Equal(t, created.ID, got.ID)

	// Fetch by tracking number.
	tnResp := h.do(t, http.MethodGet, "/api/v1/tickets/"+string(created.TrackingNumber), nil)
	require.Equal(t, http.StatusOK, tnResp.StatusCode)
}

func TestGetTicket_NotFound(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.do(t, http.MethodGet, "/api/v1/tickets/"+uuid.New().String(), nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestAddReply(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	createResp := h.do(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject":     "Reply test",
		"description": "Details",
		"category_id": h.catID.String(),
	})
	require.Equal(t, http.StatusCreated, createResp.StatusCode)
	var tk ticket.Ticket
	decodeJSON(t, createResp, &tk)

	replyResp := h.do(t, http.MethodPost, fmt.Sprintf("/api/v1/tickets/%s/replies", tk.ID), map[string]any{
		"body": "Working on it.",
	})
	require.Equal(t, http.StatusCreated, replyResp.StatusCode)

	listResp := h.do(t, http.MethodGet, fmt.Sprintf("/api/v1/tickets/%s/replies", tk.ID), nil)
	require.Equal(t, http.StatusOK, listResp.StatusCode)
	var replies []ticket.Reply
	decodeJSON(t, listResp, &replies)
	require.Len(t, replies, 1)
	require.Equal(t, "Working on it.", replies[0].Body)
}

func TestAddReply_EmptyBody(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	createResp := h.do(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject":     "Empty reply test",
		"description": "Details",
		"category_id": h.catID.String(),
	})
	require.Equal(t, http.StatusCreated, createResp.StatusCode)
	var tk ticket.Ticket
	decodeJSON(t, createResp, &tk)

	resp := h.do(t, http.MethodPost, fmt.Sprintf("/api/v1/tickets/%s/replies", tk.ID), map[string]any{
		"body": "   ",
	})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestResolveTicket(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	createResp := h.do(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject":     "Resolve me",
		"description": "Details",
		"category_id": h.catID.String(),
	})
	require.Equal(t, http.StatusCreated, createResp.StatusCode)
	var tk ticket.Ticket
	decodeJSON(t, createResp, &tk)

	resolveResp := h.do(t, http.MethodPost, fmt.Sprintf("/api/v1/tickets/%s/resolve", tk.ID), map[string]any{
		"notes": "Fixed it.",
	})
	require.Equal(t, http.StatusOK, resolveResp.StatusCode)

	var resolved ticket.Ticket
	decodeJSON(t, resolveResp, &resolved)
	require.NotNil(t, resolved.ResolvedAt)
}

// ── List tickets: admin scopes + scoped dashboard counts ─────────────────────

func TestListTickets_AdminScopes(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// Three tickets: one created by staff (stays assigned to nobody),
	// one created by admin (also unassigned), and one created by staff and
	// then reassigned to admin via PATCH.
	for _, subject := range []string{"staff ticket A", "admin ticket B"} {
		var resp *http.Response
		if subject == "admin ticket B" {
			resp = h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets", map[string]any{
				"subject":     subject,
				"description": "x",
				"category_id": h.catID.String(),
			})
		} else {
			resp = h.do(t, http.MethodPost, "/api/v1/tickets", map[string]any{
				"subject":     subject,
				"description": "x",
				"category_id": h.catID.String(),
			})
		}
		require.Equal(t, http.StatusCreated, resp.StatusCode)
	}
	assignResp := h.do(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject":     "reassigned to admin",
		"description": "x",
		"category_id": h.catID.String(),
	})
	require.Equal(t, http.StatusCreated, assignResp.StatusCode)
	var tk ticket.Ticket
	decodeJSON(t, assignResp, &tk)
	patch := h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), map[string]any{
		"assignee_user_id": h.adminID.String(),
	})
	require.Equal(t, http.StatusOK, patch.StatusCode)

	t.Run("scope=all returns every ticket", func(t *testing.T) {
		resp := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets?scope=all", nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var tickets []ticket.Ticket
		decodeJSON(t, resp, &tickets)
		require.Len(t, tickets, 3)
	})

	t.Run("scope=unassigned excludes assigned tickets", func(t *testing.T) {
		resp := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets?scope=unassigned", nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var tickets []ticket.Ticket
		decodeJSON(t, resp, &tickets)
		require.Len(t, tickets, 2)
		for _, tk := range tickets {
			require.Nil(t, tk.AssigneeUserID)
			require.Nil(t, tk.AssigneeGroupID)
		}
	})

	t.Run("default scope returns only tickets assigned to the admin", func(t *testing.T) {
		resp := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets", nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var tickets []ticket.Ticket
		decodeJSON(t, resp, &tickets)
		require.Len(t, tickets, 1)
		require.NotNil(t, tickets[0].AssigneeUserID)
		require.Equal(t, h.adminID, *tickets[0].AssigneeUserID)
	})

	t.Run("staff cannot use admin-only scopes", func(t *testing.T) {
		resp := h.do(t, http.MethodGet, "/api/v1/tickets?scope=all", nil)
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
		resp = h.do(t, http.MethodGet, "/api/v1/tickets?scope=unassigned", nil)
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	})
}

func TestListStatuses_DashboardCountsScopedByRole(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// Create two unassigned tickets as staff, then assign one to staff.
	var first ticket.Ticket
	for i, subject := range []string{"alpha", "beta"} {
		resp := h.do(t, http.MethodPost, "/api/v1/tickets", map[string]any{
			"subject":     subject,
			"description": "x",
			"category_id": h.catID.String(),
		})
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		if i == 0 {
			decodeJSON(t, resp, &first)
		}
	}
	patch := h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+first.ID.String(), map[string]any{
		"assignee_user_id": h.staffID.String(),
	})
	require.Equal(t, http.StatusOK, patch.StatusCode)

	// Admin sees the global count (both tickets).
	adminResp := h.doAsAdmin(t, http.MethodGet, "/api/v1/statuses", nil)
	require.Equal(t, http.StatusOK, adminResp.StatusCode)
	var adminStatuses []ticket.Status
	decodeJSON(t, adminResp, &adminStatuses)
	adminNew := findStatus(t, adminStatuses, ticket.StatusNameNew)
	require.Equal(t, int64(2), adminNew.TicketCount)

	// Staff sees only their assigned count (1).
	staffResp := h.do(t, http.MethodGet, "/api/v1/statuses", nil)
	require.Equal(t, http.StatusOK, staffResp.StatusCode)
	var staffStatuses []ticket.Status
	decodeJSON(t, staffResp, &staffStatuses)
	staffNew := findStatus(t, staffStatuses, ticket.StatusNameNew)
	require.Equal(t, int64(1), staffNew.TicketCount)
}

func findStatus(t *testing.T, statuses []ticket.Status, name string) ticket.Status {
	t.Helper()
	for _, s := range statuses {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("status %q not found", name)
	return ticket.Status{}
}

// ── Auth: require role ────────────────────────────────────────────────────────

func TestAdminRoute_RequiresAuth(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doUnauth(t, http.MethodGet, "/api/v1/admin/users", nil)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestAdminRoute_StaffForbidden(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// The harness API key belongs to a staff user — admin routes should 403.
	resp := h.do(t, http.MethodGet, "/api/v1/admin/users", nil)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

// ── Statuses ─────────────────────────────────────────────────────────────────

func TestListStatuses_Seeded(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// /admin/statuses requires admin; swap to an admin API key.
	// For simplicity, use the local login path to get a session isn't practical
	// here — instead we test via the ticket service's ListStatuses exposed on
	// the ticket handler by checking that the seeded statuses are reachable via
	// the public harness.  A dedicated admin-key harness would be needed for
	// full coverage; that is left as a future integration test.
	//
	// For now, verify the seeded statuses are present through the ticket store
	// indirectly: creating a ticket succeeds (it requires the "New" status to exist).
	resp := h.do(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject":     "Status seed check",
		"description": "If New status missing, this 500s",
		"category_id": h.catID.String(),
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
}

// ── Admin: users ──────────────────────────────────────────────────────────────

func TestListUsers_AsAdmin(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/users", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var users []user.User
	decodeJSON(t, resp, &users)
	require.GreaterOrEqual(t, len(users), 2) // admin + staff seeded in harness
}

func TestCreateUser_AsAdmin(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"email":        "newuser@example.com",
		"display_name": "New User",
		"role":         "user",
		"password":     "password123",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var u user.User
	decodeJSON(t, resp, &u)
	require.NotEqual(t, uuid.Nil, u.ID)
	require.Equal(t, "newuser@example.com", u.Email)
}

func TestGetUser_AsAdmin(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodGet, fmt.Sprintf("/api/v1/admin/users/%s", h.staffID), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var u user.User
	decodeJSON(t, resp, &u)
	require.Equal(t, h.staffID, u.ID)
}

func TestUpdateUser_AsAdmin(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	newName := "Staff Renamed"
	resp := h.doAsAdmin(t, http.MethodPatch, fmt.Sprintf("/api/v1/admin/users/%s", h.staffID), map[string]any{
		"display_name": newName,
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var u user.User
	decodeJSON(t, resp, &u)
	require.Equal(t, newName, u.DisplayName)
}

func TestDeleteUser_AsAdmin(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// Create a user to delete.
	createResp := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"email":        "todelete@example.com",
		"display_name": "To Delete",
		"role":         "user",
		"password":     "password123",
	})
	require.Equal(t, http.StatusCreated, createResp.StatusCode)
	var created user.User
	decodeJSON(t, createResp, &created)

	resp := h.doAsAdmin(t, http.MethodDelete, fmt.Sprintf("/api/v1/admin/users/%s", created.ID), nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}

// ── Admin: categories ─────────────────────────────────────────────────────────

func TestListCategories_AsAdmin(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/categories", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var cats []map[string]any
	decodeJSON(t, resp, &cats)
	require.GreaterOrEqual(t, len(cats), 1) // "General" seeded in harness
}

func TestCreateCategory_AsAdmin(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/categories", map[string]any{
		"name":       "Networking",
		"sort_order": 2,
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var cat map[string]any
	decodeJSON(t, resp, &cat)
	require.Equal(t, "Networking", cat["name"])
}

func TestCreateType_AsAdmin(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodPost,
		fmt.Sprintf("/api/v1/admin/categories/%s/types", h.catID),
		map[string]any{"name": "Hardware", "sort_order": 1})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
}

func TestCreateItem_AsAdmin(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// Create type first.
	typeResp := h.doAsAdmin(t, http.MethodPost,
		fmt.Sprintf("/api/v1/admin/categories/%s/types", h.catID),
		map[string]any{"name": "Software", "sort_order": 1})
	require.Equal(t, http.StatusCreated, typeResp.StatusCode)
	var tp map[string]any
	decodeJSON(t, typeResp, &tp)

	resp := h.doAsAdmin(t, http.MethodPost,
		fmt.Sprintf("/api/v1/admin/categories/%s/types/%s/items", h.catID, tp["id"]),
		map[string]any{"name": "Windows", "sort_order": 1})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
}

// ── Admin: statuses ───────────────────────────────────────────────────────────

func TestListStatuses_AsAdmin(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/statuses", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var statuses []map[string]any
	decodeJSON(t, resp, &statuses)
	require.GreaterOrEqual(t, len(statuses), 3) // New, Resolved, Closed at minimum
}

func TestCreateStatus_AsAdmin(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/statuses", map[string]any{
		"name":       "In Progress",
		"sort_order": 10,
		"color":      "#ff9900",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var st map[string]any
	decodeJSON(t, resp, &st)
	require.Equal(t, "In Progress", st["name"])
}

func TestDeleteStatus_Custom_AsAdmin(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	createResp := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/statuses", map[string]any{
		"name":       "Pending",
		"sort_order": 11,
		"color":      "#aabbcc",
	})
	require.Equal(t, http.StatusCreated, createResp.StatusCode)
	var st map[string]any
	decodeJSON(t, createResp, &st)

	resp := h.doAsAdmin(t, http.MethodDelete,
		fmt.Sprintf("/api/v1/admin/statuses/%s", st["id"]), nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}

// ── Admin: settings ───────────────────────────────────────────────────────────

func TestGetSettings_AsAdmin(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/settings", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var settings map[string]any
	decodeJSON(t, resp, &settings)
	// May be empty if no settings have been set yet — just verify 200.
}

func TestUpdateSettings_AsAdmin(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		"reopen_window_days": 14,
	})
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Verify the value was stored.
	getResp := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/settings", nil)
	require.Equal(t, http.StatusOK, getResp.StatusCode)
	var settings map[string]any
	decodeJSON(t, getResp, &settings)
	require.Contains(t, settings, "reopen_window_days")
}

// ── Me ────────────────────────────────────────────────────────────────────────

func TestGetMe_AsStaff(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.do(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var u user.User
	decodeJSON(t, resp, &u)
	require.Equal(t, h.staffID, u.ID)
}

func TestChangePassword_AsStaff(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.do(t, http.MethodPatch, "/api/v1/me/password", map[string]any{
		"password": "newpassword123",
	})
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestChangePassword_TooShort(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.do(t, http.MethodPatch, "/api/v1/me/password", map[string]any{
		"password": "short",
	})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// ── Auth ──────────────────────────────────────────────────────────────────────

func TestLocalLogin_Valid(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doUnauth(t, http.MethodPost, "/api/v1/auth/local/login", map[string]any{
		"email":    "staff@test.local",
		"password": "password",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]any
	decodeJSON(t, resp, &body)
	require.NotNil(t, body["user"])
	mfaNeeded, _ := body["mfa_needed"].(bool)
	require.False(t, mfaNeeded)
}

func TestLocalLogin_WrongPassword(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doUnauth(t, http.MethodPost, "/api/v1/auth/local/login", map[string]any{
		"email":    "staff@test.local",
		"password": "wrongpassword",
	})
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestLogout(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doUnauth(t, http.MethodPost, "/api/v1/auth/local/logout", nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}

// ── Setup ─────────────────────────────────────────────────────────────────────

// newBareHarness builds a test server with no seeded users, for testing the
// first-run setup flow.
func newBareHarness(t *testing.T) (*harness, func()) {
	t.Helper()
	db, closeDB := testutil.NewDB(t)
	q, rollback := testutil.TxQueries(t, db)

	ctx := context.Background()

	uStore := userstore.New(q)
	tStore := ticketstore.New(q)
	cStore := categorystore.New(q)
	gStore := groupstore.New(q)
	aStore := adminstore.New(q)
	auStore := auditstore.New(q)
	authSt := authstore.New(q)
	cfStore := customfieldstore.New(q)
	tagSt := tagstore.New(q)
	crStore := cannedresponsestore.New(q)

	userSvc := user.NewService(uStore)
	categorySvc := category.NewService(cStore)
	groupSvc := group.NewService(gStore)
	adminSvc := admin.NewService(aStore)
	tagSvc := tag.NewService(tagSt)
	customFieldSvc := customfield.NewService(cfStore)
	cannedResponseSvc := cannedresponse.NewService(crStore)
	dispatcher := notify.NewMulti()
	ticketSvc := ticket.NewService(tStore, tStore, dispatcher, auStore, nil)
	require.NoError(t, ticketSvc.LoadSystemStatuses(ctx))

	apiKeyLookup := authmw.APIKeyAuthFunc(func(ctx context.Context, hashed string) (auth.APIKey, user.User, error) {
		k, err := authSt.GetByHash(ctx, hashed)
		if err != nil {
			return auth.APIKey{}, user.User{}, err
		}
		u, err := userSvc.GetByID(ctx, k.UserID)
		if err != nil {
			return auth.APIKey{}, user.User{}, err
		}
		return k, u, nil
	})

	cfg := &config.Config{
		SessionSecret: "test-session-secret-32-bytes-long!",
		JWTSecret:     "test-jwt-secret",
	}
	sessionStore := sessions.NewCookieStore([]byte(cfg.SessionSecret))

	srv := server.New(
		cfg,
		sessionStore,
		userSvc,
		ticketSvc,
		categorySvc,
		groupSvc,
		tagSvc,
		adminSvc,
		customFieldSvc,
		sla.NewService(slastore.New(q)),
		plugin.NewRegistry(),
		apiKeyLookup,
		authSt,
		authSt,
		nil, // registration service not needed in integration tests
		cannedResponseSvc,
	)

	h := &harness{srv: srv}
	cleanup := func() {
		rollback()
		closeDB()
	}
	return h, cleanup
}

func TestSetupStatus_Needed(t *testing.T) {
	h, cleanup := newBareHarness(t)
	defer cleanup()

	resp := h.doUnauth(t, http.MethodGet, "/api/v1/setup/status", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		Needed bool `json:"needed"`
	}
	decodeJSON(t, resp, &body)
	require.True(t, body.Needed)
}

func TestSetupStatus_NotNeeded(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doUnauth(t, http.MethodGet, "/api/v1/setup/status", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		Needed bool `json:"needed"`
	}
	decodeJSON(t, resp, &body)
	require.False(t, body.Needed)
}

func TestSetup_CreatesAdmin(t *testing.T) {
	h, cleanup := newBareHarness(t)
	defer cleanup()

	resp := h.doUnauth(t, http.MethodPost, "/api/v1/setup", map[string]any{
		"email":        "admin@example.com",
		"display_name": "Admin",
		"password":     "correct-horse-battery",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var u user.User
	decodeJSON(t, resp, &u)
	require.Equal(t, "admin@example.com", u.Email)
	require.Equal(t, user.RoleAdmin, u.Role)
	require.Empty(t, u.PasswordHash) // never expose the hash
}

func TestSetup_BlockedWhenUsersExist(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doUnauth(t, http.MethodPost, "/api/v1/setup", map[string]any{
		"email":        "another@example.com",
		"display_name": "Another",
		"password":     "password",
	})
	require.Equal(t, http.StatusConflict, resp.StatusCode)
}

func TestSetup_MissingFields(t *testing.T) {
	h, cleanup := newBareHarness(t)
	defer cleanup()

	resp := h.doUnauth(t, http.MethodPost, "/api/v1/setup", map[string]any{
		"email": "admin@example.com",
		// missing display_name and password
	})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// enrollMFA runs the normal enrollment flow end-to-end so the user ends up
// with MFAEnabled=true in the DB.
func enrollMFA(t *testing.T, ctx context.Context, userSvc *user.Service, id uuid.UUID) {
	t.Helper()
	secret, _, err := userSvc.EnrollMFA(ctx, id, "test")
	require.NoError(t, err)
	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)
	require.NoError(t, userSvc.ConfirmMFAEnrollment(ctx, id, code))
}

// ── MFA Enable vs Require ─────────────────────────────────────────────────────

func TestLocalLogin_MFADisabled_NoPromptsEvenIfEnrolled(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	// User has MFA flag on but global MFA is disabled → no prompts.
	enrollMFA(t, ctx, h.userSvc, h.staffID)

	resp := h.doUnauth(t, http.MethodPost, "/api/v1/auth/local/login", map[string]any{
		"email":    "staff@test.local",
		"password": "password",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]any
	decodeJSON(t, resp, &body)
	require.False(t, body["mfa_needed"].(bool))
	require.False(t, body["mfa_enrollment_needed"].(bool))
}

func TestLocalLogin_MFAOptIn_NotEnrolled_Passes(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	// Global enabled, but role not in enforced list → opt-in; unenrolled passes.
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyMFAEnabled, true))

	resp := h.doUnauth(t, http.MethodPost, "/api/v1/auth/local/login", map[string]any{
		"email":    "staff@test.local",
		"password": "password",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]any
	decodeJSON(t, resp, &body)
	require.False(t, body["mfa_needed"].(bool))
	require.False(t, body["mfa_enrollment_needed"].(bool))
}

func TestLocalLogin_MFARequired_NotEnrolled_DemandsEnrollment(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	// Global enabled AND staff role enforced → unenrolled staff must enroll.
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyMFAEnabled, true))
	require.NoError(t, h.adminSvc.SetRaw(ctx, admin.KeyMFAEnforcedRoles, []byte(`["staff","admin"]`)))

	resp := h.doUnauth(t, http.MethodPost, "/api/v1/auth/local/login", map[string]any{
		"email":    "staff@test.local",
		"password": "password",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]any
	decodeJSON(t, resp, &body)
	require.False(t, body["mfa_needed"].(bool))
	require.True(t, body["mfa_enrollment_needed"].(bool))
}

func TestLocalLogin_MFARequired_RoleNotEnforced_Passes(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	// Only admin role enforced → staff user is unaffected.
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyMFAEnabled, true))
	require.NoError(t, h.adminSvc.SetRaw(ctx, admin.KeyMFAEnforcedRoles, []byte(`["admin"]`)))

	resp := h.doUnauth(t, http.MethodPost, "/api/v1/auth/local/login", map[string]any{
		"email":    "staff@test.local",
		"password": "password",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]any
	decodeJSON(t, resp, &body)
	require.False(t, body["mfa_needed"].(bool))
	require.False(t, body["mfa_enrollment_needed"].(bool))
}

func TestLocalLogin_MFAEnrolled_DemandsVerification(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	// Global enabled and user enrolled → mfa_needed (verify flow).
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyMFAEnabled, true))
	enrollMFA(t, ctx, h.userSvc, h.staffID)

	resp := h.doUnauth(t, http.MethodPost, "/api/v1/auth/local/login", map[string]any{
		"email":    "staff@test.local",
		"password": "password",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]any
	decodeJSON(t, resp, &body)
	require.True(t, body["mfa_needed"].(bool))
	require.False(t, body["mfa_enrollment_needed"].(bool))
}

// ── Canned responses ─────────────────────────────────────────────────────────

func TestAdminCreateCannedResponse(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/canned-responses", map[string]any{
		"name":       "Greeting",
		"body":       "Hello, thanks for reaching out.",
		"sort_order": 1,
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var cr cannedresponse.CannedResponse
	decodeJSON(t, resp, &cr)
	require.NotEqual(t, uuid.Nil, cr.ID)
	require.Equal(t, "Greeting", cr.Name)
	require.Equal(t, "Hello, thanks for reaching out.", cr.Body)
	require.Nil(t, cr.CategoryID)
	require.Nil(t, cr.TypeID)
	require.Equal(t, 1, cr.SortOrder)
	require.False(t, cr.CreatedAt.IsZero(), "created_at should be the DB-assigned timestamp, not a zero value")
}

func TestAdminCreateCannedResponse_TypeRequiresCategory(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/canned-responses", map[string]any{
		"name":    "Bad scope",
		"body":    "This should fail.",
		"type_id": uuid.New().String(),
	})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestAdminListCannedResponses(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	for _, name := range []string{"First", "Second"} {
		resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/canned-responses", map[string]any{
			"name": name,
			"body": "Body for " + name,
		})
		require.Equal(t, http.StatusCreated, resp.StatusCode)
	}

	resp := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/canned-responses", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var crs []cannedresponse.CannedResponse
	decodeJSON(t, resp, &crs)
	require.Len(t, crs, 2)
}

func TestAdminUpdateCannedResponse(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	createResp := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/canned-responses", map[string]any{
		"name": "Original",
		"body": "Original body",
	})
	require.Equal(t, http.StatusCreated, createResp.StatusCode)
	var created cannedresponse.CannedResponse
	decodeJSON(t, createResp, &created)

	updateResp := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/canned-responses/"+created.ID.String(), map[string]any{
		"name":       "Updated",
		"body":       "Updated body",
		"sort_order": 5,
	})
	require.Equal(t, http.StatusOK, updateResp.StatusCode)

	var updated cannedresponse.CannedResponse
	decodeJSON(t, updateResp, &updated)
	require.Equal(t, created.ID, updated.ID)
	require.Equal(t, "Updated", updated.Name)
	require.Equal(t, "Updated body", updated.Body)
	require.Equal(t, 5, updated.SortOrder)
	require.Equal(t, created.CreatedAt, updated.CreatedAt, "created_at must be preserved, not zeroed, across an update")

	// Confirm the change persisted.
	got, err := h.cannedResponses.Get(context.Background(), created.ID)
	require.NoError(t, err)
	require.Equal(t, "Updated", got.Name)
}

// TestAdminUpdateCannedResponse_NotFound guards against a real bug: an UPDATE
// whose WHERE clause matches no row succeeds silently at the SQL level, so
// without an existence check a PATCH to a nonexistent id previously returned
// 200 with a fabricated response body instead of 404.
func TestAdminUpdateCannedResponse_NotFound(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/canned-responses/"+uuid.New().String(), map[string]any{
		"name": "Ghost",
		"body": "Body",
	})
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestAdminDeleteCannedResponse(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	createResp := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/canned-responses", map[string]any{
		"name": "To delete",
		"body": "Body",
	})
	require.Equal(t, http.StatusCreated, createResp.StatusCode)
	var created cannedresponse.CannedResponse
	decodeJSON(t, createResp, &created)

	deleteResp := h.doAsAdmin(t, http.MethodDelete, "/api/v1/admin/canned-responses/"+created.ID.String(), nil)
	require.Equal(t, http.StatusNoContent, deleteResp.StatusCode)

	_, err := h.cannedResponses.Get(context.Background(), created.ID)
	require.Error(t, err)
}

func TestCannedResponses_StaffForbidden(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// The harness API key belongs to a staff user — admin routes should 403.
	resp := h.do(t, http.MethodPost, "/api/v1/admin/canned-responses", map[string]any{
		"name": "Nope",
		"body": "Should not be allowed",
	})
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestListTicketCannedResponses_ScopeFiltering(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	// A second category, distinct from the ticket's category.
	otherCat, err := h.categorySvc.CreateCategory(ctx, "Other", 2)
	require.NoError(t, err)

	// Global response — should always be available.
	global, err := h.cannedResponses.Create(ctx, "Global", "Global body", nil, nil, 0)
	require.NoError(t, err)

	// Scoped to the ticket's category — should be available.
	matching, err := h.cannedResponses.Create(ctx, "Matching category", "Matching body", &h.catID, nil, 0)
	require.NoError(t, err)

	// Scoped to a different category — should NOT be available.
	_, err = h.cannedResponses.Create(ctx, "Other category", "Other body", &otherCat.ID, nil, 0)
	require.NoError(t, err)

	// Create a ticket in h.catID.
	createResp := h.do(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject":     "Scope filter test",
		"description": "Details",
		"category_id": h.catID.String(),
	})
	require.Equal(t, http.StatusCreated, createResp.StatusCode)
	var tk ticket.Ticket
	decodeJSON(t, createResp, &tk)

	resp := h.do(t, http.MethodGet, fmt.Sprintf("/api/v1/tickets/%s/canned-responses", tk.ID), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var crs []cannedresponse.CannedResponse
	decodeJSON(t, resp, &crs)
	gotIDs := make(map[uuid.UUID]bool)
	for _, cr := range crs {
		gotIDs[cr.ID] = true
	}
	require.Len(t, crs, 2)
	require.True(t, gotIDs[global.ID])
	require.True(t, gotIDs[matching.ID])
}

// TestListTicketCannedResponses_UserForbidden confirms the reporting user
// (RoleUser) cannot reach the staff/admin-only picker endpoint — the one
// route within ticketRouter that narrows below its top-level role check.
func TestListTicketCannedResponses_UserForbidden(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	createResp := h.do(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject":     "User forbidden test",
		"description": "Details",
		"category_id": h.catID.String(),
	})
	require.Equal(t, http.StatusCreated, createResp.StatusCode)
	var tk ticket.Ticket
	decodeJSON(t, createResp, &tk)

	resp := h.doAsUser(t, http.MethodGet, fmt.Sprintf("/api/v1/tickets/%s/canned-responses", tk.ID), nil)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

// TestListTicketCannedResponses_TypeScopeFiltering covers the category+type
// scope level at the HTTP layer (TestListTicketCannedResponses_ScopeFiltering
// above only covers global vs. category-only).
func TestListTicketCannedResponses_TypeScopeFiltering(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	typeA, err := h.categorySvc.CreateType(ctx, h.catID, "Type A", 1)
	require.NoError(t, err)
	typeB, err := h.categorySvc.CreateType(ctx, h.catID, "Type B", 2)
	require.NoError(t, err)

	// Scoped to the ticket's category AND type A — should be available for a
	// type-A ticket, excluded for a type-B ticket.
	typeAScoped, err := h.cannedResponses.Create(ctx, "Type A only", "Body", &h.catID, &typeA.ID, 0)
	require.NoError(t, err)

	// Create a ticket in category h.catID, type A.
	createResp := h.do(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject":     "Type scope filter test",
		"description": "Details",
		"category_id": h.catID.String(),
		"type_id":     typeA.ID.String(),
	})
	require.Equal(t, http.StatusCreated, createResp.StatusCode)
	var tkA ticket.Ticket
	decodeJSON(t, createResp, &tkA)

	respA := h.do(t, http.MethodGet, fmt.Sprintf("/api/v1/tickets/%s/canned-responses", tkA.ID), nil)
	require.Equal(t, http.StatusOK, respA.StatusCode)
	var crsA []cannedresponse.CannedResponse
	decodeJSON(t, respA, &crsA)
	idsA := make(map[uuid.UUID]bool)
	for _, cr := range crsA {
		idsA[cr.ID] = true
	}
	require.True(t, idsA[typeAScoped.ID], "type-A-scoped response should appear for a type-A ticket")

	// A second ticket in the same category but type B should NOT see it.
	createRespB := h.do(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject":     "Type scope filter test B",
		"description": "Details",
		"category_id": h.catID.String(),
		"type_id":     typeB.ID.String(),
	})
	require.Equal(t, http.StatusCreated, createRespB.StatusCode)
	var tkB ticket.Ticket
	decodeJSON(t, createRespB, &tkB)

	respB := h.do(t, http.MethodGet, fmt.Sprintf("/api/v1/tickets/%s/canned-responses", tkB.ID), nil)
	require.Equal(t, http.StatusOK, respB.StatusCode)
	var crsB []cannedresponse.CannedResponse
	decodeJSON(t, respB, &crsB)
	for _, cr := range crsB {
		require.NotEqual(t, typeAScoped.ID, cr.ID, "type-A-scoped response must not appear for a type-B ticket")
	}
}
