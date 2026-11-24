package user_test

import (
	"context"
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/stretchr/testify/require"
)

// These tests pin the behaviour UpsertSAMLUser *must* have.
//
// PR #59 fixed six defects in the OIDC path and repeatedly cited the SAML path
// as the correct reference to copy — "the SAML path next door already did most
// of this correctly and is the shape these fixes follow". Nobody checked that
// claim. SAML had 3 test functions to OIDC's 32.
//
// It was half true. SAML genuinely is safer in one important way: it never
// adopts an account by email address, so the account-takeover defect that
// shipped in OIDC cannot happen here. But four of the defects #59 fixed in
// OIDC are present in SAML, unchanged, and were simply never looked for.
//
// Tests below that fail are defects, named in each case.

// TestUpsertSAMLUser_StoreFailureIsNotNotFound pins DEFECT A.
//
// UpsertSAMLUser opens with `if err == nil` and has no other error branch, so
// ANY failure from GetBySAMLSubject — a dropped connection, a timeout, a
// context cancellation — is indistinguishable from "no such row" and falls
// straight through to the create path.
//
// The consequence is worse than a failed login: a user whose account already
// exists gets a SECOND account created during a transient database blip, and
// the unique index on saml_subject then rejects it, so the login fails anyway
// after a write attempt. This is the same defect #59 fixed as OIDC defect 6.
func TestUpsertSAMLUser_StoreFailureIsNotNotFound(t *testing.T) {
	store := newFakeUserStore()
	store.errGetBySAML = errStoreDown
	svc := user.NewService(store)

	_, err := svc.UpsertSAMLUser(context.Background(),
		"saml-subject-1", "person@example.com", "A Person", nil)

	require.Error(t, err, "a store failure must not be treated as a missing row")
	require.ErrorIs(t, err, errStoreDown, "the underlying store error must propagate")
	require.Equal(t, 0, store.creates, "a store failure must never create an account")
	require.Equal(t, 0, store.updates, "a store failure must never mutate an account")
}

// TestUpsertSAMLUser_RejectsEmptySubject pins DEFECT B.
//
// The unique index on saml_subject is partial, excluding the empty string,
// because the empty string is the "not federated" sentinel every local account
// carries. A lookup for the empty subject therefore never matches.
//
// So an assertion with an empty NameID misses on lookup, falls to the create
// path, and stores yet another account whose saml_subject is empty — every
// time. Repeated logins mint unlimited orphan accounts rather than failing.
// OIDC returns ErrSubjectRequired for exactly this; SAML has no such guard.
func TestUpsertSAMLUser_RejectsEmptySubject(t *testing.T) {
	for _, subject := range []string{"", "   "} {
		store := newFakeUserStore()
		svc := user.NewService(store)

		_, err := svc.UpsertSAMLUser(context.Background(),
			subject, "nameless@example.com", "No Subject", nil)

		require.Error(t, err, "an assertion with no NameID must be refused, got subject %q", subject)
		require.ErrorIs(t, err, user.ErrSubjectRequired)
		require.Equal(t, 0, store.creates, "no account may be created without a subject")
	}
}

// TestUpsertSAMLUser_DoesNotBlankStoredEmail pins DEFECT C.
//
// The profile sync assigns u.Email unconditionally. An IdP that stops releasing
// the email attribute — a changed attribute-release policy, a claims-mapping
// mistake — therefore blanks the stored address on the user's next login.
//
// The damage compounds: the email column is unique, so the SECOND user this
// happens to collides on the empty string and cannot log in at all. #59 fixed
// this in OIDC by syncing only when non-empty.
func TestUpsertSAMLUser_DoesNotBlankStoredEmail(t *testing.T) {
	existing := seedUser("keep-me@example.com", "Keep Me", user.RoleUser)
	existing.SAMLSubject = "saml-keep-email"

	store := newFakeUserStore()
	store.seed(existing)
	svc := user.NewService(store)

	got, err := svc.UpsertSAMLUser(context.Background(),
		"saml-keep-email", "", "Keep Me", nil)
	require.NoError(t, err)

	require.Equal(t, "keep-me@example.com", got.Email,
		"an absent email attribute must leave the stored address alone")
}

// TestUpsertSAMLUser_DoesNotBlankStoredDisplayName pins DEFECT D.
//
// Same shape as DEFECT C for the display name, which is likewise assigned
// unconditionally. The handler's fallback chain makes this hard to reach from
// a real IdP, but the service must not depend on its caller for the invariant:
// UpsertSAMLUser is exported and Validate() rejects an empty display name, so
// this turns a login into a 500.
func TestUpsertSAMLUser_DoesNotBlankStoredDisplayName(t *testing.T) {
	existing := seedUser("keepname@example.com", "Real Name", user.RoleUser)
	existing.SAMLSubject = "saml-keep-name"

	store := newFakeUserStore()
	store.seed(existing)
	svc := user.NewService(store)

	got, err := svc.UpsertSAMLUser(context.Background(),
		"saml-keep-name", "keepname@example.com", "", nil)
	require.NoError(t, err)

	require.Equal(t, "Real Name", got.DisplayName,
		"an absent display-name attribute must leave the stored name alone")
}

// ── Behaviour that is already correct, pinned so it stays that way ───────────

// TestUpsertSAMLUser_NewUserGetsRoleUser is the property #59 held SAML up as
// the reference for. It passes today; it exists so that it cannot regress the
// way the OIDC path did.
func TestUpsertSAMLUser_NewUserGetsRoleUser(t *testing.T) {
	store := newFakeUserStore()
	svc := user.NewService(store)

	got, err := svc.UpsertSAMLUser(context.Background(),
		"saml-new", "new@example.com", "New Person", nil)
	require.NoError(t, err)

	require.Equal(t, user.RoleUser, got.Role,
		"a federated login must never mint an agent-level account")
}

// TestUpsertSAMLUser_NeverAdoptsAnAccountByEmail is the defect SAML does NOT
// have, and the reason #59 was right to treat it as the safer path. An unknown
// subject must not attach itself to an existing account that happens to share
// the address — least of all an administrator's.
func TestUpsertSAMLUser_NeverAdoptsAnAccountByEmail(t *testing.T) {
	admin := seedUser("admin@example.com", "The Admin", user.RoleAdmin)

	store := newFakeUserStore()
	store.seed(admin)
	svc := user.NewService(store)

	got, err := svc.UpsertSAMLUser(context.Background(),
		"attacker-subject", "admin@example.com", "Not The Admin", nil)

	// Either outcome is acceptable — refusing outright, or creating a separate
	// unprivileged account. What must never happen is the attacker's subject
	// landing on the admin record.
	if err == nil {
		require.NotEqual(t, admin.ID, got.ID,
			"an unknown subject must not bind itself to the admin account")
		require.Equal(t, user.RoleUser, got.Role)
	}
	require.Empty(t, store.byID[admin.ID].SAMLSubject,
		"the admin account must not have acquired a SAML subject")
}

// TestUpsertSAMLUser_DomainCheckIsCaseInsensitive guards a bypass that the
// OIDC path cannot have, because its handler lowercases the address before
// calling. UpsertSAMLUser receives whatever the ACS handler extracted and runs
// the domain check on it directly, so a mixed-case domain must still match.
func TestUpsertSAMLUser_DomainCheckIsCaseInsensitive(t *testing.T) {
	store := newFakeUserStore()
	svc := user.NewService(store)

	_, err := svc.UpsertSAMLUser(context.Background(),
		"saml-mixed-case", "Person@Example.COM", "Mixed Case", []string{"example.com"})

	require.NoError(t, err,
		"an allowed domain must match regardless of the case the IdP sends")
}
