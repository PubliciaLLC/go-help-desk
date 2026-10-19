package server_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/mcp"
)

// #290, "no enumeration surface": reporter_email must not tell a caller
// whether an address has an account unless it files a real ticket. So every
// refusal that does not depend on the address (the classification, the
// subject) comes first, and is identical for an address with an account and
// one without; and what only an address with no account can trip (the missing
// name, guest submission being off) is reachable only after all of that.

var badBodies = map[string]map[string]any{
	"a category that does not exist": {"category_id": "RANDOM"},
	"a type from another category":   {"type_id": "RANDOM"},
	"a blank subject":                {"subject": "   "},
}

// accountAddr has an account; strangerAddr has none. The stranger carries no
// guest_name on purpose: "name is required" is the refusal that gave it away.
func twoAddresses() (account, stranger map[string]any) {
	return map[string]any{"reporter_email": "user@test.local"},
		map[string]any{"reporter_email": "stranger@example.test"}
}

func fill(extra, bad map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range extra {
		out[k] = v
	}
	for k, v := range bad {
		if v == "RANDOM" {
			v = uuid.NewString()
		}
		out[k] = v
	}
	return out
}

func TestOnBehalf_REST_BadRequestAnswersTheSameForEveryAddress(t *testing.T) {
	for _, guestOn := range []bool{true, false} {
		for name, bad := range badBodies {
			t.Run(fmt.Sprintf("%s/guest_submission=%v", name, guestOn), func(t *testing.T) {
				h, cleanup := newHarness(t)
				defer cleanup()
				require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeyGuestSubmissionEnabled, guestOn))
				account, stranger := twoAddresses()
				// Fixed ids so the two bodies are the same request.
				if bad["category_id"] == "RANDOM" {
					bad = map[string]any{"category_id": uuid.NewString()}
				}
				if bad["type_id"] == "RANDOM" {
					bad = map[string]any{"type_id": uuid.NewString()}
				}

				a := h.postOnBehalf(t, "staff", fill(account, bad))
				aBody := readBody(t, a)
				s := h.postOnBehalf(t, "staff", fill(stranger, bad))
				sBody := readBody(t, s)

				require.Equal(t, http.StatusBadRequest, a.StatusCode, aBody)
				require.Equal(t, a.StatusCode, s.StatusCode)
				require.Equal(t, aBody, sBody, "byte-identical, so the address tells nothing")
			})
		}
	}
}

func TestOnBehalf_MCP_BadRequestAnswersTheSameForEveryAddress(t *testing.T) {
	for _, guestOn := range []bool{true, false} {
		for name, bad := range badBodies {
			t.Run(fmt.Sprintf("%s/guest_submission=%v", name, guestOn), func(t *testing.T) {
				h, c, cleanup := newMCPHarness(t)
				defer cleanup()
				require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeyGuestSubmissionEnabled, guestOn))
				account, stranger := twoAddresses()
				if bad["category_id"] == "RANDOM" {
					bad = map[string]any{"category_id": uuid.NewString()}
				}
				if bad["type_id"] == "RANDOM" {
					bad = map[string]any{"type_id": uuid.NewString()}
				}

				_, aErr := mcpCreate(t, c, fill(account, bad), h)
				_, sErr := mcpCreate(t, c, fill(stranger, bad), h)

				require.NotEmpty(t, aErr)
				require.Equal(t, aErr, sErr, "the same refusal, so the address tells nothing")
			})
		}
	}
}

// The rest of the bound (#290): once a request passes validation, an address
// is only ever learned by spending one of the caller's 40 units an hour. A
// missing guest_name is refused for every address alike, an account's included.

func TestOnBehalf_NoGuestNameIsRefusedAlikeForEveryAddress(t *testing.T) {
	for _, guestOn := range []bool{true, false} {
		t.Run(fmt.Sprintf("REST/guest_submission=%v", guestOn), func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeyGuestSubmissionEnabled, guestOn))
			account, stranger := twoAddresses()

			a := h.postOnBehalf(t, "staff", account)
			aBody := readBody(t, a)
			s := h.postOnBehalf(t, "staff", stranger)
			sBody := readBody(t, s)

			require.Equal(t, http.StatusBadRequest, a.StatusCode, aBody)
			require.Contains(t, aBody, "name is required")
			require.Equal(t, a.StatusCode, s.StatusCode)
			require.Equal(t, aBody, sBody)
		})
		t.Run(fmt.Sprintf("MCP/guest_submission=%v", guestOn), func(t *testing.T) {
			h, c, cleanup := newMCPHarness(t)
			defer cleanup()
			require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeyGuestSubmissionEnabled, guestOn))
			account, stranger := twoAddresses()

			_, aErr := mcpCreate(t, c, account, h)
			_, sErr := mcpCreate(t, c, stranger, h)

			require.Contains(t, aErr, "name is required")
			require.Equal(t, aErr, sErr)
		})
	}
}

// With guest submission off an address with no account is refused, and the
// refusal still costs a unit: 41 probes, the 41st is 429.
func TestOnBehalf_GuestSubmissionOff_ProbesAreCappedAt40(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeyGuestSubmissionEnabled, false))

	for i := 0; i < onBehalfMailBudget; i++ {
		res := h.postOnBehalf(t, "staff", map[string]any{
			"reporter_email": fmt.Sprintf("probe-%d@example.test", i), "guest_name": "Ada"})
		body := readBody(t, res)
		require.Equal(t, http.StatusBadRequest, res.StatusCode, "probe %d: %s", i, body)
		require.Contains(t, body, "guest submission")
	}
	res := h.postOnBehalf(t, "staff", map[string]any{"reporter_email": "probe-41@example.test", "guest_name": "Ada"})
	require.Equal(t, http.StatusTooManyRequests, res.StatusCode, readBody(t, res))
}

func TestOnBehalf_MCP_GuestSubmissionOff_ProbesAreCappedAt40(t *testing.T) {
	h, c, cleanup := newMCPHarness(t)
	defer cleanup()
	require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeyGuestSubmissionEnabled, false))

	for i := 0; i < onBehalfMailBudget; i++ {
		_, errText := mcpCreate(t, c, map[string]any{
			"reporter_email": fmt.Sprintf("probe-%d@example.test", i), "guest_name": "Ada"}, h)
		require.Contains(t, errText, "guest submission", "probe %d", i)
	}
	_, errText := mcpCreate(t, c, map[string]any{"reporter_email": "probe-41@example.test", "guest_name": "Ada"}, h)
	require.Contains(t, errText, "too many")
}

// Filing for an address that has an account spends a unit too.
func TestOnBehalf_AccountAddressSpendsAUnit(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeyGuestSubmissionEnabled, true))

	for i := 0; i < onBehalfMailBudget; i++ {
		res := h.postOnBehalf(t, "staff", map[string]any{"reporter_email": "user@test.local", "guest_name": "Ada"})
		require.Equal(t, http.StatusCreated, res.StatusCode, "filing %d: %s", i, readBody(t, res))
		res.Body.Close()
	}
	res := h.postOnBehalf(t, "staff", map[string]any{"reporter_email": "user@test.local", "guest_name": "Ada"})
	require.Equal(t, http.StatusTooManyRequests, res.StatusCode, readBody(t, res))
	// A picked id is not an address probe and is not counted.
	res = h.postOnBehalf(t, "staff", map[string]any{"reporter_user_id": h.userID.String()})
	require.Equal(t, http.StatusCreated, res.StatusCode, readBody(t, res))
}

// A request refused by validation spends nothing: it taught the caller
// nothing about any address.
func TestOnBehalf_ValidationRefusalsSpendNothing(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeyGuestSubmissionEnabled, true))

	for i := 0; i < onBehalfMailBudget+5; i++ {
		res := h.postOnBehalf(t, "staff", map[string]any{
			"reporter_email": "user@test.local", "guest_name": "Ada", "category_id": uuid.NewString()})
		require.Equal(t, http.StatusBadRequest, res.StatusCode, "bad category %d", i)
		res.Body.Close()
		res = h.postOnBehalf(t, "staff", map[string]any{"reporter_email": "user@test.local"})
		require.Equal(t, http.StatusBadRequest, res.StatusCode, "no name %d", i)
		res.Body.Close()
	}
	res := h.postOnBehalf(t, "staff", map[string]any{"reporter_email": "user@test.local", "guest_name": "Ada"})
	require.Equal(t, http.StatusCreated, res.StatusCode, readBody(t, res))
}

// A control character in guest_name or guest_phone is refused as validation,
// for every address. Before, an account's address ignored the fields (201) and
// an address with no account hit Postgres's refusal of NUL (500, and the unit
// refunded), so a miss was free.
func TestOnBehalf_ControlCharacterInGuestFieldsIsRefusedAlike(t *testing.T) {
	for _, field := range []string{"guest_name", "guest_phone"} {
		for _, bad := range []string{"a\x00b", "a\x07b"} {
			body := func(email string) map[string]any {
				m := map[string]any{"reporter_email": email, "guest_name": "Ada"}
				m[field] = bad
				return m
			}
			t.Run(fmt.Sprintf("REST/%s/%q", field, bad), func(t *testing.T) {
				h, cleanup := newHarness(t)
				defer cleanup()
				require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeyGuestSubmissionEnabled, true))

				a := h.postOnBehalf(t, "staff", body("user@test.local"))
				aBody := readBody(t, a)
				s := h.postOnBehalf(t, "staff", body("stranger@example.test"))
				sBody := readBody(t, s)

				require.Equal(t, http.StatusBadRequest, a.StatusCode, aBody)
				require.Equal(t, a.StatusCode, s.StatusCode)
				require.Equal(t, aBody, sBody)
			})
			t.Run(fmt.Sprintf("MCP/%s/%q", field, bad), func(t *testing.T) {
				h, c, cleanup := newMCPHarness(t)
				defer cleanup()

				_, aErr := mcpCreate(t, c, body("user@test.local"), h)
				_, sErr := mcpCreate(t, c, body("stranger@example.test"), h)

				require.NotEmpty(t, aErr)
				require.Equal(t, aErr, sErr)
			})
		}
	}
}

// Once a unit is taken it is never given back, not even when Create then
// fails: a refund after the address is resolved makes a miss free whenever the
// failure can be provoked for one kind of address only. 39 units, then a
// create that fails inside Create (the MCP server below has a ticket service
// whose store is down), then the next attempt is the 41st and gets 429.
type downStore struct{ ticket.Store }

func (downStore) CategoryExists(context.Context, uuid.UUID) (bool, error) {
	return false, fmt.Errorf("database is down")
}

func TestOnBehalf_ACreateThatFailsAfterTheUnitStillCostsIt(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeyGuestSubmissionEnabled, true))
	c := h.newServiceClient(t, "Filer", "tickets:read", "tickets:write")

	good := newMCPSession(t, h, c.Token)
	broken := newMCPSessionWith(t, h, c.Token, mcp.New(
		ticket.NewService(downStore{}, nil, nil, nil, nil, nil), nil, h.srv, h.categorySvc, h.srv))
	file := func(s *mcpSession, i int) (string, bool) {
		return s.tool("create_ticket", map[string]any{
			"subject": "x", "category_id": h.catID.String(),
			"reporter_email": fmt.Sprintf("walk-in-%d@example.test", i), "guest_name": "Ada"})
	}

	for i := 0; i < onBehalfMailBudget-1; i++ {
		text, isErr := file(good, i)
		require.False(t, isErr, "filing %d: %s", i, text)
	}
	_, isErr := file(broken, 1000)
	require.True(t, isErr, "the create was meant to fail")
	text, isErr := file(good, 1001)
	require.True(t, isErr)
	require.Contains(t, text, "too many", "the failed attempt kept its unit, so this is the 41st")
}
