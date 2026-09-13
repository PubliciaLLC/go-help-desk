package server

import (
	"encoding/base64"
	"errors"
	"net/http"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// GET /api/v1/me
func (s *Server) handleGetMe(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	u, err := s.users.GetByID(r.Context(), a.UserID)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, u)
}

// PATCH /api/v1/me/password
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	var body struct {
		Password string `json:"password"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if len(body.Password) < 8 {
		Error(w, http.StatusBadRequest, "bad_request", "password must be at least 8 characters")
		return
	}
	if err := s.users.SetPassword(r.Context(), a.UserID, body.Password); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/v1/me/mfa/enroll
func (s *Server) handleMFAEnrollStart(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	// This route sits outside RequireMFA so a user compelled to enrol can
	// finish. That makes it reachable with a session that has NOT passed the
	// MFA challenge, so re-enrolment of an already-protected account is only
	// permitted once that challenge has been satisfied — otherwise a password
	// alone would be enough to replace the victim's authenticator.
	secret, qrURL, err := s.users.EnrollMFA(r.Context(), a.UserID, s.cfg.BaseURL, a.MFAPassed)
	if err != nil {
		if errors.Is(err, user.ErrMFAAlreadyEnrolled) {
			Error(w, http.StatusForbidden, "mfa_already_enrolled",
				"MFA is already enabled. Verify with your current authenticator before enrolling a new one, or ask an administrator to reset it.")
			return
		}
		handleError(w, err)
		return
	}
	// Render the otpauth:// URL as a QR code PNG encoded as a data URL so the
	// client can render it inline — never sending the secret to a third party.
	png, err := qrcode.Encode(qrURL, qrcode.Medium, 256)
	if err != nil {
		handleError(w, err)
		return
	}
	qrDataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	JSON(w, http.StatusOK, map[string]string{
		"secret":      secret,
		"qr_url":      qrURL,
		"qr_data_url": qrDataURL,
	})
}

// POST /api/v1/me/mfa/enroll/confirm
func (s *Server) handleMFAEnrollConfirm(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	var body struct {
		Code string `json:"code"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	// Same durable budget as verification: this also takes a six-digit code.
	if err := s.users.CheckMFALock(r.Context(), a.UserID); err != nil {
		if errors.Is(err, user.ErrMFALocked) {
			tooManyAttempts(w, user.MFALockDuration)
			return
		}
		handleError(w, err)
		return
	}
	if err := s.users.ConfirmMFAEnrollment(r.Context(), a.UserID, body.Code); err != nil {
		if lockErr := s.users.RecordMFAFailure(r.Context(), a.UserID); lockErr != nil && !errors.Is(lockErr, user.ErrMFALocked) {
			handleError(w, lockErr)
			return
		}
		Error(w, http.StatusBadRequest, "invalid_code", err.Error())
		return
	}
	if err := s.users.ClearMFAFailures(r.Context(), a.UserID); err != nil {
		handleError(w, err)
		return
	}
	// Successful enrollment satisfies this login's MFA challenge — flip the
	// session so forced-enrollment users aren't locked out until they log out.
	if err := s.writeSession(w, r, auth.SessionData{
		UserID:    a.UserID,
		Role:      a.Role,
		MFAPassed: true,
	}); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
