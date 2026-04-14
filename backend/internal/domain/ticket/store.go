package ticket

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Store is the persistence interface for tickets and their sub-resources.
type Store interface {
	// Ticket CRUD
	Create(ctx context.Context, t Ticket) error
	GetByID(ctx context.Context, id uuid.UUID) (Ticket, error)
	GetByTrackingNumber(ctx context.Context, tn TrackingNumber) (Ticket, error)
	Update(ctx context.Context, t Ticket) error

	// Listings
	ListByReporter(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Ticket, error)
	ListByAssigneeUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Ticket, error)
	ListByAssigneeGroup(ctx context.Context, groupID uuid.UUID, limit, offset int) ([]Ticket, error)
	ListByStatus(ctx context.Context, statusID uuid.UUID, limit, offset int) ([]Ticket, error)
	ListResolvedBefore(ctx context.Context, before time.Time, limit int) ([]Ticket, error)

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
}
