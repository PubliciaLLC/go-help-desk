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

// The custom field entries (#603) show their field name, option text and path;
// the rest are flags and ids. All of them survive Redact, and none of them is
// mistaken for a secret on the way (TestShownKeys_NoneLooksLikeASecret covers
// the whole list; this names the ones this feature relies on).
func TestRedact_CustomFieldKeysAreShown(t *testing.T) {
	entry := map[string]any{
		"field_name": "Department", "field_type": "select", "sort_order": 3, "active": true,
		"option_value": "Marketing", "position": 2, "retired": false,
		"assignment_id": "a", "scope_type": "type", "scope_id": "s", "scope_path": "Hardware › Laptop",
		"visible_on_new": true, "required_on_new": false, "node_deleted": true,
	}
	got, _ := Redact(entry, nil)
	for k, v := range entry {
		if got[k] != v {
			t.Errorf("%s: got %v, want %v", k, got[k], v)
		}
	}
	for _, k := range []string{"field_name", "option_value", "scope_path"} {
		if isSensitive(k) {
			t.Errorf("%q matches the secret-name deny-list", k)
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
