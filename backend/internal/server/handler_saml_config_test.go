package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// signInAsAdmin logs the seeded local admin in through the ordinary password
// path and returns a session ready to drive further requests.
func signInAsAdmin(t *testing.T, h *harness) *session {
	t.Helper()
	sess := &session{h: h}
	res, body := sess.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "admin@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "admin login; body: %s", body)
	return sess
}

// TestSaveSAMLConfig_OmittedFieldsPreserveExisting pins the documented
// "callers never need to re-upload a key they did not change" behaviour: a
// field left out of the request body entirely keeps its stored value.
func TestSaveSAMLConfig_OmittedFieldsPreserveExisting(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	require.NoError(t, h.adminSvc.SetSAMLConfig(ctx, "https://idp.test/metadata", validSAMLCert, validSAMLKey))

	sess := signInAsAdmin(t, h)
	res, body := sess.send(t, http.MethodPut, "/api/v1/admin/saml", map[string]any{
		"metadata_url": "https://idp.test/rotated-metadata",
		// cert_pem, key_pem omitted entirely.
	})
	// 200, not 204: the fields are complete, so buildSAMLMiddleware genuinely
	// attempts to fetch metadata from "https://idp.test/rotated-metadata",
	// which does not answer in this sandbox — a non-fatal reload-failure
	// warning, not a #300 stranding one (the seeded local admin has a
	// password, so nobody is stranded either way).
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)

	url, cert, key := h.adminSvc.GetSAMLConfig(ctx)
	require.Equal(t, "https://idp.test/rotated-metadata", url)
	require.Equal(t, validSAMLCert, cert, "an omitted cert_pem must keep the stored certificate")
	require.Equal(t, validSAMLKey, key, "an omitted key_pem must keep the stored key")
}

// TestSaveSAMLConfig_ExplicitEmptyFieldsClear pins the other half of the same
// promise: sending a field as an explicit empty string clears it, rather
// than being indistinguishable from omitting it. Before this, the handler
// read a plain string and could not tell the two apart — every field
// backfilled from its stored value whenever it was empty, so "send all three
// as empty strings to clear", the handler's own documented escape hatch, was
// never actually reachable.
func TestSaveSAMLConfig_ExplicitEmptyFieldsClear(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	require.NoError(t, h.adminSvc.SetSAMLConfig(ctx, "https://idp.test/metadata", validSAMLCert, validSAMLKey))

	sess := signInAsAdmin(t, h)
	res, body := sess.send(t, http.MethodPut, "/api/v1/admin/saml", map[string]any{
		"metadata_url": "",
		"cert_pem":     "",
		"key_pem":      "",
	})
	// 204, not 200: nobody is stranded by clearing SAML here (the seeded
	// local admin has a password), so there is no #300 warning to report.
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)

	url, cert, key := h.adminSvc.GetSAMLConfig(ctx)
	require.Equal(t, "", url)
	require.Equal(t, "", cert)
	require.Equal(t, "", key)
}
