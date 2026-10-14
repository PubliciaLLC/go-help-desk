package server

import (
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// originPolicy decides whether a request's Origin header names this instance.
//
// want is the canonical origin of BASE_URL ("scheme://host:port", the port
// always written), or "" when BASE_URL has no usable origin. In that case every
// request that carries an Origin is refused: the check fails closed, as
// webauthn.NewService does at startup for the same setting, rather than
// guessing what the operator meant. Production never gets there, because it
// refuses to start on such a BASE_URL; only a hand-built Config does.
type originPolicy struct{ want string }

func newOriginPolicy(baseURL string) originPolicy {
	u, err := url.Parse(baseURL)
	if err != nil {
		return originPolicy{}
	}
	// BASE_URL is a URL, so its path, query and any userinfo are not part of
	// the origin. Rebuilding from scheme and host is what webauthn.NewService
	// does for the relying-party origin.
	want, _ := canonicalOrigin(u.Scheme + "://" + u.Host)
	return originPolicy{want: want}
}

// allows reports whether a request with these headers may reach /mcp.
//
// No Origin header passes: only browsers send one on every cross-origin request,
// and a non-browser client has no reason to. A header that is present must be
// the only one, and must be exactly the origin of BASE_URL.
func (p originPolicy) allows(h http.Header) bool {
	vals, present := h["Origin"]
	if !present {
		return true
	}
	if len(vals) != 1 || p.want == "" {
		return false
	}
	got, ok := canonicalOrigin(vals[0])
	return ok && got == p.want
}

// canonicalOrigin turns a serialized origin into one comparable string:
// lower-case, bracketed IPv6 in its shortest form, and the port always
// present. It accepts what a browser sends (scheme://host[:port]) and nothing
// else, so "null", a trailing slash, a path, userinfo and a percent-encoded
// host are all refused rather than interpreted.
func canonicalOrigin(raw string) (string, bool) {
	scheme, authority, found := strings.Cut(raw, "://")
	scheme = strings.ToLower(scheme)
	if !found || (scheme != "http" && scheme != "https") {
		return "", false
	}
	if authority == "" || strings.ContainsAny(authority, "/?#@%\\ \t") || strings.HasSuffix(authority, ":") {
		return "", false
	}
	u, err := url.Parse(scheme + "://" + authority)
	if err != nil || u.Hostname() == "" {
		return "", false
	}

	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[scheme]
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil || n == 0 {
		return "", false
	}
	port = strconv.FormatUint(n, 10)

	host := strings.ToLower(u.Hostname())
	if strings.Contains(authority, "[") {
		// Bracketed means IPv6. Anything else in the brackets, or an address
		// that is not one, is not an origin.
		addr, err := netip.ParseAddr(host)
		if err != nil || !addr.Is6() {
			return "", false
		}
		host = addr.String()
	}
	return scheme + "://" + net.JoinHostPort(host, port), true
}
