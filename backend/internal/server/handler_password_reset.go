package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/passwordreset"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
	"github.com/publiciallc/go-help-desk/backend/internal/server/notify"
)

// passwordResetAvailable reports whether "forgot password" can work here: the
// feature is wired and the server can send the mail. It is what the request
// endpoint answers 503 on and what /auth/providers reports.
func (s *Server) passwordResetAvailable() bool {
	return s.passwordReset != nil && s.cfg.EmailEnabled()
}

// POST /api/v1/auth/forgot-password — ask for a link to choose a new password.
//
// Always 202 to a well-formed request, whether or not the address has an
// account, and the same work either way: the request queues one event and
// looks nothing up. Whether anything is mailed is decided when it would be sent
// (SendPasswordReset), off the request, as signup does (#348). Anything that
// distinguished the two would make this a way to find out who has an account.
func (s *Server) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	if !s.passwordResetAvailable() {
		Error(w, http.StatusServiceUnavailable, "email_not_configured",
			"password reset needs email, and this server cannot send it; ask an administrator to reset your password")
		return
	}
	// Counted per source address, as signup is: there is no account to key on,
	// because whether there is one must not be learned. The per-account limit is
	// on the send, where it costs the requester no information.
	if !s.loginLimiter.Allow("forgot-password:" + authmw.ClientAddr(r)) {
		tooManyAttempts(w, s.loginLimiter.Remaining())
		return
	}
	var body struct {
		Email string `json:"email"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if err := s.passwordReset.Request(r.Context(), body.Email); err != nil {
		// A fault queueing is ours, and answering anything but 202 would say
		// something about the address, so it is logged and the answer stands.
		slog.ErrorContext(r.Context(), "password reset request could not be queued", "error", err)
	}
	JSON(w, http.StatusAccepted, map[string]string{
		"message": "If that address has an account, we have emailed it a link to choose a new password.",
	})
}

// SendPasswordReset is the send-time step for a password reset (#451), run by
// the verification channel when the worker takes a request off the queue. It
// decides whether the address is to be mailed and mails the link; see
// passwordreset.Service.Send.
//
// The per-account budget is charged here, once per request on its first
// attempt, not once per attempt: charging the retry of a send that failed would
// refuse it, and the request would be dropped with no mail sent (the same defect
// #164 round 1 found for guest links). Outside the worker the attempt is 0,
// which is a first attempt too.
func (s *Server) SendPasswordReset(ctx context.Context, ev notification.Event) error {
	if s.passwordReset == nil {
		return errors.New("password reset is not set up")
	}
	return s.passwordReset.Send(ctx, ev, func(id uuid.UUID) bool {
		return notify.Attempt(ctx) > 1 || s.passwordResetLimiter.Allow(id.String())
	})
}

// POST /api/v1/auth/reset-password — choose a new password with a mailed link.
//
// Gives the person NO session. They go back to the sign-in page and sign in
// with the new password and, if they have one, their second factor. A link in an
// inbox must not be a way past MFA.
//
// Not gated on email being on: a link already mailed was a promise made while it
// was.
func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	if s.passwordReset == nil {
		Error(w, http.StatusServiceUnavailable, "email_not_configured", "password reset is not available")
		return
	}
	// Counted per source address like the request. A link is 256 random bits, so
	// this is not what protects it; it bounds the bcrypt work an anonymous caller
	// can make the server spend.
	if !s.loginLimiter.Allow("reset-password:" + authmw.ClientAddr(r)) {
		tooManyAttempts(w, s.loginLimiter.Remaining())
		return
	}
	var body struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	err := s.passwordReset.Reset(r.Context(), body.Token, body.Password)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, passwordreset.ErrPasswordTooShort):
		Error(w, http.StatusBadRequest, "password_too_short", err.Error())
	case errors.Is(err, passwordreset.ErrPasswordTooLong):
		Error(w, http.StatusBadRequest, "password_too_long", err.Error())
	case errors.Is(err, passwordreset.ErrTokenInvalid):
		Error(w, http.StatusUnprocessableEntity, "token_invalid",
			"this link is invalid, has expired or has already been used; ask for a new one")
	default:
		handleError(w, err)
	}
}
