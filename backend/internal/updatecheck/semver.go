package updatecheck

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// version is a parsed semantic version (semver.org). Build metadata is
// dropped: it takes no part in precedence.
type version struct {
	core [3]uint64
	pre  []string
}

func parse(s string) (version, error) {
	orig := s
	s = strings.TrimPrefix(s, "v")
	s, _, _ = strings.Cut(s, "+")
	s, pre, hasPre := strings.Cut(s, "-")

	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return version{}, fmt.Errorf("%q is not a major.minor.patch version", orig)
	}
	var v version
	for i, p := range parts {
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil || (len(p) > 1 && p[0] == '0') {
			return version{}, fmt.Errorf("%q is not a major.minor.patch version", orig)
		}
		v.core[i] = n
	}
	if hasPre {
		v.pre = strings.Split(pre, ".")
		for _, id := range v.pre {
			if id == "" {
				return version{}, fmt.Errorf("%q has an empty prerelease identifier", orig)
			}
		}
	}
	return v, nil
}

// cmp returns -1, 0 or 1 by semver precedence.
func (a version) cmp(b version) int {
	for i := range a.core {
		if a.core[i] != b.core[i] {
			if a.core[i] < b.core[i] {
				return -1
			}
			return 1
		}
	}
	// A release outranks any prerelease of the same core.
	switch {
	case len(a.pre) == 0 && len(b.pre) == 0:
		return 0
	case len(a.pre) == 0:
		return 1
	case len(b.pre) == 0:
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		if c := cmpIdent(a.pre[i], b.pre[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(a.pre) < len(b.pre):
		return -1
	case len(a.pre) > len(b.pre):
		return 1
	}
	return 0
}

// cmpIdent orders prerelease identifiers: numeric ones numerically and below
// any alphanumeric one, alphanumeric ones in ASCII order.
func cmpIdent(a, b string) int {
	an, aErr := strconv.ParseUint(a, 10, 64)
	bn, bErr := strconv.ParseUint(b, 10, 64)
	switch {
	case aErr == nil && bErr == nil:
		if an < bn {
			return -1
		}
		if an > bn {
			return 1
		}
		return 0
	case aErr == nil:
		return -1
	case bErr == nil:
		return 1
	}
	return strings.Compare(a, b)
}

// tagPattern is the only tag shape a release can have for this feature: an
// optional v, then three numbers with no leading zeros. No build metadata and
// no prerelease, so nothing outside digits and dots survives parsing.
var tagPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)

// Canonical returns "MAJOR.MINOR.PATCH", rebuilt from the parsed numbers, for a
// tag of exactly the shape v?MAJOR.MINOR.PATCH and an error for anything else.
//
// What a release tag looks like is decided by whoever controls the response,
// so nothing of the raw text may reach storage or a link: a tag such as
// "v99.0.0+/../../attacker/repo" is refused here, and what is kept is built from
// numbers.
func Canonical(tag string) (string, error) {
	m := tagPattern.FindStringSubmatch(tag)
	if m == nil {
		return "", fmt.Errorf("%q is not a release version", tag)
	}
	var n [3]int
	for i := range n {
		n[i], _ = strconv.Atoi(m[i+1]) // at most 9 digits: cannot overflow
	}
	return fmt.Sprintf("%d.%d.%d", n[0], n[1], n[2]), nil
}

// Newer reports whether latest is a higher version than current. Either one
// that is not a semantic version is an error rather than "not newer": a
// running version of "dev" should not silently disable the check's answer.
func Newer(current, latest string) (bool, error) {
	c, err := parse(current)
	if err != nil {
		return false, fmt.Errorf("current version: %w", err)
	}
	l, err := parse(latest)
	if err != nil {
		return false, fmt.Errorf("latest version: %w", err)
	}
	return l.cmp(c) > 0, nil
}
