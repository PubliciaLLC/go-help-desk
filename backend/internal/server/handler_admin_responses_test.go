package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// #419, response half: bodies that were wrong or stale, and the rules decided
// with them.

// respMap reads a JSON object body and closes it.
func respMap(t *testing.T, res *http.Response) map[string]any {
	t.Helper()
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out), "body: %s", raw)
	return out
}

// respList reads a JSON array body and closes it.
func respList(t *testing.T, res *http.Response) []map[string]any {
	t.Helper()
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	var out []map[string]any
	require.NoError(t, json.Unmarshal(raw, &out), "body: %s", raw)
	return out
}

// errBody is the {error: {code, message}} envelope.
func errBody(t *testing.T, res *http.Response) (code, message string) {
	t.Helper()
	m := respMap(t, res)
	e, ok := m["error"].(map[string]any)
	require.True(t, ok, "not an error envelope: %v", m)
	code, _ = e["code"].(string)
	message, _ = e["message"].(string)
	return code, message
}

// noSQLText fails when a message carries what only the database says.
func noSQLText(t *testing.T, msg string) {
	t.Helper()
	for _, bad := range []string{"SQLSTATE", "violates", "constraint", "duplicate key", "pq:", "pgx", "relation "} {
		require.NotContains(t, strings.ToLower(msg), strings.ToLower(bad), "raw database text in message %q", msg)
	}
}

// ── Webhook PATCH echoes the saved row ───────────────────────────────────────

// Changing the URL clears the hook's last delivery in the database, but the
// response was built from the row read before the update, so it still showed
// the old receiver's result.
func TestWebhookPatch_UrlChangeDoesNotEchoStaleLastDelivery(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	hook := authstore.WebhookConfig{
		ID: uuid.New(), URL: "https://example.com/old", Events: []string{"*"},
		Enabled: true, CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, h.authStore.CreateWebhook(ctx, hook))
	require.NoError(t, h.authStore.RecordWebhookDelivery(ctx, hook.ID, hook.URL,
		authstore.WebhookDelivery{At: time.Now().UTC(), Status: 500, Error: "http_status"}))

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/webhooks/"+hook.ID.String(),
		map[string]any{"url": "https://example.com/new"})
	require.Equal(t, http.StatusOK, res.StatusCode)
	got := respMap(t, res)
	require.Equal(t, "https://example.com/new", got["url"])
	require.Nil(t, got["last_delivery"], "the old receiver's result must not be reported for the new URL")

	// Not touching the URL keeps the result.
	require.NoError(t, h.authStore.RecordWebhookDelivery(ctx, hook.ID, "https://example.com/new",
		authstore.WebhookDelivery{At: time.Now().UTC(), Status: 200}))
	res = h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/webhooks/"+hook.ID.String(),
		map[string]any{"enabled": false})
	require.Equal(t, http.StatusOK, res.StatusCode)
	got = respMap(t, res)
	require.NotNil(t, got["last_delivery"], "a PATCH that leaves the URL alone keeps the last delivery")
}

// ── Names are trimmed and the saved row comes back ───────────────────────────

func TestPatchCTI_TrimsNameAndReturnsSavedRow(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c, tp, it := mkCTI(t, h)

	cases := []struct {
		name, path string
	}{
		{"category", ctiBase + "/" + c.ID.String()},
		{"type", ctiBase + "/" + c.ID.String() + "/types/" + tp.ID.String()},
		{"item", ctiBase + "/" + c.ID.String() + "/types/" + tp.ID.String() + "/items/" + it.ID.String()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := h.doAsAdmin(t, http.MethodPatch, tc.path, map[string]any{"name": "  Renamed " + tc.name + "  "})
			require.Equal(t, http.StatusOK, res.StatusCode)
			require.Equal(t, "Renamed "+tc.name, respMap(t, res)["name"], "response must carry the trimmed, saved name")

			res = h.doAsAdmin(t, http.MethodGet, tc.path, nil)
			if tc.name == "category" {
				require.Equal(t, "Renamed "+tc.name, respMap(t, res)["name"], "stored name must be trimmed")
			} else {
				res.Body.Close()
			}
		})
	}
}

func TestPatchCTI_StillRefusesABlankName(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c, _, _ := mkCTI(t, h)
	res := h.doAsAdmin(t, http.MethodPatch, ctiBase+"/"+c.ID.String(), map[string]any{"name": "   "})
	defer res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestPatchFieldDef_ReturnsSavedTrimmedRow(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	def, err := h.customFieldSvc.CreateFieldDef(context.Background(), "Asset tag", customfield.FieldTypeText, nil, 1)
	require.NoError(t, err)

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/custom-fields/"+def.ID.String(),
		map[string]any{"name": "  Serial  "})
	require.Equal(t, http.StatusOK, res.StatusCode)
	got := respMap(t, res)
	require.Equal(t, "Serial", got["name"])
	require.NotEmpty(t, got["created_at"], "the saved row, not the edited copy: created_at comes from the database")
	stored, err := h.customFieldSvc.GetFieldDef(context.Background(), def.ID)
	require.NoError(t, err)
	require.Equal(t, "Serial", stored.Name)
}

// ── auth_type ────────────────────────────────────────────────────────────────

func TestAdminUsers_AuthTypeFollowsTheSubjectsPresent(t *testing.T) {
	cases := []struct {
		name     string
		password bool
		saml     string
		oidc     string
		want     string
	}{
		{"password only", true, "", "", "local"},
		{"nothing yet", false, "", "", "local"},
		{"saml only", false, "s1", "", "saml"},
		{"oidc only", false, "", "o1", "oidc"},
		{"password and saml", true, "s2", "", "both"},
		{"password and oidc", true, "", "o2", "local+oidc"},
		{"saml and oidc", false, "s3", "o3", "saml+oidc"},
		{"all three", true, "s4", "o4", "local+saml+oidc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			ctx := context.Background()
			u, err := h.userSvc.Create(ctx, user.CreateUserInput{
				Email: "auth-" + uuid.NewString()[:8] + "@t.local", DisplayName: "A", Role: user.RoleUser, Password: "password",
			})
			require.NoError(t, err)
			_, err = h.tx.ExecContext(ctx,
				`UPDATE users SET password_hash = CASE WHEN $2 THEN password_hash ELSE '' END,
				                  saml_subject = $3, oidc_subject = $4 WHERE id = $1`,
				u.ID, tc.password, tc.saml, tc.oidc)
			require.NoError(t, err)

			res := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/users/"+u.ID.String(), nil)
			require.Equal(t, tc.want, respMap(t, res)["auth_type"], "detail")

			res = h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/users", nil)
			var seen bool
			for _, row := range respList(t, res) {
				if row["id"] == u.ID.String() {
					seen = true
					require.Equal(t, tc.want, row["auth_type"], "list")
				}
			}
			require.True(t, seen)
		})
	}
}

// ── PATCH users: passkey_count ───────────────────────────────────────────────

func TestAdminPatchUser_ReportsPasskeyCount(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.passkeyStore.Create(ctx, passkeyFor(h.userID, "one")))
	require.NoError(t, h.passkeyStore.Create(ctx, passkeyFor(h.userID, "two")))

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/users/"+h.userID.String(),
		map[string]any{"display_name": "Renamed"})
	require.Equal(t, http.StatusOK, res.StatusCode)
	got := respMap(t, res)
	require.Equal(t, float64(2), got["passkey_count"])
}

// ── Status ticket_count ──────────────────────────────────────────────────────

func mkTicketInStatus(t *testing.T, h *harness, statusID uuid.UUID) {
	t.Helper()
	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "s", Description: "d", CategoryID: h.catID, ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)
	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), map[string]any{"status_id": statusID})
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func mkStatus(t *testing.T, h *harness, name string) string {
	t.Helper()
	res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/statuses", map[string]any{"name": name, "sort_order": 50})
	require.Equal(t, http.StatusCreated, res.StatusCode)
	body := respMap(t, res)
	require.Contains(t, body, "ticket_count")
	require.Equal(t, float64(0), body["ticket_count"], "a new status holds no tickets")
	return body["id"].(string)
}

func TestStatusPatch_CountsTheStatusesTickets(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id := mkStatus(t, h, "Waiting on vendor")
	sid := uuid.MustParse(id)
	mkTicketInStatus(t, h, sid)
	mkTicketInStatus(t, h, sid)

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/statuses/"+id, map[string]any{"color": "#112233"})
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, float64(2), respMap(t, res)["ticket_count"])
}

// Reporting 0 for a count that failed told an administrator the status was
// empty, and the page offers hard-delete on that basis.
func TestStatusCount_FailureFailsTheRequest(t *testing.T) {
	for _, tc := range []struct {
		name, method string
		body         any
		status       int
	}{
		{"list", http.MethodGet, nil, http.StatusOK},
		{"patch", http.MethodPatch, map[string]any{"color": "#000000"}, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			id := mkStatus(t, h, "Parked")
			// Counting reads tickets; nothing else here does.
			_, err := h.tx.ExecContext(context.Background(), `ALTER TABLE tickets RENAME TO tickets_gone`)
			require.NoError(t, err)

			path := "/api/v1/admin/statuses"
			if tc.method == http.MethodPatch {
				path += "/" + id
			}
			res := h.doAsAdmin(t, tc.method, path, tc.body)
			defer res.Body.Close()
			require.Equal(t, http.StatusInternalServerError, res.StatusCode)
		})
	}
}

// ── Custom-field errors ──────────────────────────────────────────────────────

func TestCustomFieldErrors_StatusAndCode(t *testing.T) {
	cases := []struct {
		name   string
		act    func(t *testing.T, h *harness) *http.Response
		status int
		code   string
	}{
		{"create blank name is a validation error",
			func(t *testing.T, h *harness) *http.Response {
				return h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/custom-fields",
					map[string]any{"name": " ", "field_type": "text"})
			}, 400, "bad_request"},
		{"create select without options is a validation error",
			func(t *testing.T, h *harness) *http.Response {
				return h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/custom-fields",
					map[string]any{"name": "Pick", "field_type": "select"})
			}, 400, "bad_request"},
		{"create with a taken name is a conflict",
			func(t *testing.T, h *harness) *http.Response {
				_, err := h.customFieldSvc.CreateFieldDef(context.Background(), "Taken", customfield.FieldTypeText, nil, 1)
				require.NoError(t, err)
				return h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/custom-fields",
					map[string]any{"name": "Taken", "field_type": "text"})
			}, 409, "conflict"},
		{"patch to a taken name is a conflict",
			func(t *testing.T, h *harness) *http.Response {
				ctx := context.Background()
				_, err := h.customFieldSvc.CreateFieldDef(ctx, "One", customfield.FieldTypeText, nil, 1)
				require.NoError(t, err)
				two, err := h.customFieldSvc.CreateFieldDef(ctx, "Two", customfield.FieldTypeText, nil, 1)
				require.NoError(t, err)
				return h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/custom-fields/"+two.ID.String(),
					map[string]any{"name": "One"})
			}, 409, "conflict"},
		{"patch with an invalid type is a validation error",
			func(t *testing.T, h *harness) *http.Response {
				d, err := h.customFieldSvc.CreateFieldDef(context.Background(), "Typed", customfield.FieldTypeText, nil, 1)
				require.NoError(t, err)
				return h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/custom-fields/"+d.ID.String(),
					map[string]any{"field_type": "colour"})
			}, 400, "bad_request"},
		{"assigning the same field twice is a conflict",
			func(t *testing.T, h *harness) *http.Response {
				c, _, _ := mkCTI(t, h)
				d, err := h.customFieldSvc.CreateFieldDef(context.Background(), "Dup", customfield.FieldTypeText, nil, 1)
				require.NoError(t, err)
				_, err = h.customFieldSvc.CreateAssignment(context.Background(), d.ID, customfield.CategoryScope, c.ID, 1, true, false)
				require.NoError(t, err)
				return h.doAsAdmin(t, http.MethodPost, ctiBase+"/"+c.ID.String()+"/fields",
					map[string]any{"field_def_id": d.ID})
			}, 409, "conflict"},
		{"assigning a field that does not exist is an invalid reference",
			func(t *testing.T, h *harness) *http.Response {
				c, _, _ := mkCTI(t, h)
				return h.doAsAdmin(t, http.MethodPost, ctiBase+"/"+c.ID.String()+"/fields",
					map[string]any{"field_def_id": uuid.New()})
			}, 400, "invalid_reference"},
		{"a database failure is a 500 with nothing from the database in it",
			func(t *testing.T, h *harness) *http.Response {
				_, err := h.tx.ExecContext(context.Background(), `ALTER TABLE custom_field_defs RENAME TO custom_field_defs_gone`)
				require.NoError(t, err)
				return h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/custom-fields",
					map[string]any{"name": "Anything", "field_type": "text"})
			}, 500, "internal_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			res := tc.act(t, h)
			require.Equal(t, tc.status, res.StatusCode)
			code, msg := errBody(t, res)
			require.Equal(t, tc.code, code)
			noSQLText(t, msg)
		})
	}
}

// ── visible_on_new ───────────────────────────────────────────────────────────

func TestAssignment_VisibleOnNewDefaultsToFalseAndHonoursTrue(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c, _, _ := mkCTI(t, h)
	mk := func(extra map[string]any) map[string]any {
		d, err := h.customFieldSvc.CreateFieldDef(context.Background(), "F-"+uuid.NewString()[:8], customfield.FieldTypeText, nil, 1)
		require.NoError(t, err)
		body := map[string]any{"field_def_id": d.ID}
		for k, v := range extra {
			body[k] = v
		}
		res := h.doAsAdmin(t, http.MethodPost, ctiBase+"/"+c.ID.String()+"/fields", body)
		require.Equal(t, http.StatusCreated, res.StatusCode)
		return respMap(t, res)
	}
	require.Equal(t, false, mk(nil)["visible_on_new"], "omitted keeps today's API default")
	require.Equal(t, true, mk(map[string]any{"visible_on_new": true})["visible_on_new"])
	require.Equal(t, false, mk(map[string]any{"visible_on_new": false})["visible_on_new"])
}

func TestAssignment_ColumnDefaultMatchesTheAPIDefault(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	var def string
	require.NoError(t, h.tx.QueryRowContext(context.Background(),
		`SELECT column_default FROM information_schema.columns
		  WHERE table_name = 'custom_field_assignments' AND column_name = 'visible_on_new'`).Scan(&def))
	require.Equal(t, "false", def)
}

// ── SLA policy list order ────────────────────────────────────────────────────

func TestSLAPolicyList_OrdersBySeverityNotAlphabet(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	mk := func(name string, priority any) {
		body := map[string]any{"name": name, "response_target_min": 10, "resolution_target_min": 60}
		if priority != nil {
			body["priority"] = priority
		}
		res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/sla/policies", body)
		res.Body.Close()
		require.Equal(t, http.StatusCreated, res.StatusCode)
	}
	mk("m", "medium")
	mk("l", "low")
	mk("any", nil)
	mk("c", "critical")
	mk("h2", "high")
	mk("h1", "high")

	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/sla/policies", nil)
	var got []string
	for _, p := range respList(t, res) {
		got = append(got, p["name"].(string))
	}
	require.Equal(t, []string{"c", "h1", "h2", "m", "l", "any"}, got)
}

// ── OAuth client creation returns its id ─────────────────────────────────────

func TestCreateOAuthClient_ReturnsID(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	res := h.createCredential(t, "/api/v1/admin/oauth-clients",
		map[string]any{"name": "ci", "scopes": []string{"tickets:read"}})
	require.Equal(t, http.StatusCreated, res.StatusCode)
	created := respMap(t, res)
	id, _ := created["id"].(string)
	require.NotEmpty(t, id)

	res = h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/oauth-clients", nil)
	var listed bool
	for _, c := range respList(t, res) {
		if c["id"] == id {
			listed = true
			require.Equal(t, created["client_id"], c["client_id"])
		}
	}
	require.True(t, listed, "the id in the 201 body is the id the list and DELETE use")
}

// ── API keys: a signed-in admin sees every key, a key sees its owner's ───────

func keyIDsOf(t *testing.T, h *harness, uid uuid.UUID) []uuid.UUID {
	t.Helper()
	keys, err := h.authStore.ListByUser(context.Background(), uid)
	require.NoError(t, err)
	out := make([]uuid.UUID, len(keys))
	for i, k := range keys {
		out[i] = k.ID
	}
	return out
}

func TestAPIKeys_SignedInAdminListsEveryKeyWithItsOwner(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)
	staffKey := keyIDsOf(t, h, h.staffID)
	require.NotEmpty(t, staffKey)
	adminKey := keyIDsOf(t, h, h.adminID)
	require.NotEmpty(t, adminKey)

	res, raw := sess.send(t, http.MethodGet, "/api/v1/admin/api-keys", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", raw)
	var rows []map[string]any
	require.NoError(t, json.Unmarshal(raw, &rows))
	byID := map[string]map[string]any{}
	for _, r := range rows {
		byID[r["id"].(string)] = r
	}
	require.Contains(t, byID, staffKey[0].String(), "another user's key must be listed")
	require.Contains(t, byID, adminKey[0].String())
	staff := byID[staffKey[0].String()]
	require.Equal(t, h.staffID.String(), staff["user_id"])
	require.NotEmpty(t, staff["owner_email"], "the owner is shown")
	require.NotEmpty(t, staff["owner_name"])
	require.NotContains(t, string(raw), "hashed_token")
}

func TestAPIKeys_SignedInAdminRevokesAnotherUsersKey(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)
	staffKey := keyIDsOf(t, h, h.staffID)[0]

	res, raw := sess.send(t, http.MethodDelete, "/api/v1/admin/api-keys/"+staffKey.String(), nil)
	require.Equal(t, http.StatusNoContent, res.StatusCode, "%s", raw)
	require.Empty(t, keyIDsOf(t, h, h.staffID))
}

// An API key sees and revokes only itself (Erik, 2026-10-13, tightened): not its
// owner's other keys, and not anyone else's.
func TestAPIKeys_AKeyListsOnlyItself(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	other := keyIDsOf(t, h, h.staffID)[0]
	mine := keyIDsOf(t, h, h.adminID)[0]
	// A second key of the same owner: still not the calling key.
	sibling := auth.APIKey{ID: uuid.New(), Name: "sibling", HashedToken: auth.HashToken("s" + uuid.NewString()),
		UserID: h.adminID, Scopes: allScopes(), CreatedAt: time.Now()}
	require.NoError(t, h.authStore.CreateAPIKey(context.Background(), sibling))

	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/api-keys", nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	var ids []string
	for _, r := range respList(t, res) {
		ids = append(ids, r["id"].(string))
	}
	require.Equal(t, []string{mine.String()}, ids, "the calling key and nothing else")
	require.NotContains(t, ids, other.String())
	require.NotContains(t, ids, sibling.ID.String())
}

func TestAPIKeys_AKeyRevokesOnlyItself(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	other := keyIDsOf(t, h, h.staffID)[0]
	mine := keyIDsOf(t, h, h.adminID)[0]
	sibling := auth.APIKey{ID: uuid.New(), Name: "sibling", HashedToken: auth.HashToken("s" + uuid.NewString()),
		UserID: h.adminID, Scopes: allScopes(), CreatedAt: time.Now()}
	require.NoError(t, h.authStore.CreateAPIKey(context.Background(), sibling))

	for name, id := range map[string]uuid.UUID{"another user's key": other, "its owner's other key": sibling.ID} {
		res := h.doAsAdmin(t, http.MethodDelete, "/api/v1/admin/api-keys/"+id.String(), nil)
		res.Body.Close()
		require.Equal(t, http.StatusNotFound, res.StatusCode, name+": not found, not forbidden, so it says nothing about whose key it is")
	}
	require.Contains(t, keyIDsOf(t, h, h.staffID), other)
	require.Contains(t, keyIDsOf(t, h, h.adminID), sibling.ID)

	res := h.doAsAdmin(t, http.MethodDelete, "/api/v1/admin/api-keys/"+mine.String(), nil)
	res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode, "a key may revoke itself")
	require.NotContains(t, keyIDsOf(t, h, h.adminID), mine)
}

// ── Inactive statuses are retired for everyone ───────────────────────────────

func TestInactiveStatus_RefusesMovesInForEveryRoleAndLetsTicketsOut(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	retired := mkStatus(t, h, "Retired")
	live := mkStatus(t, h, "Live")
	newTicket := func() ticket.Ticket {
		tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
			Subject: "s", Description: "d", CategoryID: h.catID, ReporterUserID: &h.staffID,
		})
		require.NoError(t, err)
		return tk
	}
	sitting := newTicket()
	setStatus := func(tk ticket.Ticket, id string) {
		res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), map[string]any{"status_id": id})
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
	}
	setStatus(sitting, retired)

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/statuses/"+retired, map[string]any{"active": false})
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	t.Run("a ticket already in it moves out", func(t *testing.T) {
		res := h.do(t, http.MethodPatch, "/api/v1/tickets/"+sitting.ID.String(), map[string]any{"status_id": live})
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
	})
	for _, who := range []struct {
		name string
		do   func(*testing.T, string, string, any) *http.Response
	}{
		{"staff", h.do},
		{"admin", h.doAsAdmin},
	} {
		t.Run(who.name+" cannot move a ticket into it", func(t *testing.T) {
			tk := newTicket()
			res := who.do(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), map[string]any{"status_id": retired})
			require.Equal(t, http.StatusConflict, res.StatusCode)
			code, _ := errBody(t, res)
			require.Equal(t, "status_inactive", code)
			after, err := h.ticketSvc.GetByID(ctx, tk.ID)
			require.NoError(t, err)
			require.NotEqual(t, retired, after.StatusID.String())
		})
	}
	t.Run("resending the status it already holds is not a move", func(t *testing.T) {
		tk := newTicket()
		res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/statuses/"+live, map[string]any{"active": true})
		res.Body.Close()
		setStatus(tk, live)
		res = h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/statuses/"+live, map[string]any{"active": false})
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
		res = h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(),
			map[string]any{"status_id": live, "assignee_user_id": h.staffID})
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
	})
}
