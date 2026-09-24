// Package reputation asks a third-party service what it already knows about a
// file, identified by its SHA-256 and nothing else.
//
// It sits beside internal/antivirus rather than under internal/domain for the
// same reason antivirus does: both are a network call to an external service,
// which is infrastructure, and internal/domain may not import infrastructure.
//
// It shares antivirus's central lesson, too. A lookup that failed, timed out
// or ran out of quota reports Unavailable. It never reports Clean. Clean is
// reachable only from a completed lookup that came back with no detections —
// as the providers themselves say, a missing report is not a statement that a
// file is safe.
//
// Nothing here uploads a file. A hash lookup tells a provider that someone has
// seen a file they already have on record; a submission hands them a
// customer's document. The second is a different feature with different terms
// attached, and is deliberately not built.
package reputation

import (
	"context"
	"time"
)

// State is what the provider was able to say. Five cases, and the distinctions
// between them are the whole value of the type.
type State string

const (
	// Unseen: the provider has no record of this hash at all. Not a verdict.
	Unseen State = "unseen"

	// Unscanned: the provider knows the hash but holds no verdict for it —
	// MetaDefender's 404011, "scanned as a private file and we do not store
	// the file". Neither Unseen nor Clean, and it must never collapse into
	// either.
	Unscanned State = "unscanned"

	// Clean: the lookup completed and no engine flagged the file. Only ever
	// reachable from a completed lookup.
	Clean State = "clean"

	// Detected: at least one engine flagged it.
	Detected State = "detected"

	// Unavailable: no answer. Unreachable, timed out, rate-limited, quota
	// exhausted, bad key. A state and not an error return, so that a caller
	// which forgets to check err still cannot render a failure as "no
	// detections".
	Unavailable State = "unavailable"
)

// Provider names. These are the values stored in
// attachment_reputation.provider and accepted by the
// attachment_reputation_provider setting, so they are constants rather than
// literals: the column and the setting have to agree on the spelling, and a
// typo in either silently makes a cached verdict unreachable.
const (
	ProviderVirusTotal   = "virustotal"
	ProviderMetaDefender = "metadefender"
)

// DisplayName is the provider's name as a person reads it.
//
// Here rather than on the Provider interface because it is a property of the
// name, not of a configured client: the payload needs it wherever a verdict
// came from, including one read straight out of the cache with no client
// built. An unknown name comes back unchanged, which is the honest rendering
// of a row written by a version of this that knew something we do not.
func DisplayName(provider string) string {
	switch provider {
	case ProviderVirusTotal:
		return "VirusTotal"
	case ProviderMetaDefender:
		return "MetaDefender"
	}
	// Empty, not the raw value.
	//
	// The setting is operator-typed, and the reader is staff looking at a
	// malware verdict. Passing an unrecognised value through puts whatever was
	// typed into a sentence that attributes a claim — "xyzzy has never seen
	// this file" — which reads as a service that exists. An empty name lets
	// the caller fall back to unattributed wording, which is true: we do not
	// know who said it, so we do not say.
	//
	// Not reachable while the settings handler refuses an unknown provider,
	// but this function is exported and that validation was itself missing
	// until recently.
	return ""
}

// ValidProvider reports whether name is a provider this build can talk to.
//
// Next to the constants rather than in the settings handler, so a third
// implementation cannot leave the validation behind. That is the failure this
// function exists for: the setting shipped with no validation at all — it was
// accepted on write, fell back to the default on read, and told the operator
// nothing, which is the same "accepted and then ignored" shape the rest of
// this handler refuses.
func ValidProvider(name string) bool {
	return name == ProviderVirusTotal || name == ProviderMetaDefender
}

// known reports whether s is one of the five declared states.
//
// The zero value is not one of them, which is the case that matters: an
// uninitialised Reputation has State "" and would otherwise travel all the way
// to a renderer that has no branch for it and draws it as safe.
func (s State) known() bool {
	switch s {
	case Unseen, Unscanned, Clean, Detected, Unavailable:
		return true
	}
	return false
}

// Reputation is one provider's answer about one hash.
//
// Detected, Total and ThreatName are meaningful only when State is Detected or
// Clean; for the other three the provider gave us no numbers, and zeroes here
// mean "not recorded", never "nothing found". The database column is NULL in
// that case for the same reason.
type Reputation struct {
	State      State
	Detected   int    // engines flagging it
	Total      int    // engines that ran
	ThreatName string // the provider's name for it, may be empty

	// AnalysedAt is when the PROVIDER last analysed the file, not when we
	// asked. Nil when they never did, or did not say. When we asked is
	// attachment_reputation.fetched_at, which is our own clock and always
	// known.
	AnalysedAt *time.Time

	// FetchedAt is when WE last fetched this verdict, as opposed to when the
	// provider analysed the file. It is the field both expiry rules are
	// decided on: the automatic re-check compares it against the configured
	// interval, and the manual one against ManualRefreshFloor.
	//
	// Read-side only. A Store fills it in on Get; Put ignores it, because the
	// row's fetched_at is the database's own clock — one clock decides one
	// timeline. A provider never sets it, and the zero value means the age of
	// this verdict is unknown, which is treated as "do not re-fetch" rather
	// than "fetched in year one".
	FetchedAt time.Time
}

// Provider is one reputation service.
//
// LinkURL takes no key and makes no request: it is a string the UI renders so
// an analyst can read the full report in their own browser. It is therefore
// useful on an instance that has configured no API key at all, which is why
// the provider setting is meaningful even with the lookup switched off.
//
// Lookup returns Unavailable rather than failing open on any error. The error
// is for the operator; the State is what the caller renders.
type Provider interface {
	Name() string
	Lookup(ctx context.Context, sha256 string) (Reputation, error)
	LinkURL(sha256 string) string
}
