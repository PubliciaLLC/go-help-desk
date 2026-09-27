package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/webauthn"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// The passkey surface: register one, list them, remove one, and sign in with
// one. See docs/DESIGN.md → Authentication → Passkeys (WebAuthn).
//
// Registration lives under meRouter's DenyMachineCredentials group and
// OUTSIDE RequireMFA. Both placements are load-bearing. A key or OAuth client
// must not be able to add a way of becoming its owner, and somebody compelled
// to enrol has to be able to finish enrolling — the same reason TOTP
// enrolment sits there, and the mechanism
// TestSoleAdministrator_CanSelfRecoverWithNoSecondFactor pins.

// accountFor assembles what the ceremonies need to know about a person.
func (s *Server) accountFor(r *http.Request, id uuid.UUID) (webauthn.Account, error) {
	u, err := s.users.GetByID(r.Context(), id)
	if err != nil {
		return webauthn.Account{}, err
	}
	creds, err := s.passkeyStore.ListByUser(r.Context(), id)
	if err != nil {
		return webauthn.Account{}, err
	}
	return webauthn.Account{
		ID:          u.ID,
		Email:       u.Email,
		DisplayName: u.DisplayName,
		Credentials: creds,
	}, nil
}

// stagePasskey puts a challenge in the session, and takes it back out.
//
// Read-and-clear on the way out, always: a challenge is answerable once. Left
// in place it could be replayed for as long as the session lived, which is
// the window the expiry exists to bound and this makes unreachable even
// inside it.
func (s *Server) stagePasskey(w http.ResponseWriter, r *http.Request, staged webauthn.Staged) error {
	enc, err := staged.Encode()
	if err != nil {
		return err
	}
	session, _ := s.sessions.Get(r, auth.SessionName)
	sd, _ := session.Values[auth.SessionDataKey].(auth.SessionData)
	sd.PendingPasskey = enc
	return s.writeSession(w, r, sd)
}

func (s *Server) takePasskeyChallenge(w http.ResponseWriter, r *http.Request) (webauthn.Staged, auth.SessionData, error) {
	session, _ := s.sessions.Get(r, auth.SessionName)
	sd, _ := session.Values[auth.SessionDataKey].(auth.SessionData)
	staged, err := webauthn.DecodeStaged(sd.PendingPasskey)
	sd.PendingPasskey = ""
	if werr := s.writeSession(w, r, sd); werr != nil {
		return webauthn.Staged{}, sd, werr
	}
	return staged, sd, err
}

// alreadyProtected reports whether this account already has a working second
// factor — a TOTP enrolment or at least one passkey.
//
// It is the question handleMFAEnrollStart asks before letting somebody enrol,
// and the reason is the same: these routes sit outside RequireMFA so that a
// person with NO factor can recover, and that placement means a session
// holding only the password reaches them. Without this check, an attacker who
// knows the password registers their own key on a protected account and now
// holds a second factor. That is not "removing a control", it is passing it.
//
// GenerateMFASecret has enforced this for TOTP since the re-enrolment fix.
// Copying the route placement without copying the guard turned a deliberate
// design into a bypass, which is what an automated review caught here.
func (s *Server) alreadyProtected(r *http.Request, id uuid.UUID) (bool, error) {
	u, err := s.users.GetByID(r.Context(), id)
	if err != nil {
		return false, err
	}
	if u.MFAEnabled {
		return true, nil
	}
	n, err := s.passkeyStore.CountForUser(r.Context(), id)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// requireFactorOrFirstEnrolment refuses a request that would add or remove a
// way of authenticating, when the caller has only proved the first factor and
// the account already has a second.
//
// Reports whether the caller may go on.
func (s *Server) requireFactorOrFirstEnrolment(w http.ResponseWriter, r *http.Request) bool {
	a := authmw.GetActor(r)
	if a == nil || a.UserID == uuid.Nil {
		Error(w, http.StatusUnauthorized, "unauthorized", "sign in first")
		return false
	}
	if a.MFAPassed {
		return true
	}
	protected, err := s.alreadyProtected(r, a.UserID)
	if err != nil {
		handleError(w, err)
		return false
	}
	if protected {
		Error(w, http.StatusForbidden, "mfa_required",
			"this account already has a second factor. Verify with it before adding or removing one, "+
				"or ask an administrator to reset it.")
		return false
	}
	return true
}

// POST /api/v1/me/passkeys/register/start
func (s *Server) handlePasskeyRegisterStart(w http.ResponseWriter, r *http.Request) {
	if !s.requireFactorOrFirstEnrolment(w, r) {
		return
	}
	a := authmw.GetActor(r)
	acct, err := s.accountFor(r, a.UserID)
	if err != nil {
		handleError(w, err)
		return
	}
	creation, staged, err := s.passkeys.BeginRegistration(r.Context(), acct)
	if err != nil {
		handleError(w, err)
		return
	}
	if err := s.stagePasskey(w, r, staged); err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, creation)
}

// POST /api/v1/me/passkeys/register/finish
func (s *Server) handlePasskeyRegisterFinish(w http.ResponseWriter, r *http.Request) {
	if !s.requireFactorOrFirstEnrolment(w, r) {
		return
	}
	a := authmw.GetActor(r)
	staged, _, err := s.takePasskeyChallenge(w, r)
	if err != nil {
		Error(w, http.StatusBadRequest, "no_challenge", err.Error())
		return
	}
	acct, err := s.accountFor(r, a.UserID)
	if err != nil {
		handleError(w, err)
		return
	}
	name := r.URL.Query().Get("name")

	cred, err := s.passkeys.FinishRegistration(r.Context(), acct, staged, r)
	if err != nil {
		passkeyCeremonyError(w, err)
		return
	}
	cred.Name = name
	if err := s.passkeyStore.Create(r.Context(), cred); err != nil {
		if errors.Is(err, webauthn.ErrCredentialExists) {
			Error(w, http.StatusConflict, "credential_exists",
				"that key is already registered")
			return
		}
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/v1/me/passkeys
func (s *Server) handleListPasskeys(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	creds, err := s.passkeyStore.ListByUser(r.Context(), a.UserID)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, creds)
}

// DELETE /api/v1/me/passkeys/{id}
func (s *Server) handleDeletePasskey(w http.ResponseWriter, r *http.Request) {
	// Removing a factor is the same class of act as adding one. A session
	// holding only the password must not be able to strip the second factor
	// off an account and leave it protected by the password alone.
	if !s.requireFactorOrFirstEnrolment(w, r) {
		return
	}
	a := authmw.GetActor(r)
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid credential id")
		return
	}
	// Scoped to the owner in the statement, not by a check here: an id is not
	// an authorisation, and "not yours" and "does not exist" deliberately
	// answer the same way.
	if err := s.passkeyStore.Delete(r.Context(), id, a.UserID); err != nil {
		if errors.Is(err, webauthn.ErrNotFound) {
			Error(w, http.StatusNotFound, "not_found", "no such passkey on this account")
			return
		}
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// passkeyCeremonyError maps a refused ceremony.
//
// The library's own reason is deliberately not returned. It describes the
// exchange — a challenge mismatch, an origin mismatch, a signature that did
// not verify — and none of that is something the person holding the key can
// act on. It is worth logging and not worth showing.
func passkeyCeremonyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, webauthn.ErrChallengeExpired):
		Error(w, http.StatusBadRequest, "challenge_expired", webauthn.ErrChallengeExpired.Error())
	case errors.Is(err, webauthn.ErrNoCredentials):
		Error(w, http.StatusBadRequest, "no_passkey", webauthn.ErrNoCredentials.Error())
	case errors.Is(err, webauthn.ErrRegistrationRefused):
		Error(w, http.StatusBadRequest, "registration_refused", webauthn.ErrRegistrationRefused.Error())
	case errors.Is(err, webauthn.ErrAssertionRefused):
		Error(w, http.StatusUnauthorized, "assertion_refused", webauthn.ErrAssertionRefused.Error())
	default:
		handleError(w, err)
	}
}

// POST /api/v1/auth/local/passkey/start
//
// The second factor half. The password has already been accepted, so the
// session names the account and this only has to prove the key.
func (s *Server) handlePasskeyLoginStart(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	if a == nil || a.UserID == uuid.Nil {
		Error(w, http.StatusUnauthorized, "unauthorized", "sign in with your password first")
		return
	}
	acct, err := s.accountFor(r, a.UserID)
	if err != nil {
		handleError(w, err)
		return
	}
	assertion, staged, err := s.passkeys.BeginLogin(r.Context(), acct)
	if err != nil {
		passkeyCeremonyError(w, err)
		return
	}
	if err := s.stagePasskey(w, r, staged); err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, assertion)
}

// POST /api/v1/auth/local/passkey/finish
func (s *Server) handlePasskeyLoginFinish(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	if a == nil || a.UserID == uuid.Nil {
		Error(w, http.StatusUnauthorized, "unauthorized", "sign in with your password first")
		return
	}
	staged, sd, err := s.takePasskeyChallenge(w, r)
	if err != nil {
		Error(w, http.StatusBadRequest, "no_challenge", err.Error())
		return
	}
	acct, err := s.accountFor(r, a.UserID)
	if err != nil {
		handleError(w, err)
		return
	}
	used, err := s.passkeys.FinishLogin(r.Context(), acct, staged, r)
	if err != nil {
		passkeyCeremonyError(w, err)
		return
	}

	// Recorded off the response path: a sign-in must not wait on a timestamp
	// or fail because of one. The same shape webhook dispatch uses.
	go func(id uuid.UUID, count int64) {
		if err := s.passkeyStore.Touch(context.WithoutCancel(r.Context()), id, count); err != nil {
			slog.Warn("recording passkey use failed", "credential", id, "error", err)
		}
	}(used.ID, used.SignCount)

	sd.UserID = a.UserID
	sd.Role = a.Role
	sd.MFAPassed = true
	if err := s.writeSession(w, r, sd); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
