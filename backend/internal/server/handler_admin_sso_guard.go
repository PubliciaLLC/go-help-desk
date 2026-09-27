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

// samlReachableAfter reports whether SAML login would work once these three
// fields are saved — mirroring reloadSAML's own gate exactly: any field
// blank clears the middleware unconditionally, with no fail-safe carve-out
// for "incomplete" the way OIDC has one (see oidcReachableAfter). A
// construction failure after fields are complete (bad metadata, a cert that
// does not parse) is not modelled here — that leaves the previous middleware
// in place per reloadSAML's own comment, so treating "fields complete" as
// reachable is the same optimistic assumption oidcReachableAfter makes for a
// discovery failure, not a new one.
func samlReachableAfter(metadataURL, certPEM, keyPEM string) bool {
	return metadataURL != "" && certPEM != "" && keyPEM != ""
}

// samlReachableNow reports SAML's live reachability, unaffected by whatever
// this request is changing — used when the OIDC settings handler needs to
// know SAML's side of the picture.
func (s *Server) samlReachableNow() bool {
	return s.samlHTTP() != nil
}

// oidcReachableAfter reports whether OIDC login would work once cfg is
// saved and InitOIDC runs against it, mirroring InitOIDC's own fail-safe
// exactly: disabling clears the provider unconditionally, but an enabled,
// INCOMPLETE config leaves the existing provider untouched rather than
// clearing it — so reachability in that case is whatever it already was, not
// automatically false. A discovery failure against a complete config is not
// modelled (same accepted optimism as samlReachableAfter): InitOIDC also
// leaves the previous provider in place there, so "complete" is treated as
// reachable.
func (s *Server) oidcReachableAfter(cfg auth.OIDCConfig) bool {
	if !cfg.Enabled {
		return false
	}
	if cfg.IssuerURL == "" || cfg.ClientID == "" || cfg.ClientSecret == "" {
		return s.oidcReachableNow()
	}
	return true
}

// oidcReachableNow reports OIDC's live reachability, unaffected by whatever
// this request is changing — used when the SAML settings handler needs to
// know OIDC's side of the picture.
func (s *Server) oidcReachableNow() bool {
	s.oidcMu.RLock()
	defer s.oidcMu.RUnlock()
	return s.oidcProvider != nil
}
