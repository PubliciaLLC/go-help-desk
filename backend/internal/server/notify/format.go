package notify

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// Format is the value of webhook_configs.payload_format.
type Format string

const (
	FormatRaw     Format = "raw"
	FormatSlack   Format = "slack"
	FormatTeams   Format = "teams"
	FormatDiscord Format = "discord"
	FormatJIRA    Format = "jira"
)

// Formats is what the admin handler validates a payload_format against.
// Order is the UI order.
var Formats = []Format{FormatRaw, FormatSlack, FormatTeams, FormatDiscord, FormatJIRA}

// IsValid reports whether f is one of Formats.
func (f Format) IsValid() bool {
	for _, v := range Formats {
		if f == v {
			return true
		}
	}
	return false
}

// renderer turns one lifecycle event into the body a service's incoming
// webhook expects. It has no network and no store: given the same summary it
// returns the same bytes, which is what makes it testable without a live
// target and what keeps the raw format untouched by any of this.
type renderer func(s summary) ([]byte, error)

// renderers is keyed by Format. FormatRaw is deliberately absent: the
// absence of a renderer for it IS raw behaviour, so bodyFor's raw path below
// has no branch added to the existing marshal-and-send flow.
var renderers = map[Format]renderer{
	FormatSlack:   renderSlack,
	FormatTeams:   renderTeams,
	FormatDiscord: renderDiscord,
	FormatJIRA:    renderJIRA,
}

// summary is the one intermediate representation every chat/ITSM renderer
// reads. Renderers never touch notification.Event.Payload directly — that
// keeps "what a chat message may contain" a decision made in exactly one
// place (summarize), rather than re-derived, and possibly re-broken, in each
// of the four renderers.
type summary struct {
	Event      notification.EventType
	TicketID   uuid.UUID
	Ref        string // tracking number, or the ticket id when none is known
	Headline   string // per-event line; see headline()
	Subject    string // "" when the event does not carry one
	Body       string // public reply body only, truncated to 1000 runes; "" otherwise
	StatusName string // ticket.status_changed only
	Internal   bool
	URL        string // baseURL + "/tickets/" + TicketID — the staff link, never the guest one

	// Extra fields the JIRA body needs and no chat renderer reads. Kept on
	// summary rather than read off Payload in format_jira.go so every
	// renderer still goes through the one function that decides what a
	// Payload key is allowed to become a rendered field.
	Priority       string // ticket.created only
	LinkedTicketID string // ticket.linked only
	LinkType       string // ticket.linked only

	OccurredAt time.Time
}

// summarize builds the shared intermediate representation for all four
// chat/ITSM renderers from one lifecycle event.
//
// The internal-note rule is enforced here, once: Payload["ReplyBody"] is
// read only when Payload["internal"] is not true. AddReply already omits
// ReplyBody from the event for an internal note (see
// internal/domain/ticket/service.go), so this is belt-and-braces — a
// renderer cannot emit body content the summary does not hold, and the
// summary will not hold one for an internal note even if some future caller
// puts it in the payload anyway.
func summarize(ev notification.Event, baseURL string) summary {
	internal, _ := ev.Payload["internal"].(bool)

	var body string
	if !internal {
		if b, ok := ev.Payload["ReplyBody"].(string); ok {
			body = truncateRunes(b, 1000)
		}
	}

	var priority, linkedTicketID, linkType string
	switch ev.Type {
	case notification.EventTicketCreated:
		if p, ok := ev.Payload["Priority"].(string); ok {
			priority = p
		}
	case notification.EventTicketLinked:
		// target_id is a uuid.UUID and link_type is a ticket.LinkType (a
		// defined string type); this package does not import
		// internal/domain/ticket, so %v is used rather than a type
		// assertion — both types implement String()/have a string
		// underlying type, so %v renders exactly the value each would
		// format as on its own.
		if v, ok := ev.Payload["target_id"]; ok {
			linkedTicketID = fmt.Sprintf("%v", v)
		}
		if v, ok := ev.Payload["link_type"]; ok {
			linkType = fmt.Sprintf("%v", v)
		}
	}

	return summary{
		Event:          ev.Type,
		TicketID:       ev.TicketID,
		Ref:            ref(ev),
		Headline:       headline(ev.Type, ev.StatusName, internal, linkedTicketID, linkType),
		Subject:        sanitizeSubject(ev.Subject),
		Body:           body,
		StatusName:     ev.StatusName,
		Internal:       internal,
		URL:            strings.TrimRight(baseURL, "/") + "/tickets/" + ev.TicketID.String(),
		Priority:       priority,
		LinkedTicketID: linkedTicketID,
		LinkType:       linkType,
		OccurredAt:     ev.OccurredAt,
	}
}

// ref prefers the typed TrackingNumber field (set at every dispatch site),
// falls back to the payload key of the same name for any event that
// predates that, and falls back to the ticket id so a renderer never has to
// handle an empty reference.
func ref(ev notification.Event) string {
	if ev.TrackingNumber != "" {
		return ev.TrackingNumber
	}
	if tn, ok := ev.Payload["TrackingNumber"].(string); ok && tn != "" {
		return tn
	}
	return ev.TicketID.String()
}

// headline is the one-line summary every format leads with.
func headline(t notification.EventType, statusName string, internal bool, linkedTicketID, linkType string) string {
	switch t {
	case notification.EventTicketCreated:
		return "New ticket"
	case notification.EventTicketReplied:
		if internal {
			return "Internal note added"
		}
		return "New reply"
	case notification.EventTicketStatusChanged:
		return "Status changed to " + statusName
	case notification.EventTicketAssigned:
		return "Assigned"
	case notification.EventTicketResolved:
		return "Resolved"
	case notification.EventTicketClosed:
		return "Closed"
	case notification.EventTicketReopened:
		return "Reopened"
	case notification.EventTicketLinked:
		return fmt.Sprintf("Linked to %s (%s)", linkedTicketID, linkType)
	default:
		return string(t)
	}
}

// sanitizeSubject strips CR/LF from a reporter-controlled subject before any
// renderer sees it. Every chat renderer treats the subject as one line
// within a fixed, known set of lines (headline+subject, optional quoted
// body, URL); a subject containing a newline could otherwise forge an
// extra line — e.g. a fake bolded "event line" in Discord or Slack — that
// reads as if the server emitted it. Replacing with a space, rather than
// deleting, avoids running two words together.
func sanitizeSubject(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

// escapeChatMarkdown neutralizes the markdown control characters common to
// Teams' Adaptive Card TextBlock and Discord's message content, so
// reporter-controlled text (ticket Subject, reply Body) cannot become a
// masked link ("[text](url)"), a heading, a code span, bold/italic/strike
// markers, or a table pipe when either renderer parses it. Backslash-escaping
// is the standard escape both renderers honour: it turns the character into
// literal text instead of markup.
//
// Slack does not use this — its mrkdwn has no "[text](url)" link syntax (a
// link there requires the "<url|text>" form the renderer itself constructs),
// and it has its own escaping (slackEscape) for the characters that *are*
// special to it (&, <, >).
//
// ">" is only special at the start of a line (blockquote syntax), so it is
// escaped only there; escaping it mid-sentence would be visible noise for no
// security benefit.
func escapeChatMarkdown(s string) string {
	var b strings.Builder
	atLineStart := true
	for _, r := range s {
		switch r {
		case '[', ']', '(', ')', '*', '_', '~', '`', '|', '#':
			b.WriteByte('\\')
			b.WriteRune(r)
			atLineStart = false
		case '>':
			if atLineStart {
				b.WriteByte('\\')
			}
			b.WriteRune(r)
			atLineStart = false
		case '\n':
			b.WriteRune(r)
			atLineStart = true
		case ' ', '\t':
			b.WriteRune(r)
			// leading whitespace does not end "line start" — markdown
			// blockquotes tolerate up to three leading spaces.
		default:
			b.WriteRune(r)
			atLineStart = false
		}
	}
	return b.String()
}

// truncateRunes caps a body at n runes rather than n bytes, so multi-byte
// text is not sliced mid-character.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// bodyFor picks the bytes a hook receives. raw (and "", for a row written
// before the payload_format column existed) returns the pre-marshalled
// event untouched — the existing marshal-and-send path gains no branch.
// An unrecognised format is refused rather than silently sent as raw: a
// Slack URL fed the full raw JSON is a delivery bug, not a fallback.
func bodyFor(hook authstore.WebhookConfig, ev notification.Event, raw []byte, baseURL string) ([]byte, error) {
	f := Format(hook.PayloadFormat)
	if f == "" || f == FormatRaw {
		return raw, nil
	}
	r, ok := renderers[f]
	if !ok {
		return nil, fmt.Errorf("webhook %s: unknown payload_format %q", hook.ID, hook.PayloadFormat)
	}
	return r(summarize(ev, baseURL))
}

// utf16Len returns the number of UTF-16 code units needed to encode s.
// Each rune takes either 1 or 2 units (surrogates for code points outside BMP).
func utf16Len(s string) int {
	count := 0
	for _, r := range s {
		count += utf16.RuneLen(r)
	}
	return count
}

// truncateUTF16 returns the longest prefix of s that fits within n UTF-16 code
// units, never splitting a rune. Returns s itself if it is already short enough.
func truncateUTF16(s string, n int) string {
	if n < 0 {
		n = 0
	}
	units := 0
	for i, r := range s {
		rlen := utf16.RuneLen(r)
		if units+rlen > n {
			return s[:i]
		}
		units += rlen
	}
	return s
}
