package notify

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/emailtemplate"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// Erik's ruling (#158): once an HTML header or footer is saved, EVERY mail has
// an HTML part. A template with no HTML of its own has its text turned into
// paragraphs (links made clickable) and wrapped in the HTML header and footer.
// With no HTML header, footer or body saved anywhere, mail stays one text part.

var brandedShell = []emailtemplate.Override{
	{Name: NameHeader, HTML: `<table><tr><td><b>{{.SiteName}}</b></td></tr><tr><td>`},
	{Name: NameFooter, HTML: `</td></tr><tr><td>Sent by {{.SiteName}}</td></tr></table>`},
}

func TestHTMLAll_EveryMailGetsAnHTMLPartOnceTheShellHasHTML(t *testing.T) {
	for _, c := range goldenCases() {
		t.Run(c.file, func(t *testing.T) {
			_, plain := parts(t, captureMessage(t, c.send, nil))
			mt, _, _, ps := readMessage(t, captureMessage(t, c.send, with(overrides(brandedShell...))))
			require.Equal(t, "multipart/alternative", mt)
			require.Len(t, ps, 2)
			require.Equal(t, plain, ps[0].body, "the text part is exactly what it was")
			require.True(t, strings.HasPrefix(ps[1].body, `<table><tr><td><b>Acme Support</b></td></tr><tr><td><p>`), ps[1].body)
			require.True(t, strings.HasSuffix(ps[1].body, `</p></td></tr><tr><td>Sent by Acme Support</td></tr></table>`), ps[1].body)
			require.Contains(t, ps[1].body, `<a href="https://help.example.com/`, "the link is clickable")
		})
	}
}

func TestHTMLAll_JustOneOfTheTwoIsEnough(t *testing.T) {
	for _, one := range brandedShell {
		t.Run(one.Name, func(t *testing.T) {
			mt, _, _, ps := readMessage(t, captureMessage(t, createdEvent, with(overrides(one))))
			require.Equal(t, "multipart/alternative", mt)
			require.Len(t, ps, 2)
		})
	}
}

func TestHTMLAll_AShellWithOnlyTextChangesNothingAboutTheParts(t *testing.T) {
	ov := overrides(
		emailtemplate.Override{Name: NameHeader, Body: "H\n"},
		emailtemplate.Override{Name: NameFooter, Body: "F\n"},
	)
	for _, c := range goldenCases() {
		t.Run(c.file, func(t *testing.T) {
			mt, _, _, ps := readMessage(t, captureMessage(t, c.send, with(ov)))
			require.Equal(t, "text/plain", mt)
			require.Len(t, ps, 1)
		})
	}
}

func TestHTMLAll_TheTextBecomesParagraphsWithAClickableLink(t *testing.T) {
	_, _, _, ps := readMessage(t, captureMessage(t, createdEvent, with(overrides(brandedShell...))))
	require.Len(t, ps, 2)
	id := "https://help.example.com/tickets/" + goldenTicketID.String()
	want := `<p>We have received your ticket.</p>
<p>Ticket: GHD-2026-000042</p>
<p>View it here:<br>
<a href="` + id + `">` + id + `</a></p>
<p>You will be notified when there is an update.</p>`
	require.Equal(t, `<table><tr><td><b>Acme Support</b></td></tr><tr><td>`+want+`</td></tr><tr><td>Sent by Acme Support</td></tr></table>`, ps[1].body)
}

func TestHTMLAll_ATemplateTextIsEscapedAndItsIfsKept(t *testing.T) {
	ov := overrides(append([]emailtemplate.Override{{
		Name: "ticket_created", Subject: "s",
		Body: "Tom & Jerry <b>bold</b> \"q\"\n\n{{if .TrackingNumber}}Ref {{.TrackingNumber}}{{else}}no ref{{end}}\n{{.TicketURL}}\n",
	}}, brandedShell...)...)
	_, _, _, ps := readMessage(t, captureMessage(t, createdEvent, with(ov)))
	require.Len(t, ps, 2)
	require.Contains(t, ps[1].body, `<p>Tom &amp; Jerry &lt;b&gt;bold&lt;/b&gt; &#34;q&#34;</p>`)
	require.Contains(t, ps[1].body, `<p>Ref GHD-2026-000042<br>`)
	require.NotContains(t, ps[1].body, "<b>bold</b>")

	noRef := goldenEvent(notification.EventTicketCreated, func(e *notification.Event) { e.TrackingNumber = "" })
	_, _, _, ps = readMessage(t, captureMessage(t, noRef, with(ov)))
	require.Len(t, ps, 2)
	require.Contains(t, ps[1].body, "no ref")
}

func TestHTMLAll_ValuesInTheConvertedTextAreEscaped(t *testing.T) {
	raw := captureMessage(t, func(d *EmailDispatcher) error {
		return sendReset(d, "to@example.com", `a"><script>alert(1)</script>`, "https://help.example.com")
	}, with(overrides(brandedShell...)))
	_, _, _, ps := readMessage(t, raw)
	require.Len(t, ps, 2)
	require.NotContains(t, ps[1].body, "<script>")
	require.Contains(t, ps[1].body, "&lt;script&gt;")
}

func TestHTMLAll_AnOwnHTMLBodyStillWinsOverTheConvertedText(t *testing.T) {
	ov := overrides(append([]emailtemplate.Override{{
		Name: "ticket_created", Subject: "s", Body: "Text {{.TicketURL}}", HTML: `<h1>Mine</h1><a href="{{.TicketURL}}">go</a>`,
	}}, brandedShell...)...)
	_, _, _, ps := readMessage(t, captureMessage(t, createdEvent, with(ov)))
	require.Len(t, ps, 2)
	require.Contains(t, ps[1].body, `<h1>Mine</h1>`)
	require.NotContains(t, ps[1].body, "We have received")
}

func TestHTMLAll_APreviewOfATemplateWithNoHTMLShowsItsTextAsParagraphs(t *testing.T) {
	d := dispatcherFor(t, "127.0.0.1:1").WithTemplates(overrides(brandedShell...), fakeSite("Acme Support"))
	d.cfg.BaseURL = "https://help.example.com"
	p, err := d.PreviewDraftHTML(context.Background(), mustDef(t, "ticket_created"), "s", "Hi {{.TicketURL}}", "", "GHD")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(p.HTML, `<table><tr><td><b>Acme Support</b>`), p.HTML)
	require.Contains(t, p.HTML, `<p>Hi <a href="https://help.example.com/tickets/00000000-0000-0000-0000-000000000000">`)
}

func TestHTMLAll_DraftingAShellWithNoHTMLWhileTheOtherHasSome(t *testing.T) {
	d := dispatcherFor(t, "127.0.0.1:1").WithTemplates(overrides(brandedShell[1]), fakeSite("x"))
	d.cfg.BaseURL = "https://help.example.com"
	p, err := d.PreviewDraftHTML(context.Background(), mustDef(t, NameHeader), "", "", "", "GHD")
	require.NoError(t, err)
	require.Contains(t, p.HTML, "Sent by x", "the footer's HTML part is enough to make the mail HTML")
}

func TestHTMLFromText_Shape(t *testing.T) {
	def := mustDef(t, "ticket_created")
	for _, tc := range []struct{ name, text, want string }{
		{"a paragraph", "Hello.\n\n{{.TicketURL}}", `<p>Hello.</p>` + "\n" + `<p><a href="{{.TicketURL}}">{{.TicketURL}}</a></p>`},
		{"a line break", "a\nb\n{{.TicketURL}}", "<p>a<br>\nb<br>\n" + `<a href="{{.TicketURL}}">{{.TicketURL}}</a></p>`},
		{"trailing newlines are not a blank paragraph", "x {{.TicketURL}}\n\n\n", `<p>x <a href="{{.TicketURL}}">{{.TicketURL}}</a></p>`},
		{"a variable that is not a link stays text", "n {{.TrackingNumber}} {{.TicketURL}}", `<p>n {{.TrackingNumber}} <a href="{{.TicketURL}}">{{.TicketURL}}</a></p>`},
		{"escaped", "a < b & c {{.TicketURL}}", `<p>a &lt; b &amp; c <a href="{{.TicketURL}}">{{.TicketURL}}</a></p>`},
		{"an if", "{{if .TrackingNumber}}t{{else}}u{{end}} {{.TicketURL}}", `<p>{{if .TrackingNumber}}t{{else}}u{{end}} <a href="{{.TicketURL}}">{{.TicketURL}}</a></p>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := htmlFromText(def, tc.text)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
			// And it is something html/template accepts.
			_, err = RenderHTMLUnchecked(def, got, SampleVars(def, "https://x.example", "GHD"))
			require.NoError(t, err)
		})
	}
}

// ── the joined size ──────────────────────────────────────────────────────────

func pad(n int) string { return strings.Repeat("x", n) }

func jrows() map[string]emailtemplate.Override {
	return map[string]emailtemplate.Override{
		NameHeader:       {Name: NameHeader, HTML: `<div>` + pad(40*1024) + `</div>`},
		NameFooter:       {Name: NameFooter, HTML: `<div>` + pad(40*1024) + `</div>`},
		"ticket_created": {Name: "ticket_created", Subject: "s", Body: "{{.TicketURL}}", HTML: `<div>` + pad(40*1024) + `</div><a href="{{.TicketURL}}">x</a>`},
	}
}

func TestJoined_EachPartPassesAloneAndTogetherTheyDoNot(t *testing.T) {
	def := mustDef(t, "ticket_created")
	for _, o := range jrows() {
		d := def
		if o.Name != "ticket_created" {
			d = mustDef(t, o.Name)
		}
		require.Empty(t, ValidateHTML(d, o.HTML), o.Name+" is fine alone")
	}
	over := OverCapHTML(jrows(), defaultSiteName)
	require.Len(t, over, 1, "only the template with the 40 KiB body goes over; the others have a short text")
	var created *OverCap
	for i := range over {
		if over[i].Name == "ticket_created" {
			created = &over[i]
		}
	}
	require.NotNil(t, created)
	require.Equal(t, "Ticket received", created.Label)
	require.Greater(t, created.Bytes, MaxRenderedHTMLBytes)
}

func TestJoined_BigShellsPutEveryTemplateOver(t *testing.T) {
	// 51 KiB each: every template gets an HTML part (its text as paragraphs)
	// and every one is over, not just the ones with HTML of their own.
	rows := map[string]emailtemplate.Override{
		NameHeader: {Name: NameHeader, HTML: `<div>` + pad(51*1024) + `</div>`},
		NameFooter: {Name: NameFooter, HTML: `<div>` + pad(51*1024) + `</div>`},
	}
	require.Len(t, OverCapHTML(rows, defaultSiteName), 17)
}

func TestJoined_ABodyAloneIsOnlyChecked_WhenNoShellHasHTML(t *testing.T) {
	rows := jrows()
	delete(rows, NameHeader)
	delete(rows, NameFooter)
	require.Empty(t, OverCapHTML(rows, defaultSiteName), "40 KiB alone is fine")
	rows["ticket_created"] = emailtemplate.Override{Name: "ticket_created", Subject: "s", Body: "{{.TicketURL}}", HTML: `<div>` + pad(60*1024) + `</div><a href="{{.TicketURL}}">x</a>`}
	require.Empty(t, OverCapHTML(rows, defaultSiteName))
}

func TestJoined_CheckJoinedHTML_ForAContentTemplateNamesIt(t *testing.T) {
	rows := jrows()
	cand := rows["ticket_created"]
	delete(rows, "ticket_created")
	te := CheckJoinedHTML(mustDef(t, "ticket_created"), rows, cand, defaultSiteName)
	require.NotNil(t, te)
	require.Equal(t, "html", te.Field)
	require.Contains(t, te.Message, `"Ticket received"`)
	require.Contains(t, te.Message, "100 KiB")
	require.Contains(t, te.Message, "header and footer")
}

func TestJoined_CheckJoinedHTML_ForAShellNamesTheTemplateItWouldBreak(t *testing.T) {
	rows := jrows()
	cand := rows[NameFooter]
	delete(rows, NameFooter)
	// With the header and the body at 40 KiB each, a 40 KiB footer is over.
	te := CheckJoinedHTML(mustDef(t, NameFooter), rows, cand, defaultSiteName)
	require.NotNil(t, te)
	require.Equal(t, "html", te.Field)
	require.Contains(t, te.Message, "would come to")

	small := emailtemplate.Override{Name: NameFooter, HTML: `<p>small</p>`}
	require.Nil(t, CheckJoinedHTML(mustDef(t, NameFooter), rows, small, defaultSiteName))
}

func TestJoined_ARevertOrASmallerSaveIsNeverRefused(t *testing.T) {
	rows := jrows()
	delete(rows, NameFooter)
	delete(rows, "ticket_created")
	// A header that is fine alone and with every default text.
	require.Nil(t, CheckJoinedHTML(mustDef(t, NameHeader), rows, emailtemplate.Override{Name: NameHeader, HTML: `<div>` + pad(40*1024) + `</div>`}, defaultSiteName))
}

func TestJoined_MeasuredWithLongLinksSoWhatSavesCanBeSent(t *testing.T) {
	// 30 copies of a link and its text: small with the sample address, past the
	// cap with a 2 KB one (a guest link with its token, behind a long BASE_URL).
	def := mustDef(t, "ticket_created")
	body := strings.Repeat(`<p><a href="{{.TicketURL}}">{{.TicketURL}}</a></p>`, 30)
	require.Empty(t, ValidateHTML(def, body), "fine with the sample address")
	te := CheckJoinedHTML(def, nil, emailtemplate.Override{Name: "ticket_created", Subject: "s", Body: "{{.TicketURL}}", HTML: body}, defaultSiteName)
	require.NotNil(t, te, "but not with a long one")
	// Fewer copies fit with room to spare.
	ok := strings.Repeat(`<p><a href="{{.TicketURL}}">{{.TicketURL}}</a></p>`, 5)
	require.Nil(t, CheckJoinedHTML(def, nil, emailtemplate.Override{Name: "ticket_created", Subject: "s", Body: "{{.TicketURL}}", HTML: ok}, defaultSiteName))
}

func TestJoined_BrokenTemplatesReportsAnOverCapCombination(t *testing.T) {
	at := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	var rows []emailtemplate.Override
	for _, o := range jrows() {
		o.UpdatedAt = at
		rows = append(rows, o)
	}
	got := BrokenTemplates(rows)
	names := map[string]BrokenTemplate{}
	for _, b := range got {
		names[b.Name] = b
	}
	b, ok := names["ticket_created"]
	require.True(t, ok, "the combination is reported against the template that would fail")
	require.Contains(t, b.Reason, "100 KiB")
	require.Equal(t, at, b.UpdatedAt)

	// And the send really does fail, as the notice says.
	d := brokenFor(t, overrides(rows...))
	err := createdEvent(d)
	require.ErrorIs(t, err, ErrPermanent)
	require.Contains(t, err.Error(), "100 KiB")
}

func TestJoined_PreviewOfAnOverCapDraftIsInvalidAndNamesTheTemplate(t *testing.T) {
	rows := jrows()
	own := rows["ticket_created"]
	delete(rows, "ticket_created")
	var list []emailtemplate.Override
	for _, o := range rows {
		list = append(list, o)
	}
	d := dispatcherFor(t, "127.0.0.1:1").WithTemplates(overrides(list...), fakeSite("x"))
	_, err := d.PreviewDraftHTML(context.Background(), mustDef(t, "ticket_created"), own.Subject, own.Body, own.HTML, "GHD")
	var inv *InvalidTemplateError
	require.ErrorAs(t, err, &inv)
	require.Equal(t, "html", inv.Errors[0].Field)
	require.Contains(t, inv.Errors[0].Message, `"Ticket received"`)
}
