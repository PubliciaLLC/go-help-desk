package ticket

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/user"
)

// Priority is one of the four configurable severity levels.
type Priority string

const (
	PriorityCritical Priority = "critical"
	PriorityHigh     Priority = "high"
	PriorityMedium   Priority = "medium"
	PriorityLow      Priority = "low"
)

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
)

// Status represents a ticket state. System statuses have special lifecycle
// rules; custom statuses are fully configurable by admins.
type Status struct {
	ID        uuid.UUID
	Name      string
	Kind      StatusKind
	SortOrder int
	Color     string // hex colour for the UI, e.g. "#10b981"
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
	SourceTicketID uuid.UUID
	TargetTicketID uuid.UUID
	LinkType       LinkType
}

// TrackingNumber is the human-readable identifier for a ticket, e.g.
// "OHD-2024-000001". It is unique across all tickets and never reused.
type TrackingNumber string

// Ticket is the central entity of the system. All business state lives here.
// Optional foreign keys are represented as pointers to make "not set" explicit.
type Ticket struct {
	ID              uuid.UUID
	TrackingNumber  TrackingNumber
	Subject         string
	Description     string
	CategoryID      uuid.UUID
	TypeID          *uuid.UUID
	ItemID          *uuid.UUID
	Priority        Priority
	StatusID        uuid.UUID
	AssigneeUserID  *uuid.UUID
	AssigneeGroupID *uuid.UUID
	ReporterUserID  *uuid.UUID // nil for guest-submitted tickets
	GuestEmail      *string   // set for guest-submitted tickets
	ResolutionNotes *string
	ResolvedAt      *time.Time
	ClosedAt        *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Reply is a message on a ticket thread, from either a staff member or the
// original reporter. Internal replies are visible to staff only.
type Reply struct {
	ID         uuid.UUID
	TicketID   uuid.UUID
	AuthorID   *uuid.UUID // nil for guest replies
	GuestToken *string    // token from the tracking-number email, nil for auth'd replies
	Body       string
	Internal   bool // staff-only note
	CreatedAt  time.Time
}

// Attachment stores file metadata. Bytes live on disk/object storage at StoragePath.
type Attachment struct {
	ID          uuid.UUID
	TicketID    uuid.UUID
	Filename    string
	MimeType    string
	SizeBytes   int64
	StoragePath string
	CreatedAt   time.Time
}

// GenerateTrackingNumber formats the canonical tracking number for a ticket.
// seq must be the globally-unique monotonic sequence value from the database.
func GenerateTrackingNumber(year int, seq int64) TrackingNumber {
	return TrackingNumber(fmt.Sprintf("OHD-%d-%06d", year, seq))
}

// Errors returned by rule functions.
var (
	ErrForbidden = errors.New("forbidden")
	ErrClosed    = errors.New("ticket is closed")
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
	if status.Name == StatusNameClosed {
		return ErrClosed
	}
	if status.Name == StatusNameResolved {
		if t.ResolvedAt == nil {
			// Resolved but no timestamp — treat as permanently resolved.
			return ErrForbidden
		}
		deadline := t.ResolvedAt.AddDate(0, 0, reopenWindowDays)
		if time.Now().After(deadline) {
			return ErrForbidden
		}
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
