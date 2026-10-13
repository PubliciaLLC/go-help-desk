package config_test

import (
	"os"
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/config"
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
			},
			wantErr: false,
		},
		{
			name: "missing DATABASE_URL",
			env: map[string]string{
				"BASE_URL":       "https://helpdesk.example.com",
				"SESSION_SECRET": "supersecret",
			},
			wantErr: true,
		},
		{
			name: "missing BASE_URL",
			env: map[string]string{
				"DATABASE_URL":   "postgres://user:pass@localhost/db",
				"SESSION_SECRET": "supersecret",
			},
			wantErr: true,
		},
		{
			name: "JWT_SECRET can be absent (generated from database)",
			env: map[string]string{
				"DATABASE_URL":   "postgres://user:pass@localhost/db",
				"BASE_URL":       "https://helpdesk.example.com",
				"SESSION_SECRET": "supersecret",
			},
			wantErr: false,
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
		"SAML_ENABLED", "SLA_ENABLED",
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
	require.False(t, cfg.SLAEnabled)
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

func TestLoad_WithAPPENV(t *testing.T) {
	for _, key := range []string{
		"DATABASE_URL", "BASE_URL", "SESSION_SECRET", "JWT_SECRET",
		"HTTP_PORT", "SMTP_PORT", "ATTACHMENT_DIR", "APP_ENV",
		"SAML_ENABLED", "SLA_ENABLED",
	} {
		os.Unsetenv(key)
	}
	os.Setenv("DATABASE_URL", "postgres://user:pass@localhost/db")
	os.Setenv("BASE_URL", "https://helpdesk.example.com")
	os.Setenv("SESSION_SECRET", "s")
	os.Setenv("JWT_SECRET", "j")
	os.Setenv("APP_ENV", "development")
	t.Cleanup(func() {
		for _, key := range []string{"DATABASE_URL", "BASE_URL", "SESSION_SECRET", "JWT_SECRET", "APP_ENV"} {
			os.Unsetenv(key)
		}
	})

	cfg, err := config.Load()
	require.NoError(t, err)
	require.Equal(t, "development", cfg.AppEnv)
}

func TestLoad_AuthThrottleDelay(t *testing.T) {
	setRequired := func() {
		os.Setenv("DATABASE_URL", "postgres://user:pass@localhost/db")
		os.Setenv("BASE_URL", "https://helpdesk.example.com")
		os.Setenv("SESSION_SECRET", "s")
		os.Setenv("JWT_SECRET", "j")
	}
	cleanupRequired := func() {
		for _, key := range []string{"DATABASE_URL", "BASE_URL", "SESSION_SECRET", "JWT_SECRET", "AUTH_THROTTLE_DELAY", "GHD_ALLOW_ZERO_AUTH_THROTTLE"} {
			os.Unsetenv(key)
		}
	}

	cases := []struct {
		name      string
		value     string
		allowZero bool
		wantErr   bool
		errMsg    string
	}{
		{name: "unset (default 1s)", value: "", allowZero: false, wantErr: false},
		{name: "1s accepted", value: "1s", allowZero: false, wantErr: false},
		{name: "2m accepted", value: "2m", allowZero: false, wantErr: false},
		{name: "0 refused", value: "0", allowZero: false, wantErr: true, errMsg: "AUTH_THROTTLE_DELAY"},
		{name: "0s refused", value: "0s", allowZero: false, wantErr: true, errMsg: "AUTH_THROTTLE_DELAY"},
		{name: "-1s refused", value: "-1s", allowZero: false, wantErr: true, errMsg: "AUTH_THROTTLE_DELAY"},
		{name: "0 allowed with GHD_ALLOW_ZERO_AUTH_THROTTLE=1", value: "0", allowZero: true, wantErr: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cleanupRequired()
			setRequired()
			if tc.value != "" {
				os.Setenv("AUTH_THROTTLE_DELAY", tc.value)
			}
			if tc.allowZero {
				os.Setenv("GHD_ALLOW_ZERO_AUTH_THROTTLE", "1")
			}
			t.Cleanup(cleanupRequired)

			_, err := config.Load()
			if tc.wantErr {
				require.Error(t, err, "expected an error but got none")
				if tc.errMsg != "" {
					require.Contains(t, err.Error(), tc.errMsg, "error message does not contain expected text")
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}
