package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// #615: removing your own passkey ends your OTHER sessions, keeps the one you
// removed it from, and is audited, as a password change and a new factor
// already end the others. A key you remove may be the one somebody else is
// using.

// signedInWithKey is a session that has proved the account's key, so the factor
// guard lets it remove one, plus a second key to remove.
func signedInWithKey(t *testing.T) (*harness, *session, uuid.UUID) {
	t.Helper()
	h, s, key := cloneHarness(t, 0)
	res, body := signInWithKey(t, h, s, key, 0)
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	keepLastCookies(s)

	require.NoError(t, h.passkeyStore.Create(context.Background(), passkeyFor(h.staffID, "second-key")))
	creds, err := h.passkeyStore.ListByUser(context.Background(), h.staffID)
	require.NoError(t, err)
	require.Len(t, creds, 2)
	return h, s, creds[1].ID
}

func TestDeleteOwnPasskey_EndsOtherSessionsKeepsThisOneAndAudits(t *testing.T) {
	h, s, second := signedInWithKey(t)
	ctx := context.Background()
	other := staffPasswordLogin(t, h)
	require.Equal(t, 2, sessionCount(t, h), "precondition: this session and another")

	res, body := s.send(t, http.MethodDelete, "/api/v1/me/passkeys/"+second.String(), nil)
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)

	require.Equal(t, 1, sessionCount(t, h), "the other session survived the removal")
	res, _ = other.send(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	res, body = s.send(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "the session that removed the key was signed out; body: %s", body)

	entries, err := h.auditStore.ListByEntity(ctx, "user", h.staffID, 10, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "passkey_removed", entries[0].Action)
	require.NotNil(t, entries[0].ActorID, "the user is the actor of their own removal")
	require.Equal(t, h.staffID, *entries[0].ActorID)
	require.Equal(t, second.String(), entries[0].After["passkey_id"])
	require.NotContains(t, entries[0].After, "name", "the key's label is free text and must not be written")
}

// A removal that removes nothing ends nothing and records nothing.
func TestDeleteOwnPasskey_AnotherUsersKeyEndsNoSessionAndIsNotRecorded(t *testing.T) {
	h, s, _ := signedInWithKey(t)
	ctx := context.Background()
	theirs, _ := seedPasskeys(t, h, h.userID)
	other := staffPasswordLogin(t, h)

	res, body := s.send(t, http.MethodDelete, "/api/v1/me/passkeys/"+theirs.String(), nil)
	require.Equal(t, http.StatusNotFound, res.StatusCode, "body: %s", body)

	require.Equal(t, 2, sessionCount(t, h), "a removal that removed nothing ended a session")
	res, _ = other.send(t, http.MethodGet, "/api/v1/me", nil)
	require.NotEqual(t, http.StatusInternalServerError, res.StatusCode)
	n, err := h.passkeyStore.CountForUser(ctx, h.userID)
	require.NoError(t, err)
	require.EqualValues(t, 2, n)
	entries, err := h.auditStore.ListByEntity(ctx, "user", h.staffID, 10, 0)
	require.NoError(t, err)
	require.Empty(t, entries)
}

// The factor guard is unchanged: a session that has only proved the password is
// refused, and nothing ends.
func TestDeleteOwnPasskey_PasswordOnlySessionIsRefusedAndNothingEnds(t *testing.T) {
	h, _, _ := cloneHarness(t, 0)
	creds, err := h.passkeyStore.ListByUser(context.Background(), h.staffID)
	require.NoError(t, err)
	weak := staffPasswordLogin(t, h)
	before := sessionCount(t, h)

	res, body := weak.send(t, http.MethodDelete, "/api/v1/me/passkeys/"+creds[0].ID.String(), nil)
	require.Equal(t, http.StatusForbidden, res.StatusCode, "body: %s", body)
	require.Contains(t, string(body), "mfa_required")
	require.Equal(t, before, sessionCount(t, h))
}

// Detached from the caller, as the admin removal is: hanging up after the
// removal commits must not suppress the entry or report a done removal as failed.
func TestRemoveOwnPasskey_SucceedsAndIsAuditedEvenIfTheCallerHasDisconnected(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	first, _ := seedPasskeys(t, h, h.staffID)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, h.userSvc.RemoveOwnPasskey(canceled, h.staffID, first, "current-session"))

	n, err := h.passkeyStore.CountForUser(ctx, h.staffID)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	entries, err := h.auditStore.ListByEntity(ctx, "user", h.staffID, 10, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "passkey_removed", entries[0].Action)
}
