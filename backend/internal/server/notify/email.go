// Package notify implements the notification.Dispatcher interface for email
// and webhook delivery.
package notify

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"io"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/netip"
	"net/smtp"
	"strings"
	"text/template"
	"time"

	"github.com/publiciallc/go-help-desk/backend/internal/config"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/emailtemplate"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

// EmailDispatcher sends email notifications on ticket events.
// It is a no-op when SMTP is not configured.
type EmailDispatcher struct {
	cfg *config.Config
	// templates holds the compiled defaults, by file name. An admin's edited
	// version, when there is one, is read at send time instead (#158).
	templates *template.Template
	overrides emailtemplate.Reader
	site      SiteNamer
}

// SiteNamer supplies the help desk's name for the shared header and footer.
// It is the one value in those that is not fixed text.
type SiteNamer interface {
	SiteName(ctx context.Context) string
}

// WithTemplates lets the admin's edited templates, header and footer take
// effect. Without it every mail is the compiled default.
func (d *EmailDispatcher) WithTemplates(r emailtemplate.Reader, site SiteNamer) *EmailDispatcher {
	d.overrides, d.site = r, site
	return d
}

// ErrEmailNotConfigured is returned by SendTest when SMTP is not set up.
var ErrEmailNotConfigured = errors.New("email is not configured")

// NewEmailDispatcher loads templates and returns an EmailDispatcher.
// Returns a no-op dispatcher when SMTP is not configured.
func NewEmailDispatcher(cfg *config.Config) (*EmailDispatcher, error) {
	if !cfg.EmailEnabled() {
		return &EmailDispatcher{cfg: cfg}, nil
	}
	tmpl, err := template.ParseFS(templateFS, "templates/*.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parsing email templates: %w", err)
	}
	return &EmailDispatcher{cfg: cfg, templates: tmpl}, nil
}

// sanitizeHeader strips CR and LF so user-controlled values interpolated into
// email headers cannot inject additional headers.
func sanitizeHeader(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

// ErrPermanent marks a delivery error that retrying cannot fix, such as an
// email template that does not render. The outbox worker fails the row at once
// on it instead of retrying (#456).
var ErrPermanent = errors.New("permanent delivery error")

// Dispatch sends an email for supported event types. Unsupported events are
// silently ignored. A template that fails to render, or a failed send, is
// returned: the outbox worker retries the second and fails the first at once
// (ErrPermanent), and either shows up in the admin email-health warning.
func (d *EmailDispatcher) Dispatch(ctx context.Context, event notification.Event) error {
	if !d.cfg.EmailEnabled() {
		return nil
	}

	templateName, subject, to, data, ok := d.eventToEmail(event)
	if !ok || to == "" {
		return nil
	}

	view, _ := data.(map[string]string)
	subject, body, htmlBody, err := d.compose(ctx, strings.TrimSuffix(templateName, ".tmpl"), subject, view)
	if err != nil {
		return err
	}
	return d.sendMessage(to, subject, body, htmlBody)
}

// compose builds the subject, the plain-text part and, when the template has
// one, the HTML part of one mail: the template's edited version if the admin
// saved one, else the compiled default, wrapped in the shared header and footer
// (empty unless the admin wrote them).
//
// The HTML part exists only when the template itself has saved HTML. The header
// and footer wrap it; they do not make a mail multipart on their own, so a
// template with no HTML is sent as plain text exactly as before, whatever the
// shell holds. The text part is never generated from the HTML: it is the
// separately edited text template.
//
// An edited version that no longer validates FAILS the mail, permanently,
// naming the template; it does not fall back to the default. The admin chose
// that knowing a reset or guest link could be missed (#158), and in return the
// failure is never quiet: it reaches the email-health warning, the startup log
// and the broken-template notice, and reverting is one click.
func (d *EmailDispatcher) compose(ctx context.Context, name, defaultSubject string, view map[string]string) (subject string, text, htmlPart []byte, err error) {
	def, ok := LookupTemplate(name)
	if !ok {
		return "", nil, nil, fmt.Errorf("no email template %q: %w", name, ErrPermanent)
	}
	ovs, err := d.storedOverrides(ctx, name, NameHeader, NameFooter)
	if err != nil {
		return "", nil, nil, err
	}

	subject, content := defaultSubject, ""
	ov, edited := ovs[name]
	if edited {
		subject, content, err = RenderTemplate(def, ov.Subject, ov.Body, view)
		if err != nil {
			return "", nil, nil, &staleTemplateError{label: def.Label, err: err}
		}
	} else {
		var buf bytes.Buffer
		if err := d.templates.ExecuteTemplate(&buf, name+".tmpl", view); err != nil {
			// Used to be swallowed ("non-fatal"), which made the worker delete the
			// row as delivered: a broken template lost every mail silently.
			return "", nil, nil, fmt.Errorf("rendering %s.tmpl: %w: %w", name, ErrPermanent, err)
		}
		content = buf.String()
	}

	header, err := d.renderShell(ctx, ovs, NameHeader)
	if err != nil {
		return "", nil, nil, err
	}
	footer, err := d.renderShell(ctx, ovs, NameFooter)
	if err != nil {
		return "", nil, nil, err
	}
	text = []byte(header + content + footer)

	if edited && ov.HTML != "" {
		htmlPart, err = d.composeHTML(ctx, def, ov.HTML, view, ovs)
		if err != nil {
			return "", nil, nil, err
		}
	}
	return subject, text, htmlPart, nil
}

// composeHTML renders the HTML part: the template's HTML between the saved
// header's and footer's. The length limit is on the whole, which is what a mail
// client clips.
func (d *EmailDispatcher) composeHTML(ctx context.Context, def TemplateDef, src string, view map[string]string, ovs map[string]emailtemplate.Override) ([]byte, error) {
	content, err := RenderHTML(def, src, view)
	if err != nil {
		return nil, &staleTemplateError{label: def.Label, err: err}
	}
	header, err := d.renderShellHTML(ctx, ovs, NameHeader)
	if err != nil {
		return nil, err
	}
	footer, err := d.renderShellHTML(ctx, ovs, NameFooter)
	if err != nil {
		return nil, err
	}
	return joinHTML(def, header, content, footer)
}

// joinHTML puts the three pieces together and holds the whole to the cap.
func joinHTML(def TemplateDef, header, content, footer string) ([]byte, error) {
	if n := len(header) + len(content) + len(footer); n > MaxRenderedHTMLBytes {
		return nil, fmt.Errorf("email template %q: the HTML part comes to %d bytes with the header and footer, and mail clients cut off what is over 100 KiB; shorten it: %w",
			def.Label, n, ErrPermanent)
	}
	return []byte(header + content + footer), nil
}

// storedOverrides reads the rows for these names in one lookup. A failed read
// is returned as it is, so the worker retries: the database having a bad
// moment is not the template's fault, and sending the default instead would
// quietly drop the admin's wording.
func (d *EmailDispatcher) storedOverrides(ctx context.Context, names ...string) (map[string]emailtemplate.Override, error) {
	if d.overrides == nil {
		return nil, nil
	}
	ovs, err := d.overrides.Overrides(ctx, names...)
	if err != nil {
		return nil, fmt.Errorf("reading edited email templates: %w", err)
	}
	return ovs, nil
}

func (d *EmailDispatcher) siteName(ctx context.Context) string {
	if d.site == nil {
		return "Go Help Desk"
	}
	return d.site.SiteName(ctx)
}

// renderShell renders the saved header or footer, or "" when there is none.
func (d *EmailDispatcher) renderShell(ctx context.Context, ovs map[string]emailtemplate.Override, name string) (string, error) {
	ov, ok := ovs[name]
	if !ok {
		return "", nil
	}
	def, _ := LookupTemplate(name)
	_, text, err := RenderTemplate(def, ov.Subject, ov.Body, map[string]string{"SiteName": d.siteName(ctx)})
	if err != nil {
		return "", &staleTemplateError{label: def.Label, err: err}
	}
	return text, nil
}

// renderShellHTML renders the saved header's or footer's HTML, or "" when it
// has none.
func (d *EmailDispatcher) renderShellHTML(ctx context.Context, ovs map[string]emailtemplate.Override, name string) (string, error) {
	ov, ok := ovs[name]
	if !ok || ov.HTML == "" {
		return "", nil
	}
	def, _ := LookupTemplate(name)
	out, err := RenderHTML(def, ov.HTML, map[string]string{"SiteName": d.siteName(ctx)})
	if err != nil {
		return "", &staleTemplateError{label: def.Label, err: err}
	}
	return out, nil
}

// staleTemplateError is an edited template that does not validate any more.
// Its text is what the outbox keeps as the failure reason and the admin reads,
// so it names the template and the problem and holds no address.
type staleTemplateError struct {
	label string
	err   error
}

func (e *staleTemplateError) Error() string {
	return fmt.Sprintf("email template %q: your edited version does not work with this release (%s); revert it under Admin, Email templates",
		e.label, e.err)
}

func (e *staleTemplateError) Unwrap() error { return e.err }

// Is makes it permanent: the text cannot change by itself, so the worker fails
// the row at once instead of retrying.
func (e *staleTemplateError) Is(target error) bool { return target == ErrPermanent }

// Preview is a draft rendered with sample data, as it would be mailed. HTML is
// empty when the draft has no HTML part.
type Preview struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
	HTML    string `json:"html"`
}

// PreviewDraft is PreviewDraftHTML for a draft with no HTML part.
func (d *EmailDispatcher) PreviewDraft(ctx context.Context, def TemplateDef, subject, body, trackingPrefix string) (Preview, error) {
	return d.PreviewDraftHTML(ctx, def, subject, body, "", trackingPrefix)
}

// PreviewDraftHTML renders an unsaved draft of def with sample data, inside the
// saved header and footer. Drafting the header or footer itself shows it
// around a sample ticket mail. Nothing is sent or stored. An invalid draft is
// an *InvalidTemplateError holding every problem, text and HTML together, so
// the editor can mark each field.
func (d *EmailDispatcher) PreviewDraftHTML(ctx context.Context, def TemplateDef, subject, body, htmlSrc, trackingPrefix string) (Preview, error) {
	if errs := append(ValidateTemplate(def, subject, body), ValidateHTML(def, htmlSrc)...); len(errs) > 0 {
		return Preview{}, &InvalidTemplateError{Errors: errs}
	}
	vars := SampleVars(def, d.cfg.BaseURL, trackingPrefix)
	if _, has := vars["SiteName"]; has {
		vars["SiteName"] = d.siteName(ctx) // the real name, not the sample's
	}
	renderedSubject, renderedBody, err := RenderTemplate(def, subject, body, vars)
	if err != nil {
		return Preview{}, err
	}
	renderedHTML, err := RenderHTML(def, htmlSrc, vars)
	if err != nil {
		return Preview{}, err
	}
	ovs, err := d.storedOverrides(ctx, NameHeader, NameFooter)
	if err != nil {
		return Preview{}, err
	}
	header, err := d.renderShell(ctx, ovs, NameHeader)
	if err != nil {
		return Preview{}, err
	}
	footer, err := d.renderShell(ctx, ovs, NameFooter)
	if err != nil {
		return Preview{}, err
	}
	headerHTML, err := d.renderShellHTML(ctx, ovs, NameHeader)
	if err != nil {
		return Preview{}, err
	}
	footerHTML, err := d.renderShellHTML(ctx, ovs, NameFooter)
	if err != nil {
		return Preview{}, err
	}
	switch def.Name {
	case NameHeader:
		header, headerHTML = renderedBody, renderedHTML
	case NameFooter:
		footer, footerHTML = renderedBody, renderedHTML
	}
	if def.Shell {
		// Around a sample mail, so the admin sees it in place.
		sample, _ := LookupTemplate("ticket_created")
		renderedSubject, renderedBody, err = RenderTemplate(sample, sample.DefaultSubject, sample.DefaultBody,
			SampleVars(sample, d.cfg.BaseURL, trackingPrefix))
		if err != nil {
			return Preview{}, err
		}
		if renderedHTML != "" {
			renderedHTML = textToHTML(renderedBody)
		}
	}
	p := Preview{Subject: renderedSubject, Body: header + renderedBody + footer}
	if renderedHTML != "" {
		joined, err := joinHTML(def, headerHTML, renderedHTML, footerHTML)
		if err != nil {
			return Preview{}, err
		}
		p.HTML = string(joined)
	}
	return p, nil
}

// textToHTML is a plain-text message as paragraphs, for previewing a header or
// footer around something. Only the preview uses it; no mail does.
func textToHTML(text string) string {
	var b strings.Builder
	for _, para := range strings.Split(strings.TrimSpace(text), "\n\n") {
		b.WriteString("<p>")
		b.WriteString(strings.ReplaceAll(htmltemplate.HTMLEscapeString(para), "\n", "<br>"))
		b.WriteString("</p>")
	}
	return b.String()
}

// SendTest mails a preview to one address with "[Test] " in front of the
// subject, so it cannot be mistaken for a real notification. The caller picks
// the address; the request that triggers this carries none.
func (d *EmailDispatcher) SendTest(_ context.Context, to string, p Preview) error {
	if !d.cfg.EmailEnabled() {
		return ErrEmailNotConfigured
	}
	var htmlBody []byte
	if p.HTML != "" {
		htmlBody = []byte(p.HTML)
	}
	return d.sendMessage(to, "[Test] "+p.Subject, []byte(p.Body), htmlBody)
}

// eventToEmail decides what to send. It deliberately sends no content.
//
// Ticket emails used to carry the ticket subject and the reply body. That makes
// the message a carrier for text somebody else chose, sent from a domain the
// recipient trusts — content spoofing, CWE-640. It was not theoretical here:
// filing a ticket with a chosen subject was enough to have this server mail
// that text out over its own reputation.
//
// So the email now says only that something happened, names the ticket by its
// tracking number, and links to it. The tracking number and the address both
// come off the persisted ticket (Event.TrackingNumber, Event.Recipient), never
// out of Event.Payload, which carries request text. The content stays in the
// application behind the login that already guards it.
func (d *EmailDispatcher) eventToEmail(event notification.Event) (templateName, subject, to string, data any, ok bool) {
	if event.Recipient == "" {
		return "", "", "", nil, false
	}

	// Every part of the link is trusted: a token this server minted, or the
	// ticket's own identifier, on the configured base URL.
	//
	// A guest has no account, so the token is how they reach their own ticket.
	// This does not reopen what 1.2.0 closed — that removed CONTENT chosen by
	// whoever filed the ticket, its subject line and reply bodies, from mail
	// sent over the operator's domain. A link this server generated is not
	// that, and the message still carries nothing anyone else wrote.
	// A guest link carries the token in the URL FRAGMENT, not the path.
	//
	// A fragment is never sent to a server. Not to this one, not to a proxy in
	// front of it, not in a Referer header. The path form was logged verbatim
	// at INFO by requestLogger on the browser's first request for the SPA
	// shell — the application's own access log was the leak the frontend
	// comment claimed to be defending against — and no amount of redaction
	// makes it safe in every proxy, CDN and aggregator in front of us.
	//
	// The SPA reads location.hash and clears it. The server never learns it
	// except as an Authorization header on the API calls that need it.
	path := "/tickets/" + event.TicketID.String()
	// Staff sign in; a guest credential has no place in their mail, whatever
	// the event carries.
	isStaffMail := event.Type == notification.EventStaffAssigned || event.Type == notification.EventStaffRequesterReplied ||
		event.Type == notification.EventStaffReopened
	if event.GuestToken != "" && !isStaffMail {
		path = "/g#" + event.GuestToken
	}
	view := map[string]string{
		"TrackingNumber": event.TrackingNumber,
		"TicketURL":      strings.TrimRight(d.cfg.BaseURL, "/") + path,
	}

	templateName, ok = emailTemplateFor(event)
	if !ok {
		return "", "", "", nil, false
	}
	// The default subject is the registry's text, so the one the editor shows
	// as "the default" is the one a never-edited instance sends. A missing
	// tracking number costs the subject its reference, not the customer the
	// notification.
	def, found := LookupTemplate(strings.TrimSuffix(templateName, ".tmpl"))
	if !found {
		return "", "", "", nil, false
	}
	subject, err := execute(def, def.DefaultSubject, view)
	if err != nil {
		return "", "", "", nil, false
	}
	return templateName, subject, event.Recipient, view, true
}

// emailTemplateFor picks the template for an event.
//
// ticket_status_changed, ticket_resolved and ticket_reopened send only to a
// guest — Recipient is empty for a ticket with a reporter account, so
// eventToEmail returns before here. An account holder signs in and sees a
// status change; mailing them every transition would be a stream of email
// nobody asked for, carrying a link they do not need. They exist because
// rotation has to deliver its replacement: each invalidates the link already
// in the guest's inbox, so each has to carry the new one or the guest is
// locked out.
//
// Staff mail (#441) follows the same no-content rule: the tracking number and
// a link to the ticket behind the sign-in, nothing a requester wrote.
// Somebody mailed because of their group is told it is their group's ticket,
// and a follower that it is one they follow: "assigned to you" would be false
// for them.
func emailTemplateFor(event notification.Event) (string, bool) {
	staff := func(base string) (string, bool) {
		switch event.StaffReason {
		case notification.SwitchGroup:
			return base + "_group.tmpl", true
		case notification.SwitchFollow:
			return base + "_follow.tmpl", true
		}
		return base + ".tmpl", true
	}
	switch event.Type {
	case notification.EventTicketCreated:
		return "ticket_created.tmpl", true
	case notification.EventTicketReplied:
		return "ticket_replied.tmpl", true
	case notification.EventTicketStatusChanged:
		return "ticket_status_changed.tmpl", true
	case notification.EventTicketResolved:
		return "ticket_resolved.tmpl", true
	case notification.EventTicketReopened:
		return "ticket_reopened.tmpl", true
	case notification.EventStaffAssigned:
		return staff("staff_assigned")
	case notification.EventStaffRequesterReplied:
		return staff("staff_requester_replied")
	case notification.EventStaffReopened:
		return staff("staff_reopened")
	case notification.EventGuestLinkResent:
		return "guest_link.tmpl", true
	}
	return "", false
}

// SendVerificationEmail sends a transactional email containing the account
// verification link. It implements registration.Mailer.
func (d *EmailDispatcher) SendVerificationEmail(ctx context.Context, to, token, baseURL string) error {
	if !d.cfg.EmailEnabled() {
		return nil
	}
	return d.sendAccountMail(ctx, to, "email_verification", map[string]string{
		"VerifyURL": baseURL + "/verify-email?token=" + token,
	})
}

// SendPasswordResetEmail sends the link a "forgot password" request is
// answered with (#451). It implements passwordreset.Mailer. The link's host
// comes from baseURL, which the caller takes from configuration, never from a
// request.
func (d *EmailDispatcher) SendPasswordResetEmail(ctx context.Context, to, token, baseURL string) error {
	if !d.cfg.EmailEnabled() {
		return nil
	}
	return d.sendAccountMail(ctx, to, "password_reset", map[string]string{
		"ResetURL": baseURL + "/reset-password?token=" + token,
	})
}

func (d *EmailDispatcher) sendAccountMail(ctx context.Context, to, name string, view map[string]string) error {
	def, _ := LookupTemplate(name)
	defaultSubject, err := execute(def, def.DefaultSubject, view)
	if err != nil {
		return fmt.Errorf("rendering %s subject: %w: %w", name, ErrPermanent, err)
	}
	subject, body, htmlBody, err := d.compose(ctx, name, defaultSubject, view)
	if err != nil {
		return err
	}
	return d.sendMessage(to, subject, body, htmlBody)
}

// send builds a MIME message with sanitized headers and quoted-printable-encoded
// body, then hands it to smtp.SendMail. All user-controlled input passes through
// validation (mail.ParseAddress) or encoding (quoted-printable / sanitizeHeader)
// before reaching the SMTP sink.
// validateSendAddresses parses and validates the recipient and sender
// addresses, rejecting header-injection attempts (a CRLF/LF-bearing address
// would otherwise let an attacker smuggle extra SMTP headers, e.g. a forged
// Bcc, into the message). Pulled out of send() so this pure validation logic
// is testable without an actual SMTP server to send through.
func validateSendAddresses(to, from string) (toAddr, fromAddr *mail.Address, err error) {
	toAddr, err = mail.ParseAddress(to)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid recipient address: %w", err)
	}
	fromAddr, err = mail.ParseAddress(from)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid sender address: %w", err)
	}
	return toAddr, fromAddr, nil
}

func (d *EmailDispatcher) send(to, subject string, body []byte) error {
	return d.sendMessage(to, subject, body, nil)
}

// sendMessage is send with an optional HTML part. With none the message is one
// text/plain part, byte for byte what it was before the HTML part existed; with
// one it is multipart/alternative, text first (the last part is the one a
// client prefers).
func (d *EmailDispatcher) sendMessage(to, subject string, body, htmlBody []byte) error {
	toAddr, fromAddr, err := validateSendAddresses(to, d.cfg.SMTPFrom)
	if err != nil {
		return err
	}

	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\n", fromAddr.String())
	// toAddr.Address, not toAddr.String(): ParseAddress accepts a display name
	// and String() puts it back, so `"text" <victim@example.com>` would deliver
	// whatever text the sender chose in a header of a message sent from this
	// server's domain. The bare address is all a recipient needs.
	fmt.Fprintf(&msg, "To: <%s>\r\n", toAddr.Address)
	fmt.Fprintf(&msg, "Subject: %s\r\n", sanitizeHeader(subject))
	msg.WriteString("MIME-Version: 1.0\r\n")

	if len(htmlBody) == 0 {
		msg.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
		msg.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
		msg.WriteString("\r\n")
		if err := writeQP(&msg, body); err != nil {
			return err
		}
	} else {
		boundary, err := newBoundary()
		if err != nil {
			return err
		}
		fmt.Fprintf(&msg, "Content-Type: multipart/alternative; boundary=%s\r\n\r\n", boundary)
		for _, part := range []struct {
			mediaType string
			body      []byte
		}{{"text/plain", body}, {"text/html", htmlBody}} {
			fmt.Fprintf(&msg, "--%s\r\nContent-Type: %s; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", boundary, part.mediaType)
			if err := writeQP(&msg, part.body); err != nil {
				return err
			}
			// The line break before a delimiter belongs to the delimiter.
			msg.WriteString("\r\n")
		}
		fmt.Fprintf(&msg, "--%s--\r\n", boundary)
	}

	opts := smtpOptionsFor(d.cfg)
	opts.Timeout = smtpTimeout
	return sendMail(opts, fromAddr.Address, toAddr.Address, msg.Bytes())
}

func writeQP(w io.Writer, body []byte) error {
	qp := quotedprintable.NewWriter(w)
	if _, err := qp.Write(body); err != nil {
		return fmt.Errorf("encoding body: %w", err)
	}
	if err := qp.Close(); err != nil {
		return fmt.Errorf("closing encoder: %w", err)
	}
	return nil
}

// newBoundary is random per message. A quoted-printable body cannot contain a
// line that starts with "--" and this many random characters, so no part can
// end the message early; it is also what stops a guessable one being aimed at.
func newBoundary() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("creating a MIME boundary: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// smtpTimeout bounds a single delivery end to end.
//
// net/smtp.SendMail dials with no timeout and sets no deadline, and delivery
// happens on the request goroutine after the ticket has already committed. A
// relay that accepts the connection and then stops responding therefore parked
// the handler indefinitely: the client eventually saw the write timeout drop
// the connection with no response, retried, and filed a duplicate ticket, while
// every goroutine that touched email piled up until the relay recovered.
const smtpTimeout = 20 * time.Second

// smtpOptions is everything one delivery needs to know about the server.
type smtpOptions struct {
	Addr     string // host:port
	User     string
	Password string
	// Implicit means TLS from the first byte (port 465), not STARTTLS.
	Implicit bool
	Timeout  time.Duration

	// Test hooks. Production leaves both nil: RootCAs nil means the system
	// roots, and verification is never switched off, only extended.
	RootCAs *x509.CertPool
	dial    func(network, addr string, timeout time.Duration) (net.Conn, error)
}

// usesImplicitTLS reports whether a port is the implicit-TLS submission port.
// Chosen by port rather than a setting: 465 speaks TLS from the first byte, so
// a STARTTLS client waited on a banner that never came and hung until the
// timeout. No working setup changes.
func usesImplicitTLS(port int) bool { return port == 465 }

func smtpOptionsFor(cfg *config.Config) smtpOptions {
	return smtpOptions{
		Addr:     fmt.Sprintf("%s:%d", cfg.SMTPHost, cfg.SMTPPort),
		User:     cfg.SMTPUser,
		Password: cfg.SMTPPassword,
		Implicit: usesImplicitTLS(cfg.SMTPPort),
	}
}

// isPrivateIP reports loopback, RFC 1918 (10/8, 172.16/12, 192.168/16) and
// IPv6 unique local addresses (fc00::/7), and nothing else. Link-local and
// carrier-grade NAT ranges are deliberately not private here.
func isPrivateIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	return addr.IsLoopback() || addr.IsPrivate()
}

// peerIP is the address the connection actually reached, not the one the
// configured name resolved to some time earlier: the rule is about where the
// bytes go.
func peerIP(conn net.Conn) net.IP {
	if a, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		return a.IP
	}
	host, _, err := net.SplitHostPort(conn.RemoteAddr().String())
	if err != nil {
		return nil
	}
	return net.ParseIP(host)
}

// The TLS rule, the whole of it:
//
//   - A password is never sent unencrypted, to any address, private or not.
//     Implicit TLS on 465, STARTTLS everywhere else; if STARTTLS is not
//     offered the send fails. A man in the middle stripping STARTTLS from the
//     greeting therefore costs a failed send, not a password.
//   - With no password, plain SMTP is allowed only to a private address
//     (loopback, RFC 1918, fc00::/7): an internal relay. A public address
//     still requires TLS. STARTTLS is used whenever it is offered.
//
// sendMail is net/smtp.SendMail with those rules, a dial timeout, and a
// deadline covering the whole conversation.
func sendMail(o smtpOptions, from, to string, msg []byte) error {
	dial := o.dial
	if dial == nil {
		dial = net.DialTimeout
	}
	conn, err := dial("tcp", o.Addr, o.Timeout)
	if err != nil {
		return fmt.Errorf("dialing SMTP server: %w", err)
	}
	// One deadline for the whole exchange, so a relay that answers the dial and
	// then stalls mid-conversation cannot hold the goroutine either.
	if err := conn.SetDeadline(time.Now().Add(o.Timeout)); err != nil {
		_ = conn.Close()
		return fmt.Errorf("setting SMTP deadline: %w", err)
	}

	host, _, err := net.SplitHostPort(o.Addr)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("parsing SMTP address: %w", err)
	}
	tlsCfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12, RootCAs: o.RootCAs}

	if o.Implicit {
		tc := tls.Client(conn, tlsCfg)
		if err := tc.Handshake(); err != nil {
			_ = conn.Close()
			return fmt.Errorf("SMTP implicit TLS: %w", err)
		}
		conn = tc
	}

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("SMTP handshake: %w", err)
	}
	defer func() { _ = c.Close() }()

	if !o.Implicit {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsCfg); err != nil {
				return fmt.Errorf("STARTTLS: %w", err)
			}
		} else if o.Password != "" {
			return errors.New("SMTP server does not offer STARTTLS, and SMTP_PASSWORD is set: " +
				"refusing to send the password unencrypted")
		} else if !isPrivateIP(peerIP(conn)) {
			return errors.New("SMTP server does not offer STARTTLS and is not on a private address: " +
				"refusing to send mail unencrypted")
		}
	}

	// After STARTTLS: AUTH is often advertised only on the encrypted session.
	// Sending anonymously when credentials are configured would hide a broken
	// relay behind apparent success.
	if o.User != "" {
		if ok, _ := c.Extension("AUTH"); !ok {
			return errors.New("SMTP server does not offer AUTH but SMTP_USER is set")
		}
		if err := c.Auth(smtp.PlainAuth("", o.User, o.Password, host)); err != nil {
			return fmt.Errorf("SMTP auth: %w", err)
		}
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("SMTP MAIL FROM: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("SMTP RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("writing message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("closing message: %w", err)
	}
	return c.Quit()
}
