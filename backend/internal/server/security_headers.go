package server

import "net/http"

// securityHeaders sets the response headers that limit what a browser will do
// with what this server sends.
//
// None of these were set. The admin pages could be framed by another site and
// clicked through, and a file served with a guessable type could be sniffed
// into something executable.
//
// The policy is strict because the built frontend allows it: one external
// module script, no inline scripts, no eval. 'unsafe-inline' is needed only for
// styles, because Radix and dnd-kit set inline style attributes and so do the
// status colours. data: is needed for images because the TOTP QR code is a data
// URL.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// Set, not add, and only if the handler has not chosen its own — the
		// logo route sets a stricter policy for the file it serves.
		if h.Get("Content-Security-Policy") == "" {
			h.Set("Content-Security-Policy", defaultCSP)
		}
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

const defaultCSP = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; " +
	"font-src 'self'; " +
	"connect-src 'self'; " +
	"frame-ancestors 'none'; " +
	"base-uri 'none'; " +
	"form-action 'self'"

// logoCSP makes an uploaded logo inert whatever it contains.
//
// An SVG is markup, and markup served from this origin runs with this origin's
// cookies if a browser is pointed straight at it. The upload path rejects
// scripts by matching patterns, and pattern matching on markup gets bypassed —
// XML character references defeated the first version. This does not depend on
// catching the content: sandbox drops the file into an opaque origin and
// script-src 'none' stops it executing there either way.
const logoCSP = "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:; script-src 'none'"
