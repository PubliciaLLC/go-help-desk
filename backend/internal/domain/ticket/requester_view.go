package ticket

import (
	"encoding/json"
	"fmt"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// What a requester sees of a ticket (Erik, 2026-10-15): the current state and
// the conversation, not the history or the staff's working data. The list below
// was approved by Erik on 2026-10-15.
//
// This is an ALLOWED LIST applied to the response on its way out, not a list of
// things to remove. A field added to Ticket is hidden from requesters until it
// is added here on purpose; TestRequesterKeys_EveryTicketKeyIsClassified fails
// until it is placed on one of the two lists below.

// requesterTicketKeys are the Ticket JSON keys a requester (an account with the
// user role, or any role this does not recognise) receives.
//
//	id, tracking_number, subject, description   the ticket itself
//	status_id, priority                         its current state, both shown on the page
//	category_id, type_id, item_id               its classification (the page's Classification panel)
//	created_at, resolved_at                     when it was opened and resolved (the page shows both)
//	assignee_user_id                            who holds it, as an id only: a requester has no route that turns it into a name
//	resolution_notes                            sent to requesters before this list existed; their visibility is unchanged
var requesterTicketKeys = []string{
	"id", "tracking_number", "subject", "description",
	"status_id", "priority",
	"category_id", "type_id", "item_id",
	"created_at", "resolved_at",
	"assignee_user_id",
	"resolution_notes",
}

// requesterNeverTicketKeys are the Ticket JSON keys a requester does not
// receive, so that the test over Ticket's fields can tell "left out on purpose"
// from "nobody has decided yet".
//
//	assignee_group_id                     which team holds it: staff routing
//	reporter_user_id                      the requester's own id; the page never reads it
//	guest_email, guest_name, guest_phone  a guest's contact details; a requester's own tickets carry none
//	closed_at, updated_at                 history-like timestamps (Erik): when it was archived, when anything last touched it
//	pending_since, sla_paused_seconds     when and for how long the ticket waited: audit information, not state
//	reopen_count                          staff information (the Reopened badge is staff-only)
var requesterNeverTicketKeys = []string{
	"assignee_group_id", "reporter_user_id",
	"guest_email", "guest_name", "guest_phone",
	"closed_at", "updated_at",
	"pending_since", "sla_paused_seconds",
	"reopen_count",
}

// requesterWrapperKeys are the extra keys a response wraps around a ticket that
// a requester also receives. The ticket page reads none of them for a requester
// except through the conversation:
//
//	sla         already null for a requester; kept so the key is still there
//	can_reopen  always false for a requester; kept for the same reason
//	replies     already filtered by VisibleReplies (no internal notes)
//	links       ticket-to-ticket ids and a type: no content, and the REST links route sends the same to a requester
var requesterWrapperKeys = []string{"sla", "can_reopen", "replies", "links"}

// guestTicketKeys are the Ticket keys a guest receives: a subset of the
// requester's, since a guest has no account and needs only to read their own
// ticket by its tracking number. Plus guestWrapperKeys, which are not Ticket
// fields: the status's name (derived) and the conversation.
var guestTicketKeys = []string{"tracking_number", "subject", "description", "created_at"}
var guestWrapperKeys = []string{"status", "replies"}

// RequesterTicketKeys returns a copy of the allowed list.
func RequesterTicketKeys() []string { return append([]string(nil), requesterTicketKeys...) }

// RequesterNeverTicketKeys returns a copy of the explicit never list.
func RequesterNeverTicketKeys() []string { return append([]string(nil), requesterNeverTicketKeys...) }

// RequesterWrapperKeys returns a copy of the wrapper keys a requester keeps.
func RequesterWrapperKeys() []string { return append([]string(nil), requesterWrapperKeys...) }

// GuestTicketKeys returns a copy of the Ticket keys a guest receives.
func GuestTicketKeys() []string { return append([]string(nil), guestTicketKeys...) }

// FilterForViewer is the exit point for any response that carries tickets. For
// staff and administrators v is returned as it is. For anyone else (a requester,
// or a role this does not recognise: fail closed) v is encoded and, inside each
// top-level object, or each object of a top-level array, only the keys on the
// allowed list and the wrapper keys are kept. That covers one ticket, a list of
// tickets, and a ticket wrapped with extra fields. Values are not rewritten, and
// nested values (replies, links) are not looked into: they are not tickets.
func FilterForViewer(role user.Role, v any) (any, error) {
	if role == user.RoleStaff || role == user.RoleAdmin {
		return v, nil
	}
	return keepKeys(v, append(append([]string(nil), requesterTicketKeys...), requesterWrapperKeys...))
}

// FilterForGuest is FilterForViewer for the guest's view, which is a subset of
// the requester's.
func FilterForGuest(v any) (any, error) {
	return keepKeys(v, append(append([]string(nil), guestTicketKeys...), guestWrapperKeys...))
}

func keepKeys(v any, keep []string) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encoding response: %w", err)
	}
	allowed := make(map[string]bool, len(keep))
	for _, k := range keep {
		allowed[k] = true
	}
	var top any
	if err := json.Unmarshal(b, &top); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	filter := func(o map[string]any) {
		for k := range o {
			if !allowed[k] {
				delete(o, k)
			}
		}
	}
	switch x := top.(type) {
	case map[string]any:
		filter(x)
	case []any:
		for _, e := range x {
			if o, ok := e.(map[string]any); ok {
				filter(o)
			}
		}
	default:
		return v, nil
	}
	out, err := json.Marshal(top)
	if err != nil {
		return nil, fmt.Errorf("encoding filtered response: %w", err)
	}
	return json.RawMessage(out), nil
}
