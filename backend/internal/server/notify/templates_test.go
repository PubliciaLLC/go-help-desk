package notify

import (
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/config"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// allEventShapes is one event for every requester and staff template.
func allEventShapes() []notification.Event {
	var out []notification.Event
	for _, typ := range []notification.EventType{
		notification.EventTicketCreated, notification.EventTicketReplied, notification.EventTicketStatusChanged,
		notification.EventTicketResolved, notification.EventTicketReopened, notification.EventGuestLinkResent,
	} {
		out = append(out, notification.Event{Type: typ, TicketID: uuid.New(), TrackingNumber: "GHD-1", Recipient: "a@example.com", GuestToken: "g"})
	}
	for _, typ := range []notification.EventType{notification.EventStaffAssigned, notification.EventStaffRequesterReplied, notification.EventStaffReopened} {
		for _, r := range []notification.StaffSwitch{notification.SwitchAssigned, notification.SwitchGroup, notification.SwitchFollow} {
			out = append(out, notification.Event{Type: typ, TicketID: uuid.New(), TrackingNumber: "GHD-1", Recipient: "a@example.com", StaffReason: r})
		}
	}
	return out
}

// The email template editor (#158). A template is text/template, but only a
// sliver of it is allowed: the words, {{.Name}} for a variable this template
// is given, and {{if .Name}}…{{else}}…{{end}}. Everything else is refused at
// save with the position, because the way to learn a template is broken must
// not be a failed send.

func mustDef(t *testing.T, name string) TemplateDef {
	t.Helper()
	def, ok := LookupTemplate(name)
	require.True(t, ok, "no template %q", name)
	return def
}

// ── the accept / refuse table ────────────────────────────────────────────────

func TestValidate_BodyTable(t *testing.T) {
	def := mustDef(t, "ticket_created")
	for _, tc := range []struct {
		name string
		body string
		// want is empty for a body that must be accepted.
		wantLine, wantCol int
		wantMsg           string
	}{
		{name: "plain text with the link", body: "Open it: {{.TicketURL}}\n"},
		{name: "both variables", body: "Ticket {{.TrackingNumber}}\n{{.TicketURL}}"},
		{name: "if on a variable", body: "{{if .TrackingNumber}}Ticket {{.TrackingNumber}}{{end}}\n{{.TicketURL}}"},
		{name: "if else", body: "{{if .TrackingNumber}}a{{else}}b{{end}}\n{{.TicketURL}}"},
		{name: "else if", body: "{{if .TrackingNumber}}a{{else if .TicketURL}}b{{end}}\n{{.TicketURL}}"},
		{name: "trim markers", body: "x\n{{- .TicketURL -}}\n"},
		{name: "a comment", body: "{{/* the link */}}{{.TicketURL}}"},

		{name: "range", body: "{{.TicketURL}}\n{{range .TrackingNumber}}x{{end}}", wantLine: 2, wantCol: 1, wantMsg: "range"},
		{name: "with", body: "{{.TicketURL}}{{with .TrackingNumber}}x{{end}}", wantLine: 1, wantCol: 15, wantMsg: "with"},
		{name: "template call", body: "{{.TicketURL}}{{template \"x\"}}", wantLine: 1, wantCol: 15, wantMsg: "template"},
		{name: "define", body: "{{.TicketURL}}{{define \"x\"}}y{{end}}", wantLine: 1, wantCol: 15, wantMsg: "define"},
		{name: "block", body: "{{.TicketURL}}{{block \"x\" .}}y{{end}}", wantLine: 1, wantCol: 15, wantMsg: "block"},
		{name: "break", body: "{{.TicketURL}}{{if .TrackingNumber}}{{break}}{{end}}", wantLine: 1, wantCol: 37, wantMsg: "break"},
		{name: "a variable declaration", body: "{{.TicketURL}}\n{{$x := .TrackingNumber}}", wantLine: 2, wantCol: 3, wantMsg: "variable"},
		{name: "a variable use", body: "{{.TicketURL}}{{$}}", wantLine: 1, wantCol: 17, wantMsg: "only"},
		{name: "a pipe", body: "{{.TicketURL | printf \"%s\"}}", wantLine: 1, wantCol: 3, wantMsg: "pipe"},
		{name: "printf", body: "{{.TicketURL}}\n{{printf \"%0999999999d\" 1}}", wantLine: 2, wantCol: 3, wantMsg: "printf"},
		{name: "call", body: "{{.TicketURL}}{{call .TrackingNumber}}", wantLine: 1, wantCol: 17, wantMsg: "call"},
		{name: "index", body: "{{.TicketURL}}{{index .TrackingNumber 0}}", wantLine: 1, wantCol: 17, wantMsg: "index"},
		{name: "len", body: "{{.TicketURL}}{{len .TrackingNumber}}", wantLine: 1, wantCol: 17, wantMsg: "len"},
		{name: "a function in an if", body: "{{.TicketURL}}{{if not .TrackingNumber}}x{{end}}", wantLine: 1, wantCol: 20, wantMsg: "not"},
		{name: "ticket content by path", body: "x {{.Ticket.Subject}} {{.TicketURL}}", wantLine: 1, wantCol: 5, wantMsg: "not allowed"},
		{name: "an unknown variable", body: "{{.TicketURL}}\n{{.Subject}}", wantLine: 2, wantCol: 3, wantMsg: "Subject"},
		{name: "another template's variable", body: "{{.TicketURL}}{{.ResetURL}}", wantLine: 1, wantCol: 17, wantMsg: "ResetURL"},
		{name: "the whole data", body: "{{.TicketURL}}{{.}}", wantLine: 1, wantCol: 17, wantMsg: "only"},
		{name: "a field of a field", body: "{{.TicketURL.Foo}}", wantLine: 1, wantCol: 3, wantMsg: "not allowed"},
		{name: "three parts at the start", body: "{{.TicketURL.Host.X}}", wantLine: 1, wantCol: 3, wantMsg: "not allowed"},
		{name: "four parts after text", body: "x{{.A.B.C.D}}", wantLine: 1, wantCol: 4, wantMsg: "not allowed"},
		{name: "four parts after sixteen characters", body: "xxxxxxxxxxxxxxxx{{.A.B.C.D}}", wantLine: 1, wantCol: 19, wantMsg: "not allowed"},
		{name: "uneven parts", body: "{{.a.bbbbbbbb.c}}", wantLine: 1, wantCol: 3, wantMsg: "not allowed"},
		{name: "a parenthesised pipe", body: "{{.TicketURL}}{{(.TrackingNumber)}}", wantLine: 1, wantCol: 17, wantMsg: "only"},
		{name: "a literal", body: "{{.TicketURL}}{{\"x\"}}", wantLine: 1, wantCol: 17, wantMsg: "only"},

		{name: "an unclosed action", body: "ok\n{{.TicketURL", wantLine: 2, wantCol: 1, wantMsg: "unclosed"},
		{name: "a stray end", body: "{{.TicketURL}}\nx {{end}}", wantLine: 2, wantCol: 3, wantMsg: "end"},
		{name: "an unterminated if", body: "{{.TicketURL}}{{if .TrackingNumber}}x", wantMsg: "{{end}}"},

		{name: "no link at all", body: "Hello.", wantLine: 1, wantCol: 1, wantMsg: "TicketURL"},
		{name: "the link only inside an if", body: "{{if .TrackingNumber}}{{.TicketURL}}{{end}}", wantLine: 1, wantCol: 1, wantMsg: "TicketURL"},
		{name: "the link only in the else branch", body: "{{if .TrackingNumber}}a{{else}}{{.TicketURL}}{{end}}", wantLine: 1, wantCol: 1, wantMsg: "TicketURL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := ValidateTemplate(def, "A subject", tc.body)
			if tc.wantMsg == "" {
				require.Empty(t, errs)
				return
			}
			require.Len(t, errs, 1, "%+v", errs)
			require.Equal(t, "body", errs[0].Field)
			require.Contains(t, errs[0].Message, tc.wantMsg)
			if tc.wantLine != 0 {
				require.Equal(t, tc.wantLine, errs[0].Line, "line")
				require.Equal(t, tc.wantCol, errs[0].Col, "col")
			} else {
				require.NotZero(t, errs[0].Line, "an error with no line cannot be shown inline")
			}
		})
	}
}

func TestValidate_ColumnCountsCharactersNotBytes(t *testing.T) {
	def := mustDef(t, "ticket_created")
	errs := ValidateTemplate(def, "s", "héllo wörld {{range .TrackingNumber}}x{{end}}{{.TicketURL}}")
	require.Len(t, errs, 1)
	require.Equal(t, 1, errs[0].Line)
	require.Equal(t, 13, errs[0].Col)
}

func TestValidate_TextBeforeAnErrorOnALaterLine(t *testing.T) {
	def := mustDef(t, "ticket_created")
	errs := ValidateTemplate(def, "s", "one\ntwo\n  {{index .TicketURL 1}}\n{{.TicketURL}}")
	require.Len(t, errs, 1)
	require.Equal(t, 3, errs[0].Line)
	require.Equal(t, 5, errs[0].Col)
}

// ── caps and bytes ───────────────────────────────────────────────────────────

func TestValidate_Caps(t *testing.T) {
	def := mustDef(t, "ticket_created")
	link := "{{.TicketURL}}"

	t.Run("a body at the cap passes", func(t *testing.T) {
		require.Empty(t, ValidateTemplate(def, "s", link+strings.Repeat("x", MaxBodyBytes-len(link))))
	})
	t.Run("a body over the cap is refused", func(t *testing.T) {
		errs := ValidateTemplate(def, "s", link+strings.Repeat("x", MaxBodyBytes-len(link)+1))
		require.Len(t, errs, 1)
		require.Equal(t, "body", errs[0].Field)
		require.Contains(t, errs[0].Message, "8192")
	})
	t.Run("a subject at the cap passes", func(t *testing.T) {
		require.Empty(t, ValidateTemplate(def, strings.Repeat("s", MaxSubjectBytes), link))
	})
	t.Run("a subject over the cap is refused", func(t *testing.T) {
		errs := ValidateTemplate(def, strings.Repeat("s", MaxSubjectBytes+1), link)
		require.Len(t, errs, 1)
		require.Equal(t, "subject", errs[0].Field)
	})
	t.Run("an empty subject is refused", func(t *testing.T) {
		errs := ValidateTemplate(def, "   ", link)
		require.Len(t, errs, 1)
		require.Equal(t, "subject", errs[0].Field)
	})
	for name, subject := range map[string]string{"LF": "a\nb", "CR": "a\rb", "CRLF": "a\r\nBcc: x@y.z"} {
		t.Run("a subject with "+name+" is refused", func(t *testing.T) {
			errs := ValidateTemplate(def, subject, link)
			require.Len(t, errs, 1)
			require.Equal(t, "subject", errs[0].Field)
			require.Contains(t, errs[0].Message, "one line")
		})
	}
	t.Run("a NUL byte is refused", func(t *testing.T) {
		errs := ValidateTemplate(def, "s", link+"a\x00b")
		require.Len(t, errs, 1)
		require.Contains(t, errs[0].Message, "NUL")
	})
	t.Run("invalid UTF-8 is refused", func(t *testing.T) {
		errs := ValidateTemplate(def, "s", link+"a\xffb")
		require.Len(t, errs, 1)
		require.Contains(t, errs[0].Message, "UTF-8")
	})
}

// Both fields are checked in one pass, so the editor can mark both.
func TestValidate_ReportsBothFields(t *testing.T) {
	def := mustDef(t, "ticket_created")
	errs := ValidateTemplate(def, "{{printf \"x\"}}", "no link here")
	require.Len(t, errs, 2)
	fields := []string{errs[0].Field, errs[1].Field}
	sort.Strings(fields)
	require.Equal(t, []string{"body", "subject"}, fields)
}

func TestValidate_TheSubjectFollowsTheSameRules(t *testing.T) {
	def := mustDef(t, "ticket_created")
	errs := ValidateTemplate(def, "Re {{.Ticket.Subject}}", "{{.TicketURL}}")
	require.Len(t, errs, 1)
	require.Equal(t, "subject", errs[0].Field)
	require.Equal(t, 1, errs[0].Line)
	require.Equal(t, 6, errs[0].Col)
	require.Empty(t, ValidateTemplate(def, "Re {{.TrackingNumber}}", "{{.TicketURL}}"))
	// The subject needs no link of its own.
	require.Empty(t, ValidateTemplate(def, "Hello", "{{.TicketURL}}"))
}

// ── the registry ─────────────────────────────────────────────────────────────

func TestRegistry_HasSeventeenContentTemplatesAndTwoShells(t *testing.T) {
	var content, shells []string
	for _, d := range Templates() {
		if d.Shell {
			shells = append(shells, d.Name)
		} else {
			content = append(content, d.Name)
		}
	}
	require.Len(t, content, 17)
	require.Equal(t, []string{"_header", "_footer"}, shells)
}

// Every embedded file has a registry entry and every entry a file.
func TestRegistry_MatchesTheEmbeddedFiles(t *testing.T) {
	files, err := templateFS.ReadDir("templates")
	require.NoError(t, err)
	var onDisk, inRegistry []string
	for _, f := range files {
		onDisk = append(onDisk, strings.TrimSuffix(f.Name(), ".tmpl"))
	}
	for _, d := range Templates() {
		if !d.Shell {
			inRegistry = append(inRegistry, d.Name)
		}
	}
	sort.Strings(onDisk)
	sort.Strings(inRegistry)
	require.Equal(t, onDisk, inRegistry)
}

// The shipped text passes the validator it ships with, and renders to what the
// compiled file always rendered to.
func TestRegistry_EveryDefaultPassesAndRendersAsTheFile(t *testing.T) {
	for _, def := range Templates() {
		t.Run(def.Name, func(t *testing.T) {
			require.Empty(t, ValidateTemplate(def, def.DefaultSubject, def.DefaultBody), "the default does not pass its own validator")
			vars := SampleVars(def, "https://help.example.com", "GHD")
			_, body, err := RenderTemplate(def, def.DefaultSubject, def.DefaultBody, vars)
			require.NoError(t, err)
			if def.Shell {
				require.Empty(t, body, "a shell defaults to empty so mail is unchanged")
				return
			}
			raw, err := os.ReadFile("templates/" + def.Name + ".tmpl")
			require.NoError(t, err)
			require.Equal(t, string(raw), def.DefaultBody)
		})
	}
}

// The variables are the whole of what a template can see. This is the
// content-free rule (GHSA-p2wx-wvfr-7h3p) as a test: adding a ticket field to
// a template means changing this table, which is a decision, not a side effect.
func TestRegistry_VariableSetsArePinned(t *testing.T) {
	want := map[string][]string{
		"email_verification": {"VerifyURL"},
		"password_reset":     {"ResetURL"},
		"_header":            {"SiteName"},
		"_footer":            {"SiteName"},
	}
	for _, n := range []string{
		"ticket_created", "ticket_replied", "ticket_status_changed", "ticket_resolved", "ticket_reopened", "guest_link",
		"staff_assigned", "staff_assigned_group", "staff_assigned_follow",
		"staff_requester_replied", "staff_requester_replied_group", "staff_requester_replied_follow",
		"staff_reopened", "staff_reopened_group", "staff_reopened_follow",
	} {
		want[n] = []string{"TicketURL", "TrackingNumber"}
	}
	for _, def := range Templates() {
		got := def.VarNames()
		sort.Strings(got)
		require.Equal(t, want[def.Name], got, def.Name)
	}
	require.Len(t, want, 19)
}

// The data the dispatcher actually hands a template has exactly the variables
// the registry allows, no more: the registry is not a promise the dispatcher
// can quietly outgrow.
func TestRegistry_TheViewBuiltPerEventHasExactlyTheAllowedKeys(t *testing.T) {
	d := &EmailDispatcher{cfg: &config.Config{BaseURL: "https://help.example.com"}}
	for _, ev := range allEventShapes() {
		name, _, _, data, ok := d.eventToEmail(ev)
		require.True(t, ok)
		def := mustDef(t, strings.TrimSuffix(name, ".tmpl"))
		view, isMap := data.(map[string]string)
		require.True(t, isMap)
		var keys []string
		for k := range view {
			keys = append(keys, k)
		}
		got := def.VarNames()
		sort.Strings(keys)
		sort.Strings(got)
		require.Equal(t, got, keys, def.Name)
	}
}

// A template whose link is dropped is refused, for every one of the 17 and
// whichever link it is, and a link that only appears under an if does not count.
func TestRegistry_EveryContentTemplateRequiresItsLink(t *testing.T) {
	for _, def := range Templates() {
		if def.Shell {
			continue
		}
		t.Run(def.Name, func(t *testing.T) {
			require.NotEmpty(t, def.Required)
			require.Contains(t, def.VarNames(), def.Required)

			noLink := strings.ReplaceAll(def.DefaultBody, "{{."+def.Required+"}}", "")
			errs := ValidateTemplate(def, def.DefaultSubject, noLink)
			require.Len(t, errs, 1)
			require.Contains(t, errs[0].Message, def.Required)

			inIf := "{{if ." + def.VarNames()[0] + "}}{{." + def.Required + "}}{{end}}"
			errs = ValidateTemplate(def, def.DefaultSubject, inIf)
			require.Len(t, errs, 1, "a link under an if is not always there")
			require.Contains(t, errs[0].Message, def.Required)
		})
	}
}

// The guest's rotated link: the mails that carry it must keep TicketURL.
func TestRegistry_GuestMailRequiresTicketURL(t *testing.T) {
	for _, n := range []string{"guest_link", "ticket_status_changed", "ticket_resolved", "ticket_reopened"} {
		require.Equal(t, "TicketURL", mustDef(t, n).Required, n)
	}
	require.Equal(t, "VerifyURL", mustDef(t, "email_verification").Required)
	require.Equal(t, "ResetURL", mustDef(t, "password_reset").Required)
}

// ── the shared header and footer ─────────────────────────────────────────────

func TestShell_HeaderAndFooterRules(t *testing.T) {
	for _, name := range []string{"_header", "_footer"} {
		def := mustDef(t, name)
		t.Run(name, func(t *testing.T) {
			require.Empty(t, ValidateTemplate(def, "", ""), "empty is the default and is valid")
			require.Empty(t, ValidateTemplate(def, "", "Acme Support\n{{.SiteName}}\n"))
			require.Empty(t, ValidateTemplate(def, "", "{{if .SiteName}}{{.SiteName}}{{end}}"))

			for _, bad := range []string{"{{.TicketURL}}", "{{.TrackingNumber}}", "{{.ResetURL}}", "{{.Ticket.Subject}}", "{{printf \"x\"}}", "{{range .SiteName}}x{{end}}"} {
				errs := ValidateTemplate(def, "", bad)
				require.NotEmpty(t, errs, bad)
				require.Equal(t, "body", errs[0].Field, bad)
			}

			errs := ValidateTemplate(def, "has a subject", "x")
			require.Len(t, errs, 1)
			require.Equal(t, "subject", errs[0].Field)

			errs = ValidateTemplate(def, "", strings.Repeat("x", MaxBodyBytes+1))
			require.Len(t, errs, 1)
		})
	}
}

func TestRender_SubstitutesAndRefusesInvalid(t *testing.T) {
	def := mustDef(t, "ticket_created")
	vars := map[string]string{"TrackingNumber": "GHD-1", "TicketURL": "https://x/t"}

	subj, body, err := RenderTemplate(def, "Got {{.TrackingNumber}}", "See {{.TicketURL}}", vars)
	require.NoError(t, err)
	require.Equal(t, "Got GHD-1", subj)
	require.Equal(t, "See https://x/t", body)

	_, _, err = RenderTemplate(def, "s", "no link", vars)
	var inv *InvalidTemplateError
	require.ErrorAs(t, err, &inv)
	require.Len(t, inv.Errors, 1)
	require.Contains(t, err.Error(), "TicketURL")
}

// A value that looks like a template stays text: execution does not re-parse.
func TestRender_ValuesAreNotParsed(t *testing.T) {
	def := mustDef(t, "ticket_created")
	_, body, err := RenderTemplate(def, "s", "{{.TrackingNumber}} {{.TicketURL}}", map[string]string{
		"TrackingNumber": "{{printf \"x\"}}", "TicketURL": "u",
	})
	require.NoError(t, err)
	require.Equal(t, "{{printf \"x\"}} u", body)
}

func TestSampleVars_AreSafeToMail(t *testing.T) {
	v := SampleVars(mustDef(t, "password_reset"), "https://help.example.com/", "GHD")
	require.Equal(t, "https://help.example.com/reset-password?token=sample-not-a-real-link", v["ResetURL"])
	v = SampleVars(mustDef(t, "email_verification"), "https://help.example.com", "GHD")
	require.Equal(t, "https://help.example.com/verify-email?token=sample-not-a-real-link", v["VerifyURL"])
	v = SampleVars(mustDef(t, "ticket_created"), "https://help.example.com", "ACME")
	require.Equal(t, "https://help.example.com/tickets/00000000-0000-0000-0000-000000000000", v["TicketURL"])
	require.Regexp(t, `^ACME-\d{4}-000042$`, v["TrackingNumber"])
}

// A draft is whatever somebody types, so no input may panic the validator (a
// panic here was a 500 on preview and PUT), and a position is never below 1.
func FuzzValidateTemplate(f *testing.F) {
	for _, seed := range []string{
		"{{.TicketURL.Host.X}}", "x{{.A.B.C.D}}", "{{.a.bbbbbbbb.c}}", "{{", "}}", "{{.", "{{$x := 1}}", "{{(.A)}}",
		"{{if .TicketURL}}{{else if}}", "\n\n{{index}}", "{{/*", "é{{.É.É.É}}", "{{template}}", "{{- -}}", "{{.TicketURL}}",
	} {
		f.Add(seed)
	}
	defs := Templates()
	f.Fuzz(func(t *testing.T, text string) {
		for _, def := range defs[:3] {
			for _, te := range ValidateTemplate(def, text, text) {
				if te.Line < 0 || te.Col < 0 || (te.Line > 0) != (te.Col > 0) {
					t.Fatalf("bad position %+v for %q", te, text)
				}
				if te.Line > 0 && te.Col < 1 {
					t.Fatalf("column below 1: %+v for %q", te, text)
				}
			}
		}
	})
}

func TestLineCol_NeverBelowOneAndNeverPanics(t *testing.T) {
	text := "ab\ncd é"
	for pos := -5; pos < len(text)+5; pos++ {
		line, col := lineCol(text, pos)
		require.GreaterOrEqual(t, line, 1, pos)
		require.GreaterOrEqual(t, col, 1, pos)
	}
}

// A value that smuggles a line break into the subject is caught after render,
// not only at save: the validator saw the sample's values, not these.
func TestRender_RefusesALineBreakInTheRenderedSubject(t *testing.T) {
	def := mustDef(t, "ticket_created")
	for _, v := range []string{"a\nBcc: x@y.z", "a\rb", "a\r\nb"} {
		_, _, err := RenderTemplate(def, "Re {{.TrackingNumber}}", "{{.TicketURL}}", map[string]string{"TrackingNumber": v, "TicketURL": "u"})
		require.Error(t, err, "%q", v)
		require.Contains(t, err.Error(), "line break")
	}
	// The body may hold line breaks.
	_, _, err := RenderTemplate(def, "s", "{{.TrackingNumber}}{{.TicketURL}}", map[string]string{"TrackingNumber": "a\nb", "TicketURL": "u"})
	require.NoError(t, err)
}
