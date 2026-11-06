package mcp

// Every tool is classified by whether its answer can contain a ticket. A tool
// that can must send it through ticketResult (the requester allowed list), and
// is driven as a requester by TestRequesterResponses_MCP in internal/server,
// which takes its tool list from TicketTools. A new tool fails
// TestEveryToolIsClassifiedForRequesterTicketFields until it is listed here.
var (
	ticketTools = []string{
		"get_ticket", "list_tickets", "create_ticket",
		"assign_ticket", "update_ticket_status", "create_follow_up",
	}
	nonTicketTools = []string{"add_reply", "list_categories", "list_statuses"}
)

// TicketTools returns the tools whose answers can contain a ticket.
func TicketTools() []string { return append([]string(nil), ticketTools...) }

// NonTicketTools returns the tools whose answers contain no ticket.
func NonTicketTools() []string { return append([]string(nil), nonTicketTools...) }
