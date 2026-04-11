package config

import "github.com/kelseyhightower/envconfig"

// Config holds all runtime configuration loaded from environment variables.
type Config struct {
	// Database
	DatabaseURL string `envconfig:"DATABASE_URL" required:"true"`

	// Server
	HTTPPort int    `envconfig:"HTTP_PORT" default:"8080"`
	BaseURL  string `envconfig:"BASE_URL" required:"true"`

	// Auth
	SessionSecret string `envconfig:"SESSION_SECRET" required:"true"`
	JWTSecret     string `envconfig:"JWT_SECRET" required:"true"`

	// SAML (optional)
	SAMLEnabled     bool   `envconfig:"SAML_ENABLED" default:"false"`
	SAMLMetadataURL string `envconfig:"SAML_METADATA_URL"`
	SAMLCertFile    string `envconfig:"SAML_CERT_FILE"`
	SAMLKeyFile     string `envconfig:"SAML_KEY_FILE"`

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

	// Environment
	AppEnv string `envconfig:"APP_ENV" default:"production"`
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
