package user

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Role names the three access tiers. Order matters: do not change values.
type Role string

const (
	RoleAdmin Role = "admin"
	RoleStaff Role = "staff"
	RoleUser  Role = "user"
)

// User is the canonical representation of an identity in the system.
// It is auth-method-agnostic: PasswordHash is empty for SAML-only accounts;
// SAMLSubject is empty for local-only accounts.
type User struct {
	ID           uuid.UUID  `json:"id"`
	Email        string     `json:"email"`
	DisplayName  string     `json:"display_name"`
	Role         Role       `json:"role"`
	PasswordHash string     `json:"-"`
	MFASecret    string     `json:"-"`
	MFAEnabled   bool       `json:"mfa_enabled"`
	SAMLSubject  string     `json:"-"`
	OIDCSubject  string     `json:"-"`
	Disabled     bool       `json:"disabled"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	DeletedAt    *time.Time `json:"-"`
}

// IsActive returns true when the user is neither disabled nor soft-deleted.
func (u User) IsActive() bool { return !u.Disabled && u.DeletedAt == nil }

// Validate returns an error if the user is structurally invalid.
// It does not validate the password hash or MFA secret — those are set by
// the service layer during specific operations.
// ValidateEmail checks that s is a single, bare email address and returns it
// normalised.
//
// Nothing in this application validated an email address. Register stored
// whatever arrived, User.Validate only checked non-empty, and the sole
// mail.ParseAddress lived in the mail sender — which meant an address
// containing CRLF was accepted at signup, written to the database, and became
// a real account on verification. The SMTP layer refuses to send to it, so a
// header was never injected, but the address is also a login identity and it
// was never a valid one.
//
// A display name is rejected: "Attacker <victim@example.com>" parses happily
// and would store a different address than it appears to.
func ValidateEmail(s string) (string, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return "", errors.New("email is required")
	}
	addr, err := mail.ParseAddress(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid email address: %w", err)
	}
	if addr.Name != "" {
		return "", errors.New("email address must not include a display name")
	}
	if addr.Address == "" {
		return "", errors.New("invalid email address")
	}
	return strings.ToLower(addr.Address), nil
}

func (u User) Validate() error {
	if _, err := ValidateEmail(u.Email); err != nil {
		return err
	}
	if strings.TrimSpace(u.DisplayName) == "" {
		return errors.New("display name is required")
	}
	switch u.Role {
	case RoleAdmin, RoleStaff, RoleUser:
	default:
		return errors.New("invalid role")
	}
	return nil
}

// IsLocalAuthAllowed returns true if this user may log in with a username and
// password. When SAML is globally enabled, only admins retain local auth as a
// failsafe. In all other cases local auth is available to everyone.
func IsLocalAuthAllowed(u User, samlEnabled bool) bool {
	if !samlEnabled {
		return true
	}
	return u.Role == RoleAdmin
}
