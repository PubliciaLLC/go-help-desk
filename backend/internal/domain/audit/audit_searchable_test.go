package audit

import (
	"slices"
	"testing"
)

// The search key list is the whole of what a free-text search can reach (#629).
// Two ways it could be wrong: it holds a key Redact would hide (a search then
// confirms a hidden value), or a shown key has no spelling (it is silently
// unsearchable).
func TestSearchableKeys_EveryKeyIsShownByRedact(t *testing.T) {
	keys := SearchableKeys()
	if len(keys) != len(searchSpellings) {
		t.Errorf("%d spellings but %d searchable: a spelling does not normalise to a shown key", len(searchSpellings), len(keys))
	}
	for _, k := range keys {
		got, _ := Redact(map[string]any{k: "value"}, nil)
		if got[k] != "value" {
			t.Errorf("%q is searchable but Redact does not show it: a search would be an oracle on a hidden value", k)
		}
	}
}

// A key added to shownKeys is searchable only once it has a spelling in
// searchSpellings. Failing here, rather than leaving the key quietly out of
// search, is the point: add the spelling the writer uses.
func TestSearchableKeys_EveryShownKeyHasASpelling(t *testing.T) {
	spelled := map[string]bool{}
	for _, k := range SearchableKeys() {
		spelled[normaliseKey(k)] = true
	}
	for k := range shownKeys {
		if !isSensitive(k) && !spelled[k] {
			t.Errorf("shown key %q has no spelling in searchSpellings (search.go): add the one its writer uses", k)
		}
	}
}

// A shown key that also looks like a secret renders as a placeholder, so its
// value must never be searchable. TestShownKeys_NoneLooksLikeASecret says no
// such key exists today; this keeps the exclusion in force if one is added.
func TestSearchableKeys_LeavesOutAShownKeyThatLooksLikeASecret(t *testing.T) {
	shownKeys["apikeysecret"] = true
	searchSpellings = append(searchSpellings, "api_key_secret")
	defer func() {
		delete(shownKeys, "apikeysecret")
		searchSpellings = searchSpellings[:len(searchSpellings)-1]
	}()
	if slices.Contains(SearchableKeys(), "api_key_secret") {
		t.Error("a shown key that Redact turns into a placeholder is searchable")
	}
}

func TestSearchableKeys_IsSorted(t *testing.T) {
	if keys := SearchableKeys(); !slices.IsSorted(keys) {
		t.Error("SearchableKeys must be stable between calls: the list is sent as a query parameter")
	}
}
