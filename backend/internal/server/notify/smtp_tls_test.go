package notify

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/config"
)

var testMsg = []byte("Subject: x\r\n\r\nbody")

const (
	publicIP  = "203.0.113.9" // TEST-NET-3: never private
	privateIP = "10.1.2.3"
)

func TestUsesImplicitTLS(t *testing.T) {
	for port, want := range map[int]bool{465: true, 587: false, 25: false, 2525: false, 0: false} {
		require.Equal(t, want, usesImplicitTLS(port), "port %d", port)
	}
}

func TestIsPrivateIP(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1":          true,
		"::1":                true,
		"10.0.0.1":           true,
		"172.16.0.1":         true,
		"172.31.255.255":     true,
		"172.15.255.255":     false,
		"172.32.0.1":         false,
		"192.168.1.1":        true,
		"192.169.0.1":        false,
		"fc00::1":            true,
		"fd12:3456::1":       true,
		"fe80::1":            false,
		"2001:db8::1":        false,
		"8.8.8.8":            false,
		"169.254.1.1":        false,
		"::ffff:10.0.0.1":    true,
		"::ffff:203.0.113.9": false,
		"100.64.0.1":         false, // carrier-grade NAT is not RFC 1918
	}
	for ip, want := range cases {
		require.Equal(t, want, isPrivateIP(net.ParseIP(ip)), ip)
	}
	require.False(t, isPrivateIP(nil))
}

func TestSMTPOptionsFor_PortDecidesImplicitTLS(t *testing.T) {
	o := smtpOptionsFor(&config.Config{SMTPHost: "mail.example.com", SMTPPort: 465, SMTPUser: "u", SMTPPassword: "p"})
	require.True(t, o.Implicit)
	require.Equal(t, "mail.example.com:465", o.Addr)
	require.Equal(t, "u", o.User)
	require.Equal(t, "p", o.Password)

	o = smtpOptionsFor(&config.Config{SMTPHost: "mail.example.com", SMTPPort: 587})
	require.False(t, o.Implicit)
	require.Equal(t, "mail.example.com:587", o.Addr)
}

// Every row of the rule. The server is always on loopback; dialAs makes the
// client see it as a public or a private address.
func TestSendMail_TLSRule(t *testing.T) {
	cases := []struct {
		name         string
		password     string // "" means no password
		implicit     bool
		hideSTARTTLS bool
		peer         string
		wantErr      string // "" means delivered
	}{
		// Password set: TLS always, wherever the server is.
		{name: "password, public, STARTTLS", password: "pw", peer: publicIP},
		{name: "password, private, STARTTLS", password: "pw", peer: privateIP},
		{name: "password, loopback, STARTTLS", password: "pw", peer: "127.0.0.1"},
		{name: "password, public, no STARTTLS", password: "pw", peer: publicIP, hideSTARTTLS: true, wantErr: "STARTTLS"},
		{name: "password, private, no STARTTLS", password: "pw", peer: privateIP, hideSTARTTLS: true, wantErr: "STARTTLS"},
		{name: "password, loopback, no STARTTLS", password: "pw", peer: "127.0.0.1", hideSTARTTLS: true, wantErr: "STARTTLS"},
		{name: "password, public, implicit TLS (465)", password: "pw", implicit: true, peer: publicIP},
		{name: "password, private, implicit TLS (465)", password: "pw", implicit: true, peer: privateIP},

		// No password: plain allowed to a private address only.
		{name: "no password, private, no STARTTLS", peer: privateIP, hideSTARTTLS: true},
		{name: "no password, loopback, no STARTTLS", peer: "127.0.0.1", hideSTARTTLS: true},
		{name: "no password, public, no STARTTLS", peer: publicIP, hideSTARTTLS: true, wantErr: "STARTTLS"},
		{name: "no password, public, STARTTLS", peer: publicIP},
		{name: "no password, private, STARTTLS", peer: privateIP},
		{name: "no password, public, implicit TLS (465)", implicit: true, peer: publicIP},
		{name: "no password, private, implicit TLS (465)", implicit: true, peer: privateIP},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newFakeSMTP(t, func(f *fakeSMTP) {
				f.implicitTLS = tc.implicit
				f.hideSTARTTLS = tc.hideSTARTTLS
			})
			opts := smtpOptions{
				Addr: srv.addr(), Password: tc.password, Implicit: tc.implicit,
				Timeout: 5 * time.Second, RootCAs: srv.pool, dial: dialAs(tc.peer),
			}
			if tc.password != "" {
				opts.User = "user"
			}
			err := sendMail(opts, "from@example.com", "to@example.com", testMsg)

			delivered, auth, _, plaintextAuth := srv.snapshot()
			require.False(t, plaintextAuth, "credentials were sent on an unencrypted connection")
			if tc.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErr)
				require.Zero(t, delivered, "a refused send still delivered")
				require.Zero(t, auth, "a refused send still authenticated")
				return
			}
			require.NoError(t, err)
			require.Equal(t, 1, delivered)
			if tc.password != "" {
				require.Equal(t, 1, auth)
			}
		})
	}
}

// The clear error names the cause, so the admin warning can show it.
func TestSendMail_PasswordWithoutSTARTTLSSaysWhy(t *testing.T) {
	srv := newFakeSMTP(t, func(f *fakeSMTP) { f.hideSTARTTLS = true })
	err := sendMail(smtpOptions{Addr: srv.addr(), User: "u", Password: "p", Timeout: 5 * time.Second, RootCAs: srv.pool},
		"from@example.com", "to@example.com", testMsg)
	require.EqualError(t, err, "SMTP server does not offer STARTTLS, and SMTP_PASSWORD is set: "+
		"refusing to send the password unencrypted")
}

func TestSendMail_PublicServerWithoutSTARTTLSSaysWhy(t *testing.T) {
	srv := newFakeSMTP(t, func(f *fakeSMTP) { f.hideSTARTTLS = true })
	err := sendMail(smtpOptions{Addr: srv.addr(), Timeout: 5 * time.Second, RootCAs: srv.pool, dial: dialAs(publicIP)},
		"from@example.com", "to@example.com", testMsg)
	require.EqualError(t, err, "SMTP server does not offer STARTTLS and is not on a private address: "+
		"refusing to send mail unencrypted")
}

// AUTH is often advertised only after STARTTLS, so it is checked after it.
// A relay that never offers it fails the send instead of sending anonymously.
func TestSendMail_UserSetButNoAUTHFails(t *testing.T) {
	srv := newFakeSMTP(t, func(f *fakeSMTP) { f.hideAUTH = true })
	err := sendMail(smtpOptions{Addr: srv.addr(), User: "u", Password: "p", Timeout: 5 * time.Second, RootCAs: srv.pool},
		"from@example.com", "to@example.com", testMsg)
	require.EqualError(t, err, "SMTP server does not offer AUTH but SMTP_USER is set")
	delivered, auth, mail, _ := srv.snapshot()
	require.Zero(t, delivered)
	require.Zero(t, auth)
	require.Zero(t, mail, "went on to MAIL FROM after refusing")
}

// No user: nothing to authenticate, so a server with no AUTH is fine.
func TestSendMail_NoUserNoAUTHDelivers(t *testing.T) {
	srv := newFakeSMTP(t, func(f *fakeSMTP) { f.hideAUTH = true })
	err := sendMail(smtpOptions{Addr: srv.addr(), Timeout: 5 * time.Second, RootCAs: srv.pool},
		"from@example.com", "to@example.com", testMsg)
	require.NoError(t, err)
	delivered, _, _, _ := srv.snapshot()
	require.Equal(t, 1, delivered)
}

// Port 465 against a server that talks plain SMTP must fail at the handshake,
// not hang until the timeout (it used to hang: the client waited for a banner
// the server was waiting for the client to prompt).
func TestSendMail_ImplicitTLSAgainstAPlainServerFailsFast(t *testing.T) {
	srv := newFakeSMTP(t, nil) // speaks plain SMTP
	start := time.Now()
	err := sendMail(smtpOptions{Addr: srv.addr(), Implicit: true, Timeout: 5 * time.Second, RootCAs: srv.pool},
		"from@example.com", "to@example.com", testMsg)
	require.Error(t, err)
	require.Less(t, time.Since(start), 3*time.Second)
	delivered, _, _, _ := srv.snapshot()
	require.Zero(t, delivered)
}

// A certificate the client does not trust is not talked to. The test hook only
// adds a root; without it the self-signed certificate must be refused.
func TestSendMail_UntrustedCertificateIsRefused(t *testing.T) {
	srv := newFakeSMTP(t, nil)
	err := sendMail(smtpOptions{Addr: srv.addr(), User: "u", Password: "p", Timeout: 5 * time.Second},
		"from@example.com", "to@example.com", testMsg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "STARTTLS")
	delivered, auth, _, _ := srv.snapshot()
	require.Zero(t, delivered)
	require.Zero(t, auth)
}
