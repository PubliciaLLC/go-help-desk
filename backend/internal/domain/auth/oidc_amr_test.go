package auth

import "testing"

func TestOIDCClaims_AssertedMFA(t *testing.T) {
	cases := []struct {
		name string
		amr  []string
		want bool
	}{
		{"entra authenticator push", []string{"pwd", "rsa", "ngcmfa", "mfa"}, true},
		{"passkey", []string{"fido", "mfa"}, true},
		{"password only", []string{"pwd"}, false},
		{"no amr sent", nil, false},
		{"case matters (RFC 8176 values are lowercase)", []string{"MFA"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := (OIDCClaims{AMR: tc.amr}).AssertedMFA(); got != tc.want {
				t.Fatalf("AssertedMFA(%v) = %v, want %v", tc.amr, got, tc.want)
			}
		})
	}
}
