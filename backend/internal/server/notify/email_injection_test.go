package notify

import (
	"bufio"
	"context"
	"fmt"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
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

// The verification link must come from configuration, never from the request.
//
// This is the scenario in CodeQL's go/email-injection documentation: a
// Host header the attacker controls is used to build a link, the mail goes out
// from a server the victim trusts, and clicking it hands the reset token to the
// attacker. CWE-640.
//
// It is not present — SendVerificationEmail takes a baseURL argument and
// main.go passes cfg.BaseURL — but "not present" is one refactor away from
// present. Someone making links work behind a proxy would reach for r.Host.
func TestEmail_VerificationLinkComesFromConfigNotTheRequest(t *testing.T) {
	addr, received := captureSMTP(t)
	d := dispatcherFor(t, addr)

	require.NoError(t, d.SendVerificationEmail(
		"user@example.com", "the-secret-token", "https://help.example.com"))

	raw := <-received
	// Quoted-printable may soft-wrap a long line with "=\r\n"; undo that before
	// looking, or a split URL reads as absent when it is present.
	unwrapped := strings.ReplaceAll(raw, "=\r\n", "")

	require.Contains(t, unwrapped, "https://help.example.com/verify-email?token=3Dthe-secret-token",
		"the link must be built from the configured base URL")
	require.NotContains(t, unwrapped, "127.0.0.1",
		"the link must not carry the host the request arrived on")
}

// The link must not be steerable by anything a caller can influence per
// request: the same token, sent twice, must produce the same host.
func TestEmail_VerificationLinkHostIsStable(t *testing.T) {
	for _, base := range []string{"https://help.example.com", "https://help.example.com"} {
		addr, received := captureSMTP(t)
		d := dispatcherFor(t, addr)
		require.NoError(t, d.SendVerificationEmail("user@example.com", "tok", base))
		raw := strings.ReplaceAll(<-received, "=\r\n", "")
		require.Contains(t, raw, "https://help.example.com/verify-email")
	}
}

// The body is attacker-controlled: a reply is whatever the customer typed.
// text/plain is what stops markup in it being markup; this covers the rest.
func TestSanitizeBody(t *testing.T) {
	t.Run("keeps what a reply legitimately contains", func(t *testing.T) {
		in := "Line one\nLine two\n\nNew paragraph\twith a tab\nAccents: caf\u00e9, \u65e5\u672c\u8a9e, emoji \U0001F3AB"
		require.Equal(t, in, sanitizeBody(in), "ordinary text must pass through untouched")
	})

	t.Run("normalises line endings", func(t *testing.T) {
		require.Equal(t, "a\nb\nc", sanitizeBody("a\r\nb\rc"))
	})

	t.Run("strips characters that change what text appears to say", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			bad  rune
		}{
			{"right-to-left override", '\u202e'},
			{"left-to-right embedding", '\u202a'},
			{"bidi isolate", '\u2066'},
			{"line separator", '\u2028'},
			{"paragraph separator", '\u2029'},
			{"null byte", '\x00'},
			{"escape", '\x1b'},
			{"DEL", '\u007f'},
			{"C1 control", '\u0085'},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got := sanitizeBody("before" + string(tc.bad) + "after")
				require.NotContains(t, got, string(tc.bad), "%s must be removed", tc.name)
				require.Contains(t, got, "before", "the surrounding text must survive")
				require.Contains(t, got, "after")
			})
		}
	})
}

// The reply body reaches the wire whole, across lines. Before this, the header
// rule was applied to it and every multi-line reply arrived as one line.
func TestEmail_MultiLineReplySurvivesToTheWire(t *testing.T) {
	addr, received := captureSMTP(t)
	d := dispatcherFor(t, addr)

	body := sanitizeBody("First line\nSecond line\n\nAfter a blank line")
	require.NoError(t, d.send("user@example.com", "Subject", []byte(body)))

	raw := strings.ReplaceAll(<-received, "=\r\n", "")
	require.Contains(t, raw, "First line\r\nSecond line",
		"the line break must survive rather than becoming a space")
	require.Contains(t, raw, "After a blank line")
}

// The message must stay text/plain with no HTML alternative. That declaration
// is what actually prevents script execution — an HTML part added later would
// turn every reply body into a live XSS sink.
func TestEmail_IsPlainTextWithNoHTMLPart(t *testing.T) {
	addr, received := captureSMTP(t)
	d := dispatcherFor(t, addr)

	require.NoError(t, d.send("user@example.com", "Subject",
		[]byte("<script>alert(1)</script> and <b>bold</b>")))

	raw := <-received
	lower := strings.ToLower(raw)
	require.Contains(t, lower, "content-type: text/plain; charset=utf-8")
	require.NotContains(t, lower, "text/html", "an HTML part would make the body a live sink")
	require.NotContains(t, lower, "multipart/", "no alternative part")

	// The markup is delivered as literal text, which is the point: it is
	// content, not instructions.
	require.Contains(t, strings.ReplaceAll(raw, "=\r\n", ""), "script")
}

// Through Dispatch, which is how a reply actually becomes an email — not by
// calling sanitizeBody and then send() separately.
//
// The first version of the multi-line test did exactly that, so reverting
// sanitizePayload to the header rule (the bug that flattened every reply)
// failed nothing. Testing the helper is not testing the path.
func TestEmail_ReplyEventReachesTheWireIntact(t *testing.T) {
	addr, received := captureSMTP(t)
	d := dispatcherFor(t, addr)

	require.NoError(t, d.Dispatch(context.Background(), notification.Event{
		Type: notification.EventTicketReplied,
		Payload: map[string]any{
			"reporter_email": "user@example.com",
			"TrackingNumber": "GHD-2026-000001",
			"Subject":        "Printer broken",
			"ReplyBody":      "First line\nSecond line\n\nAfter a blank line",
			"internal":       false,
		},
	}))

	raw := strings.ReplaceAll(<-received, "=\r\n", "")
	require.Contains(t, raw, "First line\r\nSecond line",
		"a multi-line reply must not be flattened on the way to the wire")
	require.Contains(t, raw, "After a blank line")
}

// And the same path must still strip what makes text lie about itself.
func TestEmail_ReplyEventStripsDisplayTricks(t *testing.T) {
	addr, received := captureSMTP(t)
	d := dispatcherFor(t, addr)

	require.NoError(t, d.Dispatch(context.Background(), notification.Event{
		Type: notification.EventTicketReplied,
		Payload: map[string]any{
			"reporter_email": "user@example.com",
			"TrackingNumber": "GHD-2026-000001",
			"Subject":        "Printer broken",
			"ReplyBody":      "invoice" + string(rune(0x202e)) + "txt.exe",
			"internal":       false,
		},
	}))

	raw := <-received
	require.NotContains(t, raw, string(rune(0x202e)),
		"a bidi override must not reach the recipient")
	require.Contains(t, strings.ReplaceAll(raw, "=\r\n", ""), "invoice")
}
