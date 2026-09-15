package notify

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/config"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// These drive the real send path against a real SMTP listener and read what
// actually goes on the wire.
//
// The file started out arguing that CodeQL's go/email-injection finding was a
// false positive. It was not — the finding was a regression of mine, and the
// fix was to stop putting request text in the message at all. What the tests
// assert now is that outcome: an attacker chooses the ticket subject, the
// reply body and, on a guest ticket, the recipient address, and none of it
// reaches the message — no added header, no redirect, no early end, and no
// text delivered in a header of a message sent from this server's domain.

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

// The link host follows the configured base URL and nothing else.
//
// This looped over the same base URL twice, so it asserted nothing the test
// above did not already assert. Two different base URLs is the check that has
// a chance of failing: the link has to track the configuration, and a host
// taken from somewhere else would show up as the wrong one in at least one of
// the two runs.
func TestEmail_VerificationLinkHostFollowsTheConfiguredBaseURL(t *testing.T) {
	for _, base := range []string{"https://help.example.com", "https://support.example.org"} {
		t.Run(base, func(t *testing.T) {
			addr, received := captureSMTP(t)
			d := dispatcherFor(t, addr)
			require.NoError(t, d.SendVerificationEmail("user@example.com", "tok", base))
			raw := strings.ReplaceAll(<-received, "=\r\n", "")
			require.Contains(t, raw, base+"/verify-email")
			require.NotContains(t, raw, "127.0.0.1")
		})
	}
}

// send() must carry a multi-line body to the wire whole. Nothing
// attacker-controlled reaches it any more, but the templates are multi-line
// and a body flattened to one line was a real bug here once.
func TestEmail_MultiLineBodySurvivesToTheWire(t *testing.T) {
	addr, received := captureSMTP(t)
	d := dispatcherFor(t, addr)

	require.NoError(t, d.send("user@example.com", "Subject",
		[]byte("First line\nSecond line\n\nAfter a blank line")))

	raw := strings.ReplaceAll(<-received, "=\r\n", "")
	require.Contains(t, raw, "First line\r\nSecond line",
		"the line break must survive rather than becoming a space")
	require.Contains(t, raw, "After a blank line")
}

// The message must stay text/plain with no HTML alternative. That declaration
// is what actually prevents script execution — an HTML part added later would
// turn the body into a live sink the moment any content went back into it.
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

// This is the one that matters, and it goes through Dispatch, which is how a
// ticket event actually becomes an email.
//
// Every string an attacker can choose is in the payload. None of them may
// appear on the wire — not in a header, not in the body, not flattened, not
// encoded. The tracking number and the link must, because that is the whole
// message now.
func TestEmail_CarriesNothingFromThePayload(t *testing.T) {
	id := uuid.New()

	for _, tc := range []struct {
		name   string
		evType notification.EventType
	}{
		{"created", notification.EventTicketCreated},
		{"replied", notification.EventTicketReplied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addr, received := captureSMTP(t)
			d := dispatcherFor(t, addr)
			d.cfg.BaseURL = "https://help.example.com"

			require.NoError(t, d.Dispatch(context.Background(), notification.Event{
				Type:           tc.evType,
				TicketID:       id,
				TrackingNumber: "GHD-2026-000001",
				Recipient:      "user@example.com",
				Payload: map[string]any{
					"guest_email":    "attacker@evil.test",
					"reporter_email": "attacker@evil.test",
					"TrackingNumber": "FORGED-0000-000000",
					"Subject":        "Your account is suspended, call 555-0100",
					"ReplyBody":      "Wire the money to account 12345",
					"Priority":       "critical",
				},
			}))

			raw := strings.ReplaceAll(<-received, "=\r\n", "")

			for _, forbidden := range []string{
				"Your account is suspended",
				"555-0100",
				"Wire the money",
				"12345",
				"attacker@evil.test",
				"FORGED",
				"critical",
			} {
				require.NotContains(t, raw, forbidden,
					"payload text must not reach the message")
			}

			require.Contains(t, raw, "GHD-2026-000001", "the ticket reference must be there")
			require.Contains(t, raw, "https://help.example.com/tickets/"+id.String(),
				"the link must be built from the configured base URL")
			require.Contains(t, raw, "To: <user@example.com>",
				"the mail must go to the address on the ticket")
		})
	}
}

// An event with no Recipient produces no mail at all. Previously the address
// came out of the payload, so this is the assertion that the payload is not
// being consulted as a fallback: the listener would have received a message.
func TestEmail_PayloadAddressIsNotAFallbackRecipient(t *testing.T) {
	addr, received := captureSMTP(t)
	d := dispatcherFor(t, addr)

	require.NoError(t, d.Dispatch(context.Background(), notification.Event{
		Type:           notification.EventTicketReplied,
		TicketID:       uuid.New(),
		TrackingNumber: "GHD-2026-000001",
		Payload: map[string]any{
			"reporter_email": "attacker@evil.test",
			"guest_email":    "attacker@evil.test",
		},
	}))

	select {
	case raw := <-received:
		t.Fatalf("a message was sent with no Recipient set:\n%s", raw)
	case <-time.After(300 * time.Millisecond):
	}
}

// A display name is attacker text, and it is delivered.
//
// mail.ParseAddress accepts `"Pay now, call 555-0100" <victim@example.com>`,
// and toAddr.String() puts the quoted part back. So the To header was the one
// place request text still reached the wire after ticket content was taken out
// of the subject and the body — same content spoofing, moved one header down,
// and aimed at whatever address the person filing the ticket chose.
//
// send() now writes the bare address. The name of the person being emailed is
// not worth a channel for arbitrary text.
func TestEmail_RecipientDisplayNameDoesNotReachTheWire(t *testing.T) {
	addr, received := captureSMTP(t)
	d := dispatcherFor(t, addr)

	require.NoError(t, d.send(
		`"URGENT: your account is suspended, call 555-0100" <victim@example.com>`,
		"Subject", []byte("body")))

	raw := strings.ReplaceAll(<-received, "=\r\n", "")
	require.Contains(t, raw, "To: <victim@example.com>")
	require.NotContains(t, raw, "555-0100", "the display name must not be delivered")
	require.NotContains(t, raw, "suspended")
}
