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
}

const SessionName = "ohd_session"
