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

	// What happens to an upload whose content contradicts the extension it
	// arrived under, when the type it turned out to be is not one this
	// instance accepts: refuse it, or accept it wrapped and loudly renamed.
	//
	// Default refuse, which is what every release before this one did — a
	// 415. Relaxing a security control in an upgrade nobody opted into is the
	// wrong default for a behaviour only some deployments want. The wrap
	// exists for the IT or security team whose tickets are *about* suspicious
	// files, where refusing the attachment refuses the ticket's whole point.
	//
	// Deliberately the same shape, the same words and the same default as
	// KeyAttachmentInfectedHandling above, because it is the same decision
	// about a different question: does this instance store a file it has
	// reason to distrust, or turn it away? The two stay independent — "the
	// scanner named this" and "the content is not what the name says" are not
	// the same claim, and an operator may reasonably keep one and refuse the
	// other.
	KeyAttachmentMismatchHandling = "attachment_mismatch_handling" // refuse | wrap

	// Deprecated: replaced by the per-provider settings below, and read by
	// nothing.
	//
	// These named VirusTotal because it was the only service considered. There
	// are now four, each with its own toggle and its own key, so a key called
	// "attachment_vt_api_key" holding a MetaDefender key would be a lie in the
	// settings table.
	//
	// Note this pair was superseded twice: first by
	// KeyAttachmentReputationProvider and KeyAttachmentReputationAPIKey, which
	// are themselves now deprecated a few lines below. Pointing a reader at a
	// replacement that is also dead is worse than pointing at nothing, so this
	// names the live settings instead.
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

	// Deprecated: replaced by the seven per-provider settings below, and read
	// by nothing.
	//
	// One selected provider could not express the thing operators actually
	// wanted — two services answering different questions about the same file
	// — and the single key it went with destroyed the key you had already
	// pasted every time you switched. Worse, it carried a state that could not
	// be refused: a commercial provider selected with no key was "configured"
	// and silently did nothing.
	//
	// Kept declared for one release rather than deleted outright, exactly as
	// the attachment_vt_* pair above is. An instance that set either still has
	// the row, and a constant that no longer exists makes that row
	// unexplainable to the next person who finds it. They are removed, with a
	// migration that deletes the rows, in the release after this one.
	//
	// Nothing reads them. An operator who configured a provider and a key
	// before this change has to re-enter that key against the provider they
	// want, which is stated in the release notes; silently copying a secret
	// from one key to another is not something to do on somebody's behalf.
	KeyAttachmentReputationProvider = "attachment_reputation_provider" // Deprecated: unused.
	KeyAttachmentReputationAPIKey   = "attachment_reputation_api_key"  // Deprecated: unused.

	// Whether this instance asks each reputation service about a quarantined
	// attachment's SHA-256, and the key it asks with.
	//
	// Four independent toggles rather than one selected provider, because the
	// four answer different questions and neither subsumes another: VirusTotal
	// counts engines, CIRCL says whether a catalogue has the file on record. A
	// sample one calls "detected" and another calls "known" is telling staff
	// something either alone would hide. Every enabled provider is queried and
	// every answer is stored; the row shows the worst of them and expands to
	// all.
	//
	// All four off is how the lookup is off, and it is a supported
	// configuration rather than a broken one: it means this instance judges
	// attachments by its own scanner alone, which is a complete answer and the
	// legitimate choice of an operator who cannot send customer file hashes
	// anywhere.
	//
	// ENABLING A PROVIDER REQUIRES ITS KEY. The settings endpoint refuses the
	// write with invalid_reputation_config, naming the provider that lacks
	// one. That is the same rule as everywhere else in that handler — a
	// setting accepted and then ignored is worse than a refusal — and it
	// disposes of the old "enabled but silently doing nothing" state by making
	// it unreachable.
	//
	// The keys are write-only over the API: see secretSettingKeys in
	// handler_admin_settings.go. They are never logged, never wrapped into an
	// error and never returned to a client. There are three of them now, which
	// is three times the number of places that rule can be broken.
	//
	// All seven are session-gated: see AuthCriticalKeys. The toggles decide
	// where customers' file hashes go and the keys decide whether they go
	// anywhere, and neither is a decision a leaked API key may make.
	KeyAttachmentReputationVirusTotalEnabled   = "attachment_reputation_virustotal_enabled"   // bool
	KeyAttachmentReputationVirusTotalKey       = "attachment_reputation_virustotal_key"       // write-only
	KeyAttachmentReputationMetaDefenderEnabled = "attachment_reputation_metadefender_enabled" // bool
	KeyAttachmentReputationMetaDefenderKey     = "attachment_reputation_metadefender_key"     // write-only
	KeyAttachmentReputationPolySwarmEnabled    = "attachment_reputation_polyswarm_enabled"    // bool
	KeyAttachmentReputationPolySwarmKey        = "attachment_reputation_polyswarm_key"        // write-only

	// CIRCL has no key setting at all, and the absence is the decision.
	//
	// hashlookup authenticates nobody, so there is no key an operator could
	// supply. An empty box beside the other three is a box somebody feels
	// obliged to fill, and then goes looking for a fault when the feature
	// works without it.
	KeyAttachmentReputationCIRCLEnabled = "attachment_reputation_circl_enabled" // bool

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

// The two values KeyAttachmentMismatchHandling takes.
//
// Constants rather than literals for the same reason as the infected-handling
// pair: the difference between them is whether this instance stores a file it
// has decided it cannot identify, and a typo in a string comparison there
// fails silently in the permissive direction.
const (
	MismatchHandlingRefuse = "refuse"
	MismatchHandlingWrap   = "wrap"
)

// ValidMismatchHandling reports whether v is a value the setting accepts.
//
// Used by the settings endpoint to refuse a write, not by the reader: the
// reader falls back to "refuse", so an unrecognised value is safe but
// baffling — an operator who typed "zip", saw a 204 and expected their
// triage instance to start accepting mislabelled files has been told nothing.
// Same shape, and the same reasoning, as ValidInfectedHandling.
func ValidMismatchHandling(v string) bool {
	return v == MismatchHandlingRefuse || v == MismatchHandlingWrap
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
		// And whether a file whose content contradicts its name is refused or
		// stored wrapped. Same reasoning again: it decides what this instance
		// will hold, and a leaked API key must not be able to switch an
		// instance into accepting files it had decided to turn away.
		KeyAttachmentMismatchHandling,
		// The deprecated VirusTotal pair. Nothing reads them, so gating them
		// protects nothing — they stay listed because they are still
		// writable, and a key that used to require a session should not
		// quietly stop requiring one on the way to being deleted.
		KeyAttachmentVTLookup,
		KeyAttachmentVTAPIKey,
		// The deprecated single-provider pair, for the same reason as the
		// attachment_vt_* one: nothing reads them, they are still writable,
		// and a key that used to require a session should not quietly stop
		// requiring one on the way out.
		KeyAttachmentReputationProvider,
		KeyAttachmentReputationAPIKey,
		// And all seven of the per-provider settings that replaced them. A
		// toggle decides where customers' file hashes go; a key decides
		// whether they go anywhere. Neither is a decision a leaked API key may
		// make, and leaving one of the seven off this list is a way to turn a
		// provider on without a session.
		KeyAttachmentReputationVirusTotalEnabled,
		KeyAttachmentReputationVirusTotalKey,
		KeyAttachmentReputationMetaDefenderEnabled,
		KeyAttachmentReputationMetaDefenderKey,
		KeyAttachmentReputationPolySwarmEnabled,
		KeyAttachmentReputationPolySwarmKey,
		KeyAttachmentReputationCIRCLEnabled,
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

// ReputationProviders is every provider the per-provider settings cover, in
// the order the product presents them.
//
// The spellings are internal/reputation's ProviderVirusTotal,
// ProviderMetaDefender, ProviderPolySwarm and ProviderCIRCL. They are not
// referenced by name here because internal/domain may not import an
// infrastructure package; a test pins that the two lists agree, which is the
// only thing standing between a renamed constant and a setting nobody reads.
func ReputationProviders() []string {
	return []string{"virustotal", "metadefender", "polyswarm", "circl"}
}

// ReputationSettingKeys is the pair of setting names that govern one provider:
// the toggle, and the key it looks up with.
//
// apiKeyKey is empty for a provider that authenticates nobody — CIRCL — and
// that emptiness is the fact callers branch on rather than a special case they
// hard-code. ok is false for a name this build does not cover, so an
// unrecognised provider reads as nothing configured rather than as a lookup
// against settings that do not exist.
func ReputationSettingKeys(provider string) (enabledKey, apiKeyKey string, ok bool) {
	switch provider {
	case "virustotal":
		return KeyAttachmentReputationVirusTotalEnabled, KeyAttachmentReputationVirusTotalKey, true
	case "metadefender":
		return KeyAttachmentReputationMetaDefenderEnabled, KeyAttachmentReputationMetaDefenderKey, true
	case "polyswarm":
		return KeyAttachmentReputationPolySwarmEnabled, KeyAttachmentReputationPolySwarmKey, true
	case "circl":
		return KeyAttachmentReputationCIRCLEnabled, "", true
	}
	return "", "", false
}
