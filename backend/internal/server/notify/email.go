// Package notify implements the notification.Dispatcher interface for email
// and webhook delivery.
package notify

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"net/smtp"
	"text/template"

	"github.com/open-help-desk/open-help-desk/backend/internal/config"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/notification"
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

// Dispatch sends an email for supported event types. Unsupported events are
// silently ignored — the dispatcher never returns an error to the caller.
func (d *EmailDispatcher) Dispatch(_ context.Context, event notification.Event) error {
	if !d.cfg.EmailEnabled() {
		return nil
	}

	templateName, to, data, ok := d.eventToEmail(event)
	if !ok {
		return nil
	}
	if to == "" {
		return nil
	}

	var buf bytes.Buffer
	if err := d.templates.ExecuteTemplate(&buf, templateName, data); err != nil {
		return nil // template failure is non-fatal
	}

	return d.send(to, buf.Bytes())
}

func (d *EmailDispatcher) eventToEmail(event notification.Event) (templateName, to string, data any, ok bool) {
	switch event.Type {
	case notification.EventTicketCreated:
		guestEmail, _ := event.Payload["guest_email"].(string)
		if guestEmail == "" {
			return "", "", nil, false
		}
		return "ticket_created.tmpl", guestEmail, event.Payload, true
	case notification.EventTicketReplied:
		reporterEmail, _ := event.Payload["reporter_email"].(string)
		return "ticket_replied.tmpl", reporterEmail, event.Payload, true
	}
	return "", "", nil, false
}

func (d *EmailDispatcher) send(to string, body []byte) error {
	addr := fmt.Sprintf("%s:%d", d.cfg.SMTPHost, d.cfg.SMTPPort)
	var auth smtp.Auth
	if d.cfg.SMTPUser != "" {
		auth = smtp.PlainAuth("", d.cfg.SMTPUser, d.cfg.SMTPPassword, d.cfg.SMTPHost)
	}
	msg := append([]byte(fmt.Sprintf("From: %s\r\nTo: %s\r\n", d.cfg.SMTPFrom, to)), body...)
	return smtp.SendMail(addr, auth, d.cfg.SMTPFrom, []string{to}, msg)
}
