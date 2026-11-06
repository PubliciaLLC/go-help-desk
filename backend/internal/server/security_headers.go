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
// The logo route now serves PNG and nothing else — SVG left the uploader in
// #165 step 3, because pattern-matching markup for scripts is a losing game
// and XML character references defeated the first attempt. That removed the
// case this policy was written for; it did not remove the reason to keep it.
//
// What is served here is still bytes an uploader chose, from this origin,
// reachable directly in a browser, and what decides they are a PNG is a
// four-byte magic check rather than a proof. A file that satisfies that check
// and is also valid markup to a sniffing browser is the shape this closes:
// sandbox drops the response into an opaque origin and script-src 'none' stops
// it executing there, without depending on having recognised the content.
// Two header bytes on a cached image, for a class of bug that has already been
// found here once.
const logoCSP = "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:; script-src 'none'"
