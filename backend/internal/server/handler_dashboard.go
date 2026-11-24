package server

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/dashboard"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

type dashboardQueue struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

// dashboardAttention is one row of the needs-attention list. Ticket is exactly
// what GET /tickets sends that caller for the ticket; Since is the instant the
// row's age is measured from, which for a reopened ticket is not otherwise sent.
type dashboardAttention struct {
	Reason dashboard.AttentionReason `json:"reason"`
	Since  time.Time                 `json:"since"`
	Ticket any                       `json:"ticket"`
}

type dashboardVolume struct {
	TZ   string             `json:"tz"`
	Days []ticket.DayVolume `json:"days"`
}

// dashboardResponse leaves Attention and Volume nil for a requester: null is
// "not part of your dashboard", and [] is "nothing to show".
type dashboardResponse struct {
	Queues    []dashboardQueue     `json:"queues"`
	Attention []dashboardAttention `json:"attention"`
	Volume    *dashboardVolume     `json:"volume"`
}

// handleGetDashboard is GET /api/v1/dashboard (#675): the queue strip, the
// needs-attention list and the 30-day trend, each computed by the path
// GET /tickets uses (TicketVisibility, ResolveQueue, the list's own WHERE), so
// there is no second rule for who may see what.
func (s *Server) handleGetDashboard(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	ctx := r.Context()

	tz, err := parseTZ(r)
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	fail := func(err error) {
		slog.ErrorContext(ctx, "dashboard", "error", err)
		Error(w, http.StatusInternalServerError, "internal_error", "an internal error occurred")
	}

	vis, err := s.TicketVisibility(ctx, a)
	if err != nil {
		fail(err)
		return
	}
	staff := a.Role == user.RoleStaff || a.Role == user.RoleAdmin
	now := time.Now()
	var resp dashboardResponse

	// The trend is asked first so a zone Postgres does not know is refused
	// before any other work is done.
	if staff {
		days, err := s.tickets.VolumeByDay(ctx, ticket.Filter{ActorID: a.UserID, Visibility: vis}, tz, now)
		if errors.Is(err, ticket.ErrUnknownTimeZone) {
			Error(w, http.StatusBadRequest, "bad_request", "unknown time zone")
			return
		}
		if err != nil {
			fail(err)
			return
		}
		resp.Volume = &dashboardVolume{TZ: tz, Days: days}
	}

	// A read failure is "off" for this response: the two sla_* cells are left
	// out rather than the whole dashboard failing.
	slaOn, err := s.adminSvc.SLAEnabled(ctx)
	if err != nil {
		slog.WarnContext(ctx, "reading SLA enabled setting failed; leaving SLA out of the dashboard", "error", err)
		slaOn = false
	}
	availableOK := s.AvailableQueueAllowed(ctx, a)

	resp.Queues = []dashboardQueue{}
	for _, c := range dashboard.Cells(a.Role, slaOn) {
		f := c.Filter
		f.ActorID = a.UserID
		f.Visibility = vis
		f.IncludeInternal = ticket.IncludeInternalFor(a.Role)
		if err := f.AllowedFor(a.Role, canSearchRequesters(a)); err != nil {
			fail(err)
			return
		}
		if f.Queue, err = ticket.ResolveQueue(c.Scope, f, a.Role, availableOK); err != nil {
			fail(err)
			return
		}
		n, err := s.tickets.CountFiltered(ctx, f)
		if err != nil {
			fail(err)
			return
		}
		resp.Queues = append(resp.Queues, dashboardQueue{Key: c.Key, Count: n})
	}

	if staff {
		if resp.Attention, err = s.dashboardAttention(r, a, vis, slaOn, availableOK, now); err != nil {
			fail(err)
			return
		}
	}
	JSON(w, http.StatusOK, resp)
}

// dashboardAttention is the needs-attention list: up to dashboard.AttentionLimit
// open tickets the caller can see, by reason in the order breached, at risk,
// unassigned, reopened.
func (s *Server) dashboardAttention(r *http.Request, a *authmw.Actor, vis ticket.Visibility, slaOn, availableOK bool, now time.Time) ([]dashboardAttention, error) {
	ctx := r.Context()

	statuses, err := s.tickets.ListStatuses(ctx)
	if err != nil {
		return nil, err
	}
	// Open is "not the Resolved or Closed system status", whether or not a
	// status is active: a ticket may sit in a status an administrator has since
	// retired.
	var open []uuid.UUID
	for _, st := range statuses {
		done := st.Kind == ticket.StatusKindSystem && (st.Name == ticket.StatusNameResolved || st.Name == ticket.StatusNameClosed)
		if !done {
			open = append(open, st.ID)
		}
	}

	base := ticket.Filter{
		ActorID:         a.UserID,
		Visibility:      vis,
		StatusIDs:       open,
		IncludeInternal: ticket.IncludeInternalFor(a.Role),
		Limit:           dashboard.AttentionLimit,
		Sort:            ticket.SortCreated,
		Order:           ticket.OrderAsc,
	}
	reasons := []struct {
		reason dashboard.AttentionReason
		scope  ticket.Scope
		on     bool
		tweak  func(*ticket.Filter)
	}{
		{dashboard.ReasonBreached, ticket.ScopeVisible, slaOn, func(f *ticket.Filter) { f.SLA = ticket.SLABreached }},
		{dashboard.ReasonAtRisk, ticket.ScopeVisible, slaOn, func(f *ticket.Filter) { f.SLA = ticket.SLAAtRisk }},
		{dashboard.ReasonUnassigned, ticket.ScopeUnassigned, availableOK, func(*ticket.Filter) {}},
		{dashboard.ReasonReopened, ticket.ScopeVisible, true, func(f *ticket.Filter) {
			since := now.Add(-dashboard.ReopenedWindow)
			f.ReopenedAfter = &since
			f.Sort, f.Order = ticket.SortUpdated, ticket.OrderDesc
		}},
	}

	var lists []dashboard.AttentionList
	for _, rs := range reasons {
		if !rs.on {
			continue
		}
		f := base
		rs.tweak(&f)
		if f.Queue, err = ticket.ResolveQueue(rs.scope, f, a.Role, availableOK); err != nil {
			return nil, err
		}
		ts, err := s.tickets.ListFiltered(ctx, f)
		if err != nil {
			return nil, err
		}
		lists = append(lists, dashboard.AttentionList{Reason: rs.reason, Tickets: ts})
	}

	items := dashboard.PickAttention(lists, dashboard.AttentionLimit)
	picked := make([]ticket.Ticket, len(items))
	for i, it := range items {
		picked[i] = it.Ticket
	}
	// The same shaping GET /tickets applies, in one batch.
	views, err := s.ticketViews(ctx, picked, a)
	if err != nil {
		return nil, err
	}
	out := make([]dashboardAttention, len(items))
	for i, it := range items {
		out[i] = dashboardAttention{Reason: it.Reason, Since: it.Since.UTC(), Ticket: viewerJSON(a, views[i])}
	}
	return out, nil
}
