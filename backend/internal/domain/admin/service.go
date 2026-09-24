package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/antivirus"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// Service provides typed access to the settings table.
type Service struct{ store Store }

// NewService returns a Service backed by the given Store.
func NewService(store Store) *Service { return &Service{store: store} }

// GetBool returns a boolean setting value.
func (s *Service) GetBool(ctx context.Context, key string) (bool, error) {
	raw, err := s.store.Get(ctx, key)
	if err != nil {
		return false, fmt.Errorf("getting setting %q: %w", key, err)
	}
	var v bool
	if err := json.Unmarshal(raw, &v); err != nil {
		return false, fmt.Errorf("parsing setting %q as bool: %w", key, err)
	}
	return v, nil
}

// GetInt returns an integer setting value.
func (s *Service) GetInt(ctx context.Context, key string) (int, error) {
	raw, err := s.store.Get(ctx, key)
	if err != nil {
		return 0, fmt.Errorf("getting setting %q: %w", key, err)
	}
	var v int
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0, fmt.Errorf("parsing setting %q as int: %w", key, err)
	}
	return v, nil
}

// GetString returns a string setting value.
func (s *Service) GetString(ctx context.Context, key string) (string, error) {
	raw, err := s.store.Get(ctx, key)
	if err != nil {
		return "", fmt.Errorf("getting setting %q: %w", key, err)
	}
	var v string
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", fmt.Errorf("parsing setting %q as string: %w", key, err)
	}
	return v, nil
}

// SetBool persists a boolean setting.
func (s *Service) SetBool(ctx context.Context, key string, v bool) error {
	b := []byte(strconv.FormatBool(v))
	return s.store.Set(ctx, key, b)
}

// SetInt persists an integer setting.
func (s *Service) SetInt(ctx context.Context, key string, v int) error {
	b := []byte(strconv.Itoa(v))
	return s.store.Set(ctx, key, b)
}

// SetString persists a string setting.
func (s *Service) SetString(ctx context.Context, key string, v string) error {
	b, _ := json.Marshal(v)
	return s.store.Set(ctx, key, b)
}

// ReopenWindowDays returns the configured reopen window, defaulting to 7.
func (s *Service) ReopenWindowDays(ctx context.Context) int {
	v, err := s.GetInt(ctx, KeyReopenWindowDays)
	if err != nil {
		return 7
	}
	return v
}

// SAMLEnabled returns whether SAML authentication is enabled.
func (s *Service) SAMLEnabled(ctx context.Context) bool {
	v, _ := s.GetBool(ctx, KeySAMLEnabled)
	return v
}

// GetSAMLConfig returns the three SAML SP fields stored in settings.
// Missing keys are returned as empty strings (treated as unconfigured).
func (s *Service) GetSAMLConfig(ctx context.Context) (metadataURL, certPEM, keyPEM string) {
	metadataURL, _ = s.GetString(ctx, KeySAMLMetadataURL)
	certPEM, _ = s.GetString(ctx, KeySAMLCertPEM)
	keyPEM, _ = s.GetString(ctx, KeySAMLKeyPEM)
	return
}

// GetOIDCConfig returns the OIDC configuration stored in settings.
func (s *Service) GetOIDCConfig(ctx context.Context) auth.OIDCConfig {

	enabled, _ := s.GetBool(ctx, KeyOIDCEnabled)

	issuerURL, _ := s.GetString(ctx, KeyOIDCIssuerURL)
	clientID, _ := s.GetString(ctx, KeyOIDCClientID)
	clientSecret, _ := s.GetString(ctx, KeyOIDCClientSecret)
	redirectURL, _ := s.GetString(ctx, KeyOIDCRedirectURL)

	return auth.OIDCConfig{
		Enabled:      enabled,
		IssuerURL:    issuerURL,
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
	}
}

// SAMLConfigured returns true when all three SAML fields are non-empty.
func (s *Service) SAMLConfigured(ctx context.Context) bool {
	u, c, k := s.GetSAMLConfig(ctx)
	return u != "" && c != "" && k != ""
}

// OIDCConfigured returns true when required OIDC settings exist.
func (s *Service) OIDCConfigured(ctx context.Context) bool {

	cfg := s.GetOIDCConfig(ctx)

	return cfg.IssuerURL != "" &&
		cfg.ClientID != "" &&
		cfg.ClientSecret != ""
}

// SetOIDCConfig persists OIDC provider configuration.
func (s *Service) SetOIDCConfig(
	ctx context.Context,
	cfg auth.OIDCConfig,
) error {

	if err := s.SetBool(ctx, KeyOIDCEnabled, cfg.Enabled); err != nil {
		return fmt.Errorf("saving oidc_enabled: %w", err)
	}

	if err := s.SetString(ctx, KeyOIDCIssuerURL, cfg.IssuerURL); err != nil {
		return fmt.Errorf("saving oidc_issuer_url: %w", err)
	}

	if err := s.SetString(ctx, KeyOIDCClientID, cfg.ClientID); err != nil {
		return fmt.Errorf("saving oidc_client_id: %w", err)
	}

	if err := s.SetString(ctx, KeyOIDCClientSecret, cfg.ClientSecret); err != nil {
		return fmt.Errorf("saving oidc_client_secret: %w", err)
	}

	if err := s.SetString(ctx, KeyOIDCRedirectURL, cfg.RedirectURL); err != nil {
		return fmt.Errorf("saving oidc_redirect_url: %w", err)
	}

	return nil
}

// SetSAMLConfig persists the three SAML SP fields.
func (s *Service) SetSAMLConfig(ctx context.Context, metadataURL, certPEM, keyPEM string) error {
	if err := s.SetString(ctx, KeySAMLMetadataURL, metadataURL); err != nil {
		return fmt.Errorf("saving saml_metadata_url: %w", err)
	}
	if err := s.SetString(ctx, KeySAMLCertPEM, certPEM); err != nil {
		return fmt.Errorf("saving saml_cert_pem: %w", err)
	}
	if err := s.SetString(ctx, KeySAMLKeyPEM, keyPEM); err != nil {
		return fmt.Errorf("saving saml_key_pem: %w", err)
	}
	return nil
}

// GuestSubmissionEnabled returns whether unauthenticated ticket submission is allowed.
func (s *Service) GuestSubmissionEnabled(ctx context.Context) bool {
	v, _ := s.GetBool(ctx, KeyGuestSubmissionEnabled)
	return v
}

// SLAEnabled returns whether SLA tracking is active.
// TicketPrefix returns the configured tracking-number prefix, falling back to
// the default when unset or invalid.
func (s *Service) TicketPrefix(ctx context.Context) string {
	v, _ := s.GetString(ctx, KeyTicketPrefix)
	if ticket.ValidateTrackingPrefix(v) != nil {
		return ticket.DefaultTrackingPrefix
	}
	return v
}

// TicketScopeEnforced reports whether staff ticket visibility is limited to
// their group scope.
//
// Defaults to false, and deliberately so. DESIGN.md specifies scoped
// visibility, but no release ever implemented it, so every existing instance
// has staff who can see every ticket. Turning that on during an upgrade would
// hide tickets out from under people mid-conversation; an admin switches it on
// once groups are configured.
func (s *Service) TicketScopeEnforced(ctx context.Context) bool {
	v, _ := s.GetBool(ctx, KeyTicketScopeEnforced)
	return v
}

func (s *Service) SLAEnabled(ctx context.Context) bool {
	v, _ := s.GetBool(ctx, KeySLAEnabled)
	return v
}

// MFAEnabled returns whether MFA is available.
func (s *Service) MFAEnabled(ctx context.Context) bool {
	v, _ := s.GetBool(ctx, KeyMFAEnabled)
	return v
}

// MFAEnforcedRoles returns the roles (as lowercase strings) that must enroll
// in MFA. Users in these roles without enrollment are forced to enroll before
// they can use the system.
func (s *Service) MFAEnforcedRoles(ctx context.Context) []string {
	raw, err := s.store.Get(ctx, KeyMFAEnforcedRoles)
	if err != nil {
		return nil
	}
	var roles []string
	if err := json.Unmarshal(raw, &roles); err != nil {
		return nil
	}
	return roles
}

// MFARequiredFor returns true when MFA is globally enabled AND the given role
// is in the enforced-roles list.
func (s *Service) MFARequiredFor(ctx context.Context, role string) bool {
	if !s.MFAEnabled(ctx) {
		return false
	}
	for _, r := range s.MFAEnforcedRoles(ctx) {
		if r == role {
			return true
		}
	}
	return false
}

// ReopenTargetStatusName returns the name of the status tickets are moved to
// when reopened, defaulting to "New".
func (s *Service) ReopenTargetStatusName(ctx context.Context) string {
	v, err := s.GetString(ctx, KeyReopenTargetStatusName)
	if err != nil {
		return "New"
	}
	return v
}

// SiteName returns the configured site name, defaulting to "Go Help Desk".
func (s *Service) SiteName(ctx context.Context) string {
	v, err := s.GetString(ctx, KeySiteName)
	if err != nil || v == "" {
		return "Go Help Desk"
	}
	return v
}

// SiteLogoURL returns the configured logo URL (empty if not set).
func (s *Service) SiteLogoURL(ctx context.Context) string {
	v, _ := s.GetString(ctx, KeySiteLogoURL)
	return v
}

// AllowedEmailDomains returns the list of permitted email domains. Empty means unrestricted.
func (s *Service) AllowedEmailDomains(ctx context.Context) []string {
	raw, err := s.store.Get(ctx, KeyAllowedEmailDomains)
	if err != nil {
		return nil
	}
	var domains []string
	if err := json.Unmarshal(raw, &domains); err != nil {
		return nil
	}
	return domains
}

// SelfSignupEnabled returns whether the self-service signup page is enabled.
func (s *Service) SelfSignupEnabled(ctx context.Context) bool {
	v, _ := s.GetBool(ctx, KeySelfSignupEnabled)
	return v
}

// OpenRegistrationEnabled returns whether signup is allowed without a domain restriction.
func (s *Service) OpenRegistrationEnabled(ctx context.Context) bool {
	v, _ := s.GetBool(ctx, KeyOpenRegistrationEnabled)
	return v
}

// ListAll returns all settings as raw JSON map.
func (s *Service) ListAll(ctx context.Context) (map[string][]byte, error) {
	return s.store.List(ctx)
}

// SetRaw persists a raw JSON value for the given key.
func (s *Service) SetRaw(ctx context.Context, key string, value []byte) error {
	return s.store.Set(ctx, key, value)
}

// AutoAssignGroupID returns the configured auto-assign group, or nil if not set.
func (s *Service) AutoAssignGroupID(ctx context.Context) *uuid.UUID {
	v, err := s.GetString(ctx, KeyAutoAssignGroupID)
	if err != nil || v == "" {
		return nil
	}
	id, err := uuid.Parse(v)
	if err != nil {
		return nil
	}
	return &id
}

// AutoAssignUserIDs returns the configured round-robin user list, or nil if not set.
func (s *Service) AutoAssignUserIDs(ctx context.Context) []uuid.UUID {
	raw, err := s.store.Get(ctx, KeyAutoAssignUserIDs)
	if err != nil {
		return nil
	}
	var strs []string
	if err := json.Unmarshal(raw, &strs); err != nil {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(strs))
	for _, str := range strs {
		if id, err := uuid.Parse(str); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// AttachmentScanPolicy decides what an unscannable upload means.
//
// Defaults to "required" wherever a scanner address exists, and "off" where
// none does. That default is the whole point of the setting: configuring a
// scanner and then accepting files it could not look at is not a position
// anyone holds deliberately, and the previous behaviour — accept everything,
// log a warning — was that position by accident.
//
// An unrecognised stored value falls back the same way rather than being
// treated as "off", so a typo cannot silently disable scanning.
func (s *Service) AttachmentScanPolicy(ctx context.Context, addrConfigured bool) antivirus.Policy {
	v, _ := s.GetString(ctx, KeyAttachmentScanPolicy)
	if antivirus.ValidPolicy(v) {
		return antivirus.Policy(v)
	}
	if addrConfigured {
		return antivirus.PolicyRequired
	}
	return antivirus.PolicyOff
}

// AttachmentScanAddress is the operator's override for where the scanner
// lives, empty when the environment value should stand.
//
// Follows the same shape as SAML and the rest: the environment sets what the
// instance starts with, and a saved setting takes precedence.
func (s *Service) AttachmentScanAddress(ctx context.Context) string {
	v, _ := s.GetString(ctx, KeyAttachmentScanAddress)
	return strings.TrimSpace(v)
}

// DefaultAllowedTypes is what an instance accepts as an attachment when the
// operator has never said otherwise.
//
// It is exactly the set that was hard-coded in the upload handler before this
// setting existed, neither trimmed to look safer nor extended from memory: an
// instance that upgrades and never touches the setting must accept precisely
// what it accepted before. The extension-to-MIME map in the server package
// still needs an entry for every extension here.
func DefaultAllowedTypes() []string {
	return []string{".pdf", ".docx", ".xlsx", ".txt", ".log", ".jpg", ".jpeg", ".png", ".bmp"}
}

// AllowedTypes is what this instance accepts as an attachment, as a set of
// lowercase extensions with the leading dot, ready for a membership test.
//
// It stopped being a security boundary when every download became
// application/octet-stream with an attachment disposition and nothing is
// rendered (#165 step 1). What is left is a policy choice about what a
// deployment is willing to hold, which belongs to the operator: an IT team
// triaging a suspicious .exe has a real reason to attach one, and a deployment
// that wants PDF and nothing else has an equally real reason to say so.
//
// An absent, null or unparseable value means unset and falls back to the
// default. An empty array does not: it is an operator saying this instance
// takes no attachments at all, which is a legitimate choice.
func (s *Service) AllowedTypes(ctx context.Context) map[string]bool {
	list := DefaultAllowedTypes()
	if raw, err := s.store.Get(ctx, KeyAttachmentAllowedTypes); err == nil {
		var stored []string
		if err := json.Unmarshal(raw, &stored); err == nil && stored != nil {
			list = stored
		}
	}

	set := make(map[string]bool, len(list)+1)
	for _, ext := range list {
		ext = strings.ToLower(strings.TrimSpace(ext))
		set[ext] = true
		// .jpg and .jpeg name one format, so allowing either allows both —
		// the same normalisation attachment.IsMismatch applies, for the same
		// reason. An operator who writes ".jpg" means JPEG images, and being
		// surprised that ".jpeg" also works is a far smaller problem than
		// being surprised that it does not, which reads as a broken setting.
		switch ext {
		case ".jpg":
			set[".jpeg"] = true
		case ".jpeg":
			set[".jpg"] = true
		}
	}
	return set
}

// InfectedHandling decides what happens to an upload the scanner identified as
// malicious: InfectedHandlingRefuse or InfectedHandlingQuarantine.
//
// Defaults to "refuse", which is what every instance that upgrades into this
// feature has. An ordinary help desk fielding printer problems should not
// start storing malware because nobody said otherwise; an operator who wants
// it should have said so.
//
// An unrecognised stored value falls back to "refuse" as well, never to
// "quarantine" — the same rule as the scan policy, and here the value being
// misread decides whether malware is written to disk.
//
// It only ever decides what to do with a verdict the scanner actually
// returned. With the scan policy "off", or the scanner unreachable under
// "permissive", nothing is ever identified as infected and this setting does
// nothing at all.
// ReputationEnabled reports whether this instance asks the named provider
// about quarantined attachment hashes.
//
// False for every provider by default, and false for a name this build does
// not know. All four off is a supported configuration and not a broken one:
// it means attachments are judged by this instance's own scanner alone, which
// is a complete answer and the deliberate choice of an operator who cannot
// send customer file hashes to a third party.
//
// Whether the provider can actually run is a second question — a commercial
// one still needs its key — and it is asked by reputation.CanLookup. The
// settings endpoint refuses the combination that makes the two disagree, so
// "enabled and silently doing nothing" is not a state an operator can reach
// through the API.
func (s *Service) ReputationEnabled(ctx context.Context, provider string) bool {
	enabledKey, _, ok := ReputationSettingKeys(provider)
	if !ok {
		return false
	}
	v, _ := s.GetBool(ctx, enabledKey)
	return v
}

// ReputationKey is the API key for one provider.
//
// Per provider, which the single key this replaced could not do: switching
// from VirusTotal to MetaDefender used to destroy the key you had already
// pasted, and an operator holding keys for two services had to choose between
// them.
//
// Empty for CIRCL, always, because there is no such setting: it authenticates
// nobody. Empty is also the answer for a provider this build does not know.
//
// The value is stored write-only — the settings endpoint accepts it and never
// echoes it back — and it must stay that way on this side too: never logged,
// never wrapped into an error, never returned to a client. The only thing it
// is for is an outbound request header.
//
// Trimmed because an operator pasting a key picks up a trailing newline often
// enough to matter, and because a setting containing nothing but spaces should
// read as unconfigured rather than as a key the provider will reject.
func (s *Service) ReputationKey(ctx context.Context, provider string) string {
	_, apiKeyKey, ok := ReputationSettingKeys(provider)
	if !ok || apiKeyKey == "" {
		return ""
	}
	v, _ := s.GetString(ctx, apiKeyKey)
	return strings.TrimSpace(v)
}

// EnabledReputationProviders is every provider this instance asks, in
// ReputationProviders order.
//
// Ordered rather than a set, because the order decides which provider's answer
// summarises the row when two are equally serious, and an order that came out
// of map iteration would make that summary change between renders.
//
// An empty slice is the ordinary state of a fresh instance and means the
// reputation block is absent from the payload entirely — not "not checked
// yet", which describes a lookup that was attempted and did not finish.
func (s *Service) EnabledReputationProviders(ctx context.Context) []string {
	var out []string
	for _, p := range ReputationProviders() {
		if s.ReputationEnabled(ctx, p) {
			out = append(out, p)
		}
	}
	return out
}

func (s *Service) InfectedHandling(ctx context.Context) string {
	v, _ := s.GetString(ctx, KeyAttachmentInfectedHandling)
	if v == InfectedHandlingQuarantine {
		return InfectedHandlingQuarantine
	}
	return InfectedHandlingRefuse
}

// MismatchHandling decides what happens to an upload whose content contradicts
// its extension, where the detected type is not on the allowlist either:
// MismatchHandlingRefuse or MismatchHandlingWrap.
//
// Defaults to "refuse", which is what every release before this feature did.
// A file that lied about its type was answered with a 415, and an upgrade
// nobody opted into must not quietly start storing one instead.
//
// An unrecognised stored value falls back to "refuse" as well, never to
// "wrap" — the same rule, and the same direction, as InfectedHandling: a typo
// must not land on the permissive option.
//
// It governs one condition and swaps only the action taken on it. A mismatch
// whose detected type IS accepted is flagged and stored under its own name
// either way, and a file the scanner identified is governed by
// InfectedHandling instead; this setting never applies to it.
func (s *Service) MismatchHandling(ctx context.Context) string {
	v, _ := s.GetString(ctx, KeyAttachmentMismatchHandling)
	if v == MismatchHandlingWrap {
		return MismatchHandlingWrap
	}
	return MismatchHandlingRefuse
}

// ReputationRefresh is how often this instance re-checks a stored verdict:
// "weekly", "biweekly" (the default), "monthly", "quarterly" or "never".
//
// Anything unrecognised falls back to the default rather than to "never", and
// the direction matters: a typo must not silently switch automatic re-checking
// off, because the symptom is a verdict that stays on screen looking current
// for as long as the instance lives.
func (s *Service) ReputationRefresh(ctx context.Context) string {
	v, _ := s.GetString(ctx, KeyAttachmentReputationRefresh)
	if ValidReputationRefresh(v) {
		return v
	}
	return ReputationRefreshBiweekly
}

// ReputationRefreshInterval is the same setting as a duration: how old a
// non-detected verdict may be before the next lookup re-fetches it.
//
// Zero means "never", and it is the caller's job to read it that way. An
// interval of zero compared against an age would make every verdict stale on
// every render — the exact opposite of the setting, spending the operator's
// allowance to do it.
func (s *Service) ReputationRefreshInterval(ctx context.Context) time.Duration {
	const day = 24 * time.Hour
	switch s.ReputationRefresh(ctx) {
	case ReputationRefreshWeekly:
		return 7 * day
	case ReputationRefreshMonthly:
		return 30 * day
	case ReputationRefreshQuarterly:
		return 90 * day
	case ReputationRefreshNever:
		return 0
	default:
		return 14 * day
	}
}
