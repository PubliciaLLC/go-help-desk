package server

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/registration"
)

// GET /api/v1/auth/signup/status — public; tells the frontend whether to show signup.
func (s *Server) handleSignupStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	JSON(w, http.StatusOK, map[string]any{
		"enabled":          s.adminSvc.SelfSignupEnabled(ctx),
		"open_registration": s.adminSvc.OpenRegistrationEnabled(ctx),
		"saml_enabled":     s.adminSvc.SAMLEnabled(ctx),
	})
}

// POST /api/v1/auth/signup — submit a new registration request.
func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !s.adminSvc.SelfSignupEnabled(ctx) {
		Error(w, http.StatusForbidden, "signup_disabled", "self-service signup is not enabled")
		return
	}

	var body struct {
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}

	allowedDomains := s.adminSvc.AllowedEmailDomains(ctx)
	openReg := s.adminSvc.OpenRegistrationEnabled(ctx)

	err := s.registration.Register(ctx, body.Email, body.DisplayName, body.Password, allowedDomains, openReg)
	if err != nil {
		switch {
		case errors.Is(err, registration.ErrDomainNotAllowed):
			Error(w, http.StatusUnprocessableEntity, "domain_not_allowed", "your email domain is not permitted")
		case errors.Is(err, registration.ErrOpenRegistrationRequired):
			Error(w, http.StatusUnprocessableEntity, "domain_not_allowed", "your email domain is not permitted")
		default:
			handleError(w, err)
		}
		return
	}

	JSON(w, http.StatusAccepted, map[string]string{
		"message": "Check your email to complete registration.",
	})
}

// POST /api/v1/auth/verify-email — exchange a token for an active session.
func (s *Server) handleVerifyEmail(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}

	tokenID, err := uuid.Parse(body.Token)
	if err != nil {
		Error(w, http.StatusUnprocessableEntity, "token_invalid", "invalid verification token")
		return
	}

	u, err := s.registration.Verify(r.Context(), tokenID)
	if err != nil {
		if errors.Is(err, registration.ErrTokenExpired) {
			Error(w, http.StatusUnprocessableEntity, "token_expired", "verification link has expired")
			return
		}
		Error(w, http.StatusUnprocessableEntity, "token_invalid", "invalid or already used verification token")
		return
	}

	mfaEnabled := s.adminSvc.MFAEnabled(r.Context())
	mfaEnrollmentNeeded := mfaEnabled && s.adminSvc.MFARequiredFor(r.Context(), string(u.Role))

	if err := s.writeSession(w, r, auth.SessionData{
		UserID:    u.ID,
		Role:      u.Role,
		MFAPassed: !mfaEnrollmentNeeded,
	}); err != nil {
		handleError(w, err)
		return
	}

	JSON(w, http.StatusOK, map[string]any{
		"user":                  u,
		"mfa_enrollment_needed": mfaEnrollmentNeeded,
	})
}

