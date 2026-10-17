package notification

import "slices"

// WebhookEvents is the list of event types that can be subscribed to via
// webhooks. guest.link_resent is excluded because webhooks are defined as HTTP
// callbacks for ticket lifecycle events; guest link resends are not ticket
// changes.
var WebhookEvents = []EventType{
	EventTicketCreated,
	EventTicketAssigned,
	EventTicketStatusChanged,
	EventTicketReplied,
	EventTicketNoteAdded,
	EventTicketResolved,
	EventTicketClosed,
	EventTicketReopened,
	EventTicketLinked,
	EventTicketPriorityChanged,
	EventTicketClassified,
}

// IsWebhookEvent reports whether e is "*" or one of WebhookEvents.
// Exact, case-sensitive, untrimmed: the same comparison a hook's subscription
// makes.
func IsWebhookEvent(e string) bool {
	return e == "*" || slices.Contains(WebhookEvents, EventType(e))
}

// MailsStaff reports whether the event can mean mail to staff: an assignment,
// a reopen, or a requester's reply. Who is mailed is decided by the worker
// when it picks the row up (#506), so the row has to be queued for any change
// that could mean someone.
func (e Event) MailsStaff() bool {
	for _, k := range e.AllKinds() {
		switch {
		case k == EventTicketAssigned, k == EventTicketReopened:
			return true
		case k == EventTicketReplied && e.FromRequester:
			return true
		}
	}
	return false
}

// Carries reports whether a channel can deliver an event at all. A row for an
// event its channel would discard is a write, a claim and a delete for
// nothing — and guest resend, which queues on every unauthenticated request,
// wrote one webhook row per request that way (#164 round 1).
func Carries(channel string, ev Event) bool {
	switch channel {
	case "webhook":
		// Any one kind is enough: the hook that subscribes to it gets the change.
		return slices.ContainsFunc(ev.AllKinds(), func(k EventType) bool { return IsWebhookEvent(string(k)) })
	case "email":
		// An account holder's status change has nobody to mail. A guest-link
		// event may have no recipient yet: the send-time step fills it in. So
		// does a staff event, whose address is read when it is sent. A change
		// that may mean staff mail is queued whole and split by the worker.
		//
		// A password reset request carries a Recipient and is still not for
		// this channel: it has its own, with the signup mail (#451).
		if ev.Type == EventPasswordResetRequested {
			return false
		}
		return ev.Recipient != "" || ev.GuestLink || ev.StaffUserID != nil || (len(ev.Kinds) > 0 && ev.MailsStaff())
	case "verification":
		return ev.Type == EventRegistrationVerify || ev.Type == EventPasswordResetRequested
	}
	return true
}
