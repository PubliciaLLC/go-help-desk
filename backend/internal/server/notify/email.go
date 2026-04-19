// Package notify implements the notification.Dispatcher interface for email
// and webhook delivery.
package notify

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"mime/quotedprintable"
	"net/mail"
	"net/smtp"
	"strings"
	"text/template"

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
func sanitizePayload(payload map[string]any) map[string]any {
	if payload == nil {
		return nil
	}
	out := make(map[string]any, len(payload))
	for k, v := range payload {
		if s, ok := v.(string); ok {
			out[k] = sanitizeHeader(s)
			continue
		}
		out[k] = v
	}
	return out
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

// send builds a MIME message with sanitized headers and quoted-printable-encoded
// body, then hands it to smtp.SendMail. All user-controlled input passes through
// validation (mail.ParseAddress) or encoding (quoted-printable / sanitizeHeader)
// before reaching the SMTP sink.
func (d *EmailDispatcher) send(to, subject string, body []byte) error {
	toAddr, err := mail.ParseAddress(to)
	if err != nil {
		return fmt.Errorf("invalid recipient address: %w", err)
	}
	fromAddr, err := mail.ParseAddress(d.cfg.SMTPFrom)
	if err != nil {
		return fmt.Errorf("invalid sender address: %w", err)
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
	return smtp.SendMail(addr, auth, fromAddr.Address, []string{toAddr.Address}, msg.Bytes())
}
