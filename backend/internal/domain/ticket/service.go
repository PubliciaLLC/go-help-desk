package ticket

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

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
}

// SetClosedReopenPolicy wires the source of the closed_reopen_policy setting
// (#349). Called once at start-up, before the service takes requests; without
// it Closed is terminal for every role, which is the default setting.
func (s *Service) SetClosedReopenPolicy(read func(context.Context) string) {
	s.closedReopenPolicy = read
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
)

// CreateInput is the data needed to open a new ticket.
type CreateInput struct {
	Subject     string
	Description string
	CategoryID  uuid.UUID
	TypeID      *uuid.UUID
	ItemID      *uuid.UUID
	Priority    Priority

	// Exactly one of ReporterUserID or GuestEmail must be set.
	ReporterUserID *uuid.UUID
	GuestEmail     *string
	GuestName      string // required when GuestEmail is set
	GuestPhone     string // optional

	// TrackingPrefix is the instance's configured tracking-number prefix.
	// Empty falls back to DefaultTrackingPrefix, so a caller that does not
	// care — a test, a script — still produces well-formed numbers.
	//
	// Passed in rather than read here because internal/domain must not reach
	// into settings; this follows the same shape as reopenWindowDays on
	// AddReply.
	TrackingPrefix string
}

// Create opens a new ticket, fires the created event, and optionally attaches
// an SLA policy.
func (s *Service) Create(ctx context.Context, in CreateInput) (Ticket, error) {
	return s.create(ctx, in, Actor{UserID: in.ReporterUserID}, nil)
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
func (s *Service) CreateFollowUp(ctx context.Context, originalID uuid.UUID, trackingPrefix string, actor Actor) (Ticket, error) {
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
		Subject:        orig.Subject,
		Description:    orig.Description,
		CategoryID:     orig.CategoryID,
		TypeID:         orig.TypeID,
		ItemID:         orig.ItemID,
		Priority:       orig.Priority,
		ReporterUserID: orig.ReporterUserID,
		GuestEmail:     orig.GuestEmail,
		GuestName:      orig.GuestName,
		GuestPhone:     orig.GuestPhone,
		TrackingPrefix: trackingPrefix,
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
		Subject:        orig.Subject,
		Description:    orig.Description,
		CategoryID:     orig.CategoryID,
		Priority:       PriorityMedium,
		ReporterUserID: orig.ReporterUserID,
		GuestEmail:     orig.GuestEmail,
		GuestName:      orig.GuestName,
		GuestPhone:     orig.GuestPhone,
		TrackingPrefix: trackingPrefix,
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

// create is the one path that opens a ticket. actor is who did it, for the
// status history, the audit entry and the event: the reporter for an ordinary
// create (Create passes them), the member of staff for a follow-up. followUpOf
// is the closed ticket a follow-up continues, or nil.
func (s *Service) create(ctx context.Context, in CreateInput, actor Actor, followUpOf *Ticket) (Ticket, error) {
	if strings.TrimSpace(in.Subject) == "" {
		return Ticket{}, fmt.Errorf("subject is required: %w", ErrValidation)
	}
	if len(in.Subject) > MaxSubjectLength {
		return Ticket{}, fmt.Errorf("subject must be %d characters or fewer: %w", MaxSubjectLength, ErrValidation)
	}
	if len(in.Description) > MaxDescriptionLength {
		return Ticket{}, fmt.Errorf("description must be %d characters or fewer: %w", MaxDescriptionLength, ErrValidation)
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

	// The category, checked for the same reason as everything else here:
	// after this point a tracking number has been taken, and an id the
	// foreign key refuses costs a 500 and a permanent gap in the sequence.
	// Only a reporting user's category was checked, and that check asks
	// whether it is OPEN to them — not whether it exists at all, which is
	// what staff and MCP needed.
	if ok, err := s.store.CategoryExists(ctx, in.CategoryID); err != nil {
		return Ticket{}, err
	} else if !ok {
		return Ticket{}, fmt.Errorf("%w: category_id is not a category on this help desk", ErrValidation)
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
		return Ticket{}, err
	} else if !ok {
		return Ticket{}, fmt.Errorf(
			"%w: type_id and item_id must belong to the category and to each other", ErrValidation)
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
	t := Ticket{
		ID:             uuid.New(),
		TrackingNumber: GenerateTrackingNumber(in.TrackingPrefix, now.Year(), seq),
		Subject:        strings.TrimSpace(in.Subject),
		Description:    in.Description,
		CategoryID:     in.CategoryID,
		TypeID:         in.TypeID,
		ItemID:         in.ItemID,
		Priority:       in.Priority,
		StatusID:       s.sys.newID,
		ReporterUserID: in.ReporterUserID,
		GuestEmail:     in.GuestEmail,
		GuestName:      in.GuestName,
		GuestPhone:     in.GuestPhone,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	// The ticket, its opening status-history row and its audit entry commit
	// together or not at all.
	if err := s.atomic.InTx(ctx, func(st Store, au audit.Store) error {
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
		return nil
	}); err != nil {
		return Ticket{}, err
	}

	// Re-read what was committed, and announce that rather than the struct we
	// asked the database to store.
	//
	// The two values the notification email is allowed to carry — the tracking
	// number and the guest's address — come from this row and from nowhere
	// else. Built in memory they are request text that happens to have been
	// written down; read back they are the record.
	//
	// There is deliberately no fallback to the in-memory copy. Falling back
	// would put the request copy back in the email on the one path where the
	// read failed, which is the whole thing this avoids — and it would do it
	// invisibly. A read failure costs the notification, not the ticket: the
	// ticket is committed and is returned to the caller either way.
	var emailTracking, emailRecipient string
	if stored, err := s.store.GetByID(ctx, t.ID); err == nil {
		emailTracking = string(stored.TrackingNumber)
		if stored.GuestEmail != nil {
			emailRecipient = *stored.GuestEmail
		}
	}

	// Everything below runs only after the commit. Dispatching inside the
	// transaction would announce a ticket that a rollback then discarded.
	if s.sla != nil {
		_ = s.sla.AttachPolicy(ctx, t) // SLA failure is non-fatal
	}

	// The payload is what makes this email reachable at all. It shipped with
	// none, and eventToEmail returns ok=false without a recipient — so the
	// "Your ticket has been received" mail has never been sent to anyone. For a
	// guest that mail is the only place the tracking number appears, so a guest
	// ticket was unreachable by the person who filed it.
	//
	// The existing email test hand-built this payload, which is why it passed
	// while nothing populated it.
	createdPayload := map[string]any{
		"TrackingNumber": string(t.TrackingNumber),
		"Subject":        t.Subject,
		"Priority":       string(t.Priority),
	}
	if t.GuestEmail != nil && *t.GuestEmail != "" {
		createdPayload["guest_email"] = *t.GuestEmail
	}
	_ = s.dispatcher.Dispatch(ctx, notification.Event{
		Type:           notification.EventTicketCreated,
		TicketID:       t.ID,
		ActorID:        actor.UserID,
		Payload:        createdPayload,
		OccurredAt:     now,
		TrackingNumber: emailTracking,
		Recipient:      emailRecipient,
		GuestLink:      guestLink,
		Subject:        t.Subject,
	})
	// Announced like any other link (AddLink), for whoever follows
	// ticket.linked: the original is the source, the follow-up its child.
	if followUpOf != nil {
		_ = s.dispatcher.Dispatch(ctx, notification.Event{
			Type:           notification.EventTicketLinked,
			TicketID:       followUpOf.ID,
			ActorID:        actor.UserID,
			Payload:        map[string]any{"target_id": t.ID, "link_type": LinkParentChild},
			OccurredAt:     now,
			TrackingNumber: string(followUpOf.TrackingNumber),
			Subject:        followUpOf.Subject,
		})
	}

	return t, nil
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
	newStatus, err := s.getStatusByID(ctx, newStatusID)
	if err != nil {
		return Ticket{}, err
	}

	// The role check does not depend on the row, so it stays outside the
	// transaction and refuses without taking a lock.
	if err := CanTransitionStatus(newStatus, actor.Role); err != nil {
		return Ticket{}, fmt.Errorf("status transition not allowed: %w", err)
	}

	var t Ticket
	var before map[string]any
	var oldStatusID uuid.UUID
	var guestLink bool
	var closing bool
	now := time.Now()

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
	if err := s.atomic.InTx(ctx, func(st Store, au audit.Store) error {
		// Read under the lock: everything below is computed from this row, so
		// a concurrent writer that committed first is seen rather than
		// overwritten.
		var err error
		t, err = st.GetByIDForUpdate(ctx, ticketID)
		if err != nil {
			return err
		}
		// Closed is terminal by default (#349): nothing leaves it by a status
		// change unless closed_reopen_policy lets this role. Decided on the
		// locked row, so a close that committed first is seen, and the policy
		// is read now. Staff and admin otherwise get a new linked ticket
		// (CreateFollowUp).
		forcedPolicy := ""
		if t.StatusID == s.sys.closedID && newStatusID != s.sys.closedID {
			p, err := s.forceReopenGate(ctx, actor)
			if err != nil {
				return err
			}
			forcedPolicy = p
		}
		before = ticketMap(t)
		oldStatusID = t.StatusID
		t.StatusID = newStatusID
		t.UpdatedAt = now
		applyStatusTimestamps(&t, oldStatusID, newStatus, s.sys, now)

		if err := st.Update(ctx, t); err != nil {
			return fmt.Errorf("updating ticket status: %w", err)
		}
		if err := st.CreateStatusHistoryEntry(ctx, statusHistoryEntry(t.ID, &oldStatusID, newStatusID, actor)); err != nil {
			return fmt.Errorf("recording status change: %w", err)
		}
		after := ticketMap(t)
		if forcedPolicy != "" {
			forcedAudit(after, forcedPolicy)
		}
		if err := au.Create(ctx, auditEntry(actor.UserID, "ticket", t.ID, "status_changed", before, after)); err != nil {
			return fmt.Errorf("auditing status change: %w", err)
		}
		// A status change is something the guest is told about, so it rotates
		// the link and the notification carries the replacement. Closing is
		// the exception: it stops rotating, and leaves the links the guest
		// holds alone (#349) — the ticket is an archive they may still read,
		// until the link expires.
		if newStatusID == s.sys.closedID {
			// And tell nobody, which is what Close() does. There is no new
			// link to send, and a mail with none fell back to the account URL
			// — the guest was sent "see where it stands" pointing at
			// /tickets/<uuid>, a page they have no account to open. The two
			// doors into Closed behave the same way.
			closing = true
		} else {
			guestLink = hasGuestRecipient(t)
		}
		return nil
	}); err != nil {
		return Ticket{}, err
	}

	// The other door into Resolved. Recording this only in Resolve() meant a
	// ticket resolved by PATCHing status_id, or by MCP update_ticket_status,
	// left sla_records.resolved_at NULL — and the breach evaluator reads NULL
	// as "never resolved", so an on-time resolution became a permanent false
	// breach. Non-fatal and after the commit, matching Resolve.
	if s.sla != nil && newStatusID == s.sys.resolvedID {
		// Non-fatal: SLA bookkeeping must not fail the status change it
		// describes. Discarded here rather than logged because domain code
		// does not log — cmd/server wraps the SLA service so the boundary
		// reports these, which is where a failure can actually be seen.
		//
		// #234: resolutionInstant, not a bare now — a genuine re-resolve
		// through this door (#225) preserves t.ResolvedAt at its ORIGINAL
		// instant (applyStatusTimestamps never moves it forward), but
		// RecordResolved's own no-op guard only fires once sla_records
		// already has a resolution — if an earlier call was dropped (a
		// pre-#216 toggle gap, or a transient failure) this is the repair
		// path, and it must stamp the real original instant, not this later
		// re-resolve's now. See the identical reasoning on the Closed door
		// just below.
		_ = s.sla.RecordResolved(ctx, t, resolutionInstant(t, now))
	}
	// The other door into Closed. Same #220 reasoning as close(): a ticket
	// moved straight to Closed here without ever resolving would otherwise
	// carry a NULL sla_records.resolved_at forever. RecordResolved no-ops
	// when a resolution is already recorded, so this is a no-op on the
	// ordinary Resolved-then-Closed path.
	//
	// #227: resolutionInstant, not a bare now — applyStatusTimestamps' closedID
	// case never touches ResolvedAt, so t.ResolvedAt already holds the real
	// resolution instant whenever one exists (e.g. an earlier RecordResolved
	// call that failed non-fatally, or was dropped by the pre-#216 toggle
	// bug). Stamping breaches against the LATER close instant instead can
	// manufacture a false breach on a ticket that was actually resolved on
	// time.
	if s.sla != nil && newStatusID == s.sys.closedID {
		_ = s.sla.RecordResolved(ctx, t, resolutionInstant(t, now))
	}

	_ = s.dispatcher.Dispatch(ctx, notification.Event{
		Type:           notification.EventTicketStatusChanged,
		TicketID:       t.ID,
		ActorID:        actor.UserID,
		Payload:        map[string]any{"new_status_id": newStatusID},
		OccurredAt:     time.Now(),
		TrackingNumber: string(t.TrackingNumber),
		Recipient:      guestNotifyTarget(t, closing),
		GuestLink:      guestLink,
		Subject:        t.Subject,
		StatusName:     newStatus.Name,
	})

	return t, nil
}

// Assign sets the assignee user and/or group on a ticket.
func (s *Service) Assign(ctx context.Context, ticketID uuid.UUID, assigneeUserID, assigneeGroupID *uuid.UUID, actor Actor) (Ticket, error) {
	var t Ticket
	now := time.Now()

	if err := s.atomic.InTx(ctx, func(st Store, au audit.Store) error {
		var err error
		t, err = st.GetByIDForUpdate(ctx, ticketID)
		if err != nil {
			return err
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

		before := ticketMap(t)
		t.AssigneeUserID = assigneeUserID
		t.AssigneeGroupID = assigneeGroupID
		t.UpdatedAt = now

		if err := st.Update(ctx, t); err != nil {
			return fmt.Errorf("assigning ticket: %w", err)
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
			return fmt.Errorf("auditing assignment: %w", err)
		}
		return nil
	}); err != nil {
		return Ticket{}, err
	}

	_ = s.dispatcher.Dispatch(ctx, notification.Event{
		Type:           notification.EventTicketAssigned,
		TicketID:       t.ID,
		ActorID:        actor.UserID,
		OccurredAt:     time.Now(),
		TrackingNumber: string(t.TrackingNumber),
		Subject:        t.Subject,
	})

	return t, nil
}

// AddReply appends a reply to a ticket. If the actor is a user replying to a
// Resolved ticket within the reopen window, the ticket is automatically
// reopened to the configured target status.
//
// notifyCustomer controls whether a ticket-update email is sent to the
// reporter. It is forced false for internal notes. reporterEmail is the
// recipient address; callers are responsible for looking it up.
func (s *Service) AddReply(ctx context.Context, ticketID uuid.UUID, body string, internal bool, notifyCustomer bool, reporterEmail string, actor Actor, reopenWindowDays int, reopenTargetStatusID uuid.UUID) (Reply, error) {
	return s.addReply(ctx, ticketID, body, internal, notifyCustomer, reporterEmail, actor, reopenWindowDays, reopenTargetStatusID, nil)
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
func (s *Service) AddGuestReply(ctx context.Context, ticketID uuid.UUID, body string, reopenWindowDays int, reopenTargetStatusID uuid.UUID) (Reply, error) {
	guest := Actor{UserID: nil, Role: user.RoleUser}
	return s.addReply(ctx, ticketID, body, false, false, "", guest, reopenWindowDays, reopenTargetStatusID,
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
func (s *Service) addReply(ctx context.Context, ticketID uuid.UUID, body string, internal bool, notifyCustomer bool, reporterEmail string, actor Actor, reopenWindowDays int, reopenTargetStatusID uuid.UUID, authorize func(Ticket, Status) error) (Reply, error) {
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

	if err := s.atomic.InTx(ctx, func(st Store, au audit.Store) error {
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
		if !reopened {
			return nil
		}
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
			return nil
		}
		before := ticketMap(locked)
		locked.StatusID = t.StatusID
		locked.UpdatedAt = t.UpdatedAt
		// Through the shared rule, so this door agrees with the others about
		// resolved_at and closed_at rather than clearing one and forgetting
		// the other.
		applyStatusTimestamps(&locked, oldStatusID, target, s.sys, t.UpdatedAt)
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
		return nil
	}); err != nil {
		return Reply{}, err
	}

	// Announced only after the commit, so a rollback cannot send a "reopened"
	// email for a transition that did not happen.
	if reopened {
		_ = s.dispatcher.Dispatch(ctx, notification.Event{
			Type:           notification.EventTicketReopened,
			TicketID:       t.ID,
			ActorID:        actor.UserID,
			OccurredAt:     time.Now(),
			TrackingNumber: string(t.TrackingNumber),
			Subject:        t.Subject,
		})
	}

	// Record first staff response for SLA. Deliberately best-effort: the reply
	// is already persisted and this is a metric, not the user's intent, so an
	// SLA outage must not fail a reply that succeeded. Unlike the reopen above,
	// nothing is announced on the strength of this write.
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

	// Dispatch reply event. reporter_email is only populated when notifyCustomer
	// is true; the email dispatcher skips sending when the address is empty.
	//
	// Recipient and TrackingNumber are the same two values again, carried
	// separately from Payload because they are the only ones an email may use.
	// reporterEmail is looked up from the ticket's own row by the caller, and t
	// is a row read from the store — under FOR UPDATE on the reopen path,
	// plainly otherwise. Either way it is the record, not request text, which
	// is the property that matters here.
	_ = s.dispatcher.Dispatch(ctx, notification.Event{
		Type:           notification.EventTicketReplied,
		TicketID:       t.ID,
		ActorID:        actor.UserID,
		TrackingNumber: string(t.TrackingNumber),
		Recipient:      reporterEmail,
		GuestLink:      guestLink,
		Subject:        t.Subject,
		Payload: func() map[string]any {
			p := map[string]any{
				"reporter_email": reporterEmail, // used by dispatcher to set To address
				"TrackingNumber": string(t.TrackingNumber),
				"Subject":        t.Subject,
				"internal":       internal,
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
				p["ReplyBody"] = body
			}
			return p
		}(),
		OccurredAt: time.Now(),
	})

	return reply, nil
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
// Returns the updated ticket and the new guest token.
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
func (s *Service) resolveInTx(ctx context.Context, st Store, au audit.Store, ticketID uuid.UUID, notes string, actor Actor, now time.Time, sameTarget bool) (Ticket, bool, bool, error) {
	t, err := st.GetByIDForUpdate(ctx, ticketID)
	if err != nil {
		return Ticket{}, false, false, err
	}
	// Resolving a Closed ticket is a door out of Closed, which is terminal
	// unless closed_reopen_policy lets this role (#349). Checked on the locked
	// row, before the no-op test: Closed is never "already resolved".
	forcedPolicy := ""
	if t.StatusID == s.sys.closedID {
		p, err := s.forceReopenGate(ctx, actor)
		if err != nil {
			return Ticket{}, false, false, err
		}
		forcedPolicy = p
	}
	if t.StatusID == s.sys.resolvedID && sameTarget && resolutionNotesMatch(t.ResolutionNotes, notes) {
		return t, false, true, nil
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
		return Ticket{}, false, false, fmt.Errorf("resolving ticket: %w", err)
	}
	if err := st.CreateStatusHistoryEntry(ctx, statusHistoryEntry(t.ID, &oldStatusID, s.sys.resolvedID, actor)); err != nil {
		return Ticket{}, false, false, fmt.Errorf("recording resolution: %w", err)
	}
	after := ticketMap(t)
	if forcedPolicy != "" {
		forcedAudit(after, forcedPolicy)
	}
	if err := au.Create(ctx, auditEntry(actor.UserID, "ticket", t.ID, "resolved", before, after)); err != nil {
		return Ticket{}, false, false, fmt.Errorf("auditing resolution: %w", err)
	}
	// Rotate: the guest is told, and the link they are told with is the
	// one they need to reopen inside the window.
	return t, hasGuestRecipient(t), false, nil
}

// Resolve transitions a ticket to Resolved and records resolution notes.
func (s *Service) Resolve(ctx context.Context, ticketID uuid.UUID, notes string, actor Actor) (Ticket, error) {
	if err := CanTransitionStatus(s.sys.resolved, actor.Role); err != nil {
		return Ticket{}, fmt.Errorf("cannot resolve ticket: %w", err)
	}
	var t Ticket
	var guestLink bool
	var alreadyResolved bool
	now := time.Now()
	if err := s.atomic.InTx(ctx, func(st Store, au audit.Store) error {
		var err error
		// Resolve has no target of its own, so its no-op decision rests on
		// notes alone — sameTarget is unconditionally true.
		t, guestLink, alreadyResolved, err = s.resolveInTx(ctx, st, au, ticketID, notes, actor, now, true)
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

	// After the commit, like the dispatch below: an SLA record stamped for a
	// resolution that then rolled back would be worse than a missing one.
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

	_ = s.dispatcher.Dispatch(ctx, notification.Event{
		Type:           notification.EventTicketResolved,
		TicketID:       t.ID,
		ActorID:        actor.UserID,
		OccurredAt:     now,
		TrackingNumber: string(t.TrackingNumber),
		Recipient:      guestRecipient(t),
		GuestLink:      guestLink,
		Subject:        t.Subject,
	})

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
	var guestLink bool
	var alreadyResolved bool
	var alreadyLinked bool
	now := time.Now()

	if err := s.atomic.InTx(ctx, func(st Store, au audit.Store) error {
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
		if strings.TrimSpace(notes) == "" {
			notes = DuplicateResolutionNotes(target.TrackingNumber)
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
		t, guestLink, alreadyResolved, txErr = s.resolveInTx(ctx, st, au, sourceID, notes, actor, now, alreadyLinked)
		return txErr
	}); err != nil {
		return Ticket{}, err
	}

	// #229: no new link was created when alreadyLinked is true, so there is
	// nothing new to announce — AddLink (or an earlier ResolveAsDuplicate
	// call) already dispatched EventTicketLinked for this exact pair.
	// Dispatching it again here on every repeat call, including a genuine
	// double-submit, reported the same link as freshly made every time.
	if !alreadyLinked {
		// t is the source ticket (resolveInTx returns the row it just
		// resolved), so its Subject and TrackingNumber describe sourceID, the
		// ticket TicketID names here.
		_ = s.dispatcher.Dispatch(ctx, notification.Event{
			Type:           notification.EventTicketLinked,
			TicketID:       sourceID,
			ActorID:        actor.UserID,
			Payload:        map[string]any{"target_id": targetID, "link_type": LinkDuplicateOf},
			OccurredAt:     now,
			TrackingNumber: string(t.TrackingNumber),
			Subject:        t.Subject,
		})
	}

	// True double-submit (e.g. a stale second tab): identical notes against
	// the same already-linked target, so the resolve side effects already
	// happened once. See #209 and #225.
	if alreadyResolved {
		return t, nil
	}

	// After the commit, dispatch events and record SLA, as Resolve does.
	//
	// #234: resolutionInstant, not a bare now — same reasoning as Resolve and
	// UpdateStatus.
	if s.sla != nil {
		_ = s.sla.RecordResolved(ctx, t, resolutionInstant(t, now))
	}

	_ = s.dispatcher.Dispatch(ctx, notification.Event{
		Type:           notification.EventTicketResolved,
		TicketID:       t.ID,
		ActorID:        actor.UserID,
		OccurredAt:     now,
		TrackingNumber: string(t.TrackingNumber),
		Recipient:      guestRecipient(t),
		GuestLink:      guestLink,
		Subject:        t.Subject,
	})

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

	if err := s.atomic.InTx(ctx, func(st Store, au audit.Store) error {
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

	// Skip and re-close dispatch nothing, same as before.
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

	_ = s.dispatcher.Dispatch(ctx, notification.Event{
		Type:           notification.EventTicketClosed,
		TicketID:       t.ID,
		ActorID:        actor.UserID,
		OccurredAt:     now,
		TrackingNumber: string(t.TrackingNumber),
		Subject:        t.Subject,
	})
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

	now := time.Now()
	var oldStatusID uuid.UUID
	var guestLink bool

	if err := s.atomic.InTx(ctx, func(st Store, au audit.Store) error {
		// Re-read under the lock. The check above answered the precondition on
		// an unlocked copy; this is the row actually being overwritten, and a
		// concurrent close or resolve may have landed in between.
		t, err = st.GetByIDForUpdate(ctx, ticketID)
		if err != nil {
			return err
		}
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
		// if the reopen target is Pending — a reopened ticket is not a new SLA
		// clock.
		applyStatusTimestamps(&t, oldStatusID, target, s.sys, now)

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
		guestLink = hasGuestRecipient(t)
		return nil
	}); err != nil {
		return Ticket{}, err
	}

	_ = s.dispatcher.Dispatch(ctx, notification.Event{
		Type:           notification.EventTicketReopened,
		TicketID:       t.ID,
		ActorID:        actor.UserID,
		OccurredAt:     time.Now(),
		TrackingNumber: string(t.TrackingNumber),
		Recipient:      guestRecipient(t),
		GuestLink:      guestLink,
		Subject:        t.Subject,
	})
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

// ListAuditEntries returns everything recorded against one ticket, oldest
// first — the same ordering ListStatusHistory uses, so the two feeds a
// ticket's detail page shows side by side read the same direction.
//
// ListByEntity's own query is newest-first, shared with the (not yet built)
// admin-wide view where that ordering suits pagination; reversed here rather
// than changing the shared query underneath it.
func (s *Service) ListAuditEntries(ctx context.Context, ticketID uuid.UUID) ([]audit.Entry, error) {
	entries, err := s.auditStore.ListByEntity(ctx, "ticket", ticketID, ticketAuditCap, 0)
	if err != nil {
		return nil, fmt.Errorf("listing audit entries: %w", err)
	}
	slices.Reverse(entries)
	return entries, nil
}

// AddLink creates a directed link between two tickets.
func (s *Service) AddLink(ctx context.Context, sourceID, targetID uuid.UUID, lt LinkType, actor Actor) error {
	if sourceID == targetID {
		return fmt.Errorf("cannot add link: %w", ErrSelfLink)
	}
	// Validate LinkType before attempting to write to the database.
	if !lt.Valid() {
		return fmt.Errorf("%q: %w", lt, ErrInvalidLinkType)
	}

	var subject, tracking string
	if err := s.atomic.InTx(ctx, func(st Store, _ audit.Store) error {
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
		subject = source.Subject
		tracking = string(source.TrackingNumber)

		link := TicketLink{SourceTicketID: sourceID, TargetTicketID: targetID, LinkType: lt}
		if err := st.CreateLink(ctx, link); err != nil {
			return fmt.Errorf("creating link: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}

	_ = s.dispatcher.Dispatch(ctx, notification.Event{
		Type:           notification.EventTicketLinked,
		TicketID:       sourceID,
		ActorID:        actor.UserID,
		Payload:        map[string]any{"target_id": targetID, "link_type": lt},
		OccurredAt:     time.Now(),
		TrackingNumber: tracking,
		Subject:        subject,
	})
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

// UpdateCTI changes the category/type/item classification of a ticket.
// Only staff and admin may call this; enforcement is at the handler layer.
func (s *Service) UpdateCTI(ctx context.Context, id, categoryID uuid.UUID, typeID, itemID *uuid.UUID) (Ticket, error) {
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
		return Ticket{}, err
	} else if !ok {
		return Ticket{}, fmt.Errorf("%w: category_id is not a category on this help desk", ErrValidation)
	}
	if ok, err := s.store.CTIIsCoherent(ctx, categoryID, typeID, itemID); err != nil {
		return Ticket{}, err
	} else if !ok {
		return Ticket{}, fmt.Errorf(
			"%w: type_id and item_id must belong to the category and to each other", ErrValidation)
	}

	if err := s.store.UpdateCTI(ctx, id, categoryID, typeID, itemID); err != nil {
		return Ticket{}, fmt.Errorf("updating ticket CTI: %w", err)
	}
	return s.store.GetByID(ctx, id)
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
	err := s.atomic.InTx(ctx, func(st Store, au audit.Store) error {
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
	return s.atomic.InTx(ctx, func(st Store, _ audit.Store) error {
		if _, err := s.refuseRequesterOnClosed(ctx, st, a.TicketID, actor); err != nil {
			return err
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
