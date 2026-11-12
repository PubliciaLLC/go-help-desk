package auth

import (
	"encoding/json"
	"testing"
)

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

// A provider that sends amr as a single string must still sign in: before
// amr was read, nothing decoded it, so nothing could fail on it.
func TestOIDCClaims_AMRDecodesFromStringOrArray(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{`{"sub":"x","amr":["pwd","mfa"]}`, true},
		{`{"sub":"x","amr":"mfa"}`, true},
		{`{"sub":"x","amr":"pwd"}`, false},
		{`{"sub":"x"}`, false},
		{`{"sub":"x","amr":null}`, false},
	}
	for _, tc := range cases {
		var c OIDCClaims
		if err := json.Unmarshal([]byte(tc.raw), &c); err != nil {
			t.Fatalf("%s: %v", tc.raw, err)
		}
		if got := c.AssertedMFA(); got != tc.want {
			t.Fatalf("%s: AssertedMFA = %v, want %v", tc.raw, got, tc.want)
		}
	}
}
