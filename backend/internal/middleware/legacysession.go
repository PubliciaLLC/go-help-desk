package middleware

import (
	"net/http"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
)

// ExpireLegacySession deletes the pre-rename session cookie when a browser
// still presents one.
//
// Renaming the cookie in the GHD rename release orphaned ohd_session: nothing
// reads it, but the
// browser keeps sending it for the remainder of its 30-day MaxAge. This clears
// it on the first request that carries one.
//
// It only ever deletes. It does not read the value, does not decode it, and
// cannot grant anything — which is what makes handling a legacy credential name
// here safe rather than a compatibility path in the auth chain.
//
// Remove once instances have had a release cycle to upgrade: 30 days after the
// rename release, no live browser can still hold one.
func ExpireLegacySession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie(auth.LegacySessionName); err == nil {
			http.SetCookie(w, &http.Cookie{
				Name:     auth.LegacySessionName,
				Value:    "",
				Path:     "/",
				MaxAge:   -1,
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
			})
		}
		next.ServeHTTP(w, r)
	})
}
