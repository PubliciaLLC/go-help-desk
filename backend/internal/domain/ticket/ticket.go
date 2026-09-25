package ticket

import (
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Priority is one of the four configurable severity levels.
type Priority string

const (
	PriorityCritical Priority = "critical"
	PriorityHigh     Priority = "high"
	PriorityMedium   Priority = "medium"
	PriorityLow      Priority = "low"
)

// Valid reports whether p is one of the four levels. The database enforces the
// same set with a CHECK constraint; this lets a caller reject a bad value with
// a useful message instead of a constraint violation.
func (p Priority) Valid() bool {
	switch p {
	case PriorityCritical, PriorityHigh, PriorityMedium, PriorityLow:
		return true
	}
	return false
}

// StatusKind distinguishes the three system statuses from admin-defined ones.
// Resolved and Closed have hardcoded lifecycle semantics; New is the starting
// point. Everything else is Custom.
type StatusKind string

const (
	StatusKindSystem StatusKind = "system"
	StatusKindCustom StatusKind = "custom"
)

// Well-known names for system statuses. The IDs are assigned at migration time.
const (
	StatusNameNew      = "New"
	StatusNameResolved = "Resolved"
	StatusNameClosed   = "Closed"

	// StatusNamePending is a seeded *custom* status (migration 000001), not a
	// system one, so there is no cached ID for it the way there is for the
	// three above. It is identified by name, matching how
	// lifecycleAllowsReply already compares Resolved/Closed. Known
	// consequence, accepted: an admin who renames the status stops future
	// SLA pauses (see sla.Elapsed and applyStatusTimestamps).
	StatusNamePending = "Pending"
)

// Status represents a ticket state. System statuses have special lifecycle
// rules; custom statuses are fully configurable by admins.
type Status struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	Kind        StatusKind `json:"kind"`
	SortOrder   int        `json:"sort_order"`
	Color       string     `json:"color"`
	Active      bool       `json:"active"`
	TicketCount int64      `json:"ticket_count"`
}

// StatusHistoryEntry records a single status transition on a ticket.
type StatusHistoryEntry struct {
	ID              uuid.UUID  `json:"id"`
	TicketID        uuid.UUID  `json:"ticket_id"`
	FromStatusID    *uuid.UUID `json:"from_status_id"`
	FromStatusName  string     `json:"from_status_name"`
	FromStatusColor string     `json:"from_status_color"`
	ToStatusID      uuid.UUID  `json:"to_status_id"`
	ToStatusName    string     `json:"to_status_name"`
	ToStatusColor   string     `json:"to_status_color"`
	ChangedByUserID *uuid.UUID `json:"changed_by_user_id"`
	ChangedByName   string     `json:"changed_by_name"`
	CreatedAt       time.Time  `json:"created_at"`
}

// LinkType describes the relationship between two tickets.
type LinkType string

const (
	LinkRelatedTo   LinkType = "related_to"
	LinkParentChild LinkType = "parent_child" // source is the parent
	LinkCausedBy    LinkType = "caused_by"    // source was caused by target
	LinkDuplicateOf LinkType = "duplicate_of"
)

// TicketLink records a directional relationship between two tickets.
// Both tickets are identified by UUID; no embedding is done to keep the
// type flat.
type TicketLink struct {
	SourceTicketID uuid.UUID `json:"source_id"`
	TargetTicketID uuid.UUID `json:"target_id"`
	LinkType       LinkType  `json:"link_type"`
}

// TrackingNumber is the human-readable identifier for a ticket, e.g.
// "GHD-2024-000001". It is unique across all tickets and never reused.
type TrackingNumber string

// Ticket is the central entity of the system. All business state lives here.
// Optional foreign keys are represented as pointers to make "not set" explicit.
type Ticket struct {
	ID              uuid.UUID      `json:"id"`
	TrackingNumber  TrackingNumber `json:"tracking_number"`
	Subject         string         `json:"subject"`
	Description     string         `json:"description"`
	CategoryID      uuid.UUID      `json:"category_id"`
	TypeID          *uuid.UUID     `json:"type_id,omitempty"`
	ItemID          *uuid.UUID     `json:"item_id,omitempty"`
	Priority        Priority       `json:"priority"`
	StatusID        uuid.UUID      `json:"status_id"`
	AssigneeUserID  *uuid.UUID     `json:"assignee_user_id,omitempty"`
	AssigneeGroupID *uuid.UUID     `json:"assignee_group_id,omitempty"`
	ReporterUserID  *uuid.UUID     `json:"reporter_user_id,omitempty"`
	GuestEmail      *string        `json:"guest_email,omitempty"`
	GuestName       string         `json:"guest_name,omitempty"`
	GuestPhone      string         `json:"guest_phone,omitempty"`
	ResolutionNotes *string        `json:"resolution_notes,omitempty"`
	ResolvedAt      *time.Time     `json:"resolved_at,omitempty"`
	ClosedAt        *time.Time     `json:"closed_at,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`

	// PendingSince is when the ticket entered its current Pending interval; nil
	// when it is not Pending. SLAPausedSeconds is the sum of every Pending
	// interval that has already closed. Both are maintained by
	// applyStatusTimestamps and read by sla.Elapsed; nothing else writes them.
	PendingSince     *time.Time `json:"pending_since,omitempty"`
	SLAPausedSeconds int64      `json:"sla_paused_seconds"`
}

// Reply is a message on a ticket thread, from either a staff member or the
// original reporter. Internal replies are visible to staff only.
// NotifyCustomer controls whether a ticket-update email is sent to the reporter;
// it is always false for internal notes.
type Reply struct {
	ID       uuid.UUID `json:"id"`
	TicketID uuid.UUID `json:"ticket_id"`
	// AuthorID is NULL for a reply written by a guest, who has no account.
	// That is the only way it is NULL — every other path passes the acting
	// user — so a reply with no author on a ticket with a guest address came
	// from the customer. TestReply_OnlyGuestsWriteAnAuthorlessReply pins it.
	AuthorID       *uuid.UUID `json:"author_id,omitempty"`
	Body           string     `json:"body"`
	Internal       bool       `json:"internal"`
	NotifyCustomer bool       `json:"notify_customer"`
	CreatedAt      time.Time  `json:"created_at"`
}

// Attachment stores file metadata. Bytes live on disk at StoragePath.
// StoragePath is the obfuscated on-disk path; Filename is the original name.
type Attachment struct {
	ID          uuid.UUID `json:"id"`
	TicketID    uuid.UUID `json:"ticket_id"`
	Filename    string    `json:"filename"`
	MimeType    string    `json:"mime_type"`
	SizeBytes   int64     `json:"size_bytes"`
	StoragePath string    `json:"-"` // never sent to clients
	CreatedAt   time.Time `json:"created_at"`

	// What the upload actually was, recorded when it arrived. All three are
	// pointers because nil is a fact: the file predates the inspection, and
	// "not recorded" must never be rendered as "nothing wrong".

	// DetectedMime is what the content sniffs as, independent of the claimed
	// extension. Compared against the filename to flag a mismatch, which is
	// shown and never blocks.
	DetectedMime *string `json:"detected_mime"`

	// SHA256 is of the bytes as uploaded — before image recompression and
	// before quarantine wrapping — hex encoded. For a recompressed image it is
	// therefore not the hash of the file on disk.
	SHA256 *string `json:"sha256"`

	// VirusName is the scanner's name for the detection on a quarantined
	// upload, e.g. Eicar-Test-Signature. nil means the file was not identified
	// as malicious.
	VirusName *string `json:"virus_name"`

	// ContentMismatch is whether the content contradicted the name the file
	// was uploaded under, decided when it arrived.
	//
	// Recorded rather than recomputed on read. The comparison needs the name
	// the uploader claimed, and for any file we renamed — a suspicious wrap,
	// or an infected file stored as sample.pdf.zip — the stored name is ours,
	// so recomputing compares ".zip" against the content and reports a
	// truthfully named file as lying. nil means nothing was inspected.
	ContentMismatch *bool `json:"mismatch"`

	// ReputationURL is where a person can read a public report on this file's
	// hash. VirusTotal, on the attachments worth a second opinion.
	//
	// Two separate rules decide it, and conflating them is how one of them
	// gets deleted. It does NOT follow the per-provider toggles — see below.
	// It DOES follow whether this instance found anything about the file
	// worth investigating: the scanner named it, or the content contradicts
	// its name. An ordinary attachment whose content matches its name and
	// which the scanner passed carries the hash and no link, because a link
	// and a line of explanatory text under every holiday-request PDF is the
	// noise that teaches people to stop reading the rows that matter. See
	// worthLookingUp in the server package.
	//
	// NOT a function of which providers are enabled, and the distinction is
	// the whole reason this field is not gated. A LOOKUP is this server
	// sending a customer's file hash to a third party: the operator's
	// decision, their API allowance, and what the per-provider toggles govern.
	// A LINK sends nothing from this server. It is an anchor the analyst
	// clicks in their own browser, under their own account or none, exactly as
	// if they had copied the hash off the page and pasted it themselves —
	// which they can do anyway, because the hash is right there with a copy
	// control. Switching VirusTotal off means "do not send my customers'
	// hashes to VirusTotal from my server", not "my staff may never look at
	// VirusTotal", and collapsing the two takes a decision away from the
	// analyst that was never the operator's to make.
	//
	// VirusTotal specifically because its page is the one every analyst
	// already knows: no account needed and the full report renders logged out.
	// MetaDefender's public page announces itself as a reduced view, and CIRCL
	// has no per-hash web UI at all. An enabled provider's own link sits next
	// to its own verdict in Reputation.Providers instead, where a link and a
	// verdict from the same service belong together.
	//
	// Filled in at the HTTP boundary rather than read from storage, and nil
	// when there is no hash to look up.
	ReputationURL *string `json:"reputation_url"`

	// Reputation is what the enabled reputation services said, when this file
	// was a candidate for a lookup at all.
	//
	// nil means NOTHING WAS ATTEMPTED: no provider is enabled, or this file
	// was never a candidate — an ordinary attachment, or one with no hash. It
	// does NOT mean a lookup failed. A failed lookup is a provider entry with
	// state "unavailable", because an operator whose key has been rejected
	// needs to see that, and a reader needs to be able to tell "we could not
	// ask" from "we did not ask".
	//
	// With every toggle off the block is absent and that is the complete
	// answer, not a degraded one: the local scanner's verdict stands on its
	// own. No warning, no banner, and specifically no "not checked yet" —
	// that phrase belongs to a lookup that was attempted and did not finish.
	//
	// Filled in at the HTTP boundary like ReputationURL, and for the same
	// reason: the verdicts belong to whichever providers are enabled now.
	Reputation *AttachmentReputation `json:"reputation"`
}

// AttachmentReputation is what the enabled reputation services said about one
// file: the worst single answer, for the row, and every provider's own answer
// under it, for the expanded view.
//
// A second struct rather than internal/reputation's own because this package
// is domain and may not import infrastructure. The states are spelled there; a
// test pins that the two agree.
//
// The summary fields repeat one entry of Providers rather than aggregating
// across them, and that is deliberate: 62 of 81 engines is a fact about
// VirusTotal's analysis, and averaging it with a catalogue hit from CIRCL
// would produce a number no service ever said. Which entry is repeated is
// named by ProviderKey and marked by AttachmentProviderVerdict.Inline, so the
// summary is never a claim from nowhere.
type AttachmentReputation struct {
	// State is the worst verdict across every provider that answered:
	//
	//	detected  >  unseen  >  unscanned  >  clean  >  known
	//
	// "unseen" outranks "clean" because only quarantined files reach here —
	// every hash is one the local scanner already called malicious — so a file
	// no service has ever seen is a novel sample, and more concerning than one
	// seventy engines examined and passed.
	//
	// "unavailable" is not in that ordering at all: it is a failed lookup
	// rather than a verdict, and it must never displace a real answer, so
	// VirusTotal timing out while CIRCL says "known" reads "known" here. It
	// appears as this field's value only when NOTHING answered, which is the
	// honest summary of a row whose every provider is failing. It is always
	// present in Providers either way.
	State string `json:"state"`

	// Detected and Total are the engines that flagged the file and the engines
	// that ran. Pointers because nil is a fact: only a completed analysis has
	// numbers, and 0 of 0 reads as "nothing found anything" — the false
	// reassurance this feature exists to avoid.
	Detected *int `json:"detected"`
	Total    *int `json:"total"`

	// ThreatName is the provider's own consensus name for what it found, and
	// is often empty. Attacker-influenced text — a malware author picks the
	// filename the engines name it after — so it is escaped on the way to a
	// browser like any other untrusted string.
	ThreatName string `json:"threat_name"`

	// AnalysedAt is when the PROVIDER last analysed the file, not when we
	// asked. Staff read it to judge whether a clean verdict predates the
	// sample's first appearance in the wild.
	AnalysedAt *time.Time `json:"analysed_at"`

	// Provider is the service whose verdict is summarised above, as a person
	// reads it: "VirusTotal", "MetaDefender". ProviderKey is the same service
	// as a machine reads it: "virustotal", "metadefender".
	//
	// Here because the UI cannot work it out for itself and must not try. Which
	// providers are enabled is session-gated configuration staff cannot read,
	// and a frontend that guessed from a link's host would be holding a second
	// copy of provider knowledge to drift from this one. Attribution is the
	// point: "VirusTotal has never seen this file" is a claim with a source,
	// and "the reputation service has never seen this file" is a claim from
	// nowhere.
	//
	// Both are empty when nothing answered, because then the summary belongs
	// to no one.
	Provider    string `json:"provider"`
	ProviderKey string `json:"provider_key"`

	// KnownFeeds names the feeds that carry a file whose State is "known", and
	// is empty on every other state.
	//
	// "known" is the one verdict in this feature that renders as reassurance,
	// and it earns that by having a source: a named feed has this exact hash
	// on file. The state deliberately stops there, because how much that is
	// worth depends entirely on which feed — an Authenticode signature
	// assertion is a claim that the file is signed and trusted, while an NSRL
	// catalogue entry means only that it appeared in a software distribution,
	// and NSRL catalogues hacking tools.
	//
	// So a renderer MUST name the feed: "known file, signed by Microsoft
	// Windows" and "known file, catalogued by NSRL" are not the same sentence,
	// and "known good" is neither of them.
	KnownFeeds []string `json:"known_feeds"`

	// FetchedAt is when WE last asked, as against AnalysedAt, when the
	// provider last looked. It is what the "check again" control is enabled
	// on: a verdict may be re-checked by hand once every seven days.
	//
	// A pointer because nil is a fact here too — a verdict from a cache that
	// records no fetch time has an age nobody knows — though against the
	// database it is always set, because attachment_reputation.fetched_at is
	// NOT NULL.
	FetchedAt *time.Time `json:"fetched_at"`

	// Providers is every enabled provider's own answer, in a fixed order, one
	// entry each — including the ones that failed.
	//
	// The list is the reason for having more than one provider. They answer
	// different questions: VirusTotal counts engines, CIRCL says whether a
	// catalogue has the file on record, and a sample one calls "detected"
	// while another calls "known" is telling staff something either alone
	// would hide. Flattening four statements into a summary and sending only
	// that would throw away exactly what made the second, third and fourth
	// lookup worth making.
	//
	// Never empty when this struct is non-nil: a block with no providers under
	// it would be a verdict with no source.
	Providers []AttachmentProviderVerdict `json:"providers"`
}

// AttachmentProviderVerdict is one service's statement about one file at one
// time, for the expanded view of an attachment row.
//
// Each carries its own timestamps and its own eligibility for a re-check
// because each is genuinely separate: the cache is keyed by hash AND provider,
// so every verdict has its own expiry clock and its own Check again control.
// A shared "last checked" across four providers would be wrong for at least
// three of them.
type AttachmentProviderVerdict struct {
	// Provider is the name as a person reads it, ProviderKey as a machine
	// does. The second is what the re-check endpoint's ?provider= takes, and
	// what matches this entry against AttachmentReputation.ProviderKey.
	Provider    string `json:"provider"`
	ProviderKey string `json:"provider_key"`

	// State is this provider's own verdict, and unlike the summary above it
	// may be "unavailable": the lookup was attempted and did not finish — a
	// spent allowance, a provider that is down, a key that was rejected. An
	// operator has to be able to see their key failing, and hiding it behind a
	// sibling's good answer is how a dead integration goes unnoticed for a
	// month.
	State string `json:"state"`

	// Detected and Total are the engines that flagged the file and the engines
	// that ran, for the providers that run engines. Pointers because nil is a
	// fact: 0 of 0 reads as "nothing found anything", which is the false
	// reassurance this feature exists to avoid. Always nil for a catalogue
	// answer, where no engine ran at all.
	Detected *int `json:"detected"`
	Total    *int `json:"total"`

	// ThreatName is this provider's own consensus name for what it found, and
	// is often empty. Attacker-influenced text, escaped on the way to a
	// browser like any other untrusted string.
	ThreatName string `json:"threat_name"`

	// KnownFeeds names the feeds that carry a file whose State is "known", and
	// is empty on every other state. The feed is the evidence: an Authenticode
	// signature assertion and an NSRL catalogue entry are not the same claim,
	// and NSRL catalogues hacking tools, so a renderer must name the feed
	// rather than say "known good".
	KnownFeeds []string `json:"known_feeds"`

	// AnalysedAt is when THIS provider last analysed the file; FetchedAt is
	// when we last asked them. Both nil when there is nothing to say.
	AnalysedAt *time.Time `json:"analysed_at"`
	FetchedAt  *time.Time `json:"fetched_at"`

	// LinkURL is this provider's own page for the hash, beside its own
	// verdict, and nil for a provider that has no per-hash page — CIRCL,
	// whose root serves a Swagger document. A link to a page that cannot
	// answer the question the reader clicked it with is worse than no link.
	//
	// Distinct from Attachment.ReputationURL, which is the VirusTotal link
	// carried by any attachment this instance found worth a second opinion,
	// whatever is enabled.
	LinkURL *string `json:"link_url"`

	// Recheckable reports whether the Check again control should be armed for
	// this provider: the verdict can still change, and the seven-day floor has
	// passed. It deliberately does not consult the budget, which can be spent
	// between the render and the click — a control that promised otherwise
	// would be lying either way round.
	Recheckable bool `json:"recheckable"`

	// Inline marks the entry the summary above was taken from, so the row's
	// single line can be traced to the service that said it. Exactly one entry
	// carries it when anything answered, and none when nothing did.
	Inline bool `json:"inline"`
}

// DefaultTrackingPrefix is used when an instance has not set one.
//
// It is GHD because the project is Go Help Desk; it was OHD until the rename.
// An instance that was running before the default changed keeps its existing
// OHD- tickets and starts minting GHD- ones, which is harmless — lookup is an
// exact string match, so every ticket stays findable — but an operator who
// wants one consistent series should set the prefix back to OHD.
const DefaultTrackingPrefix = "GHD"

// maxTrackingPrefixLen bounds the prefix. Tracking numbers are quoted in
// emails, webhooks and customer correspondence, and are searched by prefix.
const maxTrackingPrefixLen = 8

// trackingPrefixPattern is what a prefix may contain. Uppercase letters and
// digits only: a prefix with a hyphen would make "GHD-X-2026-000001" ambiguous
// to split, and lowercase would make tracking numbers inconsistent with every
// one already issued.
var trackingPrefixPattern = regexp.MustCompile(`^[A-Z0-9]{1,8}$`)

// ErrInvalidTrackingPrefix reports a prefix that cannot be used.
var ErrInvalidTrackingPrefix = errors.New("tracking prefix must be 1-8 uppercase letters or digits")

// ValidateTrackingPrefix reports whether a prefix is usable.
//
// This is enforced where the setting is saved (handleUpdateSettings rejects an
// invalid prefix with 400) as well as at mint time, where an invalid value
// falls back to the default rather than producing a malformed number. A bad prefix accepted into settings would not fail loudly — it would
// quietly mint malformed tracking numbers that are then in customers' inboxes
// and impossible to recall.
func ValidateTrackingPrefix(prefix string) error {
	if !trackingPrefixPattern.MatchString(prefix) {
		return fmt.Errorf("%q: %w", prefix, ErrInvalidTrackingPrefix)
	}
	return nil
}

// GenerateTrackingNumber formats the canonical tracking number for a ticket.
// seq must be the globally-unique monotonic sequence value from the database.
//
// An empty or invalid prefix falls back to the default rather than producing a
// malformed number: this runs at ticket creation, where refusing would mean a
// customer cannot open a ticket because of an admin's typo.
func GenerateTrackingNumber(prefix string, year int, seq int64) TrackingNumber {
	if ValidateTrackingPrefix(prefix) != nil {
		prefix = DefaultTrackingPrefix
	}
	return TrackingNumber(fmt.Sprintf("%s-%d-%06d", prefix, year, seq))
}

// Errors returned by rule functions.
var (
	ErrForbidden = errors.New("forbidden")
	ErrClosed    = errors.New("ticket is closed")
	// ErrReopenWindowClosed is separate from ErrForbidden on purpose: the
	// caller owns the ticket and has the right to reopen it in general. What
	// expired is the window, and telling them "you do not have permission"
	// sends them to an administrator for something no administrator can grant.
	ErrReopenWindowClosed = errors.New("the reopen window for this ticket has closed")
)

// CanUserUpdate returns nil if the actor may modify this ticket.
// Rules:
//   - Admins and staff: always allowed (staff permission scoping is enforced
//     at the service layer, not here).
//   - Users: allowed only on their own tickets, and only while the ticket is
//     not Closed. If the ticket is Resolved, the reopen window must not have
//     expired.
func CanUserUpdate(t Ticket, u user.User, status Status, reopenWindowDays int) error {
	if u.Role == user.RoleAdmin || u.Role == user.RoleStaff {
		return nil
	}
	// User role from here down.

	// Ownership. This comment promised the rule for a long time while the code
	// did not implement it, and the HTTP layer did not compensate: a reporting
	// user could post into any ticket by id. The handlers now gate on
	// visibility too, and this is the second line — a domain rule its callers
	// already believed they were getting.
	//
	// A guest ticket has no reporter user, so no signed-in reporting user owns
	// it.
	if t.ReporterUserID == nil || *t.ReporterUserID != u.ID {
		return ErrForbidden
	}

	return lifecycleAllowsReply(t, status, reopenWindowDays)
}

// CanGuestUpdate is CanUserUpdate without the ownership comparison.
//
// A guest holds a token that names one ticket, so ownership was decided before
// this is reached — there is nothing here to compare, since a guest ticket has
// no reporter user. Every lifecycle rule still applies: a closed ticket is
// closed to the customer who opened it too, and the reopen window does not
// widen because the reply arrived by link rather than by login.
//
// Separate from CanUserUpdate rather than a flag on Actor because skipping an
// ownership check is not something a caller should be able to ask for by
// setting a struct field. One function, one call site.
func CanGuestUpdate(t Ticket, status Status, reopenWindowDays int) error {
	return lifecycleAllowsReply(t, status, reopenWindowDays)
}

// lifecycleAllowsReply holds the rules that do not depend on who is asking:
// whether the ticket's own state accepts another reply at all.
func lifecycleAllowsReply(t Ticket, status Status, reopenWindowDays int) error {
	if status.Name == StatusNameClosed {
		return ErrClosed
	}
	if status.Name == StatusNameResolved {
		if t.ResolvedAt == nil {
			// Resolved but no timestamp — treat as permanently resolved. Same
			// outcome for the caller as an expired window, so same error.
			return ErrReopenWindowClosed
		}
		deadline := t.ResolvedAt.AddDate(0, 0, reopenWindowDays)
		if time.Now().After(deadline) {
			return ErrReopenWindowClosed
		}
	}
	return nil
}

// CanAssign returns nil if the actor with the given role may set or clear a
// ticket's assignee.
//
// DESIGN.md gives assignment to Staff ("Assign tickets to any staff member or
// group"); the User row covers creating, viewing and updating their own
// tickets and says nothing about assignment. Being able to see a ticket is a
// separate question from being able to direct work on it — the subtree
// middleware answers the first, this answers the second.
//
// Deliberately a predicate rather than a check inside Service.Assign: routing
// rules auto-assign on create via SystemActor, and authorisation belongs to
// the caller, as it does for CanTransitionStatus.
func CanAssign(role user.Role) error {
	if role == user.RoleUser {
		return ErrForbidden
	}
	return nil
}

// CanTransitionStatus returns nil if the actor with the given role may move
// a ticket from one status to another.
// Rules:
//   - Closed can only be set by admins (the auto-close scheduler uses a
//     dedicated service method that bypasses this check).
//   - Users cannot set the status directly at all; their replies trigger
//     automatic reopens via the service layer.
func CanTransitionStatus(to Status, role user.Role) error {
	if role == user.RoleUser {
		return ErrForbidden
	}
	if to.Name == StatusNameClosed && role != user.RoleAdmin {
		return ErrForbidden
	}
	return nil
}

// VisibleReplies drops internal notes for a caller who is not staff.
//
// Internal notes are staff-to-staff. Access to a ticket is not access to them:
// the reporter may read their own thread and must still not see them.
//
// It lives here because both the REST layer and the MCP server answer with
// replies, and each had its own copy of the rule. One rule with two callers,
// not two rules.
func VisibleReplies(replies []Reply, role user.Role) []Reply {
	if role == user.RoleAdmin || role == user.RoleStaff {
		return replies
	}
	out := make([]Reply, 0, len(replies))
	for _, r := range replies {
		if !r.Internal {
			out = append(out, r)
		}
	}
	return out
}
