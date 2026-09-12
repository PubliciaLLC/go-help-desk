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
	ListResolvedBefore(ctx context.Context, before time.Time, limit int) ([]Ticket, error)

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

	// Links
	CreateLink(ctx context.Context, link TicketLink) error
	DeleteLink(ctx context.Context, source, target uuid.UUID, lt LinkType) error
	ListLinks(ctx context.Context, ticketID uuid.UUID) ([]TicketLink, error)

	// Status history
	CreateStatusHistoryEntry(ctx context.Context, e StatusHistoryEntry) error
	ListStatusHistory(ctx context.Context, ticketID uuid.UUID) ([]StatusHistoryEntry, error)
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
