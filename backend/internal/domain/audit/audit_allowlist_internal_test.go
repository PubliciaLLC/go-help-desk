package audit

import "testing"

// The secret-name deny-list runs on top of the allow-list, but only matters
// if an allow-listed name ever looks like a secret. Pin that none does, so a
// key added to shownKeys that the deny-list would catch is noticed here
// rather than silently hidden (#364 review).
func TestShownKeys_NoneLooksLikeASecret(t *testing.T) {
	for k := range shownKeys {
		if isSensitive(k) {
			t.Errorf("allow-listed key %q matches the secret-name deny-list", k)
		}
	}
}

// And the deny-list does apply to an allow-listed name: added for the length
// of this test, a secret-looking key is still shown only as a placeholder.
func TestRedact_DenyListAppliesToAllowListedNames(t *testing.T) {
	shownKeys["apikey"] = true
	defer delete(shownKeys, "apikey")
	got, _ := Redact(map[string]any{"api_key": "sk-live"}, nil)
	if got["api_key"] != redactedPlaceholder {
		t.Fatalf("got %v", got["api_key"])
	}
}
