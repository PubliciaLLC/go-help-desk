package auth

import (
	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// SessionData is the payload stored in the signed session cookie.
// MFAPassed is false until the user completes the TOTP challenge; requests
// to MFA-protected routes are rejected until it is true.
type SessionData struct {
	UserID    uuid.UUID
	Role      user.Role
	MFAPassed bool

	// OIDCState stores the temporary OAuth2 state value used during login.
	// It is cleared after callback validation.
	OIDCState string
}

const SessionName = "ohd_session"
