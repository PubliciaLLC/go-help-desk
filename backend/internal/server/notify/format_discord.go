package notify

import (
	"encoding/json"
	"strings"
)

type discordPayload struct {
	Content         string                 `json:"content"`
	AllowedMentions discordAllowedMentions `json:"allowed_mentions"`
}

type discordAllowedMentions struct {
	Parse []string `json:"parse"`
}

// renderDiscord builds a Discord webhook body.
//
// allowed_mentions: {"parse": []} is sent on every message, unconditionally —
// it is Discord's own mechanism for turning @everyone, @here, and role/user
// mentions in user-chosen text into plain text instead of a ping. Without
// it, a ticket subject can ping a whole server. content is capped at 2000
// characters by Discord; the 1000-rune body truncation in summarize plus the
// fixed framing here keeps every message well under that. Discord
// auto-links bare URLs, so the ticket link is not wrapped in markup.
func renderDiscord(s summary) ([]byte, error) {
	var b strings.Builder
	b.WriteString("**[")
	b.WriteString(s.Ref)
	b.WriteString("]** ")
	b.WriteString(s.Headline)
	if s.Subject != "" {
		b.WriteString(" — ")
		b.WriteString(s.Subject)
	}
	if s.Body != "" {
		b.WriteString("\n> ")
		b.WriteString(strings.ReplaceAll(s.Body, "\n", "\n> "))
	}
	b.WriteString("\n")
	b.WriteString(s.URL)

	return json.Marshal(discordPayload{
		Content:         b.String(),
		AllowedMentions: discordAllowedMentions{Parse: []string{}},
	})
}
