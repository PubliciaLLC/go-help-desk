package notify

import (
	"strings"
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/config"
)

func TestSendRejectsHeaderInjection(t *testing.T) {
	cases := []struct {
		name    string
		to      string
		from    string
		wantErr string
	}{
		{
			name:    "CRLF in recipient",
			to:      "victim@example.com\r\nBcc: attacker@evil.com",
			from:    "noreply@example.com",
			wantErr: "invalid recipient address",
		},
		{
			name:    "LF in recipient",
			to:      "victim@example.com\nBcc: attacker@evil.com",
			from:    "noreply@example.com",
			wantErr: "invalid recipient address",
		},
		{
			name:    "malformed recipient",
			to:      "not-an-email",
			from:    "noreply@example.com",
			wantErr: "invalid recipient address",
		},
		{
			name:    "CRLF in sender config",
			to:      "user@example.com",
			from:    "noreply@example.com\r\nBcc: attacker@evil.com",
			wantErr: "invalid sender address",
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
		{"Bcc: attacker@evil.com\r\n", "Bcc: attacker@evil.com  "},
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
