package ticket

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Actor is the identity performing an operation. Both authenticated users and
// the system scheduler are actors; system actors have a nil UserID.
type Actor struct {
	UserID *uuid.UUID
	Role   user.Role
}

// SystemActor is used by the auto-close scheduler and other system processes.
var SystemActor = Actor{UserID: nil, Role: user.RoleAdmin}

// systemStatuses caches the IDs of the three system statuses after they are
// loaded from the database at startup. This avoids hitting the DB on every
// status check.
type systemStatuses struct {
	newID      uuid.UUID
	resolvedID uuid.UUID
	closedID   uuid.UUID

	// full Status values, needed for CanTransitionStatus
	resolved Status
	closed   Status
}

// Service orchestrates all ticket lifecycle operations.
type Service struct {
	store      Store
	statuses   StatusStore
	dispatcher notification.Dispatcher
	auditStore audit.Store
	atomic     Atomic     // groups a composite operation's writes into one transaction
	sla        SLAService // may be nil when SLA is disabled

	// cached at startup
	sys *systemStatuses

	// closedReopenPolicy reads the closed_reopen_policy setting. A function,
	// not a value, and never cached: it is called when a force-reopen is
	// decided, after the ticket's row lock is taken, so a setting flipped
	// while the request was in flight is the one that applies. Nil reads as
	// off. The domain does not import the settings package; the server wires
	// this (see SetClosedReopenPolicy).
	closedReopenPolicy func(context.Context) string

	// slaEnabled reads the SLA feature toggle. A function, read inside the
	// priority change's transaction, like closedReopenPolicy. Nil reads as off.
	slaEnabled func(context.Context) bool

	// userEmail finds an account holder's address. A function because the
	// domain does not import the user package's store; the server wires it.
	// Nil means no account address can be found.
	userEmail func(context.Context, uuid.UUID) (string, error)
}

// SetUserEmailLookup wires the lookup ReplyRecipient uses for tickets filed by
// an account. Called once at start-up, before the service takes requests.
func (s *Service) SetUserEmailLookup(find func(context.Context, uuid.UUID) (string, error)) {
	s.userEmail = find
}

// ReplyRecipient is the address a public reply on t is mailed to: the guest's
// address, else the reporting account's. Empty means the requester cannot be
// emailed (no address on record, or the account is gone or unreadable), and the
// caller then sends the reply without mail rather than rejecting it.
//
// One rule for every door that accepts a reply. The REST handler had it inline
// and MCP passed "", so a reply an AI agent wrote never reached the customer
// (#442). A staff member or administrator is not mailed a copy of their own
// reply, even on a ticket they filed (Erik's decision; REST used to mail them).
// An ordinary requester replying on their own ticket still is: the copy is
// their receipt, and nothing about that changed.
func (s *Service) ReplyRecipient(ctx context.Context, t Ticket, author Actor) string {
	if t.GuestEmail != nil {
		return *t.GuestEmail
	}
	authorIsRequester := author.UserID != nil && t.ReporterUserID != nil && *author.UserID == *t.ReporterUserID
	if authorIsRequester && author.Role != user.RoleUser {
		return ""
	}
	if t.ReporterUserID != nil && s.userEmail != nil {
		if email, err := s.userEmail(ctx, *t.ReporterUserID); err == nil {
			return email
		}
	}
	return ""
}

// SetClosedReopenPolicy wires the source of the closed_reopen_policy setting
// (#349). Called once at start-up, before the service takes requests; without
// it Closed is terminal for every role, which is the default setting.
func (s *Service) SetClosedReopenPolicy(read func(context.Context) string) {
	s.closedReopenPolicy = read
}

// SetSLAEnabled wires the source of the SLA feature toggle for the priority
// re-match (#443b). Called once at start-up, before the service takes requests;
// without it a priority change never touches the SLA. Like AttachPolicy, a
// re-match creates records, so it is gated on the toggle; writing facts onto an
// existing record (RecordFirstResponse, RecordResolved) is not.
func (s *Service) SetSLAEnabled(read func(context.Context) bool) {
	s.slaEnabled = read
}

// forceReopenGate is asked by every route that would move a ticket OUT of
// Closed, on the row it has locked. It returns the policy in force and nil when
// the actor may, or the refusal. Read here, never earlier: see
// closedReopenPolicy.
func (s *Service) forceReopenGate(ctx context.Context, actor Actor) (string, error) {
	policy := ReopenPolicyOff
	if s.closedReopenPolicy != nil {
		policy = s.closedReopenPolicy(ctx)
	}
	if !CanForceReopen(policy, actor.Role) {
		return policy, reopenRefusal(policy)
	}
	return policy, nil
}

// forcedAudit adds to an audit entry's "after" what makes a force-reopen
// distinguishable from an ordinary change: that it left Closed, and the policy
// that allowed it.
func forcedAudit(after map[string]any, policy string) map[string]any {
	after["forced_reopen"] = true
	after["closed_reopen_policy"] = policy
	return after
}

// SLAService is the narrow interface the ticket service needs from the SLA
// layer. RecordFirstResponse and RecordResolved take the full ticket, not
// just its id: the SLA layer freezes elapsed-toward-target as of this exact
// moment (see sla.Service.RecordFirstResponse), which needs CreatedAt,
// SLAPausedSeconds and PendingSince as they stood right now — not whatever
// they read back on a second trip to the store, by which time a pause
// interval could have opened or closed.
type SLAService interface {
	AttachPolicy(ctx context.Context, t Ticket) error
	RecordFirstResponse(ctx context.Context, t Ticket, at time.Time) error
	RecordResolved(ctx context.Context, t Ticket, at time.Time) error
}

// NewService constructs a Service. Call LoadSystemStatuses before use.
func NewService(
	store Store,
	statuses StatusStore,
	dispatcher notification.Dispatcher,
	auditStore audit.Store,
	atomic Atomic, // required: composite writes must be one transaction
	sla SLAService, // nil when SLA feature is disabled
) *Service {
	return &Service{
		store:      store,
		statuses:   statuses,
		dispatcher: dispatcher,
		auditStore: auditStore,
		atomic:     atomic,
		sla:        sla,
	}
}

// LoadSystemStatuses loads the three system status IDs from the database and
// caches them. Must be called once after startup before any other method.
func (s *Service) LoadSystemStatuses(ctx context.Context) error {
	newSt, err := s.statuses.GetStatusByName(ctx, StatusNameNew)
	if err != nil {
		return fmt.Errorf("loading New status: %w", err)
	}
	resolvedSt, err := s.statuses.GetStatusByName(ctx, StatusNameResolved)
	if err != nil {
		return fmt.Errorf("loading Resolved status: %w", err)
	}
	closedSt, err := s.statuses.GetStatusByName(ctx, StatusNameClosed)
	if err != nil {
		return fmt.Errorf("loading Closed status: %w", err)
	}
	s.sys = &systemStatuses{
		newID:      newSt.ID,
		resolvedID: resolvedSt.ID,
		closedID:   closedSt.ID,
		resolved:   resolvedSt,
		closed:     closedSt,
	}
	return nil
}

// MaxSubjectLength and MaxDescriptionLength bound the two fields that feed
// tickets.search_vector (see migration 000014). Postgres's tsvector has a
// hard ~1 MB limit ("string is too long for tsvector"); both caps keep any
// realistic combination of subject+description far under that regardless of
// multi-byte UTF-8 expansion, while remaining far larger than any legitimate
// ticket needs (a one-line summary, and a detailed multi-paragraph report).
const (
	MaxSubjectLength     = 500
	MaxDescriptionLength = 50_000
	// MaxReplyLength is the same limit for a reply or note (#443). Counted the
	// same way, in bytes of the stored text. A reply had no cap, and a megabyte
	// of text is a burden on every page that shows it, whatever the page does.
	MaxReplyLength = MaxDescriptionLength
)

// ErrReplyTooLong is returned for a reply longer than MaxReplyLength. It is also
// a validation error (400), with its own code so a client can tell why.
var ErrReplyTooLong = errors.New("reply too long")

// CreateInput is the data needed to open a new ticket.
type CreateInput struct {
	Subject     string
	Description string
	// DescriptionFormat is required; an empty value is ErrInvalidTextFormat.
	DescriptionFormat TextFormat
	CategoryID        uuid.UUID
	TypeID            *uuid.UUID
	ItemID            *uuid.UUID
	Priority          Priority

	// Exactly one of ReporterUserID or GuestEmail must be set.
	ReporterUserID *uuid.UUID
	GuestEmail     *string
	GuestName      string // required when GuestEmail is set
	GuestPhone     string // optional

	// FiledBy is the member of staff filing the ticket on someone else's
	// behalf (#444). The status history, the "created" audit entry and the
	// "created" event name them as the actor; the requester above stays the
	// ticket's reporter or guest. Nil means the requester filed it themselves,
	// and is the actor, as before. Whether the caller may file for someone else
	// is the caller's decision: the service cannot see a role here.
	FiledBy *uuid.UUID

	// TrackingPrefix is the instance's configured tracking-number prefix.
	// Empty falls back to DefaultTrackingPrefix, so a caller that does not
	// care — a test, a script — still produces well-formed numbers.
	//
	// Passed in rather than read here because internal/domain must not reach
	// into settings; this follows the same shape as reopenWindowDays on
	// AddReply.
	TrackingPrefix string

	// AutoAssign is who the ticket may be routed to, best candidate first
	// (#506). The caller works the list out before the transaction (the CTI
	// group, the global group, the round-robin users, as before); the first
	// one the assignment would accept is assigned inside the create's
	// transaction, so the ticket is announced once, as created and assigned.
	// Empty leaves it unassigned. Guest and MCP creates pass none.
	AutoAssign []Assignment
}

// Assignment is who a ticket is given to. Both nil is nobody.
type Assignment struct {
	UserID  *uuid.UUID
	GroupID *uuid.UUID
}

// Create opens a new ticket, fires the created event, and optionally attaches
// an SLA policy.
func (s *Service) Create(ctx context.Context, in CreateInput) (Ticket, error) {
	actor := Actor{UserID: in.ReporterUserID}
	if in.FiledBy != nil {
		actor.UserID = in.FiledBy
	}
	return s.create(ctx, in, actor, nil)
}

// CreateFollowUp opens a NEW ticket from a Closed one, linked back to it
// (#349). It is the way forward from a closed ticket that works in every
// mode: Closed is terminal by default, so unless an operator has allowed it
// nobody can reopen it, and staff and admin start a fresh one instead; and where an operator has allowed forced
// reopen (closed_reopen_policy) the follow-up is still available beside it.
//
// What is copied, and what is not — recorded in DESIGN.md → Closing:
//   - copied: subject, description, category / type / item, priority, and the
//     requester (the reporting user, or the guest's address, name and phone);
//   - not copied: replies and internal notes, attachments, status history,
//     custom field values, tags, the assignee, and any SLA state. The new
//     ticket starts New and unassigned and runs its own SLA clock; the closed
//     one is left exactly as it was, link tokens included.
//
// The new ticket is made by the same path as any other (create): the same
// validation, tracking number, status history, audit entry, SLA policy and
// "created" notification. It is linked to the original as its CHILD
// (parent_child, source = the closed ticket), in the same transaction, so there
// is no follow-up without its link.
//
// Staff and admin only (ErrForbidden otherwise), and only from a Closed ticket
// (ErrNotClosed otherwise): an open ticket needs no way forward, and allowing
// it would make this a general "clone" that two tickets can then drift apart
// from. trackingPrefix is the instance's configured prefix, as in CreateInput.
//
// route is who the new ticket may be routed to, as CreateInput.AutoAssign; none
// leaves it unassigned (the MCP tool passes none).
func (s *Service) CreateFollowUp(ctx context.Context, originalID uuid.UUID, trackingPrefix string, actor Actor, route ...Assignment) (Ticket, error) {
	if actor.Role == user.RoleUser {
		return Ticket{}, ErrForbidden
	}
	orig, err := s.store.GetByID(ctx, originalID)
	if err != nil {
		return Ticket{}, err
	}
	// An unlocked read. It can go stale only if forced reopen is enabled and
	// the ticket is reopened between here and the insert, in which case the
	// follow-up continues a ticket that is open again: harmless, and the link
	// back is still true.
	if orig.StatusID != s.sys.closedID {
		return Ticket{}, fmt.Errorf("%w: a follow-up is created from a closed ticket", ErrNotClosed)
	}
	in := CreateInput{
		Subject:     orig.Subject,
		Description: orig.Description,
		// The format travels with the text: a follow-up of an old plain
		// ticket must not start rendering as Markdown.
		DescriptionFormat: orig.DescriptionFormat,
		CategoryID:        orig.CategoryID,
		TypeID:            orig.TypeID,
		ItemID:            orig.ItemID,
		Priority:          orig.Priority,
		ReporterUserID:    orig.ReporterUserID,
		GuestEmail:        orig.GuestEmail,
		GuestName:         orig.GuestName,
		GuestPhone:        orig.GuestPhone,
		TrackingPrefix:    trackingPrefix,
		AutoAssign:        route,
	}
	return s.create(ctx, in, actor, &orig)
}

// CreateRequesterFollowUp opens a follow-up of a Closed ticket on behalf of its
// REQUESTER (#349): the account holder who reported it (actor.UserID set), or
// the guest whose link reached it (actor.UserID nil, Role RoleUser). Staff and
// admin use CreateFollowUp.
//
// The principle: it must not produce a ticket the requester could not have
// created directly. So it copies only what a requester may set on creation —
// subject, description, category, the type for an account holder (never for a
// guest), and who they are — and takes every staff-controlled field from the
// defaults a normal create gives: medium priority, no item, unassigned, a
// fresh SLA clock. open applies the role's own creation rules that this
// package cannot know (the category, and the type, must still be active, as for
// a normal create); its refusal is an ErrValidation, before a tracking number
// is taken.
//
// Limits: only the requester of that ticket (ErrForbidden otherwise), only from
// a Closed ticket (ErrNotClosed), and ONE per closed ticket (ErrFollowUpExists;
// a follow-up staff opened counts), re-checked on the locked row in the
// transaction that creates the ticket and its link. Per-address rate limits are
// the caller's, as for a normal guest ticket.
func (s *Service) CreateRequesterFollowUp(
	ctx context.Context,
	originalID uuid.UUID,
	trackingPrefix string,
	actor Actor,
	open func(ctx context.Context, categoryID uuid.UUID, typeID *uuid.UUID) error,
	route ...Assignment,
) (Ticket, error) {
	if actor.Role != user.RoleUser {
		return Ticket{}, ErrForbidden
	}
	orig, err := s.store.GetByID(ctx, originalID)
	if err != nil {
		return Ticket{}, err
	}
	// Whose ticket it is, decided here and not by the caller: an account
	// holder's own, or the one a guest's link names (a ticket with a guest
	// address and no account behind it).
	if actor.UserID != nil {
		if orig.ReporterUserID == nil || *orig.ReporterUserID != *actor.UserID {
			return Ticket{}, ErrForbidden
		}
	} else if orig.ReporterUserID != nil || orig.GuestEmail == nil || *orig.GuestEmail == "" {
		return Ticket{}, ErrForbidden
	}
	if orig.StatusID != s.sys.closedID {
		return Ticket{}, fmt.Errorf("%w: a follow-up is created from a closed ticket", ErrNotClosed)
	}
	// Before the tracking number is taken. The locked re-check in create is
	// the one that holds against a concurrent request.
	if err := s.refuseSecondFollowUp(ctx, s.store, orig.ID); err != nil {
		return Ticket{}, err
	}
	in := CreateInput{
		Subject:     orig.Subject,
		Description: orig.Description,
		// The format travels with the text: a follow-up of an old plain
		// ticket must not start rendering as Markdown.
		DescriptionFormat: orig.DescriptionFormat,
		CategoryID:        orig.CategoryID,
		Priority:          PriorityMedium,
		ReporterUserID:    orig.ReporterUserID,
		GuestEmail:        orig.GuestEmail,
		GuestName:         orig.GuestName,
		GuestPhone:        orig.GuestPhone,
		TrackingPrefix:    trackingPrefix,
		AutoAssign:        route,
	}
	if actor.UserID != nil {
		in.TypeID = orig.TypeID // an account holder picks category and type
	}
	if open != nil {
		if err := open(ctx, in.CategoryID, in.TypeID); err != nil {
			return Ticket{}, fmt.Errorf("%s: %w", err.Error(), ErrValidation)
		}
	}
	return s.create(ctx, in, actor, &orig)
}

// IsClosed reports whether t is in the Closed system status. For a caller that
// must answer differently before it has touched anything (the guest follow-up
// route refuses an open ticket with the generic 404 ahead of every other check).
func (s *Service) IsClosed(t Ticket) bool { return t.StatusID == s.sys.closedID }

// refuseSecondFollowUp is ErrFollowUpExists when the closed ticket already has
// a follow-up — conservatively: ANY parent_child link out of it counts, because
// the link row carries no marker for "made by the follow-up action" and a marker
// would be a schema change (an audit entry would lapse with the retention
// sweep). A staff member who hand-links the closed ticket as a parent therefore
// uses up its requester's one follow-up: it can only refuse a requester, never
// admit a second ticket. Documented in DESIGN.md -> Known limits and pinned by
// TestRequesterFollowUp_OnePerClosedTicket/a_hand-made_parent_link_counts.
func (s *Service) refuseSecondFollowUp(ctx context.Context, st Store, closedID uuid.UUID) error {
	links, err := st.ListLinks(ctx, closedID)
	if err != nil {
		return err
	}
	for _, l := range links {
		if l.SourceTicketID == closedID && l.LinkType == LinkParentChild {
			return ErrFollowUpExists
		}
	}
	return nil
}

// CheckFreeText refuses what Postgres refuses in a text column, and nothing
// else: NUL and invalid UTF-8. Either was a 500 (and, on a create, a tracking
// number already taken, so a gap in the sequence); it is a 400 before anything
// is written. Every other character, tabs, newlines, ESC and the rest, was
// accepted and stored as given in 1.2, and a follow-up copies a stored subject
// and description into a create, so refusing any of them would make a ticket
// that holds one impossible to follow up.
func CheckFreeText(field, v string) error {
	if !utf8.ValidString(v) {
		return fmt.Errorf("%s: is not valid UTF-8: %w", field, ErrValidation)
	}
	if strings.ContainsRune(v, 0) {
		return fmt.Errorf("%s: must not contain a NUL character: %w", field, ErrValidation)
	}
	return nil
}

// checkCreateText is CheckFreeText over a create's free-text fields.
func checkCreateText(in CreateInput) error {
	for _, f := range []struct{ name, v string }{
		{"subject", in.Subject},
		{"description", in.Description},
		{"guest_name", in.GuestName},
		{"guest_phone", in.GuestPhone},
	} {
		if err := CheckFreeText(f.name, f.v); err != nil {
			return err
		}
	}
	return nil
}

// ValidateCreate refuses what create would refuse for the ticket's own fields
// and classification (subject, lengths, priority, category, type, item,
// archived picks), without writing anything and without looking at who the
// ticket is for.
//
// It exists so a caller that resolves the requester can do it AFTER these
// checks: filing for a typed address answers differently for an address with
// an account and one without, and if that came before a refusal the address
// does not affect, a bad category would tell the caller which addresses have
// accounts, for free (#290, "no enumeration surface").
func (s *Service) ValidateCreate(ctx context.Context, in CreateInput) error {
	if err := checkCreateText(in); err != nil {
		return err
	}
	if _, err := ParseTextFormat(string(in.DescriptionFormat)); err != nil {
		return fmt.Errorf("description: %w", err)
	}
	if strings.TrimSpace(in.Subject) == "" {
		return fmt.Errorf("subject is required: %w", ErrValidation)
	}
	if len(in.Subject) > MaxSubjectLength {
		return fmt.Errorf("subject must be %d characters or fewer: %w", MaxSubjectLength, ErrValidation)
	}
	if len(in.Description) > MaxDescriptionLength {
		return fmt.Errorf("description must be at most %d bytes: %w", MaxDescriptionLength, ErrValidation)
	}
	if in.Priority != "" && !in.Priority.Valid() {
		return fmt.Errorf("priority must be one of critical, high, medium, low: %w", ErrValidation)
	}
	return s.validateClassification(ctx, in, nil)
}

// validateClassification is the category, type and item part of create's
// checks, shared with ValidateCreate.
func (s *Service) validateClassification(ctx context.Context, in CreateInput, followUpOf *Ticket) error {
	// The category, checked for the same reason as everything else here:
	// after this point a tracking number has been taken, and an id the
	// foreign key refuses costs a 500 and a permanent gap in the sequence.
	// Only a reporting user's category was checked, and that check asks
	// whether it is OPEN to them — not whether it exists at all, which is
	// what staff and MCP needed.
	if ok, err := s.store.CategoryExists(ctx, in.CategoryID); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("%w: category_id is not a category on this help desk", ErrValidation)
	}

	// And the rest of the classification: that the type belongs to the
	// category, and the item to the type.
	//
	// Verified before this existed: five refused creates advanced the
	// sequence by five. The REST handler checked type-against-category and
	// MCP checked nothing, and nobody checked the item — there is no
	// composite key for item-to-type, so a ticket could carry a type and an
	// item that do not go together and then be routed on that pairing.
	if ok, err := s.store.CTIIsCoherent(ctx, in.CategoryID, in.TypeID, in.ItemID); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf(
			"%w: type_id and item_id must belong to the category and to each other", ErrValidation)
	}

	// Nobody newly picks an archived node (#421). A follow-up is exempt: it
	// copies the closed ticket's classification, which may have been retired
	// since, and that is not a new pick. A requester's follow-up is held to
	// the active-only rule by its own caller (see CreateRequesterFollowUp).
	if followUpOf == nil {
		if err := s.refuseArchivedPick(ctx, in.CategoryID, in.TypeID, in.ItemID, nil); err != nil {
			return err
		}
	}
	return nil
}

// create is the one path that opens a ticket. actor is who did it, for the
// status history, the audit entry and the event: the reporter for an ordinary
// create (Create passes them), the member of staff for a follow-up. followUpOf
// is the closed ticket a follow-up continues, or nil.
func (s *Service) create(ctx context.Context, in CreateInput, actor Actor, followUpOf *Ticket) (Ticket, error) {
	if err := checkCreateText(in); err != nil {
		return Ticket{}, err
	}
	if strings.TrimSpace(in.Subject) == "" {
		return Ticket{}, fmt.Errorf("subject is required: %w", ErrValidation)
	}
	if len(in.Subject) > MaxSubjectLength {
		return Ticket{}, fmt.Errorf("subject must be %d characters or fewer: %w", MaxSubjectLength, ErrValidation)
	}
	if _, err := ParseTextFormat(string(in.DescriptionFormat)); err != nil {
		return Ticket{}, fmt.Errorf("description: %w", err)
	}
	if len(in.Description) > MaxDescriptionLength {
		return Ticket{}, fmt.Errorf("description must be at most %d bytes: %w", MaxDescriptionLength, ErrValidation)
	}
	if in.ReporterUserID == nil && (in.GuestEmail == nil || *in.GuestEmail == "") {
		return Ticket{}, fmt.Errorf("reporter user or guest email is required: %w", ErrValidation)
	}
	// A guest address is a stored recipient, so it goes through the same gate
	// as an account's address. Nothing checked it before: any string was
	// accepted, written to the row, and handed to the mailer — including one
	// carrying a display name, which is chosen text delivered in a header of a
	// message sent from this server's domain.
	if in.GuestEmail != nil && *in.GuestEmail != "" {
		normalised, err := user.ValidateEmail(*in.GuestEmail)
		if err != nil {
			return Ticket{}, fmt.Errorf("guest email: %s: %w", err, ErrValidation)
		}
		in.GuestEmail = &normalised
	}
	// Priority is optional; both callers were defaulting it to medium
	// themselves, so the default lives here now rather than in two places.
	// A value that is present but wrong is a different matter: unchecked it
	// reached the priority CHECK constraint and failed the transaction AFTER
	// NextSeq had consumed a tracking number, so a typo cost a 500 and a
	// permanent gap in the ticket sequence.
	if in.Priority == "" {
		in.Priority = PriorityMedium
	}
	if !in.Priority.Valid() {
		return Ticket{}, fmt.Errorf("priority must be one of critical, high, medium, low: %w", ErrValidation)
	}

	if err := s.validateClassification(ctx, in, followUpOf); err != nil {
		return Ticket{}, err
	}

	// A supplied reporter, checked for the same reason the priority above is:
	// everything after this takes a tracking number first, so an id the
	// foreign key refuses costs a 500 and a permanent gap in the sequence.
	// MCP's create_ticket takes a reporter_user_id from the caller, which is
	// how an unknown one gets here.
	if in.ReporterUserID != nil {
		ok, err := s.store.UserExists(ctx, *in.ReporterUserID)
		if err != nil {
			return Ticket{}, err
		}
		if !ok {
			return Ticket{}, fmt.Errorf("%w: reporter_user_id is not a user on this help desk", ErrValidation)
		}
	}

	seq, err := s.store.NextSeq(ctx)
	if err != nil {
		return Ticket{}, fmt.Errorf("getting ticket sequence: %w", err)
	}

	now := time.Now()
	var guestLink bool
	filedFor := s.filedForAddress(ctx, in)
	t := Ticket{
		ID:                uuid.New(),
		TrackingNumber:    GenerateTrackingNumber(in.TrackingPrefix, now.Year(), seq),
		Subject:           strings.TrimSpace(in.Subject),
		Description:       in.Description,
		DescriptionFormat: in.DescriptionFormat,
		CategoryID:        in.CategoryID,
		TypeID:            in.TypeID,
		ItemID:            in.ItemID,
		Priority:          in.Priority,
		StatusID:          s.sys.newID,
		ReporterUserID:    in.ReporterUserID,
		GuestEmail:        in.GuestEmail,
		GuestName:         in.GuestName,
		GuestPhone:        in.GuestPhone,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	// The ticket, its opening status-history row and its audit entry commit
	// together or not at all.
	if err := s.atomic.InTx(ctx, func(st Store, au audit.Store, ch *notification.Changes) error {
		// A requester's follow-up is decided again on the locked original: it
		// is still Closed, and no follow-up appeared since the check before the
		// tracking number was taken (two clicks, two requests).
		if followUpOf != nil && actor.Role == user.RoleUser {
			locked, err := st.GetByIDForUpdate(ctx, followUpOf.ID)
			if err != nil {
				return err
			}
			if locked.StatusID != s.sys.closedID {
				return fmt.Errorf("%w: a follow-up is created from a closed ticket", ErrNotClosed)
			}
			if err := s.refuseSecondFollowUp(ctx, st, followUpOf.ID); err != nil {
				return err
			}
		}
		// Routing (#506): the first candidate the assignment accepts. A
		// candidate refused as unassignable (a departed colleague still on the
		// round-robin list) is skipped for the next, as the handler did when
		// routing was a second write after the create. Any other error is the
		// database's, which has aborted this transaction anyway.
		// t is captured, so a retry starts from what the rolled-back attempt
		// left in it: routed to a colleague who has since gone, the retry
		// would find nobody and keep the old assignee.
		t.AssigneeUserID, t.AssigneeGroupID = nil, nil
		unrouted := ticketMap(t)
		unrouted["assignee_user_id"], unrouted["assignee_group_id"] = nil, nil
		var routed *Assignment
		for i := range in.AutoAssign {
			c := in.AutoAssign[i]
			err := checkAssignable(ctx, st, c.UserID, c.GroupID)
			if errors.Is(err, ErrValidation) {
				continue
			}
			if err != nil {
				return err
			}
			routed = &c
			break
		}
		if routed != nil {
			t.AssigneeUserID, t.AssigneeGroupID = routed.UserID, routed.GroupID
		}
		if err := st.Create(ctx, t); err != nil {
			return fmt.Errorf("creating ticket: %w", err)
		}
		if err := st.CreateStatusHistoryEntry(ctx, statusHistoryEntry(t.ID, nil, s.sys.newID, actor)); err != nil {
			return fmt.Errorf("recording opening status: %w", err)
		}
		after := ticketMap(t)
		if followUpOf != nil {
			after["follow_up_of"] = followUpOf.ID
		}
		if err := au.Create(ctx, auditEntry(actor.UserID, "ticket", t.ID, "created", nil, after)); err != nil {
			return fmt.Errorf("auditing ticket creation: %w", err)
		}
		// Routing is the system's act, as it was when it was a second write:
		// its own audit entry, with no person behind it.
		if routed != nil {
			routedTo := ticketMap(t)
			routedTo["assignee_user_id"], routedTo["assignee_group_id"] = idString(routed.UserID), idString(routed.GroupID)
			if err := au.Create(ctx, auditEntry(nil, "ticket", t.ID, "assigned", unrouted, routedTo)); err != nil {
				return fmt.Errorf("auditing routing: %w", err)
			}
		}
		// The link back, in the same transaction: a follow-up that exists
		// without its link is a ticket nobody can trace to the one it
		// continues. The closed ticket itself is not written.
		if followUpOf != nil {
			link := TicketLink{SourceTicketID: followUpOf.ID, TargetTicketID: t.ID, LinkType: LinkParentChild}
			if err := st.CreateLink(ctx, link); err != nil {
				return fmt.Errorf("linking follow-up to the closed ticket: %w", err)
			}
		}
		// A guest's first link. Created at send time, not here (#164): see
		// hasGuestRecipient.
		guestLink = hasGuestRecipient(t)

		// Re-read what was written, and announce that rather than the struct we
		// asked the database to store.
		//
		// The two values the notification email is allowed to carry — the
		// tracking number and the guest's address — come from this row and from
		// nowhere else. Built in memory they are request text that happens to
		// have been written down; read back they are the record.
		//
		// There is deliberately no fallback to the in-memory copy. Falling back
		// would put the request copy back in the email on the one path where
		// the read failed, which is the whole thing this avoids — and it would
		// do it invisibly. A read failure costs the notification, not the
		// ticket. (A statement that fails in Postgres has aborted this
		// transaction and surfaces at the next one; a row that is simply not
		// there lands here.)
		var emailTracking, emailRecipient string
		if stored, err := st.GetByID(ctx, t.ID); err == nil {
			emailTracking = string(stored.TrackingNumber)
			emailRecipient = guestRecipient(stored)
			if emailRecipient == "" {
				emailRecipient = filedFor
			}
		}

		// The payload is what makes this email reachable at all. It shipped with
		// none, and eventToEmail returns ok=false without a recipient — so the
		// "Your ticket has been received" mail has never been sent to anyone. For
		// a guest that mail is the only place the tracking number appears, so a
		// guest ticket was unreachable by the person who filed it.
		createdPayload := map[string]any{
			"TrackingNumber": string(t.TrackingNumber),
			"Subject":        t.Subject,
			"Priority":       string(t.Priority),
		}
		if t.GuestEmail != nil && *t.GuestEmail != "" {
			createdPayload["guest_email"] = *t.GuestEmail
		}
		created := raiseOn(t, actor, now)
		created.TrackingNumber = emailTracking
		created.Kinds = []notification.EventType{notification.EventTicketCreated}
		created.Payload = createdPayload
		created.Recipient = emailRecipient
		created.GuestLink = guestLink
		ch.Raise(created)
		if routed != nil {
			assigned := raiseOn(t, actor, now)
			assigned.Kinds = []notification.EventType{notification.EventTicketAssigned}
			// Routing picked the assignee, not the creator: staff mail must not
			// treat it as the creator's own action.
			assigned.RoutedAssignment = true
			assigned.Changes = append(optIDChange("assignee_user_id", nil, routed.UserID),
				optIDChange("assignee_group_id", nil, routed.GroupID)...)
			ch.Raise(assigned)
		}
		// Announced like any other link (AddLink), for whoever follows
		// ticket.linked: the original is the source, the follow-up its child.
		// Its own event, on the original: one event per ticket.
		if followUpOf != nil {
			linked := raiseOn(*followUpOf, actor, now)
			linked.Kinds = []notification.EventType{notification.EventTicketLinked}
			linked.Payload = map[string]any{"target_id": t.ID, "link_type": LinkParentChild}
			ch.Raise(linked)
		}
		return nil
	}); err != nil {
		return Ticket{}, err
	}

	// Everything below runs only after the commit.
	if s.sla != nil {
		_ = s.sla.AttachPolicy(ctx, t) // SLA failure is non-fatal
	}

	return t, nil
}

// filedForAddress is the address of the account somebody else has filed a
// ticket for (#444), who is told with the ordinary "we have received your
// ticket" mail. Empty for everyone else: a requester or a member of staff who
// filed their own ticket (CreateInput.FiledBy nil, or themselves) is not mailed
// about it, as before, and a guest's mail is addressed from the stored ticket.
// Empty too when the address cannot be had (no lookup wired, an account that
// cannot be read, a service account): the ticket is filed, only the mail is lost.
func (s *Service) filedForAddress(ctx context.Context, in CreateInput) string {
	if in.FiledBy == nil || in.ReporterUserID == nil || *in.FiledBy == *in.ReporterUserID || s.userEmail == nil {
		return ""
	}
	email, err := s.userEmail(ctx, *in.ReporterUserID)
	if err != nil {
		return ""
	}
	return email
}

// UpdateStatus changes the ticket status after verifying the actor has
// permission to make that transition.
// applyStatusTimestamps keeps resolved_at, closed_at and the SLA pause fields
// consistent with the status a ticket is being moved to.
//
// Pure and taking the cached system statuses explicitly so the rule lives in
// one place rather than being re-derived at each of the five call sites that
// change a status. Called by every door that changes a status, inside its
// transaction, on the locked row.
func applyStatusTimestamps(t *Ticket, oldStatusID uuid.UUID, newStatus Status, sys *systemStatuses, now time.Time) {
	switch newStatus.ID {
	case sys.resolvedID:
		// Preserve the timestamp only when the ticket is ALREADY Resolved, so
		// re-resolving does not silently extend the reopen window. A ticket
		// arriving from any other status — notably Closed, which keeps its old
		// resolved_at — is being resolved afresh and gets a fresh stamp;
		// otherwise the reporter is judged against a window that expired
		// before this resolution happened.
		// oldStatusID, not t.StatusID: both callers assign the new status
		// before calling, so reading it here would always match.
		if oldStatusID != sys.resolvedID || t.ResolvedAt == nil {
			t.ResolvedAt = &now
		}
		t.ClosedAt = nil
	case sys.closedID:
		if t.ClosedAt == nil {
			t.ClosedAt = &now
		}
	default:
		// Any other status means the ticket is open again.
		t.ResolvedAt = nil
		t.ClosedAt = nil
	}

	// SLA pause. Decided from the ROW, not from oldStatusID: t.PendingSince
	// being set is the fact that an interval is open, so Pending→Pending is a
	// no-op, leaving Pending by any door closes the interval, and a status
	// renamed away from "Pending" still closes the interval it opened.
	entering := newStatus.Name == StatusNamePending
	switch {
	case entering && t.PendingSince == nil:
		t.PendingSince = &now
	case !entering && t.PendingSince != nil:
		// Clamped at zero: PendingSince may have been written by a different
		// app replica than the one computing now. With clock skew between
		// them, entering Pending on a fast-clocked replica and leaving on a
		// slow-clocked one within the skew window makes the delta negative,
		// which migration 000025's CHECK (sla_paused_seconds >= 0) rejects —
		// failing whichever door is leaving Pending (UpdateStatus,
		// reply-reopen, resolve, close) with a 500 until the skew elapses.
		// See #223.
		if delta := now.Sub(*t.PendingSince); delta > 0 {
			t.SLAPausedSeconds += int64(delta / time.Second)
		}
		t.PendingSince = nil
	}
}

// resolutionInstant is the instant a resolution should be recorded against
// for SLA purposes: the ticket's own ResolvedAt when it already has one
// (applyStatusTimestamps' closedID case never sets or clears it, so it holds
// whatever it held when the ticket entered — or last sat in — Resolved), and
// now only when the ticket has genuinely never been resolved. See #227.
func resolutionInstant(t Ticket, now time.Time) time.Time {
	if t.ResolvedAt != nil {
		return *t.ResolvedAt
	}
	return now
}

func (s *Service) UpdateStatus(ctx context.Context, ticketID, newStatusID uuid.UUID, actor Actor) (Ticket, error) {
	newStatus, err := s.statusFor(ctx, newStatusID, actor)
	if err != nil {
		return Ticket{}, err
	}

	var t Ticket
	var now time.Time
	if err := s.atomic.InTx(ctx, func(st Store, au audit.Store, ch *notification.Changes) error {
		var err error
		t, now, err = s.updateStatusInTx(ctx, st, au, ch, ticketID, newStatus, actor)
		return err
	}); err != nil {
		return Ticket{}, err
	}

	s.recordResolvedAfterStatus(ctx, t, newStatus.ID, now)
	return t, nil
}

// statusFor reads the status a change is to move a ticket into and checks that
// the actor's role may move tickets there. The role check does not depend on
// the row, so it stays outside the transaction and refuses without taking a
// lock.
func (s *Service) statusFor(ctx context.Context, newStatusID uuid.UUID, actor Actor) (Status, error) {
	newStatus, err := s.getStatusByID(ctx, newStatusID)
	if err != nil {
		return Status{}, err
	}
	if err := CanTransitionStatus(newStatus, actor.Role); err != nil {
		return Status{}, fmt.Errorf("status transition not allowed: %w", err)
	}
	return newStatus, nil
}

// recordResolvedAfterStatus is what a status change into Resolved or Closed
// does for the SLA once it has committed.
//
// The other door into Resolved. Recording this only in Resolve() meant a
// ticket resolved by PATCHing status_id, or by MCP update_ticket_status,
// left sla_records.resolved_at NULL — and the breach evaluator reads NULL
// as "never resolved", so an on-time resolution became a permanent false
// breach. Non-fatal and after the commit, matching Resolve.
//
// And the other door into Closed. Same #220 reasoning as close(): a ticket
// moved straight to Closed without ever resolving would otherwise carry a NULL
// sla_records.resolved_at forever. RecordResolved no-ops when a resolution is
// already recorded, so this is a no-op on the ordinary Resolved-then-Closed
// path.
//
// Non-fatal: SLA bookkeeping must not fail the status change it describes.
// Discarded here rather than logged because domain code does not log —
// cmd/server wraps the SLA service so the boundary reports these, which is
// where a failure can actually be seen.
//
// #234, #227: resolutionInstant, not a bare now. A genuine re-resolve (#225)
// preserves t.ResolvedAt at its ORIGINAL instant (applyStatusTimestamps never
// moves it forward), but RecordResolved's own no-op guard only fires once
// sla_records already has a resolution — if an earlier call was dropped this is
// the repair path, and it must stamp the real original instant, not this later
// call's now, which can manufacture a false breach on a ticket that was
// actually resolved on time.
func (s *Service) recordResolvedAfterStatus(ctx context.Context, t Ticket, newStatusID uuid.UUID, now time.Time) {
	if s.sla != nil && (newStatusID == s.sys.resolvedID || newStatusID == s.sys.closedID) {
		_ = s.sla.RecordResolved(ctx, t, resolutionInstant(t, now))
	}
}

// updateStatusInTx is UpdateStatus's body for a caller that already has a
// transaction. It returns the ticket as written and the time taken under the
// row lock.
//
// resolved_at and closed_at are maintained here as well as in
// Resolve/Close, because this is a second door into the same three
// states and it used to set StatusID alone.
//
// A ticket moved to Resolved this way had a NULL resolved_at, and
// CanUserUpdate reads that as "resolved but no timestamp — treat as
// permanently resolved", so the reporter was refused inside an open reopen
// window. It was also invisible to ListResolvedBefore and so would never
// auto-close. Moving OFF Resolved without clearing the timestamp is the
// mirror image: once the auto-close scheduler is wired, a ticket being
// actively worked would be closed underneath whoever was working it.
func (s *Service) updateStatusInTx(ctx context.Context, st Store, au audit.Store, ch *notification.Changes,
	ticketID uuid.UUID, newStatus Status, actor Actor) (Ticket, time.Time, error) {
	newStatusID := newStatus.ID
	// Read under the lock: everything below is computed from this row, so
	// a concurrent writer that committed first is seen rather than
	// overwritten.
	t, err := st.GetByIDForUpdate(ctx, ticketID)
	if err != nil {
		return Ticket{}, time.Time{}, err
	}
	now := time.Now()
	// Closed is terminal by default (#349): nothing leaves it by a status
	// change unless closed_reopen_policy lets this role. Decided on the
	// locked row, so a close that committed first is seen, and the policy
	// is read now. Staff and admin otherwise get a new linked ticket
	// (CreateFollowUp).
	forcedPolicy := ""
	if t.StatusID == s.sys.closedID && newStatusID != s.sys.closedID {
		p, err := s.forceReopenGate(ctx, actor)
		if err != nil {
			return Ticket{}, time.Time{}, err
		}
		forcedPolicy = p
	}
	// A retired status takes no new tickets, whoever asks. Decided on the
	// locked row so "already in it" is the truth: a ticket sitting in an
	// inactive status may be sent the status it holds (a PATCH that
	// repeats it alongside another change), and may leave.
	if !newStatus.Active && t.StatusID != newStatusID {
		return Ticket{}, time.Time{}, fmt.Errorf("status %q: %w", newStatus.Name, ErrStatusInactive)
	}
	before := ticketMap(t)
	oldStatusID := t.StatusID
	t.StatusID = newStatusID
	t.UpdatedAt = now
	applyStatusTimestamps(&t, oldStatusID, newStatus, s.sys, now)
	// Decided on the locked row, like the Closed gate above. The count goes
	// in before the write so one Update saves the status and the flag.
	reopening := s.isReopen(oldStatusID, newStatusID)
	firstReopen := false
	if reopening {
		firstReopen = markReopened(&t, now)
	}

	if err := st.Update(ctx, t); err != nil {
		return Ticket{}, time.Time{}, fmt.Errorf("updating ticket status: %w", err)
	}
	if err := st.CreateStatusHistoryEntry(ctx, statusHistoryEntry(t.ID, &oldStatusID, newStatusID, actor)); err != nil {
		return Ticket{}, time.Time{}, fmt.Errorf("recording status change: %w", err)
	}
	after := ticketMap(t)
	if forcedPolicy != "" {
		forcedAudit(after, forcedPolicy)
	}
	if err := au.Create(ctx, auditEntry(actor.UserID, "ticket", t.ID, "status_changed", before, after)); err != nil {
		return Ticket{}, time.Time{}, fmt.Errorf("auditing status change: %w", err)
	}
	if reopening {
		// The same entry the other two doors write, so the feed and the
		// audit view's action filter find every reopen by one name.
		reopenedAfter := ticketMap(t)
		if forcedPolicy != "" {
			forcedAudit(reopenedAfter, forcedPolicy)
		}
		if err := au.Create(ctx, auditEntry(actor.UserID, "ticket", t.ID, "reopened", before, reopenedAfter)); err != nil {
			return Ticket{}, time.Time{}, fmt.Errorf("auditing reopen: %w", err)
		}
	}

	// A status change is something the guest is told about, so it rotates
	// the link and the notification carries the replacement. Closing is
	// the exception: it stops rotating, and leaves the links the guest
	// holds alone (#349) — the ticket is an archive they may still read,
	// until the link expires. And it tells nobody, which is what Close()
	// does. There is no new link to send, and a mail with none fell back to
	// the account URL — the guest was sent "see where it stands" pointing at
	// /tickets/<uuid>, a page they have no account to open. The two doors into
	// Closed behave the same way.
	closing := newStatusID == s.sys.closedID
	if kinds := s.statusKinds(oldStatusID, newStatusID); len(kinds) > 0 {
		ev := raiseOn(t, actor, now)
		ev.Kinds = kinds
		ev.Changes = statusChanges(oldStatusID, newStatusID)
		ev.Payload = map[string]any{"new_status_id": newStatusID}
		ev.Recipient = guestNotifyTarget(t, closing)
		ev.GuestLink = !closing && hasGuestRecipient(t)
		ev.StatusName = newStatus.Name
		ch.Raise(ev)
	}

	if reopening {
		// After the status is raised, so the priority it may raise is the
		// ticket's last word.
		if t, err = s.finishReopenInTx(ctx, st, au, ch, t, firstReopen, actor, now); err != nil {
			return Ticket{}, time.Time{}, err
		}
	}
	return t, now, nil
}

// Patch is the set of changes one PATCH of a ticket makes. A nil field is
// left alone.
type Patch struct {
	StatusID *uuid.UUID
	// Assign gives the ticket to somebody; Assignment{} (both nil) clears it.
	Assign   *Assignment
	CTI      *Classification
	Priority *Priority
}

// Classification is a ticket's category, type and item.
type Classification struct {
	CategoryID uuid.UUID
	TypeID     *uuid.UUID
	ItemID     *uuid.UUID
}

// Update applies a PATCH in ONE transaction (#506): the status, the
// assignment, the classification and the priority, in that order, each by the
// same step that changes it alone. They commit together or not at all, and
// announce one event for the ticket, however many of them there are. The
// Validate* checks a caller makes up front stay the early answer; every step
// still checks on the locked row, as it does alone.
//
// The priority is last so that a category changed in the same request is the
// one its SLA policy is matched against.
func (s *Service) Update(ctx context.Context, ticketID uuid.UUID, p Patch, actor Actor) (Ticket, error) {
	var newStatus Status
	if p.StatusID != nil {
		var err error
		if newStatus, err = s.statusFor(ctx, *p.StatusID, actor); err != nil {
			return Ticket{}, err
		}
	}
	if p.CTI != nil {
		if err := s.ValidateCTI(ctx, ticketID, p.CTI.CategoryID, p.CTI.TypeID, p.CTI.ItemID); err != nil {
			return Ticket{}, err
		}
	}

	var (
		statusTicket Ticket
		statusAt     time.Time
	)
	if err := s.atomic.InTx(ctx, func(st Store, au audit.Store, ch *notification.Changes) error {
		var err error
		if p.StatusID != nil {
			if statusTicket, statusAt, err = s.updateStatusInTx(ctx, st, au, ch, ticketID, newStatus, actor); err != nil {
				return err
			}
		}
		if p.Assign != nil {
			if _, err = s.assignInTx(ctx, st, au, ch, ticketID, p.Assign.UserID, p.Assign.GroupID, actor); err != nil {
				return err
			}
		}
		if p.CTI != nil {
			if _, err = s.classifyInTx(ctx, st, ch, ticketID, *p.CTI, actor); err != nil {
				return err
			}
		}
		if p.Priority != nil {
			if _, err = s.changePriorityLocked(ctx, st, au, ch, ticketID, *p.Priority, actor); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return Ticket{}, err
	}

	if p.StatusID != nil {
		s.recordResolvedAfterStatus(ctx, statusTicket, newStatus.ID, statusAt)
	}
	return s.store.GetByID(ctx, ticketID)
}

// checkAssignable is the assignment's own refusal: the user must be somebody
// a ticket can be given to, and the group must exist. Assign asks it on the
// locked row, where it is the guarantee; ValidateAssignment asks it ahead of
// the first write, where it is the early answer.
func checkAssignable(ctx context.Context, st Store, assigneeUserID, assigneeGroupID *uuid.UUID) error {
	if assigneeUserID != nil {
		ok, err := st.IsAssignableUser(ctx, *assigneeUserID)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: that account cannot be given a ticket — it may be deleted, disabled, or not staff", ErrValidation)
		}
	}
	if assigneeGroupID != nil {
		ok, err := st.IsAssignableGroup(ctx, *assigneeGroupID)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: no such group", ErrValidation)
		}
	}
	return nil
}

// ValidateAssignment runs the checks Assign makes on its assignee and group,
// without writing. A caller that applies other changes in the same request
// (the PATCH handler) calls it first, so a refused assignment leaves the whole
// request unapplied (#468). Assign still checks on the locked row, so this is
// the early answer, not the guarantee: an account disabled between the two is
// refused by Assign, after anything saved before it.
func (s *Service) ValidateAssignment(ctx context.Context, assigneeUserID, assigneeGroupID *uuid.UUID) error {
	return checkAssignable(ctx, s.store, assigneeUserID, assigneeGroupID)
}

// Assign sets the assignee user and/or group on a ticket.
func (s *Service) Assign(ctx context.Context, ticketID uuid.UUID, assigneeUserID, assigneeGroupID *uuid.UUID, actor Actor) (Ticket, error) {
	var t Ticket

	if err := s.atomic.InTx(ctx, func(st Store, au audit.Store, ch *notification.Changes) error {
		var err error
		t, err = s.assignInTx(ctx, st, au, ch, ticketID, assigneeUserID, assigneeGroupID, actor)
		return err
	}); err != nil {
		return Ticket{}, err
	}
	return t, nil
}

// assignInTx is Assign's body for a caller that already has a transaction.
func (s *Service) assignInTx(ctx context.Context, st Store, au audit.Store, ch *notification.Changes,
	ticketID uuid.UUID, assigneeUserID, assigneeGroupID *uuid.UUID, actor Actor) (Ticket, error) {
	now := time.Now()
	t, err := st.GetByIDForUpdate(ctx, ticketID)
	if err != nil {
		return Ticket{}, err
	}
	// Checked here, inside the transaction that writes it, because the
	// caller is not the only caller.
	//
	// The REST handler checked the assignee and MCP did not, so
	// assign_ticket put tickets on deleted accounts and on reporting
	// users — and a ticket assigned to a deleted account is invisible:
	// it renders with no assignee anyone can resolve and it is missing
	// from the unassigned queue, because the column is not null. The
	// auto-assign list has the same problem, since nothing removes
	// somebody from it when they leave.
	//
	// A check the caller makes is a check every future caller has to
	// remember to make. This one is where the write is.
	if err := checkAssignable(ctx, st, assigneeUserID, assigneeGroupID); err != nil {
		return Ticket{}, err
	}

	before := ticketMap(t)
	previous, previousGroup := t.AssigneeUserID, t.AssigneeGroupID
	t.AssigneeUserID = assigneeUserID
	t.AssigneeGroupID = assigneeGroupID
	t.UpdatedAt = now

	if err := st.Update(ctx, t); err != nil {
		return Ticket{}, fmt.Errorf("assigning ticket: %w", err)
	}
	// "unassigned" when the new assignee is nobody, "assigned" otherwise
	// — mirroring how UnassignForUser already distinguishes the two.
	// Writing "assigned" unconditionally here made PATCH
	// {clear_assignee: true} indistinguishable in the feed from an
	// actual assignment. See #326.
	action := "assigned"
	if assigneeUserID == nil && assigneeGroupID == nil {
		action = "unassigned"
	}
	if err := au.Create(ctx, auditEntry(actor.UserID, "ticket", t.ID, action, before, ticketMap(t))); err != nil {
		return Ticket{}, fmt.Errorf("auditing assignment: %w", err)
	}
	// Reassigned away from somebody: their follow goes with the
	// assignment (#441), written after the assignment's own entry so the
	// feed reads the reassignment first. The same person again changes
	// nothing.
	if previous != nil && (assigneeUserID == nil || *assigneeUserID != *previous) {
		if err := dropAssigneeFollow(ctx, st, au, t.ID, *previous, actor.UserID, assigneeUserID); err != nil {
			return Ticket{}, err
		}
	}

	// Nothing moved (same user and group, or a clear on an unassigned ticket):
	// nothing is announced, as for a same-status or same-priority change. The
	// audit entry above is still written, as a same-status move writes its own.
	changes := append(optIDChange("assignee_user_id", previous, assigneeUserID),
		optIDChange("assignee_group_id", previousGroup, assigneeGroupID)...)
	if len(changes) == 0 {
		return t, nil
	}
	ev := raiseOn(t, actor, now)
	ev.Kinds = []notification.EventType{notification.EventTicketAssigned}
	ev.Changes = changes
	ch.Raise(ev)
	return t, nil
}

// ChangePriority sets a ticket's priority and re-matches its SLA policy for the
// new priority (#443). Staff and admin only: anyone else gets ErrForbidden,
// decided here so that a caller added later cannot forget it. Whether the
// caller may see the ticket at all is the route's question.
//
// A Resolved or Closed ticket is refused outright, priority included
// (ErrClosed, the same 409 every other write to a closed ticket gets): a
// finished ticket's priority is part of what its SLA was measured against.
// Checked on the locked row.
//
// A staff change never restarts an SLA clock; see planSLARematch. Everything
// happens in one transaction: the priority, the SLA rows and the audit entry
// commit together or not at all. See changePriorityInTx.
func (s *Service) ChangePriority(ctx context.Context, ticketID uuid.UUID, p Priority, actor Actor) (Ticket, error) {
	var out Ticket

	if err := s.atomic.InTx(ctx, func(st Store, au audit.Store, ch *notification.Changes) error {
		var err error
		out, err = s.changePriorityLocked(ctx, st, au, ch, ticketID, p, actor)
		return err
	}); err != nil {
		return Ticket{}, err
	}
	return out, nil
}

// changePriorityLocked is ChangePriority's body for a caller that already has a
// transaction: Update runs it as the last step of a PATCH.
func (s *Service) changePriorityLocked(ctx context.Context, st Store, au audit.Store, ch *notification.Changes,
	ticketID uuid.UUID, p Priority, actor Actor) (Ticket, error) {
	t, err := st.GetByIDForUpdate(ctx, ticketID)
	if err != nil {
		return Ticket{}, err
	}
	// Taken under the row lock, not before it: a change that waited behind
	// another one must not stamp the record it supersedes with a time from
	// before that record existed. superseded_at orders a ticket's history.
	now := time.Now()
	if err := CanChangePriority(actor.Role); err != nil {
		return Ticket{}, err
	}
	if s.isFinished(t.StatusID) {
		return Ticket{}, ErrClosed
	}
	return s.changePriorityInTx(ctx, st, au, ch, t, p, PriorityChangeManual, actor, now)
}

// isFinished reports whether a status is Resolved or Closed: the two states in
// which a ticket's priority is frozen (#443).
func (s *Service) isFinished(statusID uuid.UUID) bool {
	return statusID == s.sys.closedID || statusID == s.sys.resolvedID
}

// ValidatePriorityChange is the closed-state check ChangePriority makes, without
// writing, for a caller that will apply other changes first. statusAfter is the
// status the same request moves the ticket to, or nil when it leaves the status
// alone. The priority is refused (ErrClosed) when the ticket would end up
// Resolved or Closed: either the request moves it there, or it is already there
// and the request does not move it out. A move out of Closed that the reopen
// policy refuses is UpdateStatus's refusal to give; here it counts as moving
// out. ChangePriority still checks on the locked row, so this is the early
// answer, not the guarantee.
func (s *Service) ValidatePriorityChange(ctx context.Context, ticketID uuid.UUID, statusAfter *uuid.UUID) error {
	if statusAfter != nil {
		if s.isFinished(*statusAfter) {
			return ErrClosed
		}
		return nil
	}
	t, err := s.store.GetByID(ctx, ticketID)
	if err != nil {
		return err
	}
	if s.isFinished(t.StatusID) {
		return ErrClosed
	}
	return nil
}

// isReopen reports whether a status change takes a ticket out of Resolved or
// Closed to an open status (#464). Resolved to Closed, and Closed to Resolved,
// stay finished and are not reopens.
func (s *Service) isReopen(oldStatusID, newStatusID uuid.UUID) bool {
	return s.isFinished(oldStatusID) && !s.isFinished(newStatusID)
}

// markReopened counts a reopen on t, ahead of the write that saves the new
// status, and reports whether it is the ticket's first. The count is the whole
// memory of "first": it is never reset, so a later reopen flags the ticket again
// but is not bumped again.
//
// It also stamps the reopen as a restart point on the ticket: when it happened
// and the Pending total then. Call it after applyStatusTimestamps, so an
// interval the move just closed or opened is counted. The SLA clock resumes
// from here if a later priority change gives the ticket a record back after a
// reopen that started none (it landed on a priority with no policy).
func markReopened(t *Ticket, now time.Time) (first bool) {
	first = t.ReopenCount == 0
	t.ReopenCount++
	t.LastReopenedAt = &now
	t.PausedSecondsAtLastReopen = pausedSecondsAt(*t, now)
	return first
}

// finishReopenInTx is what every reopen does after its status write, inside the
// same transaction: raise the priority one level when this is the first reopen,
// and start the SLA over. The ticket's audit trail (reopened, priority_changed,
// sla_restarted) and the SLA rows commit with the status change or not at all.
//
// A later reopen passes the priority it already has: the clocks still start
// over (changePriorityInTx restarts them whatever the priority), and no
// priority_changed entry is written because nothing changed.
func (s *Service) finishReopenInTx(ctx context.Context, st Store, au audit.Store, ch *notification.Changes, t Ticket, first bool,
	actor Actor, now time.Time) (Ticket, error) {
	p := t.Priority
	if first {
		p = p.Raised()
	}
	return s.changePriorityInTx(ctx, st, au, ch, t, p, PriorityChangeReopened, actor, now)
}

// changePriorityInTx is ChangePriority's body for a caller that already holds
// the ticket's row lock in its own transaction and has written its own changes
// to t. It checks no role and no status: the caller decided this change is
// allowed, and for a reopen has just moved the ticket out of Closed in this
// same transaction. Unexported because ChangePriority and the reopen paths
// (finishReopenInTx, from addReply, UpdateStatus and Reopen) are its only callers.
//
// reason decides what the change does to the SLA:
//
//   - PriorityChangeManual (ChangePriority): the same priority is a no-op, with
//     no write, no audit entry and no re-match. Otherwise the policy is
//     re-matched and the clock is kept (planSLARematch).
//   - PriorityChangeReopened (a reopen, by any door): the clocks always start
//     over from now (planSLARestart), even when the priority does not change,
//     which is the case for a ticket already at the highest priority and for
//     every reopen after the first. The caller makes one call whatever the
//     priority (finishReopenInTx): the raised priority on the first reopen, the
//     current one after.
//     With the SLA switched off no record is created or superseded.
func (s *Service) changePriorityInTx(ctx context.Context, st Store, au audit.Store, ch *notification.Changes, t Ticket,
	p Priority, reason PriorityChangeReason, actor Actor, now time.Time) (Ticket, error) {
	if !p.Valid() {
		return Ticket{}, fmt.Errorf("%w: priority must be one of critical, high, medium, low", ErrValidation)
	}
	reopen := reason == PriorityChangeReopened
	changed := p != t.Priority
	if !changed && !reopen {
		return t, nil
	}

	before := ticketMap(t)
	oldPriority := t.Priority
	if changed {
		t.Priority = p
		t.UpdatedAt = now
		if err := st.Update(ctx, t); err != nil {
			return Ticket{}, fmt.Errorf("changing priority: %w", err)
		}
	}

	cur, err := st.CurrentSLARecordForUpdate(ctx, t.ID)
	if err != nil {
		return Ticket{}, fmt.Errorf("reading SLA record: %w", err)
	}
	matched, err := st.MatchSLAPolicy(ctx, p, t.CategoryID)
	if err != nil {
		return Ticket{}, fmt.Errorf("matching SLA policy: %w", err)
	}
	enabled := s.slaEnabled != nil && s.slaEnabled(ctx)
	plan := planSLARematch
	if reopen {
		plan = planSLARestart
	}
	outcome, start := plan(enabled, cur, matched, t, now)
	// A staff change that gives a record to a ticket that has none: the clock
	// resumes from the later of the ticket's last reopen and the newest
	// superseded record's start, with the pause base that goes with it and none
	// of the old record's facts. A restart is not lost to a trip through a
	// priority with no policy, whether the restart was a record or only a
	// reopen that landed on such a priority, and the time spent there still
	// counts. A ticket that never had a record and was never reopened keeps the
	// plan's NULL start, the ticket's creation.
	if outcome == SLAAttached && !reopen {
		prior, err := st.LatestSupersededSLARecord(ctx, t.ID)
		if err != nil {
			return Ticket{}, fmt.Errorf("reading superseded SLA record: %w", err)
		}
		if prior != nil {
			start.StartedAt, start.PausedSecondsAtStart = prior.StartedAt, prior.PausedSecondsAtStart
		}
		// A NULL start is the ticket's creation, earlier than any reopen.
		if t.LastReopenedAt != nil && (start.StartedAt == nil || t.LastReopenedAt.After(*start.StartedAt)) {
			start.StartedAt, start.PausedSecondsAtStart = t.LastReopenedAt, t.PausedSecondsAtLastReopen
		}
	}

	// The old record first: a ticket has one current record at most.
	switch outcome {
	case SLARematched, SLARestarted, SLARemoved:
		if err := st.SupersedeSLARecord(ctx, cur.ID, now); err != nil {
			return Ticket{}, fmt.Errorf("superseding SLA record: %w", err)
		}
	}
	if start != nil {
		if err := st.StartSLARecord(ctx, *start); err != nil {
			return Ticket{}, fmt.Errorf("starting SLA record: %w", err)
		}
	}

	if changed {
		if cur != nil {
			before["sla_policy_id"] = cur.PolicyID
			before["sla_record_id"] = cur.ID
		}
		after := ticketMap(t)
		after["priority_change_reason"] = reason
		after["sla_outcome"] = outcome
		if start != nil {
			after["sla_policy_id"] = start.PolicyID
			after["sla_record_id"] = start.ID
		}
		if err := au.Create(ctx, auditEntry(actor.UserID, "ticket", t.ID, "priority_changed", before, after)); err != nil {
			return Ticket{}, fmt.Errorf("auditing priority change: %w", err)
		}
		// A priority change announced nothing before #506. Only a change that
		// moved the priority does: a reopen that restarts the clocks at the same
		// priority has nothing to say about it.
		ev := raiseOn(t, actor, now)
		ev.Kinds = []notification.EventType{notification.EventTicketPriorityChanged}
		ev.Changes = []notification.Change{notification.ValueChange("priority", string(oldPriority), string(p))}
		ch.Raise(ev)
	}

	// A reopen that replaced a clock says so in an entry of its own, so the
	// audit view's action filter finds the moments a clock started again, and so
	// a restart at an unchanged priority is not silent. Only when a record was
	// superseded (Erik, 2026-10-15): a reopen that starts a first clock on a
	// ticket with none replaced nothing, and a priority with no policy still
	// supersedes the old record (and starts none). With the SLA off nothing was
	// superseded either.
	if reopen && (outcome == SLARestarted || outcome == SLARemoved) {
		prev := map[string]any{"sla_record_id": cur.ID, "sla_policy_id": cur.PolicyID}
		next := map[string]any{"previous_sla_record_id": cur.ID}
		if start != nil {
			next["sla_record_id"] = start.ID
			next["sla_policy_id"] = start.PolicyID
		}
		if err := au.Create(ctx, auditEntry(actor.UserID, "ticket", t.ID, "sla_restarted", prev, next)); err != nil {
			return Ticket{}, fmt.Errorf("auditing SLA restart: %w", err)
		}
	}
	return t, nil
}

// AddReply appends a reply to a ticket. If the actor is a user replying to a
// Resolved ticket within the reopen window, the ticket is automatically
// reopened to the configured target status.
//
// notifyCustomer controls whether a ticket-update email is sent to the
// reporter. It is forced false for internal notes. reporterEmail is the
// recipient address; callers are responsible for looking it up.
func (s *Service) AddReply(ctx context.Context, ticketID uuid.UUID, body string, format TextFormat, internal bool, notifyCustomer bool, reporterEmail string, actor Actor, reopenWindowDays int, reopenTargetStatusID uuid.UUID) (Reply, error) {
	return s.addReply(ctx, ticketID, body, format, internal, notifyCustomer, reporterEmail, actor, reopenWindowDays, reopenTargetStatusID, nil)
}

// AddGuestReply appends a reply from a guest, who has no account.
//
// The caller has already resolved a token to this ticket, so ownership is
// settled before this is reached — which is why the ownership half of
// CanUserUpdate is skipped and the lifecycle half is not. A closed ticket is
// closed to the customer who opened it, and the reopen window does not widen
// because the reply arrived by link rather than by login.
//
// The reply has no author. That is the only way AuthorID is NULL, which is what
// lets a reader tell a customer's words from staff's without another column.
//
// A guest reply is never internal and never notifies: the customer is the one
// writing, and mailing them their own message back is noise. Staff see it in
// the thread.
func (s *Service) AddGuestReply(ctx context.Context, ticketID uuid.UUID, body string, format TextFormat, reopenWindowDays int, reopenTargetStatusID uuid.UUID) (Reply, error) {
	guest := Actor{UserID: nil, Role: user.RoleUser}
	return s.addReply(ctx, ticketID, body, format, false, false, "", guest, reopenWindowDays, reopenTargetStatusID,
		func(t Ticket, status Status) error {
			return CanGuestUpdate(t, status, reopenWindowDays)
		})
}

// CanUploadAttachment reports whether actor may attach a file to this ticket
// right now — the same rule addReply enforces for a written reply, reused
// rather than duplicated: an attachment is the same kind of update, so a
// reporting user must own the ticket, and neither a Closed ticket nor a
// Resolved one past its reopen window accepts one from anybody but staff or
// an admin.
func (s *Service) CanUploadAttachment(ctx context.Context, ticketID uuid.UUID, actor Actor, reopenWindowDays int) error {
	t, status, err := s.ticketAndStatus(ctx, ticketID)
	if err != nil {
		return err
	}
	u := user.User{Role: actor.Role}
	if actor.UserID != nil {
		u.ID = *actor.UserID
	}
	return CanUserUpdate(t, u, status, reopenWindowDays)
}

// CanGuestUploadAttachment is CanUploadAttachment for a guest. The caller has
// already resolved a token to this one ticket — see AddGuestReply — so there
// is no ownership left to check, only the lifecycle rule.
func (s *Service) CanGuestUploadAttachment(ctx context.Context, ticketID uuid.UUID, reopenWindowDays int) error {
	t, status, err := s.ticketAndStatus(ctx, ticketID)
	if err != nil {
		return err
	}
	return CanGuestUpdate(t, status, reopenWindowDays)
}

// CanRequesterWrite is the rule for every write a requester has that is not a
// reply or an upload: a reporting user may not change a Closed ticket (#349).
// Staff and admin are not asked. Returns ErrClosed, or ErrNotFound for a
// ticket that does not exist.
//
// A separate question from CanUploadAttachment on purpose. That one is the
// reply rule, which also refuses a Resolved ticket past its reopen window; an
// edit to a field or a link has never been held to that, and #349 does not
// change it. Both ask closedRefusal, so "closed" means one thing.
func (s *Service) CanRequesterWrite(ctx context.Context, ticketID uuid.UUID, actor Actor) error {
	if actor.Role != user.RoleUser {
		return nil
	}
	_, status, err := s.ticketAndStatus(ctx, ticketID)
	if err != nil {
		return err
	}
	return closedRefusal(status)
}

// ticketAndStatus fetches a ticket and the Status its StatusID names — the
// pair every authorisation check here needs.
func (s *Service) ticketAndStatus(ctx context.Context, ticketID uuid.UUID) (Ticket, Status, error) {
	t, err := s.store.GetByID(ctx, ticketID)
	if err != nil {
		return Ticket{}, Status{}, err
	}
	status, err := s.getStatusByID(ctx, t.StatusID)
	if err != nil {
		return Ticket{}, Status{}, err
	}
	return t, status, nil
}

// addReply is the one reply path. authorize replaces the default ownership
// check when non-nil; everything after it — the reopen, the write, the audit,
// the dispatch — is shared, so a guest reply cannot drift from a user's.
func (s *Service) addReply(ctx context.Context, ticketID uuid.UUID, body string, format TextFormat, internal bool, notifyCustomer bool, reporterEmail string, actor Actor, reopenWindowDays int, reopenTargetStatusID uuid.UUID, authorize func(Ticket, Status) error) (Reply, error) {
	if err := CheckFreeText("body", body); err != nil {
		return Reply{}, err
	}
	if len(body) > MaxReplyLength {
		return Reply{}, fmt.Errorf("body must be at most %d bytes: %w: %w", MaxReplyLength, ErrReplyTooLong, ErrValidation)
	}
	if _, err := ParseTextFormat(string(format)); err != nil {
		return Reply{}, fmt.Errorf("reply: %w", err)
	}
	t, err := s.store.GetByID(ctx, ticketID)
	if err != nil {
		return Reply{}, err
	}

	currentStatus, err := s.getStatusByID(ctx, t.StatusID)
	if err != nil {
		return Reply{}, err
	}

	// The ID matters as much as the Role: CanUserUpdate compares the ticket's
	// reporter against it. Passing a User carrying only a Role is what left the
	// ownership rule unimplementable, and therefore unimplemented.
	u := user.User{Role: actor.Role}
	if actor.UserID != nil {
		u.ID = *actor.UserID
	}
	check := func(t Ticket, status Status) error { return CanUserUpdate(t, u, status, reopenWindowDays) }
	if authorize != nil {
		check = authorize
	}
	if err := check(t, currentStatus); err != nil {
		return Reply{}, fmt.Errorf("cannot reply to ticket: %w", err)
	}

	// Internal notes are never sent to customers.
	if internal {
		notifyCustomer = false
		reporterEmail = ""
	}

	reply := Reply{
		ID:             uuid.New(),
		TicketID:       ticketID,
		AuthorID:       actor.UserID,
		Body:           body,
		BodyFormat:     format,
		Internal:       internal,
		NotifyCustomer: notifyCustomer,
		CreatedAt:      time.Now(),
	}
	// Auto-reopen: user reply to a Resolved ticket within the window.
	reopened := actor.Role == user.RoleUser && currentStatus.Name == StatusNameResolved
	oldStatusID := t.StatusID
	var target Status
	if reopened {
		// An unresolvable configured status arrives here as uuid.Nil. Left
		// alone it reached the status_id foreign key, rolled the transaction
		// back, and took the user's reply with it — reported as a 500 on a
		// perfectly ordinary reply. Refusing up front at least fails before
		// the write and says what is actually wrong.
		if reopenTargetStatusID == uuid.Nil {
			return Reply{}, fmt.Errorf("no valid reopen target status is configured: %w", ErrValidation)
		}
		// Fetched once, up front: the reopen target can be Pending (any
		// active non-system status), and applyStatusTimestamps needs its
		// Name, not just its ID.
		target, err = s.getStatusByID(ctx, reopenTargetStatusID)
		if err != nil {
			return Reply{}, err
		}
		t.StatusID = reopenTargetStatusID
		t.ResolvedAt = nil
		t.UpdatedAt = time.Now()
	}

	// The reply and, when it triggers one, the reopen commit together. Before
	// this the reply could persist while the reopen failed, leaving the ticket
	// Resolved with a user reply sitting under it.
	// Mark the event for a guest link if and only if the reply will be mailed
	// to the guest. The link itself is created when the mail is sent (#164),
	// by IssueGuestLink under the ticket's row lock.
	//
	// reporterEmail is the address this reply will be mailed to, and it is
	// empty in three cases that all used to rotate anyway: an internal note, a
	// staff reply with notify_customer off, and — worst — the guest's own
	// reply, which passes "" because mailing customers their own words back is
	// noise. Each minted a token that reached nobody and killed the one the
	// guest was holding, so replying, the single thing a guest comes back to
	// do, locked them out of their own ticket.
	//
	// Rotation once ran here on autocommit, a DELETE and an INSERT after the
	// reply had landed, and two concurrent replies interleaved into two live
	// tokens. It moved into this transaction, and since #164 to the send,
	// where it holds the ticket's row lock for the same reason.
	//
	// The honest limit: a send rotates before it knows whether the mail got
	// through, so a failed send leaves the old link dead until the retry
	// succeeds. The outbox retries, and /resend mints another.
	var guestLink bool
	rotateFor := t.GuestEmail != nil && *t.GuestEmail != "" && reporterEmail != ""

	// t, reopened and guestLink are rewritten inside the closure, so each
	// attempt starts from the values decided above and not from what the
	// attempt that was rolled back left in them.
	t0, reopened0 := t, reopened

	if err := s.atomic.InTx(ctx, func(st Store, au audit.Store, ch *notification.Changes) error {
		t, reopened, guestLink = t0, reopened0, false
		// A requester's reply is decided AGAIN here, on the locked row (#349).
		// The check above read the ticket without a lock, and the insert below
		// takes none unless the reply reopens, so a close that committed in
		// between used to land the reply on a Closed ticket — contradicting
		// "Closed is read-only for requesters". The lock also orders this
		// against the close: whichever commits first, the other sees it.
		// Staff are not asked; they may annotate a closed ticket.
		lockedByRequester, err := s.refuseRequesterOnClosed(ctx, st, ticketID, actor)
		if err != nil {
			return fmt.Errorf("cannot reply to ticket: %w", err)
		}
		if err := st.CreateReply(ctx, reply); err != nil {
			return fmt.Errorf("creating reply: %w", err)
		}
		if rotateFor {
			guestLink = hasGuestRecipient(t)
		}
		if reopened {
			// The row to overwrite is the one locked above, for the closed check:
			// the copy outside decided WHETHER to reopen — a decision about the
			// status the reporter replied to — but st.Update writes every column,
			// so the row it writes has to be the locked one, or a concurrent assign
			// or edit is lost. Only a requester reopens, and a requester's reply
			// always locks, so there is exactly one read.
			locked := lockedByRequester
			// Re-decide on the locked row, not just re-read it.
			//
			// The copy above decided to reopen because the ticket was Resolved when
			// the reply arrived. If an administrator closed it in between, that
			// decision is stale: reopening from the unlocked copy wrote the new
			// status while leaving closed_at set, producing an open ticket with a
			// closing timestamp — and a history row claiming it moved from
			// Resolved when it actually left Closed.
			if locked.StatusID != oldStatusID {
				// Someone else moved it. The reply is already written and stands;
				// the reopen does not, because the condition for it is gone.
				reopened = false
				t = locked
			} else {
				before := ticketMap(locked)
				now := time.Now() // under the row lock, like ChangePriority's
				locked.StatusID = reopenTargetStatusID
				locked.UpdatedAt = now
				// Through the shared rule, so this door agrees with the others about
				// resolved_at and closed_at rather than clearing one and forgetting
				// the other.
				applyStatusTimestamps(&locked, oldStatusID, target, s.sys, now)
				firstReopen := markReopened(&locked, now)
				t = locked

				if err := st.Update(ctx, t); err != nil {
					return fmt.Errorf("reopening ticket: %w", err)
				}
				if err := st.CreateStatusHistoryEntry(ctx, statusHistoryEntry(t.ID, &oldStatusID, reopenTargetStatusID, actor)); err != nil {
					return fmt.Errorf("recording reopen: %w", err)
				}
				// An audit entry for the reopen, so /history and /audit agree about
				// whether the ticket was open. (Same kind of entry Service.Reopen
				// writes for a force-reopen of a Closed ticket, which #349 gated behind
				// closed_reopen_policy; this one is the requester's reply reopening a
				// Resolved ticket, the only reopen a requester has.) See #326.
				if err := au.Create(ctx, auditEntry(actor.UserID, "ticket", t.ID, "reopened", before, ticketMap(t))); err != nil {
					return fmt.Errorf("auditing reopen: %w", err)
				}
				// The reopen is part of this reply's event, not an event of its own:
				// the reply that reopens is one change (#506). No recipient of its own
				// either; the reply's decides who is mailed.
				if kinds := s.statusKinds(oldStatusID, reopenTargetStatusID); len(kinds) > 0 {
					moved := raiseOn(t, actor, now)
					moved.Kinds = kinds
					moved.Changes = statusChanges(oldStatusID, reopenTargetStatusID)
					moved.Payload = map[string]any{"new_status_id": reopenTargetStatusID}
					moved.StatusName = target.Name
					ch.Raise(moved)
				}
				if t, err = s.finishReopenInTx(ctx, st, au, ch, t, firstReopen, actor, now); err != nil {
					return err
				}
			}
		}

		// The reply's own part of the event. reporter_email is only populated when
		// notifyCustomer is true; the email dispatcher skips sending when the
		// address is empty.
		//
		// Recipient and TrackingNumber are the same two values again, carried
		// separately from Payload because they are the only ones an email may use.
		// reporterEmail is looked up from the ticket's own row by the caller, and t
		// is a row read from the store — under FOR UPDATE on the reopen path,
		// plainly otherwise. Either way it is the record, not request text, which
		// is the property that matters here.
		//
		// A staff-only note is its own kind, so a webhook subscribed to
		// ticket.replied sees customer-visible replies only (#448). It has no
		// email: reporterEmail is blank for a note.
		replyKind := notification.EventTicketReplied
		if internal {
			replyKind = notification.EventTicketNoteAdded
		}
		ev := raiseOn(t, actor, time.Now())
		ev.Kinds = []notification.EventType{replyKind}
		ev.Recipient = reporterEmail
		ev.GuestLink = guestLink
		// A public reply by the requester is what staff mail is for (#441).
		// The requester is whoever filed the ticket, not whoever holds the
		// user role: a staff member replying on a ticket they filed is the
		// requester replying, and one replying on somebody else's is not. A
		// guest has no account and no id, and is always the requester.
		// An internal note is staff talking to staff.
		//
		// A reply that reopened the ticket is still the requester replying: the
		// change carries both kinds, and staff mail gives each person one mail
		// (#506).
		ev.FromRequester = !internal && isRequester(t, actor)
		ev.Payload = map[string]any{
			"reporter_email": reporterEmail, // used by dispatcher to set To address
			"TrackingNumber": string(t.TrackingNumber),
			"Subject":        t.Subject,
			"internal":       internal,
			"reply_id":       reply.ID,
		}
		// An internal note's body does not go in the event.
		//
		// Blanking reporter_email keeps it out of the customer's email, but
		// the webhook dispatcher marshals this whole event and POSTs it to
		// every subscriber. A credential holding only webhooks:write —
		// refused tickets:read — could register a URL and receive the body
		// of every staff-only note on the instance. Scopes are supposed to
		// narrow, and that one widened.
		//
		// The flag stays so a legitimate subscriber can tell the two apart,
		// which it previously could not.
		if !internal {
			ev.Payload["ReplyBody"] = body
		}
		ch.Raise(ev)
		return nil
	}); err != nil {
		return Reply{}, err
	}

	// Record first staff response for SLA. Deliberately best-effort: the reply
	// is already persisted and this is a metric, not the user's intent, so an
	// SLA outage must not fail a reply that succeeded.
	//
	// !internal: DESIGN.md defines the response target as "first staff
	// reply", and this codebase already distinguishes internal notes from
	// customer-visible replies everywhere else (VisibleReplies, webhook/email
	// suppression of internal-note content). A staff-to-staff note the
	// customer never sees must not freeze the response target as met. See
	// #221.
	if s.sla != nil && actor.Role != user.RoleUser && !internal {
		_ = s.sla.RecordFirstResponse(ctx, t, reply.CreatedAt)
	}

	return reply, nil
}

// isRequester reports whether actor is the person who filed t: its reporter,
// or the guest (no id, user role) the ticket was filed for.
func isRequester(t Ticket, actor Actor) bool {
	if actor.UserID == nil {
		return actor.Role == user.RoleUser
	}
	return t.ReporterUserID != nil && *t.ReporterUserID == *actor.UserID
}

// resolutionNotesMatch reports whether notes is exactly what a ticket already
// has recorded as its resolution notes. A nil stored value (a ticket moved to
// Resolved through a door other than Resolve/ResolveAsDuplicate — UpdateStatus
// never touches ResolutionNotes) never matches, so the first call through this
// door always writes, however coincidentally.
func resolutionNotesMatch(stored *string, notes string) bool {
	return stored != nil && *stored == notes
}

// resolveInTx is the transactional body of Resolve, extracted so ResolveAsDuplicate
// can reuse it. It transitions the ticket to Resolved, records resolution notes,
// and performs all the necessary updates in one transaction.
// Returns the updated ticket; what it did is raised into ch as one event.
//
// The returned bool is true when the call was a true no-op: the ticket was
// already Resolved AND notes (and, via sameTarget, whatever else the caller
// considers part of "what's already stored") match what is already recorded.
// A double-submit (e.g. a stale second browser tab) landing on an
// already-resolved ticket with the SAME notes must not re-run the resolve
// side effects — a second status-history row, a second audit entry, another
// guest-token rotation invalidating the link just emailed, and another
// EventTicketResolved dispatch. Checked under the same FOR UPDATE lock as
// the mutation itself, so two concurrent resolves cannot both pass it.
// Same pattern as close()'s alreadyClosed guard. See #209.
//
// Before #225's fix, this short-circuited on t.StatusID alone, so a
// LEGITIMATE re-resolve with genuinely different notes was silently dropped:
// 200 response, stale notes, no re-recorded resolution. Notes now have to
// match too, or this proceeds as a real re-resolve — updating
// ResolutionNotes and re-running the side effects — while still preserving
// the original resolved_at via applyStatusTimestamps' existing rule.
//
// sameTarget lets ResolveAsDuplicate fold its own "the link already points
// where this call asked" check into the same no-op decision: Resolve has no
// notion of a target and always passes true, so its no-op decision rests on
// notes alone. See #225's scope note on ResolveAsDuplicate.
func (s *Service) resolveInTx(ctx context.Context, st Store, au audit.Store, ch *notification.Changes, ticketID uuid.UUID, notes string, actor Actor, now time.Time, sameTarget bool) (Ticket, bool, error) {
	t, err := st.GetByIDForUpdate(ctx, ticketID)
	if err != nil {
		return Ticket{}, false, err
	}
	// Resolving a Closed ticket is a door out of Closed, which is terminal
	// unless closed_reopen_policy lets this role (#349). Checked on the locked
	// row, before the no-op test: Closed is never "already resolved".
	forcedPolicy := ""
	if t.StatusID == s.sys.closedID {
		p, err := s.forceReopenGate(ctx, actor)
		if err != nil {
			return Ticket{}, false, err
		}
		forcedPolicy = p
	}
	if t.StatusID == s.sys.resolvedID && sameTarget && resolutionNotesMatch(t.ResolutionNotes, notes) {
		return t, true, nil
	}
	before := ticketMap(t)
	oldStatusID := t.StatusID
	t.StatusID = s.sys.resolvedID
	t.ResolutionNotes = &notes
	t.UpdatedAt = now
	// Through the shared rule, not by hand. Setting ResolvedAt directly
	// here was how this door came to disagree with UpdateStatus: it
	// restarted the reopen window on a re-resolve, and resolving a CLOSED
	// ticket left closed_at set on an open ticket, which hides it from the
	// auto-close query forever.
	applyStatusTimestamps(&t, oldStatusID, s.sys.resolved, s.sys, now)

	if err := st.Update(ctx, t); err != nil {
		return Ticket{}, false, fmt.Errorf("resolving ticket: %w", err)
	}
	if err := st.CreateStatusHistoryEntry(ctx, statusHistoryEntry(t.ID, &oldStatusID, s.sys.resolvedID, actor)); err != nil {
		return Ticket{}, false, fmt.Errorf("recording resolution: %w", err)
	}
	after := ticketMap(t)
	if forcedPolicy != "" {
		forcedAudit(after, forcedPolicy)
	}
	if err := au.Create(ctx, auditEntry(actor.UserID, "ticket", t.ID, "resolved", before, after)); err != nil {
		return Ticket{}, false, fmt.Errorf("auditing resolution: %w", err)
	}
	// Rotate: the guest is told, and the link they are told with is the
	// one they need to reopen inside the window.
	ev := raiseOn(t, actor, now)
	ev.Kinds = s.statusKinds(oldStatusID, s.sys.resolvedID)
	if len(ev.Kinds) == 0 {
		// Resolving a ticket that is already Resolved, with new notes: not a
		// status move, but the Resolve button's resolution all the same.
		ev.Kinds = []notification.EventType{notification.EventTicketResolved}
	}
	ev.Changes = statusChanges(oldStatusID, s.sys.resolvedID)
	ev.Payload = map[string]any{"new_status_id": s.sys.resolvedID}
	ev.Recipient = guestRecipient(t)
	ev.GuestLink = hasGuestRecipient(t)
	ev.StatusName = s.sys.resolved.Name
	ch.Raise(ev)
	return t, false, nil
}

// Resolve transitions a ticket to Resolved and records resolution notes.
func (s *Service) Resolve(ctx context.Context, ticketID uuid.UUID, notes string, actor Actor) (Ticket, error) {
	if err := CanTransitionStatus(s.sys.resolved, actor.Role); err != nil {
		return Ticket{}, fmt.Errorf("cannot resolve ticket: %w", err)
	}
	var t Ticket
	var alreadyResolved bool
	now := time.Now()
	if err := s.atomic.InTx(ctx, func(st Store, au audit.Store, ch *notification.Changes) error {
		var err error
		// Resolve has no target of its own, so its no-op decision rests on
		// notes alone — sameTarget is unconditionally true.
		t, alreadyResolved, err = s.resolveInTx(ctx, st, au, ch, ticketID, notes, actor, now, true)
		return err
	}); err != nil {
		return Ticket{}, err
	}

	// True double-submit (e.g. a stale second tab): identical notes against
	// an already-resolved ticket, so the resolve side effects already
	// happened once. See #209 and #225.
	if alreadyResolved {
		return t, nil
	}

	// After the commit: an SLA record stamped for a resolution that then rolled
	// back would be worse than a missing one.
	// Non-fatal for the same reason AttachPolicy is — SLA reporting must not
	// fail the resolution itself.
	if s.sla != nil {
		// See UpdateStatus: non-fatal, and reported by the boundary wrapper in
		// cmd/server rather than logged from the domain.
		//
		// #234: resolutionInstant, not a bare now — see the comment on the
		// equivalent call in UpdateStatus. alreadyResolved above already
		// returned early for a true double-submit; a re-resolve that reaches
		// here (different notes, per #225) still resolved at t.ResolvedAt's
		// original instant, not this call's now.
		_ = s.sla.RecordResolved(ctx, t, resolutionInstant(t, now))
	}

	return t, nil
}

// ResolveAsDuplicate creates a duplicate_of link and resolves the source ticket
// in one atomic transaction. It combines the link creation with the resolution,
// ensuring both succeed or both fail together.
func (s *Service) ResolveAsDuplicate(ctx context.Context, sourceID, targetID uuid.UUID, notes string, actor Actor) (Ticket, error) {
	if err := CanTransitionStatus(s.sys.resolved, actor.Role); err != nil {
		return Ticket{}, fmt.Errorf("cannot resolve ticket: %w", err)
	}
	if sourceID == targetID {
		return Ticket{}, fmt.Errorf("cannot resolve as duplicate: %w", ErrSelfLink)
	}

	var t Ticket
	var alreadyResolved bool
	var alreadyLinked bool
	now := time.Now()

	if err := s.atomic.InTx(ctx, func(st Store, au audit.Store, ch *notification.Changes) error {
		// Lock both ticket rows FOR UPDATE up front, always in the same order
		// regardless of which is source and which is target.
		//
		// Without this, two opposite-direction calls on the same pair (A
		// dup-of B, and concurrently B dup-of A) deadlock (#196): CreateLink's
		// FK check takes a FOR KEY SHARE lock on both referenced rows, and
		// resolveInTx's GetByIDForUpdate later takes FOR UPDATE on the source.
		// Call 1 ends up holding KEY SHARE on {A,B} and waiting for FOR UPDATE
		// on A, while call 2 holds KEY SHARE on {B,A} and waits for FOR
		// UPDATE on B — a cycle Postgres has to abort with 40P01. Taking a
		// FOR UPDATE lock on both rows here first, in a fixed order (the
		// lexicographically smaller id first), means both calls queue behind
		// the same single row instead of forming a cycle: whichever call gets
		// there first holds both locks by the time it reaches CreateLink and
		// resolveInTx, and the second call simply waits for the whole first
		// transaction to finish.
		first, second := sourceID, targetID
		if bytes.Compare(second[:], first[:]) < 0 {
			first, second = second, first
		}
		lockedFirst, err := st.GetByIDForUpdate(ctx, first)
		if err != nil {
			return err
		}
		lockedSecond, err := st.GetByIDForUpdate(ctx, second)
		if err != nil {
			return err
		}
		target := lockedFirst
		if target.ID != targetID {
			target = lockedSecond
		}

		// Apply default notes if empty.
		resolution := notes
		if strings.TrimSpace(resolution) == "" {
			resolution = DuplicateResolutionNotes(target.TrackingNumber)
		}

		// Check for the exact link before inserting, rather than attempting the
		// insert and catching ErrLinkAlreadyExists: Postgres marks the whole
		// transaction aborted the instant one statement fails a constraint, so
		// catching that error in Go and carrying on does not work here — every
		// statement after it (resolveInTx's own reads and writes) would fail
		// with "current transaction is aborted" (25P02), turning the intended
		// 200 into a 500. A plain existence check has no such cost, and it is
		// race-safe here specifically because the FOR UPDATE locks taken above
		// already cover both sourceID and targetID: CreateLink's FK check
		// requires a FOR KEY SHARE lock on each referenced row, which
		// conflicts with the FOR UPDATE this transaction already holds, so no
		// concurrent link insert on this exact pair can land between this
		// check and this transaction's commit.
		// Captured, so decided afresh on every attempt: a retry must not
		// inherit "found" from an attempt that was rolled back.
		alreadyLinked = false
		existingLinks, err := st.ListLinks(ctx, sourceID)
		if err != nil {
			return fmt.Errorf("checking for an existing duplicate link: %w", err)
		}
		for _, l := range existingLinks {
			if l.TargetTicketID == targetID && l.LinkType == LinkDuplicateOf {
				alreadyLinked = true
				break
			}
		}
		if !alreadyLinked {
			// The identical duplicate_of link not existing yet is the normal
			// case; when it does, it is satisfied, not a conflict (#194).
			// Staff may have linked the two tickets earlier without checking
			// "resolve as duplicate", and later want to resolve — aborting
			// the whole transaction over a link that already says exactly
			// what this call asked for left the ticket stuck unresolved for
			// no reason. A same-pair link of a DIFFERENT type does not match
			// this check, so it is created normally alongside the new one,
			// with no special-casing needed.
			link := TicketLink{SourceTicketID: sourceID, TargetTicketID: targetID, LinkType: LinkDuplicateOf}
			if err := st.CreateLink(ctx, link); err != nil {
				return fmt.Errorf("creating duplicate link: %w", err)
			}
			// #229: announced only when a link was made — AddLink (or an
			// earlier ResolveAsDuplicate call) already announced the link that
			// existed, and announcing it again on every repeat call, including
			// a genuine double-submit, reported the same link as freshly made
			// every time. On the source ticket, in the same event as the
			// resolution below: one change, one event (#506).
			source := lockedFirst
			if source.ID != sourceID {
				source = lockedSecond
			}
			linked := raiseOn(source, actor, now)
			linked.Kinds = []notification.EventType{notification.EventTicketLinked}
			linked.Payload = map[string]any{"target_id": targetID, "link_type": LinkDuplicateOf}
			ch.Raise(linked)
		}

		// Resolve the source ticket. Link creation above is already
		// idempotent-satisfied (#194) regardless of the ticket's resolved
		// state, so it always runs; resolveInTx itself is the one that skips
		// the resolve side effects on a double-submit. See #209.
		//
		// sameTarget: alreadyLinked. #225's narrower no-op guard requires the
		// TARGET to match what's already stored, not just the notes text —
		// two custom-notes calls that happen to use identical wording but
		// name a DIFFERENT target must not be treated as a no-op merely
		// because the strings match; alreadyLinked (a new link was NOT just
		// created) is exactly "this call's target is the one already on
		// record."
		var txErr error
		t, alreadyResolved, txErr = s.resolveInTx(ctx, st, au, ch, sourceID, resolution, actor, now, alreadyLinked)
		return txErr
	}); err != nil {
		return Ticket{}, err
	}

	// True double-submit (e.g. a stale second tab): identical notes against
	// the same already-linked target, so the resolve side effects already
	// happened once. See #209 and #225.
	if alreadyResolved {
		return t, nil
	}

	// After the commit, record SLA, as Resolve does.
	//
	// #234: resolutionInstant, not a bare now — same reasoning as Resolve and
	// UpdateStatus.
	if s.sla != nil {
		_ = s.sla.RecordResolved(ctx, t, resolutionInstant(t, now))
	}

	return t, nil
}

// Close transitions a ticket to Closed. Used by the auto-close scheduler and
// admin overrides. It does NOT call CanTransitionStatus — the caller decides
// whether this is authorised.
//
// It deliberately does NOT consult CanTransitionStatus: the auto-close
// scheduler has no actor, and the authorisation decision belongs to the
// caller. That is a recorded architecture decision and is pinned by
// TestClose_BypassesTransitionRules.
//
// actor is used for attribution only — the status-history row and the audit
// entry — and never for authorisation, so the recorded decision is unchanged.
// The scheduler passes SystemActor; handleCloseTicket passes the administrator
// who pressed the button. Previously SystemActor was hardcoded, so a manual
// close showed as "System" in the timeline and wrote no audit entry at all,
// while DESIGN.md requires history to name whoever made the change.
func (s *Service) Close(ctx context.Context, ticketID uuid.UUID, actor Actor) error {
	_, err := s.close(ctx, ticketID, actor, nil)
	return err
}

// AutoClose closes every ticket whose Resolved state has outlived the reopen
// window, attributed to SystemActor. One page per call; rows that close or
// are reopened both fall out of the query, so the next call continues.
// Returns the number closed and the joined per-ticket errors; a failure on
// one ticket does not stop the others.
func (s *Service) AutoClose(ctx context.Context, reopenWindowDays, limit int) (int, error) {
	cutoff := time.Now().AddDate(0, 0, -reopenWindowDays)
	candidates, err := s.store.ListResolvedBefore(ctx, cutoff, s.sys.resolvedID, limit)
	if err != nil {
		return 0, fmt.Errorf("listing tickets to auto-close: %w", err)
	}
	stillEligible := func(t Ticket) bool {
		return t.StatusID == s.sys.resolvedID && t.ResolvedAt != nil && t.ResolvedAt.Before(cutoff)
	}
	var closed int
	var errs []error
	for _, c := range candidates {
		n, err := s.close(ctx, c.ID, SystemActor, stillEligible)
		if err != nil {
			errs = append(errs, fmt.Errorf("auto-closing %s: %w", c.TrackingNumber, err))
			continue
		}
		closed += n
	}
	return closed, errors.Join(errs...)
}

// close is Close's body. eligible, when non-nil, is re-evaluated on the row
// read under FOR UPDATE; a ticket that no longer qualifies is left untouched:
// no write, no history row, no notification. Returns (0, error) if skipped
// or already closed, (1, error) if successfully closed.
func (s *Service) close(ctx context.Context, ticketID uuid.UUID, actor Actor, eligible func(Ticket) bool) (int, error) {
	var t Ticket
	var closed int
	alreadyClosed := false
	skipped := false
	now := time.Now()

	if err := s.atomic.InTx(ctx, func(st Store, au audit.Store, ch *notification.Changes) error {
		// Captured, so decided afresh on every attempt: a retry must not
		// inherit a verdict from an attempt that was rolled back.
		closed, alreadyClosed, skipped = 0, false, false
		var err error
		t, err = st.GetByIDForUpdate(ctx, ticketID)
		if err != nil {
			return err
		}
		if eligible != nil && !eligible(t) {
			// Listed as a candidate, then moved by someone else before the lock was
			// taken. The row that decided this is the row being written, so the
			// decision holds. Same skip path as alreadyClosed: no write, no dispatch.
			skipped = true
			return nil
		}
		if t.StatusID == s.sys.closedID {
			// Already closed. Without this, re-closing appends a duplicate
			// Closed→Closed history row and re-fires the notification.
			// Checked under the lock so two concurrent closes cannot both
			// pass it.
			alreadyClosed = true
			return nil
		}
		before := ticketMap(t)
		oldStatusID := t.StatusID
		t.StatusID = s.sys.closedID
		t.UpdatedAt = now
		// Through the shared rule rather than by hand, so closing a Pending
		// ticket closes its SLA pause interval too.
		applyStatusTimestamps(&t, oldStatusID, s.sys.closed, s.sys, now)

		if err := st.Update(ctx, t); err != nil {
			return fmt.Errorf("closing ticket: %w", err)
		}
		if err := st.CreateStatusHistoryEntry(ctx, statusHistoryEntry(t.ID, &oldStatusID, s.sys.closedID, actor)); err != nil {
			return fmt.Errorf("recording close: %w", err)
		}
		// Every other lifecycle operation audits; Close did not, so a manual
		// close left no trace in the audit log.
		if err := au.Create(ctx, auditEntry(actor.UserID, "ticket", t.ID, "closed", before, ticketMap(t))); err != nil {
			return fmt.Errorf("auditing close: %w", err)
		}
		// No recipient: closing tells nobody, whichever door closed it.
		ev := raiseOn(t, actor, now)
		ev.Kinds = s.statusKinds(oldStatusID, s.sys.closedID) // never empty: an already-Closed ticket returned above
		ev.Changes = statusChanges(oldStatusID, s.sys.closedID)
		ev.Payload = map[string]any{"new_status_id": s.sys.closedID}
		ev.StatusName = s.sys.closed.Name
		ch.Raise(ev)
		// Neither rotate nor revoke (#349). A closed ticket accepts nothing
		// from its requester, but they may still READ it: the link they were
		// last sent keeps working until it expires (GuestTokenTTL), which is
		// what bounds how long an archive stays open to a bearer credential.
		// Revoking here is what made a guest unable to read the answer on a
		// ticket closed straight after it.
		closed = 1
		return nil
	}); err != nil {
		return 0, err
	}

	// Skip and re-close announce nothing, same as before.
	if skipped || alreadyClosed {
		return 0, nil
	}

	// A ticket can reach Closed without ever passing through Resolved (an
	// admin moving it straight from an open status, or the reopen window
	// simply never being used). Without this, sla_records.resolved_at stays
	// NULL forever: the sweep's closed_at IS NULL guard then excludes it from
	// ever being evaluated again, but the indicator has no MetAt to freeze
	// against, so it keeps computing a live, ever-growing Elapsed(t, now)
	// against a ticket that will never move again. RecordResolved is a no-op
	// when a resolution is already recorded (the ordinary Resolved-then-Closed
	// path), so this only ever does anything for the case it exists to fix.
	// Non-fatal and after the commit, matching Resolve/UpdateStatus. See #220.
	//
	// #227: resolutionInstant, not a bare now — see the comment on the
	// equivalent call in UpdateStatus.
	if s.sla != nil {
		_ = s.sla.RecordResolved(ctx, t, resolutionInstant(t, now))
	}
	return closed, nil
}

// Reopen force-reopens a Closed ticket into targetStatusID. Staff and admin
// only, and only when closed_reopen_policy lets the actor's role: Closed is
// terminal by default (#349), a requester can never (ErrForbidden), and a
// refusal by the setting is a *ReopenRefusedError (an ErrClosed) that says why.
//
// Restored from before #349 with the policy in front of it and nothing else
// changed: the same target status, history row, reopen notification and
// guest link issued at send time, and the shared timestamp rule that carries
// the SLA pause forward. What is new is that the audit entry says it was
// forced and under which policy. The guest's links are not touched here —
// closing no longer revokes them — and the ticket is writable through them again
// as soon as it is not Closed.
func (s *Service) Reopen(ctx context.Context, ticketID uuid.UUID, targetStatusID uuid.UUID, actor Actor) (Ticket, error) {
	if actor.Role == user.RoleUser {
		return Ticket{}, ErrForbidden
	}
	t, err := s.store.GetByID(ctx, ticketID)
	if err != nil {
		return Ticket{}, err
	}
	if t.StatusID != s.sys.closedID {
		return Ticket{}, fmt.Errorf("%w: ticket is not closed", ErrNotClosed)
	}
	// Same guard as AddReply's auto-reopen: an unresolvable configured status
	// arrives as uuid.Nil and would otherwise fail the status_id foreign key
	// mid-transaction.
	if targetStatusID == uuid.Nil {
		return Ticket{}, fmt.Errorf("no valid reopen target status is configured: %w", ErrValidation)
	}
	target, err := s.getStatusByID(ctx, targetStatusID)
	if err != nil {
		return Ticket{}, err
	}

	var now time.Time // taken under the row lock, like ChangePriority's
	var oldStatusID uuid.UUID

	if err := s.atomic.InTx(ctx, func(st Store, au audit.Store, ch *notification.Changes) error {
		// Re-read under the lock. The check above answered the precondition on
		// an unlocked copy; this is the row actually being overwritten, and a
		// concurrent close or resolve may have landed in between.
		t, err = st.GetByIDForUpdate(ctx, ticketID)
		if err != nil {
			return err
		}
		now = time.Now()
		if t.StatusID != s.sys.closedID {
			return fmt.Errorf("%w: ticket is not closed", ErrNotClosed)
		}
		// The policy, read now that the row is locked: a setting flipped to
		// off since the request began is the one that applies.
		policy, err := s.forceReopenGate(ctx, actor)
		if err != nil {
			return err
		}
		before := ticketMap(t)
		oldStatusID = t.StatusID
		t.StatusID = targetStatusID
		t.UpdatedAt = now
		// Through the shared rule: its default arm clears ClosedAt/ResolvedAt,
		// carries the accumulated SLA pause forward and opens a fresh interval
		// if the reopen target is Pending. The SLA clock itself restarts below
		// (finishReopenInTx).
		applyStatusTimestamps(&t, oldStatusID, target, s.sys, now)
		firstReopen := markReopened(&t, now)

		if err := st.Update(ctx, t); err != nil {
			return fmt.Errorf("reopening ticket: %w", err)
		}
		if err := st.CreateStatusHistoryEntry(ctx, statusHistoryEntry(t.ID, &oldStatusID, targetStatusID, actor)); err != nil {
			return fmt.Errorf("recording reopen: %w", err)
		}
		if err := au.Create(ctx, auditEntry(actor.UserID, "ticket", t.ID, "reopened", before, forcedAudit(ticketMap(t), policy))); err != nil {
			return fmt.Errorf("auditing reopen: %w", err)
		}
		// The guest is told, with a link issued when the mail is sent.
		if kinds := s.statusKinds(oldStatusID, targetStatusID); len(kinds) > 0 {
			ev := raiseOn(t, actor, now)
			ev.Kinds = kinds
			ev.Changes = statusChanges(oldStatusID, targetStatusID)
			ev.Payload = map[string]any{"new_status_id": targetStatusID}
			ev.Recipient = guestRecipient(t)
			ev.GuestLink = hasGuestRecipient(t)
			ev.StatusName = target.Name
			ch.Raise(ev)
		}
		// After the status is raised, so the priority it may raise is the
		// ticket's last word.
		if t, err = s.finishReopenInTx(ctx, st, au, ch, t, firstReopen, actor, now); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return Ticket{}, err
	}

	return t, nil
}

// statusHistoryEntry builds the history row for a transition.
func statusHistoryEntry(ticketID uuid.UUID, fromStatusID *uuid.UUID, toStatusID uuid.UUID, actor Actor) StatusHistoryEntry {
	return StatusHistoryEntry{
		ID:              uuid.New(),
		TicketID:        ticketID,
		FromStatusID:    fromStatusID,
		ToStatusID:      toStatusID,
		ChangedByUserID: actor.UserID,
		CreatedAt:       time.Now(),
	}
}

// ListStatusHistory returns the status transition history for a ticket.
func (s *Service) ListStatusHistory(ctx context.Context, ticketID uuid.UUID) ([]StatusHistoryEntry, error) {
	return s.store.ListStatusHistory(ctx, ticketID)
}

// ticketAuditCap bounds a single ticket's feed. A ticket's lifecycle produces
// a handful of entries; this is a safety net against an unbounded query, not
// a limit anyone is expected to hit. The admin-wide audit view (#129) needs
// real pagination; one ticket does not.
const ticketAuditCap = 500

// AddLink creates a directed link between two tickets.
func (s *Service) AddLink(ctx context.Context, sourceID, targetID uuid.UUID, lt LinkType, actor Actor) error {
	if sourceID == targetID {
		return fmt.Errorf("cannot add link: %w", ErrSelfLink)
	}
	// Validate LinkType before attempting to write to the database.
	if !lt.Valid() {
		return fmt.Errorf("%q: %w", lt, ErrInvalidLinkType)
	}

	if err := s.atomic.InTx(ctx, func(st Store, _ audit.Store, ch *notification.Changes) error {
		// Lock both ticket rows FOR UPDATE up front, in the same fixed order
		// (lexicographically smaller id first) ResolveAsDuplicate uses, before
		// the link insert. Without this, a plain AddLink relies only on
		// CreateLink's FK-triggered FOR KEY SHARE lock, which does not
		// participate in that ordering discipline — a concurrent
		// ResolveAsDuplicate on the same ticket pair, holding its ordered FOR
		// UPDATE locks, can still deadlock against it (40P01, surfacing as an
		// unhandled 500). See #210.
		first, second := sourceID, targetID
		if bytes.Compare(second[:], first[:]) < 0 {
			first, second = second, first
		}
		lockedFirst, err := st.GetByIDForUpdate(ctx, first)
		if err != nil {
			return err
		}
		lockedSecond, err := st.GetByIDForUpdate(ctx, second)
		if err != nil {
			return err
		}
		source, target := lockedFirst, lockedSecond
		if source.ID != sourceID {
			source, target = lockedSecond, lockedFirst
		}
		// A link is written onto both tickets' threads, so a requester may
		// not add one to or from a Closed ticket (#349). Staff may: linking a
		// closed ticket is how a follow-up is tied to it.
		if actor.Role == user.RoleUser && (source.StatusID == s.sys.closedID || target.StatusID == s.sys.closedID) {
			return ErrClosed
		}
		link := TicketLink{SourceTicketID: sourceID, TargetTicketID: targetID, LinkType: lt}
		if err := st.CreateLink(ctx, link); err != nil {
			return fmt.Errorf("creating link: %w", err)
		}
		ev := raiseOn(source, actor, time.Now())
		ev.Kinds = []notification.EventType{notification.EventTicketLinked}
		ev.Payload = map[string]any{"target_id": targetID, "link_type": lt}
		ch.Raise(ev)
		return nil
	}); err != nil {
		return err
	}
	return nil
}

// RemoveLink deletes a directed link between two tickets.
func (s *Service) RemoveLink(ctx context.Context, sourceID, targetID uuid.UUID, lt LinkType) error {
	return s.store.DeleteLink(ctx, sourceID, targetID, lt)
}

// GetByID returns the ticket with the given ID.
func (s *Service) GetByID(ctx context.Context, id uuid.UUID) (Ticket, error) {
	return s.store.GetByID(ctx, id)
}

// refuseArchivedPick is ErrValidation when a classification being picked
// includes an archived category, type or item. It is the one place the rule
// lives, so REST, MCP and the guest form all obey it, and it holds for every
// role: a retired node is out of circulation for staff and administrators too
// (#421).
//
// current is the ticket's existing classification when this is a reclassify,
// nil for a new ticket. A level that is unchanged is not a new pick and is not
// checked, so a ticket already sitting in an archived node keeps it through any
// edit that leaves that level alone.
func (s *Service) refuseArchivedPick(ctx context.Context, categoryID uuid.UUID, typeID, itemID *uuid.UUID, current *Ticket) error {
	var cat *uuid.UUID
	if current == nil || current.CategoryID != categoryID {
		cat = &categoryID
	}
	if current != nil {
		if sameID(typeID, current.TypeID) {
			typeID = nil
		}
		if sameID(itemID, current.ItemID) {
			itemID = nil
		}
	}
	level, err := s.store.ArchivedCTINode(ctx, cat, typeID, itemID)
	if err != nil {
		return err
	}
	if level != "" {
		return fmt.Errorf("%w: %s_id is not an active %s", ErrValidation, level, level)
	}
	return nil
}

func sameID(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// UpdateCTI changes the category/type/item classification of a ticket.
// Only staff and admin may call this; enforcement is at the handler layer.
// Moving INTO an archived node is refused for them too (refuseArchivedPick).
//
// actor is who reclassified it, for the event (#506): a reclassification
// announced nothing before, and now raises ticket.classified.
func (s *Service) UpdateCTI(ctx context.Context, id, categoryID uuid.UUID, typeID, itemID *uuid.UUID, actor Actor) (Ticket, error) {
	c := Classification{CategoryID: categoryID, TypeID: typeID, ItemID: itemID}
	if err := s.ValidateCTI(ctx, id, categoryID, typeID, itemID); err != nil {
		return Ticket{}, err
	}
	var out Ticket
	if err := s.atomic.InTx(ctx, func(st Store, _ audit.Store, ch *notification.Changes) error {
		var err error
		out, err = s.classifyInTx(ctx, st, ch, id, c, actor)
		return err
	}); err != nil {
		return Ticket{}, err
	}
	return out, nil
}

// classifyInTx is UpdateCTI's write for a caller that already has a
// transaction. No audit entry: reclassifying never wrote one, and adding one is
// a separate decision (#506 → Out of scope).
func (s *Service) classifyInTx(ctx context.Context, st Store, ch *notification.Changes,
	id uuid.UUID, c Classification, actor Actor) (Ticket, error) {
	t, err := st.GetByIDForUpdate(ctx, id)
	if err != nil {
		return Ticket{}, err
	}
	if err := st.UpdateCTI(ctx, id, c.CategoryID, c.TypeID, c.ItemID); err != nil {
		return Ticket{}, fmt.Errorf("updating ticket CTI: %w", err)
	}
	was := t
	t.CategoryID, t.TypeID, t.ItemID = c.CategoryID, c.TypeID, c.ItemID
	ev := raiseOn(t, actor, time.Now())
	ev.Kinds = []notification.EventType{notification.EventTicketClassified}
	ev.Changes = append(append(
		[]notification.Change(nil),
		optIDChange("category_id", &was.CategoryID, &t.CategoryID)...),
		append(optIDChange("type_id", was.TypeID, t.TypeID), optIDChange("item_id", was.ItemID, t.ItemID)...)...)
	// A reclassification that changed nothing is not a change to announce.
	if len(ev.Changes) > 0 {
		ch.Raise(ev)
	}
	return t, nil
}

// ValidateCTI runs every check UpdateCTI makes, without writing. A caller that
// applies other changes in the same request (the PATCH handler) calls it first,
// so a refused classification leaves the whole request unapplied rather than
// half done.
func (s *Service) ValidateCTI(ctx context.Context, id, categoryID uuid.UUID, typeID, itemID *uuid.UUID) error {
	// The same checks Create makes, because reclassifying is creating a
	// classification.
	//
	// They were on the create path only, so the pairing that path refuses was
	// one PATCH away: a category from one tree with an item from another
	// stored happily, and an item with no type at all. Group routing keys on
	// this triple, so a ticket could be routed on a pairing that does not
	// exist. An unknown id answered 500 from the foreign key rather than
	// saying which id was wrong.
	if ok, err := s.store.CategoryExists(ctx, categoryID); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("%w: category_id is not a category on this help desk", ErrValidation)
	}
	if ok, err := s.store.CTIIsCoherent(ctx, categoryID, typeID, itemID); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf(
			"%w: type_id and item_id must belong to the category and to each other", ErrValidation)
	}
	current, err := s.store.GetByID(ctx, id)
	if err != nil {
		return err
	}
	return s.refuseArchivedPick(ctx, categoryID, typeID, itemID, &current)
}

// GetByTrackingNumber returns the ticket with the given tracking number.
func (s *Service) GetByTrackingNumber(ctx context.Context, tn TrackingNumber) (Ticket, error) {
	return s.store.GetByTrackingNumber(ctx, tn)
}

// ListReplies returns all replies for a ticket.
func (s *Service) ListReplies(ctx context.Context, ticketID uuid.UUID) ([]Reply, error) {
	return s.store.ListReplies(ctx, ticketID)
}

// ListLinks returns all links for a ticket.
func (s *Service) ListLinks(ctx context.Context, ticketID uuid.UUID) ([]TicketLink, error) {
	return s.store.ListLinks(ctx, ticketID)
}

// ListByReporter returns tickets submitted by the given user.
func (s *Service) ListByReporter(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Ticket, error) {
	return s.store.ListByReporter(ctx, userID, limit, offset)
}

// ListByAssigneeUser returns tickets assigned to the given user.
func (s *Service) ListByAssigneeUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Ticket, error) {
	return s.store.ListByAssigneeUser(ctx, userID, limit, offset)
}

// ListByAssigneeGroup returns tickets assigned to the given group.
func (s *Service) ListByAssigneeGroup(ctx context.Context, groupID uuid.UUID, limit, offset int) ([]Ticket, error) {
	return s.store.ListByAssigneeGroup(ctx, groupID, limit, offset)
}

// SearchByReporter filters the reporter's tickets by tracking number, subject, or description.
func (s *Service) SearchByReporter(ctx context.Context, userID uuid.UUID, q string, limit, offset int) ([]Ticket, error) {
	return s.store.SearchByReporter(ctx, userID, q, limit, offset)
}

// SearchByAssigneeUser filters tickets assigned to the user by tracking number, subject, or description.
func (s *Service) SearchByAssigneeUser(ctx context.Context, userID uuid.UUID, q string, limit, offset int) ([]Ticket, error) {
	return s.store.SearchByAssigneeUser(ctx, userID, q, limit, offset)
}

// SearchByAssigneeGroup filters tickets assigned to the group by tracking number, subject, or description.
func (s *Service) SearchByAssigneeGroup(ctx context.Context, groupID uuid.UUID, q string, limit, offset int) ([]Ticket, error) {
	return s.store.SearchByAssigneeGroup(ctx, groupID, q, limit, offset)
}

// ListAll returns every ticket, newest first. Intended for admin-scope views.
func (s *Service) ListAll(ctx context.Context, limit, offset int) ([]Ticket, error) {
	return s.store.ListAll(ctx, limit, offset)
}

// SearchAll filters every ticket by tracking number, subject, or description.
func (s *Service) SearchAll(ctx context.Context, q string, limit, offset int) ([]Ticket, error) {
	return s.store.SearchAll(ctx, q, limit, offset)
}

// ListUnassigned returns tickets with neither an assignee user nor an assignee group.
func (s *Service) ListUnassigned(ctx context.Context, limit, offset int) ([]Ticket, error) {
	return s.store.ListUnassigned(ctx, limit, offset)
}

// SearchUnassigned filters unassigned tickets by tracking number, subject, or description.
func (s *Service) SearchUnassigned(ctx context.Context, q string, limit, offset int) ([]Ticket, error) {
	return s.store.SearchUnassigned(ctx, q, limit, offset)
}

// ListResolvedBefore is used by the auto-close scheduler.
func (s *Service) ListResolvedBefore(ctx context.Context, before time.Time, limit int) ([]Ticket, error) {
	return s.store.ListResolvedBefore(ctx, before, s.sys.resolvedID, limit)
}

// ListStatuses returns all configured statuses.
func (s *Service) ListStatuses(ctx context.Context) ([]Status, error) {
	return s.statuses.ListStatuses(ctx)
}

// AddStatus creates a new custom status entry. The name is checked for
// emptiness here (NOT NULL alone doesn't reject "") and for uniqueness at the
// store, which maps the statuses_name_key violation to ErrStatusNameTaken
// (#278) rather than letting the raw pgconn error reach handleError
// unrecognized.
//
// Returns the saved Status — with Name trimmed and Active set — rather than
// leaving the caller's own pre-call copy to be echoed back. handleCreateStatus
// used to serialize its own copy, whose Name was never trimmed: a name with
// trailing whitespace came back untrimmed in the 201 body while the stored
// (and trimmed) row disagreed with it (#285). Mirrors
// category.Service.CreateCategory, which returns the saved Category for the
// same reason.
func (s *Service) AddStatus(ctx context.Context, st Status) (Status, error) {
	st.Name = strings.TrimSpace(st.Name)
	if st.Name == "" {
		return Status{}, ErrInvalidStatusName
	}
	st.Active = true
	if err := s.statuses.CreateStatus(ctx, st); err != nil {
		return Status{}, err
	}
	return st, nil
}

// SaveStatus persists changes to an existing status record. Renaming a
// system status is refused here, and only here (#269 removed the HTTP
// handler's own inline rename check, so handleUpdateStatus now relies
// entirely on this refusal): system statuses are found by name at startup
// (LoadSystemStatuses) and compared by name in lifecycle rules, so a rename
// that reached the store would reintroduce the restart-crash hazard #263
// closed. Mirrors RemoveStatus's own system-status refusal below. The
// refusal wraps ErrSystemStatusImmutable (#269) so handleError can map it to
// a clean 403 rather than a bare 500 for any caller, HTTP or otherwise, that
// reaches this method.
//
// Returns the saved Status — with Name trimmed — rather than leaving the
// caller's own pre-call copy to be echoed back. handleUpdateStatus used to
// serialize its own copy, whose Name was never trimmed, the same #285
// mismatch AddStatus had. Mirrors AddStatus's own return above.
func (s *Service) SaveStatus(ctx context.Context, st Status) (Status, error) {
	st.Name = strings.TrimSpace(st.Name)
	if st.Name == "" {
		return Status{}, ErrInvalidStatusName
	}
	current, err := s.getStatusByID(ctx, st.ID)
	if err != nil {
		return Status{}, err
	}
	if current.Kind == StatusKindSystem && st.Name != current.Name {
		return Status{}, fmt.Errorf("system status %q cannot be renamed: %w", current.Name, ErrSystemStatusImmutable)
	}
	if err := s.statuses.UpdateStatus(ctx, st); err != nil {
		return Status{}, err
	}
	return st, nil
}

// CountByStatus returns the number of tickets currently in the given status.
func (s *Service) CountByStatus(ctx context.Context, id uuid.UUID) (int64, error) {
	return s.statuses.CountByStatus(ctx, id)
}

// CountByStatusForReporter counts tickets in the given status reported by a user.
func (s *Service) CountByStatusForReporter(ctx context.Context, statusID, userID uuid.UUID) (int64, error) {
	return s.statuses.CountByStatusForReporter(ctx, statusID, userID)
}

// CountByStatusForAssignee counts tickets in the given status assigned to a user
// or to any of the supplied groups.
func (s *Service) CountByStatusForAssignee(ctx context.Context, statusID, userID uuid.UUID, groupIDs []uuid.UUID) (int64, error) {
	return s.statuses.CountByStatusForAssignee(ctx, statusID, userID, groupIDs)
}

// RemoveStatus hard-deletes a custom status. Blocked if the status is a
// system status or if any tickets currently have this status. The
// system-status refusal wraps ErrSystemStatusImmutable (#269) for the same
// reason SaveStatus's does: a bare fmt.Errorf here maps to a bare 500 for
// any caller, and DELETE on a system status is reachable via the HTTP
// handler with no inline guard of its own. The two in-use refusals below
// wrap ErrStatusInUse for the same reason (#275): both were still bare
// fmt.Errorf as of review round 4, so the "deactivate it instead" guidance
// they carry never reached the caller — handleError had no case for either
// and both fell through to a 500. The counts and the final DeleteStatus below
// are separate statements, so a ticket can be PATCHed into this status (or
// transitioned through it) between the counts and the delete; that race is
// backstopped at the store layer (ticketstore.Store.DeleteStatus), which maps
// the resulting foreign-key violation to this same ErrStatusInUse rather than
// letting it surface as a 500 — the identical shape slastore.DeletePolicy
// established for the same count-then-delete race (#261), added here for
// #279.
func (s *Service) RemoveStatus(ctx context.Context, id uuid.UUID) error {
	st, err := s.getStatusByID(ctx, id)
	if err != nil {
		return err
	}
	if st.Kind != StatusKindCustom {
		return fmt.Errorf("system status %q cannot be deleted: %w", st.Name, ErrSystemStatusImmutable)
	}
	count, err := s.statuses.CountByStatus(ctx, id)
	if err != nil {
		return fmt.Errorf("counting tickets for status: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("status %q has %d ticket(s); deactivate it instead of deleting: %w", st.Name, count, ErrStatusInUse)
	}
	// Zero current tickets is not enough: ticket_status_history references
	// statuses with no ON DELETE action, so any past transition through this
	// status makes the DELETE fail on a foreign key. That surfaced as a raw
	// 500 and contradicted the "deactivate instead" guidance above, which
	// implies a zero-count status is deletable.
	histCount, err := s.statuses.CountStatusHistoryByStatus(ctx, id)
	if err != nil {
		return fmt.Errorf("counting status history: %w", err)
	}
	if histCount > 0 {
		return fmt.Errorf("status %q appears in %d past ticket transition(s) and cannot be deleted; deactivate it instead: %w", st.Name, histCount, ErrStatusInUse)
	}
	return s.statuses.DeleteStatus(ctx, id)
}

// getStatusByID fetches a status; returns a descriptive error on miss. The
// miss wraps ErrStatusNotFound (#273) so handleError maps it to a 404
// instead of falling through to a 500 for callers that don't do their own
// existence check first — handleDeleteStatus is the one that doesn't.
func (s *Service) getStatusByID(ctx context.Context, id uuid.UUID) (Status, error) {
	statuses, err := s.statuses.ListStatuses(ctx)
	if err != nil {
		return Status{}, fmt.Errorf("listing statuses: %w", err)
	}
	for _, st := range statuses {
		if st.ID == id {
			return st, nil
		}
	}
	return Status{}, fmt.Errorf("status %s not found: %w", id, ErrStatusNotFound)
}

// idString is an optional id as the audit entries hold it: its text, or nil.
func idString(id *uuid.UUID) any {
	if id == nil {
		return nil
	}
	return id.String()
}

// auditEntry builds an audit row. Callers write it through the transaction's
// audit store so it commits with the change it describes; it used to be written
// separately with its error discarded, which meant the trail could lose entries
// silently.
func auditEntry(actorID *uuid.UUID, entityType string, entityID uuid.UUID, action string, before, after map[string]any) audit.Entry {
	return audit.Entry{
		ID:         uuid.New(),
		ActorID:    actorID,
		EntityType: entityType,
		EntityID:   entityID,
		Action:     action,
		Before:     before,
		After:      after,
		CreatedAt:  time.Now(),
	}
}

// ticketMap produces a shallow map representation of a ticket for audit logs.
func ticketMap(t Ticket) map[string]any {
	return map[string]any{
		"id":        t.ID,
		"status_id": t.StatusID,
		"priority":  t.Priority,
		"subject":   t.Subject,
	}
}

// UnassignForUser returns a departing user's open tickets to the queue, and
// reports how many moved.
//
// Deleting a user is a soft delete, so the assignee column kept pointing at a
// row that no longer appears in the user list: the ticket rendered as
// "Unassigned" because the lookup found nobody, was absent from the
// unassigned queue because the column was not null, and was in nobody's
// "assigned to me". It sat between the two lists with nothing to prompt
// anyone to pick it up.
//
// Open tickets only. A resolved or closed one assigned to somebody who has
// left is history, and history should record who actually handled it.
func (s *Service) UnassignForUser(ctx context.Context, actorID, userID uuid.UUID) (int, error) {
	var moved int
	// In one transaction with the audit entries, like every other assignment
	// change. Assign records who moved a ticket and what it looked like
	// before; this moved N tickets and recorded nothing, so the trail for a
	// ticket read "created, assigned to Ann" and then showed no assignee with
	// no row explaining it. An audit entry written apart from the change it
	// describes is not an audit trail, and one that is missing entirely is
	// worse.
	err := s.atomic.InTx(ctx, func(st Store, au audit.Store, _ *notification.Changes) error {
		ids, err := st.UnassignForUser(ctx, userID)
		if err != nil {
			return err
		}
		for _, id := range ids {
			before := map[string]any{"assignee_user_id": userID.String()}
			after := map[string]any{"assignee_user_id": nil}
			if err := au.Create(ctx, auditEntry(&actorID, "ticket", id, "unassigned", before, after)); err != nil {
				return fmt.Errorf("auditing unassignment: %w", err)
			}
			if err := dropAssigneeFollow(ctx, st, au, id, userID, &actorID, nil); err != nil {
				return err
			}
		}
		moved = len(ids)
		return nil
	})
	return moved, err
}

// DeleteGroup deletes a group and returns every ticket it held to the queue,
// reporting how many moved. actorID is nil when no person is behind the call.
//
// All of it in one transaction with the audit entries (#419). The database
// clears assignee_group_id by itself when the group row goes, so the tickets
// used to lose their group with nothing in the trail to say so: the same gap
// UnassignForUser closed for a deleted user.
//
// This is the one place the ticket service deletes something that is not a
// ticket. Atomic hands out only the ticket and audit stores, and the audit
// entries must commit with the unassignment they describe, so the delete rides
// along rather than running in a second transaction.
//
// Every ticket, not only open ones: the foreign key would clear a closed
// ticket's group as well, and the trail should say what actually happened.
func (s *Service) DeleteGroup(ctx context.Context, actorID *uuid.UUID, groupID uuid.UUID) (int, error) {
	var moved int
	err := s.atomic.InTx(ctx, func(st Store, au audit.Store, _ *notification.Changes) error {
		ids, err := st.UnassignForGroup(ctx, groupID)
		if err != nil {
			return err
		}
		for _, id := range ids {
			before := map[string]any{"assignee_group_id": groupID.String()}
			after := map[string]any{"assignee_group_id": nil}
			if err := au.Create(ctx, auditEntry(actorID, "ticket", id, "unassigned", before, after)); err != nil {
				return fmt.Errorf("auditing unassignment: %w", err)
			}
		}
		if err := st.DeleteGroup(ctx, groupID); err != nil {
			return err
		}
		moved = len(ids)
		return nil
	})
	return moved, err
}

// ErrValidation wraps input-validation failures, so callers — the HTTP
// handler — can map them to 400 rather than the 500 handleError falls back to
// for an error it does not recognise.
var ErrValidation = errors.New("validation failed")

// ── Attachments ───────────────────────────────────────────────────────────────

// CreateAttachment records attachment metadata after the file has been written to disk.
//
// actor is who uploaded it. For a requester (a reporting user, or a guest, who
// is Actor{Role: RoleUser} with no UserID) the ticket is locked and checked
// again here (#349): the upload was authorised before the file was read,
// scanned and written, which is the widest gap between a check and a write in
// the system, and a close that committed in it must refuse the row. Returns
// ErrClosed, and the caller removes the file. Staff are not asked.
func (s *Service) CreateAttachment(ctx context.Context, a Attachment, actor Actor) error {
	return s.atomic.InTx(ctx, func(st Store, _ audit.Store, _ *notification.Changes) error {
		if _, err := s.refuseRequesterOnClosed(ctx, st, a.TicketID, actor); err != nil {
			return err
		}
		return st.CreateAttachment(ctx, a)
	})
}

// CreateGuestAttachment is CreateAttachment for a guest, and the authority on the
// per-ticket cap (#445): the count is taken inside the transaction that holds
// the ticket's row lock, so uploads racing for the last slot cannot all pass.
// Returns ErrGuestAttachmentLimit when the ticket already holds max guest files.
//
// The closure is safe to run again (the transaction runner retries a deadlock,
// #541): it captures nothing it writes to; n and err are its own.
func (s *Service) CreateGuestAttachment(ctx context.Context, a Attachment, actor Actor, max int) error {
	return s.atomic.InTx(ctx, func(st Store, _ audit.Store, _ *notification.Changes) error {
		// Takes the ticket's row lock (a guest is a requester), which is what
		// serialises concurrent uploads: the second one's count below waits
		// for the first to commit and sees its row.
		if _, err := s.refuseRequesterOnClosed(ctx, st, a.TicketID, actor); err != nil {
			return err
		}
		n, err := st.CountGuestAttachments(ctx, a.TicketID)
		if err != nil {
			return err
		}
		if n >= max {
			return ErrGuestAttachmentLimit
		}
		return st.CreateAttachment(ctx, a)
	})
}

// refuseRequesterOnClosed is closedRefusal for a write that is about to
// happen: it takes the ticket's row lock and refuses a requester when the
// locked row is Closed, returning the locked row for the caller to reuse. A
// zero Ticket and nil for staff and admin, who are not locked out of a closed
// ticket and so need no lock.
func (s *Service) refuseRequesterOnClosed(ctx context.Context, st Store, ticketID uuid.UUID, actor Actor) (Ticket, error) {
	if actor.Role != user.RoleUser {
		return Ticket{}, nil
	}
	locked, err := st.GetByIDForUpdate(ctx, ticketID)
	if err != nil {
		return Ticket{}, err
	}
	if locked.StatusID == s.sys.closedID {
		return Ticket{}, ErrClosed
	}
	return locked, nil
}

// GetAttachment returns a single attachment by ID.
func (s *Service) GetAttachment(ctx context.Context, id uuid.UUID) (Attachment, error) {
	return s.store.GetAttachmentByID(ctx, id)
}

// AnyAttachmentExists reports whether any attachment is stored on the instance.
func (s *Service) AnyAttachmentExists(ctx context.Context) (bool, error) {
	return s.store.AnyAttachmentExists(ctx)
}

// CountGuestAttachments is how many files a guest has attached to the ticket
// (#445); staff and reporter uploads are not counted.
func (s *Service) CountGuestAttachments(ctx context.Context, ticketID uuid.UUID) (int, error) {
	return s.store.CountGuestAttachments(ctx, ticketID)
}

// ListAttachments returns all attachments for a ticket.
func (s *Service) ListAttachments(ctx context.Context, ticketID uuid.UUID) ([]Attachment, error) {
	return s.store.ListAttachments(ctx, ticketID)
}

// DeleteAttachment removes attachment metadata from the DB. Callers are
// responsible for deleting the file on disk.
func (s *Service) DeleteAttachment(ctx context.Context, id uuid.UUID) error {
	return s.store.DeleteAttachment(ctx, id)
}

// ListVisibleToStaff returns the tickets a staff member may see under the
// scope model. Used when an instance has scope enforcement switched on.
func (s *Service) ListVisibleToStaff(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Ticket, error) {
	return s.store.ListVisibleToStaff(ctx, userID, limit, offset)
}

// ListFiltered returns tickets matching a Filter. The caller is responsible
// for setting Filter.Visibility correctly — this does not re-derive the
// actor's authority, it applies what it is given.
func (s *Service) ListFiltered(ctx context.Context, f Filter) ([]Ticket, error) {
	return s.store.ListFiltered(ctx, f)
}

// SearchVisibleToStaff is ListVisibleToStaff with a search term.
func (s *Service) SearchVisibleToStaff(ctx context.Context, userID uuid.UUID, q string, limit, offset int) ([]Ticket, error) {
	return s.store.SearchVisibleToStaff(ctx, userID, q, limit, offset)
}
