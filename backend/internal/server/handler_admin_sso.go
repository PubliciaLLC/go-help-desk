package server

import (
	"crypto/tls"
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

	JSON(w, http.StatusOK, map[string]any{
		"ok": true,
	})
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
// Accepts metadata_url, cert_pem, key_pem. Any field left empty retains the
// existing value from the database, so callers never need to re-upload a key
// they did not change. To clear all SAML config, send all three as empty strings.
func (s *Server) handleSaveSAMLConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MetadataURL string `json:"metadata_url"`
		CertPEM     string `json:"cert_pem"`
		KeyPEM      string `json:"key_pem"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}

	// Fill in blanks from the existing saved config so partial updates work.
	existingURL, existingCert, existingKey := s.adminSvc.GetSAMLConfig(r.Context())
	metadataURL := body.MetadataURL
	if metadataURL == "" {
		metadataURL = existingURL
	}
	certPEM := body.CertPEM
	if certPEM == "" {
		certPEM = existingCert
	}
	keyPEM := body.KeyPEM
	if keyPEM == "" {
		keyPEM = existingKey
	}

	// Validate the cert/key pair when either is present.
	if certPEM != "" || keyPEM != "" {
		if _, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM)); err != nil {
			Error(w, http.StatusBadRequest, "invalid_cert_key",
				"certificate and private key do not match or are invalid: "+err.Error())
			return
		}
	}

	if err := s.adminSvc.SetSAMLConfig(r.Context(), metadataURL, certPEM, keyPEM); err != nil {
		handleError(w, err)
		return
	}

	// Hot-reload the SAML middleware. A failure here is non-fatal: the config is
	// saved and will be retried on next restart, but we report it to the caller.
	if err := s.reloadSAML(r.Context()); err != nil {
		JSON(w, http.StatusOK, map[string]any{
			"warning": "SAML config saved but middleware could not be loaded: " + err.Error(),
		})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
