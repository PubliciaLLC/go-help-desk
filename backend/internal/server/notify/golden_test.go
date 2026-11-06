package notify

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// Golden parity (#158): with no override stored, every mail is byte-identical
// to what the release before the template editor sent. The files under
// testdata/golden were captured from the code as it stood BEFORE the editor
// was written; they are the whole MIME message as it went over the wire, so a
// changed header, subject, encoding or body fails here.
//
// Regenerate deliberately with GHD_UPDATE_GOLDEN=1, and only when a mail is
// meant to change.

var goldenTicketID = uuid.MustParse("11111111-2222-3333-4444-555555555555")

type goldenCase struct {
	file string
	send func(d *EmailDispatcher) error
}

func goldenEvent(typ notification.EventType, mod func(*notification.Event)) func(*EmailDispatcher) error {
	return func(d *EmailDispatcher) error {
		ev := notification.Event{
			Type: typ, TicketID: goldenTicketID, TrackingNumber: "GHD-2026-000042",
			Recipient: "to@example.com",
		}
		if mod != nil {
			mod(&ev)
		}
		return d.Dispatch(context.Background(), ev)
	}
}

func goldenCases() []goldenCase {
	guest := func(e *notification.Event) { e.GuestToken = "guest-token-abc" }
	reason := func(r notification.StaffSwitch) func(*notification.Event) {
		return func(e *notification.Event) { e.StaffReason = r }
	}
	cases := []goldenCase{
		{"ticket_created", goldenEvent(notification.EventTicketCreated, nil)},
		{"ticket_created_no_tracking", goldenEvent(notification.EventTicketCreated, func(e *notification.Event) { e.TrackingNumber = "" })},
		{"ticket_created_guest", goldenEvent(notification.EventTicketCreated, guest)},
		{"ticket_replied", goldenEvent(notification.EventTicketReplied, nil)},
		{"ticket_status_changed", goldenEvent(notification.EventTicketStatusChanged, guest)},
		{"ticket_resolved", goldenEvent(notification.EventTicketResolved, guest)},
		{"ticket_reopened", goldenEvent(notification.EventTicketReopened, guest)},
		{"guest_link", goldenEvent(notification.EventGuestLinkResent, guest)},
		{"email_verification", func(d *EmailDispatcher) error {
			return sendVerification(d, "to@example.com", "verify-token", "https://help.example.com")
		}},
		{"password_reset", func(d *EmailDispatcher) error {
			return sendReset(d, "to@example.com", "reset-token", "https://help.example.com")
		}},
	}
	for _, ev := range []struct {
		prefix string
		typ    notification.EventType
	}{
		{"staff_assigned", notification.EventStaffAssigned},
		{"staff_requester_replied", notification.EventStaffRequesterReplied},
		{"staff_reopened", notification.EventStaffReopened},
	} {
		cases = append(cases,
			goldenCase{ev.prefix, goldenEvent(ev.typ, reason(notification.SwitchAssigned))},
			goldenCase{ev.prefix + "_group", goldenEvent(ev.typ, reason(notification.SwitchGroup))},
			goldenCase{ev.prefix + "_follow", goldenEvent(ev.typ, reason(notification.SwitchFollow))},
		)
	}
	return cases
}

func sendVerification(d *EmailDispatcher, to, token, baseURL string) error {
	return d.SendVerificationEmail(context.Background(), to, token, baseURL)
}

func sendReset(d *EmailDispatcher, to, token, baseURL string) error {
	return d.SendPasswordResetEmail(context.Background(), to, token, baseURL)
}

// captureMessage sends through a real dispatcher to a real listener and
// returns the whole message. wire, when set, attaches overrides first.
func captureMessage(t *testing.T, send func(d *EmailDispatcher) error, wire func(*EmailDispatcher)) string {
	t.Helper()
	addr, received := captureSMTP(t)
	d := dispatcherFor(t, addr)
	d.cfg.BaseURL = "https://help.example.com"
	if wire != nil {
		wire(d)
	}
	require.NoError(t, send(d))
	return <-received
}

func TestGolden_EveryMailIsByteIdenticalWithNoOverride(t *testing.T) {
	update := os.Getenv("GHD_UPDATE_GOLDEN") != ""
	dir := filepath.Join("testdata", "golden")
	for _, c := range goldenCases() {
		t.Run(c.file, func(t *testing.T) {
			got := captureMessage(t, c.send, nil)
			path := filepath.Join(dir, c.file+".eml")
			if update {
				require.NoError(t, os.MkdirAll(dir, 0o755))
				require.NoError(t, os.WriteFile(path, []byte(got), 0o644))
				return
			}
			want, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, string(want), got)
		})
	}
}

// The 17 templates are all reached by the cases above.
func TestGolden_CoversEverySeventeenTemplates(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range goldenCases() {
		seen[strings.TrimSuffix(strings.TrimSuffix(c.file, "_no_tracking"), "_guest")] = true
	}
	files, err := templateFS.ReadDir("templates")
	require.NoError(t, err)
	require.Len(t, files, 17)
	for _, f := range files {
		name := strings.TrimSuffix(f.Name(), ".tmpl")
		require.True(t, seen[name], "no golden case for %s", name)
	}
}
