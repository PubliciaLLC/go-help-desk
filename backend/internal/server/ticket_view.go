package server

import (
	"context"
	"net/http"
	"time"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// ticketView is a ticket as the API sends it: the domain ticket plus its
// computed SLA status. ticket.Ticket cannot carry sla.Status itself — sla
// imports ticket for Priority, so the reverse import would be a cycle — and
// the status is derived, not stored, so it belongs on the wire type, not the
// domain type.
type ticketView struct {
	ticket.Ticket
	SLA *sla.Status `json:"sla"` // nil: no matching policy, or SLA tracking is off
}

// ticketViews attaches each ticket's live SLA status via one batch call
// (sla.Service.StatusesFor), so the cost is two extra queries per response
// regardless of how many tickets are on the page. Callers pass the page
// after it has already been paged/sliced/merged — never before, which would
// compute SLA for rows the response throws away (see the staff "mine" scope
// merge in handleListTickets and CLAUDE.md's "Ticket list paging" note).
//
// When SLA tracking is off, every SLA field is nil and the store is never
// called: an instance with the feature disabled pays nothing for it.
func (s *Server) ticketViews(ctx context.Context, ts []ticket.Ticket) ([]ticketView, error) {
	views := make([]ticketView, len(ts))
	for i, t := range ts {
		views[i] = ticketView{Ticket: t}
	}
	if !s.adminSvc.SLAEnabled(ctx) {
		return views, nil
	}
	statuses, err := s.slaPolicies.StatusesFor(ctx, ts, time.Now())
	if err != nil {
		return nil, err
	}
	for i := range views {
		if st, ok := statuses[views[i].ID]; ok {
			st := st
			views[i].SLA = &st
		}
	}
	return views, nil
}

// writeTickets is handleListTickets' common exit: attach SLA status to the
// page it was given and write the result as JSON. Every list branch in that
// handler goes through here, so the wrapper runs exactly once per request.
func (s *Server) writeTickets(w http.ResponseWriter, r *http.Request, tickets []ticket.Ticket) {
	views, err := s.ticketViews(r.Context(), tickets)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, views)
}
