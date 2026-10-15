package ticket

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
)

// Atomic runs a set of writes against stores bound to a single transaction,
// committing when fn returns nil and rolling back otherwise.
//
// The ticket service's composite operations each write to several tables —
// the ticket row, its status history, the audit log — and before this they were
// independent statements. A failure partway through left the database
// half-applied, and because the history and audit writes discarded their
// errors, invisibly so: the audit trail could lose entries with no trace.
//
// fn receives both stores because atomicity has to span them. An audit entry
// committed separately from the change it describes is not an audit trail.
type Atomic interface {
	InTx(ctx context.Context, fn func(Store, audit.Store) error) error
}

// Store is the persistence interface for tickets and their sub-resources.
type Store interface {
	// Ticket CRUD
	Create(ctx context.Context, t Ticket) error
	GetByID(ctx context.Context, id uuid.UUID) (Ticket, error)

	// GetByIDForUpdate is GetByID holding a write lock until the transaction
	// ends. Lifecycle writes use it INSIDE their transaction so that the row
	// they mutate is the row they read: UpdateTicket overwrites every column,
	// so a copy read before the transaction is a lost update waiting for a
	// second writer.
	GetByIDForUpdate(ctx context.Context, id uuid.UUID) (Ticket, error)
	GetByTrackingNumber(ctx context.Context, tn TrackingNumber) (Ticket, error)
	Update(ctx context.Context, t Ticket) error
	UpdateCTI(ctx context.Context, id, categoryID uuid.UUID, typeID, itemID *uuid.UUID) error

	// Listings
	ListByReporter(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Ticket, error)
	ListByAssigneeUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Ticket, error)
	ListByAssigneeGroup(ctx context.Context, groupID uuid.UUID, limit, offset int) ([]Ticket, error)
	ListByStatus(ctx context.Context, statusID uuid.UUID, limit, offset int) ([]Ticket, error)
	ListAll(ctx context.Context, limit, offset int) ([]Ticket, error)
	ListUnassigned(ctx context.Context, limit, offset int) ([]Ticket, error)
	// UnassignForUser clears the assignee on every OPEN ticket held by a
	// user, and reports how many moved. Used when an account is deleted.
	UnassignForUser(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error)
	// UnassignForGroup clears the group on EVERY ticket held by it, open or
	// not, and returns their ids. It locks the group row first, so a ticket
	// cannot be assigned to the group between this and DeleteGroup; an unknown
	// group wraps group.ErrNotFound.
	UnassignForGroup(ctx context.Context, groupID uuid.UUID) ([]uuid.UUID, error)
	// DeleteGroup removes the group row. It lives on this store so a group can
	// be deleted in the same transaction as the audit entries for the tickets
	// it held (see Service.DeleteGroup).
	DeleteGroup(ctx context.Context, groupID uuid.UUID) error
	// IsAssignableUser and IsAssignableGroup are asked inside the assignment
	// transaction, so every caller gets the same answer and no caller has to
	// remember to ask.
	IsAssignableUser(ctx context.Context, userID uuid.UUID) (bool, error)
	// UserExists checks a supplied reporter before a tracking number is
	// taken, so an unknown one cannot leave a hole in the numbering.
	UserExists(ctx context.Context, userID uuid.UUID) (bool, error)
	// CategoryExists, for the same reason: the foreign key only speaks at
	// the INSERT, after the tracking number has been taken.
	CategoryExists(ctx context.Context, categoryID uuid.UUID) (bool, error)
	// CTIIsCoherent checks the type belongs to the category and the item to
	// the type — the pairings the foreign keys only enforce at the INSERT,
	// which is after the tracking number has been taken.
	CTIIsCoherent(ctx context.Context, categoryID uuid.UUID, typeID, itemID *uuid.UUID) (bool, error)
	// ArchivedCTINode names the first archived level among the ids given
	// ("category", "type" or "item"), or "" when none is; a nil id is not
	// checked. It is how "nobody newly picks an archived node" is decided.
	ArchivedCTINode(ctx context.Context, categoryID, typeID, itemID *uuid.UUID) (string, error)
	IsAssignableGroup(ctx context.Context, groupID uuid.UUID) (bool, error)
	// ListResolvedBefore lists tickets resolved before the given time.
	// resolvedStatusID must be the Resolved status's id: without it, a row
	// whose resolved_at predates the cutoff but whose status has since moved
	// on (or never was Resolved) would be listed forever. See #191.
	ListResolvedBefore(ctx context.Context, before time.Time, resolvedStatusID uuid.UUID, limit int) ([]Ticket, error)

	// Guest access tokens.
	//
	// On Store rather than an interface of their own because rotation has to
	// happen in the same transaction as the change that caused it — a status
	// change that rolls back must not leave the customer holding a dead link.
	// InTx hands the caller a Store, so that is where these have to live.
	//
	// Only hashes cross this boundary. The raw token exists in one email.
	CreateGuestToken(ctx context.Context, id, ticketID uuid.UUID, hash string, expiresAt time.Time) error
	TicketByGuestToken(ctx context.Context, hash string) (Ticket, error)
	TouchGuestToken(ctx context.Context, hash string) error
	DeleteGuestTokensForTicket(ctx context.Context, ticketID uuid.UUID) error
	TicketIDByTrackingAndGuestEmail(ctx context.Context, tn TrackingNumber, email string) (uuid.UUID, error)

	// Search — ILIKE across tracking_number, subject, description
	SearchByReporter(ctx context.Context, userID uuid.UUID, q string, limit, offset int) ([]Ticket, error)
	SearchByAssigneeUser(ctx context.Context, userID uuid.UUID, q string, limit, offset int) ([]Ticket, error)
	SearchByAssigneeGroup(ctx context.Context, groupID uuid.UUID, q string, limit, offset int) ([]Ticket, error)
	SearchAll(ctx context.Context, q string, limit, offset int) ([]Ticket, error)
	SearchUnassigned(ctx context.Context, q string, limit, offset int) ([]Ticket, error)

	// Scope-aware listing. Returns the tickets a staff member may see under
	// DESIGN.md's model — reported by them, assigned to them, assigned to one
	// of their groups, or within a Category/Type their groups cover.
	//
	// This has to be a query rather than a filter over another listing,
	// because callers paginate: filtering a fetched page returns short pages
	// and silently skips rows.
	ListVisibleToStaff(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Ticket, error)
	SearchVisibleToStaff(ctx context.Context, userID uuid.UUID, q string, limit, offset int) ([]Ticket, error)
	ListFiltered(ctx context.Context, f Filter) ([]Ticket, error)

	// Next sequence value for tracking-number generation
	NextSeq(ctx context.Context) (int64, error)

	// Replies
	CreateReply(ctx context.Context, r Reply) error
	ListReplies(ctx context.Context, ticketID uuid.UUID) ([]Reply, error)

	// Attachments
	CreateAttachment(ctx context.Context, a Attachment) error
	GetAttachmentByID(ctx context.Context, id uuid.UUID) (Attachment, error)
	ListAttachments(ctx context.Context, ticketID uuid.UUID) ([]Attachment, error)
	DeleteAttachment(ctx context.Context, id uuid.UUID) error
	// AnyAttachmentExists reports whether the instance holds any attachment.
	AnyAttachmentExists(ctx context.Context) (bool, error)

	// Links
	CreateLink(ctx context.Context, link TicketLink) error
	DeleteLink(ctx context.Context, source, target uuid.UUID, lt LinkType) error
	ListLinks(ctx context.Context, ticketID uuid.UUID) ([]TicketLink, error)

	// Followers (#441). On Store because following is audited, and the audit
	// entry must commit with the row it describes, which InTx hands out only
	// through a Store.
	//
	// AddFollower and RemoveFollower report whether they changed anything, so
	// a repeat is not audited twice.
	AddFollower(ctx context.Context, ticketID, userID uuid.UUID) (bool, error)
	RemoveFollower(ctx context.Context, ticketID, userID uuid.UUID) (bool, error)
	ListFollowers(ctx context.Context, ticketID uuid.UUID) ([]Follower, error)
	// ListMailableFollowerIDs: followers staff email can go to (active staff
	// or admin, never a service account). Whether they can still see the
	// ticket is the send-time check's question, not this one's.
	ListMailableFollowerIDs(ctx context.Context, ticketID uuid.UUID) ([]uuid.UUID, error)

	// Status history
	CreateStatusHistoryEntry(ctx context.Context, e StatusHistoryEntry) error
	ListStatusHistory(ctx context.Context, ticketID uuid.UUID) ([]StatusHistoryEntry, error)

	// SLA re-match (#443b). A priority change re-matches the ticket's SLA in
	// the ticket's own transaction, so its writes are reached through this
	// Store: ticket.Atomic hands out only a Store and an audit.Store, and the
	// sla package imports ticket, not the other way round. The sla package
	// keeps everything about elapsed time and colour; these only say which row
	// is current and where its clock starts.

	// CurrentSLARecordForUpdate returns the ticket's current record, locked
	// until the transaction ends. nil, nil means the ticket has none.
	CurrentSLARecordForUpdate(ctx context.Context, ticketID uuid.UUID) (*SLARecordState, error)
	// MatchSLAPolicy returns the policy that governs a ticket with this
	// priority and category. nil, nil means none does.
	MatchSLAPolicy(ctx context.Context, p Priority, categoryID uuid.UUID) (*uuid.UUID, error)
	// SupersedeSLARecord marks a record as replaced, writing superseded_at and
	// nothing else. Called before StartSLARecord: a ticket has one current
	// record at most.
	SupersedeSLARecord(ctx context.Context, id uuid.UUID, at time.Time) error
	// StartSLARecord inserts the record a re-match starts.
	StartSLARecord(ctx context.Context, r SLARecordStart) error
}

// StatusStore manages ticket statuses. Method names use a "Status" suffix to
// avoid collision with ticket.Store's Create/Update/Delete methods when a
// single concrete type implements both interfaces.
type StatusStore interface {
	GetStatusByName(ctx context.Context, name string) (Status, error)
	ListStatuses(ctx context.Context) ([]Status, error)
	CreateStatus(ctx context.Context, s Status) error
	UpdateStatus(ctx context.Context, s Status) error
	DeleteStatus(ctx context.Context, id uuid.UUID) error
	CountByStatus(ctx context.Context, id uuid.UUID) (int64, error)
	// CountStatusHistoryByStatus counts past transitions mentioning a status.
	// ticket_status_history has no ON DELETE action, so history alone can make
	// a status undeletable.
	CountStatusHistoryByStatus(ctx context.Context, id uuid.UUID) (int64, error)
	CountByStatusForReporter(ctx context.Context, statusID, userID uuid.UUID) (int64, error)
	CountByStatusForAssignee(ctx context.Context, statusID, userID uuid.UUID, groupIDs []uuid.UUID) (int64, error)
}
