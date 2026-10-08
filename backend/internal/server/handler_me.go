package server

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// GET /api/v1/me
func (s *Server) handleGetMe(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)

	// An OAuth client is not a person, and this route is deliberately open to
	// machine credentials because an integration legitimately needs to know
	// what it is acting as. It used to look up the nil user id and answer
	// 404 "user 00000000-0000-0000-0000-000000000000", which is nonsense
	// dressed as an error.
	//
	// What it can honestly say is what the credential is: its role and the
	// scopes it holds, which is the part an integration checks.
	if a.UserID == uuid.Nil {
		JSON(w, http.StatusOK, map[string]any{
			"machine": true,
			"role":    a.Role,
			"scopes":  a.Scopes,
		})
		return
	}

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
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
		// Deprecated: the original name for NewPassword. Kept so an API
		// consumer using it is not broken by the rename; it is only honoured
		// when new_password is absent, and current_password is required
		// either way.
		Password string `json:"password"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	next := body.NewPassword
	if next == "" {
		next = body.Password
	}
	if err := user.ValidatePassword(next); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	// The current password, checked.
	//
	// It was not asked for at all: a session cookie was the whole of it. So
	// anyone who got hold of a session — a shared machine left unlocked, a
	// stolen cookie — could set a new password, and the owner was locked out
	// of their own account permanently, because the change also revokes every
	// other session. The cost of asking is one field; the cost of not asking
	// is that a borrowed session becomes a taken account.
	//
	// The frontend's API client has sent current_password since it was
	// written. Nothing read it.
	u, err := s.users.GetByID(r.Context(), a.UserID)
	if err != nil {
		handleError(w, err)
		return
	}
	if _, err := s.users.VerifyPassword(r.Context(), u.Email, body.CurrentPassword); err != nil {
		Error(w, http.StatusUnauthorized, "invalid_credentials", "current password is incorrect")
		return
	}

	if err := s.users.SetPassword(r.Context(), a.UserID, next); err != nil {
		handleError(w, err)
		return
	}
	// Every other session for this user, then a fresh one for the caller.
	// Changing your password is how you evict someone who has your old one, so
	// the other sessions must die — but signing yourself out of the tab you
	// just used to do it is a bug, not security.
	// Detached: the new password is already written, so a client that hangs
	// up here must not leave the sessions it was evicting alive.
	if err := s.sessions.DeleteForUser(context.WithoutCancel(r.Context()), a.UserID); err != nil {
		handleError(w, err)
		return
	}
	if err := s.writeSession(w, r, auth.SessionData{
		UserID:         a.UserID,
		Role:           a.Role,
		MFAPassed:      a.MFAPassed,
		FactorVerified: a.FactorVerified,
	}); err != nil {
		handleError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/me/mfa/enroll
func (s *Server) handleMFAEnrollStart(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	// This route sits outside RequireMFA so a user compelled to enrol can
	// finish. That makes it reachable with a session that has NOT passed the
	// MFA challenge, so re-enrolment of an already-protected account is only
	// permitted once this session has proved a factor — otherwise a password
	// alone would be enough to replace the victim's authenticator.
	// FactorVerified, not MFAPassed: a login that owed nothing has MFAPassed
	// without having proved anything (#333).
	//
	// The secret is minted but NOT written to the user row. Writing it there
	// overwrote the authenticator the user was still relying on, while
	// MFAEnabled stayed true — so opening this screen and closing it locked
	// them out, and an administrator reset was the only way back. It is staged
	// in the session and becomes the user's only once they prove possession by
	// confirming a code from it.
	// Asked through the shared guard, not GenerateMFASecret's own check.
	//
	// That check reads u.MFAEnabled, which is the TOTP column. It was a
	// complete answer to "does this account already have a second factor"
	// while TOTP was the only kind. Passkeys made it a partial one: an
	// account protected by a passkey and no TOTP reads MFAEnabled false, so
	// a session holding only the password could enrol its own authenticator
	// here and walk through the very gate the passkey was protecting.
	//
	// The guard asks about both kinds. Adding a second sort of factor without
	// widening every question that means "has a factor" is how one door gets
	// locked and the one beside it does not.
	if !s.requireFactorOrFirstEnrolment(w, r) {
		return
	}
	secret, qrURL, err := s.users.GenerateMFASecret(r.Context(), a.UserID, s.cfg.BaseURL, a.FactorVerified)
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
	session, _ := s.sessions.Get(r, auth.SessionName)
	sd, _ := session.Values[auth.SessionDataKey].(auth.SessionData)
	sd.PendingMFASecret = secret
	if err := s.writeSession(w, r, sd); err != nil {
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
	// Re-checked here, not just at /mfa/enroll: that call staged a secret at
	// a moment the account happened to be unprotected, and this one decides
	// whether to adopt it. Without re-running the guard, an attacker who
	// staged a secret while the account was still unprotected could confirm
	// it later — even after the legitimate owner finished their own
	// enrolment in the meantime — and silently overwrite it. See #327;
	// register/finish has carried the equivalent re-check for passkeys
	// since #307 item 3.
	if !s.requireFactorOrFirstEnrolment(w, r) {
		return
	}
	a := authmw.GetActor(r)
	var body struct {
		Code string `json:"code"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	// Same durable budget as verification, and spent the same way: before the
	// code is checked, so concurrent attempts cannot all pass an unchanged
	// count.
	if err := s.users.ClaimMFAAttempt(r.Context(), a.UserID); err != nil {
		if errors.Is(err, user.ErrMFALocked) {
			tooManyAttempts(w, user.MFALockDuration)
			return
		}
		handleError(w, err)
		return
	}
	session, _ := s.sessions.Get(r, auth.SessionName)
	sd, _ := session.Values[auth.SessionDataKey].(auth.SessionData)

	if err := s.users.ConfirmMFAEnrollmentWith(r.Context(), a.UserID, sd.PendingMFASecret, body.Code, a.FactorVerified); err != nil {
		// Another confirm enrolled this account between the guard above and
		// the write (#338): the same refusal the guard gives, not a bad code.
		if errors.Is(err, user.ErrMFAAlreadyEnrolled) {
			Error(w, http.StatusForbidden, "mfa_required",
				"this account already has a second factor. Verify with it before adding or removing one, "+
					"or ask an administrator to reset it.")
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
	// Every other session ends: see writeSessionAfterNewFactor.
	if err := s.writeSessionAfterNewFactor(w, r, auth.SessionData{
		UserID:         a.UserID,
		Role:           a.Role,
		MFAPassed:      true,
		FactorVerified: true,
	}); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
