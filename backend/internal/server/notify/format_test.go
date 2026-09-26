package notify

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// Fixtures shared by every case below, matching the ones in the design
// (#187): tracking GHD-2026-000001, subject "Printer jammed", reply "Paper
// tray 2 was empty.", status "Resolved", this ticket id, this base URL.
var (
	fixtureTicketID  = uuid.MustParse("11111111-2222-3333-4444-555555555555")
	fixtureActorID   = uuid.MustParse("22222222-3333-4444-5555-666666666666")
	fixtureBaseURL   = "https://desk.example.com"
	fixtureTicketURL = "https://desk.example.com/tickets/11111111-2222-3333-4444-555555555555"
	fixtureOccurred  = time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
)

func fixtureReplyEvent(internal bool) notification.Event {
	payload := map[string]any{"internal": internal}
	if !internal {
		payload["ReplyBody"] = "Paper tray 2 was empty."
	}
	return notification.Event{
		Type:           notification.EventTicketReplied,
		TicketID:       fixtureTicketID,
		ActorID:        &fixtureActorID,
		Payload:        payload,
		OccurredAt:     fixtureOccurred,
		TrackingNumber: "GHD-2026-000001",
		Subject:        "Printer jammed",
	}
}

func fixtureStatusChangedEvent() notification.Event {
	return notification.Event{
		Type:           notification.EventTicketStatusChanged,
		TicketID:       fixtureTicketID,
		ActorID:        &fixtureActorID,
		Payload:        map[string]any{"new_status_id": uuid.New()},
		OccurredAt:     fixtureOccurred,
		TrackingNumber: "GHD-2026-000001",
		Subject:        "Printer jammed",
		StatusName:     "Resolved",
	}
}

// ── 1. Raw stays byte-identical ─────────────────────────────────────────────
//
// Written first, before anything else in this file: the point of this test
// is that it fails the moment anyone adds a JSON tag to Event or a key to
// Payload, whether or not they meant to change the wire format. The golden
// literals are hand-written, not derived from json.Marshal in this test, so
// they are a real pin rather than a tautology.
func TestBodyFor_RawIsByteIdenticalRegression(t *testing.T) {
	replied := notification.Event{
		Type:     notification.EventTicketReplied,
		TicketID: fixtureTicketID,
		ActorID:  &fixtureActorID,
		Payload: map[string]any{
			"reporter_email": "reporter@example.com",
			"TrackingNumber": "GHD-2026-000001",
			"Subject":        "Printer jammed",
			"internal":       false,
			"ReplyBody":      "Paper tray 2 was empty.",
		},
		OccurredAt:     fixtureOccurred,
		TrackingNumber: "GHD-2026-000001",
		Recipient:      "reporter@example.com",
		Subject:        "Printer jammed", // json:"-": must not appear below
	}
	const repliedGolden = `{"Type":"ticket.replied","TicketID":"11111111-2222-3333-4444-555555555555","ActorID":"22222222-3333-4444-5555-666666666666","Payload":{"ReplyBody":"Paper tray 2 was empty.","Subject":"Printer jammed","TrackingNumber":"GHD-2026-000001","internal":false,"reporter_email":"reporter@example.com"},"OccurredAt":"2026-09-25T12:00:00Z"}`

	raw, err := json.Marshal(replied)
	require.NoError(t, err)
	require.Equal(t, repliedGolden, string(raw),
		"the exact bytes a raw subscriber receives must not move")

	statusChanged := notification.Event{
		Type:           notification.EventTicketStatusChanged,
		TicketID:       fixtureTicketID,
		ActorID:        &fixtureActorID,
		Payload:        map[string]any{"new_status_id": "33333333-4444-5555-6666-777777777777"},
		OccurredAt:     fixtureOccurred,
		TrackingNumber: "GHD-2026-000001",
		Subject:        "Printer jammed", // json:"-"
		StatusName:     "Resolved",       // json:"-"
	}
	const statusGolden = `{"Type":"ticket.status_changed","TicketID":"11111111-2222-3333-4444-555555555555","ActorID":"22222222-3333-4444-5555-666666666666","Payload":{"new_status_id":"33333333-4444-5555-6666-777777777777"},"OccurredAt":"2026-09-25T12:00:00Z"}`

	raw2, err := json.Marshal(statusChanged)
	require.NoError(t, err)
	require.Equal(t, statusGolden, string(raw2))

	for _, tc := range []struct {
		name   string
		hook   authstore.WebhookConfig
		ev     notification.Event
		raw    []byte
		golden string
	}{
		{"raw format, replied", authstore.WebhookConfig{PayloadFormat: "raw"}, replied, raw, repliedGolden},
		{"empty format (pre-migration row), replied", authstore.WebhookConfig{PayloadFormat: ""}, replied, raw, repliedGolden},
		{"raw format, status changed", authstore.WebhookConfig{PayloadFormat: "raw"}, statusChanged, raw2, statusGolden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := bodyFor(tc.hook, tc.ev, tc.raw, fixtureBaseURL)
			require.NoError(t, err)
			require.Equal(t, tc.golden, string(got),
				"bodyFor must hand back the pre-marshalled slice untouched for raw")
		})
	}
}

// ── 2. One table per format ─────────────────────────────────────────────────

func TestRenderSlack(t *testing.T) {
	cases := []struct {
		name string
		ev   notification.Event
		want string
	}{
		{
			name: "reply created (public)",
			ev:   fixtureReplyEvent(false),
			want: `{"text": "*[GHD-2026-000001]* New reply — Printer jammed\n> Paper tray 2 was empty.\n<` + fixtureTicketURL + `|Open ticket>"}`,
		},
		{
			name: "reply created (internal)",
			ev:   fixtureReplyEvent(true),
			want: `{"text": "*[GHD-2026-000001]* Internal note added — Printer jammed\n<` + fixtureTicketURL + `|Open ticket>"}`,
		},
		{
			name: "status changed",
			ev:   fixtureStatusChangedEvent(),
			want: `{"text": "*[GHD-2026-000001]* Status changed to Resolved — Printer jammed\n<` + fixtureTicketURL + `|Open ticket>"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := renderSlack(summarize(tc.ev, fixtureBaseURL))
			require.NoError(t, err)
			require.JSONEq(t, tc.want, string(got))
		})
	}
}

func TestRenderTeams(t *testing.T) {
	cases := []struct {
		name     string
		ev       notification.Event
		wantJSON string
		wantLen  int
	}{
		{
			name: "reply created (public)",
			ev:   fixtureReplyEvent(false),
			wantJSON: `{
				"type": "message",
				"attachments": [{
					"contentType": "application/vnd.microsoft.card.adaptive",
					"contentUrl": null,
					"content": {
						"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
						"type": "AdaptiveCard",
						"version": "1.4",
						"msteams": {"width": "Full"},
						"body": [
							{"type": "TextBlock", "size": "Medium", "weight": "Bolder", "wrap": true, "text": "[GHD-2026-000001] New reply"},
							{"type": "TextBlock", "wrap": true, "text": "Printer jammed"},
							{"type": "TextBlock", "wrap": true, "text": "Paper tray 2 was empty."}
						],
						"actions": [
							{"type": "Action.OpenUrl", "title": "Open ticket", "url": "` + fixtureTicketURL + `"}
						]
					}
				}]
			}`,
			wantLen: 3,
		},
		{
			name: "reply created (internal)",
			ev:   fixtureReplyEvent(true),
			wantJSON: `{
				"type": "message",
				"attachments": [{
					"contentType": "application/vnd.microsoft.card.adaptive",
					"contentUrl": null,
					"content": {
						"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
						"type": "AdaptiveCard",
						"version": "1.4",
						"msteams": {"width": "Full"},
						"body": [
							{"type": "TextBlock", "size": "Medium", "weight": "Bolder", "wrap": true, "text": "[GHD-2026-000001] Internal note added"},
							{"type": "TextBlock", "wrap": true, "text": "Printer jammed"}
						],
						"actions": [
							{"type": "Action.OpenUrl", "title": "Open ticket", "url": "` + fixtureTicketURL + `"}
						]
					}
				}]
			}`,
			wantLen: 2,
		},
		{
			name: "status changed",
			ev:   fixtureStatusChangedEvent(),
			wantJSON: `{
				"type": "message",
				"attachments": [{
					"contentType": "application/vnd.microsoft.card.adaptive",
					"contentUrl": null,
					"content": {
						"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
						"type": "AdaptiveCard",
						"version": "1.4",
						"msteams": {"width": "Full"},
						"body": [
							{"type": "TextBlock", "size": "Medium", "weight": "Bolder", "wrap": true, "text": "[GHD-2026-000001] Status changed to Resolved"},
							{"type": "TextBlock", "wrap": true, "text": "Printer jammed"}
						],
						"actions": [
							{"type": "Action.OpenUrl", "title": "Open ticket", "url": "` + fixtureTicketURL + `"}
						]
					}
				}]
			}`,
			wantLen: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := renderTeams(summarize(tc.ev, fixtureBaseURL))
			require.NoError(t, err)
			require.JSONEq(t, tc.wantJSON, string(got))

			var env teamsEnvelope
			require.NoError(t, json.Unmarshal(got, &env))
			require.Len(t, env.Attachments, 1)
			require.Equal(t, teamsAdaptiveCardVersion, env.Attachments[0].Content.Version)
			require.Len(t, env.Attachments[0].Content.Body, tc.wantLen)
		})
	}
}

func TestRenderDiscord(t *testing.T) {
	cases := []struct {
		name string
		ev   notification.Event
		want string
	}{
		{
			name: "reply created (public)",
			ev:   fixtureReplyEvent(false),
			want: `{"content": "**[GHD-2026-000001]** New reply — Printer jammed\n> Paper tray 2 was empty.\n` + fixtureTicketURL + `", "allowed_mentions": {"parse": []}}`,
		},
		{
			name: "reply created (internal)",
			ev:   fixtureReplyEvent(true),
			want: `{"content": "**[GHD-2026-000001]** Internal note added — Printer jammed\n` + fixtureTicketURL + `", "allowed_mentions": {"parse": []}}`,
		},
		{
			name: "status changed",
			ev:   fixtureStatusChangedEvent(),
			want: `{"content": "**[GHD-2026-000001]** Status changed to Resolved — Printer jammed\n` + fixtureTicketURL + `", "allowed_mentions": {"parse": []}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := renderDiscord(summarize(tc.ev, fixtureBaseURL))
			require.NoError(t, err)
			require.JSONEq(t, tc.want, string(got))
			require.Contains(t, string(got), `"allowed_mentions":{"parse":[]}`,
				"every Discord message must disable mention parsing, not just some")
		})
	}
}

func TestRenderJIRA(t *testing.T) {
	cases := []struct {
		name string
		ev   notification.Event
		want string
	}{
		{
			name: "reply created (public)",
			ev:   fixtureReplyEvent(false),
			want: `{
				"event": "ticket.replied",
				"ticket_id": "11111111-2222-3333-4444-555555555555",
				"tracking_number": "GHD-2026-000001",
				"subject": "Printer jammed",
				"url": "` + fixtureTicketURL + `",
				"occurred_at": "2026-09-25T12:00:00Z",
				"internal": false,
				"body": "Paper tray 2 was empty."
			}`,
		},
		{
			name: "reply created (internal)",
			ev:   fixtureReplyEvent(true),
			want: `{
				"event": "ticket.replied",
				"ticket_id": "11111111-2222-3333-4444-555555555555",
				"tracking_number": "GHD-2026-000001",
				"subject": "Printer jammed",
				"url": "` + fixtureTicketURL + `",
				"occurred_at": "2026-09-25T12:00:00Z",
				"internal": true
			}`,
		},
		{
			name: "status changed",
			ev:   fixtureStatusChangedEvent(),
			want: `{
				"event": "ticket.status_changed",
				"ticket_id": "11111111-2222-3333-4444-555555555555",
				"tracking_number": "GHD-2026-000001",
				"subject": "Printer jammed",
				"url": "` + fixtureTicketURL + `",
				"occurred_at": "2026-09-25T12:00:00Z",
				"status": "Resolved"
			}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := renderJIRA(summarize(tc.ev, fixtureBaseURL))
			require.NoError(t, err)
			require.JSONEq(t, tc.want, string(got))

			var m map[string]any
			require.NoError(t, json.Unmarshal(got, &m))
			require.NotContains(t, m, "issues",
				"an empty/absent issues array is required so the Automation rule still fires; "+
					"there is no ticket-to-issue mapping to populate it with")
			if tc.name == "reply created (internal)" {
				require.NotContains(t, m, "body")
			}
		})
	}
}

// ── 3. Internal note bodies never leave the server, in any format ──────────
func TestRender_InternalNoteBodyNeverLeavesTheServer(t *testing.T) {
	const secret = "INTERNAL: the customer is under investigation"

	events := map[string]notification.Event{
		"as the service actually builds it (no ReplyBody key at all)": {
			Type:           notification.EventTicketReplied,
			TicketID:       fixtureTicketID,
			Payload:        map[string]any{"internal": true},
			OccurredAt:     fixtureOccurred,
			TrackingNumber: "GHD-2026-000001",
			Subject:        "Printer jammed",
		},
		"hostile: internal=true but ReplyBody set anyway": {
			Type:           notification.EventTicketReplied,
			TicketID:       fixtureTicketID,
			Payload:        map[string]any{"internal": true, "ReplyBody": secret},
			OccurredAt:     fixtureOccurred,
			TrackingNumber: "GHD-2026-000001",
			Subject:        "Printer jammed",
		},
	}

	for evName, ev := range events {
		for _, f := range []Format{FormatSlack, FormatTeams, FormatDiscord, FormatJIRA} {
			t.Run(string(f)+"/"+evName, func(t *testing.T) {
				got, err := renderers[f](summarize(ev, fixtureBaseURL))
				require.NoError(t, err)
				body := string(got)
				require.NotContains(t, body, secret,
					"a staff-only note must not reach a webhook subscriber, in any format")
				require.NotContains(t, body, "ReplyBody")
				if f == FormatJIRA {
					var m map[string]any
					require.NoError(t, json.Unmarshal(got, &m))
					require.NotContains(t, m, "body")
				}
			})
		}
	}
}

// ── 4. Mention safety ────────────────────────────────────────────────────────

func TestRenderSlack_EscapesMrkdwnControlCharacters(t *testing.T) {
	ev := notification.Event{
		Type:           notification.EventTicketReplied,
		TicketID:       fixtureTicketID,
		Payload:        map[string]any{"internal": false, "ReplyBody": "see & <act now>"},
		OccurredAt:     fixtureOccurred,
		TrackingNumber: "GHD-2026-000001",
		Subject:        "<!channel> urgent & broken",
	}

	got, err := renderSlack(summarize(ev, fixtureBaseURL))
	require.NoError(t, err)

	// Decoded, not a substring check on the raw bytes: json.Marshal itself
	// HTML-escapes & < > in string values (&, <, >), which is
	// a different, harmless escaping layer that any JSON parser undoes. What
	// must hold is the *decoded* text field, which is what Slack's parser
	// hands to its renderer.
	var decoded struct{ Text string }
	require.NoError(t, json.Unmarshal(got, &decoded))

	require.NotContains(t, decoded.Text, "<!channel>",
		"an unescaped <!channel> in a ticket subject would page the whole channel")
	require.Contains(t, decoded.Text, "&lt;!channel&gt; urgent &amp; broken")
	require.Contains(t, decoded.Text, "see &amp; &lt;act now&gt;")
}

func TestRenderDiscord_AlwaysDisablesMentionParsing(t *testing.T) {
	for _, ev := range []notification.Event{
		fixtureReplyEvent(false), fixtureReplyEvent(true), fixtureStatusChangedEvent(),
	} {
		got, err := renderDiscord(summarize(ev, fixtureBaseURL))
		require.NoError(t, err)

		var m map[string]any
		require.NoError(t, json.Unmarshal(got, &m))
		require.Equal(t, map[string]any{"parse": []any{}}, m["allowed_mentions"])
	}
}

// ── 5. Unknown format is skipped, not sent raw ──────────────────────────────

// ── 4b. Masked-link / markdown injection (adversarial review, v1.3.0) ──────
//
// Adaptive Cards' TextBlock (Teams) and Discord message content both render
// a markdown subset that includes masked links: "[text](url)". Unlike
// Slack's mrkdwn (which has no such syntax — a link there requires the
// "<url|text>" form the renderer itself controls), reporter-controlled
// Subject/Body text reaching Teams or Discord unescaped can become a live,
// clickable phishing link, posted under the operator's own webhook
// identity. allowed_mentions: {parse: []} on Discord only suppresses
// @-pings; it does nothing about this.

const maskedLinkPayload = "[Open ticket](https://evil.tld/login)"

func maskedLinkEvent() notification.Event {
	return notification.Event{
		Type:           notification.EventTicketReplied,
		TicketID:       fixtureTicketID,
		Payload:        map[string]any{"internal": false, "ReplyBody": maskedLinkPayload},
		OccurredAt:     fixtureOccurred,
		TrackingNumber: "GHD-2026-000001",
		Subject:        maskedLinkPayload,
	}
}

func TestRenderTeams_EscapesMaskedLinkSyntax(t *testing.T) {
	got, err := renderTeams(summarize(maskedLinkEvent(), fixtureBaseURL))
	require.NoError(t, err)

	var env teamsEnvelope
	require.NoError(t, json.Unmarshal(got, &env))
	require.Len(t, env.Attachments, 1)
	require.Len(t, env.Attachments[0].Content.Body, 3)

	subjectText := env.Attachments[0].Content.Body[1].Text
	bodyText := env.Attachments[0].Content.Body[2].Text

	for _, text := range []string{subjectText, bodyText} {
		require.NotContains(t, text, maskedLinkPayload,
			"an unescaped masked link would render as a clickable phishing link in the Adaptive Card, under the operator's webhook identity")
		require.Contains(t, text, `\[Open ticket\]\(https://evil.tld/login\)`,
			"brackets and parens must be backslash-escaped so the TextBlock renders them as literal text, not a link")
	}
}

func TestRenderDiscord_EscapesMaskedLinkSyntax(t *testing.T) {
	got, err := renderDiscord(summarize(maskedLinkEvent(), fixtureBaseURL))
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(got, &m))
	content, ok := m["content"].(string)
	require.True(t, ok)

	require.NotContains(t, content, maskedLinkPayload,
		"an unescaped masked link would render as a clickable phishing link in Discord, under the operator's webhook identity")
	require.Contains(t, content, `\[Open ticket\]\(https://evil.tld/login\)`,
		"brackets and parens must be backslash-escaped so Discord renders them as literal text, not a link")
}

// A Subject containing a newline must not let reporter-controlled text
// forge a second bolded "event line" in the rendered Discord message — the
// format only ever emits a fixed, known set of lines (headline+subject,
// optional quoted body, URL).
func TestRenderDiscord_SubjectNewlineDoesNotForgeLines(t *testing.T) {
	forgedLine := "**[GHD-2026-000001]** Status changed to Resolved"
	ev := notification.Event{
		Type:           notification.EventTicketReplied,
		TicketID:       fixtureTicketID,
		Payload:        map[string]any{"internal": false, "ReplyBody": "Paper tray 2 was empty."},
		OccurredAt:     fixtureOccurred,
		TrackingNumber: "GHD-2026-000001",
		Subject:        "Printer jammed\n" + forgedLine,
	}

	got, err := renderDiscord(summarize(ev, fixtureBaseURL))
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(got, &m))
	content, ok := m["content"].(string)
	require.True(t, ok)

	require.NotContains(t, content, "\n"+forgedLine,
		"a newline in Subject must not let attacker text become its own line, impersonating a status-change event")
	// Fixed shape: headline+subject line, "> body" line, URL line — a
	// newline smuggled in via Subject must not add a fourth.
	require.Equal(t, 2, strings.Count(content, "\n"),
		"the message must still have exactly the two newlines the format's own framing introduces")
}

func TestRenderSlack_EscapesStatusNameInHeadline(t *testing.T) {
	ev := notification.Event{
		Type:           notification.EventTicketStatusChanged,
		TicketID:       fixtureTicketID,
		Payload:        map[string]any{"new_status_id": uuid.New()},
		OccurredAt:     fixtureOccurred,
		TrackingNumber: "GHD-2026-000001",
		Subject:        "Printer jammed",
		StatusName:     "Escalated <!channel>",
	}

	got, err := renderSlack(summarize(ev, fixtureBaseURL))
	require.NoError(t, err)

	var decoded struct{ Text string }
	require.NoError(t, json.Unmarshal(got, &decoded))

	require.NotContains(t, decoded.Text, "<!channel>",
		"an admin-named status containing <!channel> must not page the whole Slack channel on every transition into it")
	require.Contains(t, decoded.Text, "Status changed to Escalated &lt;!channel&gt;")
}

func TestBodyFor_UnknownFormatIsRefusedNotSentRaw(t *testing.T) {
	ev := fixtureReplyEvent(false)
	raw, err := json.Marshal(ev)
	require.NoError(t, err)

	got, err := bodyFor(authstore.WebhookConfig{ID: uuid.New(), PayloadFormat: "xml"}, ev, raw, fixtureBaseURL)
	require.Error(t, err)
	require.Nil(t, got)
}

func TestFormat_IsValid(t *testing.T) {
	for _, f := range Formats {
		require.True(t, f.IsValid())
	}
	require.False(t, Format("xml").IsValid())
	require.False(t, Format("").IsValid())
}
