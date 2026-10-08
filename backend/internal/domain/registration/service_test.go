package registration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// ── fakes ─────────────────────────────────────────────────────────────────────

type fakeStore struct {
	record       PendingRegistration
	rewriteEmail string
	upsertErr    error
	getErr       error
	deleteErr    error
	deleted      bool
}

func (f *fakeStore) Upsert(_ context.Context, pr PendingRegistration) (PendingRegistration, error) {
	if f.upsertErr != nil {
		return PendingRegistration{}, f.upsertErr
	}
	f.record = pr
	// A real Upsert is ON CONFLICT ... RETURNING: what comes back is the row,
	// which need not equal what was handed in.
	if f.rewriteEmail != "" {
		pr.Email = f.rewriteEmail
	}
	return pr, nil
}

func (f *fakeStore) GetByToken(_ context.Context, _ uuid.UUID) (PendingRegistration, error) {
	if f.getErr != nil {
		return PendingRegistration{}, f.getErr
	}
	return f.record, nil
}

func (f *fakeStore) GetByID(_ context.Context, id uuid.UUID) (PendingRegistration, error) {
	if f.getErr != nil {
		return PendingRegistration{}, f.getErr
	}
	if f.record.ID != id {
		return PendingRegistration{}, ErrNotFound
	}
	r := f.record
	if f.rewriteEmail != "" {
		r.Email = f.rewriteEmail
	}
	return r, nil
}

func (f *fakeStore) Delete(_ context.Context, _ uuid.UUID) error {
	f.deleted = true
	return f.deleteErr
}

type fakeUsers struct {
	created user.User
	input   user.CreateUserInput
	err     error
	// existing is the set of addresses that already have an account, so the
	// pre-check can be exercised.
	existing map[string]bool
}

// EmailIsTaken reports an existing account, which stops a signup before the
// verification email goes out. Deleted accounts count here, as they do in the
// real store — that is the case the first version of this missed.
func (f *fakeUsers) EmailIsTaken(_ context.Context, email string) (bool, error) {
	return f.existing[email], nil
}

func (f *fakeUsers) Create(_ context.Context, in user.CreateUserInput) (user.User, error) {
	if f.err != nil {
		return user.User{}, f.err
	}
	f.input = in
	u := user.User{
		ID:          uuid.New(),
		Email:       in.Email,
		DisplayName: in.DisplayName,
		Role:        in.Role,
	}
	f.created = u
	return u, nil
}

type fakeMailer struct {
	sent bool
	err  error
}

func (f *fakeMailer) SendVerificationEmail(_, _, _ string) error {
	if f.err != nil {
		return f.err
	}
	f.sent = true
	return nil
}

// ── isEmailDomainAllowed ──────────────────────────────────────────────────────

func TestIsEmailDomainAllowed(t *testing.T) {
	cases := []struct {
		name    string
		email   string
		allowed []string
		openReg bool
		want    bool
	}{
		{"empty allowed + openReg", "a@example.com", nil, true, true},
		{"empty allowed + no openReg", "a@example.com", nil, false, false},
		{"matching domain", "a@example.com", []string{"example.com"}, false, true},
		{"non-matching domain", "a@other.com", []string{"example.com"}, false, false},
		{"case insensitive allowed", "a@EXAMPLE.COM", []string{"example.com"}, false, false},
		{"case insensitive list", "a@example.com", []string{"  EXAMPLE.COM  "}, false, true},
		{"multiple domains match", "a@b.com", []string{"a.com", "b.com"}, false, true},
		{"no @ in email", "badmail", []string{"example.com"}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isEmailDomainAllowed(tc.email, tc.allowed, tc.openReg); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// ── Register ──────────────────────────────────────────────────────────────────

func TestRegister(t *testing.T) {
	t.Run("domain not allowed", func(t *testing.T) {
		svc := NewService(&fakeStore{}, &fakeUsers{}, &fakeMailer{}, "http://localhost")
		err := svc.Register(context.Background(), "a@other.com", "Alice", []string{"example.com"}, false)
		if !errors.Is(err, ErrDomainNotAllowed) {
			t.Fatalf("want ErrDomainNotAllowed, got %v", err)
		}
	})

	t.Run("open registration required", func(t *testing.T) {
		svc := NewService(&fakeStore{}, &fakeUsers{}, &fakeMailer{}, "http://localhost")
		err := svc.Register(context.Background(), "a@any.com", "Alice", nil, false)
		if !errors.Is(err, ErrOpenRegistrationRequired) {
			t.Fatalf("want ErrOpenRegistrationRequired, got %v", err)
		}
	})

	t.Run("happy path — email sent", func(t *testing.T) {
		store := &fakeStore{}
		mailer := &fakeMailer{}
		svc := NewService(store, &fakeUsers{}, mailer, "http://localhost")
		err := svc.Register(context.Background(), "alice@example.com", "Alice", []string{"example.com"}, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !mailer.sent {
			t.Error("expected verification email to be sent")
		}
		if store.record.Email != "alice@example.com" {
			t.Errorf("unexpected stored email: %s", store.record.Email)
		}
	})

	t.Run("open registration", func(t *testing.T) {
		mailer := &fakeMailer{}
		svc := NewService(&fakeStore{}, &fakeUsers{}, mailer, "http://localhost")
		err := svc.Register(context.Background(), "a@any.com", "A", nil, true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !mailer.sent {
			t.Error("expected verification email to be sent")
		}
	})
}

// ── Verify ────────────────────────────────────────────────────────────────────

func TestVerify(t *testing.T) {
	t.Run("token not found", func(t *testing.T) {
		store := &fakeStore{getErr: errors.New("not found")}
		svc := NewService(store, &fakeUsers{}, &fakeMailer{}, "http://localhost")
		_, err := svc.Verify(context.Background(), uuid.New(), "a-real-passphrase")
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("token expired", func(t *testing.T) {
		store := &fakeStore{
			record: PendingRegistration{
				ID:        uuid.New(),
				Email:     "a@b.com",
				ExpiresAt: time.Now().Add(-time.Hour),
			},
		}
		svc := NewService(store, &fakeUsers{}, &fakeMailer{}, "http://localhost")
		_, err := svc.Verify(context.Background(), uuid.New(), "a-real-passphrase")
		if !errors.Is(err, ErrTokenExpired) {
			t.Fatalf("want ErrTokenExpired, got %v", err)
		}
	})

	t.Run("happy path — user created", func(t *testing.T) {
		store := &fakeStore{
			record: PendingRegistration{
				ID:          uuid.New(),
				Email:       "alice@example.com",
				DisplayName: "Alice",
				ExpiresAt:   time.Now().Add(time.Hour),
			},
		}
		users := &fakeUsers{}
		svc := NewService(store, users, &fakeMailer{}, "http://localhost")
		u, err := svc.Verify(context.Background(), uuid.New(), "a-real-passphrase")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if u.Email != "alice@example.com" {
			t.Errorf("unexpected user email: %s", u.Email)
		}
		// #360: the password is the one typed at verification, handed to
		// user.Create as plain text so it is held to the same minimum and
		// hashed the same way as every other account.
		if users.input.Password != "a-real-passphrase" {
			t.Errorf("account created with password %q, want the one given at verification", users.input.Password)
		}
		if !store.deleted {
			t.Error("expected pending record to be deleted")
		}
	})
}

// isEmailDomainAllowed does not inspect the address when open registration is
// on — it returns openReg without looking — so before validation any string at
// all was accepted, written to pending_registrations, and became a user account
// on verification. Proven before the fix: Register returned nil and stored
// "attacker@evil.test\r\nbcc: victim@example.com".
func TestRegister_RefusesAMalformedEmailBeforeStoringAnything(t *testing.T) {
	for _, addr := range []string{
		"attacker@evil.test\r\nBcc: victim@example.com",
		"a@b.test\nX-Injected: yes",
		"not an email at all",
		"Attacker <victim@example.com>",
	} {
		store := &fakeStore{}
		mailer := &fakeMailer{}
		svc := NewService(store, &fakeUsers{}, mailer, "https://help.example.com")

		err := svc.Register(context.Background(), addr, "Attacker", nil, true)

		if err == nil {
			t.Fatalf("Register accepted %q", addr)
		}
		if !errors.Is(err, ErrInvalidEmail) {
			t.Fatalf("Register(%q) = %v, want ErrInvalidEmail", addr, err)
		}
		// Nothing was written, and nothing was sent. A refusal that still
		// stored the row would leave the garbage behind.
		if store.record.Email != "" {
			t.Fatalf("a refused registration stored %q", store.record.Email)
		}
		if mailer.sent {
			t.Fatal("a refused registration still sent mail")
		}
	}
}

// A real signup must still work, and the address is normalised on the way in.
func TestRegister_AcceptsAndNormalisesARealAddress(t *testing.T) {
	store := &fakeStore{}
	svc := NewService(store, &fakeUsers{}, &fakeMailer{}, "https://help.example.com")

	if err := svc.Register(context.Background(), "  User@Example.COM  ", "User",
		nil, true); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if store.record.Email != "user@example.com" {
		t.Fatalf("stored %q, want it trimmed and lowercased", store.record.Email)
	}
}

// The verification mail must go to the address that is on the row, not the one
// in the request.
//
// They are normally the same string. They are not the same value: one is
// request text, the other is what the database returned, and only the second
// has actually been written down. Upsert is ON CONFLICT ... RETURNING, so on a
// repeat registration the row that comes back is the existing one.
//
// This also keeps request text out of the message, which is what
// go/email-injection is about — the mail is sent from this server's domain, so
// anything in it is said with this server's reputation behind it.
func TestRegister_MailsTheStoredAddressNotTheRequestedOne(t *testing.T) {
	store := &fakeStore{}
	store.rewriteEmail = "canonical@example.com"
	mailer := &recordingMailer{}
	svc := NewService(store, &fakeUsers{}, mailer, "https://help.example.com")

	err := svc.Register(context.Background(), "Requested@Example.com", "Ada", nil, true)
	require.NoError(t, err)
	require.Equal(t, "canonical@example.com", mailer.to,
		"the mail must go to the address the store returned")
}

type recordingMailer struct{ to string }

func (m *recordingMailer) SendVerificationEmail(to, _, _ string) error {
	m.to = to
	return nil
}

// Verification is held to the same password minimum as every other path that
// sets one, and a refused password does not use up the link: the person can
// try again with a longer one.
//
// Signup was the fifth such path, and the one missed when the minimum was made
// a single rule — an EMPTY password once produced a real account. Since #360
// the password is chosen here rather than at signup, so the rule moves with
// it.
func TestVerify_HoldsThePasswordMinimum(t *testing.T) {
	cases := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{name: "empty", password: "", wantErr: true},
		{name: "one character", password: "a", wantErr: true},
		{name: "one short of the minimum", password: "passwor", wantErr: true},
		{name: "exactly the minimum", password: "password"},
		{name: "comfortably over", password: "a-real-passphrase"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{record: PendingRegistration{
				ID: uuid.New(), Email: "a@any.com", DisplayName: "Alice", ExpiresAt: time.Now().Add(time.Hour),
			}}
			users := &fakeUsers{}
			svc := NewService(store, users, &fakeMailer{}, "http://localhost")
			_, err := svc.Verify(context.Background(), uuid.New(), tc.password)

			if tc.wantErr {
				if !errors.Is(err, ErrPasswordTooShort) {
					t.Fatalf("a %d-character password was accepted (err=%v)", len(tc.password), err)
				}
				if store.deleted || users.created.ID != uuid.Nil {
					t.Fatal("a refused password used up the link")
				}
				return
			}
			if err != nil {
				t.Fatalf("an acceptable password was refused: %v", err)
			}
		})
	}

	if user.MinPasswordLength != 8 {
		t.Fatalf("the cases above are written against a minimum of 8, not %d", user.MinPasswordLength)
	}
}

// A signup for an address that already has an account stops here, and says
// nothing about it to the person signing up.
//
// It used to go all the way through: the row was written, the email was sent,
// and the person clicked the link to be told their token was invalid or
// already used — which it was not. Somebody whose old account was deleted
// could never register again and was told every time that their link was
// broken.
//
// The endpoint's answer is unchanged, deliberately. A 202 either way is what
// stops signup being a way to find out who has an account here; what changes
// is that a link which cannot work is never sent.
func TestRegister_StopsWhenTheAddressAlreadyHasAnAccount(t *testing.T) {
	mailer := &fakeMailer{}
	svc := NewService(
		&fakeStore{},
		&fakeUsers{existing: map[string]bool{"taken@any.com": true}},
		mailer, "http://localhost")

	err := svc.Register(context.Background(), "taken@any.com", "A", nil, true)
	if !errors.Is(err, ErrAlreadyRegistered) {
		t.Fatalf("want ErrAlreadyRegistered, got %v", err)
	}
	if mailer.sent {
		t.Error("a verification email was sent for an address that cannot be registered")
	}
}

// A deleted account still owns its address, so a signup for it is stopped
// too.
//
// This was the case the first version of the check missed: it asked the login
// lookup, which hides deleted rows, while the unique constraint covers every
// row. So the signup was accepted, the email went out, and the link failed at
// the end with "invalid or already used token" — the exact dead end the check
// was added to prevent, still there for the exact person it was added for.
func TestRegister_ADeletedAccountStillOwnsItsAddress(t *testing.T) {
	mailer := &fakeMailer{}
	svc := NewService(
		&fakeStore{},
		&fakeUsers{existing: map[string]bool{"gone@any.com": true}},
		mailer, "http://localhost")

	err := svc.Register(context.Background(), "gone@any.com", "A", nil, true)
	if !errors.Is(err, ErrAlreadyRegistered) {
		t.Fatalf("want ErrAlreadyRegistered, got %v", err)
	}
	if mailer.sent {
		t.Error("a verification email was sent for an address that cannot be registered")
	}
}

// recordingQueue records what Register queues, without sending.
type recordingQueue struct{ events []notification.Event }

func (q *recordingQueue) Dispatch(_ context.Context, ev notification.Event) error {
	q.events = append(q.events, ev)
	return nil
}

// countingStore counts writes, so two requests can be compared.
type countingStore struct {
	fakeStore
	upserts int
}

func (c *countingStore) Upsert(ctx context.Context, pr PendingRegistration) (PendingRegistration, error) {
	c.upserts++
	return c.fakeStore.Upsert(ctx, pr)
}

// #348: a fresh address and one that already has an account do the same work
// on the request — one pending row written, one event queued — so the timing
// no longer says which it was. The event names the pending row and nothing
// else: no token, no address.
func TestRegister_FreshAndTakenAddressesDoTheSameWork(t *testing.T) {
	users := &fakeUsers{existing: map[string]bool{"taken@any.com": true}}
	for _, addr := range []string{"fresh@any.com", "taken@any.com"} {
		store, queue, mailer := &countingStore{}, &recordingQueue{}, &fakeMailer{}
		svc := NewService(store, users, mailer, "http://localhost", WithQueue(queue))
		_ = svc.Register(context.Background(), addr, "A", nil, true)

		if store.upserts != 1 || len(queue.events) != 1 {
			t.Fatalf("%s: %d pending rows written and %d events queued, want 1 and 1", addr, store.upserts, len(queue.events))
		}
		if mailer.sent {
			t.Fatalf("%s: mail was sent on the request", addr)
		}
		ev := queue.events[0]
		if ev.Type != notification.EventRegistrationVerify || ev.Recipient != "" || ev.GuestToken != "" {
			t.Fatalf("%s: unexpected event %+v", addr, ev)
		}
		if len(ev.Payload) != 1 || ev.Payload["pending_id"] != store.record.ID.String() {
			t.Fatalf("%s: payload must hold only the pending id, got %v", addr, ev.Payload)
		}
	}
}

func TestSendVerification(t *testing.T) {
	ctx := context.Background()
	fresh := PendingRegistration{
		ID: uuid.New(), Email: "fresh@any.com", Token: uuid.New(), ExpiresAt: time.Now().Add(time.Hour),
	}
	cases := []struct {
		name     string
		record   PendingRegistration
		existing map[string]bool
		id       uuid.UUID
		wantSent bool
	}{
		{"mails the stored address and token", fresh, nil, fresh.ID, true},
		{"a row that is gone sends nothing", fresh, nil, uuid.New(), false},
		{"an expired row sends nothing", PendingRegistration{ID: fresh.ID, Email: fresh.Email, Token: fresh.Token,
			ExpiresAt: time.Now().Add(-time.Minute)}, nil, fresh.ID, false},
		{"an address with an account sends nothing", fresh, map[string]bool{"fresh@any.com": true}, fresh.ID, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mailer := &sendLog{}
			svc := NewService(&fakeStore{record: tc.record}, &fakeUsers{existing: tc.existing}, mailer, "https://desk.example")
			if err := svc.SendVerification(ctx, tc.id); err != nil {
				t.Fatal(err)
			}
			if got := len(mailer.sent) == 1; got != tc.wantSent {
				t.Fatalf("sent=%v, want %v", got, tc.wantSent)
			}
			if tc.wantSent && (mailer.sent[0].to != fresh.Email || mailer.sent[0].token != fresh.Token.String()) {
				t.Fatalf("mailed %+v, want the stored address and token", mailer.sent[0])
			}
		})
	}
}

type sentMail struct{ to, token string }

type sendLog struct{ sent []sentMail }

func (m *sendLog) SendVerificationEmail(to, token, _ string) error {
	m.sent = append(m.sent, sentMail{to, token})
	return nil
}

// #370: the verification page looks the link up before asking for a password,
// so it can show which address and name the account is for (and give a
// password manager the address), and say a dead link is dead before anything
// is typed. Lookup answers exactly as Verify would and writes nothing.
func TestLookup(t *testing.T) {
	live := PendingRegistration{
		ID: uuid.New(), Email: "alice@any.com", DisplayName: "Alice", ExpiresAt: time.Now().Add(time.Hour),
	}
	cases := []struct {
		name    string
		store   *fakeStore
		wantErr error
	}{
		{"a live link", &fakeStore{record: live}, nil},
		{"an expired link", &fakeStore{record: PendingRegistration{ID: live.ID, Email: live.Email,
			ExpiresAt: time.Now().Add(-time.Minute)}}, ErrTokenExpired},
		{"an unknown link", &fakeStore{getErr: errors.New("no rows")}, ErrNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			users := &fakeUsers{}
			svc := NewService(tc.store, users, &fakeMailer{}, "http://localhost")
			pr, err := svc.Lookup(context.Background(), uuid.New())
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, "alice@any.com", pr.Email)
				require.Equal(t, "Alice", pr.DisplayName)
			}
			require.False(t, tc.store.deleted, "a lookup must not use up the link")
			require.Equal(t, uuid.Nil, users.created.ID, "a lookup must not create an account")
		})
	}
}
