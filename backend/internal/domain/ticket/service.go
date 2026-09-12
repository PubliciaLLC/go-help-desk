package ticket

import (
	"context"
	"errors"
	"fmt"
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
}

// SLAService is the narrow interface the ticket service needs from the SLA layer.
type SLAService interface {
	AttachPolicy(ctx context.Context, t Ticket) error
	RecordFirstResponse(ctx context.Context, ticketID uuid.UUID, at time.Time) error
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

	seq, err := s.store.NextSeq(ctx)
	if err != nil {
		return Ticket{}, fmt.Errorf("getting ticket sequence: %w", err)
	}

	now := time.Now()
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
		if err := st.Create(ctx, t); err != nil {
			return fmt.Errorf("creating ticket: %w", err)
		}
		if err := st.CreateStatusHistoryEntry(ctx, statusHistoryEntry(t.ID, nil, s.sys.newID, Actor{UserID: in.ReporterUserID})); err != nil {
			return fmt.Errorf("recording opening status: %w", err)
		}
		if err := au.Create(ctx, auditEntry(in.ReporterUserID, "ticket", t.ID, "created", nil, ticketMap(t))); err != nil {
			return fmt.Errorf("auditing ticket creation: %w", err)
		}
		return nil
	}); err != nil {
		return Ticket{}, err
	}

	// Everything below runs only after the commit. Dispatching inside the
	// transaction would announce a ticket that a rollback then discarded.
	if s.sla != nil {
		_ = s.sla.AttachPolicy(ctx, t) // SLA failure is non-fatal
	}

	_ = s.dispatcher.Dispatch(ctx, notification.Event{
		Type:       notification.EventTicketCreated,
		TicketID:   t.ID,
		ActorID:    in.ReporterUserID,
		OccurredAt: now,
	})

	return t, nil
}

// UpdateStatus changes the ticket status after verifying the actor has
// permission to make that transition.
func (s *Service) UpdateStatus(ctx context.Context, ticketID, newStatusID uuid.UUID, actor Actor) (Ticket, error) {
	t, err := s.store.GetByID(ctx, ticketID)
	if err != nil {
		return Ticket{}, err
	}

	newStatus, err := s.getStatusByID(ctx, newStatusID)
	if err != nil {
		return Ticket{}, err
	}

	if err := CanTransitionStatus(newStatus, actor.Role); err != nil {
		return Ticket{}, fmt.Errorf("status transition not allowed: %w", err)
	}

	before := ticketMap(t)
	oldStatusID := t.StatusID
	t.StatusID = newStatusID
	t.UpdatedAt = time.Now()

	if err := s.atomic.InTx(ctx, func(st Store, au audit.Store) error {
		if err := st.Update(ctx, t); err != nil {
			return fmt.Errorf("updating ticket status: %w", err)
		}
		if err := st.CreateStatusHistoryEntry(ctx, statusHistoryEntry(t.ID, &oldStatusID, newStatusID, actor)); err != nil {
			return fmt.Errorf("recording status change: %w", err)
		}
		if err := au.Create(ctx, auditEntry(actor.UserID, "ticket", t.ID, "status_changed", before, ticketMap(t))); err != nil {
			return fmt.Errorf("auditing status change: %w", err)
		}
		return nil
	}); err != nil {
		return Ticket{}, err
	}

	_ = s.dispatcher.Dispatch(ctx, notification.Event{
		Type:       notification.EventTicketStatusChanged,
		TicketID:   t.ID,
		ActorID:    actor.UserID,
		Payload:    map[string]any{"new_status_id": newStatusID},
		OccurredAt: time.Now(),
	})

	return t, nil
}

// Assign sets the assignee user and/or group on a ticket.
func (s *Service) Assign(ctx context.Context, ticketID uuid.UUID, assigneeUserID, assigneeGroupID *uuid.UUID, actor Actor) (Ticket, error) {
	t, err := s.store.GetByID(ctx, ticketID)
	if err != nil {
		return Ticket{}, err
	}
	before := ticketMap(t)
	t.AssigneeUserID = assigneeUserID
	t.AssigneeGroupID = assigneeGroupID
	t.UpdatedAt = time.Now()

	if err := s.atomic.InTx(ctx, func(st Store, au audit.Store) error {
		if err := st.Update(ctx, t); err != nil {
			return fmt.Errorf("assigning ticket: %w", err)
		}
		if err := au.Create(ctx, auditEntry(actor.UserID, "ticket", t.ID, "assigned", before, ticketMap(t))); err != nil {
			return fmt.Errorf("auditing assignment: %w", err)
		}
		return nil
	}); err != nil {
		return Ticket{}, err
	}

	_ = s.dispatcher.Dispatch(ctx, notification.Event{
		Type:       notification.EventTicketAssigned,
		TicketID:   t.ID,
		ActorID:    actor.UserID,
		OccurredAt: time.Now(),
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
	if err := CanUserUpdate(t, u, currentStatus, reopenWindowDays); err != nil {
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
	if reopened {
		t.StatusID = reopenTargetStatusID
		t.ResolvedAt = nil
		t.UpdatedAt = time.Now()
	}

	// The reply and, when it triggers one, the reopen commit together. Before
	// this the reply could persist while the reopen failed, leaving the ticket
	// Resolved with a user reply sitting under it.
	if err := s.atomic.InTx(ctx, func(st Store, _ audit.Store) error {
		if err := st.CreateReply(ctx, reply); err != nil {
			return fmt.Errorf("creating reply: %w", err)
		}
		if !reopened {
			return nil
		}
		if err := st.Update(ctx, t); err != nil {
			return fmt.Errorf("reopening ticket: %w", err)
		}
		if err := st.CreateStatusHistoryEntry(ctx, statusHistoryEntry(t.ID, &oldStatusID, reopenTargetStatusID, actor)); err != nil {
			return fmt.Errorf("recording reopen: %w", err)
		}
		return nil
	}); err != nil {
		return Reply{}, err
	}

	// Announced only after the commit, so a rollback cannot send a "reopened"
	// email for a transition that did not happen.
	if reopened {
		_ = s.dispatcher.Dispatch(ctx, notification.Event{
			Type:       notification.EventTicketReopened,
			TicketID:   t.ID,
			ActorID:    actor.UserID,
			OccurredAt: time.Now(),
		})
	}

	// Record first staff response for SLA. Deliberately best-effort: the reply
	// is already persisted and this is a metric, not the user's intent, so an
	// SLA outage must not fail a reply that succeeded. Unlike the reopen above,
	// nothing is announced on the strength of this write.
	if s.sla != nil && actor.Role != user.RoleUser {
		_ = s.sla.RecordFirstResponse(ctx, ticketID, reply.CreatedAt)
	}

	// Dispatch reply event. reporter_email is only populated when notifyCustomer
	// is true; the email dispatcher skips sending when the address is empty.
	_ = s.dispatcher.Dispatch(ctx, notification.Event{
		Type:     notification.EventTicketReplied,
		TicketID: t.ID,
		ActorID:  actor.UserID,
		Payload: map[string]any{
			"reporter_email": reporterEmail, // used by dispatcher to set To address
			"TrackingNumber": string(t.TrackingNumber),
			"Subject":        t.Subject,
			"ReplyBody":      body,
		},
		OccurredAt: time.Now(),
	})

	return reply, nil
}

// Resolve transitions a ticket to Resolved and records resolution notes.
func (s *Service) Resolve(ctx context.Context, ticketID uuid.UUID, notes string, actor Actor) (Ticket, error) {
	if err := CanTransitionStatus(s.sys.resolved, actor.Role); err != nil {
		return Ticket{}, fmt.Errorf("cannot resolve ticket: %w", err)
	}
	t, err := s.store.GetByID(ctx, ticketID)
	if err != nil {
		return Ticket{}, err
	}
	before := ticketMap(t)
	oldStatusID := t.StatusID
	now := time.Now()
	t.StatusID = s.sys.resolvedID
	t.ResolutionNotes = &notes
	t.ResolvedAt = &now
	t.UpdatedAt = now

	if err := s.atomic.InTx(ctx, func(st Store, au audit.Store) error {
		if err := st.Update(ctx, t); err != nil {
			return fmt.Errorf("resolving ticket: %w", err)
		}
		if err := st.CreateStatusHistoryEntry(ctx, statusHistoryEntry(t.ID, &oldStatusID, s.sys.resolvedID, actor)); err != nil {
			return fmt.Errorf("recording resolution: %w", err)
		}
		if err := au.Create(ctx, auditEntry(actor.UserID, "ticket", t.ID, "resolved", before, ticketMap(t))); err != nil {
			return fmt.Errorf("auditing resolution: %w", err)
		}
		return nil
	}); err != nil {
		return Ticket{}, err
	}

	_ = s.dispatcher.Dispatch(ctx, notification.Event{
		Type:       notification.EventTicketResolved,
		TicketID:   t.ID,
		ActorID:    actor.UserID,
		OccurredAt: now,
	})

	return t, nil
}

// Close transitions a ticket to Closed. Used by the auto-close scheduler and
// admin overrides. It does NOT call CanTransitionStatus — the caller decides
// whether this is authorised.
func (s *Service) Close(ctx context.Context, ticketID uuid.UUID) error {
	t, err := s.store.GetByID(ctx, ticketID)
	if err != nil {
		return err
	}
	oldStatusID := t.StatusID
	now := time.Now()
	t.StatusID = s.sys.closedID
	t.ClosedAt = &now
	t.UpdatedAt = now

	if err := s.atomic.InTx(ctx, func(st Store, _ audit.Store) error {
		if err := st.Update(ctx, t); err != nil {
			return fmt.Errorf("closing ticket: %w", err)
		}
		if err := st.CreateStatusHistoryEntry(ctx, statusHistoryEntry(t.ID, &oldStatusID, s.sys.closedID, SystemActor)); err != nil {
			return fmt.Errorf("recording close: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}

	_ = s.dispatcher.Dispatch(ctx, notification.Event{
		Type:       notification.EventTicketClosed,
		TicketID:   t.ID,
		OccurredAt: now,
	})
	return nil
}

// Reopen transitions a Closed ticket back to the target status. Staff/Admin only.
func (s *Service) Reopen(ctx context.Context, ticketID uuid.UUID, targetStatusID uuid.UUID, actor Actor) (Ticket, error) {
	if actor.Role == user.RoleUser {
		return Ticket{}, ErrForbidden
	}
	t, err := s.store.GetByID(ctx, ticketID)
	if err != nil {
		return Ticket{}, err
	}
	if t.StatusID != s.sys.closedID {
		return Ticket{}, fmt.Errorf("ticket is not closed")
	}
	before := ticketMap(t)
	oldStatusID := t.StatusID
	t.StatusID = targetStatusID
	t.ClosedAt = nil
	t.ResolvedAt = nil
	t.UpdatedAt = time.Now()

	if err := s.atomic.InTx(ctx, func(st Store, au audit.Store) error {
		if err := st.Update(ctx, t); err != nil {
			return fmt.Errorf("reopening ticket: %w", err)
		}
		if err := st.CreateStatusHistoryEntry(ctx, statusHistoryEntry(t.ID, &oldStatusID, targetStatusID, actor)); err != nil {
			return fmt.Errorf("recording reopen: %w", err)
		}
		if err := au.Create(ctx, auditEntry(actor.UserID, "ticket", t.ID, "reopened", before, ticketMap(t))); err != nil {
			return fmt.Errorf("auditing reopen: %w", err)
		}
		return nil
	}); err != nil {
		return Ticket{}, err
	}

	_ = s.dispatcher.Dispatch(ctx, notification.Event{
		Type:       notification.EventTicketReopened,
		TicketID:   t.ID,
		ActorID:    actor.UserID,
		OccurredAt: time.Now(),
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

// AddLink creates a directed link between two tickets.
func (s *Service) AddLink(ctx context.Context, sourceID, targetID uuid.UUID, lt LinkType, actor Actor) error {
	if sourceID == targetID {
		return fmt.Errorf("cannot link a ticket to itself")
	}
	link := TicketLink{SourceTicketID: sourceID, TargetTicketID: targetID, LinkType: lt}
	if err := s.store.CreateLink(ctx, link); err != nil {
		return fmt.Errorf("creating link: %w", err)
	}
	_ = s.dispatcher.Dispatch(ctx, notification.Event{
		Type:       notification.EventTicketLinked,
		TicketID:   sourceID,
		ActorID:    actor.UserID,
		Payload:    map[string]any{"target_id": targetID, "link_type": lt},
		OccurredAt: time.Now(),
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
	return s.store.ListResolvedBefore(ctx, before, limit)
}

// ListStatuses returns all configured statuses.
func (s *Service) ListStatuses(ctx context.Context) ([]Status, error) {
	return s.statuses.ListStatuses(ctx)
}

// AddStatus creates a new custom status entry.
func (s *Service) AddStatus(ctx context.Context, st Status) error {
	st.Active = true
	return s.statuses.CreateStatus(ctx, st)
}

// SaveStatus persists changes to an existing status record.
func (s *Service) SaveStatus(ctx context.Context, st Status) error {
	return s.statuses.UpdateStatus(ctx, st)
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
// system status or if any tickets currently have this status.
func (s *Service) RemoveStatus(ctx context.Context, id uuid.UUID) error {
	st, err := s.getStatusByID(ctx, id)
	if err != nil {
		return err
	}
	if st.Kind != StatusKindCustom {
		return fmt.Errorf("cannot delete system status %q", st.Name)
	}
	count, err := s.statuses.CountByStatus(ctx, id)
	if err != nil {
		return fmt.Errorf("counting tickets for status: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("status %q has %d ticket(s); deactivate it instead of deleting", st.Name, count)
	}
	return s.statuses.DeleteStatus(ctx, id)
}

// getStatusByID fetches a status; returns a descriptive error on miss.
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
	return Status{}, fmt.Errorf("status %s not found", id)
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

// ErrValidation wraps input-validation failures from Create, so callers
// (the HTTP handler) can map them to 400 instead of the 500 handleError
// falls back to for an unrecognized error.
var ErrValidation = errors.New("validation failed")

// ── Attachments ───────────────────────────────────────────────────────────────

// CreateAttachment records attachment metadata after the file has been written to disk.
func (s *Service) CreateAttachment(ctx context.Context, a Attachment) error {
	return s.store.CreateAttachment(ctx, a)
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
