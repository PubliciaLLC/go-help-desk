package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// ClientAddr is used only where no account key exists. It must never read a
// forwarding header: that is attacker controlled, and honouring it would let
// one client mint a fresh identity per request.
func TestClientAddr_IgnoresForwardingHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = "10.0.0.1:5555"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.Header.Set("X-Real-IP", "5.6.7.8")
	req.Header.Set("True-Client-IP", "9.9.9.9")

	require.Equal(t, "10.0.0.1", authmw.ClientAddr(req),
		"the transport address, not anything the caller asked us to believe")
}

// The port changes per connection; keying on the full RemoteAddr would hand an
// attacker a fresh budget per request.
func TestClientAddr_StripsThePort(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = "10.0.0.9:1111"
	a := authmw.ClientAddr(req)
	req.RemoteAddr = "10.0.0.9:2222"
	require.Equal(t, a, authmw.ClientAddr(req))
}
