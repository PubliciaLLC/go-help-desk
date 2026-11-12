package webauthn

import (
	"github.com/go-webauthn/webauthn/protocol"
	lib "github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
)

// Aliases so callers of this package do not have to import the library to
// hand its option objects back to a browser.
type (
	protocolCreation  = protocol.CredentialCreation
	protocolAssertion = protocol.CredentialAssertion
)

// libUser adapts an Account to the interface the library expects.
//
// WebAuthnID is the account's UUID bytes and nothing else. The specification
// is explicit that authentication decisions must be made on the user handle
// rather than the name or display name, and those two are an email address
// and a person's name here — both of which an administrator can change. A
// handle that moved when somebody was renamed would silently detach their
// credentials from them.
type libUser struct{ a Account }

func newLibUser(a Account) libUser { return libUser{a: a} }

func (u libUser) WebAuthnID() []byte {
	b := [16]byte(u.a.ID)
	return b[:]
}

func (u libUser) WebAuthnName() string { return u.a.Email }

func (u libUser) WebAuthnDisplayName() string {
	if u.a.DisplayName == "" {
		return u.a.Email
	}
	return u.a.DisplayName
}

func (u libUser) WebAuthnCredentials() []lib.Credential {
	out := make([]lib.Credential, 0, len(u.a.Credentials))
	for _, c := range u.a.Credentials {
		out = append(out, toLib(c))
	}
	return out
}

func toLib(c Credential) lib.Credential {
	tr := make([]protocol.AuthenticatorTransport, 0, len(c.Transports))
	for _, t := range c.Transports {
		tr = append(tr, protocol.AuthenticatorTransport(t))
	}
	return lib.Credential{
		ID:        c.CredentialID,
		PublicKey: c.PublicKey,
		Transport: tr,
		Flags: lib.CredentialFlags{
			BackupEligible: c.BackupEligible,
			BackupState:    c.BackupState,
		},
		Authenticator: lib.Authenticator{
			AAGUID:    c.AAGUID,
			SignCount: uint32(c.SignCount),
		},
	}
}

func fromLib(userID uuid.UUID, c *lib.Credential) Credential {
	tr := make([]string, 0, len(c.Transport))
	for _, t := range c.Transport {
		tr = append(tr, string(t))
	}
	return Credential{
		UserID:         userID,
		CredentialID:   c.ID,
		PublicKey:      c.PublicKey,
		SignCount:      int64(c.Authenticator.SignCount),
		Transports:     tr,
		AAGUID:         c.Authenticator.AAGUID,
		BackupEligible: c.Flags.BackupEligible,
		BackupState:    c.Flags.BackupState,
	}
}
