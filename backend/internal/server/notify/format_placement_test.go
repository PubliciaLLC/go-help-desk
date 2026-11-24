package notify

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// createdWith is a ticket.created event whose placement has been read: p is
// the names, held is who the ticket's ids say holds it.
func createdWith(p *notification.Placement, held notification.TicketState) notification.Event {
	return notification.Event{
		Type:           notification.EventTicketCreated,
		TicketID:       fixtureTicketID,
		OccurredAt:     fixtureOccurred,
		TrackingNumber: "GHD-2026-000003",
		Subject:        "Test 3",
		Kinds:          []notification.EventType{notification.EventTicketCreated, notification.EventTicketAssigned},
		Ticket:         &held,
		Placement:      p,
	}
}

var (
	someGroup = uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001")
	someUser  = uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000002")
)

func heldBy(group, user bool) notification.TicketState {
	var s notification.TicketState
	if group {
		s.AssigneeGroupID = &someGroup
	}
	if user {
		s.AssigneeUserID = &someUser
	}
	return s
}

// The lines are plain text; a renderer escapes them. One table covers the CTI
// levels and the group/assignee combinations, because they are two lines that
// do not depend on each other.
func TestSummarize_PlacementLines(t *testing.T) {
	cases := []struct {
		name string
		p    notification.Placement
		held notification.TicketState
		want []string
	}{
		{"nothing known, nobody holds it", notification.Placement{}, heldBy(false, false), []string{"Unassigned"}},
		{"category only", notification.Placement{Category: "Hardware"}, heldBy(false, false),
			[]string{"Hardware", "Unassigned"}},
		{"category and type", notification.Placement{Category: "Hardware", Type: "Printer"}, heldBy(false, false),
			[]string{"Hardware > Printer", "Unassigned"}},
		{"category type and item", notification.Placement{Category: "Hardware", Type: "Printer", Item: "Toner"}, heldBy(false, false),
			[]string{"Hardware > Printer > Toner", "Unassigned"}},
		{"group only", notification.Placement{Category: "Hardware", Group: "IT Support"}, heldBy(true, false),
			[]string{"Hardware", "Group: IT Support"}},
		{"assignee only", notification.Placement{Category: "Hardware", Assignee: "Jordan Blake"}, heldBy(false, true),
			[]string{"Hardware", "Assignee: Jordan Blake"}},
		{"group and assignee", notification.Placement{Category: "Hardware", Type: "Printer", Group: "IT Support", Assignee: "Jordan Blake"}, heldBy(true, true),
			[]string{"Hardware > Printer", "Group: IT Support · Assignee: Jordan Blake"}},
		// An assignee whose account is gone has an id and no name. The ticket
		// was assigned, so it is not "Unassigned"; the line says nothing.
		{"assignee id with no readable name", notification.Placement{Category: "Hardware"}, heldBy(false, true),
			[]string{"Hardware"}},
		{"group readable, assignee account gone", notification.Placement{Category: "Hardware", Group: "IT Support"}, heldBy(true, true),
			[]string{"Hardware", "Group: IT Support"}},
		// A category whose row is gone has no name: no CTI line at all.
		{"no category name", notification.Placement{Group: "IT Support"}, heldBy(true, false),
			[]string{"Group: IT Support"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.p
			got := summarize(createdWith(&p, tc.held), fixtureBaseURL).Placement
			require.Equal(t, tc.want, got)
		})
	}
}

// Placement is for ticket.created and nothing else, and only once it has been
// read. Every other name keeps the message it has.
func TestSummarize_PlacementOnlyOnCreatedAndOnlyWhenRead(t *testing.T) {
	p := &notification.Placement{Category: "Hardware", Group: "IT Support"}
	held := heldBy(true, false)

	t.Run("not read", func(t *testing.T) {
		require.Empty(t, summarize(createdWith(nil, held), fixtureBaseURL).Placement)
	})
	for _, typ := range []notification.EventType{
		notification.EventTicketAssigned, notification.EventTicketClassified,
		notification.EventTicketReplied, notification.EventTicketClosed,
	} {
		t.Run(string(typ), func(t *testing.T) {
			ev := createdWith(p, held)
			ev.Type = typ
			require.Empty(t, summarize(ev, fixtureBaseURL).Placement)
		})
	}
}

// The holder of a new ticket is a fact about the ticket as filed, so the line
// does not depend on what the hook subscribes to: a hook that lists only
// ticket.created, or a replay that has no kinds beyond its name, is told the
// same as a hook on "*". Unassigned and the names both come from the ids.
func TestSummarize_HolderDoesNotDependOnTheHooksKinds(t *testing.T) {
	cases := []struct {
		name  string
		kinds []notification.EventType
		held  notification.TicketState
		want  []string
	}{
		{"created only, ticket assigned by routing", []notification.EventType{notification.EventTicketCreated},
			heldBy(true, true), []string{"Hardware", "Group: IT Support · Assignee: Jordan Blake"}},
		{"created only, nobody holds it", []notification.EventType{notification.EventTicketCreated},
			heldBy(false, false), []string{"Hardware", "Unassigned"}},
		{"created and assigned", []notification.EventType{notification.EventTicketCreated, notification.EventTicketAssigned},
			heldBy(true, true), []string{"Hardware", "Group: IT Support · Assignee: Jordan Blake"}},
		{"no kinds (a redelivery), ticket held now", nil,
			heldBy(true, true), []string{"Hardware", "Group: IT Support · Assignee: Jordan Blake"}},
		{"no kinds (a redelivery), nobody holds it", nil,
			heldBy(false, false), []string{"Hardware", "Unassigned"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := createdWith(&notification.Placement{Category: "Hardware", Group: "IT Support", Assignee: "Jordan Blake"}, tc.held)
			ev.Kinds = tc.kinds
			require.Equal(t, tc.want, summarize(ev, fixtureBaseURL).Placement)
		})
	}
}

// Names are admin-written but they are still text on its way into someone
// else's message: a line break would forge a line, and a pasted paragraph
// would fill the card.
func TestSummarize_PlacementNamesAreOneLineAndBounded(t *testing.T) {
	long := strings.Repeat("é", 500)
	// Any line separator or control whitespace is one space, not only CR and LF.
	odd := summarize(createdWith(&notification.Placement{Category: "A\u2028B\u0085C\vD\tE  F"}, heldBy(false, false)), fixtureBaseURL).Placement
	require.Equal(t, "A B C D E F", odd[0])
	ev := createdWith(&notification.Placement{
		Category: "Hard\r\nware", Group: "IT\nSupport", Assignee: long,
	}, heldBy(true, true))
	lines := summarize(ev, fixtureBaseURL).Placement

	require.Len(t, lines, 2)
	require.Equal(t, "Hard ware", lines[0])
	for _, l := range lines {
		require.NotContains(t, l, "\n")
		require.NotContains(t, l, "\r")
		require.LessOrEqual(t, len([]rune(l)), len("Group: IT Support · Assignee: ")+100+1)
	}
	require.Contains(t, lines[1], "Group: IT Support · Assignee: "+strings.Repeat("é", 100)+"…")
}

func TestRenderSlack_Placement(t *testing.T) {
	cases := []struct {
		name string
		p    notification.Placement
		held notification.TicketState
		want string
	}{
		{"classified and held", notification.Placement{Category: "Hardware", Type: "Printer", Item: "Toner", Group: "IT Support", Assignee: "Jordan Blake"}, heldBy(true, true),
			"*[GHD-2026-000003]* New ticket — Test 3\nHardware &gt; Printer &gt; Toner\nGroup: IT Support · Assignee: Jordan Blake\n"},
		{"unassigned", notification.Placement{Category: "Hardware"}, heldBy(false, false),
			"*[GHD-2026-000003]* New ticket — Test 3\nHardware\nUnassigned\n"},
		{"special characters", notification.Placement{Category: "R&D <ops>", Type: `"Q" & A`, Group: "<!channel> Sup"}, heldBy(true, false),
			"*[GHD-2026-000003]* New ticket — Test 3\nR&amp;D &lt;ops&gt; &gt; \"Q\" &amp; A\nGroup: &lt;!channel&gt; Sup\n"},
		{"unicode and markdown characters are left as the subject's are", notification.Placement{Category: "Ünïcode ☕ *bold* _it_", Assignee: "José [x](y)"}, heldBy(false, true),
			"*[GHD-2026-000003]* New ticket — Test 3\nÜnïcode ☕ *bold* _it_\nAssignee: José [x](y)\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.p
			got, err := renderSlack(summarize(createdWith(&p, tc.held), fixtureBaseURL))
			require.NoError(t, err)
			var out struct {
				Text string `json:"text"`
			}
			require.NoError(t, json.Unmarshal(got, &out))
			// The Also line stays below the placement.
			require.Equal(t, tc.want+"Also: assigned\n<"+fixtureTicketURL+"|Open ticket>", out.Text)
		})
	}
}

// What Slack looks like when the placement has not been read, or the event is
// not a creation: the message of before.
func TestRenderSlack_NoPlacementIsTheOldMessage(t *testing.T) {
	got, err := renderSlack(summarize(createdWith(nil, heldBy(false, false)), fixtureBaseURL))
	require.NoError(t, err)
	require.JSONEq(t, `{"text": "*[GHD-2026-000003]* New ticket — Test 3\nAlso: assigned\n<`+fixtureTicketURL+`|Open ticket>", "unfurl_links": false, "unfurl_media": false}`, string(got))
}

func teamsBody(t *testing.T, ev notification.Event) []map[string]any {
	t.Helper()
	got, err := renderTeams(summarize(ev, fixtureBaseURL))
	require.NoError(t, err)
	require.True(t, json.Valid(got))
	var env struct {
		Attachments []struct {
			Content struct {
				Version string           `json:"version"`
				Schema  string           `json:"$schema"`
				Body    []map[string]any `json:"body"`
			} `json:"content"`
		} `json:"attachments"`
	}
	require.NoError(t, json.Unmarshal(got, &env))
	require.Len(t, env.Attachments, 1)
	// The card schema and version are the ones the existing card has.
	require.Equal(t, "1.4", env.Attachments[0].Content.Version)
	require.Equal(t, "http://adaptivecards.io/schemas/adaptive-card.json", env.Attachments[0].Content.Schema)
	return env.Attachments[0].Content.Body
}

func TestRenderTeams_Placement(t *testing.T) {
	cases := []struct {
		name  string
		p     notification.Placement
		held  notification.TicketState
		lines []string // the texts of the blocks after the subject, before Also
	}{
		{"classified and held", notification.Placement{Category: "Hardware", Type: "Printer", Item: "Toner", Group: "IT Support", Assignee: "Jordan Blake"}, heldBy(true, true),
			[]string{"Hardware > Printer > Toner", "Group: IT Support · Assignee: Jordan Blake"}},
		{"category and type, group", notification.Placement{Category: "Hardware", Type: "Printer", Group: "IT Support"}, heldBy(true, false),
			[]string{"Hardware > Printer", "Group: IT Support"}},
		{"unassigned", notification.Placement{Category: "Hardware"}, heldBy(false, false),
			[]string{"Hardware", "Unassigned"}},
		// Teams renders a Markdown subset in a TextBlock: a name must not
		// become a link, emphasis, a code span or a table.
		{"markdown in names", notification.Placement{Category: "[click](https://evil.example)", Type: "*bold* _it_ `code` | # ~x~", Assignee: "(a)"}, heldBy(false, true),
			[]string{`\[click\]\(https://evil.example\) > \*bold\* \_it\_ \` + "`code\\` \\| \\# \\~x\\~", `Assignee: \(a\)`}},
		{"quotes ampersand angle brackets unicode", notification.Placement{Category: `R&D "ops" <b>`, Group: "Équipe ☕ 日本語"}, heldBy(true, false),
			[]string{`R&D "ops" <b>`, "Group: Équipe ☕ 日本語"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.p
			body := teamsBody(t, createdWith(&p, tc.held))
			// title, subject, then one block per line, then Also.
			require.Len(t, body, 2+len(tc.lines)+1)
			for i, want := range tc.lines {
				b := body[2+i]
				require.Equal(t, want, b["text"])
				require.Equal(t, "TextBlock", b["type"])
				require.Equal(t, true, b["wrap"], "a long name must wrap")
				require.Equal(t, true, b["isSubtle"])
				require.Equal(t, "Small", b["spacing"])
			}
			require.Equal(t, "Also: assigned", body[len(body)-1]["text"], "the Also line stays below the placement")
			// The established blocks are untouched.
			require.Equal(t, "Test 3", body[1]["text"])
			require.NotContains(t, body[1], "isSubtle")
		})
	}
}

func TestRenderTeams_LongNamesWrapAndStayValid(t *testing.T) {
	long := strings.Repeat("W", 300)
	body := teamsBody(t, createdWith(&notification.Placement{Category: long, Assignee: long}, heldBy(false, true)))
	for _, b := range body[2:4] {
		require.Equal(t, true, b["wrap"])
		require.LessOrEqual(t, len([]rune(b["text"].(string))), len("Assignee: ")+101)
	}
}

// With no placement the Teams card is the card it was: no extra block, and the
// new attributes never appear on an existing one.
func TestRenderTeams_NoPlacementIsTheOldCard(t *testing.T) {
	body := teamsBody(t, createdWith(nil, heldBy(false, false)))
	require.Len(t, body, 3)
	for _, b := range body {
		require.NotContains(t, b, "isSubtle")
		require.NotContains(t, b, "spacing")
	}
}

// Discord and Jira are not changed by this: they ignore the placement.
func TestPlacement_DiscordAndJiraAreUnchanged(t *testing.T) {
	with := createdWith(&notification.Placement{Category: "Hardware", Group: "IT Support"}, heldBy(true, false))
	without := createdWith(nil, heldBy(true, false))
	for _, f := range []Format{FormatDiscord, FormatJIRA} {
		a, err := renderers[f](summarize(with, fixtureBaseURL))
		require.NoError(t, err)
		b, err := renderers[f](summarize(without, fixtureBaseURL))
		require.NoError(t, err)
		require.Equal(t, string(b), string(a), string(f))
	}
}
