package server

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// Admin user management: listing, creation, profile edits, password reset.
//
// Split out of handler_admin.go, which had grown to 1,332 lines across twelve
// unrelated resources. Moved verbatim: no handler logic changed.

// ── Users ────────────────────────────────────────────────────────────────��───

// adminUserSummary is the list-view representation of a user for admins.
// It exposes disabled status and auth type without returning sensitive fields.
type adminUserSummary struct {
	ID          uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	Role        user.Role `json:"role"`
	Disabled    bool      `json:"disabled"`
	AuthType    string    `json:"auth_type"` // "local", "saml", "oidc", "both" (local+saml), or "+"-joined ("local+oidc", "saml+oidc", "local+saml+oidc")
	MFAEnabled  bool      `json:"mfa_enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// adminUserDetail extends the summary with group memberships.
type adminUserDetail struct {
	adminUserSummary
	HasPassword bool `json:"has_password"`
	// PasskeyCount lets the page tell "has a second factor" from "has TOTP":
	// MFAEnabled is the TOTP flag only, so a passkey-only account read as
	// having nothing to reset (#307).
	PasskeyCount int           `json:"passkey_count"`
	Groups       []group.Group `json:"groups"`
}

// authTypeOf names the ways an account can sign in, read off what it holds: a
// password hash, a SAML subject, an OIDC subject. One method is its own name;
// several are joined with "+" in that order. "both" is local plus SAML and
// keeps the meaning it has always had. An account with nothing yet reads
// "local", as before.
func authTypeOf(u user.User) string {
	var methods []string
	if u.PasswordHash != "" {
		methods = append(methods, "local")
	}
	if u.SAMLSubject != "" {
		methods = append(methods, "saml")
	}
	if u.OIDCSubject != "" {
		methods = append(methods, "oidc")
	}
	switch got := strings.Join(methods, "+"); got {
	case "":
		return "local"
	case "local+saml":
		return "both"
	default:
		return got
	}
}

func toAdminSummary(u user.User) adminUserSummary {
	return adminUserSummary{
		ID:          u.ID,
		Email:       u.Email,
		DisplayName: u.DisplayName,
		Role:        u.Role,
		Disabled:    u.Disabled,
		AuthType:    authTypeOf(u),
		MFAEnabled:  u.MFAEnabled,
		CreatedAt:   u.CreatedAt,
		UpdatedAt:   u.UpdatedAt,
	}
}

// Paged mode of GET /admin/users (#449), switched on by ?page= and by nothing
// else. The list used to ignore every query parameter and answer with the
// newest 200 accounts, and the frontend sent limit and offset anyway, so
// honouring those (or q, or role) for a client that never asked for a page
// would change what it gets. A new parameter name cannot have been sent by an
// old client.
const (
	usersDefaultPerPage = 50
	usersMaxPerPage     = 200
)

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Has("page") {
		s.handleListUsersPaged(w, r)
		return
	}
	users, err := s.users.ListAdmin(r.Context(), 200, 0)
	if err != nil {
		handleError(w, err)
		return
	}
	out := make([]adminUserSummary, len(users))
	for i, u := range users {
		out[i] = toAdminSummary(u)
	}
	JSON(w, http.StatusOK, out)
}

// handleListUsersPaged answers a bare array, like the unpaged list, with the
// number of matches in X-Total-Count so a client can show "n of N". A page past
// the end is an empty array, not an error.
func (s *Server) handleListUsersPaged(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	page, err := strconv.Atoi(q.Get("page"))
	if err != nil || page < 1 {
		Error(w, http.StatusBadRequest, "bad_request", "page must be a whole number, 1 or more")
		return
	}
	perPage := usersDefaultPerPage
	if v := q.Get("per_page"); v != "" {
		perPage, err = strconv.Atoi(v)
		if err != nil || perPage < 1 {
			Error(w, http.StatusBadRequest, "bad_request", "per_page must be a whole number, 1 or more")
			return
		}
		perPage = min(perPage, usersMaxPerPage)
	}
	role := q.Get("role")
	switch user.Role(role) {
	case "", user.RoleAdmin, user.RoleStaff, user.RoleUser:
	default:
		Error(w, http.StatusBadRequest, "bad_request", "role must be one of: admin, staff, user")
		return
	}

	// The store takes an int32. A page number big enough to overflow it is
	// past the end of any table, so it gets the same empty answer.
	offset := math.MaxInt32
	if page-1 <= (math.MaxInt32-perPage)/perPage {
		offset = (page - 1) * perPage
	}
	users, total, err := s.users.SearchAdmin(r.Context(), role, strings.TrimSpace(q.Get("q")), perPage, offset)
	if err != nil {
		handleError(w, err)
		return
	}
	out := make([]adminUserSummary, len(users))
	for i, u := range users {
		out[i] = toAdminSummary(u)
	}
	w.Header().Set("X-Total-Count", strconv.FormatInt(total, 10))
	JSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
		Role        string `json:"role"`
		Password    string `json:"password"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if user.Role(body.Role) == user.RoleAdmin &&
		denyMachineAdminTakeover(w, r, "create an administrator") {
		return
	}
	u, err := s.users.Create(r.Context(), user.CreateUserInput{
		Email:       body.Email,
		DisplayName: body.DisplayName,
		Role:        user.Role(body.Role),
		Password:    body.Password,
	})
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusCreated, u)
}

func (s *Server) handleGetUser(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid user ID")
		return
	}
	u, err := s.users.GetByIDAdmin(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}
	detail, err := s.adminUserDetailFor(r.Context(), u)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, detail)
}

// adminUserDetailFor is the one place the detail view is built, so GET and
// PATCH cannot disagree about what it carries (PATCH once answered
// passkey_count: 0 for every account).
func (s *Server) adminUserDetailFor(ctx context.Context, u user.User) (adminUserDetail, error) {
	groups, err := s.groups.ListGroupsForUser(ctx, u.ID)
	if err != nil {
		return adminUserDetail{}, err
	}
	passkeys, err := s.passkeyStore.CountForUser(ctx, u.ID)
	if err != nil {
		return adminUserDetail{}, err
	}
	return adminUserDetail{
		adminUserSummary: toAdminSummary(u),
		HasPassword:      u.PasswordHash != "",
		PasskeyCount:     int(passkeys),
		Groups:           groups,
	}, nil
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid user ID")
		return
	}
	var body struct {
		DisplayName *string `json:"display_name"`
		Email       *string `json:"email"`
		Role        *string `json:"role"`
		Disabled    *bool   `json:"disabled"`
		ResetMFA    bool    `json:"reset_mfa"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}

	// Every field is checked before the first write (#419). The steps below
	// each commit on their own, so a request refused halfway — a disable
	// followed by an email that does not parse — had already disabled the
	// account and ended its sessions, and still answered 400.
	//
	// Before the machine check as well, so a session and an API key meet the
	// same checks in the same order: a malformed body is a 400 to both.
	if body.Role != nil && !user.Role(*body.Role).Valid() {
		handleError(w, fmt.Errorf("%w: invalid role", user.ErrValidation))
		return
	}
	if body.Email != nil {
		if _, err := user.ValidateEmail(*body.Email); err != nil {
			handleError(w, err)
			return
		}
	}
	if body.DisplayName != nil {
		if _, err := user.ValidateDisplayName(*body.DisplayName); err != nil {
			handleError(w, err)
			return
		}
	}

	// Administrators are managed by administrators, not by automation.
	//
	// Before any mutation, because the disable below used to be applied before
	// the promotion guard further down — a refused request that had already
	// disabled the account.
	//
	// Guarding only promotion left the reverse open: demote an administrator,
	// then reset the password of what is now a staff account, then sign in.
	// Three requests. The rule is therefore the whole account, in either
	// direction: a machine credential may not change an administrator's role,
	// email, enabled state, password or MFA, and may not create or promote one.
	if isMachine(r) {
		if s.denyMachineTargetingAdmin(w, r, id, "modify an administrator") {
			return
		}
		if body.Role != nil && user.Role(*body.Role) == user.RoleAdmin {
			denyMachineAdminTakeover(w, r, "promote a user to administrator")
			return
		}
	}

	// A deleted account is not there to change. Asked before the first write
	// for the same reason as the validation above: the steps below would
	// otherwise reach the deleted row one at a time, applying some and then
	// answering 404 for the rest.
	target, err := s.users.GetByIDAdmin(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}
	if target.DeletedAt != nil {
		handleError(w, fmt.Errorf("user %s: %w", id, user.ErrNotFound))
		return
	}

	// The two refusals that depend on what the database holds, asked now for
	// the same reason: an MFA reset followed by a refused demotion had already
	// cleared the factors, and a disable followed by a taken address had
	// already disabled the account. These are checks, not locks, so a request
	// that loses a race still reaches the guarded statements below, which
	// refuse on their own; this only closes the ordinary case.
	if target.Role == user.RoleAdmin && !target.Disabled &&
		((body.Disabled != nil && *body.Disabled) || (body.Role != nil && user.Role(*body.Role) != user.RoleAdmin)) {
		admins, err := s.users.ListActiveAdmins(r.Context())
		if err != nil {
			handleError(w, err)
			return
		}
		if len(admins) <= 1 {
			handleError(w, user.ErrLastAdmin)
			return
		}
	}
	if body.Email != nil {
		addr, err := user.ValidateEmail(*body.Email)
		if err != nil {
			handleError(w, err)
			return
		}
		if addr != strings.ToLower(target.Email) {
			taken, err := s.users.EmailIsTaken(r.Context(), addr)
			if err != nil {
				handleError(w, err)
				return
			}
			if taken {
				handleError(w, user.ErrEmailTaken)
				return
			}
		}
	}

	// Disable/enable toggle (processed before any profile update).
	if body.Disabled != nil {
		if *body.Disabled {
			// Disable refuses the last active administrator itself — the
			// check and the write are one statement, because asking first
			// and writing second lost the race to two parallel requests.
			if err := s.users.Disable(r.Context(), id); err != nil {
				handleError(w, err)
				return
			}
			// Disabling an account that keeps working until its cookie
			// expires is not disabling it.
			if err := s.sessions.DeleteForUser(r.Context(), id); err != nil {
				handleError(w, err)
				return
			}
		} else {
			if err := s.users.Enable(r.Context(), id); err != nil {
				handleError(w, err)
				return
			}
		}
	}

	// MFA reset (works regardless of disabled state).
	//
	// This deliberately does NOT force a password rotation. It briefly did, to
	// defend a chain where someone already holding the password burns the
	// victim's TOTP budget, waits for an administrator to clear MFA, and
	// enrols first. That chain is real but needs a targeted attacker who
	// already has valid credentials and is racing the victim; the ordinary
	// reason for a reset is a new phone. Making every routine reset also
	// require conveying a new password out of band was a large, permanent cost
	// for a rare one, so the requirement was removed. Password reset remains
	// available separately when an administrator judges it warranted.
	if body.ResetMFA {
		actor := authmw.GetActor(r)
		var actorID *uuid.UUID
		if actor != nil {
			actorID = &actor.UserID
		}
		if err := s.users.ResetMFA(r.Context(), id, actorID); err != nil {
			handleError(w, err)
			return
		}
		// Belt and braces. ResetMFA's statement (ClearFactors) already ended
		// this account's sessions in the same write, so this finds nothing to
		// delete today. It is kept because an existing session carries
		// MFAPassed=true and handleMFAEnrollStart passes that straight in as
		// allowReenroll, so whoever holds a cookie minted before the reset
		// could enrol their own authenticator afterwards, no password needed,
		// should ResetMFA ever stop ending sessions itself.
		if err := s.sessions.DeleteForUser(r.Context(), id); err != nil {
			handleError(w, err)
			return
		}
	}

	// Profile field updates.
	if body.DisplayName != nil || body.Email != nil || body.Role != nil {
		u, err := s.users.GetByIDAdmin(r.Context(), id)
		if err != nil {
			handleError(w, err)
			return
		}
		originalRole := u.Role
		if body.DisplayName != nil {
			u.DisplayName = *body.DisplayName
		}
		if body.Email != nil {
			// Lowercased and trimmed, the same as Create, SAML and OIDC.
			// Without it an administrator who typed a capital letter stored
			// the address verbatim, and login — which lowercases before
			// looking up — could no longer find the account under either
			// spelling. The user was locked out and nothing said why.
			u.Email = strings.ToLower(strings.TrimSpace(*body.Email))
		}
		// A role change is its own operation, not part of the profile write.
		//
		// It revokes sessions, it is refused to machine credentials, and it
		// is the one that can leave an instance with no administrator — so it
		// goes through the statement that checks and writes together. Folding
		// it into the general update would put the guard back in front of the
		// write, which is where it lost.
		roleChanged := body.Role != nil && user.Role(*body.Role) != originalRole
		if roleChanged {
			if err := s.users.SetRole(r.Context(), id, user.Role(*body.Role)); err != nil {
				handleError(w, err)
				return
			}
			// And carried into the profile write below, which writes the role
			// column too.
			//
			// Without this the request undid itself: SetRole wrote the new
			// role and Update immediately wrote the old one back, so nobody
			// could be promoted or demoted — while the target was still
			// signed out for a change that did not happen. It answered 200
			// and the response carried the old role, which is the shape that
			// gets believed.
			u.Role = user.Role(*body.Role)

			// Revoked HERE, straight after the role is committed — not after
			// the profile write below.
			//
			// A session carries the role in its payload, so a demoted
			// administrator with a live cookie is still an administrator
			// until it is revoked. Doing that after the profile write meant
			// any failure in between — a blank display name, an email that
			// belongs to somebody else — left the demotion committed and the
			// session alive, answering 400. Retrying then saw the role
			// already changed, so roleChanged was false and it never revoked
			// at all: a demoted account kept full authority for the life of
			// the cookie, and could promote itself straight back.
			//
			// The order that survives a failure is: change the authority,
			// then revoke what was granted under the old one, then do the
			// cosmetic part.
			if err := s.sessions.DeleteForUser(r.Context(), id); err != nil {
				handleError(w, err)
				return
			}
		}
		// Only the address and the name. Update writes the whole row from
		// the copy read at the top of this handler, so a password set, an MFA
		// enrolment or another administrator's role change that landed in
		// between would be written back.
		if err := s.users.UpdateProfile(r.Context(), id, u.Email, u.DisplayName); err != nil {
			handleError(w, err)
			return
		}
	}

	// Re-fetch and return the updated detail view.
	u, err := s.users.GetByIDAdmin(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}
	detail, err := s.adminUserDetailFor(r.Context(), u)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, detail)
}

// isMachine reports whether an API key or OAuth client made this request.
func isMachine(r *http.Request) bool {
	a := authmw.GetActor(r)
	return a != nil && a.Machine
}

// denyMachineAdminTakeover refuses a machine credential that is reaching for
// administrator credentials.
//
// Refusing only the credential's own owner closed the path and not the outcome.
// Every API key that reaches /admin is administrator-owned, because only an
// administrator can mint one and it acts at its owner's role — so with
// users:write the long way round was four requests: create a second
// administrator, sign in as them, reset the original owner's password. Promoting
// an existing user to administrator gets there too.
//
// So the rule is about administrators rather than about self: a machine
// credential may not create one, promote to one, or reset one's credentials.
// That closes every route to holding an administrator's password, including the
// self-target that started this.
//
// Managing non-administrator users — provisioning, deprovisioning, resetting a
// forgotten password — is ordinary automation and stays available.
func denyMachineAdminTakeover(w http.ResponseWriter, r *http.Request, why string) bool {
	if !isMachine(r) {
		return false
	}
	Error(w, http.StatusForbidden, "session_required",
		"an API key or OAuth client cannot "+why+"; this requires a signed-in session")
	return true
}

// denyMachineTargetingAdmin refuses when the target account is an administrator.
func (s *Server) denyMachineTargetingAdmin(w http.ResponseWriter, r *http.Request, target uuid.UUID, why string) bool {
	if !isMachine(r) {
		return false
	}
	u, err := s.users.GetByID(r.Context(), target)
	if err != nil {
		// Fail closed: if the target cannot be read, it cannot be shown to be
		// safe to act on.
		handleError(w, err)
		return true
	}
	if u.Role != user.RoleAdmin {
		return false
	}
	return denyMachineAdminTakeover(w, r, why)
}

func (s *Server) handleAdminResetPassword(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid user ID")
		return
	}
	var body struct {
		NewPassword string `json:"new_password"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	// The body is decoded and checked before the account is looked at (#419).
	// The machine check below reads the target, so with it first an API key
	// was told 404 or 403 before its password was ever validated, while a
	// session reached the password check first and got a 400.
	if err := user.CheckNewPassword(body.NewPassword); err != nil {
		handleError(w, err)
		return
	}
	if s.denyMachineTargetingAdmin(w, r, id, "reset an administrator's password") {
		return
	}
	actor := authmw.GetActor(r)
	var actorID *uuid.UUID
	if actor != nil {
		actorID = &actor.UserID
	}
	if err := s.users.AdminSetPassword(r.Context(), id, body.NewPassword, actorID); err != nil {
		handleError(w, err)
		return
	}
	// An administrator resetting someone's password is usually responding to a
	// compromise, so any session opened with the old one has to go.
	if err := s.sessions.DeleteForUser(r.Context(), id); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid user ID")
		return
	}
	if s.denyMachineTargetingAdmin(w, r, id, "delete an administrator") {
		return
	}
	// SoftDelete refuses the last active administrator itself: setup does not
	// reopen, so the instance would be left with no way in at all.
	if err := s.users.SoftDelete(r.Context(), id); err != nil {
		handleError(w, err)
		return
	}
	// SoftDelete is an UPDATE, so the sessions table's ON DELETE CASCADE never
	// fires and the deleted user's sessions would outlive them.
	if err := s.sessions.DeleteForUser(r.Context(), id); err != nil {
		handleError(w, err)
		return
	}
	// And their open tickets go back in the queue.
	//
	// The assignee column kept pointing at the soft-deleted row, which no
	// longer appears in the user list — so the ticket showed as "Unassigned"
	// on the page, was NOT in the unassigned queue because the column was not
	// null, and was in nobody's "assigned to me". It sat in the gap between
	// the two lists with nothing to prompt anyone to pick it up. Disabling an
	// account does not have this problem, because a disabled user is still in
	// the list.
	//
	// Not fatal if it fails: the account is already gone and refusing the
	// request now would leave the caller unsure whether it worked. Logged
	// instead, with the count, because an administrator watching a departure
	// wants to know how many tickets just landed back in the queue.
	actor := authmw.GetActor(r)
	var actorID uuid.UUID
	if actor != nil {
		actorID = actor.UserID
	}
	moved, err := s.tickets.UnassignForUser(r.Context(), actorID, id)
	if err != nil {
		slog.ErrorContext(r.Context(), "could not unassign the deleted user's tickets",
			"user_id", id, "error", err)
	} else if moved > 0 {
		slog.InfoContext(r.Context(), "returned the deleted user's open tickets to the queue",
			"user_id", id, "tickets", moved)
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/v1/staff — who work can be given to.
//
// Staff-readable on purpose, and deliberately narrow: an id and a display
// name, which is what assigning a ticket needs and what staff already see on
// every ticket they can read. No email, no role, no login state — that is the
// administrator's view and it stays there.
func (s *Server) handleListAssignableStaff(w http.ResponseWriter, r *http.Request) {
	staff, err := s.users.ListAssignableStaff(r.Context())
	if err != nil {
		handleError(w, err)
		return
	}
	if staff == nil {
		staff = []user.AssignableStaff{}
	}
	JSON(w, http.StatusOK, staff)
}
