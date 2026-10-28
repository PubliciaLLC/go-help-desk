package notify

import (
	"fmt"
	htmltemplate "html"
	htpl "html/template"
	"regexp"
	"strings"
	"text/template/parse"
	"time"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/emailtemplate"
)

// Which templates have an HTML part, and how big it comes to.
//
// A template has one when it has HTML of its own, or when the shared header or
// footer has HTML: then EVERY mail is multipart, and a template with no HTML of
// its own has its text turned into paragraphs (htmlFromText). With no HTML
// saved anywhere, mail is one text part, as it always was.
//
// The size limit is on the whole part (header, body, footer): that is what a
// mail client clips. A save checks it for what it would join with, a send checks
// it again, and a combination that is over fails the mail, so the save has to
// measure the way the send can only do better than: with long links.

// OverCap is a template whose HTML part would pass the size limit.
type OverCap struct {
	Name, Label string
	Bytes       int
	Problem     string
}

// Worst-case values, so that what passes a save cannot fail a send. A link is
// at most this long in practice (browsers and relays stop near it), and a
// guest's carries a token. The site name is the real one (the settings do not
// cap it, so a guess would be wrong one way or the other): renaming the site
// to something long shows the red notices at once, and the settings save refuses
// a name that would push a template over.
const (
	worstURLBytes      = 2048
	worstTrackingBytes = 64
	defaultSiteName    = "Go Help Desk"
)

func worstCaseVars(def TemplateDef, siteName string) map[string]string {
	out := map[string]string{}
	for _, v := range def.Vars {
		switch {
		case linkVariables[v.Name]:
			out[v.Name] = "https://" + strings.Repeat("h", worstURLBytes-len("https://"))
		case v.Name == "SiteName":
			out[v.Name] = siteName
		default:
			out[v.Name] = strings.Repeat("N", worstTrackingBytes)
		}
	}
	return out
}

type countingWriter struct{ n int }

func (c *countingWriter) Write(p []byte) (int, error) { c.n += len(p); return len(p), nil }

// htmlSize is how many bytes src renders to with the worst-case values.
func htmlSize(def TemplateDef, src, siteName string) (int, error) {
	if src == "" {
		return 0, nil
	}
	t, err := htpl.New(def.Name).Option("missingkey=error").Parse(src)
	if err != nil {
		return 0, err
	}
	var w countingWriter
	if err := t.Execute(&w, worstCaseVars(def, siteName)); err != nil {
		return 0, err
	}
	return w.n, nil
}

// shellSize is the size of the header's or footer's contribution: its HTML, or
// its text turned into paragraphs when it has no HTML. A failure to make HTML
// from text is returned, never counted as 0.
func shellSize(def TemplateDef, ov emailtemplate.Override, siteName string) (int, error) {
	switch {
	case ov.HTML != "":
		// The administrator's own HTML that does not render is reported on its
		// own, as broken; it is not this check's finding.
		n, _ := htmlSize(def, ov.HTML, siteName)
		return n, nil
	case ov.Body != "":
		src, err := htmlFromText(def, ov.Body)
		if err != nil {
			return 0, err
		}
		return htmlSize(def, src, siteName)
	}
	return 0, nil
}

// OverCapHTML lists, in the editor's order, every template whose HTML part
// would come to more than MaxRenderedHTMLBytes given these stored rows, or
// whose HTML part cannot be made at all (Problem set). The shell is named when
// it is the one that cannot be made.
func OverCapHTML(ovs map[string]emailtemplate.Override, siteName string) []OverCap {
	shellHTML := ovs[NameHeader].HTML != "" || ovs[NameFooter].HTML != ""
	var out []OverCap
	shells := 0
	if shellHTML {
		for _, name := range []string{NameHeader, NameFooter} {
			def, _ := LookupTemplate(name)
			n, err := shellSize(def, ovs[name], siteName)
			if err != nil {
				out = append(out, OverCap{Name: name, Label: def.Label, Problem: err.Error()})
				continue
			}
			shells += n
		}
	}

	for _, def := range Templates() {
		if def.Shell {
			continue
		}
		ov, edited := ovs[def.Name]
		var own int
		switch {
		case edited && ov.HTML != "":
			own, _ = htmlSize(def, ov.HTML, siteName) // broken on its own if it does not render
		case shellHTML:
			text := def.DefaultBody
			if edited {
				text = ov.Body
			}
			src, err := htmlFromText(def, text)
			if err == nil {
				own, err = htmlSize(def, src, siteName)
			}
			if err != nil {
				out = append(out, OverCap{Name: def.Name, Label: def.Label, Problem: err.Error()})
				continue
			}
		default:
			continue
		}
		if total := shells + own; total > MaxRenderedHTMLBytes {
			out = append(out, OverCap{Name: def.Name, Label: def.Label, Bytes: total})
		}
	}
	return out
}

// CheckJoinedHTML is the save-time check of the size limit: the stored rows with
// candidate in place of def's. For a content template it is that template's
// HTML part; for the header or footer it is every template's, because each one
// is wrapped in it. It names the template that would go over. nil is fine.
func CheckJoinedHTML(def TemplateDef, ovs map[string]emailtemplate.Override, candidate emailtemplate.Override, siteName string) *TemplateError {
	next := make(map[string]emailtemplate.Override, len(ovs)+1)
	for k, v := range ovs {
		next[k] = v
	}
	candidate.Name = def.Name
	next[def.Name] = candidate

	for _, o := range OverCapHTML(next, siteName) {
		switch {
		case o.Problem != "" && (def.Shell || o.Name == def.Name):
			return &TemplateError{Field: "html", Message: fmt.Sprintf(
				"the HTML part of %q cannot be made from its text (%s), so no mail that uses it could be sent as it is; fix or revert that template first",
				o.Label, o.Problem)}
		case o.Problem != "":
		case def.Shell:
			return &TemplateError{Field: "html", Message: fmt.Sprintf(
				"with this %s the HTML part of %q would come to %d bytes (measured with long links); the limit is 100 KiB, where mail clients cut a message off; shorten it, or the other shared part",
				strings.ToLower(def.Label), o.Label, o.Bytes)}
		case o.Name == def.Name:
			return &TemplateError{Field: "html", Message: fmt.Sprintf(
				"the HTML part of %q would come to %d bytes with the header and footer (measured with long links); the limit is 100 KiB, where mail clients cut a message off; shorten it",
				o.Label, o.Bytes)}
		}
	}
	return nil
}

// overCapBroken turns the over-cap combinations among stored rows into broken
// templates, dated by the newest row that is part of the combination.
func overCapBroken(rows []emailtemplate.Override, siteName string) []BrokenTemplate {
	ovs := make(map[string]emailtemplate.Override, len(rows))
	for _, r := range rows {
		ovs[r.Name] = r
	}
	var out []BrokenTemplate
	for _, o := range OverCapHTML(ovs, siteName) {
		at := newest(ovs[o.Name].UpdatedAt, ovs[NameHeader].UpdatedAt, ovs[NameFooter].UpdatedAt)
		if o.Problem != "" {
			out = append(out, BrokenTemplate{Name: o.Name, Label: o.Label, UpdatedAt: at,
				Reason: "html: the HTML part cannot be made from the text: " + o.Problem})
			continue
		}
		out = append(out, BrokenTemplate{
			Name: o.Name, Label: o.Label, UpdatedAt: at,
			Reason: fmt.Sprintf("html: the HTML part comes to %d bytes with the header and footer (measured with long links); the limit is 100 KiB, where mail clients cut a message off", o.Bytes),
		})
	}
	return out
}

func newest(ts ...time.Time) time.Time {
	var m time.Time
	for _, t := range ts {
		if t.After(m) {
			m = t
		}
	}
	return m
}

// RenderHTMLUnchecked renders html/template source without validating it: for
// source this package generated itself.
func RenderHTMLUnchecked(def TemplateDef, src string, vars map[string]string) (string, error) {
	return executeHTML(def, src, vars)
}

var (
	blankLine       = regexp.MustCompile("\n[ \t\r]*\n\\s*")
	trailingBreaks  = regexp.MustCompile(`(<br>\n)+</p>`)
	leadingBreaks   = regexp.MustCompile(`^<p>(<br>)?\s*`)
	emptyParagraphs = regexp.MustCompile(`(\n)?<p></p>`)
)

// htmlFromText turns a text template into html/template source: the text as
// paragraphs (a blank line starts one, a line break is a <br>), each value kept
// as an action so html/template escapes it where it lands, and each link
// variable written as a clickable link. The text is escaped here and the values
// there, so nothing the administrator or a value holds is ever markup.
func htmlFromText(def TemplateDef, text string) (string, error) {
	tree, _, te := parseAndWalk(def, "body", text)
	if te != nil {
		return "", te
	}
	var b strings.Builder
	b.WriteString("<p>")
	writeTextAsHTML(&b, tree.Root)
	b.WriteString("</p>")
	out := b.String()
	out = trailingBreaks.ReplaceAllString(out, "</p>")
	out = leadingBreaks.ReplaceAllString(out, "<p>")
	out = emptyParagraphs.ReplaceAllString(out, "")
	return out, nil
}

func writeTextAsHTML(b *strings.Builder, l *parse.ListNode) {
	if l == nil {
		return
	}
	for _, n := range l.Nodes {
		switch n := n.(type) {
		case *parse.TextNode:
			t := htmlescape(string(n.Text))
			t = blankLine.ReplaceAllString(t, "</p>\n<p>")
			t = strings.ReplaceAll(t, "\n", "<br>\n")
			// A "<br>" the blank-line rule just put next to its "\n<p>".
			t = strings.ReplaceAll(t, "</p><br>\n<p>", "</p>\n<p>")
			b.WriteString(t)
		case *parse.ActionNode:
			f := actionField(n)
			if linkVariables[f] {
				fmt.Fprintf(b, `<a href="{{.%s}}">{{.%s}}</a>`, f, f)
			} else {
				fmt.Fprintf(b, "{{.%s}}", f)
			}
		case *parse.IfNode:
			fmt.Fprintf(b, "{{if .%s}}", ifField(n))
			writeTextAsHTML(b, n.List)
			if n.ElseList != nil {
				b.WriteString("{{else}}")
				writeTextAsHTML(b, n.ElseList)
			}
			b.WriteString("{{end}}")
		}
	}
}

func ifField(n *parse.IfNode) string {
	if n.Pipe != nil && len(n.Pipe.Cmds) == 1 && len(n.Pipe.Cmds[0].Args) == 1 {
		if f, ok := n.Pipe.Cmds[0].Args[0].(*parse.FieldNode); ok && len(f.Ident) == 1 {
			return f.Ident[0]
		}
	}
	return ""
}

// htmlescape makes text safe as HTML source for html/template: markup characters
// as entities, and the braces too. A "{" left alone can sit right before an
// action's "{{" and make "{{{", which does not parse.
func htmlescape(s string) string {
	s = htmltemplate.EscapeString(s)
	s = strings.ReplaceAll(s, "{", "&#123;")
	return strings.ReplaceAll(s, "}", "&#125;")
}
