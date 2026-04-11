package admin

import "context"

// Well-known setting keys. The settings table uses JSONB values so any
// JSON-serialisable type can be stored.
const (
	KeySAMLEnabled             = "saml_enabled"
	KeyGuestSubmissionEnabled  = "guest_submission_enabled"
	KeySLAEnabled              = "sla_enabled"
	KeyMFAEnabled              = "mfa_enabled"
	KeyMFAEnforcedRoles        = "mfa_enforced_roles"
	KeyReopenWindowDays        = "reopen_window_days"
	KeyReopenTargetStatusName  = "reopen_target_status_name"
)

// Store is the persistence interface for the key/value settings table.
type Store interface {
	Get(ctx context.Context, key string) ([]byte, error)   // returns raw JSON value
	Set(ctx context.Context, key string, value []byte) error
	List(ctx context.Context) (map[string][]byte, error)
}
