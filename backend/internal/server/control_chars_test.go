package server_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// A NUL (or another control character, or invalid UTF-8) in free text used to
// reach Postgres, which refuses it: a 500, and for a create a tracking number
// already taken, so a gap in the sequence. It is validation now: 400 before
// anything is written.

func TestControlCharacters_AreRefusedAs400OnCreate(t *testing.T) {
	cases := []struct{ field, value string }{
		{"subject", "a\x00b"}, {"subject", "a\x07b"}, {"subject", "line\nbreak"},
		{"description", "a\x00b"}, {"description", "a\x1bb"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s/%q", tc.field, tc.value), func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			before := ticketCount(t, h)

			res := h.postOnBehalf(t, "staff", map[string]any{tc.field: tc.value})
			body := readBody(t, res)
			require.Equal(t, http.StatusBadRequest, res.StatusCode, body)
			require.Contains(t, body, tc.field)
			require.Equal(t, before, ticketCount(t, h))
		})
	}
}

func TestControlCharacters_NewlinesAndTabsStayInDescriptionAndReplies(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := decodeTicket(t, h.postOnBehalf(t, "staff", map[string]any{"description": "one\r\ntwo\tthree"}))
	res := h.do(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/replies", map[string]any{"body": "a\nb\tc"})
	require.Equal(t, http.StatusCreated, res.StatusCode, readBody(t, res))
}

func TestControlCharacters_AreRefusedAs400OnReply(t *testing.T) {
	for _, v := range []string{"a\x00b", "a\x07b"} {
		t.Run(fmt.Sprintf("%q", v), func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			tk := decodeTicket(t, h.postOnBehalf(t, "staff", nil))

			res := h.do(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/replies", map[string]any{"body": v})
			require.Equal(t, http.StatusBadRequest, res.StatusCode, readBody(t, res))
		})
	}
}
