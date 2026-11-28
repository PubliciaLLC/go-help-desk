package webauthn

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// #616(b): a sign-in whose counter did not advance is refused.
//
// Driven through the real library with a software key, because the point is
// what the library reports for a given pair of counters, and that is not
// something a hand-built fake can vouch for.
func TestFinishLogin_RefusesACounterThatDidNotAdvance(t *testing.T) {
	const origin = "https://help.example.com"
	svc, err := NewService(origin, "Help Desk", nil)
	require.NoError(t, err)

	cases := []struct {
		name          string
		stored, asked uint32
		suspect       bool
	}{
		// The two counters the issue is about.
		{"equal and non-zero: a replay or a clone", 5, 5, true},
		{"lower than stored: a clone that signed in first", 5, 3, true},
		// What must keep working. Most authenticators report zero forever.
		{"zero forever is an honest authenticator", 0, 0, false},
		{"advanced by one", 5, 6, false},
		{"first use of a counting key", 0, 1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := testutil.NewSoftKey(t, "help.example.com", origin)
			acct := Account{ID: uuid.New(), Email: "sam@example.com", DisplayName: "Sam"}
			acct.Credentials = []Credential{{
				ID: uuid.New(), UserID: acct.ID, CredentialID: key.CredentialID,
				PublicKey: key.PublicKey(t), SignCount: int64(tc.stored),
			}}

			_, staged, err := svc.BeginLogin(context.Background(), acct)
			require.NoError(t, err)
			handle := [16]byte(acct.ID)
			body := key.Assert(t, staged.Session.Challenge, handle[:], tc.asked)
			req := httptest.NewRequest("POST", "/finish", strings.NewReader(string(body)))

			got, err := svc.FinishLogin(context.Background(), acct, staged, req)
			if !tc.suspect {
				require.NoError(t, err)
				require.EqualValues(t, tc.asked, got.SignCount)
				return
			}
			require.ErrorIs(t, err, ErrCloneSuspected)
			// Which key it was is what the caller audits.
			require.Equal(t, acct.Credentials[0].ID, got.ID)
		})
	}
}
