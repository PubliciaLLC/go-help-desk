package notify

import (
	"context"
	"strings"
	"testing"

	htpl "html/template"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/emailtemplate"
)

// Round 3 of #158 PR 2.

var htmlHeader = emailtemplate.Override{Name: NameHeader, HTML: `<div>{{.SiteName}}</div>`}

// ── N1: a "{" in the text must not become part of an action ──────────────────

var bracedTexts = []string{
	"Ref { {{- .TrackingNumber}} {{.TicketURL}}",
	"{ {{if .TrackingNumber}}x{{else}}y{{end}}\n{{.TicketURL}}",
	"x { {{- if .TrackingNumber}}y{{end}}\n{{.TicketURL}}",
	"{{if .TrackingNumber}}a{ {{- else}}b{{end}}\n{{.TicketURL}}",
	"{{if .TrackingNumber}}a{ {{- end}}\n{{.TicketURL}}",
	"{{.TicketURL}} }} and {{ not an action",
	"}{{.TicketURL}}{",
}

func TestHTMLFromText_BracesInTheTextAreLiteral(t *testing.T) {
	def := mustDef(t, "ticket_created")
	for _, text := range bracedTexts {
		t.Run(text, func(t *testing.T) {
			if errs := ValidateTemplate(def, "s", text); len(errs) > 0 {
				t.Skipf("not a valid template: %v", errs[0])
			}
			src, err := htmlFromText(def, text)
			require.NoError(t, err)
			tp, err := htpl.New("x").Option("missingkey=error").Parse(src)
			require.NoError(t, err, "the generated source must parse: %q", src)
			var b strings.Builder
			require.NoError(t, tp.Execute(&b, SampleVars(def, "https://help.example.com", "GHD")))
			require.Nil(t, checkRenderedHTML(b.String()))
			require.NotContains(t, src, "{{{")
		})
	}
}

func TestHTMLAll_ATextWithABraceBeforeAnActionStillSends(t *testing.T) {
	ov := overrides(emailtemplate.Override{Name: "ticket_created", Subject: "s", Body: "Ref { {{- .TrackingNumber}} {{.TicketURL}}"}, htmlHeader)
	mt, _, _, ps := readMessage(t, captureMessage(t, createdEvent, with(ov)))
	require.Equal(t, "multipart/alternative", mt)
	require.Len(t, ps, 2)
	require.Contains(t, ps[1].body, "Ref &#123;GHD-2026-000042")
	_, plain := parts(t, captureMessage(t, createdEvent, with(ov)))
	require.Contains(t, plain, "Ref {GHD-2026-000042")
}

func TestJoined_ATextThatCannotBecomeHTMLIsARefusalNotZeroBytes(t *testing.T) {
	// A stored text that does not even parse (an upgrade tightened a rule) is a
	// template the HTML part cannot be made from. It must stop a header save,
	// not count as 0 bytes.
	rows := map[string]emailtemplate.Override{
		"ticket_created": {Name: "ticket_created", Subject: "s", Body: "{{range .TicketURL}}x{{end}}"},
	}
	te := CheckJoinedHTML(mustDef(t, NameHeader), rows, htmlHeader, defaultSiteName)
	require.NotNil(t, te)
	require.Equal(t, "html", te.Field)
	require.Contains(t, te.Message, `"Ticket received"`)
	require.Contains(t, te.Message, "cannot be made")

	over := OverCapHTML(map[string]emailtemplate.Override{NameHeader: htmlHeader, "ticket_created": rows["ticket_created"]}, defaultSiteName)
	require.Len(t, over, 1)
	require.NotEmpty(t, over[0].Problem)

	b := BrokenTemplates([]emailtemplate.Override{htmlHeader, rows["ticket_created"]})
	var found bool
	for _, x := range b {
		found = found || x.Name == "ticket_created"
	}
	require.True(t, found)
}

func FuzzHTMLFromText(f *testing.F) {
	for _, s := range bracedTexts {
		f.Add(s)
	}
	for _, s := range []string{"{{.TicketURL}}", "a\n\nb {{.TicketURL}}\n", "{{if .TrackingNumber}}\n\n{{end}}{{.TicketURL}}", "<b>&</b>{{.TicketURL}}", "{{- .TicketURL -}}"} {
		f.Add(s)
	}
	def, _ := LookupTemplate("ticket_created")
	vars := SampleVars(def, "https://help.example.com", "GHD")
	f.Fuzz(func(t *testing.T, text string) {
		if len(ValidateTemplate(def, "s", text)) > 0 {
			return
		}
		src, err := htmlFromText(def, text)
		if err != nil {
			t.Fatalf("a valid text did not convert: %v\ntext: %q", err, text)
		}
		out, err := RenderHTMLUnchecked(def, src, vars)
		if err != nil {
			t.Fatalf("generated source did not render: %v\ntext: %q\nsource: %q", err, text, src)
		}
		if te := checkRenderedHTML(out); te != nil {
			t.Fatalf("generated HTML fails the final check: %s\ntext: %q\noutput: %q", te.Message, text, out)
		}
	})
}

// ── a text-only header or footer is carried into the HTML part ───────────────

func TestHTMLAll_ATextOnlyFooterIsNotLostWhenTheHeaderHasHTML(t *testing.T) {
	ov := overrides(
		emailtemplate.Override{Name: NameHeader, HTML: `<div class="h">{{.SiteName}}</div>`},
		emailtemplate.Override{Name: NameFooter, Body: "\n-- \nThis is an automated message & more.\n"},
	)
	_, plain := parts(t, captureMessage(t, createdEvent, nil))
	_, _, _, ps := readMessage(t, captureMessage(t, createdEvent, with(ov)))
	require.Len(t, ps, 2)
	require.Equal(t, plain+"\n-- \nThis is an automated message & more.\n", ps[0].body)
	require.True(t, strings.HasPrefix(ps[1].body, `<div class="h">Acme Support</div><p>We have received`), ps[1].body)
	require.True(t, strings.HasSuffix(ps[1].body, `<p>-- <br>`+"\n"+`This is an automated message &amp; more.</p>`), ps[1].body)
}

func TestHTMLAll_ATextOnlyHeaderIsNotLostWhenTheFooterHasHTML(t *testing.T) {
	ov := overrides(
		emailtemplate.Override{Name: NameHeader, Body: "== {{.SiteName}} ==\n\n"},
		emailtemplate.Override{Name: NameFooter, HTML: `<hr>bye`},
	)
	_, _, _, ps := readMessage(t, captureMessage(t, createdEvent, with(ov)))
	require.Len(t, ps, 2)
	require.True(t, strings.HasPrefix(ps[1].body, `<p>== Acme Support ==</p>`), ps[1].body)
	require.True(t, strings.HasSuffix(ps[1].body, `<hr>bye`), ps[1].body)
}

func TestHTMLAll_TextOnlyShellsAloneStillMakeNoHTMLPart(t *testing.T) {
	ov := overrides(emailtemplate.Override{Name: NameHeader, Body: "H\n"}, emailtemplate.Override{Name: NameFooter, Body: "F\n"})
	mt, _, _, _ := readMessage(t, captureMessage(t, createdEvent, with(ov)))
	require.Equal(t, "text/plain", mt)
}

func TestHTMLAll_ATextShellTurnsUpInThePreviewToo(t *testing.T) {
	d := dispatcherFor(t, "127.0.0.1:1").WithTemplates(overrides(
		emailtemplate.Override{Name: NameHeader, HTML: `<div>head</div>`},
		emailtemplate.Override{Name: NameFooter, Body: "Sent by {{.SiteName}}\n"},
	), fakeSite("Acme Support"))
	d.cfg.BaseURL = "https://help.example.com"
	p, err := d.PreviewDraftHTML(context.Background(), mustDef(t, "ticket_created"), "s", "Hi {{.TicketURL}}", "", "GHD")
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(p.HTML, `<p>Sent by Acme Support</p>`), p.HTML)

	// Drafting the text-only footer itself.
	p, err = d.PreviewDraftHTML(context.Background(), mustDef(t, NameFooter), "", "Draft footer {{.SiteName}}\n", "", "GHD")
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(p.HTML, `<p>Draft footer Acme Support</p>`), p.HTML)
}

// ── the real site name ───────────────────────────────────────────────────────

func siteHeavyHeader() emailtemplate.Override {
	return emailtemplate.Override{Name: NameHeader, HTML: strings.Repeat(`<b>{{.SiteName}}</b>`, 120)}
}

func TestJoined_TheRealSiteNameIsWhatIsMeasured(t *testing.T) {
	rows := map[string]emailtemplate.Override{NameHeader: siteHeavyHeader()}
	require.Empty(t, OverCapHTML(rows, "Go Help Desk"))
	long := strings.Repeat("N", 1500) // 120 copies of this is far past 100 KiB
	require.NotEmpty(t, OverCapHTML(rows, long))

	// And the notice appears the moment the name is long, with no template touched.
	var list []emailtemplate.Override
	for _, r := range rows {
		list = append(list, r)
	}
	require.Empty(t, BrokenTemplatesFor(list, "Go Help Desk"))
	require.NotEmpty(t, BrokenTemplatesFor(list, long))
}

func TestJoined_ASmallSiteNameIsNotMeasuredAsAHugeOne(t *testing.T) {
	// Before, every site name was measured at 1,024 bytes.
	rows := map[string]emailtemplate.Override{NameHeader: {Name: NameHeader, HTML: strings.Repeat(`<b>{{.SiteName}}</b>`, 80)}}
	require.Empty(t, OverCapHTML(rows, "Acme"))
}

func TestPreview_UsesTheRealSiteNameForTheSizeCheck(t *testing.T) {
	long := strings.Repeat("N", 1500)
	d := dispatcherFor(t, "127.0.0.1:1").WithTemplates(overrides(), fakeSite(long))
	_, err := d.PreviewDraftHTML(context.Background(), mustDef(t, NameHeader), "", "", siteHeavyHeader().HTML, "GHD")
	var inv *InvalidTemplateError
	require.ErrorAs(t, err, &inv)
}
