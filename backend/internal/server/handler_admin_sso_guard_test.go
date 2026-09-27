package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/stretchr/testify/require"
)

// createOIDCAdmin seeds an active administrator whose only channel is OIDC —
// no password, no SAML subject — then completes a real OIDC login as that
// exact identity (matched by subject, so UpsertOIDCUser takes the "known
// identity" branch and never touches Role) and returns the resulting session
// cookies. This is the account shape #300 is about: the row, the role and the
// active-administrator count all look completely ordinary.
func createOIDCAdmin(t *testing.T, oh *oidcHarness, email, subject string) []*http.Cookie {
	t.Helper()
	ctx := context.Background()
	_, err := oh.userSvc.Create(ctx, user.CreateUserInput{
		Email:       email,
		DisplayName: "OIDC Admin",
		Role:        user.RoleAdmin,
		OIDCSubject: subject,
	})
	require.NoError(t, err)

	resp := oh.login(t, fakeOIDCClaims{
		Subject:       subject,
		Email:         email,
		EmailVerified: true,
		Name:          "OIDC Admin",
	})
	require.Equal(t, http.StatusSeeOther, resp.StatusCode, "OIDC login for the seeded admin must succeed")
	return resp.Cookies()
}

// TestSaveOIDCConfig_RefusesWhenItWouldOrphanTheInstance is #300's
// unrecoverable case: the ONLY active administrator has no password, and
// disabling OIDC would remove their only way to authenticate. Every existing
// guard (ErrLastAdmin and its statements) passes here, because nothing about
// the administrator ROW changes — this is the door those do not watch.
func TestSaveOIDCConfig_RefusesWhenItWouldOrphanTheInstance(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()
	ctx := context.Background()

	cookies := createOIDCAdmin(t, oh, "sole-oidc-admin@test.local", "sole-oidc-admin-sub")

	// Demote the seeded local admin so the OIDC-only admin above becomes the
	// LAST active administrator — otherwise this is #300's recoverable case
	// (see the warning test below), not its orphaning one.
	require.NoError(t, oh.userSvc.SetRole(ctx, oh.adminID, user.RoleStaff))

	sess := &session{h: oh.harness, jar: cookies}
	res, body := sess.send(t, http.MethodPut, "/api/v1/admin/oidc", map[string]any{
		"enabled":       false,
		"issuer_url":    oh.idp.issuer(),
		"client_id":     oh.idp.clientID,
		"client_secret": "",
	})
	require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", body)

	cfg := oh.adminSvc.GetOIDCConfig(ctx)
	require.True(t, cfg.Enabled, "the refused write must not have been persisted")
}

// TestSaveOIDCConfig_WarnsWithoutRefusingWhenOnlySomeAdminsAreStranded is
// #300's recoverable case: an OIDC-only administrator loses their way in, but
// another administrator (the seeded local one) can still sign in and fix
// things. Refusing this would be the least-recoverable failure mode the
// guard exists to prevent, just applied somewhere it is not needed — so it
// is allowed, and reported back rather than silently permitted.
func TestSaveOIDCConfig_WarnsWithoutRefusingWhenOnlySomeAdminsAreStranded(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()
	ctx := context.Background()

	createOIDCAdmin(t, oh, "partial-oidc-admin@test.local", "partial-oidc-admin-sub")

	// The seeded local admin keeps their password — they are the caller here,
	// signed in the ordinary way, and they are never stranded by this change.
	sess := &session{h: oh.harness}
	res, body := sess.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "admin@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "admin login; body: %s", body)

	res, body = sess.send(t, http.MethodPut, "/api/v1/admin/oidc", map[string]any{
		"enabled":       false,
		"issuer_url":    oh.idp.issuer(),
		"client_id":     oh.idp.clientID,
		"client_secret": "",
	})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	require.Contains(t, string(body), "partial-oidc-admin@test.local",
		"the warning must name the administrator who would be stranded")

	cfg := oh.adminSvc.GetOIDCConfig(ctx)
	require.False(t, cfg.Enabled, "an allowed change must actually persist")
}

// TestSaveOIDCConfig_NoWarningWhenNobodyIsStranded is the ordinary path: every
// active administrator has a password, so disabling OIDC is unremarkable.
func TestSaveOIDCConfig_NoWarningWhenNobodyIsStranded(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()

	sess := &session{h: oh.harness}
	res, body := sess.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "admin@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "admin login; body: %s", body)

	res, body = sess.send(t, http.MethodPut, "/api/v1/admin/oidc", map[string]any{
		"enabled":       false,
		"issuer_url":    oh.idp.issuer(),
		"client_id":     oh.idp.clientID,
		"client_secret": "",
	})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	require.NotContains(t, string(body), "warning")
}

// validSAMLCert and validSAMLKey are a throwaway self-signed pair — SAML's
// config-save handler validates the cert/key pair with tls.X509KeyPair before
// this guard even runs, so a SAML test needs a pair that actually parses and
// matches, not just non-empty strings.
const (
	validSAMLCert = `-----BEGIN CERTIFICATE-----
MIIBojCCAUegAwIBAgIULl1Gf+aFmRK0Alzmu/mDPvBMwdQwCgYIKoZIzj0EAwIw
JjENMAsGA1UEAwwEdGVzdDEVMBMGA1UECgwMR28gSGVscCBEZXNrMB4XDTI2MDky
NzAzMDUxNloXDTM2MDkyNDAzMDUxNlowJjENMAsGA1UEAwwEdGVzdDEVMBMGA1UE
CgwMR28gSGVscCBEZXNrMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEyeR+8bag
dsDR+FDFMV9So0OPnHiNelmnL56dB/JH8/WbtGi6Lvr6/RvIKZQCUcXBQ9JP1p4z
II3lKVOvwFbwjqNTMFEwHQYDVR0OBBYEFBNMS9byNKj0oRQMD5SCm3OvYE3+MB8G
A1UdIwQYMBaAFBNMS9byNKj0oRQMD5SCm3OvYE3+MA8GA1UdEwEB/wQFMAMBAf8w
CgYIKoZIzj0EAwIDSQAwRgIhAPhsfRcbqllDtFsedGBqZ6lXdnSdAUl6YDjY7/y6
Mwa+AiEAwwE+/IaI4BwPJ5wsNcQogWzpROWWra5pzxy5adP8QmY=
-----END CERTIFICATE-----`
	validSAMLKey = `-----BEGIN EC PRIVATE KEY-----
MHcCAQEEIFpEkiHzTE3wPejLPE+viJff1lDheSoWybr4qHiHJCrloAoGCCqGSM49
AwEHoUQDQgAEyeR+8bagdsDR+FDFMV9So0OPnHiNelmnL56dB/JH8/WbtGi6Lvr6
/RvIKZQCUcXBQ9JP1p4zII3lKVOvwFbwjg==
-----END EC PRIVATE KEY-----`
)

// TestSaveSAMLConfig_WarnsWithoutRefusingWhenOnlySomeAdminsAreStranded is
// #300's recoverable case, for SAML instead of OIDC: it exercises
// samlReachableAfter's own field-blanking logic through the real HTTP
// handler. Unlike the OIDC tests above, the stranded administrator here never
// needs to log in themselves — StrandedAdmins only needs their row, and
// building a fake SAML IdP to prove that a second, unrelated way is out of
// proportion to what this test is actually checking.
func TestSaveSAMLConfig_WarnsWithoutRefusingWhenOnlySomeAdminsAreStranded(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	_, err := h.userSvc.Create(ctx, user.CreateUserInput{
		Email:       "saml-admin@test.local",
		DisplayName: "SAML Admin",
		Role:        user.RoleAdmin,
		SAMLSubject: "saml-admin-sub",
	})
	require.NoError(t, err)

	sess := &session{h: h}
	res, body := sess.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "admin@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "admin login; body: %s", body)

	// Establish a configured SAML setup first, so the request below is a
	// genuine "turn it off", not "it was never on". samlReachableAfter (like
	// SAMLConfigured and reloadSAML itself) is a question about these three
	// fields being present, not about whether "https://idp.test/metadata"
	// actually answers — it does not, in this sandbox, so the save is
	// expected to report the existing non-fatal reload warning rather than
	// 204. That is a different, unrelated warning from the one this test
	// checks for below.
	res, _ = sess.send(t, http.MethodPut, "/api/v1/admin/saml", map[string]any{
		"metadata_url": "https://idp.test/metadata",
		"cert_pem":     validSAMLCert,
		"key_pem":      validSAMLKey,
	})
	require.Equal(t, http.StatusOK, res.StatusCode)
	metadataURL, _, _ := h.adminSvc.GetSAMLConfig(ctx)
	require.Equal(t, "https://idp.test/metadata", metadataURL, "the seeding save must have persisted")

	res, body = sess.send(t, http.MethodPut, "/api/v1/admin/saml", map[string]any{
		"metadata_url": "",
		"cert_pem":     "",
		"key_pem":      "",
	})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	require.Contains(t, string(body), "saml-admin@test.local",
		"the warning must name the administrator who would be stranded")

	metadataURL, _, _ = h.adminSvc.GetSAMLConfig(ctx)
	require.Equal(t, "", metadataURL, "an allowed change must actually persist")
}
