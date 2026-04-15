package server

import (
	"net/http"

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
	JSON(w, http.StatusOK, map[string]string{
		"secret": secret,
		"qr_url": qrURL,
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
	w.WriteHeader(http.StatusNoContent)
}
