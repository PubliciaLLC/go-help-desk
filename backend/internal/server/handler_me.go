package server

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
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
	//
	// Keyed on the client id and not the user id: since #420 a client's actor
	// does carry a user id (its service user's), and the shape this route
	// answers for a client must not change just because of that.
	if a.ClientID != "" {
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
	JSON(w, http.StatusOK, s.meView(r.Context(), u))
}

// meUser is the signed-in account as /me reports it: the user itself, plus for
// staff and administrators what their email switches actually amount to.
type meUser struct {
	user.User
	EmailNotifications *emailNotifications `json:"email_notifications,omitempty"`
	// CanCreateAPIKeys is whether the Account page should offer to make a key
	// (#450). Always present, so a client can tell false from an old server.
	CanCreateAPIKeys bool `json:"can_create_api_keys"`
	// APIKeyMaxLifetimeYears is the longest a new key may live: 1 to 5, or -1
	// for no limit (#450). The create forms cap the expiry with it.
	APIKeyMaxLifetimeYears int `json:"api_key_max_lifetime_years"`
}

// switchState is one email switch as it applies to this person: whether they
// get that mail, and whether an administrator decided that for everyone (so
// their own choice is not what applies).
type switchState struct {
	Enabled bool `json:"enabled"`
	Forced  bool `json:"forced"`
}

type emailNotifications struct {
	Assigned switchState `json:"assigned"`
	Activity switchState `json:"activity"`
	Group    switchState `json:"group"`
}

// meView adds the effective email state (#441). A requester is never sent
// staff mail, so has none to report.
func (s *Server) meView(ctx context.Context, u user.User) meUser {
	v := meUser{User: u, APIKeyMaxLifetimeYears: s.adminSvc.APIKeyMaxLifetimeYears(ctx)}
	v.CanCreateAPIKeys = u.Role == user.RoleAdmin || (u.Role == user.RoleStaff && s.adminSvc.StaffCanCreateAPIKeys(ctx))
	if u.Role == user.RoleUser {
		return v
	}
	ov := s.adminSvc.StaffEmailOverrides(ctx)
	prefs := notification.StaffPrefs{Assigned: u.EmailNotifyAssigned, Activity: u.EmailNotifyActivity, Group: u.EmailNotifyGroup}
	state := func(sw notification.StaffSwitch) switchState {
		return switchState{Enabled: ov.Allows(sw, prefs), Forced: ov.Forced(sw)}
	}
	v.EmailNotifications = &emailNotifications{
		Assigned: state(notification.SwitchAssigned),
		Activity: state(notification.SwitchActivity),
		Group:    state(notification.SwitchGroup),
	}
	return v
}

// PATCH /api/v1/me/email-preferences
//
// The caller's own staff-email switches (#441). Each is optional; only the ones
// sent change. An administrator's override can still force a switch on or off
// for everyone, whatever is saved here.
func (s *Server) handleUpdateEmailPreferences(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	var body struct {
		Assigned *bool `json:"email_notify_assigned"`
		Activity *bool `json:"email_notify_activity"`
		Group    *bool `json:"email_notify_group"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "the email switches must be true or false")
		return
	}
	if err := s.users.SetEmailPreferences(r.Context(), a.UserID, user.EmailPreferencesPatch{
		Assigned: body.Assigned, Activity: body.Activity, Group: body.Group,
	}); err != nil {
		handleError(w, err)
		return
	}
	u, err := s.users.GetByID(r.Context(), a.UserID)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, s.meView(r.Context(), u))
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
	sd := auth.SessionData{
		UserID:         a.UserID,
		Role:           a.Role,
		MFAPassed:      a.MFAPassed,
		FactorVerified: a.FactorVerified,
	}
	// Only a session that owed the change can get here without having passed
	// MFA (RequireMFAOrPasswordChange), and it still owes the enrolment, so
	// it gets the enrolment window rather than an already-expired one.
	if !sd.MFAPassed {
		sd.PendingUntil = auth.PendingDeadline(time.Now(), true)
	}
	if err := s.writeSession(w, r, sd); err != nil {
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
		// What the person got wrong is a 400; a storage fault is the server's
		// and used to land here too, telling them their code was bad (#421).
		if errors.Is(err, user.ErrMFANotStarted) || errors.Is(err, user.ErrMFAInvalidCode) {
			Error(w, http.StatusBadRequest, "invalid_code", err.Error())
			return
		}
		handleError(w, err)
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
