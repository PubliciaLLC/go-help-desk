package server_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
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
