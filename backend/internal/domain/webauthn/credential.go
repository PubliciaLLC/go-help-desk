// Package webauthn holds the credentials a person registers to sign in with a
// passkey or a hardware security key.
//
// Storage and retrieval only, at this stage. The registration and assertion
// ceremonies are not here and will not be hand-written when they arrive: they
// belong to github.com/go-webauthn/webauthn, because being exactly right about
// them is the whole value of the feature. See docs/DESIGN.md → Authentication
// → Passkeys (WebAuthn).
package webauthn

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Credential is one registered authenticator.
type Credential struct {
	ID     uuid.UUID `json:"id"`
	UserID uuid.UUID `json:"-"`

	// CredentialID is what the authenticator generated, raw bytes. Unique
	// across the instance, enforced by the schema rather than by a check
	// here: see the column comment in migration 000029.
	CredentialID []byte `json:"-"`

	// PublicKey is verification material, COSE-encoded. Not a secret — the
	// point of the feature is that losing this table does not let an attacker
	// authenticate as anybody, which is not true of a TOTP secret.
	PublicKey []byte `json:"-"`

	// SignCount is recorded and never enforced. Most authenticators report
	// zero forever, so a "must increase" rule refuses honest sign-ins; a
	// counter that was previously non-zero going backwards is the one case
	// worth logging, and it gates nothing.
	SignCount int64 `json:"-"`

	// Transports is fed back on the sign-in challenge as
	// allowCredentials[].transports, so the browser can skip authenticators
	// that cannot satisfy the request. It is not kept for a future screen.
	Transports []string `json:"transports"`

	// AAGUID names the authenticator model. Nothing reads it yet; it is free
	// at registration and unrecoverable afterwards.
	AAGUID []byte `json:"-"`

	// BackupEligible and BackupState say whether this credential is synced
	// across devices rather than bound to one authenticator. They are the
	// only way an administrator can tell a hardware key from an iCloud or
	// Google passkey after the fact, which is the distinction the design's
	// phishing-resistance claim turns on.
	BackupEligible bool `json:"backup_eligible"`
	BackupState    bool `json:"backup_state"`

	// Name is what its owner called it, and may be empty. An unnamed
	// credential is described by its transports and its age rather than by
	// its AAGUID, which would leak the model into a label.
	Name string `json:"name"`

	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

// Synced reports whether this credential lives in a cloud keychain rather than
// on one authenticator.
//
// Both flags, not just BackupState: eligible-but-not-backed-up is a credential
// that CAN be synced and currently is not, which is not the same as one that
// never can be. An administrator asking "is our second factor hardware-backed"
// is asking about the first.
func (c Credential) Synced() bool { return c.BackupEligible }

// ErrCredentialExists is a credential id already registered on this instance.
//
// The schema decides this, not a lookup here: the unique index is what makes
// it true under concurrency, and a read-then-insert has a window between the
// read and the insert whatever the read said.
var ErrCredentialExists = errors.New("this credential is already registered")

// ErrNotFound is a credential that does not exist, or does not belong to the
// user asking about it. Deliberately one error for both: telling a caller
// that somebody else's credential id is real is an answer they were not
// entitled to.
var ErrNotFound = errors.New("credential not found")

// Store is persistence for registered credentials.
type Store interface {
	Create(ctx context.Context, c Credential) error
	ListByUser(ctx context.Context, userID uuid.UUID) ([]Credential, error)
	// GetByCredentialID is the sign-in lookup: an assertion carries the
	// credential id and nothing else.
	GetByCredentialID(ctx context.Context, credentialID []byte) (Credential, error)
	// CountForUser answers whether this account has a passkey, which is what
	// lets one satisfy the instance's MFA requirement.
	CountForUser(ctx context.Context, userID uuid.UUID) (int64, error)
	// Touch records a successful assertion. Called off the authentication
	// path: a sign-in must not wait on it or fail because of it.
	Touch(ctx context.Context, id uuid.UUID, signCount int64) error
	// Delete removes one credential, scoped to its owner in the statement.
	// Reports ErrNotFound when the id is not this user's.
	Delete(ctx context.Context, id, userID uuid.UUID) error
}
