package config_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/config"
)

// Email is on only with both SMTP_HOST and SMTP_FROM. Half of that pair used
// to disable it without a word; the startup log now says which half is missing.
func TestEmailStatus(t *testing.T) {
	cases := []struct {
		name          string
		host, from    string
		wantMsg       string
		wantMisconfig bool
	}{
		{"both set", "smtp.example.com", "help@example.com", "email enabled", false},
		{"neither set", "", "", "email disabled: SMTP_HOST and SMTP_FROM are not set", false},
		{"host without sender", "smtp.example.com", "", "email disabled: SMTP_HOST is set but SMTP_FROM is not", true},
		{"sender without host", "", "help@example.com", "email disabled: SMTP_FROM is set but SMTP_HOST is not", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg, misconfigured := (&config.Config{SMTPHost: tc.host, SMTPFrom: tc.from}).EmailStatus()
			require.Equal(t, tc.wantMsg, msg)
			require.Equal(t, tc.wantMisconfig, misconfigured)
		})
	}
}
