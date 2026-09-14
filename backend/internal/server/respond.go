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
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
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
