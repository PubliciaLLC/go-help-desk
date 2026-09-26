package notify

import (
	"encoding/json"
	"strings"
)

// renderSlack builds a Slack incoming-webhook body: the single required
// `text` field, in mrkdwn.
//
// Escaping &, < and > in Subject and Body is not cosmetic. Slack's `text` is
// the one place those three characters are control characters, and a
// subject of "<!channel> urgent" would page the whole channel with the
// operator's bot identity if it reached Slack unescaped — the chat-side
// analogue of the email content-spoofing rule in DESIGN.md, bounded rather
// than forbidden here because the operator chose the webhook target.
//
// Headline gets the same treatment. It is built from static, safe text for
// every event except ticket.status_changed, where it embeds StatusName
// verbatim — an admin-defined string, not reporter-controlled, but still
// external to this package. A status literally named "Escalated <!channel>"
// would otherwise page the channel on every transition into it. Escaping
// the whole assembled headline (rather than just StatusName before it goes
// in) is a no-op for the static cases and covers the one case that matters
// without needing headline() to know which of its callers is dangerous.
func renderSlack(s summary) ([]byte, error) {
	var b strings.Builder
	b.WriteString("*[")
	b.WriteString(s.Ref)
	b.WriteString("]* ")
	b.WriteString(slackEscape(s.Headline))
	if s.Subject != "" {
		b.WriteString(" — ")
		b.WriteString(slackEscape(s.Subject))
	}
	if s.Body != "" {
		b.WriteString("\n> ")
		b.WriteString(strings.ReplaceAll(slackEscape(s.Body), "\n", "\n> "))
	}
	b.WriteString("\n<")
	b.WriteString(s.URL)
	b.WriteString("|Open ticket>")

	return json.Marshal(struct {
		Text string `json:"text"`
	}{Text: b.String()})
}

// slackEscape applies Slack's mrkdwn escaping. Order matters: & must be
// escaped first, or the entities produced for < and > would themselves be
// re-escaped.
func slackEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
