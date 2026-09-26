package notify

import (
	"encoding/json"
	"strings"
)

type discordPayload struct {
	Content         string                 `json:"content"`
	AllowedMentions discordAllowedMentions `json:"allowed_mentions"`
	// Flags: 4 is SUPPRESS_EMBEDS. Masked-link escaping neutralizes
	// "[text](url)", but Discord still auto-embeds and previews a bare URL
	// regardless of escaping — a ticket subject containing one would render
	// as a clickable, previewed link under the operator's own webhook
	// identity. See #214.
	Flags int `json:"flags"`
}

// discordSuppressEmbeds is Discord's SUPPRESS_EMBEDS message flag.
const discordSuppressEmbeds = 4

type discordAllowedMentions struct {
	Parse []string `json:"parse"`
}

const discordContentLimit = 1900 // Discord rejects >2000 with 400; counted in UTF-16 units

// renderDiscord builds a Discord webhook body.
//
// allowed_mentions: {"parse": []} is sent on every message, unconditionally —
// it is Discord's own mechanism for turning @everyone, @here, and role/user
// mentions in user-chosen text into plain text instead of a ping. Without
// it, a ticket subject can ping a whole server. That mechanism only covers
// mentions, though: Subject and Body, and Headline (which carries an
// admin-defined StatusName on ticket.status_changed), also pass through
// escapeChatMarkdown, because Discord's message content renders a markdown
// subset that includes masked links ("[text](url)"), and allowed_mentions
// does nothing about those — any of them containing one would otherwise
// become a live, clickable link posted under the operator's own webhook
// identity.
//
// Content is capped at 2000 UTF-16 units by Discord. The message is built
// and then truncated as a whole, with the URL preserved even if the text
// must be cut short. This ensures the ticket link always survives.
//
// Why the cut is safe for markdown: Everything user-controlled is already
// escaped. The only markup the renderer adds is the leading "**[ref]**",
// which a cut can't reach unless the URL is about 1870 units long, and the
// "> " prefixes. The worst a cut can do is leave a lone trailing "\" in
// front of the "\n" we append. It escapes nothing and at most shows as a
// visible backslash. No ellipsis and no extra branch.
func renderDiscord(s summary) ([]byte, error) {
	var b strings.Builder
	b.WriteString("**[")
	b.WriteString(s.Ref)
	b.WriteString("]** ")
	b.WriteString(escapeChatMarkdown(s.Headline))
	if s.Subject != "" {
		b.WriteString(" — ")
		b.WriteString(escapeChatMarkdown(s.Subject))
	}
	if s.Body != "" {
		body := escapeChatMarkdown(s.Body)
		b.WriteString("\n> ")
		b.WriteString(strings.ReplaceAll(body, "\n", "\n> "))
	}

	text := truncateUTF16(b.String(), max(0, discordContentLimit-1-utf16Len(s.URL)))
	content := text + "\n" + s.URL

	return json.Marshal(discordPayload{
		Content:         content,
		AllowedMentions: discordAllowedMentions{Parse: []string{}},
		Flags:           discordSuppressEmbeds,
	})
}
