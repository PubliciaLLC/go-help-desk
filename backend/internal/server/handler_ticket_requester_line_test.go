package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// #444: the staff ticket page names who the ticket is for, on every ticket: the
// account's name and address, or the guest's. It is a field of the single-ticket
// read, `requester`, which only staff and administrators receive. A requester
// is on their own ticket and is not sent it: the viewer's allowed list drops any
// key it does not name.

type requesterLine struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// readTicketRaw is GET /tickets/{id} as a map of keys, as the given caller.
func readTicketRaw(t *testing.T, h *harness, as string, id uuid.UUID) map[string]json.RawMessage {
	t.Helper()
	path := "/api/v1/tickets/" + id.String()
	var res *http.Response
	switch as {
	case "staff":
		res = h.do(t, http.MethodGet, path, nil)
	case "admin":
		res = h.doAsAdmin(t, http.MethodGet, path, nil)
	case "user":
		res = h.doAsUser(t, http.MethodGet, path, nil)
	default:
		t.Fatalf("unknown caller %q", as)
	}
	body := readBody(t, res)
	require.Equal(t, http.StatusOK, res.StatusCode, body)
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(body), &m))
	return m
}

func requesterOf(t *testing.T, m map[string]json.RawMessage) requesterLine {
	t.Helper()
	raw, ok := m["requester"]
	require.True(t, ok, "the ticket carries a requester")
	var got requesterLine
	require.NoError(t, json.Unmarshal(raw, &got))
	return got
}

func TestGetTicket_StaffSeeTheRequester(t *testing.T) {
	for _, caller := range []string{"staff", "admin"} {
		t.Run(caller, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeyGuestSubmissionEnabled, true))

			// An account's ticket, filed by that account.
			own := decodeTicket(t, h.postOnBehalf(t, "user", nil))
			require.Equal(t, requesterLine{Name: "Reporting User", Email: "user@test.local"},
				requesterOf(t, readTicketRaw(t, h, caller, own.ID)))

			// Staff's own ticket: the line is on every ticket, not just on-behalf ones.
			mine := decodeTicket(t, h.postOnBehalf(t, "staff", nil))
			require.Equal(t, requesterLine{Name: "Staff", Email: "staff@test.local"},
				requesterOf(t, readTicketRaw(t, h, caller, mine.ID)))

			// Filed for the account by staff: the requester is the account, not the filer.
			onBehalf := decodeTicket(t, h.postOnBehalf(t, "staff", map[string]any{"reporter_user_id": h.userID.String()}))
			require.Equal(t, requesterLine{Name: "Reporting User", Email: "user@test.local"},
				requesterOf(t, readTicketRaw(t, h, caller, onBehalf.ID)))

			// A guest ticket (a typed address with no account): the guest's name and address.
			guest := decodeTicket(t, h.postOnBehalf(t, "staff", map[string]any{
				"reporter_email": "New.Person@Example.test", "guest_name": "Ada Lovelace",
			}))
			require.Equal(t, requesterLine{Name: "Ada Lovelace", Email: "new.person@example.test"},
				requesterOf(t, readTicketRaw(t, h, caller, guest.ID)))
		})
	}
}

// The requester is on their own ticket, so the line tells them nothing, and
// "do not widen what they see" is the rule: the key is not sent to them, on a
// ticket they filed or one staff filed for them.
func TestGetTicket_ARequesterIsNotSentTheRequesterLine(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	own := decodeTicket(t, h.postOnBehalf(t, "user", nil))
	forThem := decodeTicket(t, h.postOnBehalf(t, "staff", map[string]any{"reporter_user_id": h.userID.String()}))

	for name, id := range map[string]uuid.UUID{"filed by themselves": own.ID, "filed for them by staff": forThem.ID} {
		t.Run(name, func(t *testing.T) {
			m := readTicketRaw(t, h, "user", id)
			require.NotContains(t, m, "requester")
			require.Contains(t, m, "tracking_number", "and the ticket itself is still sent")
			// Nor the staff member's name or address, by any key.
			whole, err := json.Marshal(m)
			require.NoError(t, err)
			require.NotContains(t, string(whole), "staff@test.local")
		})
	}
}

// A ticket whose account was deleted afterwards still names who it was for.
func TestGetTicket_TheRequesterLineSurvivesADeletedAccount(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := decodeTicket(t, h.postOnBehalf(t, "user", nil))
	require.NoError(t, h.userSvc.SoftDelete(context.Background(), h.userID))

	require.Equal(t, requesterLine{Name: "Reporting User", Email: "user@test.local"},
		requesterOf(t, readTicketRaw(t, h, "staff", tk.ID)))
}
