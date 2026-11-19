package auth

import "testing"

// plain drops the unexported gate marker so rows can be compared as the five
// booleans the wire carries.
func plain(o Owed) Owed {
	o.gated = false
	return o
}

func TestSecondStep(t *testing.T) {
	cases := []struct {
		name            string
		f               SignInFacts
		want            Owed
		wantPasskeyOwed bool
	}{
		// The rows below are today's outcomes, with the passkey policy off.
		{"mfa off", SignInFacts{HasTOTP: true, Passkeys: 1, PasskeysUsable: true, Enforced: true}, Owed{}, false},
		{"totp", SignInFacts{MFAOn: true, HasTOTP: true, PasskeysUsable: true}, Owed{TOTP: true}, false},
		{"totp wins over a passkey", SignInFacts{MFAOn: true, HasTOTP: true, Passkeys: 1, PasskeysUsable: true}, Owed{TOTP: true}, false},
		{"passkey only", SignInFacts{MFAOn: true, Passkeys: 2, PasskeysUsable: true}, Owed{Passkey: true}, false},
		{"passkey only, passkeys unavailable is stranded", SignInFacts{MFAOn: true, Passkeys: 1}, Owed{Passkey: true, Stranded: true}, false},
		{"totp and passkey, passkeys unavailable uses totp", SignInFacts{MFAOn: true, HasTOTP: true, Passkeys: 1}, Owed{TOTP: true}, false},
		{"enforced, no factor", SignInFacts{MFAOn: true, Enforced: true, PasskeysUsable: true}, Owed{Enrol: true}, false},
		{"not enforced, no factor", SignInFacts{MFAOn: true, PasskeysUsable: true}, Owed{}, false},
		{"sso owes nothing", SignInFacts{MFAOn: true, Enforced: true, HasTOTP: true, SSO: true, PasskeysUsable: true}, Owed{}, false},
		{"sso with a passkey owes nothing without the policy", SignInFacts{MFAOn: true, Passkeys: 1, SSO: true, PasskeysUsable: true}, Owed{}, false},

		// The passkey policy.
		{"required: passkey and totp owes the passkey only",
			SignInFacts{MFAOn: true, Enforced: true, PasskeyRequired: true, PasskeysUsable: true, HasTOTP: true, Passkeys: 1},
			Owed{Passkey: true}, true},
		{"required: passkey only",
			SignInFacts{MFAOn: true, Enforced: true, PasskeyRequired: true, PasskeysUsable: true, Passkeys: 1},
			Owed{Passkey: true}, true},
		{"required: totp only verifies then enrols a key",
			SignInFacts{MFAOn: true, Enforced: true, PasskeyRequired: true, PasskeysUsable: true, HasTOTP: true},
			Owed{TOTP: true, PasskeyEnrol: true}, true},
		{"required: totp only, sso already asserted a factor",
			SignInFacts{MFAOn: true, Enforced: true, PasskeyRequired: true, PasskeysUsable: true, HasTOTP: true, SSO: true, SSOFactorProved: true},
			Owed{PasskeyEnrol: true}, true},
		{"required: totp only, sso asserted no factor",
			SignInFacts{MFAOn: true, Enforced: true, PasskeyRequired: true, PasskeysUsable: true, HasTOTP: true, SSO: true},
			Owed{TOTP: true, PasskeyEnrol: true}, true},
		{"required: no factor enrols a key and never TOTP",
			SignInFacts{MFAOn: true, Enforced: true, PasskeyRequired: true, PasskeysUsable: true},
			Owed{PasskeyEnrol: true}, true},
		{"required: sso, no factor",
			SignInFacts{MFAOn: true, Enforced: true, PasskeyRequired: true, PasskeysUsable: true, SSO: true},
			Owed{PasskeyEnrol: true}, true},
		{"required: sso, a registered key is used",
			SignInFacts{MFAOn: true, Enforced: true, PasskeyRequired: true, PasskeysUsable: true, SSO: true, Passkeys: 1},
			Owed{Passkey: true}, true},
		{"required: sso passkey-grade assertion owes nothing",
			SignInFacts{MFAOn: true, Enforced: true, PasskeyRequired: true, PasskeysUsable: true, SSO: true, SSOFactorProved: true, SSOPasskeyGrade: true},
			Owed{}, false},
		{"required: a passkey-grade claim from a password sign-in is not believed",
			SignInFacts{MFAOn: true, Enforced: true, PasskeyRequired: true, PasskeysUsable: true, SSOPasskeyGrade: true},
			Owed{PasskeyEnrol: true}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SecondStep(tc.f)
			if plain(got) != tc.want {
				t.Fatalf("SecondStep = %+v, want %+v", plain(got), tc.want)
			}
			if got.PasskeyOwed() != tc.wantPasskeyOwed {
				t.Fatalf("PasskeyOwed() = %v, want %v", got.PasskeyOwed(), tc.wantPasskeyOwed)
			}
			if got.Passed() != (tc.want == Owed{}) {
				t.Fatalf("Passed() = %v for %+v", got.Passed(), tc.want)
			}
		})
	}
}

// PasskeyHeld is the policy owing a key the account already has, and nothing
// else: a passkey-only account outside the policy owes a key too, and the
// onboarding rows (TOTP only, no factor) owe a first one.
func TestOwed_PasskeyHeld(t *testing.T) {
	cases := []struct {
		name string
		f    SignInFacts
		want bool
	}{
		{"required, key and totp", SignInFacts{PasskeyRequired: true, HasTOTP: true, Passkeys: 1}, true},
		{"required, key only", SignInFacts{PasskeyRequired: true, Passkeys: 1}, true},
		{"required, sso with a key", SignInFacts{PasskeyRequired: true, Passkeys: 1, SSO: true, SSOFactorProved: true}, true},
		{"required, totp only", SignInFacts{PasskeyRequired: true, HasTOTP: true}, false},
		{"required, nothing", SignInFacts{PasskeyRequired: true}, false},
		{"required, passkey-grade sso", SignInFacts{PasskeyRequired: true, Passkeys: 1, SSO: true, SSOPasskeyGrade: true}, false},
		{"not required, key only", SignInFacts{Passkeys: 1}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.f
			f.MFAOn, f.Enforced, f.PasskeysUsable = true, true, true
			if got := SecondStep(f).PasskeyHeld(); got != tc.want {
				t.Fatalf("PasskeyHeld() = %v, want %v", got, tc.want)
			}
		})
	}
}
