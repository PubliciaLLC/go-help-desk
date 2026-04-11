package config_test

import (
	"os"
	"testing"

	"github.com/open-help-desk/open-help-desk/backend/internal/config"
	"github.com/stretchr/testify/require"
)

func TestLoad_RequiredFields(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		wantErr bool
	}{
		{
			name: "all required fields set",
			env: map[string]string{
				"DATABASE_URL":   "postgres://user:pass@localhost/db",
				"BASE_URL":       "https://helpdesk.example.com",
				"SESSION_SECRET": "supersecret",
				"JWT_SECRET":     "jwtsecret",
			},
			wantErr: false,
		},
		{
			name: "missing DATABASE_URL",
			env: map[string]string{
				"BASE_URL":       "https://helpdesk.example.com",
				"SESSION_SECRET": "supersecret",
				"JWT_SECRET":     "jwtsecret",
			},
			wantErr: true,
		},
		{
			name: "missing BASE_URL",
			env: map[string]string{
				"DATABASE_URL":   "postgres://user:pass@localhost/db",
				"SESSION_SECRET": "supersecret",
				"JWT_SECRET":     "jwtsecret",
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Clear all relevant env vars, set test values.
			for _, key := range []string{
				"DATABASE_URL", "BASE_URL", "SESSION_SECRET", "JWT_SECRET",
				"HTTP_PORT", "SAML_ENABLED", "APP_ENV",
			} {
				os.Unsetenv(key)
			}
			for k, v := range tc.env {
				os.Setenv(k, v)
			}
			t.Cleanup(func() {
				for k := range tc.env {
					os.Unsetenv(k)
				}
			})

			_, err := config.Load()
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestLoad_Defaults(t *testing.T) {
	for _, key := range []string{
		"DATABASE_URL", "BASE_URL", "SESSION_SECRET", "JWT_SECRET",
		"HTTP_PORT", "SMTP_PORT", "ATTACHMENT_DIR", "APP_ENV",
		"SAML_ENABLED", "GUEST_SUBMISSION_ENABLED", "SLA_ENABLED", "MFA_ENABLED",
	} {
		os.Unsetenv(key)
	}
	os.Setenv("DATABASE_URL", "postgres://user:pass@localhost/db")
	os.Setenv("BASE_URL", "https://helpdesk.example.com")
	os.Setenv("SESSION_SECRET", "s")
	os.Setenv("JWT_SECRET", "j")
	t.Cleanup(func() {
		for _, key := range []string{"DATABASE_URL", "BASE_URL", "SESSION_SECRET", "JWT_SECRET"} {
			os.Unsetenv(key)
		}
	})

	cfg, err := config.Load()
	require.NoError(t, err)
	require.Equal(t, 8080, cfg.HTTPPort)
	require.Equal(t, 587, cfg.SMTPPort)
	require.Equal(t, "/data/attachments", cfg.AttachmentDir)
	require.Equal(t, "production", cfg.AppEnv)
	require.False(t, cfg.SAMLEnabled)
	require.False(t, cfg.GuestSubmissionEnabled)
	require.False(t, cfg.SLAEnabled)
	require.False(t, cfg.MFAEnabled)
}

func TestConfig_EmailEnabled(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.Config
		want bool
	}{
		{name: "host and from set", cfg: config.Config{SMTPHost: "smtp.example.com", SMTPFrom: "noreply@example.com"}, want: true},
		{name: "host missing", cfg: config.Config{SMTPFrom: "noreply@example.com"}, want: false},
		{name: "from missing", cfg: config.Config{SMTPHost: "smtp.example.com"}, want: false},
		{name: "both missing", cfg: config.Config{}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.cfg.EmailEnabled())
		})
	}
}
