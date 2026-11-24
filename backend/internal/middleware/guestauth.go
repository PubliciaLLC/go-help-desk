package middleware

import (
	"context"
	"net/http"
	"strings"
)

// guestTicketKey carries the ticket a guest token resolved to.
//
// Its own context key, deliberately not the actor key: a guest is not an
// actor. Nothing that reads GetActor can be fooled into treating a guest as a
// signed-in user, because a guest request never sets one.
type guestTicketCtxKey struct{}

// GuestTicketID returns the ticket a guest token named, and false when the
// request carries no guest authority. Handlers outside the guest router always
// get false.
func GuestTicketID(r *http.Request) (string, bool) {
	v, ok := r.Context().Value(guestTicketCtxKey{}).(string)
	return v, ok
}

// GuestTokenResolver turns a raw token into the id of the one ticket it names.
// It returns an error for every reason a token does not resolve, and the caller
// cannot tell them apart.
type GuestTokenResolver func(ctx context.Context, raw string) (ticketID string, err error)

// GuestAuth admits a request carrying a valid per-ticket guest token.
//
// The scheme is "Guest", not "Bearer": a guest token is not an OAuth token and
// must not be accepted by anything that takes one. Sending it under Bearer
// would put it in front of BearerAuth, which decodes JWTs — and a credential
// that two middlewares both feel entitled to read is a credential whose rules
// nobody owns.
//
// Every failure is 404 with an identical body. Not 401, which would invite a
// retry, and not 403, which would confirm the token was real and only lacked
// permission. Expired, rotated away, never issued, and naming a closed ticket
// are one answer, because distinguishing them tells whoever is guessing that
// they guessed something that once existed.
func GuestAuth(resolve GuestTokenResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			const prefix = "Guest "
			h := r.Header.Get("Authorization")
			if !strings.HasPrefix(h, prefix) {
				guestNotFound(w)
				return
			}
			raw := strings.TrimSpace(strings.TrimPrefix(h, prefix))
			if raw == "" {
				guestNotFound(w)
				return
			}
			ticketID, err := resolve(r.Context(), raw)
			if err != nil {
				guestNotFound(w)
				return
			}
			ctx := context.WithValue(r.Context(), guestTicketCtxKey{}, ticketID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// guestNotFound is the single refusal. Byte-identical for every cause, so the
// response cannot be read as an oracle.
func guestNotFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	// The trailing newline matters: the handlers' own 404 goes through
	// json.Encoder, which appends one. Without it two refusals that are meant
	// to be indistinguishable differ by a byte.
	_, _ = w.Write([]byte("{\"error\":{\"code\":\"not_found\",\"message\":\"not found\"}}\n"))
}
