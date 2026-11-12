package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/cannedresponse"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/plugin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/registration"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/tag"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/ratelimit"
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
	// user.ErrNotFound as well as userstore.ErrNotFound: the store sentinel
	// WRAPS the domain one, so errors.Is does not run the other way, and a
	// domain method returning the bare sentinel fell through to 500.
	if errors.Is(err, userstore.ErrNotFound) || errors.Is(err, user.ErrNotFound) || errors.Is(err, ticketstore.ErrNotFound) || errors.Is(err, cannedresponse.ErrNotFound) || errors.Is(err, tag.ErrNotFound) || errors.Is(err, ticket.ErrStatusNotFound) || errors.Is(err, group.ErrNotFound) || errors.Is(err, auth.ErrNotFound) || errors.Is(err, plugin.ErrNotFound) || errors.Is(err, category.ErrNotFound) || errors.Is(err, customfield.ErrNotFound) || errors.Is(err, sla.ErrNotFound) {
		Error(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	// 409: the account's nature refuses the change, whoever asks. A service
	// account (an OAuth client's identity) has no password, no second factor
	// and no admin role, ever (#420).
	if errors.Is(err, user.ErrServiceAccount) {
		Error(w, http.StatusConflict, "service_account", err.Error())
		return
	}
	// The caller sent a value that is not one. Not a server fault, and it
	// used to fall through to 500 "an internal error occurred" — which
	// tells somebody their own ordinary mistake is a bug here.
	if errors.Is(err, ticket.ErrReplyTooLong) {
		Error(w, http.StatusBadRequest, "body_too_long", err.Error())
		return
	}
	if errors.Is(err, ticket.ErrInvalidTextFormat) {
		Error(w, http.StatusBadRequest, "invalid_format", err.Error())
		return
	}
	if errors.Is(err, ticket.ErrValidation) {
		Error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	// 409, like ErrClosed: the ticket's state refuses a follow-up, not the
	// caller's permissions. A bare error fell through to 500 here once (#277,
	// when this refused a reopen of a ticket that was not closed).
	if errors.Is(err, ticket.ErrNotClosed) {
		Error(w, http.StatusConflict, "ticket_not_closed", "this applies only to a closed ticket")
		return
	}
	// Not a permission problem: the caller may well own this ticket. The
	// ticket is in a state that does not accept the change, which is what
	// 409 is for. It fell through to 500 for the same reason ErrForbidden
	// did.
	// A requester's second follow-up of one closed ticket (#349). 409: the
	// ticket's state, and they already know about the first.
	if errors.Is(err, ticket.ErrFollowUpExists) {
		Error(w, http.StatusConflict, "follow_up_exists", "this ticket already has a follow-up")
		return
	}
	// A staff follow-up of a ticket whose reporting account was deleted (#625).
	// 409, not 400: nothing the caller sent is wrong, and the answer is to
	// restore the account or start a new ticket.
	if errors.Is(err, ticket.ErrReporterDeleted) {
		Error(w, http.StatusConflict, "reporter_deleted",
			"the account that reported this ticket has been deleted: restore the account, or open a new ticket")
		return
	}
	// A force-reopen refused by closed_reopen_policy keeps the status and code,
	// and says why (#349): disabled, or restricted to administrators.
	var reopenRefused *ticket.ReopenRefusedError
	if errors.As(err, &reopenRefused) {
		Error(w, http.StatusConflict, "ticket_closed", reopenRefused.Error())
		return
	}
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
	// Custom fields (#419). The messages on these were written for the
	// caller by the service and the store, never taken from the database.
	if errors.Is(err, customfield.ErrValidation) {
		Error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if errors.Is(err, customfield.ErrConflict) {
		Error(w, http.StatusConflict, "conflict", err.Error())
		return
	}
	if errors.Is(err, customfield.ErrInvalidReference) {
		Error(w, http.StatusBadRequest, "invalid_reference", err.Error())
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
	// 409, not 500: the same sla.ErrPolicyInUse shape, one layer over —
	// RemoveStatus refuses to hard-delete a custom status that a ticket
	// currently holds or that a past ticket_status_history entry references,
	// and the message carries the ticket or transition count (#275).
	if errors.Is(err, ticket.ErrStatusInUse) {
		Error(w, http.StatusConflict, "status_in_use", err.Error())
		return
	}
	// 409: the status has been retired. Hidden from the picker, and refused
	// here for every role so an old tab or an API client cannot walk around
	// the picker.
	if errors.Is(err, ticket.ErrStatusInactive) {
		Error(w, http.StatusConflict, "status_inactive", err.Error())
		return
	}
	// 409, not 500: statuses.name is TEXT NOT NULL UNIQUE, and creating or
	// renaming a status to a name that already exists is the schema working
	// as designed, not a server fault. See #278.
	if errors.Is(err, ticket.ErrStatusNameTaken) {
		Error(w, http.StatusConflict, "status_name_taken", err.Error())
		return
	}
	// 403, not 500: refusing to rename or delete a system status is the
	// domain layer working as designed, matching the sla.ErrPolicyInUse
	// pattern above for a refusal that used to fall through to 500 one
	// layer down from its HTTP-handler check. See #269.
	if errors.Is(err, ticket.ErrSystemStatusImmutable) {
		Error(w, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	// 400: group member or scope reference does not exist.
	if errors.Is(err, group.ErrInvalidReference) {
		Error(w, http.StatusBadRequest, "invalid_reference", err.Error())
		return
	}
	// 409, not 500: group.name is TEXT NOT NULL UNIQUE, and creating or
	// renaming a group to a name that already exists is the schema working
	// as designed, not a server fault.
	if errors.Is(err, group.ErrNameTaken) {
		Error(w, http.StatusConflict, "group_name_taken", err.Error())
		return
	}
	// 409: category, type, or item is referenced by tickets or SLA records.
	if errors.Is(err, category.ErrCategoryInUse) {
		Error(w, http.StatusConflict, "category_in_use", err.Error())
		return
	}
	if errors.Is(err, category.ErrTypeInUse) {
		Error(w, http.StatusConflict, "type_in_use", err.Error())
		return
	}
	if errors.Is(err, category.ErrItemInUse) {
		Error(w, http.StatusConflict, "item_in_use", err.Error())
		return
	}
	// Checked before the general validation arm below, which it is a kind of:
	// a taken address is a conflict, and saying so lets the admin form tell
	// the difference between "that is not an address" and "somebody already
	// has it".
	if errors.Is(err, user.ErrEmailTaken) {
		Error(w, http.StatusConflict, "email_taken", err.Error())
		return
	}
	// Bad input, not a fault. Without this a mistyped email address at signup,
	// or on an admin's user edit, came back as 500 "an internal error
	// occurred" and was logged as one.
	//
	// sla.ErrValidation and sla.ErrUnknownCategory are added for the SLA
	// policy create/update doors (#276): every store or validation error used
	// to be reported as a raw-text 400 regardless of what actually went
	// wrong. ticket.ErrValidation is caught by its own arm above.
	if errors.Is(err, user.ErrValidation) || errors.Is(err, registration.ErrInvalidEmail) ||
		errors.Is(err, ticket.ErrInvalidLinkType) || errors.Is(err, ticket.ErrInvalidStatusName) ||
		errors.Is(err, sla.ErrValidation) || errors.Is(err, sla.ErrUnknownCategory) || errors.Is(err, group.ErrValidation) || errors.Is(err, category.ErrValidation) {
		Error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	// 400, not 500: a self-link is bad input, not a fault. See #192.
	if errors.Is(err, ticket.ErrSelfLink) {
		Error(w, http.StatusBadRequest, "cannot_link_self", ticket.ErrSelfLink.Error())
		return
	}
	// A refused permission is an ordinary, correct outcome. Falling through to
	// 500 told the caller "an internal error occurred" for a boundary working
	// exactly as designed, and buried a real authorisation event in the error
	// log where it reads as a server bug.
	if errors.Is(err, ticket.ErrForbidden) {
		Error(w, http.StatusForbidden, "forbidden", "you do not have permission to perform this action")
		return
	}
	slog.Error("internal error", "error", err)
	Error(w, http.StatusInternalServerError, "internal_error", "an internal error occurred")
}

// allowOrRefuse counts one attempt against key and, when the caller must not
// proceed, writes the answer and returns false: 429 with the Retry-After of the
// key's own window, or an error when the count could not be taken. The limit
// fails closed on a database error (#621): it guards mail to strangers and
// guesses at passwords, so "could not check" is not "go ahead".
func allowOrRefuse(ctx context.Context, w http.ResponseWriter, l *ratelimit.Limiter, key string) bool {
	t, err := l.Take(ctx, key)
	if err != nil {
		handleError(w, err)
		return false
	}
	if !t.Allowed {
		tooManyAttempts(w, t.RetryAfter)
		return false
	}
	return true
}

// allowQuietly is allowOrRefuse for a caller with no response to write. A
// count that could not be taken is logged here, at the boundary, and refuses.
func allowQuietly(ctx context.Context, l *ratelimit.Limiter, key string) bool {
	ok, err := l.Allow(ctx, key)
	if err != nil {
		slog.ErrorContext(ctx, "rate limit could not be checked; refusing", "error", err)
		return false
	}
	return ok
}

// tarpitIfExceeded slows a key that has spent its budget before the caller
// goes on to check a password. A count that cannot be read is logged and the
// attempt is not slowed: this is a delay and not a refusal, and refusing here
// would turn a database fault into a way to keep somebody out of their own
// account. The wrong guess that follows still has to be counted, and that
// refuses (allowOrRefuse).
func (s *Server) tarpitIfExceeded(ctx context.Context, key string) {
	over, err := s.loginLimiter.Exceeded(ctx, key)
	if err != nil {
		slog.ErrorContext(ctx, "attempt count could not be read; not slowing this attempt", "error", err)
		return
	}
	if over {
		s.loginLimiter.Tarpit(ctx, key, s.loginThrottleDelay)
	}
}

// resetLoginLimit clears a key's budget after a correct password. A failure is
// logged and does not fail the sign-in: the person has proved who they are, and
// what is left is a count that expires on its own within the window.
func (s *Server) resetLoginLimit(ctx context.Context, key string) {
	if err := s.loginLimiter.Reset(ctx, key); err != nil {
		slog.ErrorContext(ctx, "attempt count could not be cleared", "error", err)
	}
}

// tooManyAttempts is the refusal for a spent credential budget.
//
// Deliberately the same message for every throttled endpoint: saying which
// budget was spent, or how much remains, tells an attacker whether an account
// exists and how close they are.
func tooManyAttempts(w http.ResponseWriter, retryAfter time.Duration) {
	// The header has to match the limit that actually refused: for a window it
	// is the time left in the refused key's own window (Taken.RetryAfter), for
	// the MFA lock the lock's length, and telling a locked-out user to come back
	// in sixty seconds is simply wrong. Whole seconds, rounded up so waiting that
	// long is enough, and never 0, which a client reads as "retry now".
	secs := max(1, int(math.Ceil(retryAfter.Seconds())))
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	Error(w, http.StatusTooManyRequests, "rate_limited",
		"too many attempts; please wait and try again")
}
