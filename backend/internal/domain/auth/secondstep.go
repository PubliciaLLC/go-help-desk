package auth

// SignInFacts is everything that decides what a sign-in still owes after the
// first step (a password, or an identity provider).
type SignInFacts struct {
	// MFAOn is the instance toggle; Enforced is whether this role must have a
	// second factor (admin.MFARequiredFor, which already includes the roles
	// that must use a passkey).
	MFAOn, Enforced bool

	// PasskeyRequired is this role's passkey requirement with availability
	// already applied: false while passkeys cannot be used on this server
	// (#494), so the policy is suspended rather than locking anyone out.
	PasskeyRequired bool

	// PasskeysUsable is whether the server can check a passkey (#494).
	PasskeysUsable bool

	// HasTOTP and Passkeys are what the account holds.
	HasTOTP  bool
	Passkeys int

	// SSO is a sign-in through SAML or OIDC. SSOFactorProved is the provider
	// asserting that MFA ran; SSOPasskeyGrade that it was a passkey.
	SSO, SSOFactorProved, SSOPasskeyGrade bool
}

// Owed is what the sign-in must still do, matching the wire (the login
// response's mfa_needed, passkey_needed, mfa_enrollment_needed and
// passkey_enrollment_needed) plus the stranded refusal.
//
// TOTP and PasskeyEnrol can both be set: verify the authenticator, then
// register a key.
type Owed struct {
	TOTP         bool // enter a code from an existing authenticator
	Passkey      bool // use a registered passkey
	Enrol        bool // set up a TOTP authenticator
	PasskeyEnrol bool // register a first passkey
	// Stranded: the account's only second factor is a passkey the server cannot
	// check (#494). The sign-in is refused.
	Stranded bool

	// gated records that the passkey policy decided this outcome, which is what
	// PasskeyOwed reports. Passkey alone cannot say: a passkey-only account
	// outside the policy owes a passkey too, and must not be marked as one
	// that a TOTP code can never satisfy.
	gated bool
}

// Passed reports that nothing is owed: the session is fully signed in.
func (o Owed) Passed() bool {
	return !o.TOTP && !o.Passkey && !o.Enrol && !o.PasskeyEnrol && !o.Stranded
}

// PasskeyOwed reports that the passkey policy applies and is not yet met: this
// sign-in must end with a passkey, and a TOTP code proves a factor without
// passing MFA.
func (o Owed) PasskeyOwed() bool {
	return o.gated && (o.Passkey || o.PasskeyEnrol)
}

// PasskeyHeld reports that the passkey policy applies and the account already
// has a key, so it is that key that is owed: see SessionData.PasskeyHeld.
func (o Owed) PasskeyHeld() bool {
	return o.gated && o.Passkey
}

// SecondStep decides what a sign-in owes. It is the one table behind the
// password login, signup verification, SAML and OIDC.
func SecondStep(f SignInFacts) Owed {
	if f.PasskeyRequired && f.MFAOn {
		return passkeyRequiredStep(f)
	}
	if f.SSO {
		// An identity provider's sign-in counts as having passed MFA.
		return Owed{}
	}
	if !f.MFAOn {
		return Owed{}
	}

	// TOTP first when both exist: it is what the account had before, and a
	// person who enrolled a key alongside it has not asked to stop using the
	// authenticator they already have.
	//
	// Passkeys unavailable (#494): a key the server cannot check is not a
	// factor, so it is never asked for, and an account whose ONLY second factor
	// is such a key is refused rather than let in on its password alone.
	var o Owed
	switch {
	case f.HasTOTP:
		o.TOTP = true
	case f.Passkeys > 0:
		o.Passkey = true
		o.Stranded = !f.PasskeysUsable
	default:
		o.Enrol = f.Enforced
	}
	return o
}

// passkeyRequiredStep is SecondStep for a role that must use a passkey. TOTP
// still proves a factor, but never passes MFA here.
func passkeyRequiredStep(f SignInFacts) Owed {
	if f.SSO && f.SSOPasskeyGrade {
		return Owed{}
	}
	o := Owed{gated: true}
	switch {
	case f.Passkeys > 0:
		o.Passkey = true
	case f.HasTOTP:
		// A passkey registration needs a proved factor when the account holds
		// one (#333, #617): the authenticator, unless the provider just
		// asserted MFA.
		o.TOTP = !(f.SSO && f.SSOFactorProved)
		o.PasskeyEnrol = true
	default:
		// Never Enrol: enrolling TOTP would not satisfy the gate.
		o.PasskeyEnrol = true
	}
	return o
}
