package notify

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

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

// The three tests that used to live here pinned the payload-key handling:
// guest_email vs GuestEmail, and a subject built as "[tracking] <ticket
// subject>". None of that exists any more — eventToEmail reads the recipient
// and the tracking number off the event's own fields and never touches
// Payload. What replaces them is in email_injection_test.go, which asserts the
// stronger property: nothing from Payload reaches the wire at all.

func TestEventToEmail_UsesTheEventFieldsNotThePayload(t *testing.T) {
	d := &EmailDispatcher{cfg: &config.Config{BaseURL: "https://help.example.com"}}
	id := uuid.New()

	for _, tc := range []struct {
		name     string
		evType   notification.EventType
		template string
		subject  string
	}{
		{"created", notification.EventTicketCreated, "ticket_created.tmpl", "We have received [GHD-2026-000001]"},
		{"replied", notification.EventTicketReplied, "ticket_replied.tmpl", "There is a new reply on [GHD-2026-000001]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			templateName, subject, to, data, ok := d.eventToEmail(notification.Event{
				Type:           tc.evType,
				TicketID:       id,
				TrackingNumber: "GHD-2026-000001",
				Recipient:      "reporter@example.com",
				// Every one of these is request text. None may be used.
				Payload: map[string]any{
					"guest_email":    "attacker@evil.test",
					"reporter_email": "attacker@evil.test",
					"TrackingNumber": "FORGED-0000-000000",
					"Subject":        "Need help",
					"ReplyBody":      "some reply",
				},
			})

			require.True(t, ok)
			require.Equal(t, tc.template, templateName)
			require.Equal(t, tc.subject, subject)
			require.Equal(t, "reporter@example.com", to, "the address must come from Recipient")

			view, isMap := data.(map[string]string)
			require.True(t, isMap, "template data must be the view built here, not the payload")
			require.Equal(t, map[string]string{
				"TrackingNumber": "GHD-2026-000001",
				"TicketURL":      "https://help.example.com/tickets/" + id.String(),
			}, view)
		})
	}
}

// No recipient, no mail. The address is not in the payload any more, so an
// event that arrives without one has nowhere to go.
func TestEventToEmail_WithoutARecipientSendsNothing(t *testing.T) {
	d := &EmailDispatcher{cfg: &config.Config{BaseURL: "https://help.example.com"}}
	_, _, _, _, ok := d.eventToEmail(notification.Event{
		Type:           notification.EventTicketReplied,
		TrackingNumber: "GHD-2026-000001",
		Payload:        map[string]any{"reporter_email": "attacker@evil.test"},
	})
	require.False(t, ok, "the payload address must not be used as a fallback")
}

// A missing tracking number costs the subject its reference, not the mail.
func TestEventToEmail_WithoutATrackingNumberStillSends(t *testing.T) {
	d := &EmailDispatcher{cfg: &config.Config{BaseURL: "https://help.example.com"}}
	_, subject, to, _, ok := d.eventToEmail(notification.Event{
		Type:      notification.EventTicketReplied,
		Recipient: "reporter@example.com",
	})
	require.True(t, ok)
	require.Equal(t, "There is a new reply on your ticket", subject)
	require.Equal(t, "reporter@example.com", to)
}
