package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// #711, the matrix: every caller kind against every combination of the three
// settings and guest submission, over the ticket routes as well as the
// catalogue. The expectations are spelled out by caller kind (policySettings.want),
// never read back from the policy under test.

type send func(method, path string, body any) *http.Response

func (h *harness) bearerSend(t *testing.T, token string) send {
	return func(method, path string, body any) *http.Response {
		t.Helper()
		var buf bytes.Buffer
		if body != nil {
			require.NoError(t, json.NewEncoder(&buf).Encode(body))
		}
		req := httptest.NewRequest(method, path, &buf)
		req.Header.Set("Authorization", "Bearer "+token)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		rr := httptest.NewRecorder()
		h.srv.ServeHTTP(rr, req)
		return rr.Result()
	}
}

func (h *harness) keySend(t *testing.T, token string) send {
	return func(method, path string, body any) *http.Response {
		t.Helper()
		var buf bytes.Buffer
		if body != nil {
			require.NoError(t, json.NewEncoder(&buf).Encode(body))
		}
		req := httptest.NewRequest(method, path, &buf)
		req.Header.Set("Authorization", "ApiKey "+token)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		rr := httptest.NewRecorder()
		h.srv.ServeHTTP(rr, req)
		return rr.Result()
	}
}

type matrixCaller struct {
	name string
	who  string // "guest", "requester", "staff"
	do   send
	// tickets is whether the credential reaches the ticket routes at all (a key
	// with no scopes does not; the catalogue is open to it).
	tickets bool
	// meShape is false for an OAuth client, whose /me is not a person's.
	meShape bool
}

// matrixFixture is a ticket its reporter owns, filed under a type and an item,
// with a value on a field only the item supplies and one on a field the type
// supplies.
type matrixFixture struct {
	tree               choiceTree
	tk                 ticket.Ticket
	itemField, typeFld customfield.FieldDef
}

func buildMatrixFixture(t *testing.T, h *harness) matrixFixture {
	t.Helper()
	ctx := context.Background()
	tree := buildChoiceTree(t, h)
	tk := reporterTicketWithItem(t, h, tree)
	itemF := h.mkField(t, "Exec asset tag", customfield.FieldTypeText, nil, 1)
	typeF := h.mkField(t, "Laptop tag", customfield.FieldTypeText, nil, 2)
	_, err := h.customFieldSvc.Attach(ctx, customfield.Assignment{FieldDefID: itemF.ID, ScopeType: customfield.ItemScope, ScopeID: tree.item, VisibleOnNew: true}, nil)
	require.NoError(t, err)
	_, err = h.customFieldSvc.Attach(ctx, customfield.Assignment{FieldDefID: typeF.ID, ScopeType: customfield.TypeScope, ScopeID: tree.typ, VisibleOnNew: true}, nil)
	require.NoError(t, err)
	require.NoError(t, h.customFieldSvc.SetValues(ctx, tk.ID, map[uuid.UUID]string{itemF.ID: "VIP-1", typeF.ID: "T-1"}))
	return matrixFixture{tree: tree, tk: tk, itemField: itemF, typeFld: typeF}
}

func bodyOf(t *testing.T, res *http.Response) (int, string) {
	t.Helper()
	b := readAll(t, res)
	return res.StatusCode, string(b)
}

func TestCataloguePolicy_MatrixOverTicketRoutes(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	fx := buildMatrixFixture(t, h)
	tree := fx.tree

	// Credentials: a session of each kind, keys, OAuth clients, a key with no scopes.
	reporter := h.session(t, asReporter)
	staffSess := h.session(t, asStaff)
	adminSess := h.session(t, asAdmin)
	sc := h.newServiceClient(t, "staff client", "tickets:read", "tickets:write")
	reqClient := h.newServiceClient(t, "requester client", "tickets:read", "tickets:write")
	if err := h.userSvc.SetRole(ctx, reqClient.UserID, user.RoleUser); err != nil {
		t.Logf("a service account cannot be made a requester (%v); the requester-role OAuth caller is skipped", err)
		reqClient = serviceClient{}
	}
	// A key with a scope that opens no ticket route (a key with no scope at all
	// is refused everywhere, at creation).
	res, b := h.issueFor(t, adminSess, h.staffID, "groups:read")
	require.Equal(t, http.StatusCreated, res.StatusCode, string(b))
	var noTicketScope issuedKey
	require.NoError(t, json.Unmarshal(b, &noTicketScope))

	sess := func(cookie string) send {
		return func(m, p string, body any) *http.Response { return h.doSession(t, cookie, m, p, body) }
	}
	key := func(do func(*testing.T, string, string, any) *http.Response) send {
		return func(m, p string, body any) *http.Response { return do(t, m, p, body) }
	}
	callers := []matrixCaller{
		{"requester session", "requester", sess(reporter), true, true},
		{"requester API key", "requester", key(h.doAsUser), true, true},
		{"staff session", "staff", sess(staffSess), true, true},
		{"staff API key", "staff", key(h.do), true, true},
		{"admin API key", "staff", key(h.doAsAdmin), true, true},
		{"OAuth client (staff service user)", "staff", h.bearerSend(t, sc.Token), true, false},
		{"staff key without a ticket scope", "staff", h.keySend(t, noTicketScope.Token), false, true},
	}
	if reqClient.Token != "" {
		callers = append(callers, matrixCaller{"OAuth client (requester role)", "requester", h.bearerSend(t, reqClient.Token), false, false})
	}

	ticketPath := "/api/v1/tickets/" + fx.tk.ID.String()
	fieldsPath := "/api/v1/tickets/fields?category_id=" + tree.cat.String() + "&type_id=" + tree.typ.String() + "&item_id=" + tree.item.String()
	valuesPath := ticketPath + "/custom-fields"

	for _, set := range allPolicySettings() {
		set.apply(t, h)
		// /site is public and carries the effective guest levels.
		gt, gi := set.want("guest")
		site := siteConfig(t, h)
		require.Equal(t, gt, site[guestsTypesKey], "/site types %s", set)
		require.Equal(t, gi, site[guestsItemsKey], "/site items %s", set)

		for _, c := range callers {
			t.Run(c.name+"/"+set.String(), func(t *testing.T) {
				_, wantItems := set.want(c.who)

				if !c.tickets {
					code, _ := bodyOf(t, c.do(http.MethodGet, ticketPath, nil))
					require.GreaterOrEqual(t, code, 400, "no ticket scope, or not the ticket's reporter: no ticket")
					// The catalogue is open to it, at its owner's levels.
					require.Equal(t, wantItems, len(listNames(t, c.do(http.MethodGet, tree.itemsPath(), nil))) > 0)
					return
				}

				// The ticket, one and in a list.
				code, body := bodyOf(t, c.do(http.MethodGet, ticketPath, nil))
				require.Equal(t, http.StatusOK, code, body)
				_, has := jsonKeys(t, []byte(body))["item_id"]
				require.Equal(t, wantItems, has, "GET /tickets/{id} item_id")
				require.Contains(t, jsonKeys(t, []byte(body)), "type_id")
				listPath := "/api/v1/tickets"
				if c.who == "staff" {
					listPath += "?reporter_id=" + h.userID.String() // staff's default list is not every ticket
				}
				code, body = bodyOf(t, c.do(http.MethodGet, listPath, nil))
				require.Equal(t, http.StatusOK, code, body)
				var rows []map[string]json.RawMessage
				require.NoError(t, json.Unmarshal([]byte(body), &rows))
				seen := false
				for _, row := range rows {
					if strings.Contains(string(row["id"]), fx.tk.ID.String()) {
						seen = true
						_, has := row["item_id"]
						require.Equal(t, wantItems, has, "GET /tickets row item_id")
					}
				}
				require.True(t, seen, "the ticket is in the list")

				// The filter on an item.
				code, body = bodyOf(t, c.do(http.MethodGet, "/api/v1/tickets?item_id="+tree.item.String(), nil))
				if wantItems {
					require.Equal(t, http.StatusOK, code, body)
				} else {
					require.Equal(t, http.StatusForbidden, code, body)
					require.Contains(t, body, "item_id is not available")
				}

				// The fields of a classification.
				code, body = bodyOf(t, c.do(http.MethodGet, fieldsPath, nil))
				require.Equal(t, http.StatusOK, code, body)
				require.Equal(t, wantItems, strings.Contains(body, "Exec asset tag"), "GET /tickets/fields item level")
				require.Equal(t, wantItems, strings.Contains(body, tree.item.String()), "no item id in an assignment")
				require.Contains(t, body, "Laptop tag")

				// The values on the ticket: an item-only field's value is not read...
				code, body = bodyOf(t, c.do(http.MethodGet, valuesPath, nil))
				require.Equal(t, http.StatusOK, code, body)
				require.Equal(t, wantItems, strings.Contains(body, "VIP-"), "GET custom-fields item value")
				require.Contains(t, body, "Laptop tag")
				require.Contains(t, body, `"value":"T-`)
				// ...and not written, with the answer an unknown field gets.
				code, body = bodyOf(t, c.do(http.MethodPut, valuesPath, map[string]string{fx.itemField.ID.String(): "VIP-2"}))
				if wantItems {
					require.Equal(t, http.StatusNoContent, code, body)
				} else {
					require.Equal(t, http.StatusBadRequest, code, body)
					require.Contains(t, body, "is not a field on this ticket")
				}
				code, body = bodyOf(t, c.do(http.MethodPut, valuesPath, map[string]string{fx.typeFld.ID.String(): "T-2"}))
				require.Equal(t, http.StatusNoContent, code, body)

				if !c.meShape {
					return
				}
				code, body = bodyOf(t, c.do(http.MethodGet, "/api/v1/me", nil))
				require.Equal(t, http.StatusOK, code, body)
				require.Equal(t, wantItems, string(jsonKeys(t, []byte(body))["can_choose_item"]) == "true", "/me can_choose_item")
			})
		}
	}
}

// A credential that is not (yet) a person is refused everywhere, whatever the
// settings: a staff session that has not answered the second factor, and a key
// whose owner has been disabled. Nothing about the tree reaches either.
func TestCataloguePolicy_HalfSignedInAndDisabledSeeNothing(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	fx := buildMatrixFixture(t, h)
	tree := fx.tree

	adminSess := h.session(t, asAdmin)
	res, b := h.issueFor(t, adminSess, h.staffID, "tickets:read")
	require.Equal(t, http.StatusCreated, res.StatusCode, string(b))
	var k issuedKey
	require.NoError(t, json.Unmarshal(b, &k))
	disabledOwner := h.keySend(t, k.Token)
	noMFA := h.sessionCookieFor(t, auth.SessionData{UserID: h.staffID, Role: user.RoleStaff, MFAPassed: false})
	require.NoError(t, h.userSvc.Disable(ctx, h.staffID))

	catalogue := map[string]bool{"/api/v1/categories": true, tree.typesPath(): true, tree.itemsPath(): true}
	paths := []string{
		"/api/v1/categories", tree.typesPath(), tree.itemsPath(), "/api/v1/tickets",
		"/api/v1/tickets/" + fx.tk.ID.String(), "/api/v1/tickets/" + fx.tk.ID.String() + "/custom-fields",
		"/api/v1/tickets/fields?category_id=" + tree.cat.String() + "&item_id=" + tree.item.String(), "/api/v1/me",
	}
	for _, set := range allPolicySettings() {
		set.apply(t, h)
		guestTypes, guestItems := set.want("guest")
		// Neither of these is a person who may use the ticket routes: a session
		// that has not answered MFA, and a key whose owner is disabled. Those
		// routes refuse them, and the public catalogue answers them as it answers
		// a guest (while guest submission is on, and refuses them while it is
		// off), at a guest's levels and no further.
		for name, do := range map[string]send{
			"a session that has not answered MFA": func(m, p string, body any) *http.Response { return h.doSession(t, noMFA, m, p, body) },
			"a key whose owner is disabled":       disabledOwner,
		} {
			for _, p := range paths {
				code, body := bodyOf(t, do(http.MethodGet, p, nil))
				if !catalogue[p] || !set.sub {
					require.GreaterOrEqual(t, code, 400, "%s %s %s: %s", name, p, set, body)
					require.NotContains(t, body, tree.itemName)
					continue
				}
				require.Equal(t, http.StatusOK, code, "%s %s: %s", name, p, body)
				switch p {
				case tree.itemsPath():
					require.Equal(t, guestItems, strings.Contains(body, tree.itemName), "%s items %s", name, set)
				case tree.typesPath():
					require.Equal(t, guestTypes, strings.Contains(body, tree.typeName), "%s types %s", name, set)
				default:
					require.NotContains(t, body, tree.itemName)
				}
			}
		}
	}
}
