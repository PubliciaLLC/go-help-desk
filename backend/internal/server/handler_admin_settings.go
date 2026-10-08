package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/publiciallc/go-help-desk/backend/internal/antivirus"
	"github.com/publiciallc/go-help-desk/backend/internal/reputation"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Admin instance settings, and the denylist of keys never returned to a client.
//
// Split out of handler_admin.go, which had grown to 1,332 lines across twelve
// unrelated resources. Moved verbatim: no handler logic changed.

// ── Settings ─────────────────────────────────────────────────────────────────

// secretSettingKeys are write-only over the API: they are accepted by
// PATCH /admin/settings but never returned by the settings dump. The dedicated
// endpoints blank them for the same reason (see handleGetOIDCConfig).
var secretSettingKeys = map[string]struct{}{
	admin.KeyOIDCClientSecret:   {},
	admin.KeySAMLKeyPEM:         {},
	admin.KeyAttachmentVTAPIKey: {},

	// The deprecated single reputation key. Read by nothing, still writable,
	// and still never echoed: a secret does not stop being a secret on its way
	// to being deleted.
	admin.KeyAttachmentReputationAPIKey: {},

	// One key per commercial reputation provider. Accepted on PATCH, never
	// echoed by the settings dump: an admin session that can read one back can
	// exfiltrate the operator's key to whatever the provider's terms attach to
	// it. There are three of them now, and a provider added here and forgotten
	// is a key the settings dump hands out.
	//
	// CIRCL has no entry because it has no key setting at all.
	admin.KeyAttachmentReputationVirusTotalKey:   {},
	admin.KeyAttachmentReputationMetaDefenderKey: {},
	admin.KeyAttachmentReputationPolySwarmKey:    {},
}

// attachmentExtPattern is what an entry in the attachment allowlist may look
// like. Deliberately narrow: an extension is compared against
// strings.ToLower(filepath.Ext(name)), so an entry with no leading dot, an
// uppercase letter or an inner space can never match any upload. Accepting one
// would leave the setting doing nothing while reporting success.
var attachmentExtPattern = regexp.MustCompile(`^\.[a-z0-9]{1,16}$`)

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	all, err := s.adminSvc.ListAll(r.Context())
	if err != nil {
		handleError(w, err)
		return
	}
	// Convert raw bytes to JSON-parseable map, omitting secrets.
	//
	// A secret is replaced by a "<key>_set" boolean rather than simply
	// dropped. Dropping it alone leaves the administration UI unable to tell
	// a configured key from an absent one — the key is missing from the dump
	// either way — so the page can only say "we cannot show you whether this
	// is set", which is useless to the person deciding whether to paste a new
	// one. The dedicated OIDC and SAML endpoints already report a `configured`
	// flag for exactly this reason; the settings dump had no equivalent.
	//
	// The flag says whether a non-empty value is stored and nothing else. It
	// cannot be used to confirm a guess at the value, which is what echoing
	// the secret itself would allow.
	out := make(map[string]json.RawMessage, len(all)+len(secretSettingKeys))

	// Every declared secret gets a flag, whether or not a row exists. Emitting
	// one only for keys already in the table leaves an unset secret with no
	// flag at all — which is the state this exists to describe, and would put
	// the UI back where it started, unable to tell "not configured" from
	// "the server did not say".
	for k := range secretSettingKeys {
		out[k+"_set"] = json.RawMessage("false")
	}
	for k, v := range all {
		if _, secret := secretSettingKeys[k]; secret {
			out[k+"_set"] = json.RawMessage(strconv.FormatBool(hasSecretValue(v)))
			continue
		}
		out[k] = json.RawMessage(v)
	}
	JSON(w, http.StatusOK, out)
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var body map[string]json.RawMessage
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	// The settings that decide who may authenticate, and how, are off limits to
	// machine credentials for the same reason /me/password is: they are a route
	// to becoming someone rather than acting for them. Turning MFA off
	// instance-wide, pointing SAML or OIDC at another IdP, or opening
	// registration each get an attacker a session they should not have.
	//
	// Everything else here — site name, SLA toggle, reopen window — is ordinary
	// configuration and stays available.
	if isMachine(r) {
		for _, k := range admin.AuthCriticalKeys() {
			if _, ok := body[k]; ok {
				Error(w, http.StatusForbidden, "session_required",
					"an API key or OAuth client cannot change "+k+"; this requires a signed-in session")
				return
			}
		}
	}

	// The settings dump emits a synthetic "<key>_set" boolean for every
	// write-only secret, so the UI can tell a stored key from an absent one
	// without the key itself ever being returned. It is a fact about the
	// table, not a row in it.
	//
	// Nothing stopped a client sending it straight back. A page that reads the
	// dump, edits one field and PATCHes the whole object wrote real settings
	// rows called oidc_client_secret_set, saml_key_pem_set and
	// attachment_reputation_virustotal_key_set — rows nothing ever reads,
	// sitting in the settings table looking like configuration. The frontend
	// has been doing exactly that since the flags landed.
	//
	// So the "_set" suffix is reserved: no declared setting key ends in it,
	// and none may, because the dump would then be unable to tell the flag
	// from the setting. A key ending in "_set" is refused by name rather than
	// dropped, for the same reason as everything else in this handler — a
	// value accepted and then ignored is worse than a refusal.
	for k := range body {
		if strings.HasSuffix(k, "_set") {
			Error(w, http.StatusBadRequest, "readonly_setting",
				k+" is a read-only presence flag from the settings dump, not a setting; "+
					"send the key itself to change it")
			return
		}
	}

	// guest_submission_enabled decides whether anonymous people on the
	// internet can file tickets into this instance, and — since the category
	// catalogue stopped being anonymous (see requireSignedInOrGuestsEnabled
	// in server.go) — also whether that catalogue is readable without a
	// session. It had no validation case at all: a JSON string, a number, or
	// the literal null was accepted and read back as false by GetBool's own
	// silent fallback, the same "accepted and then ignored" shape this
	// handler refuses everywhere else. unmarshalSetting is what the #304
	// round found necessary for the identical shape on the SSO keys — null
	// unmarshals into a bool as a silent no-op, not an error, so a bare type
	// check alone would still miss it. See #177.
	if raw, ok := body[admin.KeyGuestSubmissionEnabled]; ok {
		var enabled bool
		if err := unmarshalSetting(raw, "guest_submission_enabled", &enabled); err != nil {
			handleError(w, err)
			return
		}
	}

	// Validate before writing anything. ticket.go documents the prefix as
	// "enforced where the setting is saved rather than where a ticket is
	// created" — nothing enforced it, so an invalid prefix was accepted with a
	// 204 and then silently ignored at mint time in favour of the default. The
	// admin's setting simply did nothing, and nothing ever said so.
	if raw, ok := body[admin.KeyTicketPrefix]; ok {
		var prefix string
		if err := json.Unmarshal(raw, &prefix); err != nil {
			Error(w, http.StatusBadRequest, "bad_request", "ticket prefix must be a string")
			return
		}
		if err := ticket.ValidateTrackingPrefix(prefix); err != nil {
			Error(w, http.StatusBadRequest, "invalid_ticket_prefix",
				"ticket prefix must be 1-8 characters, uppercase letters or digits only")
			return
		}
	}

	// Retention decides how long evidence is kept, so a value that is
	// accepted and then ignored is the worst case of the rule below: an
	// operator who types "90" and gets a silent fallback believes they have a
	// 90-day window and has an unbounded table.
	//
	// unmarshalSetting, not a bare json.Unmarshal: the bare form treats the
	// literal null as a no-op that returns no error, so {"audit_retention_days":
	// null} answered 204 and silently turned a configured 30-day window into
	// "forever". That is the third time this repo has shipped that shape —
	// #177 and #304 were the first two, and this helper exists because of
	// them. Using it was the whole point of it existing.
	//
	// Zero and negative ARE valid and mean forever (admin.AuditRetentionDays).
	// What is refused is a value that is not a number, and a value so large
	// that it wraps.
	if raw, ok := body[admin.KeyAuditRetentionDays]; ok {
		var days int
		if err := unmarshalSetting(raw, "audit_retention_days", &days); err != nil {
			handleError(w, err)
			return
		}
		if days > admin.AuditRetentionMaxDays {
			Error(w, http.StatusBadRequest, "bad_request", fmt.Sprintf(
				"audit retention must be at most %d days (about a century); 0 or less keeps entries forever",
				admin.AuditRetentionMaxDays))
			return
		}
	}

	// Who may force-reopen a Closed ticket (#349). The reader falls back to
	// "off", so a typo would leave an operator who just opened this up
	// believing Closed tickets can be reopened — safe, and baffling. Refused by
	// name instead, and null with it.
	if raw, ok := body[admin.KeyClosedReopenPolicy]; ok {
		var policy string
		if err := unmarshalSetting(raw, "closed_reopen_policy", &policy); err != nil {
			handleError(w, err)
			return
		}
		if !ticket.ValidClosedReopenPolicy(policy) {
			Error(w, http.StatusBadRequest, "invalid_closed_reopen_policy",
				"closed reopen policy must be one of: off, admin, staff_admin")
			return
		}
	}

	// The diff toggle is a bool and nothing else, null included.
	if raw, ok := body[admin.KeyStaffCanViewTicketChangeHistory]; ok {
		var on bool
		if err := unmarshalSetting(raw, "staff_can_view_ticket_change_history", &on); err != nil {
			handleError(w, err)
			return
		}
	}

	// Same reasoning as the prefix above: a value that is accepted and then
	// ignored is worse than a refusal, and here the ignored value is a
	// security control. An unrecognised policy falls back to "required", so a
	// typo would silently refuse every upload rather than disable scanning —
	// safe, but baffling. Refusing it says what is wrong instead.
	if raw, ok := body[admin.KeyAttachmentScanPolicy]; ok {
		var policy string
		if err := json.Unmarshal(raw, &policy); err != nil {
			Error(w, http.StatusBadRequest, "bad_request", "scan policy must be a string")
			return
		}
		if !antivirus.ValidPolicy(policy) {
			Error(w, http.StatusBadRequest, "invalid_scan_policy",
				"scan policy must be one of: off, required, permissive")
			return
		}
	}

	// The scanner address is a network target an operator supplies, so it goes
	// through the same guard as webhook targets — with one deliberate
	// difference: private addresses are ALLOWED here. A ClamAV on the same
	// private network is the normal deployment, exactly as for a self-hosted
	// identity provider, and refusing it would break the default compose file.
	// What is checked is that it is a well-formed address of a scheme we can
	// dial, so a typo fails at save time rather than at the first upload.
	if raw, ok := body[admin.KeyAttachmentScanAddress]; ok {
		var addr string
		if err := json.Unmarshal(raw, &addr); err != nil {
			Error(w, http.StatusBadRequest, "bad_request", "scanner address must be a string")
			return
		}
		if addr != "" && !validScannerAddr(addr) {
			Error(w, http.StatusBadRequest, "invalid_scanner_address",
				`scanner address must look like "tcp://host:port" or "unix:///path/to/socket"`)
			return
		}
	}

	// And what happens to an upload the scanner calls infected. The reader
	// falls back to "refuse", so a typo here would quietly refuse malware an
	// infosec team had deliberately asked to keep — safe, and baffling for
	// exactly the operator who went looking for this setting.
	if raw, ok := body[admin.KeyAttachmentInfectedHandling]; ok {
		var handling string
		if err := json.Unmarshal(raw, &handling); err != nil {
			Error(w, http.StatusBadRequest, "bad_request", "infected attachment handling must be a string")
			return
		}
		if !admin.ValidInfectedHandling(handling) {
			Error(w, http.StatusBadRequest, "invalid_infected_handling",
				"infected attachment handling must be one of: refuse, quarantine")
			return
		}
	}

	// And what happens to a file whose content contradicts its name. Same
	// reasoning as the setting above, which this deliberately mirrors: the
	// reader falls back to "refuse", so a typo would quietly go on refusing
	// the mislabelled files a triage team had just asked to keep — safe, and
	// baffling for exactly the operator who went looking for this setting.
	//
	// "quarantine" is the value most likely to be typed here by mistake,
	// because it is the other setting's word, and it is refused rather than
	// charitably read as "wrap": guessing at intent is how an operator ends
	// up with a policy nobody wrote.
	if raw, ok := body[admin.KeyAttachmentMismatchHandling]; ok {
		var handling string
		if err := json.Unmarshal(raw, &handling); err != nil {
			Error(w, http.StatusBadRequest, "bad_request", "mismatched attachment handling must be a string")
			return
		}
		if !admin.ValidMismatchHandling(handling) {
			Error(w, http.StatusBadRequest, "invalid_mismatch_handling",
				"mismatched attachment handling must be one of: refuse, wrap")
			return
		}
	}

	// Enabling a provider requires its key, and the whole write is refused
	// when one is missing.
	//
	// Same rule as every other setting in this handler — a value accepted and
	// then ignored is worse than a refusal — and here the ignored value is a
	// provider an operator believes is answering. "Enabled with no key" was a
	// reachable state under the setting this replaced, and it looked exactly
	// like a provider that had nothing to say.
	if err := validateReputationConfig(r.Context(), s.adminSvc, body); err != nil {
		Error(w, http.StatusBadRequest, "invalid_reputation_config", err.Error())
		return
	}

	// And how often a stored verdict is re-checked. The reader falls back to
	// biweekly, so a typo would leave an operator who chose "never" to save
	// quota still spending it, or one who chose "weekly" reading a verdict a
	// fortnight old — and in both cases the page looks exactly as it should.
	if raw, ok := body[admin.KeyAttachmentReputationRefresh]; ok {
		var refresh string
		if err := json.Unmarshal(raw, &refresh); err != nil {
			Error(w, http.StatusBadRequest, "bad_request", "reputation refresh must be a string")
			return
		}
		if !admin.ValidReputationRefresh(refresh) {
			Error(w, http.StatusBadRequest, "invalid_reputation_refresh",
				"reputation refresh must be one of: weekly, biweekly, monthly, quarterly, never")
			return
		}
	}

	// What this instance accepts as an attachment. Same reasoning again, and
	// here the ignored value decides what the deployment will hold: an
	// operator who types "exe" instead of ".exe" and sees a 204 has been told
	// their instance now takes executables when it does not.
	//
	// The whole write is refused rather than the bad entry dropped, because a
	// list silently missing one of its entries is the same lie in a quieter
	// form.
	if raw, ok := body[admin.KeyAttachmentAllowedTypes]; ok {
		var types []string
		if err := json.Unmarshal(raw, &types); err != nil {
			Error(w, http.StatusBadRequest, "bad_request",
				`allowed attachment types must be a JSON array of extension strings, e.g. [".pdf", ".png"]`)
			return
		}
		for _, ext := range types {
			if !attachmentExtPattern.MatchString(ext) {
				Error(w, http.StatusBadRequest, "invalid_allowed_types",
					fmt.Sprintf("%q is not an attachment extension: each entry is a leading dot "+
						"followed by 1-16 lowercase letters or digits, e.g. \".pdf\"", ext))
				return
			}
		}
	}

	// #300's guard, and the reason it lives here too rather than only on the
	// two dedicated PUT endpoints: this generic PATCH can set oidc_enabled or
	// blank a SAML field exactly as they would, since AuthCriticalKeys() only
	// blocks MACHINE credentials from touching these — a signed-in human
	// session reaches them the same way it reaches the dedicated endpoints,
	// and unlike those, this one had no guard at all. See ssoSettingsWarning's
	// own comment for why the reachability read here is the simpler
	// field-completeness kind rather than a real construction attempt.
	warning, err := s.ssoSettingsWarning(r.Context(), body)
	if err != nil {
		handleError(w, err)
		return
	}

	for k, v := range body {
		if err := s.adminSvc.SetRaw(r.Context(), k, []byte(v)); err != nil {
			handleError(w, err)
			return
		}
	}

	if warning != "" {
		JSON(w, http.StatusOK, map[string]any{"warning": warning})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ssoSettingsWarning runs #300's stranding guard against a generic
// PATCH /admin/settings body, when it touches any key that decides SAML's or
// OIDC's reachability. Merges the body's candidate values over what is
// currently stored — a key the caller left out keeps its stored value, same
// as the dedicated endpoints' own "omitted preserves" contract — then reads
// reachability with samlFieldsLookConfigured/oidcFieldsLookConfigured rather
// than by actually building the middleware/provider: this endpoint does not
// live-reload either one today (a pre-existing gap this fix does not widen
// its scope to close), so there is no real construction attempt to observe
// here the way the two dedicated endpoints now have. The field-only read is
// still a genuine improvement over the no-check-at-all this PATCH had before
// — it is what stopped this route being a complete, unconditional bypass of
// the dedicated endpoints' own refusal.
//
// saml_enabled is deliberately NOT one of the keys this watches. #304's
// first draft treated it as SAML's counterpart to oidc_enabled; it turned
// out to already be a real, unrelated setting (user.IsLocalAuthAllowed
// reads it to decide whether non-admins keep password login once SAML is
// configured), and reverting that draft took SAML reachability back to
// depending on the three config fields alone — see samlFieldsLookConfigured
// and buildSAMLMiddleware's own comments.
//
// Every merge step below refuses on a JSON type mismatch, INCLUDING the
// literal null, rather than discarding the error and keeping the old value
// — found by review on #304, in two rounds: a malformed value
// (oidc_enabled sent as the JSON STRING "false" rather than the boolean
// false, say) would previously leave THIS guard reasoning about the OLD
// value, concluding truthfully-but-uselessly that nothing about
// reachability changed; the JSON literal null is the same failure mode by a
// different mechanism — encoding/json's Unmarshal treats null into a
// non-pointer destination as a silent no-op, no error at all, so the
// type-mismatch check alone still let {"oidc_enabled": null} straight
// through against a sole OIDC-only administrator. Either way the SetRaw
// loop below persists the malformed value regardless, and the real reader
// every other code path uses (GetBool/GetString, under OIDCEnabled/
// GetOIDCConfig/GetSAMLConfig) fails the identical unmarshal and silently
// returns the zero value, flipping the persisted setting to disabled/blank
// from that write onward. validateReputationConfig's own comment two
// hundred lines up already refuses the type-mismatch shape for the
// reputation toggles ("a setting accepted and then ignored is worse than a
// refusal"); unmarshalSetting below is that same rule, extended to also
// catch null, for every SSO key this function reads.
func (s *Server) ssoSettingsWarning(ctx context.Context, body map[string]json.RawMessage) (string, error) {
	touchesSAML := false
	touchesOIDC := false
	for _, k := range []string{admin.KeySAMLMetadataURL, admin.KeySAMLCertPEM, admin.KeySAMLKeyPEM} {
		if _, ok := body[k]; ok {
			touchesSAML = true
		}
	}
	for _, k := range []string{admin.KeyOIDCEnabled, admin.KeyOIDCIssuerURL, admin.KeyOIDCClientID, admin.KeyOIDCClientSecret} {
		if _, ok := body[k]; ok {
			touchesOIDC = true
		}
	}
	if !touchesSAML && !touchesOIDC {
		return "", nil
	}

	metadataURL, certPEM, keyPEM := s.adminSvc.GetSAMLConfig(ctx)
	if raw, ok := body[admin.KeySAMLMetadataURL]; ok {
		if err := unmarshalSetting(raw, "saml_metadata_url", &metadataURL); err != nil {
			return "", err
		}
	}
	if raw, ok := body[admin.KeySAMLCertPEM]; ok {
		if err := unmarshalSetting(raw, "saml_cert_pem", &certPEM); err != nil {
			return "", err
		}
	}
	if raw, ok := body[admin.KeySAMLKeyPEM]; ok {
		if err := unmarshalSetting(raw, "saml_key_pem", &keyPEM); err != nil {
			return "", err
		}
	}

	cfg := s.adminSvc.GetOIDCConfig(ctx)
	if raw, ok := body[admin.KeyOIDCEnabled]; ok {
		if err := unmarshalSetting(raw, "oidc_enabled", &cfg.Enabled); err != nil {
			return "", err
		}
	}
	if raw, ok := body[admin.KeyOIDCIssuerURL]; ok {
		if err := unmarshalSetting(raw, "oidc_issuer_url", &cfg.IssuerURL); err != nil {
			return "", err
		}
	}
	if raw, ok := body[admin.KeyOIDCClientID]; ok {
		if err := unmarshalSetting(raw, "oidc_client_id", &cfg.ClientID); err != nil {
			return "", err
		}
	}
	if raw, ok := body[admin.KeyOIDCClientSecret]; ok {
		if err := unmarshalSetting(raw, "oidc_client_secret", &cfg.ClientSecret); err != nil {
			return "", err
		}
	}

	// Same refusal as handleSaveOIDCConfig, and for the same reason: this
	// route can set oidc_enabled independently of the other three OIDC keys
	// (a request touching only oidc_enabled still reaches here), so it needs
	// the identical guard against persisting "enabled but incomplete" — see
	// errIncompleteOIDCConfig's own comment.
	if cfg.Enabled && !oidcConfigComplete(cfg) {
		return "", errIncompleteOIDCConfig
	}

	return s.refuseIfOrphaning(ctx, samlFieldsLookConfigured(metadataURL, certPEM, keyPEM), oidcFieldsLookConfigured(cfg))
}

// unmarshalSetting decodes a PATCH /admin/settings field into dst, refusing
// both a JSON type mismatch and the literal null — see ssoSettingsWarning's
// own comment for why null needs its own check: encoding/json's Unmarshal
// treats null into a non-pointer destination as a silent no-op rather than
// an error, so a type check alone does not catch it.
func unmarshalSetting[T any](raw json.RawMessage, name string, dst *T) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("%w: %s must not be null", user.ErrValidation, name)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("%w: %s has the wrong type", user.ErrValidation, name)
	}
	return nil
}

// securityWarnings is what an administrator needs to know about how this
// instance is configured, as opposed to what it contains.
type securityWarnings struct {
	// InsecureSecrets names environment variables still set to a value this
	// project ships as an example. Names only — never the values.
	InsecureSecrets []string `json:"insecure_secrets"`

	// AttachmentScanning is what the scanner is actually doing, as opposed to
	// what the configuration says it should.
	//
	// Here because an operator should not have to read logs, or wait for an
	// upload to fail, to learn that scanning is degraded. The old behaviour
	// wrote one slog.Warn per skipped file and nothing else, so an instance
	// whose scanner had been dead for a month looked exactly like a healthy
	// one from the admin UI.
	AttachmentScanning scanStatus `json:"attachment_scanning"`
}

type scanStatus struct {
	// Policy is off, required or permissive.
	Policy string `json:"policy"`

	// Configured reports whether an address is set at all.
	Configured bool `json:"configured"`

	// Reachable is the answer to a live ping, not a cached belief.
	Reachable bool `json:"reachable"`

	// Effect says in plain words what is happening to uploads right now,
	// because the combination of policy and reachability is not obvious and
	// this is the sentence an administrator actually needs.
	Effect string `json:"effect"`
}

// handleGetSecurityWarnings reports configuration problems that cannot be
// fixed from the UI but that an administrator must know about.
//
// Admin-only, and deliberately not part of the public /api/v1/site payload.
// Telling an anonymous visitor that this instance signs sessions with a
// publicly known key is not a warning, it is an invitation.
func (s *Server) handleGetSecurityWarnings(w http.ResponseWriter, r *http.Request) {
	JSON(w, http.StatusOK, securityWarnings{
		InsecureSecrets:    s.cfg.InsecureSecrets(),
		AttachmentScanning: s.scanStatus(r.Context()),
	})
}

// scanStatus pings the scanner rather than reporting what the settings say.
//
// The distinction is the point: "an address is configured" and "a scanner
// answers" are different facts, and only the second one protects anybody.
func (s *Server) scanStatus(ctx context.Context) scanStatus {
	sc := s.scanner(ctx)
	configured := sc.Configured()
	policy := s.adminSvc.AttachmentScanPolicy(ctx, configured)
	reachable := configured && sc.Ping(ctx) == nil

	st := scanStatus{
		Policy:     string(policy),
		Configured: configured,
		Reachable:  reachable,
	}
	switch {
	case policy == antivirus.PolicyOff:
		st.Effect = "Attachments are not scanned. Uploads are accepted without being checked."
	case reachable:
		st.Effect = "Attachments are scanned before being accepted."
	case policy == antivirus.PolicyRequired:
		st.Effect = "The scanner is unreachable, so attachment uploads are being refused until it returns."
	default:
		st.Effect = "The scanner is unreachable and the policy is permissive, so attachments are being accepted WITHOUT being scanned."
	}
	return st
}

// validScannerAddr accepts the two forms the scanner can dial. Deliberately not
// a full URL parse: "tcp://host:port" is not a URL anyone else would parse the
// same way, and the only question is whether net.Dial will understand it.
func validScannerAddr(addr string) bool {
	switch {
	case strings.HasPrefix(addr, "unix://"):
		return len(strings.TrimPrefix(addr, "unix://")) > 0
	case strings.HasPrefix(addr, "tcp://"):
		hostport := strings.TrimPrefix(addr, "tcp://")
		host, port, err := net.SplitHostPort(hostport)
		return err == nil && host != "" && port != ""
	default:
		return false
	}
}

// hasSecretValue reports whether a stored secret holds anything.
//
// A JSON string is stored, so "" and a value of only whitespace both mean
// unset — an operator who pasted a stray space has not configured a key, and
// telling them they have would send them looking for a fault somewhere else.
func hasSecretValue(raw []byte) bool {
	var v string
	if err := json.Unmarshal(raw, &v); err != nil {
		// Not a string: something wrote this key by another route. Present,
		// whatever it is.
		return len(raw) > 0
	}
	return strings.TrimSpace(v) != ""
}

// validateReputationConfig refuses two settings writes: one that would leave a
// commercial reputation provider enabled with no API key, and one whose toggle
// is not a boolean at all.
//
// It reads the STORED value for anything the write does not mention, because a
// PATCH is a patch: enabling VirusTotal in one request when its key was pasted
// in a previous one has to be allowed, and pasting the key and the toggle in
// the same request has to be allowed too. Validating the body alone would
// refuse the first, and validating the stored settings alone would refuse the
// second.
//
// EVERY provider's toggle is type-checked, including CIRCL's, and that is the
// part it is easy to skip. CIRCL has no key clause — hashlookup authenticates
// nobody, and a rule demanding one would leave the one keyless provider
// permanently unusable — but skipping the whole provider to reach that
// conclusion skipped the type check with it. The reader is GetBool, which
// answers false for anything that is not a JSON boolean, so a CIRCL toggle of
// "yes" was stored, answered 204, and read back as off: the operator is told
// their provider is on and the provider is never asked. A setting accepted and
// then ignored is the failure this handler refuses everywhere else.
//
// The error message names the PROVIDER and never the key. There are three keys
// now, and an error string is the easiest place for a write-only secret to
// escape into a log.
func validateReputationConfig(ctx context.Context, adminSvc *admin.Service, body map[string]json.RawMessage) error {
	for _, p := range reputation.ProviderNames() {
		enabledKey, apiKeyKey, ok := admin.ReputationSettingKeys(p)
		if !ok {
			continue
		}

		enabled := adminSvc.ReputationEnabled(ctx, p)
		if raw, present := body[enabledKey]; present {
			if err := json.Unmarshal(raw, &enabled); err != nil {
				return fmt.Errorf("the %s toggle must be true or false", reputation.DisplayName(p))
			}
		}
		// Past the type check, the rest is the key rule, and it applies only
		// to a provider that is on and has a key to be missing.
		if !enabled || apiKeyKey == "" || !reputation.NeedsKey(p) {
			continue
		}

		key := adminSvc.ReputationKey(ctx, p)
		if raw, present := body[apiKeyKey]; present {
			var v string
			if err := json.Unmarshal(raw, &v); err != nil {
				return fmt.Errorf("the %s API key must be a string", reputation.DisplayName(p))
			}
			// Trimmed for the same reason the reader trims: a setting holding
			// nothing but spaces is not a key, and accepting it would put the
			// instance back in the state this rule exists to remove.
			key = strings.TrimSpace(v)
		}
		if key == "" {
			return fmt.Errorf("%s cannot be enabled without an API key", reputation.DisplayName(p))
		}
	}
	return nil
}
