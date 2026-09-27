package webauthn

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestNewService_BindsToTheInstancesOwnOrigin(t *testing.T) {
	// A credential is bound to an origin, so BASE_URL decides whether
	// authentication works at all — not just how links are built. A value
	// that cannot be bound to is refused here rather than defaulted, because
	// defaulting it would produce an instance whose passkeys silently never
	// verify.
	cases := []struct {
		name, baseURL string
		wantErr       bool
	}{
		{"a real https origin", "https://help.example.com", false},
		{"localhost with a port", "http://localhost:8080", false},
		{"empty", "", true},
		{"no host at all", "/just/a/path", true},
		{"not a URL", "://nonsense", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewService(tc.baseURL, "Help Desk", nil)
			if tc.wantErr {
				require.Error(t, err, "an unusable BASE_URL was accepted")
				return
			}
			require.NoError(t, err)
		})
	}
}

// The user handle is the account id and never the address or the name.
//
// The specification is explicit that authentication decisions must be made on
// the handle rather than on name or displayName. Both of those are editable
// by an administrator here, so a handle derived from either would detach a
// person from their own credentials the moment somebody corrected a typo in
// their email.
func TestUserHandle_IsTheAccountIDAndNothingElse(t *testing.T) {
	id := uuid.New()
	a := Account{ID: id, Email: "sam@example.com", DisplayName: "Sam Staff"}

	h := newLibUser(a).WebAuthnID()
	want := [16]byte(id)
	require.Equal(t, want[:], h)

	renamed := a
	renamed.Email = "sam.staff@example.com"
	renamed.DisplayName = "Samantha Staff"
	require.Equal(t, h, newLibUser(renamed).WebAuthnID(),
		"the handle moved when the account was renamed, which detaches its credentials")

	require.NotContains(t, string(h), "sam", "the address leaked into the handle")
}

func TestDisplayName_FallsBackToTheAddress(t *testing.T) {
	a := Account{ID: uuid.New(), Email: "nameless@example.com"}
	require.Equal(t, "nameless@example.com", newLibUser(a).WebAuthnDisplayName(),
		"an account with no display name showed an empty label on the key")
}

// A staged challenge expires on a fixed deadline and does not slide forward.
//
// One left sitting in a long-lived session is a replay window that stays open
// as long as the tab does. The same rule the MFA lockout follows.
func TestStagedChallenge_ExpiresAndDoesNotSlide(t *testing.T) {
	svc, err := NewService("https://help.example.com", "Help Desk", nil)
	require.NoError(t, err)
	ctx := context.Background()
	a := Account{ID: uuid.New(), Email: "sam@example.com", DisplayName: "Sam"}

	_, staged, err := svc.BeginRegistration(ctx, a)
	require.NoError(t, err)
	require.False(t, staged.expired(time.Now()), "a fresh challenge was already expired")
	require.True(t, staged.expired(time.Now().Add(challengeTTL+time.Second)),
		"the challenge never expires")

	// Finish refuses it, rather than handing a stale challenge to the library.
	stale := staged
	stale.ExpiresAt = time.Now().Add(-time.Second)
	req := httptest.NewRequest("POST", "/finish", strings.NewReader("{}"))
	_, err = svc.FinishRegistration(ctx, a, stale, req)
	require.ErrorIs(t, err, ErrChallengeExpired)

	_, err = svc.FinishLogin(ctx, withCredential(a), stale, req)
	require.ErrorIs(t, err, ErrChallengeExpired)
}

// An account with nothing registered cannot start an assertion. Without this
// the library is asked to build a challenge with an empty allow-list, which
// is a prompt the person cannot answer.
func TestBeginLogin_RefusesAnAccountWithNoCredentials(t *testing.T) {
	svc, err := NewService("https://help.example.com", "Help Desk", nil)
	require.NoError(t, err)
	a := Account{ID: uuid.New(), Email: "sam@example.com"}

	_, _, err = svc.BeginLogin(context.Background(), a)
	require.ErrorIs(t, err, ErrNoCredentials)

	_, _, err = svc.BeginLogin(context.Background(), withCredential(a))
	require.NoError(t, err, "an account WITH a credential could not start sign-in")
}

// Round-tripping through the library's own types must not quietly drop the
// three fields the table keeps for reasons nothing reads yet.
func TestCredentialRoundTrip_KeepsTransportsAndBackupFlags(t *testing.T) {
	uid := uuid.New()
	c := Credential{
		UserID:         uid,
		CredentialID:   []byte("cred"),
		PublicKey:      []byte("pk"),
		SignCount:      7,
		Transports:     []string{"usb", "nfc"},
		AAGUID:         []byte("model"),
		BackupEligible: true,
		BackupState:    true,
	}
	back := fromLib(uid, ptr(toLib(c)))

	require.Equal(t, c.Transports, back.Transports,
		"transports were lost; the sign-in challenge needs them to skip authenticators that cannot answer")
	require.Equal(t, c.AAGUID, back.AAGUID)
	require.True(t, back.BackupEligible,
		"the synced/hardware distinction was lost, which is the one the phishing-resistance claim turns on")
	require.True(t, back.Synced())
	require.EqualValues(t, 7, back.SignCount)
}

func withCredential(a Account) Account {
	a.Credentials = []Credential{{
		UserID: a.ID, CredentialID: []byte("cred-1"), PublicKey: []byte("pk"),
		Transports: []string{"usb"},
	}}
	return a
}

func ptr[T any](v T) *T { return &v }
