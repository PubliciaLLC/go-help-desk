package middleware

// White-box test: same package to access setActor and actorKey.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/stretchr/testify/require"
)

// ok200 is a handler that always responds 200.
var ok200 = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

// actorFromContext reads the actor from a handler's request.
func actorFromContext(r *http.Request) *Actor {
	return GetActor(r)
}

// ── RequireRole ───────────────────────────────────────────────────────────────

func TestRequireRole_NoActor_401(t *testing.T) {
	handler := RequireRole(user.RoleAdmin)(ok200)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestRequireRole_WrongRole_403(t *testing.T) {
	handler := RequireRole(user.RoleAdmin)(ok200)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = setActor(req, &Actor{UserID: uuid.New(), Role: user.RoleStaff, MFAPassed: true})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusForbidden, rr.Code)
}

func TestRequireRole_CorrectRole_200(t *testing.T) {
	handler := RequireRole(user.RoleAdmin, user.RoleStaff)(ok200)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = setActor(req, &Actor{UserID: uuid.New(), Role: user.RoleStaff, MFAPassed: true})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
}

// ── RequireMFA ────────────────────────────────────────────────────────────────

func TestRequireMFA_NotPassed_403(t *testing.T) {
	handler := RequireMFA(ok200)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = setActor(req, &Actor{UserID: uuid.New(), Role: user.RoleStaff, MFAPassed: false})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusForbidden, rr.Code)
}

func TestRequireMFA_Passed_200(t *testing.T) {
	handler := RequireMFA(ok200)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = setActor(req, &Actor{UserID: uuid.New(), Role: user.RoleStaff, MFAPassed: true})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
}

// ── APIKeyAuth ────────────────────────────────────────────────────────────────

func TestAPIKeyAuth_ValidKey_SetsActor(t *testing.T) {
	rawToken := "deadbeefdeadbeefdeadbeef12345678"
	hashed := auth.HashToken(rawToken)

	expectedUser := user.User{
		ID:    uuid.New(),
		Role:  user.RoleStaff,
		Email: "staff@example.com",
	}
	key := auth.APIKey{
		ID:          uuid.New(),
		HashedToken: hashed,
		UserID:      expectedUser.ID,
		Scopes:      []string{"tickets:read"},
	}

	lookup := APIKeyAuthFunc(func(_ context.Context, h string) (auth.APIKey, user.User, error) {
		if h == hashed {
			return key, expectedUser, nil
		}
		return auth.APIKey{}, user.User{}, http.ErrNoCookie
	})

	var captured *Actor
	handler := APIKeyAuth(lookup)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured = GetActor(r)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "ApiKey "+rawToken)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.NotNil(t, captured)
	require.Equal(t, expectedUser.ID, captured.UserID)
	require.Equal(t, user.RoleStaff, captured.Role)
	require.True(t, captured.MFAPassed)
}

func TestAPIKeyAuth_ExpiredKey_PassesThrough(t *testing.T) {
	rawToken := "deadbeefdeadbeefdeadbeef12345679"
	hashed := auth.HashToken(rawToken)

	past := time.Now().Add(-time.Hour)
	key := auth.APIKey{
		ID:          uuid.New(),
		HashedToken: hashed,
		UserID:      uuid.New(),
		ExpiresAt:   &past,
	}
	u := user.User{ID: key.UserID, Role: user.RoleUser}

	lookup := APIKeyAuthFunc(func(_ context.Context, _ string) (auth.APIKey, user.User, error) {
		return key, u, nil
	})

	var captured *Actor
	handler := APIKeyAuth(lookup)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured = GetActor(r)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "ApiKey "+rawToken)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Nil(t, captured, "expired key must not set actor")
}

func TestAPIKeyAuth_SkipsWhenActorAlreadySet(t *testing.T) {
	existingActor := &Actor{UserID: uuid.New(), Role: user.RoleAdmin, MFAPassed: true}

	lookup := APIKeyAuthFunc(func(_ context.Context, _ string) (auth.APIKey, user.User, error) {
		// Should never be called.
		t.Fatal("lookup should not be called when actor is already set")
		return auth.APIKey{}, user.User{}, nil
	})

	var captured *Actor
	handler := APIKeyAuth(lookup)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured = GetActor(r)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = setActor(req, existingActor)
	req.Header.Set("Authorization", "ApiKey sometoken")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, existingActor, captured)
}

// ── BearerAuth ────────────────────────────────────────────────────────────────

func TestBearerAuth_ValidJWT_SetsActor(t *testing.T) {
	jwtSecret := "test-jwt-secret"
	client := auth.OAuthClient{
		ID:       uuid.New(),
		ClientID: "client-abc",
		Scopes:   []string{"tickets:write"},
	}
	token, err := auth.IssueAccessToken(client, jwtSecret)
	require.NoError(t, err)

	var captured *Actor
	handler := BearerAuth(jwtSecret)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured = GetActor(r)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.NotNil(t, captured)
	require.Equal(t, user.RoleStaff, captured.Role) // OAuth clients act at staff level
	require.Equal(t, "client-abc", captured.ClientID)
	require.True(t, captured.MFAPassed)
}

func TestBearerAuth_Invalid_PassesThrough(t *testing.T) {
	var captured *Actor
	handler := BearerAuth("secret")(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured = GetActor(r)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer notavalidjwt")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Nil(t, captured, "invalid JWT must not set actor")
}
