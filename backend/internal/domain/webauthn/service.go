package webauthn

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	lib "github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
)

// challengeTTL is how long a staged challenge stays usable.
//
// It expires on a deadline from when it was minted and does NOT slide forward
// on use. A challenge left sitting in a long-lived session is a replay window
// that stays open as long as the tab does, and "it is refreshed whenever they
// touch it" is how a window stays open forever. Five minutes is generous for
// somebody tapping a key they are already holding.
const challengeTTL = 5 * time.Minute

// ErrChallengeExpired is a ceremony finished too long after it began.
var ErrChallengeExpired = errors.New("this request took too long; start again")

// ErrNoCredentials is an assertion attempted by an account that has none.
var ErrNoCredentials = errors.New("this account has no passkey registered")

// Service runs the two WebAuthn ceremonies. It owns no storage; the caller
// stages the challenge and persists the credential.
//
// The ceremonies themselves are github.com/go-webauthn/webauthn's, not ours.
// Registration and assertion have many ways to be subtly wrong and being
// exactly right about them is the whole value of the feature, so the only
// thing written here is the part that is specific to this application.
type Service struct {
	w   *lib.WebAuthn
	log *slog.Logger
}

// NewService builds the relying party from the instance's own base URL.
//
// baseURL is what the browser sees, so it decides the relying-party id and
// the acceptable origin. A credential is bound to that origin: change the
// instance's domain and every registered credential stops working and every
// user re-registers. That makes BASE_URL part of whether authentication works
// rather than only how links are built, which is why a wrong one is refused
// here rather than defaulted.
func NewService(baseURL, siteName string, log *slog.Logger) (*Service, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("BASE_URL %q is not a URL passkeys can be bound to", baseURL)
	}
	if log == nil {
		log = slog.Default()
	}
	if siteName == "" {
		siteName = "Go Help Desk"
	}
	w, err := lib.New(&lib.Config{
		RPID:          u.Hostname(),
		RPDisplayName: siteName,
		// Scheme and port included: an origin is not a hostname, and a
		// mismatch here is how a ceremony fails in a way nobody can read.
		RPOrigins: []string{u.Scheme + "://" + u.Host},
	})
	if err != nil {
		return nil, fmt.Errorf("configuring passkeys: %w", err)
	}
	return &Service{w: w, log: log}, nil
}

// Account is what the ceremonies need to know about a person. The caller
// supplies it; this package does not read the user table.
type Account struct {
	ID          uuid.UUID
	Email       string
	DisplayName string
	Credentials []Credential
}

// Staged is a challenge waiting to be answered, held by the caller in the
// session and never written to the account.
//
// ExpiresAt is set once, when the challenge is minted. Finish checks it, so a
// ceremony that sat in an abandoned tab cannot be completed later.
type Staged struct {
	Session   lib.SessionData
	ExpiresAt time.Time
}

func (s Staged) expired(now time.Time) bool { return now.After(s.ExpiresAt) }

// BeginRegistration mints a challenge for a new credential.
func (s *Service) BeginRegistration(_ context.Context, a Account) (*protocolCreation, Staged, error) {
	creation, session, err := s.w.BeginRegistration(newLibUser(a))
	if err != nil {
		return nil, Staged{}, fmt.Errorf("starting registration: %w", err)
	}
	return creation, Staged{Session: *session, ExpiresAt: time.Now().Add(challengeTTL)}, nil
}

// FinishRegistration verifies the attestation and returns the credential to
// store. Nothing is persisted here.
func (s *Service) FinishRegistration(_ context.Context, a Account, staged Staged, r *http.Request) (Credential, error) {
	if staged.expired(time.Now()) {
		return Credential{}, ErrChallengeExpired
	}
	c, err := s.w.FinishRegistration(newLibUser(a), staged.Session, r)
	if err != nil {
		return Credential{}, fmt.Errorf("%w: %s", ErrRegistrationRefused, err)
	}
	return fromLib(a.ID, c), nil
}

// BeginLogin mints a challenge for an assertion.
func (s *Service) BeginLogin(_ context.Context, a Account) (*protocolAssertion, Staged, error) {
	if len(a.Credentials) == 0 {
		return nil, Staged{}, ErrNoCredentials
	}
	assertion, session, err := s.w.BeginLogin(newLibUser(a))
	if err != nil {
		return nil, Staged{}, fmt.Errorf("starting sign-in: %w", err)
	}
	return assertion, Staged{Session: *session, ExpiresAt: time.Now().Add(challengeTTL)}, nil
}

// FinishLogin verifies an assertion and reports which credential answered,
// along with the sign count to record.
//
// The counter is returned for storage and is NOT enforced. Most authenticators
// report zero forever, so refusing a non-increasing counter refuses honest
// sign-ins. One case is worth noticing and is logged rather than refused: a
// counter that was previously non-zero going backwards is a genuine clone
// signal with no false positives. Detect, record, do not branch — the same
// shape as KnownMalicious in internal/reputation/circl.go.
func (s *Service) FinishLogin(ctx context.Context, a Account, staged Staged, r *http.Request) (Credential, error) {
	if staged.expired(time.Now()) {
		return Credential{}, ErrChallengeExpired
	}
	if len(a.Credentials) == 0 {
		return Credential{}, ErrNoCredentials
	}
	c, err := s.w.FinishLogin(newLibUser(a), staged.Session, r)
	if err != nil {
		return Credential{}, fmt.Errorf("%w: %s", ErrAssertionRefused, err)
	}
	used := fromLib(a.ID, c)

	for _, known := range a.Credentials {
		if string(known.CredentialID) != string(used.CredentialID) {
			continue
		}
		used.ID = known.ID
		if known.SignCount > 0 && used.SignCount < known.SignCount {
			s.log.WarnContext(ctx, "passkey signature counter went backwards; possible cloned authenticator",
				"user_id", a.ID, "credential", known.ID,
				"stored", known.SignCount, "asserted", used.SignCount)
		}
		break
	}
	return used, nil
}

// ErrRegistrationRefused and ErrAssertionRefused wrap whatever the library
// decided was wrong. The detail is kept for the log and not handed to the
// caller: "your key did not answer correctly" is all a browser can act on,
// and the specifics describe the ceremony rather than anything the person can
// fix.
var (
	ErrRegistrationRefused = errors.New("that key could not be registered")
	ErrAssertionRefused    = errors.New("that key did not answer correctly")
)
