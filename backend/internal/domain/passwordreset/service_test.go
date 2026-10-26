package passwordreset

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// ── fakes ─────────────────────────────────────────────────────────────────────

type fakeStore struct {
	issuedFor  uuid.UUID
	issuedHash string
	issuedTTL  time.Duration
	issues     int

	redeemedHash     string
	redeemedPassword string
	redeems          int
	redeemUser       uuid.UUID
	redeemErr        error
}

func (f *fakeStore) Issue(_ context.Context, id uuid.UUID, tokenHash string, ttl time.Duration) error {
	f.issuedFor, f.issuedHash, f.issuedTTL = id, tokenHash, ttl
	f.issues++
	return nil
}

func (f *fakeStore) Redeem(_ context.Context, tokenHash, passwordHash string) (uuid.UUID, error) {
	f.redeems++
	f.redeemedHash, f.redeemedPassword = tokenHash, passwordHash
	return f.redeemUser, f.redeemErr
}

// fakeUsers refuses a lookup unless it was told to expect one: Request must
// not look an account up, or its work would depend on whether one exists.
type fakeUsers struct {
	byEmail map[string]user.User
	lookups int
}

func (f *fakeUsers) GetByEmail(_ context.Context, email string) (user.User, error) {
	f.lookups++
	u, ok := f.byEmail[email]
	if !ok {
		return user.User{}, user.ErrNotFound
	}
	return u, nil
}

func (f *fakeUsers) HashPassword(plain string) (string, error) { return "hashed:" + plain, nil }

type fakeMailer struct {
	to, token, base string
	sent            int
	err             error
}

func (f *fakeMailer) SendPasswordResetEmail(_ context.Context, to, token, baseURL string) error {
	if f.err != nil {
		return f.err
	}
	f.to, f.token, f.base = to, token, baseURL
	f.sent++
	return nil
}

type fakeQueue struct{ got []notification.Event }

func (f *fakeQueue) Dispatch(_ context.Context, ev notification.Event) error {
	f.got = append(f.got, ev)
	return nil
}

type fakeAudit struct {
	audit.Store
	entries []audit.Entry
	err     error
}

func (f *fakeAudit) Create(_ context.Context, e audit.Entry) error {
	if f.err != nil {
		return f.err
	}
	f.entries = append(f.entries, e)
	return nil
}

type rig struct {
	svc    *Service
	store  *fakeStore
	users  *fakeUsers
	mailer *fakeMailer
	queue  *fakeQueue
	audit  *fakeAudit
	logs   *bytes.Buffer
	saml   bool
}

func newRig(accounts ...user.User) *rig {
	r := &rig{
		store: &fakeStore{}, users: &fakeUsers{byEmail: map[string]user.User{}},
		mailer: &fakeMailer{}, queue: &fakeQueue{}, audit: &fakeAudit{}, logs: &bytes.Buffer{},
	}
	for _, u := range accounts {
		r.users.byEmail[u.Email] = u
	}
	r.svc = NewService(r.store, r.users, r.mailer, r.queue, r.audit,
		func(context.Context) bool { return r.saml }, "https://help.example.com",
		slog.New(slog.NewTextHandler(r.logs, nil)))
	return r
}

func account(role user.Role) user.User {
	// With a password: "forgot password" is for an account that has one.
	return user.User{ID: uuid.New(), Email: "ada@example.com", DisplayName: "Ada", Role: role, PasswordHash: "hash"}
}

// ssoOnly is an account with no password of its own. It carries no identity
// provider subject unless the test sets one, so on its own it is also a local
// account created without a password: the guard tests the hash, not the
// subject.
func ssoOnly(role user.Role) user.User {
	u := account(role)
	u.PasswordHash = ""
	return u
}

func allow(uuid.UUID) bool { return true }

// ── Request ───────────────────────────────────────────────────────────────────

// #451: the request does the same work whether or not the address has an
// account: it queues one event and looks nothing up. Whether to send is decided
// when the mail would go (Send), as signup does (#348).
func TestRequest_QueuesOneEventAndLooksNothingUp(t *testing.T) {
	cases := []struct {
		name  string
		rig   *rig
		email string
		want  string
	}{
		{"an address with an account", newRig(account(user.RoleUser)), "ada@example.com", "ada@example.com"},
		{"an address with none", newRig(), "nobody@example.com", "nobody@example.com"},
		{"the same address in other case and padding", newRig(account(user.RoleUser)), "  Ada@Example.COM ", "ada@example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, tc.rig.svc.Request(context.Background(), tc.email))

			require.Len(t, tc.rig.queue.got, 1)
			ev := tc.rig.queue.got[0]
			require.Equal(t, notification.EventPasswordResetRequested, ev.Type)
			require.Equal(t, tc.want, ev.Recipient)
			require.Zero(t, tc.rig.users.lookups, "the request looked an account up")
			require.Zero(t, tc.rig.store.issues, "the request issued a token")
		})
	}
}

// A string that is not an address has nobody to mail. It is not an error the
// requester is told about (the endpoint answers 202 whatever it is given), and
// nothing is queued for it.
func TestRequest_ANonAddressQueuesNothing(t *testing.T) {
	for _, in := range []string{"", "   ", "not an address", "Eve <eve@example.com>"} {
		r := newRig()
		require.NoError(t, r.svc.Request(context.Background(), in), "%q", in)
		require.Empty(t, r.queue.got, "%q", in)
	}
}

// ── Send ──────────────────────────────────────────────────────────────────────

func request(t *testing.T, r *rig, email string) notification.Event {
	t.Helper()
	require.NoError(t, r.svc.Request(context.Background(), email))
	require.Len(t, r.queue.got, 1)
	return r.queue.got[0]
}

func TestSend_MailsALinkWhoseTokenIsStoredHashed(t *testing.T) {
	u := account(user.RoleUser)
	r := newRig(u)

	require.NoError(t, r.svc.Send(context.Background(), request(t, r, u.Email), allow))

	require.Equal(t, 1, r.mailer.sent)
	require.Equal(t, u.Email, r.mailer.to)
	require.Equal(t, "https://help.example.com", r.mailer.base, "the link's host comes from configuration")
	require.NotEmpty(t, r.mailer.token)
	require.Equal(t, u.ID, r.store.issuedFor)
	require.Equal(t, auth.HashToken(r.mailer.token), r.store.issuedHash, "the store holds the hash of what was mailed")
	require.NotEqual(t, r.mailer.token, r.store.issuedHash, "the raw token must not be what is stored")
	require.Equal(t, 30*time.Minute, r.store.issuedTTL, "a link is good for thirty minutes")
}

func TestSend_DecidesAtSendTime(t *testing.T) {
	disabled := account(user.RoleUser)
	disabled.Disabled = true
	oidc := ssoOnly(user.RoleUser)
	oidc.OIDCSubject = "oidc-sub"
	saml := ssoOnly(user.RoleUser)
	saml.SAMLSubject = "saml-sub"

	cases := []struct {
		name     string
		accounts []user.User
		saml     bool
		email    string
		budget   bool
		wantMail bool
	}{
		{"an account", []user.User{account(user.RoleUser)}, false, "ada@example.com", true, true},
		{"no account", nil, false, "ada@example.com", true, false},
		{"a disabled account", []user.User{disabled}, false, "ada@example.com", true, false},
		{"a non-administrator when SAML restricts passwords", []user.User{account(user.RoleStaff)}, true, "ada@example.com", true, false},
		{"a requester when SAML restricts passwords", []user.User{account(user.RoleUser)}, true, "ada@example.com", true, false},
		{"an administrator when SAML restricts passwords", []user.User{account(user.RoleAdmin)}, true, "ada@example.com", true, true},
		{"a local account created without a password, with no OIDC or SAML subject", []user.User{ssoOnly(user.RoleUser)}, false, "ada@example.com", true, false},
		{"an account that signs in through OIDC and has no password", []user.User{oidc}, false, "ada@example.com", true, false},
		{"an account that signs in through SAML and has no password", []user.User{saml}, false, "ada@example.com", true, false},
		{"an administrator with no password when SAML restricts passwords", []user.User{ssoOnly(user.RoleAdmin)}, true, "ada@example.com", true, false},
		{"an account whose budget is spent", []user.User{account(user.RoleUser)}, false, "ada@example.com", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(tc.accounts...)
			r.saml = tc.saml
			ev := request(t, r, tc.email)

			err := r.svc.Send(context.Background(), ev, func(uuid.UUID) bool { return tc.budget })

			require.NoError(t, err, "a decision not to mail is not a failure the worker should retry")
			if tc.wantMail {
				require.Equal(t, 1, r.mailer.sent)
				require.Equal(t, 1, r.store.issues)
			} else {
				require.Zero(t, r.mailer.sent, "a mail went out")
				require.Zero(t, r.store.issues, "a token was issued for nobody")
			}
		})
	}
}

// A mail that could not be sent is the worker's to retry: the error comes back.
func TestSend_AMailFailureIsReturnedForRetry(t *testing.T) {
	u := account(user.RoleUser)
	r := newRig(u)
	r.mailer.err = errors.New("smtp: connection refused")

	err := r.svc.Send(context.Background(), request(t, r, u.Email), allow)

	require.ErrorContains(t, err, "connection refused")
}

func TestSend_RefusesAnEventThatIsNotAReset(t *testing.T) {
	r := newRig(account(user.RoleUser))
	err := r.svc.Send(context.Background(), notification.Event{Type: notification.EventTicketCreated, Recipient: "ada@example.com"}, allow)
	require.Error(t, err)
	require.Zero(t, r.mailer.sent)
}

// ── Reset ─────────────────────────────────────────────────────────────────────

func TestReset_SetsThePasswordAndRecordsIt(t *testing.T) {
	u := account(user.RoleUser)
	r := newRig(u)
	r.store.redeemUser = u.ID

	require.NoError(t, r.svc.Reset(context.Background(), "the-raw-token", "a-new-long-password"))

	require.Equal(t, auth.HashToken("the-raw-token"), r.store.redeemedHash, "only the hash reaches the store")
	require.Equal(t, "hashed:a-new-long-password", r.store.redeemedPassword)
	require.Len(t, r.audit.entries, 1)
	e := r.audit.entries[0]
	require.Equal(t, "password_reset_by_email", e.Action, "distinct from the administrator's password_reset_by_admin")
	require.Equal(t, "user", e.EntityType)
	require.Equal(t, u.ID, e.EntityID)
	require.Nil(t, e.ActorID, "nobody was signed in, and inventing an actor would record a claim")
	require.Equal(t, true, e.After["sessions_revoked"])
	require.NotContains(t, e.After, "password", "no secret in the trail")
}

func TestReset_RefusesAPasswordTheRulesRefuseWithoutSpendingTheLink(t *testing.T) {
	cases := []struct {
		name     string
		password string
		want     error
	}{
		{"too short", "short", user.ErrPasswordTooShort},
		{"too long", string(make([]byte, user.MaxPasswordLength+1)), user.ErrPasswordTooLong},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig()
			err := r.svc.Reset(context.Background(), "tok", tc.password)
			require.ErrorIs(t, err, tc.want)
			require.Zero(t, r.store.redeems, "the link was used up by a password that was refused")
		})
	}
}

func TestReset_ALinkThatDoesNotWorkChangesNothing(t *testing.T) {
	r := newRig()
	r.store.redeemErr = ErrTokenInvalid

	err := r.svc.Reset(context.Background(), "tok", "a-new-long-password")

	require.ErrorIs(t, err, ErrTokenInvalid)
	require.Empty(t, r.audit.entries, "an audit entry for a reset that did not happen")
}

func TestReset_AFaultIsNotAVerdictOnTheLink(t *testing.T) {
	r := newRig()
	r.store.redeemErr = errors.New("connection reset")

	err := r.svc.Reset(context.Background(), "tok", "a-new-long-password")

	require.Error(t, err)
	require.NotErrorIs(t, err, ErrTokenInvalid)
}

// The password is already changed when the trail is written, so a failure to
// write it is logged and swallowed rather than reported as a failed reset
// (the same trade user.Service.ResetMFA makes, #306).
func TestReset_ALostAuditEntryDoesNotUndoTheReset(t *testing.T) {
	u := account(user.RoleUser)
	r := newRig(u)
	r.store.redeemUser = u.ID
	r.audit.err = errors.New("audit table unavailable")

	require.NoError(t, r.svc.Reset(context.Background(), "tok", "a-new-long-password"))
	require.Contains(t, r.logs.String(), "writing audit entry failed")
}

// A client that hangs up after the password is written must not decide whether
// it leaves a trace.
func TestReset_TheAuditEntryIsWrittenEvenIfTheCallerHungUp(t *testing.T) {
	u := account(user.RoleUser)
	r := newRig(u)
	r.store.redeemUser = u.ID
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.NoError(t, r.svc.Reset(ctx, "tok", "a-new-long-password"))
	require.Len(t, r.audit.entries, 1)
}
