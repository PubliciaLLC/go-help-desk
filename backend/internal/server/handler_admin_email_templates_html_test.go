package server_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The HTML part of the email template editor's API (#158, PR 2). The routes
// are PR 1's, so who may call them is already pinned by
// TestEmailTemplates_OnlyASignedInAdministrator; these pin what the HTML adds.

func (h *harness) putTemplateHTML(t *testing.T, name, subject, body, html string) (int, []byte) {
	t.Helper()
	res := h.doAsAdminSession(t, http.MethodPut, tmplPath+"/"+name, map[string]any{"subject": subject, "body": body, "html": html})
	return res.StatusCode, readAll(t, res)
}

type htmlDetail struct {
	HTML         string `json:"html"`
	HasHTML      bool   `json:"has_html"`
	MaxHTMLBytes int    `json:"max_html_bytes"`
	Customised   bool   `json:"customised"`
	Broken       bool   `json:"broken"`
	BrokenReason string `json:"broken_reason"`
}

func (h *harness) getHTMLDetail(t *testing.T, name string) htmlDetail {
	t.Helper()
	res := h.doAsAdminSession(t, http.MethodGet, tmplPath+"/"+name, nil)
	b := readAll(t, res)
	require.Equal(t, http.StatusOK, res.StatusCode, string(b))
	var d htmlDetail
	require.NoError(t, json.Unmarshal(b, &d))
	return d
}

const goodHTML = `<p>Hello</p><a href="{{.TicketURL}}">Open your ticket</a>`

func TestEmailTemplatesHTML_SaveStoresAndTheDetailReturnsIt(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	d := h.getHTMLDetail(t, "ticket_created")
	require.Empty(t, d.HTML)
	require.False(t, d.HasHTML)
	require.Equal(t, 64*1024, d.MaxHTMLBytes)

	def := h.getTemplate(t, "ticket_created")
	// The text is the default; only the HTML is new. That is a customised
	// template: it must not be mistaken for the default and deleted.
	code, b := h.putTemplateHTML(t, "ticket_created", def.DefaultSubject, def.DefaultBody, goodHTML)
	require.Equal(t, http.StatusOK, code, string(b))

	d = h.getHTMLDetail(t, "ticket_created")
	require.Equal(t, goodHTML, d.HTML)
	require.True(t, d.HasHTML)
	require.True(t, d.Customised)
	require.False(t, d.Broken)
	require.Equal(t, def.DefaultBody, h.getTemplate(t, "ticket_created").Body)

	// The list says which templates carry HTML.
	res := h.doAsAdminSession(t, http.MethodGet, tmplPath, nil)
	var list []struct {
		Name    string `json:"name"`
		HasHTML bool   `json:"has_html"`
	}
	require.NoError(t, json.Unmarshal(readAll(t, res), &list))
	for _, e := range list {
		require.Equal(t, e.Name == "ticket_created", e.HasHTML, e.Name)
	}
}

func TestEmailTemplatesHTML_RemovingTheHTMLKeepsTheText(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	code, _ := h.putTemplateHTML(t, "ticket_created", "Custom", "{{.TicketURL}} mine", goodHTML)
	require.Equal(t, http.StatusOK, code)
	code, _ = h.putTemplateHTML(t, "ticket_created", "Custom", "{{.TicketURL}} mine", "")
	require.Equal(t, http.StatusOK, code)
	d := h.getHTMLDetail(t, "ticket_created")
	require.Empty(t, d.HTML)
	require.False(t, d.HasHTML)
	require.True(t, d.Customised)
	require.Equal(t, "{{.TicketURL}} mine", h.getTemplate(t, "ticket_created").Body)
}

func TestEmailTemplatesHTML_TheDefaultTextWithNoHTMLStillDeletesTheRow(t *testing.T) {
	// A request with no "html" key at all is a PR 1 client: text only.
	h, cleanup := newHarness(t)
	defer cleanup()
	def := h.getTemplate(t, "ticket_created")
	code, _ := h.putTemplateHTML(t, "ticket_created", def.DefaultSubject, def.DefaultBody, goodHTML)
	require.Equal(t, http.StatusOK, code)
	code, _ = h.putTemplateHTML(t, "ticket_created", def.DefaultSubject, def.DefaultBody, "")
	require.Equal(t, http.StatusOK, code)
	require.False(t, h.getTemplate(t, "ticket_created").Customised)
	var n int
	require.NoError(t, h.tx.QueryRow(`SELECT count(*) FROM email_template_overrides WHERE name = 'ticket_created'`).Scan(&n))
	require.Zero(t, n)
}

func TestEmailTemplatesHTML_AHeaderOrFooterNeedsNoLink(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	code, b := h.putTemplateHTML(t, "_header", "", "", `<table width="100%"><tr><td><b>{{.SiteName}}</b></td></tr><tr><td>`)
	require.Equal(t, http.StatusOK, code, string(b))
	code, b = h.putTemplateHTML(t, "_footer", "", "", `</td></tr></table>`)
	require.Equal(t, http.StatusOK, code, string(b))
	require.True(t, h.getHTMLDetail(t, "_header").Customised)
	require.True(t, h.getHTMLDetail(t, "_footer").HasHTML)
}

func TestEmailTemplatesHTML_SaveRefusesWhatTheValidatorRefuses(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	for _, tc := range []struct {
		name, tmpl, html string
		line, col        int
		contains         string
	}{
		{"a script", "ticket_created", "{{.TicketURL}}\n<script>x</script>", 2, 1, "<script> is not allowed"},
		{"a form", "password_reset", `<a href="{{.ResetURL}}">x</a><form action="https://evil.example"></form>`, 1, 30, "<form> is not allowed"},
		{"an event handler", "ticket_created", `<a href="{{.TicketURL}}" onclick="x()">x</a>`, 1, 1, `"onclick"`},
		{"a javascript url", "ticket_created", `{{.TicketURL}}<a href="javascript:alert(1)">x</a>`, 1, 15, "href must start with"},
		{"no link", "guest_link", `<p>no link</p>`, 1, 1, "{{.TicketURL}}"},
		{"a link only inside an if", "password_reset", `{{if .ResetURL}}<a href="{{.ResetURL}}">x</a>{{end}}`, 1, 1, "outside any {{if}}"},
		{"a function", "ticket_created", "{{.TicketURL}}\n{{printf \"x\"}}", 2, 3, "printf"},
		{"an action in a bad place", "ticket_created", `{{.TicketURL}}<p title="{{.TrackingNumber}}">x</p>`, 1, 15, "only in the text"},
		{"a header with a script", "_header", `<script>x</script>`, 1, 1, "<script> is not allowed"},
		{"a footer that wants a ticket", "_footer", `<p>{{.TicketURL}}</p>`, 1, 6, "TicketURL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Valid text, so the refusal is the HTML's alone. The shared header
			// and footer take no subject and a body of nothing.
			subject, body := "", ""
			if !strings.HasPrefix(tc.tmpl, "_") {
				def := h.getTemplate(t, tc.tmpl)
				subject, body = def.DefaultSubject, def.DefaultBody
			}
			code, b := h.putTemplateHTML(t, tc.tmpl, subject, body, tc.html)
			require.Equal(t, http.StatusBadRequest, code, string(b))
			var e tmplErr
			require.NoError(t, json.Unmarshal(b, &e))
			require.Equal(t, "invalid_template", e.Error.Code)
			require.Equal(t, "html", e.Error.Field)
			require.Equal(t, tc.line, e.Error.Line, e.Error.Message)
			require.Equal(t, tc.col, e.Error.Col, e.Error.Message)
			require.Contains(t, e.Error.Message, tc.contains)

			require.False(t, h.getHTMLDetail(t, tc.tmpl).Customised, "a refused save stores nothing")
			require.Empty(t, h.templateAudit(t, tc.tmpl), "and records nothing")
		})
	}
}

func TestEmailTemplatesHTML_SaveReportsTextAndHTMLProblemsTogether(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	code, b := h.putTemplateHTML(t, "ticket_created", "s", "no link", `<script>x</script>`)
	require.Equal(t, http.StatusBadRequest, code)
	var e tmplErr
	require.NoError(t, json.Unmarshal(b, &e))
	fields := map[string]bool{}
	for _, x := range e.Error.Errors {
		fields[x.Field] = true
	}
	require.Equal(t, map[string]bool{"body": true, "html": true}, fields)
}

func TestEmailTemplatesHTML_SaveRefusesHugeHTML(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	def := h.getTemplate(t, "ticket_created")
	code, b := h.putTemplateHTML(t, "ticket_created", def.DefaultSubject, def.DefaultBody, "{{.TicketURL}}"+strings.Repeat("x", 64*1024))
	require.Equal(t, http.StatusBadRequest, code, string(b)[:min(200, len(b))])
	var e tmplErr
	require.NoError(t, json.Unmarshal(b, &e))
	require.Equal(t, "html", e.Error.Field)
	require.Contains(t, e.Error.Message, "limit is 65536")
}

// ── audit ────────────────────────────────────────────────────────────────────

func TestEmailTemplatesHTML_AuditKeepsTheFullHTMLBeforeAndAfter(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	def := h.getTemplate(t, "ticket_created")

	code, b := h.putTemplateHTML(t, "ticket_created", def.DefaultSubject, def.DefaultBody, goodHTML)
	require.Equal(t, http.StatusOK, code, string(b))
	second := `<h1>Second</h1><a href="{{.TicketURL}}">go</a>`
	code, b = h.putTemplateHTML(t, "ticket_created", def.DefaultSubject, def.DefaultBody, second)
	require.Equal(t, http.StatusOK, code, string(b))

	res := h.doAsAdminSession(t, http.MethodDelete, tmplPath+"/ticket_created", nil)
	readAll(t, res)
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	entries := h.templateAudit(t, "ticket_created") // newest first
	require.Len(t, entries, 3)
	require.Equal(t, "email_template_reverted", entries[0].Action)
	require.Equal(t, second, entries[0].Before["template_html"], "the reverted HTML survives in the log")
	require.Nil(t, entries[0].After)
	require.Equal(t, goodHTML, entries[1].Before["template_html"])
	require.Equal(t, second, entries[1].After["template_html"])
	require.Empty(t, entries[2].Before["template_html"], "before the first edit there was no HTML")
	require.Equal(t, goodHTML, entries[2].After["template_html"])
	// The text is recorded beside it, as before.
	require.Equal(t, def.DefaultBody, entries[2].After["template_body"])
}

func TestEmailTemplatesHTML_TheAuditViewShowsTheHTML(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	def := h.getTemplate(t, "ticket_created")
	code, b := h.putTemplateHTML(t, "ticket_created", def.DefaultSubject, def.DefaultBody, `<p>Visible in the audit view</p><a href="{{.TicketURL}}">x</a>`)
	require.Equal(t, http.StatusOK, code, string(b))
	res := h.doAsAdminSession(t, http.MethodGet, "/api/v1/admin/audit?limit=100", nil)
	body := string(readAll(t, res))
	require.Equal(t, http.StatusOK, res.StatusCode, body)
	require.Contains(t, body, "Visible in the audit view")
}

// ── preview ──────────────────────────────────────────────────────────────────

type htmlPreview struct {
	Valid  bool   `json:"valid"`
	Body   string `json:"body"`
	HTML   string `json:"html"`
	Errors []struct {
		Field   string `json:"field"`
		Line    int    `json:"line"`
		Col     int    `json:"col"`
		Message string `json:"message"`
	} `json:"errors"`
}

func (h *harness) previewHTML(t *testing.T, name string, draft map[string]any) htmlPreview {
	t.Helper()
	res := h.doAsAdminSession(t, http.MethodPost, tmplPath+"/"+name+"/preview", draft)
	b := readAll(t, res)
	require.Equal(t, http.StatusOK, res.StatusCode, string(b))
	var p htmlPreview
	require.NoError(t, json.Unmarshal(b, &p))
	return p
}

func TestEmailTemplatesHTML_PreviewRendersTheHTMLWithSampleData(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	p := h.previewHTML(t, "ticket_created", map[string]any{"subject": "s", "body": "{{.TicketURL}}", "html": goodHTML})
	require.True(t, p.Valid)
	require.Equal(t, `<p>Hello</p><a href="http://localhost:8080/tickets/00000000-0000-0000-0000-000000000000">Open your ticket</a>`, p.HTML)
	require.Empty(t, h.templateAudit(t, "ticket_created"))
	require.False(t, h.getHTMLDetail(t, "ticket_created").Customised, "a preview stores nothing")
}

func TestEmailTemplatesHTML_PreviewWithoutHTMLHasNone(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	p := h.previewHTML(t, "ticket_created", map[string]any{"subject": "s", "body": "{{.TicketURL}}"})
	require.True(t, p.Valid)
	require.Empty(t, p.HTML)
}

func TestEmailTemplatesHTML_PreviewMarksTheHTMLFieldWhileTyping(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	p := h.previewHTML(t, "ticket_created", map[string]any{"subject": "s", "body": "{{.TicketURL}}", "html": "<p>x</p>\n<iframe></iframe>"})
	require.False(t, p.Valid)
	require.Len(t, p.Errors, 1)
	require.Equal(t, "html", p.Errors[0].Field)
	require.Equal(t, 2, p.Errors[0].Line)
	require.Contains(t, p.Errors[0].Message, "<iframe> is not allowed")
}

func TestEmailTemplatesHTML_PreviewOfAHeaderShowsItAroundASampleMail(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	p := h.previewHTML(t, "_header", map[string]any{"subject": "", "body": "", "html": `<h1>{{.SiteName}}</h1>`})
	require.True(t, p.Valid)
	require.True(t, strings.HasPrefix(p.HTML, "<h1>Go Help Desk</h1>"), p.HTML)
	require.Contains(t, p.HTML, "We have received your ticket.")
}

// ── test send ────────────────────────────────────────────────────────────────

func TestEmailTemplatesHTML_TestSendCarriesBothParts(t *testing.T) {
	h, smtp, cleanup := harnessWithSMTP(t)
	defer cleanup()

	res := h.doAsAdminSession(t, http.MethodPost, tmplPath+"/ticket_created/test", map[string]any{
		"subject": "Try", "body": "Plain {{.TicketURL}}", "html": goodHTML,
	})
	require.Equal(t, http.StatusOK, res.StatusCode, string(readAll(t, res)))
	_, data := smtp.snapshot()
	require.Len(t, data, 1)
	require.Contains(t, data[0], "Content-Type: multipart/alternative; boundary=")
	require.Contains(t, data[0], "Content-Type: text/plain; charset=utf-8")
	require.Contains(t, data[0], "Content-Type: text/html; charset=utf-8")
	require.Contains(t, data[0], "Subject: [Test] Try")
	require.Contains(t, data[0], "To: <admin@test.local>")
}

func TestEmailTemplatesHTML_TestSendOfTheSavedTemplateCarriesItsHTML(t *testing.T) {
	h, smtp, cleanup := harnessWithSMTP(t)
	defer cleanup()
	def := h.getTemplate(t, "ticket_created")
	code, b := h.putTemplateHTML(t, "ticket_created", def.DefaultSubject, def.DefaultBody, goodHTML)
	require.Equal(t, http.StatusOK, code, string(b))
	res := h.doAsAdminSession(t, http.MethodPost, tmplPath+"/ticket_created/test", map[string]any{})
	require.Equal(t, http.StatusOK, res.StatusCode, string(readAll(t, res)))
	_, data := smtp.snapshot()
	require.Len(t, data, 1)
	require.Contains(t, data[0], "text/html")
}

func TestEmailTemplatesHTML_TestSendRefusesInvalidHTML(t *testing.T) {
	h, smtp, cleanup := harnessWithSMTP(t)
	defer cleanup()
	res := h.doAsAdminSession(t, http.MethodPost, tmplPath+"/ticket_created/test", map[string]any{
		"subject": "s", "body": "{{.TicketURL}}", "html": "<script>x</script>{{.TicketURL}}",
	})
	b := readAll(t, res)
	require.Equal(t, http.StatusBadRequest, res.StatusCode, string(b))
	require.Equal(t, "invalid_template", errCode(t, b))
	_, data := smtp.snapshot()
	require.Empty(t, data)
}

// ── a stale HTML part ────────────────────────────────────────────────────────

func (h *harness) seedStaleHTML(t *testing.T, name, subject, body, html string) {
	t.Helper()
	_, err := h.tx.Exec(`INSERT INTO email_template_overrides (name, subject, body, html) VALUES ($1, $2, $3, $4)`, name, subject, body, html)
	require.NoError(t, err)
}

func TestEmailTemplatesHTML_AStaleHTMLPartIsListedAsBroken(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	def := h.getTemplate(t, "password_reset")
	h.seedStaleHTML(t, "password_reset", def.DefaultSubject, def.DefaultBody, `<script>x</script><a href="{{.ResetURL}}">x</a>`)

	w := h.tmplWarnings(t)
	require.Len(t, w.Email.BrokenTemplates, 1)
	b := w.Email.BrokenTemplates[0]
	require.Equal(t, "password_reset", b.Name)
	require.Contains(t, b.Reason, "<script> is not allowed")

	d := h.getHTMLDetail(t, "password_reset")
	require.True(t, d.Broken)
	require.Contains(t, d.BrokenReason, "html")

	// One click reverts it, the HTML with it, and the notice goes.
	res := h.doAsAdminSession(t, http.MethodDelete, tmplPath+"/password_reset", nil)
	readAll(t, res)
	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.Empty(t, h.tmplWarnings(t).Email.BrokenTemplates)
	require.False(t, h.getHTMLDetail(t, "password_reset").HasHTML)
}

// ── the joined size ──────────────────────────────────────────────────────────

func divOf(n int) string { return "<div>" + strings.Repeat("x", n) + "</div>" }

func (h *harness) putCreatedHTML(t *testing.T, html string) (int, []byte) {
	t.Helper()
	def := h.getTemplate(t, "ticket_created")
	return h.putTemplateHTML(t, "ticket_created", def.DefaultSubject, def.DefaultBody, html)
}

func TestEmailTemplatesHTML_TheJoinedSizeIsCheckedAtSave(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// Each is under every limit alone: a 40 KiB header, a 40 KiB footer.
	code, b := h.putTemplateHTML(t, "_header", "", "", divOf(40*1024))
	require.Equal(t, http.StatusOK, code, string(b)[:min(300, len(b))])
	code, b = h.putTemplateHTML(t, "_footer", "", "", divOf(40*1024))
	require.Equal(t, http.StatusOK, code, string(b)[:min(300, len(b))])

	// The third would make every send fail at about 123 KB.
	code, b = h.putCreatedHTML(t, divOf(40*1024)+`<a href="{{.TicketURL}}">x</a>`)
	require.Equal(t, http.StatusBadRequest, code, string(b)[:min(300, len(b))])
	var e tmplErr
	require.NoError(t, json.Unmarshal(b, &e))
	require.Equal(t, "invalid_template", e.Error.Code)
	require.Equal(t, "html", e.Error.Field)
	require.Contains(t, e.Error.Message, `"Ticket received"`)
	require.Contains(t, e.Error.Message, "100 KiB")
	require.False(t, h.getHTMLDetail(t, "ticket_created").Customised, "a refused save stores nothing")
	require.Empty(t, h.templateAudit(t, "ticket_created"))

	// A smaller one is fine.
	code, b = h.putCreatedHTML(t, `<a href="{{.TicketURL}}">x</a>`)
	require.Equal(t, http.StatusOK, code, string(b)[:min(300, len(b))])
}

func TestEmailTemplatesHTML_AShellThatWouldBreakATemplateIsRefused(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	code, b := h.putCreatedHTML(t, divOf(60*1024)+`<a href="{{.TicketURL}}">x</a>`)
	require.Equal(t, http.StatusOK, code, string(b)[:min(300, len(b))])

	code, b = h.putTemplateHTML(t, "_header", "", "", divOf(45*1024))
	require.Equal(t, http.StatusBadRequest, code, string(b)[:min(300, len(b))])
	var e tmplErr
	require.NoError(t, json.Unmarshal(b, &e))
	require.Equal(t, "html", e.Error.Field)
	require.Contains(t, e.Error.Message, `"Ticket received"`)
	require.False(t, h.getHTMLDetail(t, "_header").Customised)

	// Smaller is fine, and so is a revert of a header that is already there.
	code, b = h.putTemplateHTML(t, "_header", "", "", divOf(10*1024))
	require.Equal(t, http.StatusOK, code, string(b)[:min(300, len(b))])
	res := h.doAsAdminSession(t, http.MethodDelete, tmplPath+"/_header", nil)
	readAll(t, res)
	require.Equal(t, http.StatusNoContent, res.StatusCode)
}

func TestEmailTemplatesHTML_AShellMakesEveryTemplatesTextCount(t *testing.T) {
	// With a 51 KiB header and 51 KiB footer every template has an HTML part,
	// so the second save is refused naming a template that has no HTML of its own.
	h, cleanup := newHarness(t)
	defer cleanup()
	code, b := h.putTemplateHTML(t, "_header", "", "", divOf(51*1024))
	require.Equal(t, http.StatusOK, code, string(b)[:min(300, len(b))])
	code, b = h.putTemplateHTML(t, "_footer", "", "", divOf(51*1024))
	require.Equal(t, http.StatusBadRequest, code, string(b)[:min(300, len(b))])
	var e tmplErr
	require.NoError(t, json.Unmarshal(b, &e))
	require.Contains(t, e.Error.Message, "would come to")
}

func TestEmailTemplatesHTML_AnOverCapCombinationInTheDatabaseIsBrokenAndSaysSo(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	def := h.getTemplate(t, "ticket_created")
	h.seedStaleHTML(t, "_header", "", "", divOf(40*1024))
	h.seedStaleHTML(t, "_footer", "", "", divOf(40*1024))
	h.seedStaleHTML(t, "ticket_created", def.DefaultSubject, def.DefaultBody, divOf(40*1024)+`<a href="{{.TicketURL}}">x</a>`)

	w := h.tmplWarnings(t)
	found := false
	for _, b := range w.Email.BrokenTemplates {
		if b.Name == "ticket_created" {
			found = true
			require.Contains(t, b.Reason, "100 KiB")
		}
	}
	require.True(t, found, "%+v", w.Email.BrokenTemplates)
	d := h.getHTMLDetail(t, "ticket_created")
	require.True(t, d.Broken)
	require.Contains(t, d.BrokenReason, "100 KiB")

	res := h.doAsAdminSession(t, http.MethodGet, tmplPath, nil)
	var list []struct {
		Name   string `json:"name"`
		Broken bool   `json:"broken"`
	}
	require.NoError(t, json.Unmarshal(readAll(t, res), &list))
	for _, e := range list {
		if e.Name == "ticket_created" {
			require.True(t, e.Broken, "the list marks it too")
		}
	}
}

func TestEmailTemplatesHTML_PreviewOfAnOverCapDraftIsInvalid(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	code, b := h.putTemplateHTML(t, "_header", "", "", divOf(40*1024))
	require.Equal(t, http.StatusOK, code, string(b)[:min(300, len(b))])
	code, b = h.putTemplateHTML(t, "_footer", "", "", divOf(40*1024))
	require.Equal(t, http.StatusOK, code, string(b)[:min(300, len(b))])
	p := h.previewHTML(t, "ticket_created", map[string]any{"subject": "s", "body": "{{.TicketURL}}", "html": divOf(40*1024) + `<a href="{{.TicketURL}}">x</a>`})
	require.False(t, p.Valid, "Save must stay off")
	require.Equal(t, "html", p.Errors[0].Field)
	require.Contains(t, p.Errors[0].Message, "100 KiB")
}

// ── an absent html key leaves the stored HTML alone ─────────────────────────

func TestEmailTemplatesHTML_AnAbsentHTMLKeyKeepsTheStoredHTMLAndAnEmptyOneClearsIt(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	def := h.getTemplate(t, "ticket_created")
	code, b := h.putTemplateHTML(t, "ticket_created", def.DefaultSubject, def.DefaultBody, goodHTML)
	require.Equal(t, http.StatusOK, code, string(b))

	// An old tab (PR 1) knows nothing of HTML and sends only subject and body.
	res := h.doAsAdminSession(t, http.MethodPut, tmplPath+"/ticket_created", map[string]any{"subject": "Reworded", "body": "{{.TicketURL}} reworded"})
	require.Equal(t, http.StatusOK, res.StatusCode, string(readAll(t, res)))
	d := h.getHTMLDetail(t, "ticket_created")
	require.Equal(t, goodHTML, d.HTML, "the old tab's save did not wipe the HTML")
	require.Equal(t, "{{.TicketURL}} reworded", h.getTemplate(t, "ticket_created").Body)
	entries := h.templateAudit(t, "ticket_created")
	require.Equal(t, goodHTML, entries[0].After["template_html"], "and the audit entry says what is stored")

	// An explicit empty string is the way to remove it.
	code, b = h.putTemplateHTML(t, "ticket_created", "Reworded", "{{.TicketURL}} reworded", "")
	require.Equal(t, http.StatusOK, code, string(b))
	require.Empty(t, h.getHTMLDetail(t, "ticket_created").HTML)
}

func TestEmailTemplatesHTML_AnAbsentKeyWithTheDefaultTextStillKeepsTheRowBecauseOfItsHTML(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	def := h.getTemplate(t, "ticket_created")
	code, _ := h.putTemplateHTML(t, "ticket_created", def.DefaultSubject, def.DefaultBody, goodHTML)
	require.Equal(t, http.StatusOK, code)
	res := h.doAsAdminSession(t, http.MethodPut, tmplPath+"/ticket_created", map[string]any{"subject": def.DefaultSubject, "body": def.DefaultBody})
	require.Equal(t, http.StatusOK, res.StatusCode, string(readAll(t, res)))
	require.Equal(t, goodHTML, h.getHTMLDetail(t, "ticket_created").HTML)
}
