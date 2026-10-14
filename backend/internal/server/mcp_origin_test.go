package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The comparison behind the Origin check on every MCP route (docs/DESIGN.md,
// "MCP Interface" -> Origin). A request with no Origin header passes; one with
// an Origin must carry exactly one value, and it must be the origin of
// BASE_URL.
//
// The cases are the ways a hostile or sloppy value can look like the right one.
// "origin" here means what a browser sends: scheme://host[:port], nothing else.
func TestOriginPolicy_Allows(t *testing.T) {
	const https = "https://helpdesk.example.com"

	cases := []struct {
		name    string
		baseURL string
		origin  []string // nil: no Origin header at all
		want    bool
	}{
		// Absent, and the plain match.
		{"no Origin header", https, nil, true},
		{"exact match", https, []string{"https://helpdesk.example.com"}, true},

		// Case: scheme and host are case-insensitive, and BASE_URL may be
		// written either way.
		{"upper-case scheme and host", https, []string{"HTTPS://HELPDESK.EXAMPLE.COM"}, true},
		{"mixed-case BASE_URL", "HTTPS://Helpdesk.Example.com", []string{"https://helpdesk.example.com"}, true},

		// Default ports are the same origin whether or not they are written.
		{"https default port written", https, []string{"https://helpdesk.example.com:443"}, true},
		{"https default port in BASE_URL only", "https://helpdesk.example.com:443", []string{"https://helpdesk.example.com"}, true},
		{"http default port written", "http://helpdesk.example.com", []string{"http://helpdesk.example.com:80"}, true},
		{"http default port in BASE_URL only", "http://helpdesk.example.com:80", []string{"http://helpdesk.example.com"}, true},
		{"port 80 is not the https default", https, []string{"https://helpdesk.example.com:80"}, false},
		{"port 443 is not the http default", "http://helpdesk.example.com", []string{"http://helpdesk.example.com:443"}, false},
		{"another port", https, []string{"https://helpdesk.example.com:8443"}, false},
		{"port missing where BASE_URL has one", "http://localhost:8080", []string{"http://localhost"}, false},
		{"same port, matching", "http://localhost:8080", []string{"http://localhost:8080"}, true},
		{"different port", "http://localhost:8080", []string{"http://localhost:8081"}, false},
		{"empty port after the colon", "http://localhost:8080", []string{"http://localhost:"}, false},
		{"empty port where the default would match", https, []string{"https://helpdesk.example.com:"}, false},
		{"port out of range", "http://localhost:8080", []string{"http://localhost:65536"}, false},

		// Scheme.
		{"scheme mismatch, http for https", https, []string{"http://helpdesk.example.com"}, false},
		{"scheme mismatch, https for http", "http://helpdesk.example.com", []string{"https://helpdesk.example.com"}, false},
		{"scheme mismatch, same port", "http://localhost:8080", []string{"https://localhost:8080"}, false},
		{"not an http scheme", https, []string{"chrome-extension://helpdesk.example.com"}, false},
		{"file scheme", https, []string{"file://helpdesk.example.com"}, false},

		// An Origin is not a URL. Browsers never send these forms, and a
		// value that needs interpreting is a value to refuse.
		{"trailing slash", https, []string{"https://helpdesk.example.com/"}, false},
		{"a path", https, []string{"https://helpdesk.example.com/mcp"}, false},
		{"a query", https, []string{"https://helpdesk.example.com?x=1"}, false},
		{"a fragment", https, []string{"https://helpdesk.example.com#x"}, false},
		{"leading space", https, []string{" https://helpdesk.example.com"}, false},
		{"trailing space", https, []string{"https://helpdesk.example.com "}, false},

		// Userinfo: the host is whatever follows the @.
		{"userinfo before the right host", https, []string{"https://user@helpdesk.example.com"}, false},
		{"userinfo with password", https, []string{"https://user:pw@helpdesk.example.com"}, false},
		{"right host as userinfo of another", https, []string{"https://helpdesk.example.com@evil.example"}, false},
		{"backslash trick", https, []string{`https://helpdesk.example.com\@evil.example`}, false},
		{"percent-encoded host", https, []string{"https://helpdesk%2eexample.com"}, false},
		{"percent-encoded non-ASCII host", "https://ihelp.example", []string{"https://%C4%B0help.example"}, false},

		// A browser sends the host in ASCII (punycode), so a non-ASCII byte is
		// not an origin. Lower-casing it would be worse than refusing it:
		// Unicode case mapping sends the Kelvin sign to "k" and the dotted
		// capital I to "i", so two different hosts would compare equal.
		{"Kelvin sign for k", "https://kiosk.example", []string{"https://Kiosk.example"}, false},
		{"dotted capital I for i", "https://ihelp.example", []string{"https://İhelp.example"}, false},
		{"punycode is just ASCII", "https://xn--bcher-kva.example", []string{"https://xn--bcher-kva.example"}, true},

		// Other hosts that look close.
		{"foreign host", https, []string{"https://evil.example"}, false},
		{"subdomain", https, []string{"https://a.helpdesk.example.com"}, false},
		{"parent domain", https, []string{"https://example.com"}, false},
		{"suffix attack", https, []string{"https://helpdesk.example.com.evil.example"}, false},
		{"prefix attack", https, []string{"https://evilhelpdesk.example.com"}, false},
		{"trailing dot is another origin to a browser", https, []string{"https://helpdesk.example.com."}, false},
		{"loopback is not localhost", "http://localhost:8080", []string{"http://127.0.0.1:8080"}, false},

		// The values that are not origins at all.
		{"null", https, []string{"null"}, false},
		{"empty value", https, []string{""}, false},
		{"no scheme", https, []string{"helpdesk.example.com"}, false},
		{"scheme only", https, []string{"https://"}, false},
		{"garbage", https, []string{"%%%"}, false},

		// More than one Origin header is never what a browser sends; it is
		// refused even when every copy matches.
		{"two matching", https, []string{"https://helpdesk.example.com", "https://helpdesk.example.com"}, false},
		{"matching then foreign", https, []string{"https://helpdesk.example.com", "https://evil.example"}, false},
		{"foreign then matching", https, []string{"https://evil.example", "https://helpdesk.example.com"}, false},

		// BASE_URL is a URL, not an origin: its path and trailing slash do
		// not make a different origin.
		{"BASE_URL with trailing slash", "https://helpdesk.example.com/", []string{"https://helpdesk.example.com"}, true},
		{"BASE_URL under a path", "https://helpdesk.example.com/support/", []string{"https://helpdesk.example.com"}, true},

		// IPv6, which url.Parse brackets and a naive host:port split mangles.
		{"ipv6 match", "http://[::1]:8080", []string{"http://[::1]:8080"}, true},
		{"ipv6 spelled out", "http://[::1]:8080", []string{"http://[0:0:0:0:0:0:0:1]:8080"}, true},
		{"ipv6 upper case", "http://[2001:db8::1]:8080", []string{"http://[2001:DB8::1]:8080"}, true},
		{"ipv6 default port written", "https://[2001:db8::1]", []string{"https://[2001:db8::1]:443"}, true},
		{"ipv6 port missing", "http://[::1]:8080", []string{"http://[::1]"}, false},
		{"ipv6 other port", "http://[::1]:8080", []string{"http://[::1]:8081"}, false},
		{"ipv6 other address", "http://[::1]:8080", []string{"http://[::2]:8080"}, false},
		{"ipv6 without brackets", "http://[::1]:8080", []string{"http://::1:8080"}, false},
		{"ipv6 zone", "http://[::1]:8080", []string{"http://[::1%25eth0]:8080"}, false},

		// A BASE_URL with no usable origin. Startup refuses one with no host
		// (webauthn.NewService), but a non-http scheme or an out-of-range
		// port starts; the check fails closed rather than guessing. No
		// Origin still passes: non-browser clients send none.
		{"empty BASE_URL, no Origin", "", nil, true},
		{"empty BASE_URL, an Origin", "", []string{"https://helpdesk.example.com"}, false},
		{"empty BASE_URL, null", "", []string{"null"}, false},
		{"BASE_URL without a scheme, an Origin", "helpdesk.example.com", []string{"https://helpdesk.example.com"}, false},
		{"BASE_URL without a host, an Origin", "https://", []string{"https://"}, false},
		{"unparsable BASE_URL, an Origin", "https://%zz", []string{"https://%zz"}, false},
		{"BASE_URL not http(s), an Origin", "ftp://helpdesk.example.com", []string{"ftp://helpdesk.example.com"}, false},
		{"BASE_URL not http(s) with a port, an Origin", "ws://localhost:8080", []string{"ws://localhost:8080"}, false},
		{"unusable BASE_URL, no Origin", "not a url", nil, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			for _, v := range tc.origin {
				h.Add("Origin", v)
			}
			if got := newOriginPolicy(tc.baseURL).allows(h); got != tc.want {
				t.Errorf("BASE_URL %q, Origin %q: allows = %v, want %v", tc.baseURL, tc.origin, got, tc.want)
			}
		})
	}
}

// The only thing the check compares an Origin with is BASE_URL. Not the Host
// header, and not X-Forwarded-*: a DNS-rebound page reaches the server under
// the attacker's name, so its Host and its Origin agree with each other, and
// "Origin matches Host" is the very check rebinding defeats. The same rule is
// what makes a TLS-terminating proxy work: the browser's https Origin meets an
// app that sees plain http on an internal address, and still matches.
func TestRequireMCPOrigin_ComparesWithBaseURLNotTheRequest(t *testing.T) {
	const baseURL = "https://helpdesk.example.com"

	cases := []struct {
		name   string
		host   string
		origin string
		fwd    map[string]string
		want   int
	}{
		{
			name:   "rebinding: Origin agrees with Host, not with BASE_URL",
			host:   "evil.example:8080",
			origin: "http://evil.example:8080",
			want:   http.StatusForbidden,
		},
		{
			name:   "rebinding with forwarded headers that agree too",
			host:   "evil.example",
			origin: "https://evil.example",
			fwd:    map[string]string{"X-Forwarded-Host": "evil.example", "X-Forwarded-Proto": "https", "Forwarded": "host=evil.example;proto=https"},
			want:   http.StatusForbidden,
		},
		{
			name:   "behind a TLS-terminating proxy: plain http on an internal address",
			host:   "10.0.0.5:8080",
			origin: "https://helpdesk.example.com",
			fwd:    map[string]string{"X-Forwarded-Proto": "http", "X-Forwarded-Host": "10.0.0.5:8080"},
			want:   http.StatusTeapot,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := requireMCPOrigin(baseURL, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusTeapot) // reached the next handler
			}))
			req := httptest.NewRequest(http.MethodPost, "http://"+tc.host+"/mcp", nil)
			req.Header.Set("Origin", tc.origin)
			for k, v := range tc.fwd {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("Host %q, Origin %q: status %d, want %d", tc.host, tc.origin, rec.Code, tc.want)
			}
		})
	}
}
