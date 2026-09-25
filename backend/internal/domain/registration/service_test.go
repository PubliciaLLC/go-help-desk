package registration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

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

func (f *fakeStore) Delete(_ context.Context, _ uuid.UUID) error {
	f.deleted = true
	return f.deleteErr
}

type fakeUsers struct {
	created user.User
	err     error
	// existing is the set of addresses that already have an account, so the
	// pre-check can be exercised.
	existing map[string]bool
}

// GetByEmail reports an existing account, which stops a signup before the
// verification email goes out.
func (f *fakeUsers) GetByEmail(_ context.Context, email string) (user.User, error) {
	if f.existing[email] {
		return user.User{Email: email}, nil
	}
	return user.User{}, user.ErrNotFound
}

func (f *fakeUsers) Create(_ context.Context, in user.CreateUserInput) (user.User, error) {
	if f.err != nil {
		return user.User{}, f.err
	}
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
		err := svc.Register(context.Background(), "a@other.com", "Alice", "a-passphrase", []string{"example.com"}, false)
		if !errors.Is(err, ErrDomainNotAllowed) {
			t.Fatalf("want ErrDomainNotAllowed, got %v", err)
		}
	})

	t.Run("open registration required", func(t *testing.T) {
		svc := NewService(&fakeStore{}, &fakeUsers{}, &fakeMailer{}, "http://localhost")
		err := svc.Register(context.Background(), "a@any.com", "Alice", "a-passphrase", nil, false)
		if !errors.Is(err, ErrOpenRegistrationRequired) {
			t.Fatalf("want ErrOpenRegistrationRequired, got %v", err)
		}
	})

	t.Run("happy path — email sent", func(t *testing.T) {
		store := &fakeStore{}
		mailer := &fakeMailer{}
		svc := NewService(store, &fakeUsers{}, mailer, "http://localhost")
		err := svc.Register(context.Background(), "alice@example.com", "Alice", "password123", []string{"example.com"}, false)
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
		err := svc.Register(context.Background(), "a@any.com", "A", "a-passphrase", nil, true)
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
		_, err := svc.Verify(context.Background(), uuid.New())
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
		_, err := svc.Verify(context.Background(), uuid.New())
		if !errors.Is(err, ErrTokenExpired) {
			t.Fatalf("want ErrTokenExpired, got %v", err)
		}
	})

	t.Run("happy path — user created", func(t *testing.T) {
		store := &fakeStore{
			record: PendingRegistration{
				ID:           uuid.New(),
				Email:        "alice@example.com",
				DisplayName:  "Alice",
				PasswordHash: "hashed",
				ExpiresAt:    time.Now().Add(time.Hour),
			},
		}
		users := &fakeUsers{}
		svc := NewService(store, users, &fakeMailer{}, "http://localhost")
		u, err := svc.Verify(context.Background(), uuid.New())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if u.Email != "alice@example.com" {
			t.Errorf("unexpected user email: %s", u.Email)
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

		err := svc.Register(context.Background(), addr, "Attacker", "password123", nil, true)

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
		"password123", nil, true); err != nil {
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

	err := svc.Register(context.Background(), "Requested@Example.com", "Ada", "correct horse", nil, true)
	require.NoError(t, err)
	require.Equal(t, "canonical@example.com", mailer.to,
		"the mail must go to the address the store returned")
}

type recordingMailer struct{ to string }

func (m *recordingMailer) SendVerificationEmail(to, _, _ string) error {
	m.to = to
	return nil
}

// Signup is held to the same password minimum as every other path that sets
// one.
//
// It was the fifth path, and the one missed when the minimum was made a
// single rule: nothing checked the length here, and Verify creates the
// account from the stored hash, which skips the check in user.Service.Create.
// So a signup with an EMPTY password produced a real account whose login
// accepted an empty password. Self-service signup is off by default, which
// was the only thing standing in front of it.
func TestRegister_HoldsThePasswordMinimum(t *testing.T) {
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
			svc := NewService(&fakeStore{}, &fakeUsers{}, &fakeMailer{}, "http://localhost")
			err := svc.Register(context.Background(), "a@any.com", "Alice", tc.password, nil, true)

			if tc.wantErr {
				if !errors.Is(err, ErrPasswordTooShort) {
					t.Fatalf("a %d-character password was accepted (err=%v)", len(tc.password), err)
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

	err := svc.Register(context.Background(), "taken@any.com", "A", "a-passphrase", nil, true)
	if !errors.Is(err, ErrAlreadyRegistered) {
		t.Fatalf("want ErrAlreadyRegistered, got %v", err)
	}
	if mailer.sent {
		t.Error("a verification email was sent for an address that cannot be registered")
	}
}
