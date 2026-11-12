// Package ticketstore implements domain/ticket.Store and domain/ticket.StatusStore
// against PostgreSQL via sqlc.
package ticketstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// Store implements ticket.Store and ticket.StatusStore.
type Store struct {
	q            *dbgen.Queries
	replyCap     int
	replyTimeout time.Duration
}

const (
	// DefaultReplyCap is how many of the newest matching replies a reply search
	// looks at (see reply_hits in ListTicketsFiltered).
	DefaultReplyCap = 2000
	// DefaultReplyTimeout is how long a search that includes replies may run
	// before the reply arm is dropped and the page is answered without it.
	//
	// Five seconds is the owner's decision of 2026-11-11 ("Longer limit (e.g.
	// 5 s)"). It was two. At 5,000,000 replies a word in 1% of them or more took
	// longer than two seconds on the machine measured, so those searches were
	// answered from ticket text; five lets more of them finish. A request that
	// still runs out costs the limit plus the ticket-only search, because the
	// statement is answered again without the reply arm. The cap above is
	// unchanged.
	DefaultReplyTimeout = 5 * time.Second
)

// New returns a Store backed by the given Queries.
// pageInt32 narrows a page bound to what the generated queries accept.
//
// sqlc emits int32 for LIMIT and OFFSET. Converting a Go int directly meant an
// offset above MaxInt32 wrapped: 2147483648 went negative and Postgres answered
// "OFFSET must not be negative" as a 500, and 4294967296 wrapped to zero and
// silently returned the first page.
//
// Clamping here rather than at each caller is deliberate. The HTTP handler
// bounds its own input, but MCP's list_tickets takes an offset straight from a
// tool argument, and any future caller would have to remember. The conversion
// is the place the value stops fitting, so it is the place to make it fit.
func pageInt32(n int) int32 {
	if n < 0 {
		return 0
	}
	if n > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(n)
}

func New(q *dbgen.Queries) *Store {
	return &Store{q: q, replyCap: DefaultReplyCap, replyTimeout: DefaultReplyTimeout}
}

// WithReplyLimits returns a Store with a different reply cap and time limit, for
// a test that needs a cap of one or a limit that has already passed.
func (s *Store) WithReplyLimits(replyCap int, timeout time.Duration) *Store {
	c := *s
	c.replyCap, c.replyTimeout = replyCap, timeout
	return &c
}

// ── ticket.Store ────────────────────────────────────────────────────────────

func (s *Store) Create(ctx context.Context, t ticket.Ticket) error {
	return s.q.CreateTicket(ctx, dbgen.CreateTicketParams{
		ID:                t.ID,
		TrackingNumber:    string(t.TrackingNumber),
		Subject:           t.Subject,
		Description:       t.Description,
		DescriptionFormat: string(t.DescriptionFormat),
		SourceChannel:     string(t.SourceChannel),
		CategoryID:        t.CategoryID,
		TypeID:            database.NullUUID(t.TypeID),
		ItemID:            database.NullUUID(t.ItemID),
		Priority:          string(t.Priority),
		StatusID:          t.StatusID,
		AssigneeUserID:    database.NullUUID(t.AssigneeUserID),
		AssigneeGroupID:   database.NullUUID(t.AssigneeGroupID),
		ReporterUserID:    database.NullUUID(t.ReporterUserID),
		GuestEmail:        database.NullString(t.GuestEmail),
		GuestName:         t.GuestName,
		GuestPhone:        t.GuestPhone,
		CreatedAt:         t.CreatedAt,
		UpdatedAt:         t.UpdatedAt,
	})
}

func (s *Store) GetByID(ctx context.Context, id uuid.UUID) (ticket.Ticket, error) {
	row, err := s.q.GetTicketByID(ctx, id)
	if err != nil {
		return ticket.Ticket{}, wrapNotFound(err, "ticket", id.String())
	}
	return fromRow(ticketRow(row)), nil
}

// GetByIDForUpdate reads the ticket and holds a write lock on it until the
// transaction ends. Only meaningful inside one; on the pool it locks and
// releases immediately.
func (s *Store) GetByIDForUpdate(ctx context.Context, id uuid.UUID) (ticket.Ticket, error) {
	row, err := s.q.GetTicketByIDForUpdate(ctx, id)
	if err != nil {
		return ticket.Ticket{}, wrapNotFound(err, "ticket", id.String())
	}
	return fromRow(ticketRow(row)), nil
}

func (s *Store) GetByTrackingNumber(ctx context.Context, tn ticket.TrackingNumber) (ticket.Ticket, error) {
	row, err := s.q.GetTicketByTrackingNumber(ctx, string(tn))
	if err != nil {
		return ticket.Ticket{}, wrapNotFound(err, "ticket", string(tn))
	}
	return fromRow(ticketRow(row)), nil
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
		ID:                        t.ID,
		Subject:                   t.Subject,
		Description:               t.Description,
		TypeID:                    database.NullUUID(t.TypeID),
		ItemID:                    database.NullUUID(t.ItemID),
		Priority:                  string(t.Priority),
		StatusID:                  t.StatusID,
		AssigneeUserID:            database.NullUUID(t.AssigneeUserID),
		AssigneeGroupID:           database.NullUUID(t.AssigneeGroupID),
		ResolutionNotes:           database.NullString(t.ResolutionNotes),
		ResolvedAt:                database.NullTime(t.ResolvedAt),
		ClosedAt:                  database.NullTime(t.ClosedAt),
		UpdatedAt:                 time.Now(),
		PendingSince:              database.NullTime(t.PendingSince),
		SlaPausedSeconds:          t.SLAPausedSeconds,
		ReopenCount:               int32(t.ReopenCount),
		LastReopenedAt:            database.NullTime(t.LastReopenedAt),
		PausedSecondsAtLastReopen: t.PausedSecondsAtLastReopen,
	})
}

// searchPattern builds the ILIKE pattern for a user-supplied search term.
// Tracking-number prefixes (e.g. "GHD-" or "GHD-2025-000") use a suffix
// wildcard only; everything else is wrapped in %…% for substring matching.
func searchPattern(q string) string {
	upper := strings.ToUpper(strings.TrimSpace(q))
	if len(upper) > 0 && upper[0] >= 'A' && upper[0] <= 'Z' && strings.Contains(upper, "-") {
		return upper + "%"
	}
	return "%" + q + "%"
}

// searchTokenPattern matches whole words in any script (Unicode letters and
// digits), not just ASCII — a plain [A-Za-z0-9]+ would silently drop every
// non-Latin search term (Cyrillic, CJK, Hebrew, Arabic, ...), regressing
// behind the substring search this replaced.
var searchTokenPattern = regexp.MustCompile(`[\p{L}\p{N}]+`)

// maxSearchTokens and maxSearchTokenRunes bound the tsquery built from user
// input. Without a cap, a long unbroken string (a pasted log line, a JWT) or
// a huge word count can exceed Postgres's tsquery operand/size limits and
// turn a search request into a 500 instead of a search. Both limits are far
// beyond anything a real search phrase needs.
const (
	maxSearchTokens     = 10
	maxSearchTokenRunes = 64
)

// buildSearchTSQuery converts free-text user input into a Postgres tsquery
// string that prefix-matches every word, ANDed together — e.g. "print jam"
// becomes "print:* & jam:*", which is what makes "search as you type" work
// on partial words instead of only whole ones. Returns "" when q has no
// indexable tokens (e.g. pure punctuation); callers pass that straight
// through as the search_query param, and the query guards on it being
// non-empty before ever calling to_tsquery (an empty tsquery string is a
// syntax error in Postgres, not a "match nothing" query).
func buildSearchTSQuery(q string) string {
	tokens := searchTokenPattern.FindAllString(q, -1)
	if len(tokens) == 0 {
		return ""
	}
	if len(tokens) > maxSearchTokens {
		tokens = tokens[:maxSearchTokens]
	}
	for i, t := range tokens {
		t = strings.ToLower(t)
		if runes := []rune(t); len(runes) > maxSearchTokenRunes {
			t = string(runes[:maxSearchTokenRunes])
		}
		tokens[i] = t + ":*"
	}
	return strings.Join(tokens, " & ")
}

func (s *Store) ListResolvedBefore(ctx context.Context, before time.Time, resolvedStatusID uuid.UUID, limit int) ([]ticket.Ticket, error) {
	rows, err := s.q.ListResolvedTicketsBefore(ctx, dbgen.ListResolvedTicketsBeforeParams{
		ResolvedAt: sql.NullTime{Time: before, Valid: true},
		StatusID:   resolvedStatusID,
		Limit:      pageInt32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("listing resolved tickets before %v: %w", before, err)
	}
	out := make([]ticket.Ticket, len(rows))
	for i, r := range rows {
		out[i] = fromRow(ticketRow(r))
	}
	return out, nil
}

func (s *Store) NextSeq(ctx context.Context) (int64, error) {
	return s.q.NextTicketSeq(ctx)
}

func (s *Store) CreateReply(ctx context.Context, r ticket.Reply) error {
	return s.q.CreateReply(ctx, dbgen.CreateReplyParams{
		ID:             r.ID,
		TicketID:       r.TicketID,
		AuthorID:       database.NullUUID(r.AuthorID),
		Body:           r.Body,
		Internal:       r.Internal,
		NotifyCustomer: r.NotifyCustomer,
		CreatedAt:      r.CreatedAt,
		BodyFormat:     string(r.BodyFormat),
	})
}

// UnassignForUser clears the assignee on every open ticket held by a user,
// and returns how many moved.
// UserExists reports whether a live account holds this id.
func (s *Store) UserExists(ctx context.Context, userID uuid.UUID) (bool, error) {
	ok, err := s.q.UserExists(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("checking reporter: %w", err)
	}
	return ok, nil
}

// CTIIsCoherent reports whether the classification hangs together: the type
// belongs to the category and the item belongs to the type.
func (s *Store) CTIIsCoherent(ctx context.Context, categoryID uuid.UUID, typeID, itemID *uuid.UUID) (bool, error) {
	ok, err := s.q.CTIIsCoherent(ctx, dbgen.CTIIsCoherentParams{
		CategoryID: categoryID,
		TypeID:     database.NullUUID(typeID),
		ItemID:     database.NullUUID(itemID),
	})
	if err != nil {
		return false, fmt.Errorf("checking classification: %w", err)
	}
	return ok.Bool, nil
}

// ArchivedCTINode names the first archived level among the ids given
// ("category", "type" or "item"), or "" when none is. A nil id is not checked.
func (s *Store) ArchivedCTINode(ctx context.Context, categoryID, typeID, itemID *uuid.UUID) (string, error) {
	level, err := s.q.ArchivedCTINode(ctx, dbgen.ArchivedCTINodeParams{
		CategoryID: database.NullUUID(categoryID),
		TypeID:     database.NullUUID(typeID),
		ItemID:     database.NullUUID(itemID),
	})
	if err != nil {
		return "", fmt.Errorf("checking for archived classification: %w", err)
	}
	return level, nil
}

// CategoryExists reports whether a category id is real.
func (s *Store) CategoryExists(ctx context.Context, categoryID uuid.UUID) (bool, error) {
	ok, err := s.q.CategoryExists(ctx, categoryID)
	if err != nil {
		return false, fmt.Errorf("checking category: %w", err)
	}
	return ok, nil
}

// IsAssignableUser reports whether a user can be given a ticket.
func (s *Store) IsAssignableUser(ctx context.Context, userID uuid.UUID) (bool, error) {
	// No row means not assignable. The query takes a share lock on the row it
	// finds, so a concurrent delete waits for this transaction rather than
	// slipping in between the check and the write.
	if _, err := s.q.IsAssignableUser(ctx, userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("checking assignee: %w", err)
	}
	return true, nil
}

// IsAssignableGroup reports whether a group exists.
func (s *Store) IsAssignableGroup(ctx context.Context, groupID uuid.UUID) (bool, error) {
	ok, err := s.q.IsAssignableGroup(ctx, groupID)
	if err != nil {
		return false, fmt.Errorf("checking assignee group: %w", err)
	}
	return ok, nil
}

func (s *Store) UnassignForUser(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	ids, err := s.q.UnassignTicketsForUser(ctx, database.NullUUID(&userID))
	if err != nil {
		return nil, fmt.Errorf("unassigning tickets: %w", err)
	}
	return ids, nil
}

// UnassignForGroup takes every ticket off a group, and returns their ids.
// The group row is locked first (LockGroup), so the unassign and the delete
// that follows in the same transaction see the same set of tickets.
func (s *Store) UnassignForGroup(ctx context.Context, groupID uuid.UUID) ([]uuid.UUID, error) {
	if _, err := s.q.LockGroup(ctx, groupID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("group %s: %w", groupID, group.ErrNotFound)
		}
		return nil, fmt.Errorf("locking group %s: %w", groupID, err)
	}
	ids, err := s.q.UnassignTicketsForGroup(ctx, database.NullUUID(&groupID))
	if err != nil {
		return nil, fmt.Errorf("unassigning tickets from group %s: %w", groupID, err)
	}
	return ids, nil
}

// DeleteGroup removes the group row. See ticket.Service.DeleteGroup for why a
// group is deleted from the ticket store.
func (s *Store) DeleteGroup(ctx context.Context, groupID uuid.UUID) error {
	n, err := s.q.DeleteGroup(ctx, groupID)
	if err != nil {
		return fmt.Errorf("deleting group %s: %w", groupID, err)
	}
	if n == 0 {
		return fmt.Errorf("group %s: %w", groupID, group.ErrNotFound)
	}
	return nil
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
			AuthorName:     r.AuthorDisplayName.String,
			Body:           r.Body,
			BodyFormat:     ticket.TextFormat(r.BodyFormat),
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

		DetectedMime:    database.NullString(a.DetectedMime),
		Sha256:          database.NullString(a.SHA256),
		VirusName:       database.NullString(a.VirusName),
		ContentMismatch: database.NullBool(a.ContentMismatch),
		UploadedByGuest: a.UploadedByGuest,
	})
}

func (s *Store) QuarantinedTicketIDs(ctx context.Context, ticketIDs []uuid.UUID) ([]uuid.UUID, error) {
	ids, err := s.q.ListQuarantinedTicketIDs(ctx, ticketIDs)
	if err != nil {
		return nil, fmt.Errorf("listing tickets with quarantined attachments: %w", err)
	}
	return ids, nil
}

func (s *Store) CountGuestAttachments(ctx context.Context, ticketID uuid.UUID) (int, error) {
	n, err := s.q.CountGuestAttachments(ctx, ticketID)
	return int(n), err
}

func (s *Store) GetAttachmentByID(ctx context.Context, id uuid.UUID) (ticket.Attachment, error) {
	r, err := s.q.GetAttachmentByID(ctx, id)
	if err != nil {
		return ticket.Attachment{}, wrapNotFound(err, "attachment", id.String())
	}
	return ticket.Attachment{
		ID:          r.ID,
		TicketID:    r.TicketID,
		Filename:    r.Filename,
		MimeType:    r.MimeType,
		SizeBytes:   r.SizeBytes,
		StoragePath: r.StoragePath,
		CreatedAt:   r.CreatedAt,

		DetectedMime:    database.StringPtr(r.DetectedMime),
		SHA256:          database.StringPtr(r.Sha256),
		VirusName:       database.StringPtr(r.VirusName),
		ContentMismatch: database.BoolPtr(r.ContentMismatch),
	}, nil
}

func (s *Store) AnyAttachmentExists(ctx context.Context) (bool, error) {
	ok, err := s.q.AnyAttachmentExists(ctx)
	if err != nil {
		return false, fmt.Errorf("checking for attachments: %w", err)
	}
	return ok, nil
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

			DetectedMime:    database.StringPtr(r.DetectedMime),
			SHA256:          database.StringPtr(r.Sha256),
			VirusName:       database.StringPtr(r.VirusName),
			ContentMismatch: database.BoolPtr(r.ContentMismatch),
		}
	}
	return out, nil
}

func (s *Store) DeleteAttachment(ctx context.Context, id uuid.UUID) error {
	return s.q.DeleteAttachment(ctx, id)
}

func (s *Store) CreateLink(ctx context.Context, link ticket.TicketLink) error {
	err := s.q.CreateTicketLink(ctx, dbgen.CreateTicketLinkParams{
		SourceTicketID: link.SourceTicketID,
		TargetTicketID: link.TargetTicketID,
		LinkType:       string(link.LinkType),
		FollowUp:       link.FollowUp,
	})
	if err != nil && isDuplicateLinkViolation(err) {
		return ticket.ErrLinkAlreadyExists
	}
	return err
}

// isDuplicateLinkViolation reports whether err is the ticket_links_unique
// constraint violation — detected off the typed Postgres error, not off
// err.Error()'s text (#195). The previous
// strings.Contains(err.Error(), "ticket_links_unique") &&
// strings.Contains(err.Error(), "23505") check happened to be correct for
// pgx stdlib's current Error() format, but it depended on that exact format:
// a driver change, or lib/pq (also in go.mod) instead of pgx, would silently
// turn every duplicate-link request back into an unhandled 500 with no
// compile-time or obvious runtime signal. errors.As sees through wrapping —
// see TestIsDuplicateLinkViolation for a case where the error arrives
// wrapped in additional context, which the old text-matching approach also
// happened to tolerate, but only by accident of where the substrings landed.
func isDuplicateLinkViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "ticket_links_unique"
}

func (s *Store) DeleteLink(ctx context.Context, source, target uuid.UUID, lt ticket.LinkType) (bool, error) {
	n, err := s.q.DeleteTicketLink(ctx, dbgen.DeleteTicketLinkParams{
		SourceTicketID: source,
		TargetTicketID: target,
		LinkType:       string(lt),
	})
	if err != nil {
		return false, fmt.Errorf("deleting link: %w", err)
	}
	return n > 0, nil
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
			FollowUp:       r.FollowUp,
		}
	}
	return out, nil
}

func (s *Store) CreateStatusHistoryEntry(ctx context.Context, e ticket.StatusHistoryEntry) error {
	return s.q.CreateStatusHistoryEntry(ctx, dbgen.CreateStatusHistoryEntryParams{
		ID:              e.ID,
		TicketID:        e.TicketID,
		FromStatusID:    database.NullUUID(e.FromStatusID),
		ToStatusID:      e.ToStatusID,
		ChangedByUserID: database.NullUUID(e.ChangedByUserID),
		CreatedAt:       e.CreatedAt,
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

// CreateStatus inserts a new status. statuses.name is TEXT NOT NULL UNIQUE
// (statuses_name_key); a colliding name is mapped to ticket.ErrStatusNameTaken
// here rather than reaching handleError as a raw pgconn error (#278).
func (s *Store) CreateStatus(ctx context.Context, st ticket.Status) error {
	err := s.q.CreateStatus(ctx, dbgen.CreateStatusParams{
		ID:        st.ID,
		Name:      st.Name,
		Kind:      string(st.Kind),
		SortOrder: int32(st.SortOrder),
		Color:     st.Color,
	})
	if err != nil && isStatusNameViolation(err) {
		return ticket.ErrStatusNameTaken
	}
	return err
}

// UpdateStatus persists a status's fields, including a rename. Same
// statuses_name_key collision as CreateStatus, mapped the same way (#278).
func (s *Store) UpdateStatus(ctx context.Context, st ticket.Status) error {
	err := s.q.UpdateStatus(ctx, dbgen.UpdateStatusParams{
		ID:        st.ID,
		Name:      st.Name,
		SortOrder: int32(st.SortOrder),
		Color:     st.Color,
		Active:    st.Active,
	})
	if err != nil && isStatusNameViolation(err) {
		return ticket.ErrStatusNameTaken
	}
	return err
}

// isStatusNameViolation reports whether err is the statuses_name_key unique
// constraint violation — detected off the typed Postgres error, never
// err.Error()'s text, for the reason isDuplicateLinkViolation gives (#195).
func isStatusNameViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "statuses_name_key"
}

// DeleteStatus hard-deletes a status. Service.RemoveStatus counts referencing
// tickets and status-history rows before calling this, but the count and the
// delete are separate statements, so a ticket can be PATCHed into this status
// (or transitioned through it) between them; the foreign key then refuses the
// DELETE, and that violation is mapped to ticket.ErrStatusInUse here rather
// than surfacing as a 500 — the same backstop slastore.DeletePolicy has for
// the identical count-then-delete race (#261), added here for #279.
func (s *Store) DeleteStatus(ctx context.Context, id uuid.UUID) error {
	if err := s.q.DeleteStatus(ctx, id); err != nil {
		if isStatusInUseViolation(err) {
			return fmt.Errorf("%w: a ticket referenced it while it was being deleted", ticket.ErrStatusInUse)
		}
		return err
	}
	return nil
}

// isStatusInUseViolation reports whether err is one of the three foreign keys
// that reference statuses(id) refusing a delete — tickets.status_id or either
// side of ticket_status_history — detected off the typed Postgres error,
// never err.Error()'s text, for the reason isDuplicateLinkViolation gives
// (#195).
func isStatusInUseViolation(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		return false
	}
	switch pgErr.ConstraintName {
	case "tickets_status_id_fkey", "ticket_status_history_from_status_id_fkey", "ticket_status_history_to_status_id_fkey":
		return true
	default:
		return false
	}
}

func (s *Store) CountByStatus(ctx context.Context, id uuid.UUID) (int64, error) {
	return s.q.CountTicketsByStatus(ctx, id)
}

// CountStatusHistoryByStatus counts past transitions mentioning a status, in
// either direction.
func (s *Store) CountStatusHistoryByStatus(ctx context.Context, id uuid.UUID) (int64, error) {
	return s.q.CountStatusHistoryByStatus(ctx, id)
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

// ticketRow mirrors every ticket read query's column list (the full tickets
// table minus the generated search_vector column, which is only ever
// referenced inside a WHERE/ORDER BY predicate, never fetched — selecting it
// on every ticket read would drag a tsvector comparable in size to the
// ticket's own text across the wire for no reason).
//
// Each query below explicitly lists these same columns rather than
// SELECT *, so sqlc generates a distinct Go struct per query (e.g.
// dbgen.GetTicketByIDRow, dbgen.ListTicketsFilteredRow, ...) instead of
// reusing dbgen.Ticket. All of them share this exact field name/type/order,
// so a plain Go type conversion (ticketRow(row)) safely reinterprets any of
// them as a ticketRow — see the Go spec on struct conversion (identical
// underlying types, tags ignored). That lets fromRow's mapping logic live
// in exactly one place instead of being duplicated per query.
type ticketRow struct {
	ID                        uuid.UUID
	TrackingNumber            string
	Subject                   string
	Description               string
	CategoryID                uuid.UUID
	TypeID                    uuid.NullUUID
	ItemID                    uuid.NullUUID
	Priority                  string
	StatusID                  uuid.UUID
	AssigneeUserID            uuid.NullUUID
	AssigneeGroupID           uuid.NullUUID
	ReporterUserID            uuid.NullUUID
	GuestEmail                sql.NullString
	ResolutionNotes           sql.NullString
	ResolvedAt                sql.NullTime
	ClosedAt                  sql.NullTime
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
	GuestName                 string
	GuestPhone                string
	PendingSince              sql.NullTime
	SlaPausedSeconds          int64
	ReopenCount               int32
	LastReopenedAt            sql.NullTime
	PausedSecondsAtLastReopen int64
	DescriptionFormat         string
	SourceChannel             string
}

func fromRow(r ticketRow) ticket.Ticket {
	return ticket.Ticket{
		ID:                        r.ID,
		TrackingNumber:            ticket.TrackingNumber(r.TrackingNumber),
		Subject:                   r.Subject,
		Description:               r.Description,
		DescriptionFormat:         ticket.TextFormat(r.DescriptionFormat),
		SourceChannel:             ticket.SourceChannel(r.SourceChannel),
		CategoryID:                r.CategoryID,
		TypeID:                    database.UUIDPtr(r.TypeID),
		ItemID:                    database.UUIDPtr(r.ItemID),
		Priority:                  ticket.Priority(r.Priority),
		StatusID:                  r.StatusID,
		AssigneeUserID:            database.UUIDPtr(r.AssigneeUserID),
		AssigneeGroupID:           database.UUIDPtr(r.AssigneeGroupID),
		ReporterUserID:            database.UUIDPtr(r.ReporterUserID),
		GuestEmail:                database.StringPtr(r.GuestEmail),
		GuestName:                 r.GuestName,
		GuestPhone:                r.GuestPhone,
		ResolutionNotes:           database.StringPtr(r.ResolutionNotes),
		ResolvedAt:                database.TimePtr(r.ResolvedAt),
		ClosedAt:                  database.TimePtr(r.ClosedAt),
		CreatedAt:                 r.CreatedAt,
		UpdatedAt:                 r.UpdatedAt,
		PendingSince:              database.TimePtr(r.PendingSince),
		SLAPausedSeconds:          r.SlaPausedSeconds,
		ReopenCount:               int(r.ReopenCount),
		LastReopenedAt:            database.TimePtr(r.LastReopenedAt),
		PausedSecondsAtLastReopen: r.PausedSecondsAtLastReopen,
	}
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

// The same value as the domain sentinel, so errors.Is works against either.
var ErrNotFound = ticket.ErrNotFound

func wrapNotFound(err error, kind, id string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %s %s", ErrNotFound, kind, id)
	}
	return fmt.Errorf("getting %s %s: %w", kind, id, err)
}

// TicketNotFoundError is the exact error GetByID and GetByTrackingNumber
// already produce for a ticket that genuinely does not exist, for a caller
// that must answer "exists, but you may not see it" with the same words —
// see server.ticketNotFound and #174. Building it through this one place
// rather than a second, hand-copied format string is what keeps the two
// bodies from drifting apart if wrapNotFound's shape ever changes.
func TicketNotFoundError(id string) error {
	return wrapNotFound(sql.ErrNoRows, "ticket", id)
}

// ListFiltered applies a ticket.Filter in a single statement. The visibility
// mode and the queue arrive already resolved: this translates them, it does not
// decide them. Every other criterion is ANDed with them, so a filter can only
// narrow what the caller may see.
func (s *Store) ListFiltered(ctx context.Context, f ticket.Filter) ([]ticket.Ticket, error) {
	return s.ListFilteredAt(ctx, f, time.Now())
}

// ListFilteredAt is ListFiltered with the instant the SLA filter and sort judge
// outstanding targets by. ListFiltered passes the wall clock; a test passes a
// fixed instant to put tickets exactly on the 80% and 100% boundaries.
func (s *Store) ListFilteredAt(ctx context.Context, f ticket.Filter, now time.Time) ([]ticket.Ticket, error) {
	res, err := s.searchFilteredAt(ctx, f, now)
	return res.Tickets, err
}

// ListFilteredWithMeta is ListFiltered plus what became of a request to search
// replies, in ticket.ReplySearchMeta (GET /tickets turns it into headers):
//
//   - Replies were not asked for, or the search has no words: the zero value.
//   - They were searched: Searched is true.
//   - They could not be, because the server is still filling and indexing them
//     after an upgrade: Searched is false and IndexBuilding is true. The reply arm
//     is SKIPPED, not run as a scan: without the index a scan parses every
//     reply, which at the sizes this is built for is minutes, and a half-filled
//     column would give answers that are silently incomplete.
//   - The reply arm ran out of its time limit (five seconds for the whole
//     statement): Searched is false. The statement is cancelled and answered
//     again without the reply arm, so the caller still gets the tickets that
//     match on their own text.
//
// A page chosen without the reply arm must show no reply excerpts
// (ReplySearchMeta.ExcerptsApply).
func (s *Store) ListFilteredWithMeta(ctx context.Context, f ticket.Filter) (ticket.ListResult, error) {
	return s.searchFilteredAt(ctx, f, time.Now())
}

func (s *Store) searchFilteredAt(ctx context.Context, f ticket.Filter, now time.Time) (ticket.ListResult, error) {
	if !f.SearchReplies || buildSearchTSQuery(f.Query) == "" {
		f.SearchReplies = false
		return s.listFilteredAt(ctx, f, now)
	}

	ready, err := s.q.ReplySearchReady(ctx)
	if err != nil {
		return ticket.ListResult{}, fmt.Errorf("checking reply search: %w", err)
	}
	if !ready {
		f.SearchReplies = false
		res, err := s.listFilteredAt(ctx, f, now)
		res.Replies = ticket.ReplySearchMeta{Searched: ptr(false), IndexBuilding: true}
		return res, err
	}

	limited, cancel := context.WithTimeout(ctx, s.replyTimeout)
	defer cancel()
	res, err := s.listFilteredAt(limited, f, now)
	switch {
	case err == nil:
		res.Replies = ticket.ReplySearchMeta{Searched: ptr(true)}
		return res, nil
	case ctx.Err() == nil && isTimeout(err):
		// Our limit, not the caller's: answer without the reply arm.
		f.SearchReplies = false
		res, err = s.listFilteredAt(ctx, f, now)
		res.Replies = ticket.ReplySearchMeta{Searched: ptr(false)}
		return res, err
	}
	return ticket.ListResult{}, err
}

func ptr[T any](v T) *T { return &v }

// isTimeout says whether err is a statement cancelled by a context deadline,
// however the driver words it.
func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "57014" // query_canceled
}

// listFilteredAt runs ListTicketsFiltered for f exactly as it stands.
func (s *Store) listFilteredAt(ctx context.Context, f ticket.Filter, now time.Time) (ticket.ListResult, error) {
	sortKey, sortDir := f.ResolvedSort()
	p := dbgen.ListTicketsFilteredParams{
		Now:           now,
		Sla:           string(f.SLA),
		SortKey:       string(sortKey),
		SortDir:       string(sortDir),
		Unrestricted:  f.Visibility == ticket.VisibilityAll,
		ReporterOnly:  f.Visibility == ticket.VisibilityReporter,
		ActorID:       f.ActorID,
		Queue:         queueName(f.Queue),
		ExcludeClosed: f.ExcludeClosed,
		// Arrays are never nil: the SQL guard is cardinality(), and an absent
		// filter is an empty array, not a NULL one.
		StatusIds:      f.AllStatusIDs(),
		Priorities:     priorityNames(f.AllPriorities()),
		SourceChannels: channelNames(f.SourceChannels),
		CategoryID:     database.NullUUID(f.CategoryID),
		TypeID:         database.NullUUID(f.TypeID),
		ItemID:         database.NullUUID(f.ItemID),
		// A NULL assignee filter means "any assignee", which is why this is a
		// pointer rather than uuid.Nil — the latter is a real, if absent, value.
		AssigneeUserID:  database.NullUUID(f.AssigneeUserID),
		AssigneeGroupID: database.NullUUID(f.AssigneeGroupID),
		ReporterID:      database.NullUUID(f.ReporterID),
		CreatedAfter:    database.NullTime(f.CreatedAfter),
		CreatedBefore:   database.NullTime(f.CreatedBefore),
		UpdatedAfter:    database.NullTime(f.UpdatedAfter),
		UpdatedBefore:   database.NullTime(f.UpdatedBefore),
		// Searching is whether a term was ASKED for, which is not the same as
		// the tsquery being non-empty: "???" tokenises to nothing but is still
		// a search, and must not quietly match every ticket.
		Searching:       strings.TrimSpace(f.Query) != "",
		SearchQuery:     buildSearchTSQuery(f.Query),
		TrackingPattern: searchPattern(f.Query),
		// Both come from the Filter, which the caller built from the actor's
		// role; ReplyMatches reads the same two for the snippets.
		SearchReplies:   f.SearchReplies,
		IncludeInternal: f.IncludeInternal,
		ReplyCap:        pageInt32(s.replyCap),
		ResultLimit:     pageInt32(f.Limit),
		ResultOffset:    pageInt32(f.Offset),
	}
	// The caller's groups, and what those groups cover, go in as plain arrays and
	// not as subqueries: see mine_group_ids and the scoped arms in the query for
	// why the planner needs to be handed them. Never nil, like the other arrays.
	// Read only for the cases that use them: the queue "mine", and staff under
	// scope enforcement.
	p.MineGroupIds = []uuid.UUID{}
	p.ScopeGroupIds = []uuid.UUID{}
	p.ScopeCategoryIds = []uuid.UUID{}
	p.ScopeTypeCategoryIds = []uuid.UUID{}
	p.ScopeTypeIds = []uuid.UUID{}
	scoped := f.Visibility == ticket.VisibilityScoped
	if f.Queue == ticket.QueueMine || scoped {
		groups, err := s.q.ListGroupsForUser(ctx, f.ActorID)
		if err != nil {
			return ticket.ListResult{}, fmt.Errorf("listing the caller's groups: %w", err)
		}
		for _, g := range groups {
			if f.Queue == ticket.QueueMine {
				p.MineGroupIds = append(p.MineGroupIds, g.ID)
			}
			if scoped {
				p.ScopeGroupIds = append(p.ScopeGroupIds, g.ID)
			}
		}
	}
	if scoped {
		rows, err := s.q.ListStaffScopeRows(ctx, f.ActorID)
		if err != nil {
			return ticket.ListResult{}, fmt.Errorf("listing the caller's group scopes: %w", err)
		}
		for _, r := range rows {
			if r.TypeID.Valid {
				p.ScopeTypeCategoryIds = append(p.ScopeTypeCategoryIds, r.CategoryID)
				p.ScopeTypeIds = append(p.ScopeTypeIds, r.TypeID.UUID)
			} else {
				p.ScopeCategoryIds = append(p.ScopeCategoryIds, r.CategoryID)
			}
		}
	}
	if r := strings.TrimSpace(f.Requester); r != "" {
		p.RequesterPattern = sql.NullString{String: "%" + likeEscaper.Replace(r) + "%", Valid: true}
	}

	rows, err := s.q.ListTicketsFiltered(ctx, p)
	if err != nil {
		return ticket.ListResult{}, fmt.Errorf("listing filtered tickets: %w", err)
	}
	out := make([]ticket.Ticket, len(rows))
	for i, r := range rows {
		out[i] = fromRow(ticketRow(r))
	}
	return ticket.ListResult{Tickets: out}, nil
}

// ReplyMatches reads, for the tickets of one page, the newest reply each matched
// through (ticket.ReplyMatch). It takes the Filter ListFiltered ran with, so the
// words searched and f.IncludeInternal are the ones that chose the page: a
// ticket that matched on its own subject must not show a snippet from an
// internal note the viewer may not read, so the guard is in this query too.
//
// A query with no searchable words, SearchReplies off or no ids reads nothing.
func (s *Store) ReplyMatches(ctx context.Context, f ticket.Filter, ticketIDs []uuid.UUID) (map[uuid.UUID]ticket.ReplyMatch, error) {
	out := map[uuid.UUID]ticket.ReplyMatch{}
	query := buildSearchTSQuery(f.Query)
	if !f.SearchReplies || query == "" || len(ticketIDs) == 0 {
		return out, nil
	}
	rows, err := s.q.ReplyMatchesForTickets(ctx, dbgen.ReplyMatchesForTicketsParams{
		SearchQuery:     query,
		TicketIds:       ticketIDs,
		IncludeInternal: f.IncludeInternal,
	})
	if err != nil {
		return nil, fmt.Errorf("reading reply matches: %w", err)
	}
	for _, r := range rows {
		out[r.TicketID] = ticket.ReplyMatch{
			ReplyID:   r.ReplyID,
			Internal:  r.Internal,
			CreatedAt: r.CreatedAt,
			Snippet:   cutSnippet(r.Snippet),
		}
	}
	return out, nil
}

// cutSnippet limits a snippet to ticket.MaxSnippetRunes. ts_headline keeps to
// about two dozen words, but a word can be 2,000 characters long. A cut that
// lands inside a highlight would leave it open, so it is closed.
func cutSnippet(s string) string {
	r := []rune(s)
	if len(r) <= ticket.MaxSnippetRunes {
		return s
	}
	cut := string(r[:ticket.MaxSnippetRunes])
	if strings.Count(cut, ticket.SnippetStart) > strings.Count(cut, ticket.SnippetStop) {
		// Reserve a rune for the marker so the result is still within the limit.
		cut = string(r[:ticket.MaxSnippetRunes-1]) + ticket.SnippetStop
	}
	return cut
}

// likeEscaper makes %, _ and the escape character itself match literally in an
// ILIKE pattern, so searching for "50%" finds "50%" rather than everyone. The
// same replacer the user lookup uses.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func queueName(q ticket.Queue) string {
	switch q {
	case ticket.QueueMine:
		return "mine"
	case ticket.QueueUnassigned:
		return "unassigned"
	}
	return "any"
}

func priorityNames(ps []ticket.Priority) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = string(p)
	}
	return out
}

func channelNames(cs []ticket.SourceChannel) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = string(c)
	}
	return out
}

// ── Guest access tokens ──────────────────────────────────────────────────────
//
// Only hashes reach this file. The raw token is generated in the domain layer,
// returned to the caller for the one email that carries it, and never stored.

func (s *Store) CreateGuestToken(ctx context.Context, id, ticketID uuid.UUID, hash string, expiresAt time.Time) error {
	if err := s.q.CreateGuestAccessToken(ctx, dbgen.CreateGuestAccessTokenParams{
		ID:        id,
		TicketID:  ticketID,
		TokenHash: hash,
		ExpiresAt: expiresAt,
	}); err != nil {
		return fmt.Errorf("creating guest access token: %w", err)
	}
	return nil
}

// TicketByGuestToken returns ticket.ErrGuestTokenNotFound for every reason a
// token does not resolve, so the caller has one case to answer and cannot leak
// which one it was.
func (s *Store) TicketByGuestToken(ctx context.Context, hash string) (ticket.Ticket, error) {
	row, err := s.q.GetTicketByGuestToken(ctx, hash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ticket.Ticket{}, ticket.ErrGuestTokenNotFound
		}
		return ticket.Ticket{}, fmt.Errorf("resolving guest token: %w", err)
	}
	return fromRow(ticketRow(row)), nil
}

func (s *Store) TouchGuestToken(ctx context.Context, hash string) error {
	if err := s.q.TouchGuestAccessToken(ctx, hash); err != nil {
		return fmt.Errorf("stamping guest token use: %w", err)
	}
	return nil
}

func (s *Store) DeleteGuestTokensForTicket(ctx context.Context, ticketID uuid.UUID) error {
	if err := s.q.DeleteGuestAccessTokensForTicket(ctx, ticketID); err != nil {
		return fmt.Errorf("deleting guest access tokens: %w", err)
	}
	return nil
}

// expiredTokenBatch is how many expired guest tokens one DELETE removes, as the
// audit retention purge does: the first sweep after an upgrade meets every
// expired row at once, and one statement for all of them would hold them all
// locked and write them to the WAL in one go.
const expiredTokenBatch = 5000

// DeleteExpiredGuestTokens deletes in batches. A short batch means nothing is
// left to take, or that someone else holds the rest (SKIP LOCKED): the next
// sweep gets those. The total is returned with an error too, because batches
// already committed stay deleted.
func (s *Store) DeleteExpiredGuestTokens(ctx context.Context) (int64, error) {
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, fmt.Errorf("deleting expired guest access tokens: %w", err)
		}
		n, err := s.q.DeleteExpiredGuestAccessTokens(ctx, expiredTokenBatch)
		if err != nil {
			return total, fmt.Errorf("deleting expired guest access tokens: %w", err)
		}
		total += n
		if n < expiredTokenBatch {
			return total, nil
		}
	}
}

func (s *Store) TicketIDByTrackingAndGuestEmail(ctx context.Context, tn ticket.TrackingNumber, email string) (uuid.UUID, error) {
	id, err := s.q.GetTicketIDByTrackingAndGuestEmail(ctx, dbgen.GetTicketIDByTrackingAndGuestEmailParams{
		TrackingNumber: string(tn),
		Lower:          email,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return uuid.Nil, ticket.ErrGuestTokenNotFound
		}
		return uuid.Nil, fmt.Errorf("resolving guest ticket: %w", err)
	}
	return id, nil
}
