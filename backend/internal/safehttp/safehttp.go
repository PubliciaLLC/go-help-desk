// Package safehttp makes outbound requests to addresses an operator supplies
// without handing the caller a way to reach the network the server sits on.
//
// Webhook targets, SAML metadata URLs and the OIDC issuer are all fetched by
// the server from a URL someone typed into an admin form. Without a guard that
// is a request forgery primitive: the app container can reach the database, the
// antivirus daemon, the Docker host and cloud metadata endpoints, none of which
// are reachable from outside.
//
// The check lives in the dialer rather than in URL parsing on purpose. Checking
// the string catches "http://127.0.0.1" and nothing else: a DNS name that
// resolves to a private address defeats it, so does a redirect to one, and so
// does a name that resolves differently on the second lookup than the first
// (DNS rebinding). Vetting the IP at the moment of connection catches all
// three, because every one of them has to dial eventually.
package safehttp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

// Client returns an HTTP client that refuses to connect to private, loopback,
// link-local or otherwise internal addresses.
func Client(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:           guardedDial,
			TLSHandshakeTimeout:   timeout,
			ResponseHeaderTimeout: timeout,
		},
	}
}

func guardedDial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("parsing address %q: %w", addr, err)
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolving %q: %w", host, err)
	}
	for _, ip := range ips {
		if err := checkIP(ip.IP); err != nil {
			return nil, err
		}
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("%q resolved to no addresses", host)
	}
	// Dial the address that was vetted, not the name. Re-resolving here is what
	// would leave a rebinding window open.
	d := net.Dialer{Timeout: 10 * time.Second}
	return d.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
}

// extraInternalRanges are internal in practice but not covered by IsPrivate.
var extraInternalRanges = func() []*net.IPNet {
	cidrs := []string{
		"100.64.0.0/10", // carrier-grade NAT; Alibaba/Tencent metadata lives here
		"192.0.0.0/24",  // IETF protocol assignments
		"198.18.0.0/15", // benchmarking
		"240.0.0.0/4",   // reserved
		"64:ff9b::/96",  // NAT64, which maps straight onto IPv4 space
	}
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic("safehttp: bad built-in CIDR " + c) // a constant list; a typo is a build-time bug
		}
		out = append(out, n)
	}
	return out
}()

func inExtraInternalRange(ip net.IP) bool {
	for _, n := range extraInternalRanges {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func checkIP(ip net.IP) error {
	switch {
	case ip.IsLoopback():
		return fmt.Errorf("refusing to connect to loopback address %s", ip)
	case ip.IsPrivate():
		return fmt.Errorf("refusing to connect to private address %s", ip)
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast():
		// 169.254.169.254 is the cloud metadata endpoint on every major
		// provider, and it hands out credentials to anyone who asks.
		return fmt.Errorf("refusing to connect to link-local address %s", ip)
	case inExtraInternalRange(ip):
		// net.IP.IsPrivate covers only RFC1918 and IPv6 ULA. The ranges below
		// are not "private" by that definition but are just as internal in
		// practice — 100.64.0.0/10 is carrier-grade NAT, and 100.100.100.200
		// is the metadata endpoint on Alibaba and Tencent clouds, which the
		// link-local check does not catch.
		return fmt.Errorf("refusing to connect to internal address %s", ip)
	case ip.IsUnspecified():
		return fmt.Errorf("refusing to connect to unspecified address %s", ip)
	case ip.IsMulticast(), ip.IsInterfaceLocalMulticast():
		return fmt.Errorf("refusing to connect to multicast address %s", ip)
	}
	// IPv6 unique-local (fc00::/7) is the v6 equivalent of a private range and
	// IsPrivate covers it, but IPv4-mapped v6 addresses arrive as 16 bytes and
	// have to be unwrapped first or every check above silently misses.
	if v4 := ip.To4(); v4 != nil && !ip.Equal(v4) {
		return checkIP(v4)
	}
	return nil
}

// ValidateURL rejects a URL the guarded client would refuse to dial, so an
// operator finds out when they save the form rather than from a delivery that
// quietly never arrives.
//
// It is a convenience, not the boundary: the dialer is the boundary, because a
// name that passes here can resolve somewhere else by the time it is used.
func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL scheme must be http or https, got %q", u.Scheme)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("URL has no host")
	}
	ips, err := net.LookupIP(u.Hostname())
	if err != nil {
		return fmt.Errorf("resolving %q: %w", u.Hostname(), err)
	}
	for _, ip := range ips {
		if err := checkIP(ip); err != nil {
			return err
		}
	}
	return nil
}
