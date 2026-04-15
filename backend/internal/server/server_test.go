package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/sessions"
	"github.com/google/uuid"
	"github.com/open-help-desk/open-help-desk/backend/internal/database/adminstore"
	"github.com/open-help-desk/open-help-desk/backend/internal/database/auditstore"
	"github.com/open-help-desk/open-help-desk/backend/internal/database/authstore"
	"github.com/open-help-desk/open-help-desk/backend/internal/database/categorystore"
	"github.com/open-help-desk/open-help-desk/backend/internal/database/customfieldstore"
	"github.com/open-help-desk/open-help-desk/backend/internal/database/groupstore"
	"github.com/open-help-desk/open-help-desk/backend/internal/database/slastore"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/sla"
	"github.com/open-help-desk/open-help-desk/backend/internal/database/tagstore"
	"github.com/open-help-desk/open-help-desk/backend/internal/database/ticketstore"
	"github.com/open-help-desk/open-help-desk/backend/internal/database/userstore"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/admin"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/auth"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/category"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/customfield"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/group"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/plugin"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/tag"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/ticket"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/user"
	authmw "github.com/open-help-desk/open-help-desk/backend/internal/middleware"
	"github.com/open-help-desk/open-help-desk/backend/internal/config"
	"github.com/open-help-desk/open-help-desk/backend/internal/server"
	"github.com/open-help-desk/open-help-desk/backend/internal/server/notify"
	"github.com/open-help-desk/open-help-desk/backend/internal/testutil"
	"github.com/stretchr/testify/require"
)

// harness is a test server wired against a real (rolled-back) DB transaction.
type harness struct {
	srv      *server.Server
	apiKey   string // raw token for the seeded staff user
	adminKey string // raw token for the seeded admin user
	staffID  uuid.UUID
	adminID  uuid.UUID
	catID    uuid.UUID
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

	// Services
	userSvc := user.NewService(uStore)
	categorySvc := category.NewService(cStore)
	groupSvc := group.NewService(gStore)
	adminSvc := admin.NewService(aStore)
	tagSvc := tag.NewService(tagSt)
	customFieldSvc := customfield.NewService(cfStore)
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
	)

	h := &harness{
		srv:      srv,
		apiKey:   rawToken,
		adminKey: adminRawToken,
		staffID:  staffUser.ID,
		adminID:  adminUser.ID,
		catID:    cat.ID,
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

	userSvc := user.NewService(uStore)
	categorySvc := category.NewService(cStore)
	groupSvc := group.NewService(gStore)
	adminSvc := admin.NewService(aStore)
	tagSvc := tag.NewService(tagSt)
	customFieldSvc := customfield.NewService(cfStore)
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
