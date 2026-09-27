package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
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

// samlIDPMetadataXML is TestShib's real IdP metadata fixture, vendored
// (unmodified) from github.com/crewjam/saml's own test suite
// (samlsp/testdata/idp_metadata.xml) — genuine, parseable SAML 2.0 IdP
// metadata. samlsp.FetchMetadata performs a real HTTP fetch with no way to
// inject static content in its place (see auth.NewSAMLMiddleware), so
// proving a save that genuinely SUCCEEDS, rather than one that merely looks
// well-formed, needs an endpoint that actually answers with something the
// library will parse.
const samlIDPMetadataXML = `<?xml version="1.0" encoding="UTF-8"?>
<EntityDescriptor xmlns="urn:oasis:names:tc:SAML:2.0:metadata" xmlns:ds="http://www.w3.org/2000/09/xmldsig#" xmlns:mdalg="urn:oasis:names:tc:SAML:metadata:algsupport" xmlns:mdui="urn:oasis:names:tc:SAML:metadata:ui" xmlns:shibmd="urn:mace:shibboleth:metadata:1.0" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" Name="urn:mace:shibboleth:testshib:two" entityID="https://idp.testshib.org/idp/shibboleth">
	<Extensions>
		<mdalg:DigestMethod Algorithm="http://www.w3.org/2001/04/xmlenc#sha512" />
		<mdalg:DigestMethod Algorithm="http://www.w3.org/2001/04/xmldsig-more#sha384" />
		<mdalg:DigestMethod Algorithm="http://www.w3.org/2001/04/xmlenc#sha256" />
		<mdalg:DigestMethod Algorithm="http://www.w3.org/2000/09/xmldsig#sha1" />
		<mdalg:SigningMethod Algorithm="http://www.w3.org/2001/04/xmldsig-more#rsa-sha512" />
		<mdalg:SigningMethod Algorithm="http://www.w3.org/2001/04/xmldsig-more#rsa-sha384" />
		<mdalg:SigningMethod Algorithm="http://www.w3.org/2001/04/xmldsig-more#rsa-sha256" />
		<mdalg:SigningMethod Algorithm="http://www.w3.org/2000/09/xmldsig#rsa-sha1" />
	</Extensions>
	<IDPSSODescriptor protocolSupportEnumeration="urn:oasis:names:tc:SAML:1.1:protocol urn:mace:shibboleth:1.0 urn:oasis:names:tc:SAML:2.0:protocol">
		<Extensions>
			<shibmd:Scope regexp="false">testshib.org</shibmd:Scope>
			<mdui:UIInfo>
				<mdui:DisplayName xml:lang="en">TestShib Test IdP</mdui:DisplayName>
				<mdui:Description xml:lang="en">TestShib IdP. Use this as a source of attributes
                        for your test SP.</mdui:Description>
				<mdui:Logo height="88" width="253">https://www.testshib.org/testshibtwo.jpg</mdui:Logo>
			</mdui:UIInfo>
		</Extensions>
		<KeyDescriptor>
			<ds:KeyInfo>
				<ds:X509Data>
					<ds:X509Certificate>MIIEDjCCAvagAwIBAgIBADANBgkqhkiG9w0BAQUFADBnMQswCQYDVQQGEwJVUzEV
                            MBMGA1UECBMMUGVubnN5bHZhbmlhMRMwEQYDVQQHEwpQaXR0c2J1cmdoMREwDwYD
                            VQQKEwhUZXN0U2hpYjEZMBcGA1UEAxMQaWRwLnRlc3RzaGliLm9yZzAeFw0wNjA4
                            MzAyMTEyMjVaFw0xNjA4MjcyMTEyMjVaMGcxCzAJBgNVBAYTAlVTMRUwEwYDVQQI
                            EwxQZW5uc3lsdmFuaWExEzARBgNVBAcTClBpdHRzYnVyZ2gxETAPBgNVBAoTCFRl
                            c3RTaGliMRkwFwYDVQQDExBpZHAudGVzdHNoaWIub3JnMIIBIjANBgkqhkiG9w0B
                            AQEFAAOCAQ8AMIIBCgKCAQEArYkCGuTmJp9eAOSGHwRJo1SNatB5ZOKqDM9ysg7C
                            yVTDClcpu93gSP10nH4gkCZOlnESNgttg0r+MqL8tfJC6ybddEFB3YBo8PZajKSe
                            3OQ01Ow3yT4I+Wdg1tsTpSge9gEz7SrC07EkYmHuPtd71CHiUaCWDv+xVfUQX0aT
                            NPFmDixzUjoYzbGDrtAyCqA8f9CN2txIfJnpHE6q6CmKcoLADS4UrNPlhHSzd614
                            kR/JYiks0K4kbRqCQF0Dv0P5Di+rEfefC6glV8ysC8dB5/9nb0yh/ojRuJGmgMWH
                            gWk6h0ihjihqiu4jACovUZ7vVOCgSE5Ipn7OIwqd93zp2wIDAQABo4HEMIHBMB0G
                            A1UdDgQWBBSsBQ869nh83KqZr5jArr4/7b+QazCBkQYDVR0jBIGJMIGGgBSsBQ86
                            9nh83KqZr5jArr4/7b+Qa6FrpGkwZzELMAkGA1UEBhMCVVMxFTATBgNVBAgTDFBl
                            bm5zeWx2YW5pYTETMBEGA1UEBxMKUGl0dHNidXJnaDERMA8GA1UEChMIVGVzdFNo
                            aWIxGTAXBgNVBAMTEGlkcC50ZXN0c2hpYi5vcmeCAQAwDAYDVR0TBAUwAwEB/zAN
                            BgkqhkiG9w0BAQUFAAOCAQEAjR29PhrCbk8qLN5MFfSVk98t3CT9jHZoYxd8QMRL
                            I4j7iYQxXiGJTT1FXs1nd4Rha9un+LqTfeMMYqISdDDI6tv8iNpkOAvZZUosVkUo
                            93pv1T0RPz35hcHHYq2yee59HJOco2bFlcsH8JBXRSRrJ3Q7Eut+z9uo80JdGNJ4
                            /SJy5UorZ8KazGj16lfJhOBXldgrhppQBb0Nq6HKHguqmwRfJ+WkxemZXzhediAj
                            Geka8nz8JjwxpUjAiSWYKLtJhGEaTqCYxCCX2Dw+dOTqUzHOZ7WKv4JXPK5G/Uhr
                            8K/qhmFT2nIQi538n6rVYLeWj8Bbnl+ev0peYzxFyF5sQA==</ds:X509Certificate>
				</ds:X509Data>
			</ds:KeyInfo>
			<EncryptionMethod Algorithm="http://www.w3.org/2001/04/xmlenc#aes256-cbc" />
			<EncryptionMethod Algorithm="http://www.w3.org/2001/04/xmlenc#aes192-cbc" />
			<EncryptionMethod Algorithm="http://www.w3.org/2001/04/xmlenc#aes128-cbc" />
			<EncryptionMethod Algorithm="http://www.w3.org/2001/04/xmlenc#tripledes-cbc" />
			<EncryptionMethod Algorithm="http://www.w3.org/2001/04/xmlenc#rsa-oaep-mgf1p" />
			<EncryptionMethod Algorithm="http://www.w3.org/2001/04/xmlenc#rsa-1_5" />
		</KeyDescriptor>
		<ArtifactResolutionService Binding="urn:oasis:names:tc:SAML:1.0:bindings:SOAP-binding" Location="https://idp.testshib.org:8443/idp/profile/SAML1/SOAP/ArtifactResolution" index="1" />
		<ArtifactResolutionService Binding="urn:oasis:names:tc:SAML:2.0:bindings:SOAP" Location="https://idp.testshib.org:8443/idp/profile/SAML2/SOAP/ArtifactResolution" index="2" />
		<NameIDFormat>urn:mace:shibboleth:1.0:nameIdentifier</NameIDFormat>
		<NameIDFormat>urn:oasis:names:tc:SAML:2.0:nameid-format:transient</NameIDFormat>
		<SingleSignOnService Binding="urn:mace:shibboleth:1.0:profiles:AuthnRequest" Location="https://idp.testshib.org/idp/profile/Shibboleth/SSO" />
		<SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-POST" Location="https://idp.testshib.org/idp/profile/SAML2/POST/SSO" />
		<SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect" Location="https://idp.testshib.org/idp/profile/SAML2/Redirect/SSO" />
		<SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:SOAP" Location="https://idp.testshib.org/idp/profile/SAML2/SOAP/ECP" />
	</IDPSSODescriptor>
	<Organization>
		<OrganizationName xml:lang="en">TestShib Two Identity Provider</OrganizationName>
		<OrganizationDisplayName xml:lang="en">TestShib Two</OrganizationDisplayName>
		<OrganizationURL xml:lang="en">http://www.testshib.org/testshib-two/</OrganizationURL>
	</Organization>
	<ContactPerson contactType="technical">
		<GivenName>Nate</GivenName>
		<SurName>Klingenstein</SurName>
		<EmailAddress>ndk@internet2.edu</EmailAddress>
	</ContactPerson>
</EntityDescriptor>`

// newFakeSAMLIdP serves samlIDPMetadataXML at whatever path it is asked for,
// so a test can point metadata_url at it and get a genuinely successful
// SAML middleware construction — as opposed to every other metadata_url in
// this file ("https://idp.test/metadata"), which is deliberately chosen to
// fail in this sandbox and never answers at all.
func newFakeSAMLIdP(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(samlIDPMetadataXML))
	}))
	t.Cleanup(srv.Close)
	return srv
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
// buildSAMLMiddleware's real reachability check, through the real HTTP
// handler, on both sides of a genuine on-to-off transition. Unlike the OIDC
// tests above, the stranded administrator here never needs to log in
// themselves — StrandedAdmins only needs their row, and building a fake SAML
// IdP to prove that a second, unrelated way is out of proportion to what
// this test is actually checking (the seeding step below does need one, to
// make the "on" half of the transition genuine — see newFakeSAMLIdP).
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

	// SAML has its own enabled gate, mirroring OIDC's: flip it on first, the
	// same way the settings page's "Enable SAML login" toggle would, so the
	// seeding save below is a genuine "turn it on" rather than a write that
	// buildSAMLMiddleware would refuse to even attempt. This PATCH itself
	// warns about saml-admin too — flipping the flag alone, with no
	// metadata/cert/key seeded yet, still leaves them exactly as unable to
	// sign in as they were before this test started (ssoSettingsWarning
	// reports current-state stranding on any touching write, not a delta) —
	// so 200 with the same warning is correct here, not 204.
	res, body = sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{"saml_enabled": true})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	require.Contains(t, string(body), "saml-admin@test.local")

	idp := newFakeSAMLIdP(t)

	// Establish a genuinely working SAML setup — metadata_url points at a
	// real server that actually answers with valid IdP metadata, so this is
	// a genuine "turn it on", not "it was never on". Before #300's own review
	// found Finding C, this endpoint only checked whether the three fields
	// were non-empty, so "https://idp.test/metadata" (which never answers in
	// this sandbox) would have counted as reachable too; the point of using
	// a real, working fake IdP here is to prove the negative case below is
	// a genuine transition away from something that really worked.
	res, body = sess.send(t, http.MethodPut, "/api/v1/admin/saml", map[string]any{
		"metadata_url": idp.URL,
		"cert_pem":     validSAMLCert,
		"key_pem":      validSAMLKey,
	})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "seeding save must succeed cleanly; body: %s", body)
	metadataURL, _, _ := h.adminSvc.GetSAMLConfig(ctx)
	require.Equal(t, idp.URL, metadataURL, "the seeding save must have persisted")

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

// TestSaveSAMLConfig_WarnsEvenWhenFieldsLookComplete pins Finding C from
// #300's own review round: the field-completeness-only check the two
// dedicated endpoints used before that review (samlFieldsLookConfigured is
// what remains of it, now used only by the generic settings PATCH — see its
// own comment) would have called this configuration "reachable" purely
// because saml_enabled is true and metadata_url/cert_pem/key_pem are all
// non-empty — even though nothing answers at this metadata_url in this
// sandbox, so buildSAMLMiddleware's real construction attempt fails and no
// SAML middleware is ever actually reachable. The stranded administrator
// must still be reported, not silently waved through because the request
// merely looked complete.
func TestSaveSAMLConfig_WarnsEvenWhenFieldsLookComplete(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	_, err := h.userSvc.Create(ctx, user.CreateUserInput{
		Email:       "field-complete-saml-admin@test.local",
		DisplayName: "SAML Admin",
		Role:        user.RoleAdmin,
		SAMLSubject: "field-complete-saml-admin-sub",
	})
	require.NoError(t, err)

	sess := &session{h: h}
	res, body := sess.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "admin@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "admin login; body: %s", body)

	// Same as the test above: flipping the flag alone, before any metadata is
	// seeded, already warns about this admin — expected, not a delta check.
	res, body = sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{"saml_enabled": true})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	require.Contains(t, string(body), "field-complete-saml-admin@test.local")

	res, body = sess.send(t, http.MethodPut, "/api/v1/admin/saml", map[string]any{
		"metadata_url": "https://idp.test/metadata",
		"cert_pem":     validSAMLCert,
		"key_pem":      validSAMLKey,
	})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	require.Contains(t, string(body), "field-complete-saml-admin@test.local",
		"a config that only LOOKS complete must still warn about who it would strand")
}

// TestSaveOIDCConfig_WarnsEvenWhenFieldsLookComplete is
// TestSaveSAMLConfig_WarnsEvenWhenFieldsLookComplete's OIDC counterpart:
// Finding C applied to both providers equally (oidcFieldsLookConfigured is
// its OIDC twin), and buildOIDCProvider's own real-discovery-attempt fix
// needs the same proof. Deliberately uses a plain harness rather than
// newOIDCHarness: OIDC has never been successfully live here, so
// s.oidcProvider is nil and oidcReachableNow() cannot mask a real discovery
// failure behind an old, still-working provider the way it correctly would
// once one exists (see handleSaveOIDCConfig's own fail-safe comment).
func TestSaveOIDCConfig_WarnsEvenWhenFieldsLookComplete(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	_, err := h.userSvc.Create(context.Background(), user.CreateUserInput{
		Email:       "field-complete-oidc-admin@test.local",
		DisplayName: "OIDC Admin",
		Role:        user.RoleAdmin,
		OIDCSubject: "field-complete-oidc-admin-sub",
	})
	require.NoError(t, err)

	sess := &session{h: h}
	res, body := sess.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "admin@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "admin login; body: %s", body)

	res, body = sess.send(t, http.MethodPut, "/api/v1/admin/oidc", map[string]any{
		"enabled":       true,
		"issuer_url":    "https://idp.test",
		"client_id":     "x",
		"client_secret": "y",
	})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	require.Contains(t, string(body), "field-complete-oidc-admin@test.local",
		"a config that only LOOKS complete must still warn about who it would strand")
}

// ── The generic settings PATCH: Finding A ───────────────────────────────────
//
// PATCH /admin/settings reaches every one of these same keys through
// handleUpdateSettings/ssoSettingsWarning rather than the two dedicated PUT
// endpoints above, and AuthCriticalKeys() only blocks MACHINE credentials
// from touching them (see TestMachineCredential_CannotRepointTheIdentityProvider
// in handler_me_machine_test.go) — a signed-in human session reaches this
// route exactly as freely as the dedicated ones. Before #300's own review
// found this, that made it a complete, unconditional bypass of the guard
// above: a caller could set oidc_enabled to false, or blank any SAML field,
// through this one PATCH with no refusal and no warning at all.

// TestUpdateSettings_RefusesWhenItWouldOrphanTheInstance is
// TestSaveOIDCConfig_RefusesWhenItWouldOrphanTheInstance's counterpart
// through the generic settings route instead of the dedicated one.
func TestUpdateSettings_RefusesWhenItWouldOrphanTheInstance(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()
	ctx := context.Background()

	cookies := createOIDCAdmin(t, oh, "sole-oidc-admin-settings@test.local", "sole-oidc-admin-settings-sub")
	require.NoError(t, oh.userSvc.SetRole(ctx, oh.adminID, user.RoleStaff))

	sess := &session{h: oh.harness, jar: cookies}
	res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{"oidc_enabled": false})
	require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", body)

	cfg := oh.adminSvc.GetOIDCConfig(ctx)
	require.True(t, cfg.Enabled, "the refused write must not have been persisted")
}

// TestUpdateSettings_WarnsWithoutRefusingWhenOnlySomeAdminsAreStranded is
// TestSaveOIDCConfig_WarnsWithoutRefusingWhenOnlySomeAdminsAreStranded's
// counterpart through the generic settings route.
func TestUpdateSettings_WarnsWithoutRefusingWhenOnlySomeAdminsAreStranded(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()
	ctx := context.Background()

	createOIDCAdmin(t, oh, "partial-oidc-admin-settings@test.local", "partial-oidc-admin-settings-sub")

	sess := &session{h: oh.harness}
	res, body := sess.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "admin@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "admin login; body: %s", body)

	res, body = sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{"oidc_enabled": false})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	require.Contains(t, string(body), "partial-oidc-admin-settings@test.local",
		"the warning must name the administrator who would be stranded")

	cfg := oh.adminSvc.GetOIDCConfig(ctx)
	require.False(t, cfg.Enabled, "an allowed change must actually persist")
}

// TestUpdateSettings_SAMLFieldBlankWarnsWithoutRefusing is the SAML side of
// the same bypass: blanking a single SAML field through the generic route,
// rather than the dedicated PUT /admin/saml body, must reach the same guard.
func TestUpdateSettings_SAMLFieldBlankWarnsWithoutRefusing(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	_, err := h.userSvc.Create(ctx, user.CreateUserInput{
		Email:       "saml-admin-settings@test.local",
		DisplayName: "SAML Admin",
		Role:        user.RoleAdmin,
		SAMLSubject: "saml-admin-settings-sub",
	})
	require.NoError(t, err)

	require.NoError(t, h.adminSvc.SetSAMLEnabled(ctx, true))
	require.NoError(t, h.adminSvc.SetSAMLConfig(ctx, "https://idp.test/metadata", validSAMLCert, validSAMLKey))

	sess := &session{h: h}
	res, body := sess.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "admin@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "admin login; body: %s", body)

	res, body = sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{"saml_metadata_url": ""})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	require.Contains(t, string(body), "saml-admin-settings@test.local",
		"the warning must name the administrator who would be stranded")
}
