package user

import (
	"errors"
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
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	DeletedAt    *time.Time `json:"-"`
}

// IsActive returns true when the user has not been soft-deleted.
func (u User) IsActive() bool { return u.DeletedAt == nil }

// Validate returns an error if the user is structurally invalid.
// It does not validate the password hash or MFA secret — those are set by
// the service layer during specific operations.
func (u User) Validate() error {
	if strings.TrimSpace(u.Email) == "" {
		return errors.New("email is required")
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
