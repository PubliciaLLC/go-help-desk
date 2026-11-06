package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// #130: a ticket records how it arrived. The server derives the value from the
// entry point; the one choice a person makes is "phone", and only a member of
// staff filing on someone's behalf from the web form (a browser session).

func (h *harness) createBody(extra map[string]any) map[string]any {
	body := map[string]any{"subject": "Printer", "description": "jammed", "category_id": h.catID.String()}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

// postBearer is a POST with an OAuth client's token (the harness has only a GET).
func (h *harness) postBearer(t *testing.T, token, path string, body any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, json.NewEncoder(&buf).Encode(body))
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.srv.ServeHTTP(rr, req)
	return rr.Result()
}

// storedChannel reads the channel from the store, not from the response: a
// requester's response does not carry it.
func storedChannel(t *testing.T, h *harness, id uuid.UUID) ticket.SourceChannel {
	t.Helper()
	got, err := h.ticketSvc.GetByID(context.Background(), id)
	require.NoError(t, err)
	return got.SourceChannel
}

// createdID is the id out of a 201 body, whatever fields the viewer is shown.
func createdID(t *testing.T, res *http.Response) uuid.UUID {
	t.Helper()
	body := readBody(t, res)
	require.Equal(t, http.StatusCreated, res.StatusCode, body)
	var out struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &out))
	return out.ID
}

func TestSourceChannel_EachEntryPointRecordsItsOwn(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	enableGuests(t, h)
	client := h.newServiceClient(t, "intake", allScopes()...)

	for _, tc := range []struct {
		name string
		do   func() *http.Response
		want ticket.SourceChannel
	}{
		{"staff browser session", func() *http.Response {
			return h.doSession(t, h.session(t, asStaff), http.MethodPost, "/api/v1/tickets", h.createBody(nil))
		}, ticket.ChannelWeb},
		{"admin browser session", func() *http.Response {
			return h.doSession(t, h.session(t, asAdmin), http.MethodPost, "/api/v1/tickets", h.createBody(nil))
		}, ticket.ChannelWeb},
		{"requester browser session", func() *http.Response {
			return h.doSession(t, h.session(t, asReporter), http.MethodPost, "/api/v1/tickets", h.createBody(nil))
		}, ticket.ChannelWeb},
		{"staff API key", func() *http.Response {
			return h.do(t, http.MethodPost, "/api/v1/tickets", h.createBody(nil))
		}, ticket.ChannelAPI},
		{"requester API key", func() *http.Response {
			return h.doAsUser(t, http.MethodPost, "/api/v1/tickets", h.createBody(nil))
		}, ticket.ChannelAPI},
		{"OAuth client", func() *http.Response {
			return h.postBearer(t, client.Token, "/api/v1/tickets", h.createBody(nil))
		}, ticket.ChannelAPI},
		{"guest form", func() *http.Response {
			return h.doGuest(t, http.MethodPost, "/api/v1/guest/tickets", "", h.createBody(map[string]any{
				"guest_email": "someone@test.local", "guest_name": "Some One"}))
		}, ticket.ChannelGuest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := tc.do()
			// The guest form answers with the tracking number only.
			body := readBody(t, res)
			require.Equal(t, http.StatusCreated, res.StatusCode, body)
			var out struct {
				ID             uuid.UUID `json:"id"`
				TrackingNumber string    `json:"tracking_number"`
			}
			require.NoError(t, json.Unmarshal([]byte(body), &out))
			var id uuid.UUID
			if out.ID != uuid.Nil {
				id = out.ID
			} else {
				tk, err := h.ticketSvc.GetByTrackingNumber(context.Background(), ticket.TrackingNumber(out.TrackingNumber))
				require.NoError(t, err)
				id = tk.ID
			}
			require.Equal(t, tc.want, storedChannel(t, h, id))
		})
	}
}

// A follow-up records its own entry point, not the original's.
func TestSourceChannel_FollowUpRecordsItsOwnEntryPoint(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	enableGuests(t, h)
	followUp := func(id uuid.UUID) string { return "/api/v1/tickets/" + id.String() + "/follow-up" }

	for _, tc := range []struct {
		name string
		do   func(id uuid.UUID) *http.Response
		want ticket.SourceChannel
	}{
		{"staff browser session", func(id uuid.UUID) *http.Response {
			return h.doSession(t, h.session(t, asStaff), http.MethodPost, followUp(id), nil)
		}, ticket.ChannelWeb},
		{"staff API key", func(id uuid.UUID) *http.Response {
			return h.do(t, http.MethodPost, followUp(id), nil)
		}, ticket.ChannelAPI},
		{"requester browser session", func(id uuid.UUID) *http.Response {
			return h.doSession(t, h.session(t, asReporter), http.MethodPost, followUp(id), nil)
		}, ticket.ChannelWeb},
		{"requester API key", func(id uuid.UUID) *http.Response {
			return h.doAsUser(t, http.MethodPost, followUp(id), nil)
		}, ticket.ChannelAPI},
	} {
		t.Run(tc.name, func(t *testing.T) {
			orig := raisedClosedTicket(t, h)
			// The original arrived by phone; the follow-up must not inherit that.
			require.NotEqual(t, tc.want, ticket.ChannelPhone)
			id := createdID(t, tc.do(orig.ID))
			require.Equal(t, tc.want, storedChannel(t, h, id))
		})
	}

	t.Run("guest link", func(t *testing.T) {
		orig, held := seedGuestTicket(t, h)
		closeGuestTicket(t, h, orig)
		res := h.doGuest(t, http.MethodPost, "/api/v1/guest/follow-up", held, nil)
		body := readBody(t, res)
		require.Equal(t, http.StatusCreated, res.StatusCode, body)
		var out struct {
			TrackingNumber string `json:"tracking_number"`
		}
		require.NoError(t, json.Unmarshal([]byte(body), &out))
		tk, err := h.ticketSvc.GetByTrackingNumber(context.Background(), ticket.TrackingNumber(out.TrackingNumber))
		require.NoError(t, err)
		require.Equal(t, ticket.ChannelGuest, tk.SourceChannel)
	})
}

// "phone" is the one value a person picks: staff, in a browser session, filing
// on someone's behalf.
func TestSourceChannel_PhoneOnlyFromStaffOnBehalfInTheWebForm(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	onBehalf := map[string]any{"reporter_user_id": h.userID.String(), "source_channel": "phone"}

	t.Run("staff and admin sessions, on behalf: phone", func(t *testing.T) {
		for _, who := range []who{asStaff, asAdmin} {
			id := createdID(t, h.doSession(t, h.session(t, who), http.MethodPost, "/api/v1/tickets", h.createBody(onBehalf)))
			require.Equal(t, ticket.ChannelPhone, storedChannel(t, h, id))
		}
	})
	t.Run("the response to staff says so", func(t *testing.T) {
		tk := decodeTicket(t, h.doSession(t, h.session(t, asStaff), http.MethodPost, "/api/v1/tickets", h.createBody(onBehalf)))
		require.Equal(t, ticket.ChannelPhone, tk.SourceChannel)
	})
	t.Run("on behalf without the field stays web", func(t *testing.T) {
		id := createdID(t, h.doSession(t, h.session(t, asStaff), http.MethodPost, "/api/v1/tickets",
			h.createBody(map[string]any{"reporter_user_id": h.userID.String()})))
		require.Equal(t, ticket.ChannelWeb, storedChannel(t, h, id))
	})

	refused := func(t *testing.T, do func() *http.Response) {
		t.Helper()
		before := ticketCount(t, h)
		res := do()
		body := readBody(t, res)
		require.Equal(t, http.StatusBadRequest, res.StatusCode, body)
		require.Equal(t, before, ticketCount(t, h), "a refused request writes nothing")
	}
	t.Run("a session that files for itself cannot say phone", func(t *testing.T) {
		refused(t, func() *http.Response {
			return h.doSession(t, h.session(t, asStaff), http.MethodPost, "/api/v1/tickets",
				h.createBody(map[string]any{"source_channel": "phone"}))
		})
	})
	t.Run("a requester session cannot say phone", func(t *testing.T) {
		refused(t, func() *http.Response {
			return h.doSession(t, h.session(t, asReporter), http.MethodPost, "/api/v1/tickets",
				h.createBody(map[string]any{"source_channel": "phone"}))
		})
	})
	t.Run("a session cannot name any other channel", func(t *testing.T) {
		for _, v := range []string{"api", "mcp", "guest", "email", "web", "", "PHONE", "sms"} {
			refused(t, func() *http.Response {
				return h.doSession(t, h.session(t, asStaff), http.MethodPost, "/api/v1/tickets",
					h.createBody(map[string]any{"reporter_user_id": h.userID.String(), "source_channel": v}))
			})
		}
	})
	t.Run("a number is refused, not decoded as something else", func(t *testing.T) {
		refused(t, func() *http.Response {
			return h.doSession(t, h.session(t, asStaff), http.MethodPost, "/api/v1/tickets",
				h.createBody(map[string]any{"reporter_user_id": h.userID.String(), "source_channel": 3}))
		})
	})
}

// The API cannot set the channel at all: not "phone", not its own value.
func TestSourceChannel_APIKeyAndOAuthClientCannotSetIt(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	client := h.newServiceClient(t, "intake", allScopes()...)

	for _, v := range []string{"phone", "api", "web", "mcp", "guest", "email", "bogus"} {
		for _, onBehalf := range []bool{false, true} {
			extra := map[string]any{"source_channel": v}
			if onBehalf {
				extra["reporter_user_id"] = h.userID.String()
			}
			for name, do := range map[string]func() *http.Response{
				"staff API key": func() *http.Response { return h.do(t, http.MethodPost, "/api/v1/tickets", h.createBody(extra)) },
				"admin API key": func() *http.Response { return h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets", h.createBody(extra)) },
				"requester API key": func() *http.Response {
					return h.doAsUser(t, http.MethodPost, "/api/v1/tickets", h.createBody(extra))
				},
				"OAuth client": func() *http.Response { return h.postBearer(t, client.Token, "/api/v1/tickets", h.createBody(extra)) },
			} {
				before := ticketCount(t, h)
				res := do()
				body := readBody(t, res)
				// A requester's key that also files on behalf is refused first, as
				// 403; either way nothing is written.
				require.Contains(t, []int{http.StatusBadRequest, http.StatusForbidden}, res.StatusCode, "%s %q: %s", name, v, body)
				if name != "requester API key" || !onBehalf {
					require.Equal(t, http.StatusBadRequest, res.StatusCode, "%s %q: %s", name, v, body)
				}
				require.Equal(t, before, ticketCount(t, h), "%s %q: a refused request writes nothing", name, v)
			}
		}
	}
}

// The guest form is an API too: it takes no channel from the visitor.
func TestSourceChannel_GuestFormCannotSetIt(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	enableGuests(t, h)
	for _, v := range []string{"phone", "guest", "web"} {
		before := ticketCount(t, h)
		res := h.doGuest(t, http.MethodPost, "/api/v1/guest/tickets", "", h.createBody(map[string]any{
			"guest_email": "someone@test.local", "guest_name": "Some One", "source_channel": v}))
		body := readBody(t, res)
		require.Equal(t, http.StatusBadRequest, res.StatusCode, body)
		require.Equal(t, before, ticketCount(t, h))
	}
}

// Operational data: staff see it, requesters and guests do not, on every
// response that carries a ticket.
func TestSourceChannel_StaffSeeItRequestersAndGuestsDoNot(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	mine, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelAPI,
		Subject: "Mine", CategoryID: h.catID, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	gtk, token := seedGuestTicket(t, h)

	keysOf := func(res *http.Response) map[string]json.RawMessage {
		body := readBody(t, res)
		require.Equal(t, http.StatusOK, res.StatusCode, body)
		var m map[string]json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(body), &m))
		return m
	}
	path := "/api/v1/tickets/" + mine.ID.String()

	t.Run("staff and admin: the ticket", func(t *testing.T) {
		for _, res := range []*http.Response{
			h.do(t, http.MethodGet, path, nil),
			h.doAsAdmin(t, http.MethodGet, path, nil),
			h.doSession(t, h.session(t, asStaff), http.MethodGet, path, nil),
		} {
			m := keysOf(res)
			require.JSONEq(t, `"api"`, string(m["source_channel"]))
		}
	})
	t.Run("requester: the ticket", func(t *testing.T) {
		for _, res := range []*http.Response{
			h.doAsUser(t, http.MethodGet, path, nil),
			h.doSession(t, h.session(t, asReporter), http.MethodGet, path, nil),
		} {
			require.NotContains(t, keysOf(res), "source_channel")
		}
	})
	t.Run("requester: the list", func(t *testing.T) {
		res := h.doAsUser(t, http.MethodGet, "/api/v1/tickets", nil)
		body := readBody(t, res)
		require.Equal(t, http.StatusOK, res.StatusCode, body)
		require.NotContains(t, body, "source_channel")
	})
	t.Run("staff: the list", func(t *testing.T) {
		// A staff list is the member's own tickets, so file one.
		createdID(t, h.do(t, http.MethodPost, "/api/v1/tickets", h.createBody(nil)))
		res := h.do(t, http.MethodGet, "/api/v1/tickets?scope=visible", nil)
		body := readBody(t, res)
		require.Equal(t, http.StatusOK, res.StatusCode, body)
		require.Contains(t, body, "source_channel")
	})
	t.Run("requester: what they just created", func(t *testing.T) {
		res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets", h.createBody(nil))
		body := readBody(t, res)
		require.Equal(t, http.StatusCreated, res.StatusCode, body)
		require.NotContains(t, body, "source_channel")
	})
	t.Run("guest: their ticket", func(t *testing.T) {
		require.NotNil(t, gtk)
		require.NotContains(t, keysOf(h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", token, nil)), "source_channel")
	})
}
