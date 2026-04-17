// Package ticketstore implements domain/ticket.Store and domain/ticket.StatusStore
// against PostgreSQL via sqlc.
package ticketstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// Store implements ticket.Store and ticket.StatusStore.
type Store struct{ q *dbgen.Queries }

// New returns a Store backed by the given Queries.
func New(q *dbgen.Queries) *Store { return &Store{q: q} }

// ── ticket.Store ────────────────────────────────────────────────────────────

func (s *Store) Create(ctx context.Context, t ticket.Ticket) error {
	return s.q.CreateTicket(ctx, dbgen.CreateTicketParams{
		ID:              t.ID,
		TrackingNumber:  string(t.TrackingNumber),
		Subject:         t.Subject,
		Description:     t.Description,
		CategoryID:      t.CategoryID,
		TypeID:          database.NullUUID(t.TypeID),
		ItemID:          database.NullUUID(t.ItemID),
		Priority:        string(t.Priority),
		StatusID:        t.StatusID,
		AssigneeUserID:  database.NullUUID(t.AssigneeUserID),
		AssigneeGroupID: database.NullUUID(t.AssigneeGroupID),
		ReporterUserID:  database.NullUUID(t.ReporterUserID),
		GuestEmail:      database.NullString(t.GuestEmail),
		GuestName:       t.GuestName,
		GuestPhone:      t.GuestPhone,
		CreatedAt:       t.CreatedAt,
		UpdatedAt:       t.UpdatedAt,
	})
}

func (s *Store) GetByID(ctx context.Context, id uuid.UUID) (ticket.Ticket, error) {
	row, err := s.q.GetTicketByID(ctx, id)
	if err != nil {
		return ticket.Ticket{}, wrapNotFound(err, "ticket", id.String())
	}
	return fromRow(row), nil
}

func (s *Store) GetByTrackingNumber(ctx context.Context, tn ticket.TrackingNumber) (ticket.Ticket, error) {
	row, err := s.q.GetTicketByTrackingNumber(ctx, string(tn))
	if err != nil {
		return ticket.Ticket{}, wrapNotFound(err, "ticket", string(tn))
	}
	return fromRow(row), nil
}

func (s *Store) UpdateCTI(ctx context.Context, id, categoryID uuid.UUID, typeID, itemID *uuid.UUID) error {
	return s.q.UpdateTicketCTI(ctx, dbgen.UpdateTicketCTIParams{
		ID:         id,
		CategoryID: categoryID,
		TypeID:     database.NullUUID(typeID),
		ItemID:     database.NullUUID(itemID),
		UpdatedAt:  time.Now(),
	})
}

func (s *Store) Update(ctx context.Context, t ticket.Ticket) error {
	return s.q.UpdateTicket(ctx, dbgen.UpdateTicketParams{
		ID:              t.ID,
		Subject:         t.Subject,
		Description:     t.Description,
		TypeID:          database.NullUUID(t.TypeID),
		ItemID:          database.NullUUID(t.ItemID),
		Priority:        string(t.Priority),
		StatusID:        t.StatusID,
		AssigneeUserID:  database.NullUUID(t.AssigneeUserID),
		AssigneeGroupID: database.NullUUID(t.AssigneeGroupID),
		ResolutionNotes: database.NullString(t.ResolutionNotes),
		ResolvedAt:      database.NullTime(t.ResolvedAt),
		ClosedAt:        database.NullTime(t.ClosedAt),
		UpdatedAt:       time.Now(),
	})
}

// searchPattern builds the ILIKE pattern for a user-supplied search term.
// Tracking-number prefixes (e.g. "OHD-" or "OHD-2025-000") use a suffix
// wildcard only; everything else is wrapped in %…% for substring matching.
func searchPattern(q string) string {
	upper := strings.ToUpper(strings.TrimSpace(q))
	if len(upper) > 0 && upper[0] >= 'A' && upper[0] <= 'Z' && strings.Contains(upper, "-") {
		return upper + "%"
	}
	return "%" + q + "%"
}

func (s *Store) SearchByReporter(ctx context.Context, userID uuid.UUID, q string, limit, offset int) ([]ticket.Ticket, error) {
	rows, err := s.q.SearchTicketsByReporter(ctx, dbgen.SearchTicketsByReporterParams{
		ReporterUserID: database.NullUUID(&userID),
		Limit:          int32(limit),
		Offset:         int32(offset),
		TrackingNumber: searchPattern(q),
	})
	if err != nil {
		return nil, fmt.Errorf("searching tickets by reporter: %w", err)
	}
	return fromRows(rows), nil
}

func (s *Store) SearchByAssigneeUser(ctx context.Context, userID uuid.UUID, q string, limit, offset int) ([]ticket.Ticket, error) {
	rows, err := s.q.SearchTicketsByAssigneeUser(ctx, dbgen.SearchTicketsByAssigneeUserParams{
		AssigneeUserID: database.NullUUID(&userID),
		Limit:          int32(limit),
		Offset:         int32(offset),
		TrackingNumber: searchPattern(q),
	})
	if err != nil {
		return nil, fmt.Errorf("searching tickets by assignee user: %w", err)
	}
	return fromRows(rows), nil
}

func (s *Store) SearchByAssigneeGroup(ctx context.Context, groupID uuid.UUID, q string, limit, offset int) ([]ticket.Ticket, error) {
	rows, err := s.q.SearchTicketsByAssigneeGroup(ctx, dbgen.SearchTicketsByAssigneeGroupParams{
		AssigneeGroupID: database.NullUUID(&groupID),
		Limit:           int32(limit),
		Offset:          int32(offset),
		TrackingNumber:  searchPattern(q),
	})
	if err != nil {
		return nil, fmt.Errorf("searching tickets by assignee group: %w", err)
	}
	return fromRows(rows), nil
}

func (s *Store) ListByReporter(ctx context.Context, userID uuid.UUID, limit, offset int) ([]ticket.Ticket, error) {
	rows, err := s.q.ListTicketsByReporter(ctx, dbgen.ListTicketsByReporterParams{
		ReporterUserID: database.NullUUID(&userID),
		Limit:          int32(limit),
		Offset:         int32(offset),
	})
	if err != nil {
		return nil, fmt.Errorf("listing tickets by reporter: %w", err)
	}
	return fromRows(rows), nil
}

func (s *Store) ListByAssigneeUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]ticket.Ticket, error) {
	rows, err := s.q.ListTicketsByAssigneeUser(ctx, dbgen.ListTicketsByAssigneeUserParams{
		AssigneeUserID: database.NullUUID(&userID),
		Limit:          int32(limit),
		Offset:         int32(offset),
	})
	if err != nil {
		return nil, fmt.Errorf("listing tickets by assignee user: %w", err)
	}
	return fromRows(rows), nil
}

func (s *Store) ListByAssigneeGroup(ctx context.Context, groupID uuid.UUID, limit, offset int) ([]ticket.Ticket, error) {
	rows, err := s.q.ListTicketsByAssigneeGroup(ctx, dbgen.ListTicketsByAssigneeGroupParams{
		AssigneeGroupID: database.NullUUID(&groupID),
		Limit:           int32(limit),
		Offset:          int32(offset),
	})
	if err != nil {
		return nil, fmt.Errorf("listing tickets by assignee group: %w", err)
	}
	return fromRows(rows), nil
}

func (s *Store) ListByStatus(ctx context.Context, statusID uuid.UUID, limit, offset int) ([]ticket.Ticket, error) {
	rows, err := s.q.ListTicketsByStatus(ctx, dbgen.ListTicketsByStatusParams{
		StatusID: statusID,
		Limit:    int32(limit),
		Offset:   int32(offset),
	})
	if err != nil {
		return nil, fmt.Errorf("listing tickets by status: %w", err)
	}
	return fromRows(rows), nil
}

func (s *Store) ListAll(ctx context.Context, limit, offset int) ([]ticket.Ticket, error) {
	rows, err := s.q.ListAllTickets(ctx, dbgen.ListAllTicketsParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		return nil, fmt.Errorf("listing all tickets: %w", err)
	}
	return fromRows(rows), nil
}

func (s *Store) SearchAll(ctx context.Context, q string, limit, offset int) ([]ticket.Ticket, error) {
	rows, err := s.q.SearchAllTickets(ctx, dbgen.SearchAllTicketsParams{
		Limit:          int32(limit),
		Offset:         int32(offset),
		TrackingNumber: searchPattern(q),
	})
	if err != nil {
		return nil, fmt.Errorf("searching all tickets: %w", err)
	}
	return fromRows(rows), nil
}

func (s *Store) ListUnassigned(ctx context.Context, limit, offset int) ([]ticket.Ticket, error) {
	rows, err := s.q.ListUnassignedTickets(ctx, dbgen.ListUnassignedTicketsParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		return nil, fmt.Errorf("listing unassigned tickets: %w", err)
	}
	return fromRows(rows), nil
}

func (s *Store) SearchUnassigned(ctx context.Context, q string, limit, offset int) ([]ticket.Ticket, error) {
	rows, err := s.q.SearchUnassignedTickets(ctx, dbgen.SearchUnassignedTicketsParams{
		Limit:          int32(limit),
		Offset:         int32(offset),
		TrackingNumber: searchPattern(q),
	})
	if err != nil {
		return nil, fmt.Errorf("searching unassigned tickets: %w", err)
	}
	return fromRows(rows), nil
}

func (s *Store) ListResolvedBefore(ctx context.Context, before time.Time, limit int) ([]ticket.Ticket, error) {
	rows, err := s.q.ListResolvedTicketsBefore(ctx, dbgen.ListResolvedTicketsBeforeParams{
		ResolvedAt: sql.NullTime{Time: before, Valid: true},
		Limit:      int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("listing resolved tickets before %v: %w", before, err)
	}
	return fromRows(rows), nil
}

func (s *Store) NextSeq(ctx context.Context) (int64, error) {
	return s.q.NextTicketSeq(ctx)
}

func (s *Store) CreateReply(ctx context.Context, r ticket.Reply) error {
	return s.q.CreateReply(ctx, dbgen.CreateReplyParams{
		ID:             r.ID,
		TicketID:       r.TicketID,
		AuthorID:       database.NullUUID(r.AuthorID),
		GuestToken:     database.NullString(r.GuestToken),
		Body:           r.Body,
		Internal:       r.Internal,
		NotifyCustomer: r.NotifyCustomer,
		CreatedAt:      r.CreatedAt,
	})
}

func (s *Store) ListReplies(ctx context.Context, ticketID uuid.UUID) ([]ticket.Reply, error) {
	rows, err := s.q.ListReplies(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("listing replies: %w", err)
	}
	out := make([]ticket.Reply, len(rows))
	for i, r := range rows {
		out[i] = ticket.Reply{
			ID:             r.ID,
			TicketID:       r.TicketID,
			AuthorID:       database.UUIDPtr(r.AuthorID),
			GuestToken:     database.StringPtr(r.GuestToken),
			Body:           r.Body,
			Internal:       r.Internal,
			NotifyCustomer: r.NotifyCustomer,
			CreatedAt:      r.CreatedAt,
		}
	}
	return out, nil
}

func (s *Store) CreateAttachment(ctx context.Context, a ticket.Attachment) error {
	return s.q.CreateAttachment(ctx, dbgen.CreateAttachmentParams{
		ID:          a.ID,
		TicketID:    a.TicketID,
		Filename:    a.Filename,
		MimeType:    a.MimeType,
		SizeBytes:   a.SizeBytes,
		StoragePath: a.StoragePath,
		CreatedAt:   a.CreatedAt,
	})
}

func (s *Store) GetAttachmentByID(ctx context.Context, id uuid.UUID) (ticket.Attachment, error) {
	r, err := s.q.GetAttachmentByID(ctx, id)
	if err != nil {
		return ticket.Attachment{}, fmt.Errorf("getting attachment %s: %w", id, err)
	}
	return ticket.Attachment{
		ID:          r.ID,
		TicketID:    r.TicketID,
		Filename:    r.Filename,
		MimeType:    r.MimeType,
		SizeBytes:   r.SizeBytes,
		StoragePath: r.StoragePath,
		CreatedAt:   r.CreatedAt,
	}, nil
}

func (s *Store) ListAttachments(ctx context.Context, ticketID uuid.UUID) ([]ticket.Attachment, error) {
	rows, err := s.q.ListAttachments(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("listing attachments: %w", err)
	}
	out := make([]ticket.Attachment, len(rows))
	for i, r := range rows {
		out[i] = ticket.Attachment{
			ID:          r.ID,
			TicketID:    r.TicketID,
			Filename:    r.Filename,
			MimeType:    r.MimeType,
			SizeBytes:   r.SizeBytes,
			StoragePath: r.StoragePath,
			CreatedAt:   r.CreatedAt,
		}
	}
	return out, nil
}

func (s *Store) DeleteAttachment(ctx context.Context, id uuid.UUID) error {
	return s.q.DeleteAttachment(ctx, id)
}

func (s *Store) CreateLink(ctx context.Context, link ticket.TicketLink) error {
	return s.q.CreateTicketLink(ctx, dbgen.CreateTicketLinkParams{
		SourceTicketID: link.SourceTicketID,
		TargetTicketID: link.TargetTicketID,
		LinkType:       string(link.LinkType),
	})
}

func (s *Store) DeleteLink(ctx context.Context, source, target uuid.UUID, lt ticket.LinkType) error {
	return s.q.DeleteTicketLink(ctx, dbgen.DeleteTicketLinkParams{
		SourceTicketID: source,
		TargetTicketID: target,
		LinkType:       string(lt),
	})
}

func (s *Store) ListLinks(ctx context.Context, ticketID uuid.UUID) ([]ticket.TicketLink, error) {
	rows, err := s.q.ListTicketLinks(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("listing links: %w", err)
	}
	out := make([]ticket.TicketLink, len(rows))
	for i, r := range rows {
		out[i] = ticket.TicketLink{
			SourceTicketID: r.SourceTicketID,
			TargetTicketID: r.TargetTicketID,
			LinkType:       ticket.LinkType(r.LinkType),
		}
	}
	return out, nil
}

func (s *Store) CreateStatusHistoryEntry(ctx context.Context, e ticket.StatusHistoryEntry) error {
	return s.q.CreateStatusHistoryEntry(ctx, dbgen.CreateStatusHistoryEntryParams{
		ID:                e.ID,
		TicketID:          e.TicketID,
		FromStatusID:      database.NullUUID(e.FromStatusID),
		ToStatusID:        e.ToStatusID,
		ChangedByUserID:   database.NullUUID(e.ChangedByUserID),
		CreatedAt:         e.CreatedAt,
	})
}

func (s *Store) ListStatusHistory(ctx context.Context, ticketID uuid.UUID) ([]ticket.StatusHistoryEntry, error) {
	rows, err := s.q.ListTicketStatusHistory(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("listing status history: %w", err)
	}
	out := make([]ticket.StatusHistoryEntry, len(rows))
	for i, r := range rows {
		out[i] = ticket.StatusHistoryEntry{
			ID:              r.ID,
			TicketID:        r.TicketID,
			FromStatusID:    database.UUIDPtr(r.FromStatusID),
			FromStatusName:  r.FromStatusName,
			FromStatusColor: r.FromStatusColor,
			ToStatusID:      r.ToStatusID,
			ToStatusName:    r.ToStatusName,
			ToStatusColor:   r.ToStatusColor,
			ChangedByUserID: database.UUIDPtr(r.ChangedByUserID),
			ChangedByName:   r.ChangedByName,
			CreatedAt:       r.CreatedAt,
		}
	}
	return out, nil
}

// ── ticket.StatusStore ───────────────────────────────────────────────────────

func (s *Store) GetStatusByName(ctx context.Context, name string) (ticket.Status, error) {
	row, err := s.q.GetStatusByName(ctx, name)
	if err != nil {
		return ticket.Status{}, wrapNotFound(err, "status", name)
	}
	return statusFromRow(row), nil
}

func (s *Store) ListStatuses(ctx context.Context) ([]ticket.Status, error) {
	rows, err := s.q.ListStatuses(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing statuses: %w", err)
	}
	out := make([]ticket.Status, len(rows))
	for i, r := range rows {
		out[i] = statusFromRow(r)
	}
	return out, nil
}

func (s *Store) CreateStatus(ctx context.Context, st ticket.Status) error {
	return s.q.CreateStatus(ctx, dbgen.CreateStatusParams{
		ID:        st.ID,
		Name:      st.Name,
		Kind:      string(st.Kind),
		SortOrder: int32(st.SortOrder),
		Color:     st.Color,
	})
}

func (s *Store) UpdateStatus(ctx context.Context, st ticket.Status) error {
	return s.q.UpdateStatus(ctx, dbgen.UpdateStatusParams{
		ID:        st.ID,
		Name:      st.Name,
		SortOrder: int32(st.SortOrder),
		Color:     st.Color,
		Active:    st.Active,
	})
}

func (s *Store) DeleteStatus(ctx context.Context, id uuid.UUID) error {
	return s.q.DeleteStatus(ctx, id)
}

func (s *Store) CountByStatus(ctx context.Context, id uuid.UUID) (int64, error) {
	return s.q.CountTicketsByStatus(ctx, id)
}

func (s *Store) CountByStatusForReporter(ctx context.Context, statusID, userID uuid.UUID) (int64, error) {
	return s.q.CountTicketsByStatusForReporter(ctx, dbgen.CountTicketsByStatusForReporterParams{
		StatusID:       statusID,
		ReporterUserID: database.NullUUID(&userID),
	})
}

func (s *Store) CountByStatusForAssignee(ctx context.Context, statusID, userID uuid.UUID, groupIDs []uuid.UUID) (int64, error) {
	if groupIDs == nil {
		groupIDs = []uuid.UUID{}
	}
	return s.q.CountTicketsByStatusForAssignee(ctx, dbgen.CountTicketsByStatusForAssigneeParams{
		StatusID:       statusID,
		AssigneeUserID: database.NullUUID(&userID),
		GroupIds:       groupIDs,
	})
}

// ── helpers ──────────────────────────────────────────────────────────────────

func fromRow(r dbgen.Ticket) ticket.Ticket {
	return ticket.Ticket{
		ID:              r.ID,
		TrackingNumber:  ticket.TrackingNumber(r.TrackingNumber),
		Subject:         r.Subject,
		Description:     r.Description,
		CategoryID:      r.CategoryID,
		TypeID:          database.UUIDPtr(r.TypeID),
		ItemID:          database.UUIDPtr(r.ItemID),
		Priority:        ticket.Priority(r.Priority),
		StatusID:        r.StatusID,
		AssigneeUserID:  database.UUIDPtr(r.AssigneeUserID),
		AssigneeGroupID: database.UUIDPtr(r.AssigneeGroupID),
		ReporterUserID:  database.UUIDPtr(r.ReporterUserID),
		GuestEmail:      database.StringPtr(r.GuestEmail),
		GuestName:       r.GuestName,
		GuestPhone:      r.GuestPhone,
		ResolutionNotes: database.StringPtr(r.ResolutionNotes),
		ResolvedAt:      database.TimePtr(r.ResolvedAt),
		ClosedAt:        database.TimePtr(r.ClosedAt),
		CreatedAt:       r.CreatedAt,
		UpdatedAt:       r.UpdatedAt,
	}
}

func fromRows(rows []dbgen.Ticket) []ticket.Ticket {
	out := make([]ticket.Ticket, len(rows))
	for i, r := range rows {
		out[i] = fromRow(r)
	}
	return out
}

func statusFromRow(r dbgen.Status) ticket.Status {
	return ticket.Status{
		ID:        r.ID,
		Name:      r.Name,
		Kind:      ticket.StatusKind(r.Kind),
		SortOrder: int(r.SortOrder),
		Color:     r.Color,
		Active:    r.Active,
	}
}

var ErrNotFound = errors.New("not found")

func wrapNotFound(err error, kind, id string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %s %s", ErrNotFound, kind, id)
	}
	return fmt.Errorf("getting %s %s: %w", kind, id, err)
}
