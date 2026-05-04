package admin

import "context"

// Well-known setting keys. The settings table uses JSONB values so any
// JSON-serialisable type can be stored.
const (
	KeySAMLEnabled            = "saml_enabled"
	KeySAMLMetadataURL        = "saml_metadata_url"
	KeySAMLCertPEM            = "saml_cert_pem"
	KeySAMLKeyPEM             = "saml_key_pem"
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
	KeySelfSignupEnabled       = "self_signup_enabled"        // bool
	KeyOpenRegistrationEnabled = "open_registration_enabled"  // bool — allow signup with no domain restriction
)

// Store is the persistence interface for the key/value settings table.
type Store interface {
	Get(ctx context.Context, key string) ([]byte, error)   // returns raw JSON value
	Set(ctx context.Context, key string, value []byte) error
	List(ctx context.Context) (map[string][]byte, error)
}
