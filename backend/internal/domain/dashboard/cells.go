// Package dashboard holds the data behind the dashboard's queue strip and
// needs-attention list (#675): which cells a role sees and how attention rows
// are picked. It knows tickets and users only; it does no HTTP and no SQL.
package dashboard

import (
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Cell is one number on the queue strip: the key the page looks its label and
// link up by, and the criteria its count is taken with. Each count is the
// listing GET /tickets would return for the same scope and filter. Queue and
// visibility are set by the handler, from the caller.
type Cell struct {
	Key    string
	Scope  ticket.Scope
	Filter ticket.Filter
}

// Cells is the queue strip for a role, in display order. Staff and
// administrators get their queues; the SLA cells only while SLA tracking is
// on. A requester gets one cell, their own tickets. Any role that is not staff
// or administrator is treated as a requester.
func Cells(role user.Role, slaOn bool) []Cell {
	if role != user.RoleStaff && role != user.RoleAdmin {
		return []Cell{
			{Key: "my_tickets", Scope: ticket.ScopeMine, Filter: ticket.Filter{ExcludeClosed: true}},
		}
	}
	cells := []Cell{
		{Key: "assigned_to_me", Scope: ticket.ScopeAssignedToMe, Filter: ticket.Filter{ExcludeClosed: true}},
		{Key: "my_groups", Scope: ticket.ScopeMyGroups, Filter: ticket.Filter{ExcludeClosed: true}},
	}
	if slaOn {
		cells = append(cells,
			Cell{Key: "sla_breached", Scope: ticket.ScopeAssignedToMe, Filter: ticket.Filter{ExcludeClosed: true, SLA: ticket.SLABreached}},
			Cell{Key: "sla_at_risk", Scope: ticket.ScopeAssignedToMe, Filter: ticket.Filter{ExcludeClosed: true, SLA: ticket.SLAAtRisk}},
		)
	}
	return append(cells,
		Cell{Key: "awaiting_my_reply", Scope: ticket.ScopeAssignedToMe, Filter: ticket.Filter{ExcludeClosed: true, AwaitingReply: true}},
	)
}
