package notify

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeSMTP is a scripted SMTP server for the transport tests. It speaks just
// enough of the protocol to record what a client did, and it can leave out
// STARTTLS or AUTH to stand in for a relay that does not offer them, or for
// someone on the path stripping STARTTLS from the greeting.
type fakeSMTP struct {
	ln   net.Listener
	pool *x509.CertPool
	cert tls.Certificate

	implicitTLS  bool // the whole connection is TLS from the first byte (port 465)
	hideSTARTTLS bool // do not advertise STARTTLS (and refuse it)
	hideAUTH     bool // do not advertise AUTH

	mu sync.Mutex
	// authOverPlaintext is set when an AUTH command, credentials included,
	// arrived on a connection that was not encrypted: the leak the TLS rule
	// exists to prevent.
	authOverPlaintext bool
	authCount         int
	mailFrom          int
	delivered         int
}

// newFakeSMTP listens on a random loopback port. The certificate is
// self-signed; a client trusts it through smtpOptions.RootCAs, which only
// tests set, so the production path keeps verifying against the system roots.
func newFakeSMTP(t *testing.T, configure func(*fakeSMTP)) *fakeSMTP {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "fake smtp"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		DNSNames:              []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	leaf, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	pool.AddCert(leaf)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	f := &fakeSMTP{ln: ln, pool: pool, cert: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}}
	if configure != nil {
		configure(f)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go f.serve()
	return f
}

func (f *fakeSMTP) addr() string { return f.ln.Addr().String() }

func (f *fakeSMTP) serve() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(conn)
	}
}

func (f *fakeSMTP) handle(raw net.Conn) {
	defer func() { _ = raw.Close() }()
	_ = raw.SetDeadline(time.Now().Add(10 * time.Second))
	var conn net.Conn = raw
	encrypted := false
	if f.implicitTLS {
		conn = tls.Server(raw, &tls.Config{Certificates: []tls.Certificate{f.cert}, MinVersion: tls.VersionTLS12})
		encrypted = true
	}
	r := bufio.NewReader(conn)
	say := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }

	say("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		verb := strings.ToUpper(strings.Fields(line + " x")[0])
		switch verb {
		case "EHLO", "HELO":
			say("250-fake")
			if !f.implicitTLS && !f.hideSTARTTLS && !encrypted {
				say("250-STARTTLS")
			}
			if !f.hideAUTH {
				say("250-AUTH PLAIN")
			}
			say("250 8BITMIME")
		case "STARTTLS":
			if f.hideSTARTTLS || encrypted {
				say("502 not offered")
				continue
			}
			say("220 go ahead")
			tc := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{f.cert}, MinVersion: tls.VersionTLS12})
			if err := tc.Handshake(); err != nil {
				return
			}
			conn, encrypted = tc, true
			r = bufio.NewReader(conn)
		case "AUTH":
			f.mu.Lock()
			f.authCount++
			if !encrypted {
				f.authOverPlaintext = true
			}
			f.mu.Unlock()
			say("235 ok")
		case "MAIL":
			f.mu.Lock()
			f.mailFrom++
			f.mu.Unlock()
			say("250 ok")
		case "RCPT":
			say("250 ok")
		case "DATA":
			say("354 go")
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
			}
			f.mu.Lock()
			f.delivered++
			f.mu.Unlock()
			say("250 queued")
		case "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

func (f *fakeSMTP) snapshot() (delivered, auth, mail int, plaintextAuth bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.delivered, f.authCount, f.mailFrom, f.authOverPlaintext
}

// remoteAs makes a connection report a different peer address, so a test can
// stand a loopback listener in for a public or a private server. The rule
// looks at the address actually connected to, which is what this overrides.
type remoteAs struct {
	net.Conn
	addr net.Addr
}

func (c remoteAs) RemoteAddr() net.Addr { return c.addr }

func dialAs(ip string) func(string, string, time.Duration) (net.Conn, error) {
	return func(network, addr string, timeout time.Duration) (net.Conn, error) {
		c, err := net.DialTimeout(network, addr, timeout)
		if err != nil {
			return nil, err
		}
		return remoteAs{Conn: c, addr: &net.TCPAddr{IP: net.ParseIP(ip), Port: 25}}, nil
	}
}

// sendMailWithTimeout is the entry the original timeout tests were written
// against: no credentials, default options. Kept here so those tests stay as
// they were.
func sendMailWithTimeout(addr string, _ smtp.Auth, from, to string, msg []byte, timeout time.Duration) error {
	return sendMail(smtpOptions{Addr: addr, Timeout: timeout}, from, to, msg)
}
