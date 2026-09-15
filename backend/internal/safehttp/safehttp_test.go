package safehttp

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

// The addresses a webhook must never reach. The app container can talk to the
// database, the antivirus daemon, the Docker host and cloud metadata, none of
// which are reachable from outside — so an operator-supplied URL is a request
// forgery primitive without this.
func TestCheckIP_RefusesInternalAddresses(t *testing.T) {
	for _, tc := range []struct{ name, ip string }{
		{"IPv4 loopback", "127.0.0.1"},
		{"IPv4 loopback, other host", "127.1.2.3"},
		{"IPv6 loopback", "::1"},
		{"RFC1918 10/8", "10.0.0.5"},
		{"RFC1918 172.16/12", "172.17.0.2"},
		{"RFC1918 192.168/16", "192.168.1.10"},
		{"cloud metadata", "169.254.169.254"},
		{"link-local v6", "fe80::1"},
		{"unique-local v6", "fd00::1"},
		{"unspecified", "0.0.0.0"},
		{"multicast", "224.0.0.1"},
		// Not "private" by Go's definition, but internal in practice.
		{"carrier-grade NAT", "100.64.0.1"},
		{"Alibaba/Tencent metadata", "100.100.100.200"},
		{"IETF protocol assignments", "192.0.0.1"},
		{"benchmarking range", "198.18.0.1"},
		{"reserved 240/4", "240.0.0.1"},
		{"NAT64 mapping loopback", "64:ff9b::7f00:1"},
		// An IPv4 address wrapped as IPv6 is 16 bytes, and every check above
		// misses it unless it is unwrapped first.
		{"IPv4-mapped loopback", "::ffff:127.0.0.1"},
		{"IPv4-mapped private", "::ffff:10.0.0.5"},
		{"IPv4-mapped metadata", "::ffff:169.254.169.254"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			require.NotNil(t, ip, "bad test fixture %q", tc.ip)
			require.Error(t, checkIP(ip), "%s must be refused", tc.ip)
		})
	}
}

// A guard that refused everything would pass the test above.
func TestCheckIP_AllowsPublicAddresses(t *testing.T) {
	for _, ip := range []string{"93.184.216.34", "8.8.8.8", "2606:2800:220:1:248:1893:25c8:1946"} {
		require.NoError(t, checkIP(net.ParseIP(ip)), "%s must be allowed", ip)
	}
}

func TestValidateURL(t *testing.T) {
	require.Error(t, ValidateURL("http://127.0.0.1/hook"), "loopback must be refused")
	require.Error(t, ValidateURL("http://169.254.169.254/latest/meta-data/"),
		"the metadata endpoint must be refused")
	require.Error(t, ValidateURL("file:///etc/passwd"), "only http and https")
	require.Error(t, ValidateURL("gopher://example.com"), "only http and https")
	require.Error(t, ValidateURL("http://"), "a URL needs a host")
	require.Error(t, ValidateURL("://nonsense"), "unparseable")
}

// The dialer is the boundary, not ValidateURL: a name that passes validation
// can resolve elsewhere by the time it is used, and a redirect never goes
// through validation at all.
func TestGuardedDial_RefusesLoopback(t *testing.T) {
	srv, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = srv.Close() }()

	_, err = guardedDial(t.Context(), "tcp", srv.Addr().String())
	require.Error(t, err, "the dialer must refuse loopback even for a live listener")
	require.Contains(t, err.Error(), "loopback")
}
