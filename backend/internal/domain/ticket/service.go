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

	seq, err := s.store.NextSeq(ctx)
	if err != nil {
		return Ticket{}, fmt.Errorf("getting ticket sequence: %w", err)
	}

	now := time.Now()
	var guestToken string
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
		// A guest's first link, minted with the ticket so a rollback takes the
		// credential with it. Returns "" for a ticket with a reporter account.
		if guestToken, err = rotateGuestToken(ctx, st, t); err != nil {
			return err
		}
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
		ActorID:        in.ReporterUserID,
		Payload:        createdPayload,
		OccurredAt:     now,
		TrackingNumber: emailTracking,
		Recipient:      emailRecipient,
		GuestToken:     guestToken,
		Subject:        t.Subject,
	})

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
		t.SLAPausedSeconds += int64(now.Sub(*t.PendingSince) / time.Second)
		t.PendingSince = nil
	}
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
	var guestToken string
	var closing bool
	now := time.Now()

	// resolved_at and closed_at are maintained here as well as in
	// Resolve/Close/Reopen, because this is a second door into the same three
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
		if err := au.Create(ctx, auditEntry(actor.UserID, "ticket", t.ID, "status_changed", before, ticketMap(t))); err != nil {
			return fmt.Errorf("auditing status change: %w", err)
		}
		// A status change is something the guest is told about, so it rotates
		// the link and the notification carries the replacement. Closing is
		// the exception: it revokes instead, since there is nothing left to
		// come back to.
		if newStatusID == s.sys.closedID {
			if err := st.DeleteGuestTokensForTicket(ctx, t.ID); err != nil {
				return fmt.Errorf("revoking guest access: %w", err)
			}
			// And tell nobody, which is what Close() does. Revoking left
			// Recipient set with no token, so the mail fell back to the
			// account URL — the guest was sent "see where it stands" pointing
			// at /tickets/<uuid>, a page they have no account to open. The two
			// doors into Closed now behave the same way.
			closing = true
		} else if guestToken, err = rotateGuestToken(ctx, st, t); err != nil {
			return err
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
		_ = s.sla.RecordResolved(ctx, t, now)
	}

	_ = s.dispatcher.Dispatch(ctx, notification.Event{
		Type:           notification.EventTicketStatusChanged,
		TicketID:       t.ID,
		ActorID:        actor.UserID,
		Payload:        map[string]any{"new_status_id": newStatusID},
		OccurredAt:     time.Now(),
		TrackingNumber: string(t.TrackingNumber),
		Recipient:      guestNotifyTarget(t, closing),
		GuestToken:     guestToken,
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
		before := ticketMap(t)
		t.AssigneeUserID = assigneeUserID
		t.AssigneeGroupID = assigneeGroupID
		t.UpdatedAt = now

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
	// Rotate if and only if the replacement will be delivered, and do it in the
	// same transaction as the reply.
	//
	// reporterEmail is the address this reply will be mailed to, and it is
	// empty in three cases that all used to rotate anyway: an internal note, a
	// staff reply with notify_customer off, and — worst — the guest's own
	// reply, which passes "" because mailing customers their own words back is
	// noise. Each minted a token that reached nobody and killed the one the
	// guest was holding, so replying, the single thing a guest comes back to
	// do, locked them out of their own ticket.
	//
	// Inside the transaction because it was not, alone among the rotation
	// paths: a DELETE and an INSERT on autocommit after the reply had already
	// landed. A failure between them left a committed reply and no token, and
	// two concurrent replies could interleave into two live tokens — the
	// "rotation replaces rather than accumulates" invariant was only true
	// serially.
	//
	// The honest limit: this rotates before a send whose failure the
	// dispatcher discards, so an SMTP outage rotates and delivers nothing. The
	// guest is not stranded — /resend mints another — but "rotate iff
	// delivered" is really "iff a send is attempted", and #164 is where that
	// stops being true.
	var guestToken string
	rotateFor := t.GuestEmail != nil && *t.GuestEmail != "" && reporterEmail != ""

	if err := s.atomic.InTx(ctx, func(st Store, _ audit.Store) error {
		if err := st.CreateReply(ctx, reply); err != nil {
			return fmt.Errorf("creating reply: %w", err)
		}
		if rotateFor {
			var err error
			if guestToken, err = rotateGuestToken(ctx, st, t); err != nil {
				return err
			}
		}
		if !reopened {
			return nil
		}
		// Re-read under the lock before overwriting the row. The copy above
		// decided WHETHER to reopen — a decision about the status the reporter
		// replied to — but st.Update writes every column, so the row it writes
		// has to be the row it just read, or a concurrent assign or edit is
		// lost.
		locked, err := st.GetByIDForUpdate(ctx, ticketID)
		if err != nil {
			return err
		}
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
	if s.sla != nil && actor.Role != user.RoleUser {
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
		GuestToken:     guestToken,
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

// resolveInTx is the transactional body of Resolve, extracted so ResolveAsDuplicate
// can reuse it. It transitions the ticket to Resolved, records resolution notes,
// and performs all the necessary updates in one transaction.
// Returns the updated ticket and the new guest token.
func (s *Service) resolveInTx(ctx context.Context, st Store, au audit.Store, ticketID uuid.UUID, notes string, actor Actor, now time.Time) (Ticket, string, error) {
	t, err := st.GetByIDForUpdate(ctx, ticketID)
	if err != nil {
		return Ticket{}, "", err
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
		return Ticket{}, "", fmt.Errorf("resolving ticket: %w", err)
	}
	if err := st.CreateStatusHistoryEntry(ctx, statusHistoryEntry(t.ID, &oldStatusID, s.sys.resolvedID, actor)); err != nil {
		return Ticket{}, "", fmt.Errorf("recording resolution: %w", err)
	}
	if err := au.Create(ctx, auditEntry(actor.UserID, "ticket", t.ID, "resolved", before, ticketMap(t))); err != nil {
		return Ticket{}, "", fmt.Errorf("auditing resolution: %w", err)
	}
	// Rotate: the guest is told, and the link they are told with is the
	// one they need to reopen inside the window.
	guestToken, err := rotateGuestToken(ctx, st, t)
	if err != nil {
		return Ticket{}, "", err
	}
	return t, guestToken, nil
}

// Resolve transitions a ticket to Resolved and records resolution notes.
func (s *Service) Resolve(ctx context.Context, ticketID uuid.UUID, notes string, actor Actor) (Ticket, error) {
	if err := CanTransitionStatus(s.sys.resolved, actor.Role); err != nil {
		return Ticket{}, fmt.Errorf("cannot resolve ticket: %w", err)
	}
	var t Ticket
	var guestToken string
	now := time.Now()
	if err := s.atomic.InTx(ctx, func(st Store, au audit.Store) error {
		var err error
		t, guestToken, err = s.resolveInTx(ctx, st, au, ticketID, notes, actor, now)
		return err
	}); err != nil {
		return Ticket{}, err
	}

	// After the commit, like the dispatch below: an SLA record stamped for a
	// resolution that then rolled back would be worse than a missing one.
	// Non-fatal for the same reason AttachPolicy is — SLA reporting must not
	// fail the resolution itself.
	if s.sla != nil {
		// See UpdateStatus: non-fatal, and reported by the boundary wrapper in
		// cmd/server rather than logged from the domain.
		_ = s.sla.RecordResolved(ctx, t, now)
	}

	_ = s.dispatcher.Dispatch(ctx, notification.Event{
		Type:           notification.EventTicketResolved,
		TicketID:       t.ID,
		ActorID:        actor.UserID,
		OccurredAt:     now,
		TrackingNumber: string(t.TrackingNumber),
		Recipient:      guestRecipient(t),
		GuestToken:     guestToken,
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
		return Ticket{}, fmt.Errorf("cannot link a ticket to itself")
	}

	var t Ticket
	var guestToken string
	now := time.Now()

	if err := s.atomic.InTx(ctx, func(st Store, au audit.Store) error {
		// Read target ticket to get its tracking number for default notes.
		target, err := st.GetByID(ctx, targetID)
		if err != nil {
			return err
		}

		// Apply default notes if empty.
		if strings.TrimSpace(notes) == "" {
			notes = DuplicateResolutionNotes(target.TrackingNumber)
		}

		// Create the link first, so a duplicate link constraint violation fails
		// before the ticket is locked.
		link := TicketLink{SourceTicketID: sourceID, TargetTicketID: targetID, LinkType: LinkDuplicateOf}
		if err := st.CreateLink(ctx, link); err != nil {
			return fmt.Errorf("creating duplicate link: %w", err)
		}

		// Resolve the source ticket.
		var txErr error
		t, guestToken, txErr = s.resolveInTx(ctx, st, au, sourceID, notes, actor, now)
		return txErr
	}); err != nil {
		return Ticket{}, err
	}

	// After the commit, dispatch events and record SLA, as Resolve does.
	if s.sla != nil {
		_ = s.sla.RecordResolved(ctx, t, now)
	}

	// Dispatch both the link event and the resolve event. t is the source
	// ticket (resolveInTx returns the row it just resolved), so its Subject
	// and TrackingNumber describe sourceID, the ticket TicketID names here.
	_ = s.dispatcher.Dispatch(ctx, notification.Event{
		Type:           notification.EventTicketLinked,
		TicketID:       sourceID,
		ActorID:        actor.UserID,
		Payload:        map[string]any{"target_id": targetID, "link_type": LinkDuplicateOf},
		OccurredAt:     now,
		TrackingNumber: string(t.TrackingNumber),
		Subject:        t.Subject,
	})

	_ = s.dispatcher.Dispatch(ctx, notification.Event{
		Type:           notification.EventTicketResolved,
		TicketID:       t.ID,
		ActorID:        actor.UserID,
		OccurredAt:     now,
		TrackingNumber: string(t.TrackingNumber),
		Recipient:      guestRecipient(t),
		GuestToken:     guestToken,
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
		// Revoke rather than rotate. A closed ticket accepts nothing, so a
		// link to it would grant a read of a thread that can no longer move —
		// and leaving credentials alive after the thing they reach is finished
		// is how a link ends up working two years later.
		if err := st.DeleteGuestTokensForTicket(ctx, t.ID); err != nil {
			return fmt.Errorf("revoking guest access: %w", err)
		}
		closed = 1
		return nil
	}); err != nil {
		return 0, err
	}

	// Skip and re-close dispatch nothing, same as before.
	if skipped || alreadyClosed {
		return 0, nil
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
	var guestToken string

	if err := s.atomic.InTx(ctx, func(st Store, au audit.Store) error {
		// Re-read under the lock. The check above answered the precondition on
		// an unlocked copy; this is the row actually being overwritten, and a
		// concurrent close or resolve may have landed in between.
		t, err = st.GetByIDForUpdate(ctx, ticketID)
		if err != nil {
			return err
		}
		if t.StatusID != s.sys.closedID {
			return fmt.Errorf("ticket is not closed")
		}
		before := ticketMap(t)
		oldStatusID = t.StatusID
		t.StatusID = targetStatusID
		t.UpdatedAt = now
		// Through the shared rule: its default arm clears ClosedAt/ResolvedAt
		// exactly as the two hand assignments did, and now also carries the
		// accumulated SLA pause forward and opens a fresh interval if the
		// reopen target is Pending — a reopened ticket is not a new SLA clock.
		applyStatusTimestamps(&t, oldStatusID, target, s.sys, now)

		if err := st.Update(ctx, t); err != nil {
			return fmt.Errorf("reopening ticket: %w", err)
		}
		if err := st.CreateStatusHistoryEntry(ctx, statusHistoryEntry(t.ID, &oldStatusID, targetStatusID, actor)); err != nil {
			return fmt.Errorf("recording reopen: %w", err)
		}
		if err := au.Create(ctx, auditEntry(actor.UserID, "ticket", t.ID, "reopened", before, ticketMap(t))); err != nil {
			return fmt.Errorf("auditing reopen: %w", err)
		}
		// A reopen issues a fresh link: closing revoked the old one, and a
		// customer who reopens needs a way back to the thread they reopened.
		if guestToken, err = rotateGuestToken(ctx, st, t); err != nil {
			return err
		}
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
		GuestToken:     guestToken,
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

// AddLink creates a directed link between two tickets.
func (s *Service) AddLink(ctx context.Context, sourceID, targetID uuid.UUID, lt LinkType, actor Actor) error {
	if sourceID == targetID {
		return fmt.Errorf("cannot link a ticket to itself")
	}
	// Validate LinkType before attempting to write to the database.
	if !lt.Valid() {
		return fmt.Errorf("%q: %w", lt, ErrInvalidLinkType)
	}
	link := TicketLink{SourceTicketID: sourceID, TargetTicketID: targetID, LinkType: lt}
	if err := s.store.CreateLink(ctx, link); err != nil {
		// Check if this is a duplicate link error
		if errors.Is(err, ErrLinkAlreadyExists) {
			return fmt.Errorf("creating link: %w", err)
		}
		return fmt.Errorf("creating link: %w", err)
	}
	// Read for Subject/TrackingNumber only — a webhook renderer needs a
	// human-readable line ("Linked to <target>") and the event otherwise
	// carries only ids. Best-effort: a read failure here must not undo the
	// link that already committed, so the event still dispatches, just
	// without those two fields, same as before this existed.
	var subject, tracking string
	if t, err := s.store.GetByID(ctx, sourceID); err == nil {
		subject = t.Subject
		tracking = string(t.TrackingNumber)
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
		return fmt.Errorf("status %q appears in %d past ticket transition(s) and cannot be deleted; deactivate it instead", st.Name, histCount)
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
