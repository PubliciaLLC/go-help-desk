package middleware

import (
	"net/http"
	"time"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
)

// ExpireLegacySession deletes the pre-rename session cookie when a browser
// still presents one.
//
// The Open Help Desk to Go Help Desk rename (#79) renamed the cookie to
// ghd_session and orphaned ohd_session: nothing reads it, but a browser keeps
// sending it for the remainder of its 30-day MaxAge. This clears it on the
// first request that carries one.
//
// The rename first ships in v1.2.0, not v1.1.1 — it landed about two hours
// after v1.1.1 was tagged on the same day, which is a coincidence worth
// spelling out because it is easy to read the dates and conclude otherwise.
// v1.1.1 still sets ohd_session, with the 30-day MaxAge it had before
// lifetimes were shortened to 7.
//
// It only ever deletes. It does not read the value, does not decode it, and
// cannot grant anything — which is what makes handling a legacy credential
// name here safe rather than a compatibility path in the auth chain.
//
// DELETE THIS AFTER 2026-10-15. Thirty days past the v1.2.0 release, the last
// ohd_session any v1.1.1 instance issued has expired on its own, and this
// becomes a middleware on every request that can never fire.
//
// secure must match how the instance is served: a browser on plain HTTP drops
// a Set-Cookie carrying Secure, so setting it unconditionally would stop the
// deletion arriving on exactly the deployments that still have the old cookie.
// It comes from the same auth.SecureCookies(cfg.BaseURL) that decides it for
// the live session cookie, so the two cannot drift apart.
func ExpireLegacySession(secure bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie(auth.LegacySessionName); err == nil {
			http.SetCookie(w, &http.Cookie{
				Name:     auth.LegacySessionName,
				Value:    "",
				Path:     "/",
				MaxAge:   -1,
				Expires:  time.Unix(1, 0), // belt and braces with MaxAge
				Secure:   secure,
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
			})
		}
		next.ServeHTTP(w, r)
	})
}
