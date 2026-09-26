package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/cannedresponse"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/registration"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// JSON writes v as JSON with the given status code.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// errResponse is the standard error envelope returned by the API.
type errResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Error writes a JSON error response.
func Error(w http.ResponseWriter, status int, code, message string) {
	var body errResponse
	body.Error.Code = code
	body.Error.Message = message
	JSON(w, status, body)
}

// DecodeJSON reads and decodes JSON from r.Body into dst.
// maxJSONBody bounds a decoded request body.
//
// Nothing capped this, so any unauthenticated endpoint read an arbitrarily
// large body into memory. It surfaced as the login rate limiter retaining
// megabyte-sized keys — live heap became a function of inbound bandwidth
// rather than of account count — but the limit belongs here. File uploads go
// through their own multipart path and are unaffected.
const maxJSONBody = 1 << 20 // 1 MiB

func DecodeJSON(r *http.Request, dst any) error {
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxJSONBody)).Decode(dst)
}

// handleError maps common sentinel errors to HTTP status codes.
func handleError(w http.ResponseWriter, err error) {
	if errors.Is(err, userstore.ErrNotFound) || errors.Is(err, ticketstore.ErrNotFound) || errors.Is(err, cannedresponse.ErrNotFound) {
		Error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	// A refused permission is an ordinary, correct outcome. Falling through to
	// 500 told the caller "an internal error occurred" for a boundary working
	// exactly as designed, and buried a real authorisation event in the error
	// log where it reads as a server bug.
	// Not a permission problem: the caller may well own this ticket. The ticket
	// is in a state that does not accept the change, which is what 409 is for.
	// It fell through to 500 for the same reason ErrForbidden did.
	if errors.Is(err, ticket.ErrClosed) {
		Error(w, http.StatusConflict, "ticket_closed", "this ticket is closed")
		return
	}
	// Also 409 rather than 403: nothing about the caller's permissions would
	// change the answer, so a message about permission would send them to an
	// administrator who cannot help.
	if errors.Is(err, ticket.ErrReopenWindowClosed) {
		Error(w, http.StatusConflict, "reopen_window_closed", ticket.ErrReopenWindowClosed.Error())
		return
	}
	// 409 Conflict: link already exists
	if errors.Is(err, ticket.ErrLinkAlreadyExists) {
		Error(w, http.StatusConflict, "link_already_exists", "this link already exists")
		return
	}
	// 409, not 500: a policy some ticket's SLA record is measured against is
	// refused by the schema (ON DELETE RESTRICT), which is the database working
	// as designed. The message carries the ticket count (#261).
	if errors.Is(err, sla.ErrPolicyInUse) {
		Error(w, http.StatusConflict, "policy_in_use", err.Error())
		return
	}
	// 403, not 500: refusing to rename or delete a system status is the
	// domain layer working as designed, matching the sla.ErrPolicyInUse
	// pattern just above for a refusal that used to fall through to 500 one
	// layer down from its HTTP-handler check. See #269.
	if errors.Is(err, ticket.ErrSystemStatusImmutable) {
		Error(w, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	// Bad input, not a fault. Without this a mistyped email address at signup,
	// or on an admin's user edit, came back as 500 "an internal error
	// occurred" and was logged as one.
	if errors.Is(err, user.ErrValidation) || errors.Is(err, registration.ErrInvalidEmail) || errors.Is(err, ticket.ErrInvalidLinkType) {
		Error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	// 400, not 500: a self-link is bad input, not a fault. See #192.
	if errors.Is(err, ticket.ErrSelfLink) {
		Error(w, http.StatusBadRequest, "cannot_link_self", ticket.ErrSelfLink.Error())
		return
	}
	if errors.Is(err, ticket.ErrForbidden) {
		Error(w, http.StatusForbidden, "forbidden", "you do not have permission to perform this action")
		return
	}
	slog.Error("internal error", "error", err)
	Error(w, http.StatusInternalServerError, "internal_error", "an internal error occurred")
}

// tooManyAttempts is the refusal for a spent credential budget.
//
// Deliberately the same message for every throttled endpoint: saying which
// budget was spent, or how much remains, tells an attacker whether an account
// exists and how close they are.
func tooManyAttempts(w http.ResponseWriter, retryAfter time.Duration) {
	// The header has to match the limit that actually refused: the login
	// budget is a minute, the MFA lock is fifteen, and telling a locked-out
	// user to come back in sixty seconds is simply wrong.
	w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
	Error(w, http.StatusTooManyRequests, "rate_limited",
		"too many attempts; please wait and try again")
}
