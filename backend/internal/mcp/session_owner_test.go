package mcp

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// An SSE stream belongs to a user AND the credential that opened it (#495): the
// same person on a different key, or a key and a browser session, is a
// different owner.
func TestOwnerOf(t *testing.T) {
	u, k1, k2 := uuid.New(), uuid.New(), uuid.New()
	base := &authmw.Actor{UserID: u, Machine: true, APIKeyID: k1}

	cases := []struct {
		name string
		a, b *authmw.Actor
		same bool
	}{
		{"same key", base, &authmw.Actor{UserID: u, Machine: true, APIKeyID: k1}, true},
		{"same user, other key", base, &authmw.Actor{UserID: u, Machine: true, APIKeyID: k2}, false},
		{"same user, browser session", base, &authmw.Actor{UserID: u}, false},
		{"other user, same key id", base, &authmw.Actor{UserID: uuid.New(), Machine: true, APIKeyID: k1}, false},
		{"same OAuth client", &authmw.Actor{UserID: u, Machine: true, ClientID: "c"}, &authmw.Actor{UserID: u, Machine: true, ClientID: "c"}, true},
		{"other OAuth client", &authmw.Actor{UserID: u, Machine: true, ClientID: "c"}, &authmw.Actor{UserID: u, Machine: true, ClientID: "d"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.same, ownerOf(tc.a) == ownerOf(tc.b))
		})
	}
}
