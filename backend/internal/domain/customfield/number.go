package customfield

import (
	"math"
	"strconv"
	"strings"
)

// IsNumber reports whether s is a value a number field accepts: after leading
// and trailing space is removed, a decimal number that strconv.ParseFloat reads
// without error, and that is not NaN, infinite or hexadecimal.
//
// ParseFloat is broader than a person means by "a number": it reads hexadecimal
// floats (with underscores inside them), "NaN" and "Inf". Nothing in the UI
// produces those, and they would be hard to read in a report. An out-of-range
// value such as 1e400 comes back with an error and is refused too.
//
// Nothing left after trimming is not a number; it is a clear, and the caller
// decides whether a clear is allowed.
//
// The text is stored as written (trimmed), not reformatted: 1.50 stays 1.50.
func IsNumber(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	// ParseFloat reads 1_000 as 1000, not only inside hexadecimal.
	if hasHexPrefix(s) || strings.Contains(s, "_") {
		return false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return false
	}
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}

// hasHexPrefix is 0x, 0X, -0x or +0x, in any case.
func hasHexPrefix(s string) bool {
	if s != "" && (s[0] == '-' || s[0] == '+') {
		s = s[1:]
	}
	return len(s) >= 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X')
}
