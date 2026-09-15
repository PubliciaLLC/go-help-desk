package notify

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/config"
)

// CodeQL reports go/email-injection here: user text reaches an SMTP write.
// Rather than argue about it, these drive the real send path against a real
// SMTP listener and read what actually goes on the wire.
//
// The claim under test is narrow: an attacker controls the ticket subject and
// the reply body, and must not be able to add a header, redirect the message,
// or end it early.

// headerNames returns the lowercased name of every line that starts a header.
// A continuation line (one beginning with whitespace) is part of the header
// above it, and a line with no colon is not a header at all.
func headerNames(block string) []string {
	var out []string
	for _, line := range strings.Split(block, "\r\n") {
		if line == "" || line[0] == ' ' || line[0] == '\t' {
			continue
		}
		name, _, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		out = append(out, strings.ToLower(strings.TrimSpace(name)))
	}
	return out
}

// captureSMTP accepts one message and returns the raw DATA payload.
func captureSMTP(t *testing.T) (addr string, received chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	received = make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		r := bufio.NewReader(conn)
		w := bufio.NewWriter(conn)
		say := func(s string) { _, _ = w.WriteString(s + "\r\n"); _ = w.Flush() }

		say("220 test ESMTP")
		var body strings.Builder
		inData := false
		for {
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			line, err := r.ReadString('\n')
			if err != nil {
				received <- body.String()
				return
			}
			if inData {
				if line == ".\r\n" {
					say("250 ok")
					inData = false
					received <- body.String()
					continue
				}
				body.WriteString(line)
				continue
			}
			switch {
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
				say("250-test")
				say("250 OK")
			case strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"):
				say("250 OK")
			case strings.HasPrefix(line, "DATA"):
				say("354 send it")
				inData = true
			case strings.HasPrefix(line, "QUIT"):
				say("221 bye")
				return
			default:
				say("250 OK")
			}
		}
	}()
	return ln.Addr().String(), received
}

func dispatcherFor(t *testing.T, addr string) *EmailDispatcher {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	var port int
	_, err = fmt.Sscanf(portStr, "%d", &port)
	require.NoError(t, err)
	d, err := NewEmailDispatcher(&config.Config{
		SMTPHost: host, SMTPPort: port, SMTPFrom: "helpdesk@example.com",
	})
	require.NoError(t, err)
	return d
}

// A subject carrying CRLF and a forged Bcc must not produce a Bcc header.
func TestEmail_SubjectCannotInjectAHeader(t *testing.T) {
	addr, received := captureSMTP(t)
	d := dispatcherFor(t, addr)

	evil := "Printer broken\r\nBcc: attacker@evil.test\r\nX-Injected: yes"
	require.NoError(t, d.send("victim@example.com", evil, []byte("body")))

	raw := <-received
	headers := raw
	if i := strings.Index(raw, "\r\n\r\n"); i >= 0 {
		headers = raw[:i]
	}

	// Asserted per line, not by substring. The injected text DOES survive —
	// flattened into the Subject value — and a substring search finds "Bcc:"
	// there and calls it a header. What matters is whether any line STARTS a
	// header, which is the only way a receiver parses one.
	names := headerNames(headers)
	require.NotContains(t, names, "bcc", "a forged Bcc must not become a header")
	require.NotContains(t, names, "x-injected", "a forged header must not become one")
	require.Equal(t, []string{"from", "to", "subject", "mime-version",
		"content-type", "content-transfer-encoding"}, names,
		"the header set must be exactly what the sender wrote")

	// The text survives, flattened onto one line. Refusing the mail outright
	// would be a worse outcome than sending it with an odd-looking subject.
	require.Contains(t, headers, "Printer broken")
}

// A recipient address carrying CRLF must be refused outright rather than
// flattened, because the address decides where the mail goes.
func TestEmail_RecipientCannotInjectAHeader(t *testing.T) {
	addr, _ := captureSMTP(t)
	d := dispatcherFor(t, addr)

	err := d.send("victim@example.com>\r\nBcc: attacker@evil.test", "Subject", []byte("body"))
	require.Error(t, err, "an address containing CRLF must be refused")
	require.Contains(t, err.Error(), "invalid recipient address")
}

// The body is attacker-controlled too. It must not be able to add a header or
// end the message early with a bare dot.
func TestEmail_BodyCannotInjectAHeaderOrEndTheMessage(t *testing.T) {
	addr, received := captureSMTP(t)
	d := dispatcherFor(t, addr)

	// A bare "." line ends DATA in SMTP unless the writer dot-stuffs it.
	body := "hello\r\n.\r\nMAIL FROM:<attacker@evil.test>\r\nX-Injected: yes\r\n"
	require.NoError(t, d.send("victim@example.com", "Subject", []byte(body)))

	raw := <-received
	i := strings.Index(raw, "\r\n\r\n")
	require.GreaterOrEqual(t, i, 0, "the message must have a header/body boundary")
	headers, bodyOnWire := raw[:i], raw[i:]

	require.NotContains(t, headers, "X-Injected",
		"body content must not become a header")
	require.NotContains(t, strings.ToLower(headers), "mail from",
		"body content must not become an SMTP command")
	// The body arrived whole: the server saw the terminating dot only at the end.
	require.Contains(t, bodyOnWire, "hello")
	require.NotEmpty(t, bodyOnWire)
}
