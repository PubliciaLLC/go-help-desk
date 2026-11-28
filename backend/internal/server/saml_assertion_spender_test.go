package server

import (
	"context"
	"errors"
	"testing"

	"github.com/crewjam/saml"
)

func TestSAMLAssertionSpender(t *testing.T) {
	assertion := func(issuer, id string) *saml.Assertion {
		return &saml.Assertion{ID: id, Issuer: saml.Issuer{Value: issuer}}
	}

	tests := []struct {
		name      string
		assertion *saml.Assertion
		spend     func() (bool, error)
		wantErr   bool
		wantKey   string
	}{
		{
			name:      "first use passes, keyed by issuer and id under its own prefix",
			assertion: assertion("https://idp", "_abc"),
			spend:     func() (bool, error) { return true, nil },
			wantKey:   "saml-assertion\x00https://idp\x00_abc",
		},
		{
			name:      "second use is refused",
			assertion: assertion("https://idp", "_abc"),
			spend:     func() (bool, error) { return false, nil },
			wantErr:   true,
		},
		{
			name:      "an empty id is refused without touching the ledger",
			assertion: assertion("https://idp", ""),
			wantErr:   true,
		},
		{
			name:      "a ledger failure refuses",
			assertion: assertion("https://idp", "_abc"),
			spend:     func() (bool, error) { return false, errors.New("db down") },
			wantErr:   true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotKey string
			h := samlAssertionSpender{spend: func(_ context.Context, key string) (bool, error) {
				gotKey = key
				if tc.spend == nil {
					t.Fatal("ledger must not be written")
				}
				return tc.spend()
			}}
			err := h.HandleAssertion(tc.assertion)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantKey != "" && gotKey != tc.wantKey {
				t.Errorf("key = %q, want %q", gotKey, tc.wantKey)
			}
		})
	}
}
