package auth

import (
	"testing"
	"time"
)

// A key is dead from the instant of its expiry on (#523). The webhook pause
// check applies the same rule in SQL; authstore's test holds the two together.
func TestAPIKey_Expired(t *testing.T) {
	at := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		exp  *time.Time
		now  time.Time
		want bool
	}{
		{"no expiry never expires", nil, at.AddDate(100, 0, 0), false},
		{"before the expiry", &at, at.Add(-time.Nanosecond), false},
		{"at the expiry", &at, at, true},
		{"after the expiry", &at, at.Add(time.Nanosecond), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := (APIKey{ExpiresAt: tc.exp}).Expired(tc.now); got != tc.want {
				t.Fatalf("Expired = %v, want %v", got, tc.want)
			}
		})
	}
}
