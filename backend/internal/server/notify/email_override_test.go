package notify

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"mime/quotedprintable"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/config"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/emailtemplate"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

type fakeOverrides struct {
	rows  map[string]emailtemplate.Override
	err   error
	asked [][]string
}

func (f *fakeOverrides) Overrides(_ context.Context, names ...string) (map[string]emailtemplate.Override, error) {
	f.asked = append(f.asked, names)
	if f.err != nil {
		return nil, f.err
	}
	out := map[string]emailtemplate.Override{}
	for _, n := range names {
		if r, ok := f.rows[n]; ok {
			out[n] = r
		}
	}
	return out, nil
}

func overrides(rows ...emailtemplate.Override) *fakeOverrides {
	f := &fakeOverrides{rows: map[string]emailtemplate.Override{}}
	for _, r := range rows {
		f.rows[r.Name] = r
	}
	return f
}

type fakeSite string

func (f fakeSite) SiteName(context.Context) string { return string(f) }

func with(f *fakeOverrides) func(*EmailDispatcher) {
	return func(d *EmailDispatcher) { d.WithTemplates(f, fakeSite("Acme Support")) }
}

// parts splits a captured message into its Subject header and decoded body.
func parts(t *testing.T, raw string) (subject, body string) {
	t.Helper()
	head, enc, ok := strings.Cut(raw, "\r\n\r\n")
	require.True(t, ok, "no header/body boundary")
	for _, l := range strings.Split(head, "\r\n") {
		if v, ok := strings.CutPrefix(l, "Subject: "); ok {
			subject = v
		}
	}
	b, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(enc)))
	require.NoError(t, err)
	// The wire carries CRLF; a template is written with LF.
	return subject, strings.ReplaceAll(string(b), "\r\n", "\n")
}

func createdEvent(d *EmailDispatcher) error {
	return goldenEvent(notification.EventTicketCreated, nil)(d)
}

// ── an override is used ──────────────────────────────────────────────────────

func TestOverride_IsUsedAtSend(t *testing.T) {
	ov := overrides(emailtemplate.Override{
		Name:    "ticket_created",
		Subject: "Got it {{.TrackingNumber}}",
		Body:    "Hi.\nSee {{.TicketURL}}\nRef {{.TrackingNumber}}\n",
	})
	subj, body := parts(t, captureMessage(t, createdEvent, with(ov)))
	require.Equal(t, "Got it GHD-2026-000042", subj)
	require.Equal(t, "Hi.\nSee https://help.example.com/tickets/"+goldenTicketID.String()+"\nRef GHD-2026-000042\n", body)
}

// An override for one template leaves the others as shipped.
func TestOverride_LeavesOtherTemplatesAlone(t *testing.T) {
	ov := overrides(emailtemplate.Override{Name: "ticket_created", Subject: "s", Body: "{{.TicketURL}}"})
	got := captureMessage(t, goldenEvent(notification.EventTicketReplied, nil), with(ov))
	want := captureMessage(t, goldenEvent(notification.EventTicketReplied, nil), nil)
	require.Equal(t, want, got)
}

// One lookup per mail covers the template and the shell.
func TestOverride_OneLookupPerMail(t *testing.T) {
	ov := overrides()
	captureMessage(t, createdEvent, with(ov))
	require.Len(t, ov.asked, 1)
	require.ElementsMatch(t, []string{"ticket_created", "_header", "_footer"}, ov.asked[0])
}

// An override that says a template's subject is a ticket subject cannot get
// one: the data was never given to it. (The validator refuses it at save; this
// is the send-side proof that the value is not there to find.)
func TestOverride_TheDataHasNoTicketContent(t *testing.T) {
	d := &EmailDispatcher{cfg: testBase()}
	ev := goldenEventValue(notification.EventTicketReplied)
	ev.Payload = map[string]any{"subject": "SECRET-SUBJECT", "body": "SECRET-BODY"}
	_, _, _, data, ok := d.eventToEmail(ev)
	require.True(t, ok)
	for k, v := range data.(map[string]string) {
		require.NotContains(t, v, "SECRET", k)
	}
}

// ── the shared header and footer ─────────────────────────────────────────────

func TestShell_EmptyRowsAreIdenticalMail(t *testing.T) {
	ov := overrides(
		emailtemplate.Override{Name: NameHeader},
		emailtemplate.Override{Name: NameFooter},
	)
	for _, c := range goldenCases() {
		t.Run(c.file, func(t *testing.T) {
			want := captureMessage(t, c.send, nil)
			require.Equal(t, want, captureMessage(t, c.send, with(ov)))
		})
	}
}

func TestShell_WrapsEveryTemplate(t *testing.T) {
	ov := overrides(
		emailtemplate.Override{Name: NameHeader, Body: "== {{.SiteName}} ==\n"},
		emailtemplate.Override{Name: NameFooter, Body: "\n-- \nThis is an automated message.\n"},
	)
	for _, c := range goldenCases() {
		t.Run(c.file, func(t *testing.T) {
			plainSubj, plain := parts(t, captureMessage(t, c.send, nil))
			subj, body := parts(t, captureMessage(t, c.send, with(ov)))
			require.Equal(t, plainSubj, subj, "the header and footer do not touch the subject")
			require.Equal(t, "== Acme Support ==\n"+plain+"\n-- \nThis is an automated message.\n", body)
		})
	}
}

// The mail that carries the link still carries it when wrapped.
func TestShell_TheLinkSurvivesWrapping(t *testing.T) {
	ov := overrides(emailtemplate.Override{Name: NameHeader, Body: "H\n"})
	_, body := parts(t, captureMessage(t, func(d *EmailDispatcher) error {
		return sendReset(d, "to@example.com", "tok", "https://help.example.com")
	}, with(ov)))
	require.True(t, strings.HasPrefix(body, "H\n"))
	require.Contains(t, body, "https://help.example.com/reset-password?token=tok")
}

// ── a stale override fails the mail ──────────────────────────────────────────

func TestStale_ContentOverrideFailsPermanentlyAndNamesTheTemplate(t *testing.T) {
	// Valid when saved, not valid now: the required link is gone.
	ov := overrides(emailtemplate.Override{Name: "password_reset", Subject: "Reset", Body: "no link in here"})
	d := brokenFor(t, ov)
	err := d.SendPasswordResetEmail(context.Background(), "to@example.com", "tok", "https://help.example.com")
	require.ErrorIs(t, err, ErrPermanent)
	require.Contains(t, err.Error(), `email template "Reset your password"`)
	require.Contains(t, err.Error(), "does not work with this release")
	require.Contains(t, err.Error(), "ResetURL")
	require.Contains(t, err.Error(), "revert")
	require.NotContains(t, err.Error(), "to@example.com", "the failure text is shown to the admin and holds no address")
}

func TestStale_ADeadVariableFailsTheMail(t *testing.T) {
	ov := overrides(emailtemplate.Override{Name: "ticket_created", Subject: "s", Body: "{{.TicketURL}} {{.Gone}}"})
	err := createdEvent(brokenFor(t, ov))
	require.ErrorIs(t, err, ErrPermanent)
	require.Contains(t, err.Error(), "Gone")
}

func TestStale_ABrokenHeaderFailsEveryMail(t *testing.T) {
	ov := overrides(emailtemplate.Override{Name: NameHeader, Body: "{{.TicketURL}}"})
	for _, c := range goldenCases() {
		t.Run(c.file, func(t *testing.T) {
			err := c.send(brokenFor(t, ov))
			require.ErrorIs(t, err, ErrPermanent)
			require.Contains(t, err.Error(), `email template "Header"`)
		})
	}
}

func TestStale_ABrokenFooterNamesTheFooter(t *testing.T) {
	ov := overrides(emailtemplate.Override{Name: NameFooter, Body: "{{range .SiteName}}x{{end}}"})
	err := createdEvent(brokenFor(t, ov))
	require.ErrorIs(t, err, ErrPermanent)
	require.Contains(t, err.Error(), `email template "Footer"`)
}

// brokenFor is a dispatcher whose send would fail on the network if it got
// that far: a stale template must fail before it.
func brokenFor(t *testing.T, ov *fakeOverrides) *EmailDispatcher {
	t.Helper()
	d := dispatcherFor(t, "127.0.0.1:1")
	d.cfg.BaseURL = "https://help.example.com"
	return d.WithTemplates(ov, fakeSite("Acme Support"))
}

// A database error reading the override is not the template's fault: it is
// returned as it is and retried, never turned into the default mail.
func TestOverrideReadError_IsRetriedNotPermanent(t *testing.T) {
	ov := &fakeOverrides{err: errors.New("connection reset")}
	err := createdEvent(brokenFor(t, ov))
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrPermanent)
	require.Contains(t, err.Error(), "connection reset")
}

// ── preview and test send ────────────────────────────────────────────────────

func TestPreviewDraft_RendersSampleDataInsideTheSavedShell(t *testing.T) {
	d := dispatcherFor(t, "127.0.0.1:1").WithTemplates(overrides(
		emailtemplate.Override{Name: NameHeader, Body: "[{{.SiteName}}]\n"},
	), fakeSite("Acme Support"))
	d.cfg.BaseURL = "https://help.example.com"
	def := mustDef(t, "ticket_created")

	p, err := d.PreviewDraft(context.Background(), def, "Draft {{.TrackingNumber}}", "Look: {{.TicketURL}}", "GHD")
	require.NoError(t, err)
	require.Regexp(t, `^Draft GHD-\d{4}-000042$`, p.Subject)
	require.Equal(t, "[Acme Support]\nLook: https://help.example.com/tickets/00000000-0000-0000-0000-000000000000", p.Body)
}

func TestPreviewDraft_ADraftHeaderIsShownAroundASampleMail(t *testing.T) {
	d := dispatcherFor(t, "127.0.0.1:1").WithTemplates(overrides(
		emailtemplate.Override{Name: NameFooter, Body: "-- footer"},
	), fakeSite("Acme Support"))
	d.cfg.BaseURL = "https://help.example.com"

	p, err := d.PreviewDraft(context.Background(), mustDef(t, NameHeader), "", "Hello from {{.SiteName}}\n", "GHD")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(p.Body, "Hello from Acme Support\n"))
	require.True(t, strings.HasSuffix(p.Body, "-- footer"), "%q", p.Body)
	require.Contains(t, p.Body, "We have received your ticket.")
}

func TestPreviewDraft_RefusesAnInvalidDraft(t *testing.T) {
	d := dispatcherFor(t, "127.0.0.1:1").WithTemplates(overrides(), fakeSite("x"))
	_, err := d.PreviewDraft(context.Background(), mustDef(t, "ticket_created"), "s", "no link", "GHD")
	var inv *InvalidTemplateError
	require.ErrorAs(t, err, &inv)
}

func TestSendTest_PrependsTestToTheSubjectAndSendsToTheGivenAddress(t *testing.T) {
	addr, received := captureSMTP(t)
	d := dispatcherFor(t, addr).WithTemplates(overrides(), fakeSite("x"))
	d.cfg.BaseURL = "https://help.example.com"

	require.NoError(t, d.SendTest(context.Background(), "admin@example.com", Preview{Subject: "Hello", Body: "body"}))
	raw := <-received
	subj, body := parts(t, raw)
	require.Equal(t, "[Test] Hello", subj)
	require.Equal(t, "body\n", body)
	require.Contains(t, raw, "To: <admin@example.com>")
}

func goldenEventValue(typ notification.EventType) notification.Event {
	return notification.Event{Type: typ, TicketID: uuid.New(), TrackingNumber: "GHD-1", Recipient: "to@example.com"}
}

func testBase() *config.Config { return &config.Config{BaseURL: "https://help.example.com"} }

// ── which stored rows no longer work ─────────────────────────────────────────

func TestBrokenTemplates_ListsOnlyTheRowsThatFailValidation(t *testing.T) {
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	got := BrokenTemplates([]emailtemplate.Override{
		{Name: "ticket_created", Subject: "ok", Body: "{{.TicketURL}}", UpdatedAt: at},
		{Name: "password_reset", Subject: "ok", Body: "no link", UpdatedAt: at},
		{Name: NameFooter, Body: "{{.TicketURL}}", UpdatedAt: at},
		{Name: NameHeader, Body: "fine {{.SiteName}}", UpdatedAt: at},
		{Name: "retired_template", Subject: "x", Body: "x", UpdatedAt: at},
	})
	require.Len(t, got, 2)
	require.Equal(t, "password_reset", got[0].Name)
	require.Equal(t, "Reset your password", got[0].Label)
	require.Contains(t, got[0].Reason, "ResetURL")
	require.Equal(t, at, got[0].UpdatedAt)
	require.Equal(t, NameFooter, got[1].Name)
	require.Equal(t, "Footer", got[1].Label)
}

func TestBrokenTemplates_NoneIsNotNil(t *testing.T) {
	require.NotNil(t, BrokenTemplates(nil), "the JSON must be [], not null")
}

// ── the startup warning ──────────────────────────────────────────────────────

type lister struct {
	rows []emailtemplate.Override
	err  error
}

func (l lister) List(context.Context) ([]emailtemplate.Override, error) { return l.rows, l.err }

func TestWarnBrokenTemplates_LogsEachOneAndNeverBlocks(t *testing.T) {
	var buf strings.Builder
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	n := WarnBrokenTemplates(context.Background(), lister{rows: []emailtemplate.Override{
		{Name: "ticket_created", Subject: "ok", Body: "{{.TicketURL}}"},
		{Name: "password_reset", Subject: "ok", Body: "no link"},
		{Name: NameHeader, Body: "{{.TicketURL}}"},
	}}, log)
	require.Equal(t, 2, n)
	out := buf.String()
	require.Equal(t, 2, strings.Count(out, "level=WARN"))
	require.Contains(t, out, `template=password_reset`)
	require.Contains(t, out, `template=_header`)
	require.NotContains(t, out, "template=ticket_created")
	require.Contains(t, out, "revert")
}

func TestWarnBrokenTemplates_AReadFailureIsLoggedNotFatal(t *testing.T) {
	var buf strings.Builder
	log := slog.New(slog.NewTextHandler(&buf, nil))
	require.Zero(t, WarnBrokenTemplates(context.Background(), lister{err: errors.New("db down")}, log))
	require.Contains(t, buf.String(), "db down")
}
