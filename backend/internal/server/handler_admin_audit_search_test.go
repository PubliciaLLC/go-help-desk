package server_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// adminAuditHits runs the admin-wide view with q and returns the entry ids of
// the tickets it found.
func adminAuditHits(t *testing.T, h *harness, q string) map[string]bool {
	t.Helper()
	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/audit?limit=500&q="+url.QueryEscape(q), nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	var page struct {
		Entries []maskEntry `json:"entries"`
	}
	decodeJSON(t, res, &page)
	res.Body.Close()
	out := map[string]bool{}
	for _, e := range page.Entries {
		out[e.EntityID] = true
	}
	return out
}

// #629(b), D34: q finds an actor by display name and by email.
func TestAdminAudit_QFindsAnActorByNameAndEmail(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.userSvc.UpdateProfile(ctx, h.staffID, "zelda.zenith@test.local", "Zelda Zenith"))
	id := createTicketVia(t, h, h.do)

	require.True(t, adminAuditHits(t, h, "zenith")[id], "by display name")
	require.True(t, adminAuditHits(t, h, "ZELDA.zen")[id], "by email, whatever the case")
	require.False(t, adminAuditHits(t, h, "nobody-by-this-name")[id])
}

// A requester is not found by name while the admin log masks requester names,
// and is found, and named, when it does not. Every value of the setting is
// covered, because the choice decides which views mask.
func TestAdminAudit_QDoesNotFindAMaskedRequesterByName(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.userSvc.UpdateProfile(ctx, h.userID, "quentin.quasar@test.local", "Quentin Quasar"))
	id := createTicketVia(t, h, h.doAsUser)

	for _, tc := range []struct {
		setting   string
		wantFound bool
	}{
		{admin.MaskRequesterNamesEverywhere, false},
		{admin.MaskRequesterNamesAdminLog, false},
		{admin.MaskRequesterNamesTicketLog, true}, // the admin log names requesters
	} {
		t.Run(tc.setting, func(t *testing.T) {
			require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyAuditMaskRequesterNames, tc.setting))
			require.Equal(t, tc.wantFound, adminAuditHits(t, h, "quasar")[id], "by name")
			require.Equal(t, tc.wantFound, adminAuditHits(t, h, "quentin.quasar@")[id], "by email")
		})
	}

	// The mask narrows the name match and nothing else: the same entry is still
	// found by what it says, so the log stays searchable.
	require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyAuditMaskRequesterNames, admin.MaskRequesterNamesEverywhere))
	require.True(t, adminAuditHits(t, h, "created")[id])
}

// q is bounded, because it is sent to the database once for every searchable
// key. The bound is the documented 200 characters, counted as characters.
func TestAdminAudit_QLengthIsBounded(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ok := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/audit?q="+url.QueryEscape(strings.Repeat("é", 200)), nil)
	require.Equal(t, http.StatusOK, ok.StatusCode, "200 characters, though 400 bytes")
	ok.Body.Close()
	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/audit?q="+url.QueryEscape(strings.Repeat("a", 201)), nil)
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
	res.Body.Close()
}

// q reaches the change details, but only the part the view shows (#629a, D33).
// The ticket's subject is written into every ticket entry and is never shown, so
// searching for it must find nothing, or the search would confirm what the
// allow-list withholds.
func TestAdminAudit_QSearchesShownDetailsNotHiddenOnes(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id := createTicketVia(t, h, h.doAsUser) // subject "mask test"
	plantEntry(t, h, id, "planted_detail", nil, map[string]any{
		"scope_path": "Hardware › Laptop", "subject": "Quarterly payroll breach", "api_key": "sk-live-abc",
	})

	require.True(t, adminAuditHits(t, h, "laptop")[id], "a shown value")
	require.False(t, adminAuditHits(t, h, "payroll breach")[id], "a hidden value, planted next to a shown one")
	require.False(t, adminAuditHits(t, h, "sk-live")[id], "a secret-looking key")
	require.False(t, adminAuditHits(t, h, "mask test")[id], "the subject every ticket entry carries")
}
