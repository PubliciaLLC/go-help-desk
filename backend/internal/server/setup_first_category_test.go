package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// Setup creates the first category, not just the first administrator.
//
// Measured on a real instance before this existed: /setup created the admin,
// `categories` stayed empty, and POST /tickets then refused every shape —
// including an explicit null — with
//
//	400 {"error":{"code":"bad_request","message":"category_id is required"}}
//
// So the first thing an operator does after setup failed, and the message
// named a field rather than the action they needed to take. Nothing on the
// way in said "create a category first". See #323.
//
// The suite was blind to it because newHarness seeds a category of its own
// ("// Seed a category."). Every test is handed one, so the state a real
// first run is actually in was never exercised. These use newBareHarness,
// and go through HTTP rather than the services, because the bare harness
// deliberately wires nothing but the server.

// setupAndSignIn completes setup and returns the new administrator's session
// cookie, which is what a real operator has a second after finishing.
func setupAndSignIn(t *testing.T, h *harness, body map[string]any) *http.Cookie {
	t.Helper()
	resp := h.doUnauth(t, http.MethodPost, "/api/v1/setup", body)
	resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode, "setup failed")

	login := h.doUnauth(t, http.MethodPost, "/api/v1/auth/local/login", map[string]any{
		"email":    body["email"],
		"password": body["password"],
	})
	defer login.Body.Close()
	require.Equal(t, http.StatusOK, login.StatusCode, "the new administrator could not sign in")

	for _, c := range login.Cookies() {
		if c.Value != "" {
			return c
		}
	}
	t.Fatal("login returned no session cookie")
	return nil
}

func categoriesFor(t *testing.T, h *harness, c *http.Cookie) []map[string]any {
	t.Helper()
	resp := h.doUnauthWithCookie(t, http.MethodGet, "/api/v1/categories", c)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out []map[string]any
	decodeJSON(t, resp, &out)
	return out
}

func TestSetup_CreatesTheCategoryItWasGiven(t *testing.T) {
	h, cleanup := newBareHarness(t)
	defer cleanup()

	c := setupAndSignIn(t, h, map[string]any{
		"email": "admin@example.com", "display_name": "Admin",
		"password": "correct-horse-battery", "category": "Hardware",
	})

	cats := categoriesFor(t, h, c)
	require.Len(t, cats, 1, "setup should have created exactly one category")
	require.Equal(t, "Hardware", cats[0]["name"])
}

func TestSetup_DefaultsTheCategoryWhenNoneIsGiven(t *testing.T) {
	// The operator should not have to know the concept exists in order to
	// finish setup. An omitted name gets a sensible one rather than leaving
	// the instance unable to file a ticket.
	h, cleanup := newBareHarness(t)
	defer cleanup()

	c := setupAndSignIn(t, h, map[string]any{
		"email": "admin@example.com", "display_name": "Admin",
		"password": "correct-horse-battery",
	})

	cats := categoriesFor(t, h, c)
	require.Len(t, cats, 1, "an instance with no category cannot file a ticket")
	require.NotEmpty(t, cats[0]["name"])
}

// The property all of the above exists to produce, asserted the way the
// person hitting it would: finish setup, file a ticket, no steps in between.
func TestSetup_ANewInstanceCanFileATicketImmediately(t *testing.T) {
	h, cleanup := newBareHarness(t)
	defer cleanup()

	c := setupAndSignIn(t, h, map[string]any{
		"email": "admin@example.com", "display_name": "Admin",
		"password": "correct-horse-battery", "category": "General",
	})
	cats := categoriesFor(t, h, c)
	require.NotEmpty(t, cats, "no category to file against")

	body := map[string]any{
		"subject":     "First ticket",
		"description": "Filed immediately after setup, with no admin steps in between.",
		"category_id": cats[0]["id"],
		"priority":    "medium",
	}
	resp := h.doUnauthWithCookieAndBody(t, http.MethodPost, "/api/v1/tickets", c, body)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode,
		"a freshly set-up instance could not file a ticket")
}

// Setup does not reopen once a user exists, so a failure between creating the
// administrator and creating the category would strand the instance in
// exactly the state this fixes — with no way back through the UI. The
// category is therefore created FIRST and only when none exists, which makes
// both failure modes retryable: nothing happened, or a category exists and no
// user does. This pins that a retry reuses it rather than stacking duplicates.
func TestSetup_DoesNotAddASecondCategoryOnRetry(t *testing.T) {
	h, cleanup := newBareHarness(t)
	defer cleanup()

	// An attempt that fails on the user, after the category is created.
	first := h.doUnauth(t, http.MethodPost, "/api/v1/setup", map[string]any{
		"email": "admin@example.com", "display_name": "Admin",
		"password": "x", "category": "Hardware",
	})
	first.Body.Close()
	require.NotEqual(t, http.StatusCreated, first.StatusCode,
		"fixture: this attempt was meant to fail on the password")

	c := setupAndSignIn(t, h, map[string]any{
		"email": "admin@example.com", "display_name": "Admin",
		"password": "correct-horse-battery", "category": "Hardware",
	})

	cats := categoriesFor(t, h, c)
	require.Len(t, cats, 1, "the retry created a duplicate category")
}

// doUnauthWithCookieAndBody is doUnauthWithCookie with a request body. The
// existing helper takes no body, and this needs to POST as the freshly
// created administrator.
func (h *harness) doUnauthWithCookieAndBody(t *testing.T, method, path string, c *http.Cookie, body any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, json.NewEncoder(&buf).Encode(body))
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(c)
	rr := httptest.NewRecorder()
	h.srv.ServeHTTP(rr, req)
	return rr.Result()
}
