package server_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
)

// DELETE on an id that names nothing answers 404 not_found, not 204 (#418).
// One row per route. Each row also pins the unchanged half: deleting a row
// that exists answers 204, and deleting it a second time answers 404.

type deleteCase struct {
	name string
	// existing creates a row and returns the DELETE path (and body) for it.
	existing func(t *testing.T, h *harness) (path string, body any)
	// unknown returns the DELETE path (and body) for an id that names nothing,
	// under parents that do exist.
	unknown func(t *testing.T, h *harness) (path string, body any)
}

func deleteCases() []deleteCase {
	const admin = "/api/v1/admin"
	return []deleteCase{
		{"group",
			func(t *testing.T, h *harness) (string, any) {
				return admin + "/groups/" + mkGroup(t, h, "G").String(), nil
			},
			func(t *testing.T, h *harness) (string, any) {
				return admin + "/groups/" + uuid.NewString(), nil
			}},
		{"group member",
			func(t *testing.T, h *harness) (string, any) {
				gid := mkGroup(t, h, "G")
				require.NoError(t, h.groupSvc.AddMember(context.Background(), gid, h.staffID))
				return admin + "/groups/" + gid.String() + "/members/" + h.staffID.String(), nil
			},
			func(t *testing.T, h *harness) (string, any) {
				return admin + "/groups/" + mkGroup(t, h, "G").String() + "/members/" + uuid.NewString(), nil
			}},
		{"group scope",
			func(t *testing.T, h *harness) (string, any) {
				gid := mkGroup(t, h, "G")
				require.NoError(t, h.groupSvc.AddScope(context.Background(), group.GroupScope{GroupID: gid, CategoryID: h.catID}))
				return admin + "/groups/" + gid.String() + "/scopes", map[string]any{"category_id": h.catID}
			},
			func(t *testing.T, h *harness) (string, any) {
				return admin + "/groups/" + mkGroup(t, h, "G").String() + "/scopes", map[string]any{"category_id": h.catID}
			}},
		{"api key",
			func(t *testing.T, h *harness) (string, any) {
				_, hashed, err := auth.GenerateToken()
				require.NoError(t, err)
				k := auth.APIKey{ID: uuid.New(), Name: "k", HashedToken: hashed, UserID: h.adminID,
					Scopes: allScopes(), CreatedAt: time.Now()}
				require.NoError(t, h.authStore.CreateAPIKey(context.Background(), k))
				return admin + "/api-keys/" + k.ID.String(), nil
			},
			func(t *testing.T, h *harness) (string, any) {
				return admin + "/api-keys/" + uuid.NewString(), nil
			}},
		{"oauth client",
			func(t *testing.T, h *harness) (string, any) {
				c := auth.OAuthClient{ID: uuid.New(), ClientID: "c-" + uuid.NewString(), HashedSecret: "x",
					Name: "c", Scopes: allScopes(), CreatedAt: time.Now(), UserID: uuid.New()}
				require.NoError(t, h.authStore.CreateOAuthClient(context.Background(), c))
				return admin + "/oauth-clients/" + c.ID.String(), nil
			},
			func(t *testing.T, h *harness) (string, any) {
				return admin + "/oauth-clients/" + uuid.NewString(), nil
			}},
		{"webhook",
			func(t *testing.T, h *harness) (string, any) {
				wh := authstore.WebhookConfig{ID: uuid.New(), URL: "https://example.com/h", Events: []string{"*"},
					Enabled: true, CreatedAt: time.Now(), PayloadFormat: "raw"}
				require.NoError(t, h.authStore.CreateWebhook(context.Background(), wh))
				return admin + "/webhooks/" + wh.ID.String(), nil
			},
			func(t *testing.T, h *harness) (string, any) {
				return admin + "/webhooks/" + uuid.NewString(), nil
			}},
		{"category",
			func(t *testing.T, h *harness) (string, any) {
				c, _, _ := mkCTI(t, h)
				return admin + "/categories/" + c.ID.String(), nil
			},
			func(t *testing.T, h *harness) (string, any) {
				return admin + "/categories/" + uuid.NewString(), nil
			}},
		{"type",
			func(t *testing.T, h *harness) (string, any) {
				c, tp, _ := mkCTI(t, h)
				return admin + "/categories/" + c.ID.String() + "/types/" + tp.ID.String(), nil
			},
			func(t *testing.T, h *harness) (string, any) {
				c, _, _ := mkCTI(t, h)
				return admin + "/categories/" + c.ID.String() + "/types/" + uuid.NewString(), nil
			}},
		{"item",
			func(t *testing.T, h *harness) (string, any) {
				c, tp, it := mkCTI(t, h)
				return admin + "/categories/" + c.ID.String() + "/types/" + tp.ID.String() + "/items/" + it.ID.String(), nil
			},
			func(t *testing.T, h *harness) (string, any) {
				c, tp, _ := mkCTI(t, h)
				return admin + "/categories/" + c.ID.String() + "/types/" + tp.ID.String() + "/items/" + uuid.NewString(), nil
			}},
		{"category field assignment",
			func(t *testing.T, h *harness) (string, any) {
				c, _, _ := mkCTI(t, h)
				return admin + "/categories/" + c.ID.String() + "/fields/" + mkAssignment(t, h, customfield.CategoryScope, c.ID).String(), nil
			},
			func(t *testing.T, h *harness) (string, any) {
				c, _, _ := mkCTI(t, h)
				return admin + "/categories/" + c.ID.String() + "/fields/" + uuid.NewString(), nil
			}},
		{"type field assignment",
			func(t *testing.T, h *harness) (string, any) {
				c, tp, _ := mkCTI(t, h)
				return admin + "/categories/" + c.ID.String() + "/types/" + tp.ID.String() + "/fields/" +
					mkAssignment(t, h, customfield.TypeScope, tp.ID).String(), nil
			},
			func(t *testing.T, h *harness) (string, any) {
				c, tp, _ := mkCTI(t, h)
				return admin + "/categories/" + c.ID.String() + "/types/" + tp.ID.String() + "/fields/" + uuid.NewString(), nil
			}},
		{"item field assignment",
			func(t *testing.T, h *harness) (string, any) {
				c, tp, it := mkCTI(t, h)
				return admin + "/categories/" + c.ID.String() + "/types/" + tp.ID.String() + "/items/" + it.ID.String() +
					"/fields/" + mkAssignment(t, h, customfield.ItemScope, it.ID).String(), nil
			},
			func(t *testing.T, h *harness) (string, any) {
				c, tp, it := mkCTI(t, h)
				return admin + "/categories/" + c.ID.String() + "/types/" + tp.ID.String() + "/items/" + it.ID.String() +
					"/fields/" + uuid.NewString(), nil
			}},
		{"sla policy",
			func(t *testing.T, h *harness) (string, any) {
				p, err := h.slaSvc.CreatePolicy(context.Background(), sla.Policy{
					Name: "P", ResponseTargetMin: 60, ResolutionTargetMin: 480})
				require.NoError(t, err)
				return admin + "/sla/policies/" + p.ID.String(), nil
			},
			func(t *testing.T, h *harness) (string, any) {
				return admin + "/sla/policies/" + uuid.NewString(), nil
			}},
	}
}

func mkAssignment(t *testing.T, h *harness, scope customfield.ScopeType, scopeID uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	def, err := h.customFieldSvc.CreateFieldDef(ctx, "F-"+uuid.NewString()[:8], customfield.FieldTypeText, nil, 1)
	require.NoError(t, err)
	a, err := h.customFieldSvc.CreateAssignment(ctx, def.ID, scope, scopeID, 1, true, false)
	require.NoError(t, err)
	return a.ID
}

func TestAdminDelete_UnknownIDIs404(t *testing.T) {
	for _, tc := range deleteCases() {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			path, body := tc.unknown(t, h)
			res := h.doAsAdmin(t, http.MethodDelete, path, body)
			defer res.Body.Close()
			require.Equal(t, http.StatusNotFound, res.StatusCode)
			require.Equal(t, "not_found", errorCode(t, res))
		})
	}
}

func TestAdminDelete_ExistingIs204ThenSecondDeleteIs404(t *testing.T) {
	for _, tc := range deleteCases() {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			path, body := tc.existing(t, h)

			first := h.doAsAdmin(t, http.MethodDelete, path, body)
			first.Body.Close()
			require.Equal(t, http.StatusNoContent, first.StatusCode)

			second := h.doAsAdmin(t, http.MethodDelete, path, body)
			defer second.Body.Close()
			require.Equal(t, http.StatusNotFound, second.StatusCode)
			require.Equal(t, "not_found", errorCode(t, second))
		})
	}
}
