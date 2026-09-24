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
	KeyAttachmentScanPolicy   = "attachment_scan_policy" // off | required | permissive
	KeyAttachmentScanAddress  = "attachment_scan_address"
	KeySiteName               = "site_name"
	KeySiteLogoURL            = "site_logo_url"

	// What this instance accepts as an attachment: a JSON array of lowercase
	// extensions with the leading dot, e.g. [".pdf", ".png"]. An empty array
	// is a legitimate choice and means no attachments at all, not "unset".
	KeyAttachmentAllowedTypes = "attachment_allowed_types" // []string

	// What happens to an upload the scanner calls infected: refuse it, or
	// accept it wrapped and loudly labelled. Default refuse — an ordinary help
	// desk should not start storing malware because nobody said otherwise.
	KeyAttachmentInfectedHandling = "attachment_infected_handling" // refuse | quarantine

	// Deprecated: replaced by KeyAttachmentReputationProvider and
	// KeyAttachmentReputationAPIKey, and read by nothing.
	//
	// These named VirusTotal because it was the only service considered. It is
	// now one of three an operator can choose between, so a key called
	// "attachment_vt_api_key" holding a MetaDefender key would be a lie in the
	// settings table.
	//
	// Kept declared for one release rather than deleted outright. An instance
	// that set either still has the row, and a constant that no longer exists
	// makes that row unexplainable to the next person who finds it. They are
	// removed, with a migration that deletes the rows, in the release after
	// this one.
	//
	// Nothing reads them. An operator who set a VirusTotal key before this
	// change has to paste it into the new field, which is stated in the
	// release notes; silently copying a secret from one key to another is not
	// something to do on somebody's behalf.
	KeyAttachmentVTLookup = "attachment_vt_lookup"  // Deprecated: unused.
	KeyAttachmentVTAPIKey = "attachment_vt_api_key" // Deprecated: unused.

	// Which reputation service this instance uses for attachment hashes.
	// "virustotal" (the default), "metadefender", "polyswarm" or "circl".
	//
	// One setting with two effects: it picks the server-side lookup, and it
	// picks the service the SHA-256 in the UI links to. Keeping them together
	// is the point — an operator who chose MetaDefender should never find a
	// VirusTotal link next to a MetaDefender verdict.
	//
	// The link half needs no key and makes no server call, so this setting
	// does something useful on an instance that never configures a lookup at
	// all. An unrecognised value falls back to virustotal, and the settings
	// endpoint refuses the write outright with invalid_reputation_provider —
	// same rule, and the same reason, as the scan policy.
	KeyAttachmentReputationProvider = "attachment_reputation_provider" // virustotal | metadefender | polyswarm | circl

	// The API key for whichever provider is selected. Write-only over the API:
	// see secretSettingKeys in handler_admin_settings.go.
	//
	// For the three commercial providers this is also the on switch: no key,
	// no lookup, and there is deliberately no separate "enabled" boolean, so
	// there is no such thing as enabled-with-no-key. CIRCL is the exception
	// that made the rule insufficient rather than wrong — it authenticates
	// nobody, so there is no key that could switch it on or off. The rule that
	// covers all four: a lookup runs when the configured provider CAN run. See
	// reputation.CanLookup.
	KeyAttachmentReputationAPIKey = "attachment_reputation_api_key"

	// How often a stored verdict is re-checked: weekly, biweekly (the
	// default), monthly, quarterly or never.
	//
	// A verdict decays, which is the whole reason this exists. New signatures
	// catch old malware, and a sample nobody had submitted when we asked is
	// precisely the one that gets submitted a week later — so a quarantined
	// attachment could sit on a ticket reading "0 of 78 engines" indefinitely
	// while the real answer changed months ago.
	//
	// It governs only the automatic re-check, and only for a non-detected
	// verdict: a detection never expires, and staff can always ask again by
	// hand. "never" turns the automatic half off and nothing else.
	//
	// An unrecognised value falls back to biweekly, and the settings endpoint
	// refuses the write outright with invalid_reputation_refresh — same rule,
	// and the same reason, as the scan policy.
	KeyAttachmentReputationRefresh = "attachment_reputation_refresh" // weekly | biweekly | monthly | quarterly | never

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

// The two values KeyAttachmentInfectedHandling takes.
//
// Spelled out as constants rather than compared against literals because the
// difference between them is whether this instance stores malware, and a typo
// in a string comparison there fails silently in the permissive direction.
const (
	InfectedHandlingRefuse     = "refuse"
	InfectedHandlingQuarantine = "quarantine"
)

// ValidInfectedHandling reports whether v is a value the setting accepts.
//
// Used by the settings endpoint to refuse a write, not by the reader: the
// reader falls back to "refuse", so an unrecognised value is safe but
// baffling. Refusing it at save time says what is wrong instead. Same shape,
// and the same reasoning, as antivirus.ValidPolicy.
func ValidInfectedHandling(v string) bool {
	return v == InfectedHandlingRefuse || v == InfectedHandlingQuarantine
}

// Store is the persistence interface for the key/value settings table.
type Store interface {
	Get(ctx context.Context, key string) ([]byte, error) // returns raw JSON value
	Set(ctx context.Context, key string, value []byte) error
	List(ctx context.Context) (map[string][]byte, error)
}

// AuthCriticalKeys are the settings that decide who may authenticate and how.
//
// They are grouped here rather than checked ad hoc at the handler so that a
// setting added to this list is enforced everywhere it is read, and so the list
// itself is reviewable. Changing any of them is a route to a session the caller
// should not have: turning MFA off instance-wide, repointing SAML or OIDC at an
// identity provider the caller controls, or opening registration.
func AuthCriticalKeys() []string {
	return []string{
		KeySAMLEnabled, KeySAMLMetadataURL, KeySAMLCertPEM, KeySAMLKeyPEM,
		KeyOIDCEnabled, KeyOIDCIssuerURL, KeyOIDCClientID, KeyOIDCClientSecret,
		KeyOIDCRedirectURL,
		KeyMFAEnabled, KeyMFAEnforcedRoles,
		KeyAllowedEmailDomains, KeySelfSignupEnabled, KeyOpenRegistrationEnabled,
		// Where the virus scanner lives is a route to disabling scanning
		// entirely: point it at a daemon that answers OK to everything and
		// every upload passes. A leaked API key should not be able to do that,
		// for the same reason it cannot repoint the identity provider.
		KeyAttachmentScanAddress,
		// And the policy, since "off" reaches the same outcome by a shorter
		// path.
		KeyAttachmentScanPolicy,
		// What the instance accepts at all. A leaked API key must not be able
		// to widen the type list and then upload what it has just allowed.
		KeyAttachmentAllowedTypes,
		// Whether an infected upload is refused or stored. Same reasoning as
		// the scan policy: it decides what this instance will hold.
		KeyAttachmentInfectedHandling,
		// The deprecated VirusTotal pair. Nothing reads them, so gating them
		// protects nothing — they stay listed because they are still
		// writable, and a key that used to require a session should not
		// quietly stop requiring one on the way to being deleted.
		KeyAttachmentVTLookup,
		KeyAttachmentVTAPIKey,
		// And both halves of the provider-agnostic replacement, for the same
		// reason: the provider decides where customers' file hashes go, and
		// the key decides whether they go anywhere.
		KeyAttachmentReputationProvider,
		KeyAttachmentReputationAPIKey,
		// And how often they go. Stretching the interval to "never" from a
		// leaked API key is a quiet way to freeze every verdict on the
		// instance at whatever it said the day the key was stolen.
		KeyAttachmentReputationRefresh,
	}
}

// The five values KeyAttachmentReputationRefresh takes.
//
// Constants rather than literals for the same reason as the infected-handling
// pair: the reader, the validator and the interval table all have to agree on
// the spelling, and a typo in any of them fails silently — as a verdict that
// never refreshes, or an allowance that drains four times too fast.
const (
	ReputationRefreshWeekly    = "weekly"
	ReputationRefreshBiweekly  = "biweekly"
	ReputationRefreshMonthly   = "monthly"
	ReputationRefreshQuarterly = "quarterly"
	ReputationRefreshNever     = "never"
)

// ValidReputationRefresh reports whether v is a value the setting accepts.
//
// Used by the settings endpoint to refuse a write, not by the reader: the
// reader falls back to biweekly, so an unrecognised value is safe but
// baffling — an operator who typed "fortnightly", saw a 204 and expected their
// quarterly instance to have stopped re-checking has been told nothing. Same
// shape, and the same reasoning, as ValidInfectedHandling.
func ValidReputationRefresh(v string) bool {
	switch v {
	case ReputationRefreshWeekly, ReputationRefreshBiweekly, ReputationRefreshMonthly,
		ReputationRefreshQuarterly, ReputationRefreshNever:
		return true
	}
	return false
}
