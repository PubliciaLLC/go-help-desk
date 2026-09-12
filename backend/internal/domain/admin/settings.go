package admin

import "context"

// Well-known setting keys. The settings table uses JSONB values so any
// JSON-serialisable type can be stored.
const (
	KeySAMLEnabled     = "saml_enabled"
	KeySAMLMetadataURL = "saml_metadata_url"
	KeySAMLCertPEM     = "saml_cert_pem"
	KeySAMLKeyPEM      = "saml_key_pem"

	KeyOIDCEnabled            = "oidc_enabled"
	KeyOIDCIssuerURL          = "oidc_issuer_url"
	KeyOIDCClientID           = "oidc_client_id"
	KeyOIDCClientSecret       = "oidc_client_secret"
	KeyOIDCRedirectURL        = "oidc_redirect_url"
	KeyGuestSubmissionEnabled = "guest_submission_enabled"
	KeySLAEnabled             = "sla_enabled"
	KeyMFAEnabled             = "mfa_enabled"
	KeyMFAEnforcedRoles       = "mfa_enforced_roles"
	KeyReopenWindowDays       = "reopen_window_days"
	KeyReopenTargetStatusName = "reopen_target_status_name"
	KeySiteName               = "site_name"
	KeySiteLogoURL            = "site_logo_url"

	// Registration settings.
	KeyAllowedEmailDomains     = "allowed_email_domains"     // []string — empty = unrestricted for SAML JIT
	KeySelfSignupEnabled       = "self_signup_enabled"       // bool
	KeyOpenRegistrationEnabled = "open_registration_enabled" // bool — allow signup with no domain restriction

	// Tracking-number prefix, e.g. the GHD in GHD-2026-000001. Uppercase
	// letters and digits, 1-8 characters. Changing it affects only tickets
	// created afterwards; existing tracking numbers are never rewritten,
	// because they are already in customers' inboxes.
	KeyTicketPrefix = "ticket_prefix" // string

	// Ticket visibility. Off by default: every release before this let any
	// staff member see every ticket, and switching that on silently during an
	// upgrade would hide tickets people are working on.
	KeyTicketScopeEnforced = "ticket_scope_enforced" // bool

	// Auto-assign settings. Group takes priority over users; if neither is set, tickets stay unassigned.
	KeyAutoAssignGroupID = "auto_assign_group_id" // string UUID — assign new tickets to this group
	KeyAutoAssignUserIDs = "auto_assign_user_ids" // []string UUIDs — round-robin among these users
)

// Store is the persistence interface for the key/value settings table.
type Store interface {
	Get(ctx context.Context, key string) ([]byte, error) // returns raw JSON value
	Set(ctx context.Context, key string, value []byte) error
	List(ctx context.Context) (map[string][]byte, error)
}
