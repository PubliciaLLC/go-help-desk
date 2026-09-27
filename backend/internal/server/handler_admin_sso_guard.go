package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// refuseIfOrphaning is the settings-level counterpart to ErrLastAdmin for
// #300: it runs before an OIDC or SAML settings write and decides whether
// that write may proceed, given what SAML's and OIDC's reachability will be
// immediately afterward.
//
// ErrLastAdmin (and the *UnlessLastAdmin statements) refuse to disable,
// demote or delete the sole administrator, because each of those removes an
// administrator while leaving the count wrong. Switching off the identity
// provider a passwordless administrator depends on is the same class of
// mistake reached through a door that guard does not watch: it removes their
// ABILITY to authenticate while leaving the row, the role and the count
// untouched, so every existing guard passes.
//
// Refuses only the unrecoverable case — every active administrator left with
// no way to authenticate — matching ErrLastAdmin's own severity, per #300's
// option 3. A change that strands only SOME administrators, with at least one
// other still able to sign in and fix things, is allowed rather than refused,
// and reported back as a warning naming who is affected (#300's option 2):
// the operator may be migrating providers deliberately, and a hard refusal
// there would be the same unrecoverable-lockout shape this guard exists to
// prevent, just imposed on somebody else's account instead of avoided.
//
// Called from every route that can change SAML/OIDC reachability: the two
// dedicated PUT endpoints (with samlReachable/oidcReachable computed by
// actually building the middleware/provider against the candidate config —
// see buildSAMLMiddleware/buildOIDCProvider in server.go — so the decision
// reflects what will really happen rather than an optimistic guess) and the
// generic PATCH /admin/settings endpoint (see ssoSettingsWarning in
// handler_admin_settings.go, which uses the simpler field-completeness read
// since that path does not live-reload either provider at all — a
// pre-existing gap, not something this guard needs to paper over).
//
// Not the same kind of guard as the *UnlessLastAdmin statements: those decide
// and write a single row in one UPDATE, atomic against a concurrent guard of
// the same kind. This reads the active-administrator list, decides, and only
// then writes the setting — a real, if narrow, race against a concurrent user
// mutation (an administrator being demoted or disabled) or a second
// concurrent settings change. Accepted rather than closed: this is a
// deliberate, infrequent action taken from the admin settings page, not a
// path an unauthenticated attacker can drive the way the user-mutation races
// were driven — see DisableUnlessLastAdmin's own comment — and closing it
// would need a transaction spanning the users and settings tables, which
// nothing else in this codebase does today. Revisit if that changes.
func (s *Server) refuseIfOrphaning(ctx context.Context, samlReachable, oidcReachable bool) (warning string, err error) {
	admins, err := s.users.ListActiveAdmins(ctx)
	if err != nil {
		return "", fmt.Errorf("listing active admins: %w", err)
	}
	stranded := user.StrandedAdmins(admins, samlReachable, oidcReachable)
	if len(stranded) == 0 {
		return "", nil
	}
	if len(stranded) == len(admins) {
		return "", user.ErrWouldOrphanInstance
	}
	names := make([]string, len(stranded))
	for i, u := range stranded {
		names[i] = u.Email
	}
	return fmt.Sprintf(
		"This removes the only way to sign in for: %s. Their accounts are untouched, but they will not be able to authenticate until this is reversed or their access is restored another way.",
		strings.Join(names, ", "),
	), nil
}

// samlReachableNow reports SAML's live reachability, unaffected by whatever
// this request is changing — used when the OIDC settings handler needs to
// know SAML's side of the picture.
func (s *Server) samlReachableNow() bool {
	return s.samlHTTP() != nil
}

// oidcReachableNow reports OIDC's live reachability, unaffected by whatever
// this request is changing — used when the SAML settings handler needs to
// know OIDC's side of the picture.
func (s *Server) oidcReachableNow() bool {
	s.oidcMu.RLock()
	defer s.oidcMu.RUnlock()
	return s.oidcProvider != nil
}

// samlFieldsLookConfigured and oidcFieldsLookConfigured are the field-only
// completeness check the dedicated PUT endpoints used to rely on exclusively,
// before #300's own review round found that it can be wrong in the dangerous
// direction: "the fields are all present" is not "the IdP actually answered",
// and treating them the same let a save through that could silently orphan an
// administrator the moment the real construction attempt failed. The two
// dedicated endpoints no longer use these — they build the real
// middleware/provider first and use that outcome (see server.go) — but
// PATCH /admin/settings (handler_admin_settings.go) does not live-reload
// either provider at all today, so there is no real construction attempt to
// observe there; this field check is what it has, and it is still a real
// improvement over no check at all for the settings-PATCH bypass #300's
// review found (that path could set oidc_enabled=false, or blank any SAML
// field, with zero refusal).
func samlFieldsLookConfigured(enabled bool, metadataURL, certPEM, keyPEM string) bool {
	return enabled && metadataURL != "" && certPEM != "" && keyPEM != ""
}

func oidcFieldsLookConfigured(cfg auth.OIDCConfig) bool {
	return cfg.Enabled && cfg.IssuerURL != "" && cfg.ClientID != "" && cfg.ClientSecret != ""
}
