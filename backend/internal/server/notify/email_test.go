package notify

import (
	"strings"
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/config"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

func TestSendRejectsHeaderInjection(t *testing.T) {
	cases := []struct {
		name      string
		to        string
		from      string
		wantErr   string
		wantError bool
	}{
		{
			name:      "CRLF in recipient",
			to:        "victim@example.com\r\nBcc: attacker@evil.com",
			from:      "noreply@example.com",
			wantErr:   "invalid recipient address",
			wantError: true,
		},
		{
			name:      "LF in recipient",
			to:        "victim@example.com\nBcc: attacker@evil.com",
			from:      "noreply@example.com",
			wantErr:   "invalid recipient address",
			wantError: true,
		},
		{
			name:      "malformed recipient",
			to:        "not-an-email",
			from:      "noreply@example.com",
			wantErr:   "invalid recipient address",
			wantError: true,
		},
		{
			name:      "CRLF in sender config",
			to:        "user@example.com",
			from:      "noreply@example.com\r\nBcc: attacker@evil.com",
			wantErr:   "invalid sender address",
			wantError: true,
		},
		{
			name:      "valid recipient and sender",
			to:        "user@example.com",
			from:      "noreply@example.com",
			wantErr:   "",
			wantError: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &EmailDispatcher{cfg: &config.Config{
				SMTPHost: "localhost",
				SMTPPort: 25,
				SMTPFrom: tc.from,
			}}
			err := d.send(tc.to, "test subject", []byte("body"))
			if !tc.wantError {
				if err != nil {
					t.Fatalf("expected no error for valid addresses, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error to contain %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

func TestSanitizeHeaderStripsControlChars(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"clean subject", "clean subject"},
		{"with\r\nCRLF", "with  CRLF"},
		{"with\nLF only", "with LF only"},
		{"with\rCR only", "with CR only"},
		{"Bcc: attacker@evil.com\r\n", "Bcc: attacker@evil.com"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := sanitizeHeader(tc.in)
			if got != tc.want {
				t.Errorf("sanitizeHeader(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestEventToEmailTicketCreatedAcceptsPascalCaseGuestEmail(t *testing.T) {
	d := &EmailDispatcher{}
	templateName, subject, to, data, ok := d.eventToEmail(notification.Event{
		Type: notification.EventTicketCreated,
		Payload: map[string]any{
			"GuestEmail":     "guest@example.com",
			"TrackingNumber": "HD-123",
			"Subject":        "Need help",
		},
	})

	if !ok {
		t.Fatalf("expected event to map to email")
	}
	if templateName != "ticket_created.tmpl" {
		t.Fatalf("unexpected template: %q", templateName)
	}
	if subject != "[HD-123] Need help" {
		t.Fatalf("unexpected subject: %q", subject)
	}
	if to != "guest@example.com" {
		t.Fatalf("unexpected recipient: %q", to)
	}
	if data == nil {
		t.Fatalf("expected template data")
	}
}

func TestEventToEmailTicketRepliedAcceptsPascalCaseReporterEmail(t *testing.T) {
	d := &EmailDispatcher{}
	templateName, subject, to, data, ok := d.eventToEmail(notification.Event{
		Type: notification.EventTicketReplied,
		Payload: map[string]any{
			"ReporterEmail":  "reporter@example.com",
			"TrackingNumber": "HD-456",
			"Subject":        "Update",
		},
	})

	if !ok {
		t.Fatalf("expected event to map to email")
	}
	if templateName != "ticket_replied.tmpl" {
		t.Fatalf("unexpected template: %q", templateName)
	}
	if subject != "Re: [HD-456] Update" {
		t.Fatalf("unexpected subject: %q", subject)
	}
	if to != "reporter@example.com" {
		t.Fatalf("unexpected recipient: %q", to)
	}
	if data == nil {
		t.Fatalf("expected template data")
	}
}

func TestEventToEmailTicketRepliedMissingReporterEmailStillMaps(t *testing.T) {
	d := &EmailDispatcher{}
	templateName, subject, to, data, ok := d.eventToEmail(notification.Event{
		Type: notification.EventTicketReplied,
		Payload: map[string]any{
			"TrackingNumber": "HD-789",
			"Subject":        "No recipient",
		},
	})

	if !ok {
		t.Fatalf("expected event to map to email even when ReporterEmail is missing")
	}
	if templateName != "ticket_replied.tmpl" {
		t.Fatalf("unexpected template: %q", templateName)
	}
	if subject != "Re: [HD-789] No recipient" {
		t.Fatalf("unexpected subject: %q", subject)
	}
	if to != "" {
		t.Fatalf("expected empty recipient when ReporterEmail is missing, got: %q", to)
	}
	if data == nil {
		t.Fatalf("expected template data")
	}
}
