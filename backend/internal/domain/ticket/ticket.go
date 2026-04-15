package ticket

import (
	"errors"
	"fmt"
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
// "OHD-2024-000001". It is unique across all tickets and never reused.
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
}

// Reply is a message on a ticket thread, from either a staff member or the
// original reporter. Internal replies are visible to staff only.
// NotifyCustomer controls whether a ticket-update email is sent to the reporter;
// it is always false for internal notes.
type Reply struct {
	ID             uuid.UUID  `json:"id"`
	TicketID       uuid.UUID  `json:"ticket_id"`
	AuthorID       *uuid.UUID `json:"author_id,omitempty"`
	GuestToken     *string    `json:"-"`
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
