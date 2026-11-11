package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// An OAuth client acts as its service user, which is made with the client and
// starts as staff. A scope no route a staff account can reach uses can only
// mislead, so creation refuses it the way it does for a key (#450).
func TestAdminCreateOAuthClient_ScopesNeverExceedTheServiceUsersRole(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	cases := []struct {
		name   string
		scopes []string
		want   int
		code   string
	}{
		{"a staff scope", []string{"tickets:write"}, http.StatusCreated, ""},
		{"an admin-only scope", []string{"users:write"}, http.StatusBadRequest, "invalid_scope"},
		{"admin-only read", []string{"settings:read"}, http.StatusBadRequest, "invalid_scope"},
		{"the audit log, administrators only", []string{"audit:read"}, http.StatusBadRequest, "invalid_scope"},
		{"credentials", []string{"credentials:write"}, http.StatusBadRequest, "invalid_scope"},
		{"one admin-only scope among allowed ones", []string{"tickets:read", "webhooks:read"}, http.StatusBadRequest, "invalid_scope"},
		{"a scope that does not exist", []string{"sprockets:read"}, http.StatusBadRequest, "invalid_scope"},
		{"none", nil, http.StatusBadRequest, "scopes_required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := h.createCredential(t, "/api/v1/admin/oauth-clients",
				map[string]any{"name": "ceiling-" + tc.name, "scopes": tc.scopes})
			b := readAll(t, res)
			require.Equal(t, tc.want, res.StatusCode, "body: %s", b)
			if tc.code != "" {
				require.Equal(t, tc.code, errCode(t, b))
			}
		})
	}

	// Nothing was stored for the refusals: one client, for the accepted request.
	clients, err := h.authStore.ListOAuthClients(context.Background())
	require.NoError(t, err)
	require.Len(t, clients, 1)
}
