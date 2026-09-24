package config

import (
	"time"

	"github.com/kelseyhightower/envconfig"
)

// Config holds all runtime configuration loaded from environment variables.
type Config struct {
	// Database
	DatabaseURL string `envconfig:"DATABASE_URL" required:"true"`

	// Server
	HTTPPort int    `envconfig:"HTTP_PORT" default:"8080"`
	BaseURL  string `envconfig:"BASE_URL" required:"true"`

	// Auth
	// AuthRateLimitPerMinute caps FAILED password attempts per account per
	// minute, and signup attempts per source address per minute. 0 disables
	// both, which the test harness uses — a suite logging in hundreds of times
	// a second is not an attack.
	//
	// Not TOTP: those are counted durably on the user row, because a counter a
	// restart clears is not a limit on a six-digit secret. See
	// user.MFAMaxFailedAttempts.
	//
	// Only failures count and a correct password is always honoured, so this
	// cannot be used to lock someone out of their own account.
	//
	// It caps the status code, not the guessing: see AuthThrottleDelay, which
	// is what actually limits throughput.
	AuthRateLimitPerMinute int `envconfig:"AUTH_RATE_LIMIT_PER_MINUTE" default:"10"`

	// AuthThrottleDelay is how long a login for an account that has spent its
	// budget waits before the password is checked.
	//
	// The counter above could not limit guessing on its own, and measuring it
	// showed exactly that: with a limit of three, eight wrong guesses answered
	// 401 401 401 429 429 429 429 429 — and every one of those 429s had still
	// run the password check. An attacker who ignores the status code had
	// unlimited online guesses, bounded only by this server's bcrypt
	// throughput, around ten to fifteen a second per core. MFA is off by
	// default, so on most instances that was the only online defence.
	//
	// The delay is taken one request at a time per account, so it bounds
	// guesses per second rather than just slowing each one down. A legitimate
	// user is never refused — their correct password still works, one second
	// later — which is the property the ordering of this handler exists to
	// protect.
	//
	// 0 disables it, which the test harness uses.
	AuthThrottleDelay time.Duration `envconfig:"AUTH_THROTTLE_DELAY" default:"1s"`

	SessionSecret string `envconfig:"SESSION_SECRET" required:"true"`
	JWTSecret     string `envconfig:"JWT_SECRET" required:"true"`

	// Email (optional — notifications disabled if not set)
	SMTPHost     string `envconfig:"SMTP_HOST"`
	SMTPPort     int    `envconfig:"SMTP_PORT" default:"587"`
	SMTPUser     string `envconfig:"SMTP_USER"`
	SMTPPassword string `envconfig:"SMTP_PASSWORD"`
	SMTPFrom     string `envconfig:"SMTP_FROM"`

	// Features
	GuestSubmissionEnabled bool `envconfig:"GUEST_SUBMISSION_ENABLED" default:"false"`
	SLAEnabled             bool `envconfig:"SLA_ENABLED" default:"false"`
	MFAEnabled             bool `envconfig:"MFA_ENABLED" default:"false"`

	// Storage
	AttachmentDir string `envconfig:"ATTACHMENT_DIR" default:"/data/attachments"`

	// Antivirus — optional ClamAV daemon address (e.g. "tcp://localhost:3310").
	// When empty, virus scanning is skipped.
	ClamAVAddr string `envconfig:"CLAMAV_ADDR"`

	// Environment
	AppEnv   string `envconfig:"APP_ENV" default:"production"`
	LogLevel string `envconfig:"LOG_LEVEL" default:"info"`
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// IsDevelopment returns true when running in development mode.
func (c *Config) IsDevelopment() bool {
	return c.AppEnv == "development"
}

// EmailEnabled returns true when SMTP is configured.
func (c *Config) EmailEnabled() bool {
	return c.SMTPHost != "" && c.SMTPFrom != ""
}
