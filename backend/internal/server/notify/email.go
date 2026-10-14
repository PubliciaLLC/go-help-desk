// Package notify implements the notification.Dispatcher interface for email
// and webhook delivery.
package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"embed"
	"errors"
	"fmt"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/netip"
	"net/smtp"
	"strings"
	"text/template"
	"time"

	"github.com/publiciallc/go-help-desk/backend/internal/config"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

// EmailDispatcher sends email notifications on ticket events.
// It is a no-op when SMTP is not configured.
type EmailDispatcher struct {
	cfg       *config.Config
	templates *template.Template
}

// NewEmailDispatcher loads templates and returns an EmailDispatcher.
// Returns a no-op dispatcher when SMTP is not configured.
func NewEmailDispatcher(cfg *config.Config) (*EmailDispatcher, error) {
	if !cfg.EmailEnabled() {
		return &EmailDispatcher{cfg: cfg}, nil
	}
	tmpl, err := template.ParseFS(templateFS, "templates/*.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parsing email templates: %w", err)
	}
	return &EmailDispatcher{cfg: cfg, templates: tmpl}, nil
}

// sanitizeHeader strips CR and LF so user-controlled values interpolated into
// email headers cannot inject additional headers.
func sanitizeHeader(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

// ErrPermanent marks a delivery error that retrying cannot fix, such as an
// email template that does not render. The outbox worker fails the row at once
// on it instead of retrying (#456).
var ErrPermanent = errors.New("permanent delivery error")

// Dispatch sends an email for supported event types. Unsupported events are
// silently ignored. A template that fails to render, or a failed send, is
// returned: the outbox worker retries the second and fails the first at once
// (ErrPermanent), and either shows up in the admin email-health warning.
func (d *EmailDispatcher) Dispatch(_ context.Context, event notification.Event) error {
	if !d.cfg.EmailEnabled() {
		return nil
	}

	templateName, subject, to, data, ok := d.eventToEmail(event)
	if !ok || to == "" {
		return nil
	}

	var buf bytes.Buffer
	if err := d.templates.ExecuteTemplate(&buf, templateName, data); err != nil {
		// Used to be swallowed ("non-fatal"), which made the worker delete the
		// row as delivered: a broken template lost every mail silently.
		return fmt.Errorf("rendering %s: %w: %w", templateName, ErrPermanent, err)
	}

	return d.send(to, subject, buf.Bytes())
}

// eventToEmail decides what to send. It deliberately sends no content.
//
// Ticket emails used to carry the ticket subject and the reply body. That makes
// the message a carrier for text somebody else chose, sent from a domain the
// recipient trusts — content spoofing, CWE-640. It was not theoretical here:
// filing a ticket with a chosen subject was enough to have this server mail
// that text out over its own reputation.
//
// So the email now says only that something happened, names the ticket by its
// tracking number, and links to it. The tracking number and the address both
// come off the persisted ticket (Event.TrackingNumber, Event.Recipient), never
// out of Event.Payload, which carries request text. The content stays in the
// application behind the login that already guards it.
func (d *EmailDispatcher) eventToEmail(event notification.Event) (templateName, subject, to string, data any, ok bool) {
	if event.Recipient == "" {
		return "", "", "", nil, false
	}

	// Every part of the link is trusted: a token this server minted, or the
	// ticket's own identifier, on the configured base URL.
	//
	// A guest has no account, so the token is how they reach their own ticket.
	// This does not reopen what 1.2.0 closed — that removed CONTENT chosen by
	// whoever filed the ticket, its subject line and reply bodies, from mail
	// sent over the operator's domain. A link this server generated is not
	// that, and the message still carries nothing anyone else wrote.
	// A guest link carries the token in the URL FRAGMENT, not the path.
	//
	// A fragment is never sent to a server. Not to this one, not to a proxy in
	// front of it, not in a Referer header. The path form was logged verbatim
	// at INFO by requestLogger on the browser's first request for the SPA
	// shell — the application's own access log was the leak the frontend
	// comment claimed to be defending against — and no amount of redaction
	// makes it safe in every proxy, CDN and aggregator in front of us.
	//
	// The SPA reads location.hash and clears it. The server never learns it
	// except as an Authorization header on the API calls that need it.
	path := "/tickets/" + event.TicketID.String()
	if event.GuestToken != "" {
		path = "/g#" + event.GuestToken
	}
	view := map[string]string{
		"TrackingNumber": event.TrackingNumber,
		"TicketURL":      strings.TrimRight(d.cfg.BaseURL, "/") + path,
	}

	// A missing tracking number costs the subject line its reference but must
	// not cost the customer the notification.
	ref := "your ticket"
	if event.TrackingNumber != "" {
		ref = "[" + event.TrackingNumber + "]"
	}

	switch event.Type {
	case notification.EventTicketCreated:
		return "ticket_created.tmpl", "We have received " + ref, event.Recipient, view, true
	case notification.EventTicketReplied:
		return "ticket_replied.tmpl", "There is a new reply on " + ref, event.Recipient, view, true

	// These three send only to a guest — Recipient is empty for a ticket with
	// a reporter account, which returns above. An account holder signs in and
	// sees a status change; mailing them every transition would be a stream of
	// email nobody asked for, carrying a link they do not need.
	//
	// They exist because rotation has to deliver its replacement: each of these
	// invalidates the link already in the guest's inbox, so each has to carry
	// the new one or the guest is locked out.
	case notification.EventTicketStatusChanged:
		return "ticket_status_changed.tmpl", "There is an update on " + ref, event.Recipient, view, true
	case notification.EventTicketResolved:
		return "ticket_resolved.tmpl", "We have resolved " + ref, event.Recipient, view, true
	case notification.EventTicketReopened:
		return "ticket_reopened.tmpl", "We have reopened " + ref, event.Recipient, view, true
	case notification.EventGuestLinkResent:
		return "guest_link.tmpl", "Your link to " + ref, event.Recipient, view, true
	}
	return "", "", "", nil, false
}

// SendVerificationEmail sends a transactional email containing the account
// verification link. It implements registration.Mailer.
func (d *EmailDispatcher) SendVerificationEmail(to, token, baseURL string) error {
	if !d.cfg.EmailEnabled() {
		return nil
	}
	verifyURL := baseURL + "/verify-email?token=" + token
	var buf bytes.Buffer
	if err := d.templates.ExecuteTemplate(&buf, "email_verification.tmpl", map[string]string{
		"VerifyURL": verifyURL,
	}); err != nil {
		return fmt.Errorf("rendering verification email: %w: %w", ErrPermanent, err)
	}
	return d.send(to, "Verify your email address", buf.Bytes())
}

// send builds a MIME message with sanitized headers and quoted-printable-encoded
// body, then hands it to smtp.SendMail. All user-controlled input passes through
// validation (mail.ParseAddress) or encoding (quoted-printable / sanitizeHeader)
// before reaching the SMTP sink.
// validateSendAddresses parses and validates the recipient and sender
// addresses, rejecting header-injection attempts (a CRLF/LF-bearing address
// would otherwise let an attacker smuggle extra SMTP headers, e.g. a forged
// Bcc, into the message). Pulled out of send() so this pure validation logic
// is testable without an actual SMTP server to send through.
func validateSendAddresses(to, from string) (toAddr, fromAddr *mail.Address, err error) {
	toAddr, err = mail.ParseAddress(to)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid recipient address: %w", err)
	}
	fromAddr, err = mail.ParseAddress(from)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid sender address: %w", err)
	}
	return toAddr, fromAddr, nil
}

func (d *EmailDispatcher) send(to, subject string, body []byte) error {
	toAddr, fromAddr, err := validateSendAddresses(to, d.cfg.SMTPFrom)
	if err != nil {
		return err
	}

	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\n", fromAddr.String())
	// toAddr.Address, not toAddr.String(): ParseAddress accepts a display name
	// and String() puts it back, so `"text" <victim@example.com>` would deliver
	// whatever text the sender chose in a header of a message sent from this
	// server's domain. The bare address is all a recipient needs.
	fmt.Fprintf(&msg, "To: <%s>\r\n", toAddr.Address)
	fmt.Fprintf(&msg, "Subject: %s\r\n", sanitizeHeader(subject))
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	msg.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	msg.WriteString("\r\n")

	qp := quotedprintable.NewWriter(&msg)
	if _, err := qp.Write(body); err != nil {
		return fmt.Errorf("encoding body: %w", err)
	}
	if err := qp.Close(); err != nil {
		return fmt.Errorf("closing encoder: %w", err)
	}

	opts := smtpOptionsFor(d.cfg)
	opts.Timeout = smtpTimeout
	return sendMail(opts, fromAddr.Address, toAddr.Address, msg.Bytes())
}

// smtpTimeout bounds a single delivery end to end.
//
// net/smtp.SendMail dials with no timeout and sets no deadline, and delivery
// happens on the request goroutine after the ticket has already committed. A
// relay that accepts the connection and then stops responding therefore parked
// the handler indefinitely: the client eventually saw the write timeout drop
// the connection with no response, retried, and filed a duplicate ticket, while
// every goroutine that touched email piled up until the relay recovered.
const smtpTimeout = 20 * time.Second

// smtpOptions is everything one delivery needs to know about the server.
type smtpOptions struct {
	Addr     string // host:port
	User     string
	Password string
	// Implicit means TLS from the first byte (port 465), not STARTTLS.
	Implicit bool
	Timeout  time.Duration

	// Test hooks. Production leaves both nil: RootCAs nil means the system
	// roots, and verification is never switched off, only extended.
	RootCAs *x509.CertPool
	dial    func(network, addr string, timeout time.Duration) (net.Conn, error)
}

// usesImplicitTLS reports whether a port is the implicit-TLS submission port.
// Chosen by port rather than a setting: 465 speaks TLS from the first byte, so
// a STARTTLS client waited on a banner that never came and hung until the
// timeout. No working setup changes.
func usesImplicitTLS(port int) bool { return port == 465 }

func smtpOptionsFor(cfg *config.Config) smtpOptions {
	return smtpOptions{
		Addr:     fmt.Sprintf("%s:%d", cfg.SMTPHost, cfg.SMTPPort),
		User:     cfg.SMTPUser,
		Password: cfg.SMTPPassword,
		Implicit: usesImplicitTLS(cfg.SMTPPort),
	}
}

// isPrivateIP reports loopback, RFC 1918 (10/8, 172.16/12, 192.168/16) and
// IPv6 unique local addresses (fc00::/7), and nothing else. Link-local and
// carrier-grade NAT ranges are deliberately not private here.
func isPrivateIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	return addr.IsLoopback() || addr.IsPrivate()
}

// peerIP is the address the connection actually reached, not the one the
// configured name resolved to some time earlier: the rule is about where the
// bytes go.
func peerIP(conn net.Conn) net.IP {
	if a, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		return a.IP
	}
	host, _, err := net.SplitHostPort(conn.RemoteAddr().String())
	if err != nil {
		return nil
	}
	return net.ParseIP(host)
}

// The TLS rule, the whole of it:
//
//   - A password is never sent unencrypted, to any address, private or not.
//     Implicit TLS on 465, STARTTLS everywhere else; if STARTTLS is not
//     offered the send fails. A man in the middle stripping STARTTLS from the
//     greeting therefore costs a failed send, not a password.
//   - With no password, plain SMTP is allowed only to a private address
//     (loopback, RFC 1918, fc00::/7): an internal relay. A public address
//     still requires TLS. STARTTLS is used whenever it is offered.
//
// sendMail is net/smtp.SendMail with those rules, a dial timeout, and a
// deadline covering the whole conversation.
func sendMail(o smtpOptions, from, to string, msg []byte) error {
	dial := o.dial
	if dial == nil {
		dial = net.DialTimeout
	}
	conn, err := dial("tcp", o.Addr, o.Timeout)
	if err != nil {
		return fmt.Errorf("dialing SMTP server: %w", err)
	}
	// One deadline for the whole exchange, so a relay that answers the dial and
	// then stalls mid-conversation cannot hold the goroutine either.
	if err := conn.SetDeadline(time.Now().Add(o.Timeout)); err != nil {
		_ = conn.Close()
		return fmt.Errorf("setting SMTP deadline: %w", err)
	}

	host, _, err := net.SplitHostPort(o.Addr)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("parsing SMTP address: %w", err)
	}
	tlsCfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12, RootCAs: o.RootCAs}

	if o.Implicit {
		tc := tls.Client(conn, tlsCfg)
		if err := tc.Handshake(); err != nil {
			_ = conn.Close()
			return fmt.Errorf("SMTP implicit TLS: %w", err)
		}
		conn = tc
	}

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("SMTP handshake: %w", err)
	}
	defer func() { _ = c.Close() }()

	if !o.Implicit {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsCfg); err != nil {
				return fmt.Errorf("STARTTLS: %w", err)
			}
		} else if o.Password != "" {
			return errors.New("SMTP server does not offer STARTTLS, and SMTP_PASSWORD is set: " +
				"refusing to send the password unencrypted")
		} else if !isPrivateIP(peerIP(conn)) {
			return errors.New("SMTP server does not offer STARTTLS and is not on a private address: " +
				"refusing to send mail unencrypted")
		}
	}

	// After STARTTLS: AUTH is often advertised only on the encrypted session.
	// Sending anonymously when credentials are configured would hide a broken
	// relay behind apparent success.
	if o.User != "" {
		if ok, _ := c.Extension("AUTH"); !ok {
			return errors.New("SMTP server does not offer AUTH but SMTP_USER is set")
		}
		if err := c.Auth(smtp.PlainAuth("", o.User, o.Password, host)); err != nil {
			return fmt.Errorf("SMTP auth: %w", err)
		}
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("SMTP MAIL FROM: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("SMTP RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("writing message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("closing message: %w", err)
	}
	return c.Quit()
}
