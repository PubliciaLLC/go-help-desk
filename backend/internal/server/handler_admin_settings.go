package server

import (
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

	for k, v := range body {
		if err := s.adminSvc.SetRaw(r.Context(), k, []byte(v)); err != nil {
			handleError(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
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

// validateReputationConfig refuses a settings write that would leave a
// commercial reputation provider enabled with no API key.
//
// It reads the STORED value for anything the write does not mention, because a
// PATCH is a patch: enabling VirusTotal in one request when its key was pasted
// in a previous one has to be allowed, and pasting the key and the toggle in
// the same request has to be allowed too. Validating the body alone would
// refuse the first, and validating the stored settings alone would refuse the
// second.
//
// It reads the STORED value for anything the write does not mention, because a
// PATCH is a patch: enabling VirusTotal in one request when its key was pasted
// in a previous one has to be allowed, and pasting the key and the toggle in
// the same request has to be allowed too. Validating the body alone would
// refuse the first, and validating the settings alone would refuse the second.
//
// CIRCL has no clause here because it has no key: hashlookup authenticates
// nobody, and a rule demanding one would leave the one keyless provider
// permanently unusable.
//
// The error message names the PROVIDER and never the key. There are three keys
// now, and an error string is the easiest place for a write-only secret to
// escape into a log.
func validateReputationConfig(ctx context.Context, adminSvc *admin.Service, body map[string]json.RawMessage) error {
	for _, p := range reputation.ProviderNames() {
		enabledKey, apiKeyKey, ok := admin.ReputationSettingKeys(p)
		if !ok || apiKeyKey == "" || !reputation.NeedsKey(p) {
			continue
		}

		enabled := adminSvc.ReputationEnabled(ctx, p)
		if raw, present := body[enabledKey]; present {
			if err := json.Unmarshal(raw, &enabled); err != nil {
				return fmt.Errorf("the %s toggle must be true or false", reputation.DisplayName(p))
			}
		}
		if !enabled {
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
