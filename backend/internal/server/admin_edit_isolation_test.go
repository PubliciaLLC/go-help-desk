package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Demoting somebody revokes their sessions even when the rest of the request
// fails.
//
// The order was: write the role, write the profile, revoke. So any failure in
// between — a blank display name, an address that belongs to somebody else —
// left the demotion committed and the session alive, answering 400. Retrying
// with a good body then saw the role already changed, so it never revoked at
// all: a demoted administrator kept full authority for the life of the
// cookie and could promote themselves straight back.
//
// "Demote this person and fix their address" is an ordinary thing to do in
// one request, and it is exactly the shape that triggered it.
func TestUpdateUser_ADemotionRevokesEvenWhenTheRestFails(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	victim, err := h.userSvc.Create(ctx, user.CreateUserInput{
		Email: "second-admin@test.local", DisplayName: "Second Admin",
		Role: user.RoleAdmin, Password: "password",
	})
	require.NoError(t, err)

	// A live session for them.
	theirs := &session{h: h}
	res, body := theirs.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "second-admin@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	res, _ = theirs.send(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "precondition: the session works")

	// Demote them, with a profile change that will be refused.
	admin := loggedInAdmin(t, h)
	res, body = admin.send(t, http.MethodPatch, "/api/v1/admin/users/"+victim.ID.String(),
		map[string]any{"role": "user", "display_name": "   "})
	require.Equal(t, http.StatusBadRequest, res.StatusCode,
		"the blank name should be refused: %s", body)

	// The demotion happened...
	stored, err := h.userSvc.GetByIDAdmin(ctx, victim.ID)
	require.NoError(t, err)
	require.Equal(t, user.RoleUser, stored.Role)

	// ...so the authority granted under the old role must be gone.
	res, _ = theirs.send(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusUnauthorized, res.StatusCode,
		"a demoted administrator is still holding an administrator session")
}

// The profile write touches the address and the name, and nothing else.
//
// It used to write the WHOLE row from a copy read at the top of the request —
// role, password hash, MFA secret, federated subjects — so anything that
// changed in between was silently written back. Measured at service level:
// read a user for a rename, have them set a new password, let the rename
// land, and the new password is refused while the old one works again. The
// same shape undoes an MFA enrolment and another administrator's role change.
//
// What actually prevents it is the shape: UpdateProfile takes an id, an
// address and a name, so there is nothing else for it to write. This test
// records that and checks the columns survive — it is not a trap a
// regression would spring, because reintroducing the fault means widening
// the method again, and a test cannot catch a signature that has not been
// widened yet.
//
// It is here because the reasoning is worth keeping next to the code. Two
// versions of this test were written before this one: one raced two
// requests, which cannot be staged from outside a handler that reads the row
// fresh, and one mutated the service to re-read, which is not the defect
// either. Both passed against the broken code.
func TestUpdateProfile_WritesOnlyTheAddressAndTheName(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	// Everything about this account changes AFTER a caller would have read
	// it: a new password, a promotion.
	require.NoError(t, h.userSvc.SetPassword(ctx, h.staffID, "a-brand-new-passphrase"))
	require.NoError(t, h.userSvc.SetRole(ctx, h.staffID, user.RoleAdmin))

	// And then the rename lands.
	require.NoError(t, h.userSvc.UpdateProfile(ctx, h.staffID, "renamed@test.local", "Renamed Staff"))

	stored, err := h.userSvc.GetByIDAdmin(ctx, h.staffID)
	require.NoError(t, err)
	require.Equal(t, "renamed@test.local", stored.Email, "the rename should have happened")
	require.Equal(t, "Renamed Staff", stored.DisplayName)

	require.Equal(t, user.RoleAdmin, stored.Role,
		"the rename reverted a role change made in between")

	_, err = h.userSvc.VerifyPassword(ctx, "renamed@test.local", "a-brand-new-passphrase")
	require.NoError(t, err, "the rename wrote an older copy of the row back over the new password")
	_, err = h.userSvc.VerifyPassword(ctx, "renamed@test.local", "password")
	require.Error(t, err, "the old password works again, so the whole row was overwritten")
}

// An unknown category is refused before a tracking number is taken.
//
// Only a reporting user's category was checked, and that check asks whether
// it is open to them rather than whether it exists — so staff and MCP reached
// the foreign key, answered 500, and left a permanent gap in the numbering on
// every attempt.
func TestCreateTicket_AnUnknownCategoryDoesNotBurnANumber(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	before := latestTicketNumber(t, h)

	res := h.do(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "Nowhere", "description": "x",
		"category_id": "11111111-1111-1111-1111-111111111111",
	})
	body, _ := readAllBody(res)
	res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode,
		"an unknown category is the caller's mistake, not an internal error: %s", body)

	after := createTicketAsUser(t, h, "After the refusal")
	require.Equal(t, before+1, trackingSeq(t, after.TrackingNumber),
		"the refused request consumed a tracking number")
}

// Reclassifying a ticket is held to the same rules as classifying one.
//
// The checks were on the create path only, so the pairing that path refuses
// was one PATCH away: a category from one tree with an item from another, or
// an item with no type at all. Group routing keys on this triple, so a ticket
// could be routed on a pairing that does not exist — and an unknown id
// answered 500 from the foreign key instead of saying which id was wrong.
func TestUpdateTicket_ReclassificationIsCheckedToo(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	// Two separate trees.
	other, err := h.categorySvc.CreateCategory(ctx, "Other tree", 90)
	require.NoError(t, err)
	otherType, err := h.categorySvc.CreateType(ctx, other.ID, "Other type", 1)
	require.NoError(t, err)
	otherItem, err := h.categorySvc.CreateItem(ctx, otherType.ID, "Other item", 1)
	require.NoError(t, err)

	tk := createTicketAsUser(t, h, "To be reclassified")

	cases := []struct {
		name string
		body map[string]any
	}{
		{
			name: "an item from another tree",
			body: map[string]any{"category_id": h.catID.String(), "item_id": otherItem.ID.String()},
		},
		{
			name: "an item with no type",
			body: map[string]any{"category_id": other.ID.String(), "item_id": otherItem.ID.String()},
		},
		{
			name: "a category that does not exist",
			body: map[string]any{"category_id": "22222222-2222-2222-2222-222222222222"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := h.do(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), tc.body)
			body, _ := readAllBody(res)
			res.Body.Close()
			require.Equal(t, http.StatusBadRequest, res.StatusCode,
				"stored a classification that does not hang together: %s", body)
		})
	}

	// And an honest reclassification still works.
	res := h.do(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), map[string]any{
		"category_id": other.ID.String(),
		"type_id":     otherType.ID.String(),
		"item_id":     otherItem.ID.String(),
	})
	body, _ := readAllBody(res)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode, "a coherent triple was refused: %s", body)
}
