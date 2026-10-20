package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// #443: every description and reply records whether it is plain text or
// Markdown. The default depends on who is writing: REST says plain so that
// existing integrations see no change; MCP says markdown because its tool
// descriptions tell the agent the text is rendered as Markdown.

// rawFormats are the values a client might send for "format". The first two are
// accepted; every other one is refused with 400 invalid_format and writes
// nothing. A map literal can't hold a missing key, so absence is its own case.
var badFormats = []struct {
	name string
	json string
}{
	{"html", `"html"`},
	{"wrong case", `"Markdown"`},
	{"empty string", `""`},
	{"a number", `1`},
	{"null", `null`},
}

func postFormatBody(t *testing.T, h *harness, path, raw string) (int, map[string]any) {
	t.Helper()
	res := h.doRawBody(t, http.MethodPost, path, h.apiKey, raw)
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func TestRESTCreateTicket_FormatDefaultsToPlainAndIsValidated(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	base := `"subject":"s","description":"**d**","category_id":"` + h.catID.String() + `"`

	for _, tc := range []struct {
		name, extra string
		want        ticket.TextFormat
	}{
		{"no format", ``, ticket.TextFormatPlain},
		{"plain", `,"format":"plain"`, ticket.TextFormatPlain},
		{"markdown", `,"format":"markdown"`, ticket.TextFormatMarkdown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out := postFormatBody(t, h, "/api/v1/tickets", `{`+base+tc.extra+`}`)
			require.Equal(t, http.StatusCreated, code)
			require.Equal(t, string(tc.want), out["description_format"])
			id, err := uuid.Parse(out["id"].(string))
			require.NoError(t, err)
			stored, err := h.ticketSvc.GetByID(ctx, id)
			require.NoError(t, err)
			require.Equal(t, tc.want, stored.DescriptionFormat)
		})
	}

	for _, bad := range badFormats {
		t.Run("refuses "+bad.name, func(t *testing.T) {
			before, err := h.ticketSvc.ListAll(ctx, 1000, 0)
			require.NoError(t, err)
			code, out := postFormatBody(t, h, "/api/v1/tickets", `{`+base+`,"format":`+bad.json+`}`)
			require.Equal(t, http.StatusBadRequest, code)
			require.Equal(t, "invalid_format", fmtErrCode(out))
			after, err := h.ticketSvc.ListAll(ctx, 1000, 0)
			require.NoError(t, err)
			require.Len(t, after, len(before), "nothing is written")
		})
	}
}

// errorCode digs the code out of the {"error":{"code":...}} envelope.
func fmtErrCode(out map[string]any) string {
	if e, ok := out["error"].(map[string]any); ok {
		s, _ := e["code"].(string)
		return s
	}
	return ""
}

func TestRESTAddReply_FormatDefaultsToPlainAndIsValidated(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk := newTicketFor(t, h)
	path := "/api/v1/tickets/" + tk.ID.String() + "/replies"

	for _, tc := range []struct {
		name, extra string
		want        ticket.TextFormat
	}{
		{"no format", ``, ticket.TextFormatPlain},
		{"markdown", `,"format":"markdown"`, ticket.TextFormatMarkdown},
		{"plain", `,"format":"plain"`, ticket.TextFormatPlain},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out := postFormatBody(t, h, path, `{"body":"**b**"`+tc.extra+`}`)
			require.Equal(t, http.StatusCreated, code)
			require.Equal(t, string(tc.want), out["body_format"])
		})
	}
	replies, err := h.ticketSvc.ListReplies(ctx, tk.ID)
	require.NoError(t, err)
	require.Len(t, replies, 3)
	require.Equal(t, ticket.TextFormatMarkdown, replies[1].BodyFormat)

	for _, bad := range badFormats {
		t.Run("refuses "+bad.name, func(t *testing.T) {
			code, out := postFormatBody(t, h, path, `{"body":"x","format":`+bad.json+`}`)
			require.Equal(t, http.StatusBadRequest, code)
			require.Equal(t, "invalid_format", fmtErrCode(out))
		})
	}
	replies, err = h.ticketSvc.ListReplies(ctx, tk.ID)
	require.NoError(t, err)
	require.Len(t, replies, 3, "a refused format writes nothing")
}

func newTicketFor(t *testing.T, h *harness) ticket.Ticket {
	t.Helper()
	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "s", Description: "d", DescriptionFormat: ticket.TextFormatPlain,
		CategoryID: h.catID, Priority: ticket.PriorityMedium, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	return tk
}

func TestGuestRoutes_FormatDefaultsToPlainAndIsValidated(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyGuestSubmissionEnabled, true))

	create := func(extra string) (int, map[string]any) {
		res := h.doRawBody(t, http.MethodPost, "/api/v1/guest/tickets", "",
			`{"subject":"s","description":"d","category_id":"`+h.catID.String()+`","guest_email":"g@test.local","guest_name":"G"`+extra+`}`)
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	for _, tc := range []struct {
		name, extra string
		want        ticket.TextFormat
	}{
		{"no format", ``, ticket.TextFormatPlain},
		{"markdown", `,"format":"markdown"`, ticket.TextFormatMarkdown},
	} {
		t.Run("create "+tc.name, func(t *testing.T) {
			code, out := create(tc.extra)
			require.Equal(t, http.StatusCreated, code)
			tk, err := h.ticketSvc.GetByTrackingNumber(ctx, ticket.TrackingNumber(out["tracking_number"].(string)))
			require.NoError(t, err)
			require.Equal(t, tc.want, tk.DescriptionFormat)
		})
	}
	for _, bad := range badFormats {
		t.Run("create refuses "+bad.name, func(t *testing.T) {
			code, out := create(`,"format":` + bad.json)
			require.Equal(t, http.StatusBadRequest, code)
			require.Equal(t, "invalid_format", fmtErrCode(out))
		})
	}

	_, token := seedGuestTicket(t, h)
	reply := func(extra string) (int, map[string]any) {
		res := h.doGuestRaw(t, http.MethodPost, "/api/v1/guest/replies", token, `{"body":"hi"`+extra+`}`)
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	code, out := reply(``)
	require.Equal(t, http.StatusCreated, code)
	require.Equal(t, "plain", out["body_format"])
	code, out = reply(`,"format":"markdown"`)
	require.Equal(t, http.StatusCreated, code)
	require.Equal(t, "markdown", out["body_format"])
	for _, bad := range badFormats {
		code, out = reply(`,"format":` + bad.json)
		require.Equal(t, http.StatusBadRequest, code, bad.name)
		require.Equal(t, "invalid_format", fmtErrCode(out), bad.name)
	}

	// The guest's own view of the ticket carries both formats, or the page
	// could not tell what to render.
	res := h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", token, nil)
	defer res.Body.Close()
	var view map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&view))
	require.Equal(t, "plain", view["description_format"])
	replies := view["replies"].([]any)
	require.Len(t, replies, 2)
	require.Equal(t, "plain", replies[0].(map[string]any)["body_format"])
	require.Equal(t, "markdown", replies[1].(map[string]any)["body_format"])
}

func TestMCP_FormatDefaultsToMarkdownAndIsValidated(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	writer := h.newServiceClient(t, "Agent", "tickets:read", "tickets:write")
	sess := newMCPSession(t, h, writer.Token)

	createWith := func(extra map[string]any) (string, bool) {
		args := map[string]any{"subject": "s", "description": "**d**", "category_id": h.catID.String()}
		for k, v := range extra {
			args[k] = v
		}
		return sess.tool("create_ticket", args)
	}
	idOf := func(text string) uuid.UUID {
		var tk ticket.Ticket
		require.NoError(t, json.Unmarshal([]byte(text), &tk))
		return tk.ID
	}

	text, isErr := createWith(nil)
	require.False(t, isErr, text)
	require.Contains(t, text, `"description_format":"markdown"`)
	text, isErr = createWith(map[string]any{"format": "plain"})
	require.False(t, isErr, text)
	require.Contains(t, text, `"description_format":"plain"`)

	for _, bad := range []any{"html", "Markdown", "", 1.0} {
		before, err := h.ticketSvc.ListAll(ctx, 1000, 0)
		require.NoError(t, err)
		text, isErr = createWith(map[string]any{"format": bad})
		require.True(t, isErr, "%v: %s", bad, text)
		after, err := h.ticketSvc.ListAll(ctx, 1000, 0)
		require.NoError(t, err)
		require.Len(t, after, len(before), "nothing is written")
	}

	tid := idOf(mustText(t)(createWith(nil)))
	reply := func(extra map[string]any) (string, bool) {
		args := map[string]any{"ticket_id": tid.String(), "body": "_b_"}
		for k, v := range extra {
			args[k] = v
		}
		return sess.tool("add_reply", args)
	}
	text, isErr = reply(nil)
	require.False(t, isErr, text)
	text, isErr = reply(map[string]any{"format": "plain"})
	require.False(t, isErr, text)
	for _, bad := range []any{"html", "Markdown", "", 1.0} {
		text, isErr = reply(map[string]any{"format": bad})
		require.True(t, isErr, "%v: %s", bad, text)
	}
	replies, err := h.ticketSvc.ListReplies(ctx, tid)
	require.NoError(t, err)
	require.Len(t, replies, 2, "refused replies write nothing")
	require.Equal(t, ticket.TextFormatMarkdown, replies[0].BodyFormat)
	require.Equal(t, ticket.TextFormatPlain, replies[1].BodyFormat)

	// get_ticket carries both fields.
	text, isErr = sess.tool("get_ticket", map[string]any{"id": tid.String()})
	require.False(t, isErr, text)
	require.Contains(t, text, `"description_format":"markdown"`)
	require.Contains(t, text, `"body_format":"markdown"`)
	require.Contains(t, text, `"body_format":"plain"`)
}

func (h *harness) doGuestRaw(t *testing.T, method, path, token, body string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Guest "+token)
	rr := httptest.NewRecorder()
	h.srv.ServeHTTP(rr, req)
	return rr.Result()
}

func mustText(t *testing.T) func(string, bool) string {
	return func(text string, isErr bool) string {
		t.Helper()
		require.False(t, isErr, text)
		return text
	}
}

// A follow-up copies the original's text, so it copies the format with it: a
// follow-up of an old plain ticket must not start rendering as Markdown.
func TestFollowUp_CopiesTheOriginalsFormat(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	adminActor := ticket.Actor{UserID: &h.adminID, Role: user.RoleAdmin}

	for _, f := range []ticket.TextFormat{ticket.TextFormatPlain, ticket.TextFormatMarkdown} {
		t.Run(string(f), func(t *testing.T) {
			tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
				Subject: "s", Description: "*d*", DescriptionFormat: f,
				CategoryID: h.catID, Priority: ticket.PriorityMedium, ReporterUserID: &h.userID,
			})
			require.NoError(t, err)
			require.NoError(t, h.ticketSvc.Close(ctx, tk.ID, adminActor))

			staff, err := h.ticketSvc.CreateFollowUp(ctx, tk.ID, "GHD", adminActor)
			require.NoError(t, err)
			require.Equal(t, f, staff.DescriptionFormat)

			// The requester's route, on a fresh closed original.
			tk2, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
				Subject: "s2", Description: "*d*", DescriptionFormat: f,
				CategoryID: h.catID, Priority: ticket.PriorityMedium, ReporterUserID: &h.userID,
			})
			require.NoError(t, err)
			require.NoError(t, h.ticketSvc.Close(ctx, tk2.ID, adminActor))
			res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets/"+tk2.ID.String()+"/follow-up", nil)
			got := decodeTicket(t, res)
			require.Equal(t, f, got.DescriptionFormat)
		})
	}
}

// A reply is limited to 50,000 characters, as a description is: 400 body_too_long
// over it on every way in, nothing written, and 201 at exactly the limit.
func TestReplyLength_EveryWayIn(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyGuestSubmissionEnabled, true))
	tk := newTicketFor(t, h)
	_, token := seedGuestTicket(t, h)
	writer := h.newServiceClient(t, "Agent", "tickets:read", "tickets:write")
	sess := newMCPSession(t, h, writer.Token)

	atLimit := strings.Repeat("a", ticket.MaxReplyLength)
	over := atLimit + "a"
	rest := "/api/v1/tickets/" + tk.ID.String() + "/replies"

	t.Run("REST", func(t *testing.T) {
		code, out := postFormatBody(t, h, rest, `{"body":"`+over+`"}`)
		require.Equal(t, http.StatusBadRequest, code)
		require.Equal(t, "body_too_long", fmtErrCode(out))
		code, _ = postFormatBody(t, h, rest, `{"body":"`+atLimit+`"}`)
		require.Equal(t, http.StatusCreated, code)
		replies, err := h.ticketSvc.ListReplies(ctx, tk.ID)
		require.NoError(t, err)
		require.Len(t, replies, 1, "only the one at the limit was written")
	})

	t.Run("guest", func(t *testing.T) {
		res := h.doGuestRaw(t, http.MethodPost, "/api/v1/guest/replies", token, `{"body":"`+over+`"}`)
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		require.Equal(t, http.StatusBadRequest, res.StatusCode)
		require.Equal(t, "body_too_long", fmtErrCode(out))
		res2 := h.doGuestRaw(t, http.MethodPost, "/api/v1/guest/replies", token, `{"body":"`+atLimit+`"}`)
		defer res2.Body.Close()
		require.Equal(t, http.StatusCreated, res2.StatusCode)
	})

	t.Run("MCP add_reply", func(t *testing.T) {
		text, isErr := sess.tool("add_reply", map[string]any{"ticket_id": tk.ID.String(), "body": over})
		require.True(t, isErr, text)
		require.Contains(t, text, "50000")
		text, isErr = sess.tool("add_reply", map[string]any{"ticket_id": tk.ID.String(), "body": atLimit})
		require.False(t, isErr, text)
	})
}
