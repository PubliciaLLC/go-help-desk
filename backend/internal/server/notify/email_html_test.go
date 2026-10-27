package notify

import (
	"context"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/emailtemplate"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// The HTML part (#158, PR 2). A template with no HTML is sent exactly as
// before (the golden tests hold that for the shipped defaults; the tests here
// hold it for edited ones). A template with HTML is multipart/alternative:
// the separately edited text part first, the HTML part second.

// mimePart is one decoded part of a captured message.
type mimePart struct {
	contentType string
	encoding    string
	body        string
}

// readMessage splits a captured message into its top-level media type and its
// decoded parts. A single-part message has one part.
func readMessage(t *testing.T, raw string) (mediaType string, params map[string]string, header mail.Header, ps []mimePart) {
	t.Helper()
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	require.NoError(t, err)
	mediaType, params, err = mime.ParseMediaType(msg.Header.Get("Content-Type"))
	require.NoError(t, err)

	decode := func(enc string, r io.Reader) string {
		require.Equal(t, "quoted-printable", enc)
		b, err := io.ReadAll(quotedprintable.NewReader(r))
		require.NoError(t, err)
		return strings.ReplaceAll(string(b), "\r\n", "\n")
	}
	if !strings.HasPrefix(mediaType, "multipart/") {
		return mediaType, params, msg.Header, []mimePart{{mediaType, msg.Header.Get("Content-Transfer-Encoding"), decode(msg.Header.Get("Content-Transfer-Encoding"), msg.Body)}}
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	for {
		p, err := mr.NextRawPart()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		enc := p.Header.Get("Content-Transfer-Encoding")
		ps = append(ps, mimePart{p.Header.Get("Content-Type"), enc, decode(enc, p)})
	}
	return mediaType, params, msg.Header, ps
}

const htmlHello = `<p>Hello</p><p><a href="{{.TicketURL}}">Open your ticket</a> {{.TrackingNumber}}</p>`

func TestHTML_TheMailIsMultipartAlternativeTextFirst(t *testing.T) {
	ov := overrides(emailtemplate.Override{
		Name: "ticket_created", Subject: "Got it", Body: "Text part: {{.TicketURL}}\n", HTML: htmlHello,
	})
	raw := captureMessage(t, createdEvent, with(ov))
	mt, params, hdr, ps := readMessage(t, raw)

	require.Equal(t, "multipart/alternative", mt)
	require.Len(t, ps, 2)
	require.Equal(t, "text/plain; charset=utf-8", ps[0].contentType, "plain text first: the last part is the preferred one")
	require.Equal(t, "text/html; charset=utf-8", ps[1].contentType)

	id := "https://help.example.com/tickets/" + goldenTicketID.String()
	require.Equal(t, "Text part: "+id+"\n", ps[0].body, "the text part is the separately edited text template")
	require.Equal(t, `<p>Hello</p><p><a href="`+id+`">Open your ticket</a> GHD-2026-000042</p>`, ps[1].body)

	// The envelope is the one PR 1 builds.
	require.Equal(t, "Got it", hdr.Get("Subject"))
	require.Equal(t, "1.0", hdr.Get("Mime-Version"))
	require.Equal(t, "<to@example.com>", hdr.Get("To"))
	require.NotEmpty(t, params["boundary"])
	require.NotContains(t, ps[0].body, params["boundary"])
	require.True(t, strings.HasSuffix(raw, "--\r\n"), "the closing delimiter ends the message: %q", raw[max(0, len(raw)-60):])
}

func TestHTML_TheBoundaryIsRandomPerMail(t *testing.T) {
	ov := overrides(emailtemplate.Override{Name: "ticket_created", Subject: "s", Body: "{{.TicketURL}}", HTML: htmlHello})
	_, a, _, _ := readMessage(t, captureMessage(t, createdEvent, with(ov)))
	_, b, _, _ := readMessage(t, captureMessage(t, createdEvent, with(ov)))
	require.NotEqual(t, a["boundary"], b["boundary"])
	require.GreaterOrEqual(t, len(a["boundary"]), 32)
}

func TestHTML_NoHTMLMeansTheSameMailAsBefore(t *testing.T) {
	// An edited template WITHOUT html is single-part text/plain, byte for byte
	// what PR 1 sent: same headers, same encoding.
	edited := emailtemplate.Override{Name: "ticket_created", Subject: "Got it", Body: "Hi {{.TicketURL}}\n"}
	raw := captureMessage(t, createdEvent, with(overrides(edited)))
	mt, _, _, ps := readMessage(t, raw)
	require.Equal(t, "text/plain", mt)
	require.Len(t, ps, 1)
	require.Contains(t, raw, "Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
	require.NotContains(t, raw, "multipart")
	require.NotContains(t, raw, "text/html")
}

func TestHTML_TheShellWrapsTheHTMLPartAndTheTextShellWrapsTheTextPart(t *testing.T) {
	ov := overrides(
		emailtemplate.Override{Name: NameHeader, Body: "== {{.SiteName}} ==\n", HTML: `<table><tr><td><b>{{.SiteName}}</b></td></tr><tr><td>`},
		emailtemplate.Override{Name: NameFooter, Body: "\n-- \nbye\n", HTML: `</td></tr><tr><td>Sent by {{.SiteName}}</td></tr></table>`},
		emailtemplate.Override{Name: "ticket_created", Subject: "s", Body: "T {{.TicketURL}}", HTML: `<p>{{.TicketURL}}</p>`},
	)
	_, _, _, ps := readMessage(t, captureMessage(t, createdEvent, with(ov)))
	require.Len(t, ps, 2)
	id := "https://help.example.com/tickets/" + goldenTicketID.String()
	require.Equal(t, "== Acme Support ==\nT "+id+"\n-- \nbye\n", ps[0].body)
	require.Equal(t, `<table><tr><td><b>Acme Support</b></td></tr><tr><td><p>`+id+`</p></td></tr><tr><td>Sent by Acme Support</td></tr></table>`, ps[1].body)
}

func TestHTML_EveryTemplateCanCarryAnHTMLPart(t *testing.T) {
	// The 17 templates each get a plain HTML override; each mail comes out as
	// two parts and the HTML part holds the link the mail exists to deliver.
	for _, c := range goldenCases() {
		t.Run(c.file, func(t *testing.T) {
			plainRaw := captureMessage(t, c.send, nil)
			_, plainParts := parts(t, plainRaw)
			var rows []emailtemplate.Override
			for _, def := range Templates() {
				if def.Shell {
					continue
				}
				link := "{{." + def.Required + "}}"
				rows = append(rows, emailtemplate.Override{Name: def.Name, Subject: def.DefaultSubject, Body: def.DefaultBody, HTML: `<p><a href="` + link + `">go</a> ` + link + `</p>`})
			}
			_, _, _, ps := readMessage(t, captureMessage(t, c.send, with(overrides(rows...))))
			require.Len(t, ps, 2)
			require.Equal(t, plainParts, ps[0].body, "the text part is the default text, untouched")
			require.Contains(t, ps[1].body, `<a href="https://help.example.com/`)
		})
	}
}

func TestHTML_ValuesAreEscapedInHTMLContext(t *testing.T) {
	// The reset link is built from a token. If a token ever held markup, the
	// text part carries it as text and the HTML part must carry it as text too.
	ov := overrides(emailtemplate.Override{
		Name: "password_reset", Subject: "Reset", Body: "{{.ResetURL}}",
		HTML: `<p>{{.ResetURL}}</p><a href="{{.ResetURL}}">reset</a>`,
	})
	raw := captureMessage(t, func(d *EmailDispatcher) error {
		return sendReset(d, "to@example.com", `a"><script>alert(1)</script>`, "https://help.example.com")
	}, with(ov))
	_, _, _, ps := readMessage(t, raw)
	require.Len(t, ps, 2)
	require.NotContains(t, ps[1].body, "<script>")
	require.Contains(t, ps[1].body, "&lt;script&gt;")
}

func TestHTML_TheGuestFragmentSurvivesInTheHref(t *testing.T) {
	ov := overrides(emailtemplate.Override{Name: "ticket_created", Subject: "s", Body: "{{.TicketURL}}", HTML: `<a href="{{.TicketURL}}">open</a>`})
	send := goldenEvent(notification.EventTicketCreated, func(e *notification.Event) { e.GuestToken = "guest-token-abc" })
	_, _, _, ps := readMessage(t, captureMessage(t, send, with(ov)))
	require.Len(t, ps, 2)
	require.Equal(t, `<a href="https://help.example.com/g#guest-token-abc">open</a>`, ps[1].body)
	require.Equal(t, "https://help.example.com/g#guest-token-abc", ps[0].body)
}

// ── a stale HTML part fails the mail, as a stale text part does ──────────────

func TestHTML_AStaleHTMLPartFailsTheMailPermanently(t *testing.T) {
	// Valid when saved, not valid with this release: pretend the validator got
	// stricter by storing something it refuses.
	ov := overrides(emailtemplate.Override{Name: "password_reset", Subject: "Reset", Body: "{{.ResetURL}}", HTML: `<script>x</script><a href="{{.ResetURL}}">go</a>`})
	err := brokenFor(t, ov).SendPasswordResetEmail(context.Background(), "to@example.com", "tok", "https://help.example.com")
	require.ErrorIs(t, err, ErrPermanent)
	require.Contains(t, err.Error(), `email template "Reset your password"`)
	require.Contains(t, err.Error(), "does not work with this release")
	require.Contains(t, err.Error(), "<script> is not allowed")
	require.NotContains(t, err.Error(), "to@example.com")
}

func TestHTML_AStaleHeaderHTMLFailsEveryMailThatHasHTML(t *testing.T) {
	ov := overrides(
		emailtemplate.Override{Name: NameHeader, HTML: `<form></form>`},
		emailtemplate.Override{Name: "ticket_created", Subject: "s", Body: "{{.TicketURL}}", HTML: `<a href="{{.TicketURL}}">x</a>`},
	)
	err := createdEvent(brokenFor(t, ov))
	require.ErrorIs(t, err, ErrPermanent)
	require.Contains(t, err.Error(), `email template "Header"`)
	require.Contains(t, err.Error(), "<form> is not allowed")
}

func TestHTML_APartThatRendersTooLongFailsTheMail(t *testing.T) {
	// Each part is under the limit on its own; together they are what a mail
	// client would clip.
	pad := strings.Repeat("x", 40*1024)
	ov := overrides(
		emailtemplate.Override{Name: NameHeader, HTML: `<div>` + pad + `</div>`},
		emailtemplate.Override{Name: NameFooter, HTML: `<div>` + pad + `</div>`},
		emailtemplate.Override{Name: "ticket_created", Subject: "s", Body: "{{.TicketURL}}", HTML: `<div>` + pad + `</div><a href="{{.TicketURL}}">x</a>`},
	)
	err := createdEvent(brokenFor(t, ov))
	require.ErrorIs(t, err, ErrPermanent)
	require.Contains(t, err.Error(), "100 KiB")
}

func TestHTML_BrokenTemplatesIncludesAStaleHTMLPart(t *testing.T) {
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	got := BrokenTemplates([]emailtemplate.Override{
		{Name: "ticket_created", Subject: "ok", Body: "{{.TicketURL}}", HTML: `<a href="{{.TicketURL}}">x</a>`, UpdatedAt: at},
		{Name: "password_reset", Subject: "ok", Body: "{{.ResetURL}}", HTML: `<p>no link</p>`, UpdatedAt: at},
		{Name: NameFooter, HTML: `<script></script>`, UpdatedAt: at},
	})
	require.Len(t, got, 2)
	require.Equal(t, "password_reset", got[0].Name)
	require.Contains(t, got[0].Reason, "html")
	require.Contains(t, got[0].Reason, "ResetURL")
	require.Equal(t, NameFooter, got[1].Name)
}

// ── preview and test send ────────────────────────────────────────────────────

func TestPreviewDraftHTML_RendersTheHTMLInsideTheSavedShell(t *testing.T) {
	d := dispatcherFor(t, "127.0.0.1:1").WithTemplates(overrides(
		emailtemplate.Override{Name: NameHeader, HTML: `<div class="h">{{.SiteName}}</div>`},
		emailtemplate.Override{Name: NameFooter, HTML: `<div class="f">bye</div>`},
	), fakeSite("Acme Support"))
	d.cfg.BaseURL = "https://help.example.com"

	p, err := d.PreviewDraftHTML(context.Background(), mustDef(t, "ticket_created"), "s", "Text {{.TicketURL}}", `<p>{{.TicketURL}}</p>`, "GHD")
	require.NoError(t, err)
	require.Equal(t, `<div class="h">Acme Support</div><p>https://help.example.com/tickets/00000000-0000-0000-0000-000000000000</p><div class="f">bye</div>`, p.HTML)
	require.Equal(t, "Text https://help.example.com/tickets/00000000-0000-0000-0000-000000000000", p.Body)
}

func TestPreviewDraftHTML_NoHTMLAnywhereHasNoHTMLPreview(t *testing.T) {
	d := dispatcherFor(t, "127.0.0.1:1").WithTemplates(overrides(), fakeSite("x"))
	p, err := d.PreviewDraftHTML(context.Background(), mustDef(t, "ticket_created"), "s", "Text {{.TicketURL}}", "", "GHD")
	require.NoError(t, err)
	require.Empty(t, p.HTML)
}

func TestPreviewDraftHTML_ADraftShellIsShownAroundASampleMail(t *testing.T) {
	d := dispatcherFor(t, "127.0.0.1:1").WithTemplates(overrides(
		emailtemplate.Override{Name: NameFooter, HTML: `<p>footer</p>`},
	), fakeSite("Acme Support"))
	d.cfg.BaseURL = "https://help.example.com"
	p, err := d.PreviewDraftHTML(context.Background(), mustDef(t, NameHeader), "", "", `<h1>{{.SiteName}}</h1>`, "GHD")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(p.HTML, `<h1>Acme Support</h1>`), p.HTML)
	require.True(t, strings.HasSuffix(p.HTML, `<p>footer</p>`), p.HTML)
	require.Greater(t, len(p.HTML), len(`<h1>Acme Support</h1><p>footer</p>`), "around a sample message")
}

func TestPreviewDraftHTML_RefusesInvalidHTMLNamingTheField(t *testing.T) {
	d := dispatcherFor(t, "127.0.0.1:1").WithTemplates(overrides(), fakeSite("x"))
	_, err := d.PreviewDraftHTML(context.Background(), mustDef(t, "ticket_created"), "s", "{{.TicketURL}}", `<script>x</script>{{.TicketURL}}`, "GHD")
	var inv *InvalidTemplateError
	require.ErrorAs(t, err, &inv)
	require.Equal(t, "html", inv.Errors[0].Field)
}

func TestPreviewDraft_StaysTextOnly(t *testing.T) {
	// The PR 1 entry point is unchanged: no HTML in, none out, unless a header
	// or footer has some (then every mail has an HTML part).
	d := dispatcherFor(t, "127.0.0.1:1").WithTemplates(overrides(), fakeSite("x"))
	p, err := d.PreviewDraft(context.Background(), mustDef(t, "ticket_created"), "s", "{{.TicketURL}}", "GHD")
	require.NoError(t, err)
	require.Empty(t, p.HTML)
}

func TestSendTest_WithHTMLCarriesBothParts(t *testing.T) {
	addr, received := captureSMTP(t)
	d := dispatcherFor(t, addr).WithTemplates(overrides(), fakeSite("x"))
	require.NoError(t, d.SendTest(context.Background(), "admin@example.com", Preview{Subject: "Hello", Body: "plain", HTML: "<p>rich</p>"}))
	raw := <-received
	mt, _, hdr, ps := readMessage(t, raw)
	require.Equal(t, "multipart/alternative", mt)
	require.Equal(t, "[Test] Hello", hdr.Get("Subject"))
	require.Len(t, ps, 2)
	require.Equal(t, "plain", ps[0].body)
	require.Equal(t, "<p>rich</p>", ps[1].body)
	require.Contains(t, raw, "To: <admin@example.com>")
}
