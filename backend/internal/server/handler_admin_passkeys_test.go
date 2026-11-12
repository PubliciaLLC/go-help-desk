package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// #615: an administrator can list and remove ONE passkey of another user, and
// the two ways of an administrator clearing their OWN factor around the
// factor guard are closed (owner decisions D1, D2, D3).

func adminPasskeysPath(userID uuid.UUID) string {
	return "/api/v1/admin/users/" + userID.String() + "/passkeys"
}

// seedPasskeys gives the user two keys and returns their ids, in the order made.
func seedPasskeys(t *testing.T, h *harness, userID uuid.UUID) (first, second uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, h.passkeyStore.Create(ctx, passkeyFor(userID, "key-one-"+userID.String())))
	require.NoError(t, h.passkeyStore.Create(ctx, passkeyFor(userID, "key-two-"+userID.String())))
	creds, err := h.passkeyStore.ListByUser(ctx, userID)
	require.NoError(t, err)
	require.Len(t, creds, 2)
	return creds[0].ID, creds[1].ID
}

func TestAdminListPasskeys_ShowsTheUsersKeys(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	first, second := seedPasskeys(t, h, h.staffID)
	s := loggedInAdmin(t, h)

	res, body := s.send(t, http.MethodGet, adminPasskeysPath(h.staffID), nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	var got []struct {
		ID   uuid.UUID `json:"id"`
		Name string    `json:"name"`
	}
	require.NoError(t, json.Unmarshal(body, &got))
	require.Len(t, got, 2)
	require.Equal(t, []uuid.UUID{first, second}, []uuid.UUID{got[0].ID, got[1].ID})
	require.Equal(t, "Test key", got[0].Name)

	// Nobody's secret material is in it, same as /me/passkeys.
	require.NotContains(t, string(body), "public_key")
	require.NotContains(t, string(body), "sign_count")
}

func TestAdminListPasskeys_UnknownUserIs404(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	res, body := loggedInAdmin(t, h).send(t, http.MethodGet, adminPasskeysPath(uuid.New()), nil)
	require.Equal(t, http.StatusNotFound, res.StatusCode, "body: %s", body)
}

// Cleanup has to work while passkeys are off (#494): the list never touches
// the ceremony service.
func TestAdminPasskeys_WorkWhilePasskeysAreUnavailable(t *testing.T) {
	h, cleanup := newHarnessPasskeysOff(t)
	defer cleanup()
	first, _ := seedPasskeys(t, h, h.staffID)
	s := loggedInAdmin(t, h)

	res, body := s.send(t, http.MethodGet, adminPasskeysPath(h.staffID), nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	res, body = s.send(t, http.MethodDelete, adminPasskeysPath(h.staffID)+"/"+first.String(), nil)
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
}

func TestAdminRemovePasskey_RemovesOneEndsSessionsAndAudits(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	first, second := seedPasskeys(t, h, h.staffID)
	owner := staffPasswordLogin(t, h)
	require.Equal(t, 1, sessionCount(t, h), "precondition: the owner has a session")
	s := loggedInAdmin(t, h)

	res, body := s.send(t, http.MethodDelete, adminPasskeysPath(h.staffID)+"/"+first.String(), nil)
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)

	creds, err := h.passkeyStore.ListByUser(ctx, h.staffID)
	require.NoError(t, err)
	require.Len(t, creds, 1, "exactly one key goes")
	require.Equal(t, second, creds[0].ID)

	// D1: always, not only when it was the last factor.
	require.Zero(t, sessionCount(t, h), "the account's sessions survived the removal")
	res, _ = owner.send(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	// The administrator's own session is not the account's.
	res, _ = s.send(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusOK, res.StatusCode)

	entries, err := h.auditStore.ListByEntity(ctx, "user", h.staffID, 10, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "passkey_removed_by_admin", entries[0].Action)
	require.NotNil(t, entries[0].ActorID)
	require.Equal(t, h.adminID, *entries[0].ActorID)
	require.Equal(t, first.String(), entries[0].After["passkey_id"])
	require.NotContains(t, entries[0].After, "name", "the key's label is free text and must not be written")
}

// An id is not an authorisation: another user's credential, named under this
// user's path, is not found, is not removed, and ends nobody's session.
func TestAdminRemovePasskey_CredentialOfAnotherUserIsNotFoundAndNothingHappens(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	seedPasskeys(t, h, h.staffID)
	theirs, _ := seedPasskeys(t, h, h.userID)
	staff := staffPasswordLogin(t, h)
	s := loggedInAdmin(t, h)

	res, body := s.send(t, http.MethodDelete, adminPasskeysPath(h.staffID)+"/"+theirs.String(), nil)
	require.Equal(t, http.StatusNotFound, res.StatusCode, "body: %s", body)

	n, err := h.passkeyStore.CountForUser(ctx, h.userID)
	require.NoError(t, err)
	require.EqualValues(t, 2, n, "somebody else's credential was removed through this user's path")
	res, _ = staff.send(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "a refused removal ended the account's sessions")

	entries, err := h.auditStore.ListByEntity(ctx, "user", h.staffID, 10, 0)
	require.NoError(t, err)
	require.Empty(t, entries, "a removal that removed nothing was recorded")
}

// D2: an administrator removing their own key through this route would go
// around the factor guard, since adminRouter checks only that the session
// passed MFA, which a session that owed no factor has without proving one.
func TestAdminRemovePasskey_OwnKeyIsRefusedAndPointsToTheAccountPage(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	mine, _ := seedPasskeys(t, h, h.adminID)
	s := loggedInAdmin(t, h)

	res, body := s.send(t, http.MethodDelete, adminPasskeysPath(h.adminID)+"/"+mine.String(), nil)
	require.Equal(t, http.StatusConflict, res.StatusCode, "body: %s", body)
	require.Contains(t, string(body), "own_account")
	require.Contains(t, string(body), "Account page")

	n, err := h.passkeyStore.CountForUser(context.Background(), h.adminID)
	require.NoError(t, err)
	require.EqualValues(t, 2, n)
}

func TestAdminRemovePasskey_RefusedToKeysAndToNonAdministrators(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	first, _ := seedPasskeys(t, h, h.staffID)
	path := adminPasskeysPath(h.staffID) + "/" + first.String()

	// A key, even an administrator's: it may not change how a person signs in.
	res := h.doAsAdmin(t, http.MethodDelete, path, nil)
	res.Body.Close()
	require.Equal(t, http.StatusForbidden, res.StatusCode)

	// Staff are not administrators.
	res, body := staffPasswordLogin(t, h).send(t, http.MethodDelete, path, nil)
	require.Equal(t, http.StatusForbidden, res.StatusCode, "body: %s", body)

	n, err := h.passkeyStore.CountForUser(context.Background(), h.staffID)
	require.NoError(t, err)
	require.EqualValues(t, 2, n)
}

func TestAdminRemovePasskey_BadIDsAre400(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	s := loggedInAdmin(t, h)
	res, body := s.send(t, http.MethodDelete, adminPasskeysPath(h.staffID)+"/not-a-uuid", nil)
	require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", body)
	res, body = s.send(t, http.MethodDelete, "/api/v1/admin/users/not-a-uuid/passkeys/"+uuid.NewString(), nil)
	require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", body)
}

// A caller that hangs up after the removal commits must not decide whether it
// is recorded or reported as a failure: the same contract as ResetMFA.
func TestAdminRemovePasskey_SucceedsAndIsAuditedEvenIfTheCallerHasDisconnected(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	first, _ := seedPasskeys(t, h, h.staffID)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	actor := h.adminID
	require.NoError(t, h.userSvc.AdminRemovePasskey(canceled, h.staffID, first, &actor))

	n, err := h.passkeyStore.CountForUser(ctx, h.staffID)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	entries, err := h.auditStore.ListByEntity(ctx, "user", h.staffID, 10, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "passkey_removed_by_admin", entries[0].Action)
}

// D3: PATCH /admin/users/{self} with reset_mfa is the same bypass by another
// door, and is refused whole.
func TestAdminUpdateUser_ResetMFAOnYourselfIsRefused(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	enrollMFA(t, ctx, h.userSvc, h.adminID)
	seedPasskeys(t, h, h.adminID)
	s := loggedInAdmin(t, h)

	res, body := s.send(t, http.MethodPatch, "/api/v1/admin/users/"+h.adminID.String(),
		map[string]any{"reset_mfa": true, "display_name": "Renamed"})
	require.Equal(t, http.StatusConflict, res.StatusCode, "body: %s", body)
	require.Contains(t, string(body), "own_account")

	u, err := h.userSvc.GetByID(ctx, h.adminID)
	require.NoError(t, err)
	require.True(t, u.MFAEnabled, "the authenticator was cleared")
	require.NotEqual(t, "Renamed", u.DisplayName, "part of a refused request was applied")
	n, err := h.passkeyStore.CountForUser(ctx, h.adminID)
	require.NoError(t, err)
	require.EqualValues(t, 2, n, "the passkeys were cleared")
	entries, err := h.auditStore.ListByEntity(ctx, "user", h.adminID, 10, 0)
	require.NoError(t, err)
	require.Empty(t, entries)

	// reset_mfa: false asks for nothing, and an ordinary edit of yourself is
	// untouched.
	res, body = s.send(t, http.MethodPatch, "/api/v1/admin/users/"+h.adminID.String(),
		map[string]any{"reset_mfa": false, "display_name": "Renamed"})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
}
