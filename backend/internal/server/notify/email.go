// Package notify implements the notification.Dispatcher interface for email
// and webhook delivery.
package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"embed"
	"fmt"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"text/template"
	"time"

	"github.com/publiciallc/go-help-desk/backend/internal/config"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

// EmailDispatcher sends email notifications on ticket events.
// It is a no-op when SMTP is not configured.
type EmailDispatcher struct {
	cfg       *config.Config
	templates *template.Template
}

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

// sanitizePayload returns a shallow copy of payload where all string values are
// header/body-safe normalized text (CR/LF removed, trimmed).
// sanitizePayload cleans values for use in the message BODY.
//
// It used to apply sanitizeHeader to every string, which flattened every
// multi-line reply onto one line in the email — the header rule applied to
// content that is not a header. The header path does not need it here anyway:
// send() sanitizes the Subject at the point it writes it, which is the only
// place a payload string becomes a header.
//
// What it strips instead is the set of characters that have no business in a
// plain-text body and exist mainly to make text display as something other
// than what it is.
func sanitizePayload(payload map[string]any) map[string]any {
	if payload == nil {
		return nil
	}
	out := make(map[string]any, len(payload))
	for k, v := range payload {
		if s, ok := v.(string); ok {
			out[k] = sanitizeBody(s)
			continue
		}
		out[k] = v
	}
	return out
}

// sanitizeBody makes a string safe to place in a text/plain body while keeping
// it readable.
//
// The message is text/plain, so markup in it is not markup — that is what
// actually prevents script execution, not this. This removes what remains:
//
//   - Control characters, which no plain-text body needs and which a
//     non-conforming renderer may act on. Newline and tab are kept, because a
//     reply legitimately contains both.
//   - Bidirectional overrides (U+202A-U+202E, U+2066-U+2069). These reorder
//     displayed text without changing it, so "cancel-order.txt" can be shown
//     as something else entirely.
//   - U+2028 and U+2029, which are line terminators to a JavaScript parser and
//     would matter the moment any of this is rendered somewhere other than a
//     mail client.
//
// Line endings are normalised to \n so the quoted-printable encoder produces
// consistent CRLF rather than inheriting whatever the client sent.
func sanitizeBody(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f: // C0 controls and DEL
			continue
		case r >= 0x80 && r <= 0x9f: // C1 controls
			continue
		case r >= 0x202A && r <= 0x202E: // bidi embedding and override
			continue
		case r >= 0x2066 && r <= 0x2069: // bidi isolates
			continue
		case r == 0x2028 || r == 0x2029: // line and paragraph separators
			continue
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// Dispatch sends an email for supported event types. Unsupported events are
// silently ignored — the dispatcher never returns an error to the caller.
func (d *EmailDispatcher) Dispatch(_ context.Context, event notification.Event) error {
	if !d.cfg.EmailEnabled() {
		return nil
	}

	templateName, subject, to, data, ok := d.eventToEmail(event)
	if !ok || to == "" {
		return nil
	}

	var buf bytes.Buffer
	if err := d.templates.ExecuteTemplate(&buf, templateName, data); err != nil {
		return nil // template failure is non-fatal
	}

	return d.send(to, subject, buf.Bytes())
}

func (d *EmailDispatcher) eventToEmail(event notification.Event) (templateName, subject, to string, data any, ok bool) {
	payload := sanitizePayload(event.Payload)
	switch event.Type {
	case notification.EventTicketCreated:
		guestEmail, _ := payload["guest_email"].(string)
		if guestEmail == "" {
			guestEmail, _ = payload["GuestEmail"].(string)
		}
		tracking, _ := payload["TrackingNumber"].(string)
		subj, _ := payload["Subject"].(string)
		if guestEmail == "" {
			return "", "", "", nil, false
		}
		return "ticket_created.tmpl",
			fmt.Sprintf("[%s] %s", tracking, subj),
			guestEmail, payload, true
	case notification.EventTicketReplied:
		reporterEmail, _ := payload["reporter_email"].(string)
		if reporterEmail == "" {
			reporterEmail, _ = payload["ReporterEmail"].(string)
		}
		tracking, _ := payload["TrackingNumber"].(string)
		subj, _ := payload["Subject"].(string)
		return "ticket_replied.tmpl",
			fmt.Sprintf("Re: [%s] %s", tracking, subj),
			reporterEmail, payload, true
	}
	return "", "", "", nil, false
}

// SendVerificationEmail sends a transactional email containing the account
// verification link. It implements registration.Mailer.
func (d *EmailDispatcher) SendVerificationEmail(to, token, baseURL string) error {
	if !d.cfg.EmailEnabled() {
		return nil
	}
	verifyURL := baseURL + "/verify-email?token=" + token
	var buf bytes.Buffer
	if err := d.templates.ExecuteTemplate(&buf, "email_verification.tmpl", map[string]string{
		"VerifyURL": verifyURL,
	}); err != nil {
		return fmt.Errorf("rendering verification email: %w", err)
	}
	return d.send(to, "Verify your email address", buf.Bytes())
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
	toAddr, fromAddr, err := validateSendAddresses(to, d.cfg.SMTPFrom)
	if err != nil {
		return err
	}

	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\n", fromAddr.String())
	fmt.Fprintf(&msg, "To: %s\r\n", toAddr.String())
	fmt.Fprintf(&msg, "Subject: %s\r\n", sanitizeHeader(subject))
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	msg.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	msg.WriteString("\r\n")

	qp := quotedprintable.NewWriter(&msg)
	if _, err := qp.Write(body); err != nil {
		return fmt.Errorf("encoding body: %w", err)
	}
	if err := qp.Close(); err != nil {
		return fmt.Errorf("closing encoder: %w", err)
	}

	addr := fmt.Sprintf("%s:%d", d.cfg.SMTPHost, d.cfg.SMTPPort)
	var auth smtp.Auth
	if d.cfg.SMTPUser != "" {
		auth = smtp.PlainAuth("", d.cfg.SMTPUser, d.cfg.SMTPPassword, d.cfg.SMTPHost)
	}
	return sendMailWithTimeout(addr, auth, fromAddr.Address, toAddr.Address, msg.Bytes(), smtpTimeout)
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

// sendMailWithTimeout is net/smtp.SendMail with a dial timeout and a deadline
// covering the whole conversation.
func sendMailWithTimeout(addr string, auth smtp.Auth, from, to string, msg []byte, timeout time.Duration) error {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return fmt.Errorf("dialing SMTP server: %w", err)
	}
	// One deadline for the whole exchange, so a relay that answers the dial and
	// then stalls mid-conversation cannot hold the goroutine either.
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		_ = conn.Close()
		return fmt.Errorf("setting SMTP deadline: %w", err)
	}

	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("parsing SMTP address: %w", err)
	}

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("SMTP handshake: %w", err)
	}
	defer func() { _ = c.Close() }()

	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("STARTTLS: %w", err)
		}
	}
	if auth != nil {
		if ok, _ := c.Extension("AUTH"); ok {
			if err := c.Auth(auth); err != nil {
				return fmt.Errorf("SMTP auth: %w", err)
			}
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
