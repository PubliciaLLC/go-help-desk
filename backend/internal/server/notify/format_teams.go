package notify

import (
	"encoding/json"
	"fmt"
)

// Adaptive Card schema version 1.4: the highest version both the classic
// Office 365 "Incoming Webhook" connector and its Power Automate replacement
// ("When a Teams webhook request is received") render reliably.
const teamsAdaptiveCardVersion = "1.4"

type teamsEnvelope struct {
	Type        string            `json:"type"`
	Attachments []teamsAttachment `json:"attachments"`
}

type teamsAttachment struct {
	ContentType string    `json:"contentType"`
	ContentURL  *string   `json:"contentUrl"`
	Content     teamsCard `json:"content"`
}

type teamsCard struct {
	Schema  string            `json:"$schema"`
	Type    string            `json:"type"`
	Version string            `json:"version"`
	MSTeams map[string]string `json:"msteams"`
	Body    []teamsTextBlock  `json:"body"`
	Actions []teamsAction     `json:"actions"`
}

type teamsTextBlock struct {
	Type   string `json:"type"`
	Size   string `json:"size,omitempty"`
	Weight string `json:"weight,omitempty"`
	Wrap   bool   `json:"wrap"`
	Text   string `json:"text"`
}

type teamsAction struct {
	Type  string `json:"type"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// renderTeams builds a message envelope carrying one Adaptive Card.
//
// body[0] is always "[Ref] Headline". body[1] is present when Subject is
// non-empty; body[2] is present when Body is non-empty (a public reply
// only — an internal note has no third block, not an empty one). actions is
// always the single Action.OpenUrl back to the ticket. No mention entities
// are emitted, so user-chosen text cannot page anyone through this format.
//
// Subject and Body are reporter-controlled, and Headline carries an
// admin-defined StatusName on ticket.status_changed — all three pass through
// escapeChatMarkdown before they reach the card: Adaptive Cards' TextBlock
// renders a markdown subset that includes masked links ("[text](url)"), and
// without escaping, any of them containing one becomes a live, clickable
// link posted under the operator's own webhook identity.
func renderTeams(s summary) ([]byte, error) {
	body := []teamsTextBlock{
		{Type: "TextBlock", Size: "Medium", Weight: "Bolder", Wrap: true,
			Text: fmt.Sprintf("[%s] %s", s.Ref, escapeChatMarkdown(s.Headline))},
	}
	if s.Subject != "" {
		body = append(body, teamsTextBlock{Type: "TextBlock", Wrap: true, Text: escapeChatMarkdown(s.Subject)})
	}
	if s.Body != "" {
		body = append(body, teamsTextBlock{Type: "TextBlock", Wrap: true, Text: escapeChatMarkdown(s.Body)})
	}

	env := teamsEnvelope{
		Type: "message",
		Attachments: []teamsAttachment{{
			ContentType: "application/vnd.microsoft.card.adaptive",
			ContentURL:  nil,
			Content: teamsCard{
				Schema:  "http://adaptivecards.io/schemas/adaptive-card.json",
				Type:    "AdaptiveCard",
				Version: teamsAdaptiveCardVersion,
				MSTeams: map[string]string{"width": "Full"},
				Body:    body,
				Actions: []teamsAction{{Type: "Action.OpenUrl", Title: "Open ticket", URL: s.URL}},
			},
		}},
	}
	return json.Marshal(env)
}
