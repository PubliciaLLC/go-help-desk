package registration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// ── fakes ─────────────────────────────────────────────────────────────────────

type fakeStore struct {
	record    PendingRegistration
	upsertErr error
	getErr    error
	deleteErr error
	deleted   bool
}

func (f *fakeStore) Upsert(_ context.Context, pr PendingRegistration) (PendingRegistration, error) {
	if f.upsertErr != nil {
		return PendingRegistration{}, f.upsertErr
	}
	f.record = pr
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
		err := svc.Register(context.Background(), "a@other.com", "Alice", "pass", []string{"example.com"}, false)
		if !errors.Is(err, ErrDomainNotAllowed) {
			t.Fatalf("want ErrDomainNotAllowed, got %v", err)
		}
	})

	t.Run("open registration required", func(t *testing.T) {
		svc := NewService(&fakeStore{}, &fakeUsers{}, &fakeMailer{}, "http://localhost")
		err := svc.Register(context.Background(), "a@any.com", "Alice", "pass", nil, false)
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
		err := svc.Register(context.Background(), "a@any.com", "A", "pass", nil, true)
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
