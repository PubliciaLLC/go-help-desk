package notify

import (
	"strings"
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/config"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// TestSendRejectsHeaderInjection covers validateSendAddresses directly
// (rather than the full send(), which reaches out over the network to an
// actual SMTP server) — it's the address-parsing/header-injection defense
// that's under test here, not mail delivery, so this needs no SMTP listener
// to run and passes the same in CI as on a dev machine.
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
			toAddr, fromAddr, err := validateSendAddresses(tc.to, tc.from)
			if !tc.wantError {
				if err != nil {
					t.Fatalf("expected no error for valid addresses, got %v", err)
				}
				if toAddr == nil || fromAddr == nil {
					t.Fatal("expected parsed addresses to be returned alongside a nil error")
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

// TestSend_DialFailureIsNotMisreportedAsInvalidAddress confirms send() with
// genuinely valid addresses fails on the network dial (there's no SMTP
// server here), not on address validation — i.e. that validateSendAddresses
// really is wired into send() and not bypassed.
func TestSend_DialFailureIsNotMisreportedAsInvalidAddress(t *testing.T) {
	d := &EmailDispatcher{cfg: &config.Config{
		SMTPHost: "localhost",
		SMTPPort: 1, // nothing listens on port 1; dial fails fast
		SMTPFrom: "noreply@example.com",
	}}
	err := d.send("user@example.com", "test subject", []byte("body"))
	if err == nil {
		t.Fatal("expected a dial error with nothing listening on the configured port")
	}
	if strings.Contains(err.Error(), "invalid recipient address") || strings.Contains(err.Error(), "invalid sender address") {
		t.Fatalf("valid addresses should never fail validation, got %q", err.Error())
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
