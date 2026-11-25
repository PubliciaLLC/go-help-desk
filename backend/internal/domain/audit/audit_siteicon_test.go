package audit

import "testing"

// The site icon entries (#708) record a digest, a number, a colour and a
// version; all four survive Redact as they are, none is mistaken for a secret,
// and anything else beside them is left out.
func TestRedact_SiteIconKeysAreShown(t *testing.T) {
	entry := map[string]any{
		"icon_sha256": "ab12", "icon_source_size": 1024, "icon_background": "#ffffff", "icon_version": "9f1c2a7b3d4e5f60",
		"icon_file_name": "logo.png",
	}
	got, _ := Redact(entry, nil)
	for _, k := range []string{"icon_sha256", "icon_source_size", "icon_background", "icon_version"} {
		if got[k] != entry[k] {
			t.Errorf("%s: got %v, want %v", k, got[k], entry[k])
		}
		if isSensitive(k) {
			t.Errorf("%q matches the secret-name deny-list", k)
		}
	}
	if _, ok := got["icon_file_name"]; ok {
		t.Error("a key nobody allow-listed must be left out")
	}
}
