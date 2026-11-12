package middleware

import (
	"net"
	"net/http"
)

// ClientAddr is the transport address, with the port stripped.
//
// Only for endpoints where no account key exists yet — signup. It is
// deliberately NOT read from X-Forwarded-For: that header is attacker
// controlled unless a trusted proxy is known to rewrite it, and honouring it
// would let one client mint a fresh identity per request.
func ClientAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
