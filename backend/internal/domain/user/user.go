package user

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrValidation marks a refusal caused by the caller's input rather than by
// anything going wrong. Without it these came back as plain errors, which
// handleError cannot tell from a database outage — so a mistyped email address
// was answered with 500 "an internal error occurred" and logged as one.
var ErrValidation = errors.New("invalid input")

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
		return "", fmt.Errorf("%w: email is required", ErrValidation)
	}
	addr, err := mail.ParseAddress(trimmed)
	if err != nil {
		return "", fmt.Errorf("%w: %q is not an email address", ErrValidation, trimmed)
	}
	if addr.Name != "" {
		return "", fmt.Errorf("%w: email address must not include a display name", ErrValidation)
	}
	if addr.Address == "" {
		return "", fmt.Errorf("%w: invalid email address", ErrValidation)
	}
	return strings.ToLower(addr.Address), nil
}

func (u User) Validate() error {
	if _, err := ValidateEmail(u.Email); err != nil {
		return err
	}
	if strings.TrimSpace(u.DisplayName) == "" {
		return fmt.Errorf("%w: display name is required", ErrValidation)
	}
	switch u.Role {
	case RoleAdmin, RoleStaff, RoleUser:
	default:
		return fmt.Errorf("%w: invalid role", ErrValidation)
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
