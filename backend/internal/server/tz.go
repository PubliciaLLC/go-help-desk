package server

import (
	"errors"
	"net/http"
)

// maxTZLen is the longest time zone name the dashboard and the reports accept.
// The longest name in the tz database is under 40 bytes.
const maxTZLen = 64

// validTZ reports whether s may be handed to Postgres as a time zone name:
// at most maxTZLen bytes of A-Z a-z 0-9 _ + - /. Postgres says whether the name
// exists; this only keeps junk out of the parameter and the error text.
func validTZ(s string) bool {
	if len(s) > maxTZLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '_', c == '+', c == '-', c == '/':
		default:
			return false
		}
	}
	return true
}

// parseTZ is the tz query parameter of the dashboard (#675) and the reports
// (#131): UTC when absent or empty, otherwise the name as sent if validTZ
// accepts it. The error's text is the 400 message.
func parseTZ(r *http.Request) (string, error) {
	tz := r.URL.Query().Get("tz")
	if tz == "" {
		return "UTC", nil
	}
	if !validTZ(tz) {
		return "", errors.New("invalid tz")
	}
	return tz, nil
}
