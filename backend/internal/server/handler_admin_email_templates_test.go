package server_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/config"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
)

// The email template editor's API (#158). Session-only: an API key, an OAuth
// client and a member of staff are all refused.

const tmplPath = "/api/v1/admin/email-templates"

type tmplDetail struct {
	Name           string `json:"name"`
	Label          string `json:"label"`
	Group          string `json:"group"`
	Shell          bool   `json:"shell"`
	Subject        string `json:"subject"`
	Body           string `json:"body"`
	DefaultSubject string `json:"default_subject"`
	DefaultBody    string `json:"default_body"`
	Customised     bool   `json:"customised"`
	Required       string `json:"required"`
	Variables      []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"variables"`
	Broken       bool   `json:"broken"`
	BrokenReason string `json:"broken_reason"`
}

type tmplErr struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Field   string `json:"field"`
		Line    int    `json:"line"`
		Col     int    `json:"col"`
		Errors  []struct {
			Field string `json:"field"`
			Line  int    `json:"line"`
			Col   int    `json:"col"`
		} `json:"errors"`
	} `json:"error"`
}

func (h *harness) getTemplate(t *testing.T, name string) tmplDetail {
	t.Helper()
	res := h.doAsAdminSession(t, http.MethodGet, tmplPath+"/"+name, nil)
	b := readAll(t, res)
	require.Equal(t, http.StatusOK, res.StatusCode, string(b))
	var d tmplDetail
	require.NoError(t, json.Unmarshal(b, &d))
	return d
}

func (h *harness) putTemplate(t *testing.T, name, subject, body string) (int, []byte) {
	t.Helper()
	res := h.doAsAdminSession(t, http.MethodPut, tmplPath+"/"+name, map[string]any{"subject": subject, "body": body})
	b := readAll(t, res)
	return res.StatusCode, b
}

func (h *harness) templateAudit(t *testing.T, name string) []audit.Entry {
	t.Helper()
	entries, err := h.auditStore.ListByEntity(context.Background(), "email_template", uuid.NewSHA1(uuid.NameSpaceOID, []byte("email_template:"+name)), 50, 0)
	require.NoError(t, err)
	return entries
}

// ── who may ──────────────────────────────────────────────────────────────────

func TestEmailTemplates_OnlyASignedInAdministrator(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	client := h.newServiceClient(t, "svc", "settings:read", "settings:write")

	routes := []struct{ method, path string }{
		{http.MethodGet, tmplPath},
		{http.MethodGet, tmplPath + "/ticket_created"},
		{http.MethodPut, tmplPath + "/ticket_created"},
		{http.MethodDelete, tmplPath + "/ticket_created"},
		{http.MethodPost, tmplPath + "/ticket_created/preview"},
		{http.MethodPost, tmplPath + "/ticket_created/test"},
	}
	send := func(auth, cookie, method, path string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(`{"subject":"s","body":"{{.TicketURL}}"}`))
		req.Header.Set("Content-Type", "application/json")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		rr := httptest.NewRecorder()
		h.srv.ServeHTTP(rr, req)
		return rr.Code
	}
	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			require.Equal(t, http.StatusUnauthorized, send("", "", rt.method, rt.path), "no credential")
			require.Equal(t, http.StatusForbidden, send("ApiKey "+h.adminKey, "", rt.method, rt.path), "an administrator's API key")
			require.Equal(t, http.StatusForbidden, send("Bearer "+client.Token, "", rt.method, rt.path), "an OAuth client")
			require.Equal(t, http.StatusForbidden, send("", h.session(t, asStaff), rt.method, rt.path), "staff")
			require.Equal(t, http.StatusForbidden, send("", h.session(t, asReporter), rt.method, rt.path), "a requester")
		})
	}
	// Nothing was changed by any of the refused attempts.
	require.False(t, h.getTemplate(t, "ticket_created").Customised)
}

// ── reading ──────────────────────────────────────────────────────────────────

func TestEmailTemplates_ListHasAllNineteenInGroups(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	res := h.doAsAdminSession(t, http.MethodGet, tmplPath, nil)
	b := readAll(t, res)
	require.Equal(t, http.StatusOK, res.StatusCode, string(b))
	var list []struct {
		Name       string `json:"name"`
		Group      string `json:"group"`
		Customised bool   `json:"customised"`
		Broken     bool   `json:"broken"`
	}
	require.NoError(t, json.Unmarshal(b, &list))
	require.Len(t, list, 19)
	groups := map[string]int{}
	for _, e := range list {
		groups[e.Group]++
		require.False(t, e.Customised, e.Name)
		require.False(t, e.Broken, e.Name)
	}
	require.Equal(t, map[string]int{"requester": 6, "staff": 9, "account": 2, "shared": 2}, groups)
}

func TestEmailTemplates_DetailCarriesTheDefaultTheVariablesAndTheRequiredLink(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	d := h.getTemplate(t, "password_reset")
	require.Equal(t, "account", d.Group)
	require.Equal(t, "ResetURL", d.Required)
	require.Equal(t, d.DefaultBody, d.Body)
	require.Equal(t, "Reset your password", d.Subject)
	require.False(t, d.Customised)
	require.Len(t, d.Variables, 1)
	require.Equal(t, "ResetURL", d.Variables[0].Name)
	require.NotEmpty(t, d.Variables[0].Description)

	hdr := h.getTemplate(t, "_header")
	require.True(t, hdr.Shell)
	require.Empty(t, hdr.Body)
	require.Empty(t, hdr.Required)
}

func TestEmailTemplates_UnknownNameIs404(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	for _, m := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		res := h.doAsAdminSession(t, m, tmplPath+"/not_a_template", map[string]any{"subject": "s", "body": "b"})
		readAll(t, res)
		require.Equal(t, http.StatusNotFound, res.StatusCode, m)
	}
}

// ── saving ───────────────────────────────────────────────────────────────────

func TestEmailTemplates_SaveStoresAndAuditsTheFullText(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	code, b := h.putTemplate(t, "ticket_created", "Thanks {{.TrackingNumber}}", "Hello.\n{{.TicketURL}}\n")
	require.Equal(t, http.StatusOK, code, string(b))

	d := h.getTemplate(t, "ticket_created")
	require.True(t, d.Customised)
	require.Equal(t, "Thanks {{.TrackingNumber}}", d.Subject)
	require.Equal(t, "Hello.\n{{.TicketURL}}\n", d.Body)
	require.NotEqual(t, d.DefaultBody, d.Body)

	entries := h.templateAudit(t, "ticket_created")
	require.Len(t, entries, 1)
	e := entries[0]
	require.Equal(t, "email_template_saved", e.Action)
	require.NotNil(t, e.ActorID)
	require.Equal(t, h.adminID, *e.ActorID)
	require.Equal(t, "Thanks {{.TrackingNumber}}", e.After["template_subject"])
	require.Equal(t, "Hello.\n{{.TicketURL}}\n", e.After["template_body"])
	require.Equal(t, d.DefaultBody, e.Before["template_body"], "with no earlier edit, before is what was in effect: the default")
	require.Equal(t, "ticket_created", e.After["template_name"])

	// A second save: before is the first edit.
	code, b = h.putTemplate(t, "ticket_created", "Again", "{{.TicketURL}}")
	require.Equal(t, http.StatusOK, code, string(b))
	entries = h.templateAudit(t, "ticket_created")
	require.Len(t, entries, 2)
	require.Equal(t, "Hello.\n{{.TicketURL}}\n", entries[0].Before["template_body"]) // newest first
}

// The audit view shows the text, and still hides a ticket's own subject.
func TestEmailTemplates_TheAuditViewShowsTemplateTextAndStillHidesTicketSubjects(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	code, b := h.putTemplate(t, "ticket_created", "Seen {{.TrackingNumber}}", "{{.TicketURL}} visible body")
	require.Equal(t, http.StatusOK, code, string(b))
	createTicketID(t, h, "SECRET TICKET SUBJECT")

	res := h.doAsAdminSession(t, http.MethodGet, "/api/v1/admin/audit?limit=100", nil)
	body := string(readAll(t, res))
	require.Equal(t, http.StatusOK, res.StatusCode, body)
	require.Contains(t, body, "Seen {{.TrackingNumber}}")
	require.Contains(t, body, "visible body")
	require.NotContains(t, body, "SECRET TICKET SUBJECT")
}

func TestEmailTemplates_SaveRefusesWhatTheValidatorRefuses(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	for _, tc := range []struct {
		name, tmpl, subject, body string
		field                     string
		line, col                 int
	}{
		{"a function", "ticket_created", "s", "{{.TicketURL}}\n{{printf \"x\"}}", "body", 2, 3},
		{"range", "ticket_created", "s", "{{range .TrackingNumber}}x{{end}}{{.TicketURL}}", "body", 1, 1},
		{"ticket content", "ticket_created", "s", "{{.Ticket.Subject}} {{.TicketURL}}", "body", 1, 3},
		{"no link", "guest_link", "s", "just words", "body", 1, 1},
		{"a link inside an if", "password_reset", "s", "{{if .ResetURL}}{{.ResetURL}}{{end}}", "body", 1, 1},
		{"a bad subject", "ticket_created", "{{.Subject}}", "{{.TicketURL}}", "subject", 1, 3},
		{"a header that wants a ticket", "_header", "", "{{.TicketURL}}", "body", 1, 3},
		{"a footer with a subject", "_footer", "no", "x", "subject", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, b := h.putTemplate(t, tc.tmpl, tc.subject, tc.body)
			require.Equal(t, http.StatusBadRequest, code, string(b))
			var e tmplErr
			require.NoError(t, json.Unmarshal(b, &e))
			require.Equal(t, "invalid_template", e.Error.Code)
			require.Equal(t, tc.field, e.Error.Field)
			require.Equal(t, tc.line, e.Error.Line)
			require.Equal(t, tc.col, e.Error.Col)
			require.NotEmpty(t, e.Error.Message)
			require.NotEmpty(t, e.Error.Errors)

			require.False(t, h.getTemplate(t, tc.tmpl).Customised, "a refused save stores nothing")
			require.Empty(t, h.templateAudit(t, tc.tmpl), "and records nothing")
		})
	}
}

func TestEmailTemplates_SaveReportsBothBadFields(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	code, b := h.putTemplate(t, "ticket_created", "{{.Nope}}", "no link")
	require.Equal(t, http.StatusBadRequest, code)
	var e tmplErr
	require.NoError(t, json.Unmarshal(b, &e))
	require.Len(t, e.Error.Errors, 2)
}

func TestEmailTemplates_SaveRefusesAHugeBody(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	code, _ := h.putTemplate(t, "ticket_created", "s", "{{.TicketURL}}"+strings.Repeat("x", 9000))
	require.Equal(t, http.StatusBadRequest, code)
}

func TestEmailTemplates_SavingTheDefaultDeletesTheRow(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	def := h.getTemplate(t, "ticket_created")

	// Saving the default when nothing is stored changes nothing and records nothing.
	code, b := h.putTemplate(t, "ticket_created", def.DefaultSubject, def.DefaultBody)
	require.Equal(t, http.StatusOK, code, string(b))
	require.False(t, h.getTemplate(t, "ticket_created").Customised)
	require.Empty(t, h.templateAudit(t, "ticket_created"))

	code, _ = h.putTemplate(t, "ticket_created", "Custom", "{{.TicketURL}}")
	require.Equal(t, http.StatusOK, code)
	require.True(t, h.getTemplate(t, "ticket_created").Customised)

	code, _ = h.putTemplate(t, "ticket_created", def.DefaultSubject, def.DefaultBody)
	require.Equal(t, http.StatusOK, code)
	require.False(t, h.getTemplate(t, "ticket_created").Customised, "the row is gone, not a copy of the default")
	var n int
	require.NoError(t, h.tx.QueryRow(`SELECT count(*) FROM email_template_overrides WHERE name = 'ticket_created'`).Scan(&n))
	require.Zero(t, n)
	require.Len(t, h.templateAudit(t, "ticket_created"), 2)
}

func TestEmailTemplates_HeaderAndFooterAreStoredLikeTheRest(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	code, b := h.putTemplate(t, "_header", "", "== {{.SiteName}} ==\n")
	require.Equal(t, http.StatusOK, code, string(b))
	d := h.getTemplate(t, "_header")
	require.True(t, d.Customised)
	require.Equal(t, "== {{.SiteName}} ==\n", d.Body)
	// Empty is the default, so saving it removes the row.
	code, _ = h.putTemplate(t, "_header", "", "")
	require.Equal(t, http.StatusOK, code)
	require.False(t, h.getTemplate(t, "_header").Customised)
}

// ── reverting ────────────────────────────────────────────────────────────────

func TestEmailTemplates_RevertDeletesIsIdempotentAndAuditsOnce(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	code, _ := h.putTemplate(t, "ticket_created", "Custom", "{{.TicketURL}} mine")
	require.Equal(t, http.StatusOK, code)

	res := h.doAsAdminSession(t, http.MethodDelete, tmplPath+"/ticket_created", nil)
	readAll(t, res)
	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.False(t, h.getTemplate(t, "ticket_created").Customised)

	entries := h.templateAudit(t, "ticket_created")
	require.Len(t, entries, 2)
	rev := entries[0]
	require.Equal(t, "email_template_reverted", rev.Action)
	require.Equal(t, "{{.TicketURL}} mine", rev.Before["template_body"], "the old text survives in the log")
	require.Nil(t, rev.After)

	res = h.doAsAdminSession(t, http.MethodDelete, tmplPath+"/ticket_created", nil)
	readAll(t, res)
	require.Equal(t, http.StatusNoContent, res.StatusCode, "reverting again is fine")
	require.Len(t, h.templateAudit(t, "ticket_created"), 2, "and records nothing: nothing changed")
}

// ── preview and live validation ──────────────────────────────────────────────

func TestEmailTemplates_PreviewRendersSampleDataAndSendsNothing(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	res := h.doAsAdminSession(t, http.MethodPost, tmplPath+"/ticket_created/preview",
		map[string]any{"subject": "Hi {{.TrackingNumber}}", "body": "Open {{.TicketURL}}"})
	b := readAll(t, res)
	require.Equal(t, http.StatusOK, res.StatusCode, string(b))
	var p struct {
		Valid   bool   `json:"valid"`
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	require.NoError(t, json.Unmarshal(b, &p))
	require.True(t, p.Valid)
	require.Regexp(t, `^Hi GHD-\d{4}-000042$`, p.Subject)
	require.Equal(t, "Open http://localhost:8080/tickets/00000000-0000-0000-0000-000000000000", p.Body)
	require.Empty(t, h.templateAudit(t, "ticket_created"))
	require.False(t, h.getTemplate(t, "ticket_created").Customised, "a preview stores nothing")
}

// The live check: an invalid draft is answered, not refused, with every
// problem and its place, so the editor can mark each field.
func TestEmailTemplates_PreviewValidatesBothFieldsWithoutStoring(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	res := h.doAsAdminSession(t, http.MethodPost, tmplPath+"/ticket_created/preview",
		map[string]any{"subject": "{{.Gone}}", "body": "words\n  {{index .TicketURL 1}}"})
	b := readAll(t, res)
	require.Equal(t, http.StatusOK, res.StatusCode, string(b))
	var p struct {
		Valid  bool `json:"valid"`
		Errors []struct {
			Field   string `json:"field"`
			Line    int    `json:"line"`
			Col     int    `json:"col"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	require.NoError(t, json.Unmarshal(b, &p))
	require.False(t, p.Valid)
	require.Len(t, p.Errors, 2)
	for _, e := range p.Errors {
		if e.Field == "body" {
			require.Equal(t, 2, e.Line)
			require.Equal(t, 5, e.Col)
			require.Contains(t, e.Message, "index")
		}
		require.NotEmpty(t, e.Message)
	}
}

func TestEmailTemplates_PreviewOfAHeaderShowsItAroundASampleMail(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	res := h.doAsAdminSession(t, http.MethodPost, tmplPath+"/_header/preview", map[string]any{"subject": "", "body": "Hello from {{.SiteName}}\n"})
	var p struct {
		Valid bool   `json:"valid"`
		Body  string `json:"body"`
	}
	require.NoError(t, json.Unmarshal(readAll(t, res), &p))
	require.True(t, p.Valid)
	require.True(t, strings.HasPrefix(p.Body, "Hello from Go Help Desk\n"))
	require.Contains(t, p.Body, "We have received your ticket.")
}

// ── test send ────────────────────────────────────────────────────────────────

// recordingSMTP accepts mail and keeps every RCPT and the last DATA.
type recordingSMTP struct {
	addr string
	mu   chan struct{}
	rcpt []string
	data []string
}

func newRecordingSMTP(t *testing.T) *recordingSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	s := &recordingSMTP{addr: ln.Addr().String(), mu: make(chan struct{}, 1)}
	s.mu <- struct{}{}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				r := bufio.NewReader(conn)
				say := func(l string) { _, _ = conn.Write([]byte(l + "\r\n")) }
				say("220 test")
				var cur strings.Builder
				inData := false
				for {
					_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if inData {
						if line == ".\r\n" {
							inData = false
							<-s.mu
							s.data = append(s.data, cur.String())
							s.mu <- struct{}{}
							cur.Reset()
							say("250 ok")
							continue
						}
						cur.WriteString(line)
						continue
					}
					switch {
					case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
						say("250-test")
						say("250 OK")
					case strings.HasPrefix(line, "RCPT"):
						<-s.mu
						s.rcpt = append(s.rcpt, strings.TrimSpace(line))
						s.mu <- struct{}{}
						say("250 OK")
					case strings.HasPrefix(line, "DATA"):
						say("354 go")
						inData = true
					case strings.HasPrefix(line, "QUIT"):
						say("221 bye")
						return
					default:
						say("250 OK")
					}
				}
			}()
		}
	}()
	return s
}

func (s *recordingSMTP) snapshot() (rcpt, data []string) {
	<-s.mu
	defer func() { s.mu <- struct{}{} }()
	return append([]string(nil), s.rcpt...), append([]string(nil), s.data...)
}

func harnessWithSMTP(t *testing.T) (*harness, *recordingSMTP, func()) {
	t.Helper()
	smtp := newRecordingSMTP(t)
	host, portStr, err := net.SplitHostPort(smtp.addr)
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	h, cleanup := newHarnessPasskeys(t, 0, "", func(c *config.Config) {
		c.SMTPHost, c.SMTPPort, c.SMTPFrom = host, port, "helpdesk@example.com"
	}, true)
	return h, smtp, cleanup
}

func TestEmailTemplates_TestSendGoesOnlyToTheSignedInAdmin(t *testing.T) {
	h, smtp, cleanup := harnessWithSMTP(t)
	defer cleanup()

	// A recipient in the request is not a thing: it is ignored, not honoured.
	res := h.doAsAdminSession(t, http.MethodPost, tmplPath+"/ticket_created/test", map[string]any{
		"subject": "Try {{.TrackingNumber}}", "body": "Open {{.TicketURL}}",
		"to": "victim@example.com", "recipient": "victim@example.com", "email": "victim@example.com",
	})
	b := readAll(t, res)
	require.Equal(t, http.StatusOK, res.StatusCode, string(b))

	rcpt, data := smtp.snapshot()
	require.Len(t, rcpt, 1)
	require.Contains(t, rcpt[0], "admin@test.local")
	require.NotContains(t, strings.Join(rcpt, " "), "victim")
	require.Len(t, data, 1)
	require.Contains(t, data[0], "Subject: [Test] Try GHD-")
	require.Contains(t, data[0], "To: <admin@test.local>")
	require.NotContains(t, data[0], "victim")

	entries := h.templateAudit(t, "ticket_created")
	require.Len(t, entries, 1)
	require.Equal(t, "email_template_test_sent", entries[0].Action)
	require.Equal(t, "ticket_created", entries[0].After["template_name"])
	raw, _ := json.Marshal(entries[0])
	require.NotContains(t, string(raw), "admin@test.local", "the audit entry never holds the address")
}

func TestEmailTemplates_TestSendOfTheSavedTextWhenNoDraftIsGiven(t *testing.T) {
	h, smtp, cleanup := harnessWithSMTP(t)
	defer cleanup()
	code, _ := h.putTemplate(t, "ticket_created", "Saved subject", "Saved body {{.TicketURL}}")
	require.Equal(t, http.StatusOK, code)

	res := h.doAsAdminSession(t, http.MethodPost, tmplPath+"/ticket_created/test", map[string]any{})
	require.Equal(t, http.StatusOK, res.StatusCode, string(readAll(t, res)))
	_, data := smtp.snapshot()
	require.Len(t, data, 1)
	require.Contains(t, data[0], "Subject: [Test] Saved subject")
}

func TestEmailTemplates_TestSendRefusesAnInvalidDraft(t *testing.T) {
	h, smtp, cleanup := harnessWithSMTP(t)
	defer cleanup()
	res := h.doAsAdminSession(t, http.MethodPost, tmplPath+"/ticket_created/test", map[string]any{"subject": "s", "body": "no link"})
	b := readAll(t, res)
	require.Equal(t, http.StatusBadRequest, res.StatusCode, string(b))
	require.Equal(t, "invalid_template", errCode(t, b))
	_, data := smtp.snapshot()
	require.Empty(t, data)
}

func TestEmailTemplates_TestSendIsLimitedToFiveEveryTenMinutes(t *testing.T) {
	h, smtp, cleanup := harnessWithSMTP(t)
	defer cleanup()
	for i := 0; i < 5; i++ {
		res := h.doAsAdminSession(t, http.MethodPost, tmplPath+"/ticket_created/test", map[string]any{})
		require.Equal(t, http.StatusOK, res.StatusCode, fmt.Sprintf("send %d: %s", i+1, readAll(t, res)))
	}
	res := h.doAsAdminSession(t, http.MethodPost, tmplPath+"/ticket_created/test", map[string]any{})
	b := readAll(t, res)
	require.Equal(t, http.StatusTooManyRequests, res.StatusCode, string(b))
	secs, err := strconv.Atoi(res.Header.Get("Retry-After"))
	require.NoError(t, err)
	require.Positive(t, secs)
	require.LessOrEqual(t, secs, 600)
	_, data := smtp.snapshot()
	require.Len(t, data, 5)
}

func TestEmailTemplates_TestSendWithEmailOffIs503(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	res := h.doAsAdminSession(t, http.MethodPost, tmplPath+"/ticket_created/test", map[string]any{})
	b := readAll(t, res)
	require.Equal(t, http.StatusServiceUnavailable, res.StatusCode, string(b))
	require.Equal(t, "email_not_configured", errCode(t, b))
}

func TestEmailTemplates_TestSendReturnsTheSMTPError(t *testing.T) {
	h, cleanup := newHarnessPasskeys(t, 0, "", func(c *config.Config) {
		c.SMTPHost, c.SMTPPort, c.SMTPFrom = "127.0.0.1", 1, "helpdesk@example.com" // nothing listens
	}, true)
	defer cleanup()
	res := h.doAsAdminSession(t, http.MethodPost, tmplPath+"/ticket_created/test", map[string]any{})
	b := readAll(t, res)
	require.Equal(t, http.StatusBadGateway, res.StatusCode, string(b))
	require.Equal(t, "send_failed", errCode(t, b))
	require.Contains(t, string(b), "dialing SMTP server")
}

// ── the broken-template notice ───────────────────────────────────────────────

type tmplWarningsBody struct {
	Email struct {
		BrokenTemplates []struct {
			Name      string `json:"name"`
			Label     string `json:"label"`
			Reason    string `json:"reason"`
			UpdatedAt string `json:"updated_at"`
		} `json:"broken_templates"`
	} `json:"email"`
}

func (h *harness) tmplWarnings(t *testing.T) tmplWarningsBody {
	t.Helper()
	res := h.doAsAdminSession(t, http.MethodGet, "/api/v1/admin/security-warnings", nil)
	b := readAll(t, res)
	require.Equal(t, http.StatusOK, res.StatusCode, string(b))
	var w tmplWarningsBody
	require.NoError(t, json.Unmarshal(b, &w))
	return w
}

// An override written under an older release and no longer valid: seeded
// straight into the table, since the API would refuse it.
func (h *harness) seedStale(t *testing.T, name, subject, body string) {
	t.Helper()
	_, err := h.tx.Exec(`INSERT INTO email_template_overrides (name, subject, body) VALUES ($1, $2, $3)`, name, subject, body)
	require.NoError(t, err)
}

func TestEmailTemplates_AStaleOverrideIsListedAsBroken(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	require.Empty(t, h.tmplWarnings(t).Email.BrokenTemplates)

	h.seedStale(t, "password_reset", "Reset", "the link was dropped")
	w := h.tmplWarnings(t)
	require.Len(t, w.Email.BrokenTemplates, 1)
	b := w.Email.BrokenTemplates[0]
	require.Equal(t, "password_reset", b.Name)
	require.Equal(t, "Reset your password", b.Label)
	require.Contains(t, b.Reason, "ResetURL")
	require.NotEmpty(t, b.UpdatedAt, "the notice's signature is the name and when it was saved")

	d := h.getTemplate(t, "password_reset")
	require.True(t, d.Customised)
	require.True(t, d.Broken)
	require.Contains(t, d.BrokenReason, "ResetURL")

	// One click reverts it, and the notice goes at once.
	res := h.doAsAdminSession(t, http.MethodDelete, tmplPath+"/password_reset", nil)
	readAll(t, res)
	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.Empty(t, h.tmplWarnings(t).Email.BrokenTemplates)
	require.False(t, h.getTemplate(t, "password_reset").Broken)
}

func TestEmailTemplates_ABrokenHeaderIsNamedAndListIsMarked(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	h.seedStale(t, "_header", "", "{{.TicketURL}}")
	w := h.tmplWarnings(t)
	require.Len(t, w.Email.BrokenTemplates, 1)
	require.Equal(t, "Header", w.Email.BrokenTemplates[0].Label)

	res := h.doAsAdminSession(t, http.MethodGet, tmplPath, nil)
	var list []struct {
		Name   string `json:"name"`
		Broken bool   `json:"broken"`
	}
	require.NoError(t, json.Unmarshal(readAll(t, res), &list))
	for _, e := range list {
		require.Equal(t, e.Name == "_header", e.Broken, e.Name)
	}
}

func TestEmailTemplates_AValidOverrideIsNotBroken(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	code, _ := h.putTemplate(t, "ticket_created", "Fine", "{{.TicketURL}}")
	require.Equal(t, http.StatusOK, code)
	require.Empty(t, h.tmplWarnings(t).Email.BrokenTemplates)
}
