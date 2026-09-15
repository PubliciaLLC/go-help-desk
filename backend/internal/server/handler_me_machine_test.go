package server_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// An API key acts at its owner's identity. Before this guard a leaked key was
// full account takeover rather than the access it was issued for: change the
// owner's password, re-enroll MFA against an attacker's authenticator, and the
// human is locked out of their own account by a credential they created for a
// cron job. Revoking the key afterwards undoes neither.
//
// Scopes cannot solve this. The narrowest possible key still belongs to its
// owner, so any scope reaching these routes reaches account takeover.
func TestMachineCredential_CannotTakeOverTheAccount(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	for _, tc := range []struct {
		name, method, path string
		body               any
	}{
		{"change the owner's password", http.MethodPatch, "/api/v1/me/password",
			map[string]any{"password": "attacker-chosen-password-123"}},
		{"start MFA re-enrollment", http.MethodPost, "/api/v1/me/mfa/enroll", nil},
		{"confirm MFA re-enrollment", http.MethodPost, "/api/v1/me/mfa/enroll/confirm",
			map[string]any{"code": "000000"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := h.do(t, tc.method, tc.path, tc.body)
			require.Equal(t, http.StatusForbidden, resp.StatusCode,
				"a machine credential must not reach %s", tc.path)

			var body struct {
				Error struct{ Code string } `json:"error"`
			}
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
			require.Equal(t, "session_required", body.Error.Code)
		})
	}
}

// The password must be unchanged afterwards, not merely refused at the door.
func TestMachineCredential_PasswordChangeDoesNotTakeEffect(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.do(t, http.MethodPatch, "/api/v1/me/password",
		map[string]any{"password": "attacker-chosen-password-123"})
	require.Equal(t, http.StatusForbidden, resp.StatusCode)

	// The staff user's original password still works. loggedIn asserts the
	// login succeeds, so it failing here would mean the change took effect.
	loggedIn(t, h)
}

// A person signed in through the browser must still be able to do all of this —
// a guard that locked everyone out would pass the tests above.
func TestSession_CanStillManageItsOwnAccount(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	s := loggedIn(t, h)

	res, body := s.send(t, http.MethodPost, "/api/v1/me/mfa/enroll", nil)
	require.Equal(t, http.StatusOK, res.StatusCode,
		"a signed-in person must still be able to enroll MFA; body: %s", body)
}

// Reading your own identity stays available: an integration legitimately needs
// to know who it is acting as.
func TestMachineCredential_CanStillReadItsOwnIdentity(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.do(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// Refusing only the credential's OWN owner closed the path and not the outcome.
// Every key reaching /admin is administrator-owned, so with users:write the long
// way round was four requests: create a second administrator, sign in as them,
// reset the original owner's password.
//
// The rule is therefore about administrators, not about self.
func TestMachineCredential_CannotReachAdministratorCredentials(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	t.Run("cannot create an administrator", func(t *testing.T) {
		resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/users", map[string]any{
			"email": "second-admin@test.local", "display_name": "Second",
			"role": "admin", "password": "a-password-123",
		})
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("cannot promote to administrator", func(t *testing.T) {
		resp := h.doAsAdmin(t, http.MethodPatch,
			"/api/v1/admin/users/"+h.staffID.String(), map[string]any{"role": "admin"})
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("cannot reset an administrator's password", func(t *testing.T) {
		resp := h.doAsAdmin(t, http.MethodPost,
			"/api/v1/admin/users/"+h.adminID.String()+"/password",
			map[string]any{"new_password": "attacker-chosen-password-123"})
		require.Equal(t, http.StatusForbidden, resp.StatusCode)

		var body struct {
			Error struct{ Code string } `json:"error"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		require.Equal(t, "session_required", body.Error.Code)
	})

	t.Run("cannot reset an administrator's MFA", func(t *testing.T) {
		resp := h.doAsAdmin(t, http.MethodPatch,
			"/api/v1/admin/users/"+h.adminID.String(), map[string]any{"reset_mfa": true})
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	})
}

// Ordinary automation must keep working: provisioning and deprovisioning
// non-administrators, and resetting a forgotten password.
func TestMachineCredential_CanStillAdministerNonAdmins(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"email": "new-agent@test.local", "display_name": "Agent",
		"role": "staff", "password": "a-password-123",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode,
		"creating a non-administrator is ordinary automation")

	resp = h.doAsAdmin(t, http.MethodPost,
		"/api/v1/admin/users/"+h.staffID.String()+"/password",
		map[string]any{"new_password": "a-legitimate-reset-123"})
	require.Equal(t, http.StatusNoContent, resp.StatusCode,
		"resetting a non-administrator's password is ordinary automation")
}

// A signed-in administrator must still be able to do all of it. A guard keyed on
// the target rather than the caller would lock the humans out too.
func TestSession_CanStillAdministerAdministrators(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	sess := &session{h: h}
	res, body := sess.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "admin@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "admin login; body: %s", body)

	res, body = sess.send(t, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"email": "third-admin@test.local", "display_name": "Third",
		"role": "admin", "password": "a-password-123",
	})
	require.Equal(t, http.StatusCreated, res.StatusCode,
		"a signed-in administrator must still create administrators; body: %s", body)

	res, body = sess.send(t, http.MethodPost,
		"/api/v1/admin/users/"+h.adminID.String()+"/password",
		map[string]any{"new_password": "a-deliberate-reset-123"})
	require.Equal(t, http.StatusNoContent, res.StatusCode,
		"and reset an administrator's password; body: %s", body)
}

// The escalation chain the review demonstrated, end to end: it must fail at the
// first step rather than merely being inconvenient.
func TestMachineCredential_EscalationChainIsBlockedAtStepOne(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"email": "pivot@test.local", "display_name": "Pivot",
		"role": "admin", "password": "attacker-known-password-123",
	})
	require.Equal(t, http.StatusForbidden, resp.StatusCode,
		"step one of the chain must fail")

	// And the account must not exist, so no later step has anything to use.
	resp = h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/users", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var users []struct {
		Email string `json:"email"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&users))
	for _, u := range users {
		require.NotEqual(t, "pivot@test.local", u.Email,
			"the refused administrator must not have been created")
	}
}

// Guarding promotion left the reverse open: demote an administrator, then reset
// the password of what is now a staff account, then sign in as them. Three
// requests to hold a human's account and strip an administrator.
func TestMachineCredential_CannotDemoteThenTakeOverAnAdministrator(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodPatch,
		"/api/v1/admin/users/"+h.adminID.String(), map[string]any{"role": "staff"})
	require.Equal(t, http.StatusForbidden, resp.StatusCode,
		"a machine credential must not change an administrator's role in either direction")

	// And the demotion must not have happened, or the next request succeeds.
	resp = h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/users/"+h.adminID.String(), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var u struct {
		Role string `json:"role"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&u))
	require.Equal(t, "admin", u.Role, "the refused demotion must not have been applied")
}

// The guard runs before any mutation. It used to sit after the disable, so a
// refused request had already disabled the account.
func TestMachineCredential_RefusedUpdateAppliesNothing(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodPatch,
		"/api/v1/admin/users/"+h.adminID.String(),
		map[string]any{"disabled": true, "role": "staff"})
	require.Equal(t, http.StatusForbidden, resp.StatusCode)

	// The admin key still works, which it would not if the disable had landed.
	resp = h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/users", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode,
		"the refused disable must not have been applied")
}

func TestMachineCredential_CannotDeleteOrLockOutAnAdministrator(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	target := "/api/v1/admin/users/" + h.adminID.String()

	for _, tc := range []struct {
		name, method string
		body         any
	}{
		{"delete", http.MethodDelete, nil},
		{"disable", http.MethodPatch, map[string]any{"disabled": true}},
		{"change email", http.MethodPatch, map[string]any{"email": "attacker@evil.test"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := h.doAsAdmin(t, tc.method, target, tc.body)
			require.Equal(t, http.StatusForbidden, resp.StatusCode)
		})
	}
}

// Repointing the identity provider is a route to an administrator session:
// aim SAML or OIDC at an IdP you control, assert a federated administrator's
// subject, and the login succeeds at their role.
func TestMachineCredential_CannotRepointTheIdentityProvider(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodPut, "/api/v1/admin/oidc", map[string]any{
		"issuer_url": "https://idp.attacker.example", "client_id": "x", "client_secret": "y",
	})
	require.Equal(t, http.StatusForbidden, resp.StatusCode, "OIDC config is off limits")

	resp = h.doAsAdmin(t, http.MethodPut, "/api/v1/admin/saml", map[string]any{
		"metadata_url": "https://idp.attacker.example/meta", "cert_pem": "c", "key_pem": "k",
	})
	require.Equal(t, http.StatusForbidden, resp.StatusCode, "SAML config is off limits")
}

// Turning MFA off instance-wide, or opening registration, reaches a session the
// caller should not have just as surely.
func TestMachineCredential_CannotChangeAuthCriticalSettings(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	for _, body := range []map[string]any{
		{"mfa_enabled": false},
		{"open_registration_enabled": true},
		{"allowed_email_domains": []string{"evil.test"}},
	} {
		resp := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/settings", body)
		require.Equal(t, http.StatusForbidden, resp.StatusCode,
			"auth-critical setting %v must be refused", body)
	}

	// Ordinary configuration is still automatable.
	resp := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{"site_name": "Renamed By Automation"})
	require.Equal(t, http.StatusNoContent, resp.StatusCode,
		"non-auth settings must stay available to automation")
}

// credentials:write was every scope: hold only that, mint a key with
// users:write, use it.
func TestMachineCredential_CannotMintABroaderCredential(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	narrow := mintKey(t, h, []string{"credentials:write", "credentials:read"})

	resp := withKey(t, h, narrow, http.MethodPost, "/api/v1/admin/api-keys",
		map[string]any{"name": "escalation", "scopes": []string{"users:write"}})
	require.Equal(t, http.StatusForbidden, resp.StatusCode,
		"a credential must not mint one broader than itself")

	var body struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, "insufficient_scope", body.Error.Code)
	require.Contains(t, body.Error.Message, "users:write")

	// Minting within its own scopes is still fine.
	resp = withKey(t, h, narrow, http.MethodPost, "/api/v1/admin/api-keys",
		map[string]any{"name": "sibling", "scopes": []string{"credentials:read"}})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
}

// A signed-in administrator is not escalating — they already hold everything a
// credential could be granted — so the subset rule must not apply to them.
func TestSession_CanStillMintAnyCredential(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	sess := &session{h: h}
	res, body := sess.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "admin@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)

	res, body = sess.send(t, http.MethodPost, "/api/v1/admin/api-keys",
		map[string]any{"name": "issued-by-a-human", "scopes": []string{"users:write", "settings:write"}})
	require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", body)
}

// denyMachineTargetingAdmin must fail closed when the target cannot be read.
func TestMachineCredential_UnreadableTargetIsRefused(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodPost,
		"/api/v1/admin/users/"+uuid.NewString()+"/password",
		map[string]any{"new_password": "a-password-123"})
	require.NotEqual(t, http.StatusNoContent, resp.StatusCode,
		"a target that cannot be read cannot be shown safe to act on")
}

// A machine credential must not reach MFA verification. Each wrong code spends
// the durable per-user budget, so a leaked key could re-lock the account every
// fifteen minutes and the human would never get in — a denial of service on
// their own account that revoking the key does not immediately undo.
func TestMachineCredential_CannotSpendTheMFABudget(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	for i := 0; i < 6; i++ {
		resp := h.do(t, http.MethodPost, "/api/v1/auth/local/mfa/verify",
			map[string]any{"code": "000000"})
		require.Equal(t, http.StatusForbidden, resp.StatusCode,
			"attempt %d must be refused before it reaches the counter", i+1)
	}

	// The budget is untouched: the owner can still log in.
	loggedIn(t, h)
}

// Login providers are settable from the admin UI and nowhere else.
//
// Pointing SAML or OIDC at an identity provider you control, then signing in as
// a federated administrator, is a direct route to an administrator session. It
// is also the last way a credential could make the server fetch a URL of the
// caller's choosing on the internal network.
//
// There are two doors — the dedicated config routes and the generic settings
// endpoint — and this checks both, because closing one and leaving the other is
// exactly how this kind of guard fails.
func TestMachineCredential_CannotSetLoginProvidersByAnyRoute(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	t.Run("the dedicated config routes", func(t *testing.T) {
		resp := h.doAsAdmin(t, http.MethodPut, "/api/v1/admin/oidc", map[string]any{
			"enabled": true, "issuer_url": "https://idp.attacker.example",
			"client_id": "x", "client_secret": "y",
		})
		require.Equal(t, http.StatusForbidden, resp.StatusCode)

		resp = h.doAsAdmin(t, http.MethodPut, "/api/v1/admin/saml", map[string]any{
			"metadata_url": "https://idp.attacker.example/meta", "cert_pem": "c", "key_pem": "k",
		})
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("the generic settings endpoint", func(t *testing.T) {
		// Every key that decides who may sign in, one at a time — a list that
		// blocks most of them is not a boundary.
		for _, key := range []string{
			"oidc_issuer_url", "oidc_enabled", "oidc_client_id", "oidc_client_secret",
			"oidc_redirect_url",
			"saml_metadata_url", "saml_enabled", "saml_cert_pem", "saml_key_pem",
			"mfa_enabled", "mfa_enforced_roles",
			"allowed_email_domains", "self_signup_enabled", "open_registration_enabled",
		} {
			t.Run(key, func(t *testing.T) {
				resp := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/settings",
					map[string]any{key: "https://idp.attacker.example"})
				require.Equal(t, http.StatusForbidden, resp.StatusCode,
					"%s must not be settable by an API key", key)
			})
		}
	})

	t.Run("reading the configuration is still allowed", func(t *testing.T) {
		// Monitoring and backup tooling legitimately reads this, and the
		// handler already blanks the secrets.
		resp := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/oidc", nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
	})
}

// A signed-in administrator must still be able to configure login providers —
// the admin UI is the supported way to do it.
func TestSession_CanStillSetLoginProviders(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	sess := &session{h: h}
	res, body := sess.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "admin@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)

	res, body = sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{"mfa_enabled": true})
	require.Equal(t, http.StatusNoContent, res.StatusCode,
		"an administrator in the UI must still change auth settings; body: %s", body)
}
