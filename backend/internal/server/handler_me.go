package server

import (
	"encoding/base64"
	"net/http"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
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
	secret, qrURL, err := s.users.EnrollMFA(r.Context(), a.UserID, s.cfg.BaseURL)
	if err != nil {
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
	if err := s.users.ConfirmMFAEnrollment(r.Context(), a.UserID, body.Code); err != nil {
		Error(w, http.StatusBadRequest, "invalid_code", err.Error())
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
