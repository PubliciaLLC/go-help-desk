package server

import (
	"crypto/tls"
	"log/slog"
	"net/http"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
)

// Admin SSO configuration for OIDC and SAML. Both write credentials that
// must never be echoed back; see secretSettingKeys in handler_admin_settings.go.
//
// Split out of handler_admin.go, which had grown to 1,332 lines across twelve
// unrelated resources. Moved verbatim: no handler logic changed.

// ── OIDC ──────────────────────────────────────────────────────────────────────

// GET /api/v1/admin/oidc
//
// Returns the persisted OIDC configuration.
// The client secret is intentionally never returned to the browser.
func (s *Server) handleGetOIDCConfig(w http.ResponseWriter, r *http.Request) {
	cfg := s.adminSvc.GetOIDCConfig(r.Context())

	JSON(w, http.StatusOK, map[string]any{
		"enabled":       cfg.Enabled,
		"issuer_url":    cfg.IssuerURL,
		"client_id":     cfg.ClientID,
		"client_secret": "",
		"redirect_url":  cfg.RedirectURL,
		"configured":    s.adminSvc.OIDCConfigured(r.Context()),
	})
}

// PUT /api/v1/admin/oidc
//
// A blank client_secret preserves the existing database value.
func (s *Server) handleSaveOIDCConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled      bool   `json:"enabled"`
		IssuerURL    string `json:"issuer_url"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}

	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}

	ctx := r.Context()

	existing := s.adminSvc.GetOIDCConfig(ctx)

	clientSecret := body.ClientSecret

	if clientSecret == "" {
		clientSecret = existing.ClientSecret
	}

	redirectURL := existing.RedirectURL

	if redirectURL == "" {
		redirectURL = s.cfg.BaseURL + "/api/v1/auth/oidc/callback"
	}

	cfg := auth.OIDCConfig{
		Enabled:      body.Enabled,
		IssuerURL:    body.IssuerURL,
		ClientID:     body.ClientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
	}

	warning, err := s.refuseIfOrphaning(ctx, s.samlReachableNow(), s.oidcReachableAfter(cfg))
	if err != nil {
		handleError(w, err)
		return
	}

	if err := s.adminSvc.SetOIDCConfig(ctx, cfg); err != nil {
		handleError(w, err)
		return
	}

	// Reload the in-memory OIDC provider immediately after persisting.
	// InitOIDC is fail-safe: on discovery/initialization failure it leaves
	// the currently active provider untouched and reports the error here.
	if err := s.InitOIDC(ctx); err != nil {
		handleError(w, err)
		return
	}

	resp := map[string]any{"ok": true}
	if warning != "" {
		resp["warning"] = warning
	}
	JSON(w, http.StatusOK, resp)
}

// ── SAML ──────────────────────────────────────────────────────────────────────

// GET /api/v1/admin/saml
func (s *Server) handleGetSAMLConfig(w http.ResponseWriter, r *http.Request) {
	metadataURL, certPEM, _ := s.adminSvc.GetSAMLConfig(r.Context())
	configured := s.adminSvc.SAMLConfigured(r.Context())
	JSON(w, http.StatusOK, map[string]any{
		"configured":      configured,
		"metadata_url":    metadataURL,
		"cert_pem":        certPEM,
		"sp_metadata_url": s.cfg.BaseURL + "/api/v1/auth/saml/metadata",
	})
}

// PUT /api/v1/admin/saml
// Accepts metadata_url, cert_pem, key_pem. A field OMITTED from the request
// retains the existing value from the database, so callers never need to
// re-upload a key they did not change. To clear a field, send it as an
// explicit empty string; to clear all SAML config, send all three that way.
func (s *Server) handleSaveSAMLConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		// Pointers, not plain strings: JSON has no way to tell "omitted" from
		// "sent as the zero value" once decoded into a string, so a caller
		// clearing metadata_url with "" was indistinguishable from one who
		// simply left it out of the body. That made the second sentence of
		// this comment a promise the code below could not keep: sending all
		// three fields as "" backfilled every one of them from the existing
		// stored value, since the old logic's only question was "is this
		// empty", and an explicit clear answers that the same way an
		// omission does. Nil means omitted; a non-nil pointer to "" is a
		// deliberate clear.
		MetadataURL *string `json:"metadata_url"`
		CertPEM     *string `json:"cert_pem"`
		KeyPEM      *string `json:"key_pem"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}

	ctx := r.Context()

	// Omitted fields retain the existing saved value; a field present in the
	// body, even as "", is the caller's explicit answer.
	existingURL, existingCert, existingKey := s.adminSvc.GetSAMLConfig(ctx)
	metadataURL := existingURL
	if body.MetadataURL != nil {
		metadataURL = *body.MetadataURL
	}
	certPEM := existingCert
	if body.CertPEM != nil {
		certPEM = *body.CertPEM
	}
	keyPEM := existingKey
	if body.KeyPEM != nil {
		keyPEM = *body.KeyPEM
	}

	// Validate the cert/key pair when either is present.
	if certPEM != "" || keyPEM != "" {
		if _, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM)); err != nil {
			Error(w, http.StatusBadRequest, "invalid_cert_key",
				"certificate and private key do not match or are invalid: "+err.Error())
			return
		}
	}

	warning, err := s.refuseIfOrphaning(ctx, samlReachableAfter(metadataURL, certPEM, keyPEM), s.oidcReachableNow())
	if err != nil {
		handleError(w, err)
		return
	}

	if err := s.adminSvc.SetSAMLConfig(ctx, metadataURL, certPEM, keyPEM); err != nil {
		handleError(w, err)
		return
	}

	// Hot-reload the SAML middleware. A failure here is non-fatal: the config is
	// saved and will be retried on next restart, but we report it to the caller.
	if err := s.reloadSAML(ctx); err != nil {
		// The error is logged, not returned. The fetch reaches whatever URL the
		// caller supplied, and the failure text distinguishes a closed port
		// from a listening one, and names the root element of whatever it did
		// reach ("expected <EntityDescriptor> but have <html>"). Echoed back,
		// that turns this form into an internal port and protocol scanner.
		slog.Error("SAML reload failed", "error", err)
		reloadWarning := "SAML config saved, but the identity provider metadata could not be loaded. Check the metadata URL and the server log."
		if warning != "" {
			reloadWarning = warning + " " + reloadWarning
		}
		JSON(w, http.StatusOK, map[string]any{
			"warning": reloadWarning,
		})
		return
	}

	if warning != "" {
		JSON(w, http.StatusOK, map[string]any{"warning": warning})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
