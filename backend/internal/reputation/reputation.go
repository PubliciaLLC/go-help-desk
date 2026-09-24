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

// State is what the provider was able to say. Six cases, and the distinctions
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

	// Known: a named feed has this exact hash in its catalogue — Microsoft
	// Windows, NSRL, a golden image, a commercial software database. The file
	// is not scanned at all; the answer comes straight from the hash match.
	//
	// Not Clean, and the difference is the reason this state exists. Clean
	// means engines ran and none of them flagged it. This means somebody
	// already knows the file and will say so by name. For the case this
	// product is good at — an IT team triaging the suspicious .exe a user
	// reported — "this is the signed Microsoft binary it claims to be" is the
	// answer they want and "0 of 12 engines flagged it" is a much weaker one.
	//
	// It is also the ONE state in this feature where reassurance is the
	// correct rendering. Everywhere else the rule is that an absence must
	// never read as safety; this is an exception because a named feed made a
	// positive claim, which is precisely what Clean, Unseen and Unscanned do
	// not have behind them.
	//
	// "known" and NOT "known good", deliberately, and the distinction is the
	// whole reason for the feed names in KnownFeeds. How strong the claim is
	// varies by feed and the state name must not overclaim on the weakest of
	// them:
	//
	//   - PolySwarm's "microsoft_windows" feed is an Authenticode signature
	//     assertion — a positive claim that the file is signed and trusted.
	//   - NSRL cataloguing means only that the file appeared in a known
	//     software distribution. NSRL catalogues hacking tools too, so it is
	//     emphatically not a statement that the file is safe.
	//
	// One state, and the feeds carry the weight. A renderer must show WHICH
	// feed is speaking — "known file, signed by Microsoft Windows" against
	// "known file, catalogued by NSRL" — because those two sentences are not
	// worth the same and neither of them is "known good".
	Known State = "known"
)

// Provider names. These are the values stored in
// attachment_reputation.provider and the names the per-provider settings are
// spelled with, so they are constants rather than literals: the column and the
// settings have to agree on the spelling, and a typo in either silently makes
// a cached verdict unreachable.
const (
	ProviderVirusTotal   = "virustotal"
	ProviderMetaDefender = "metadefender"
	ProviderPolySwarm    = "polyswarm"
	ProviderCIRCL        = "circl"
)

// providerFacts is everything this package knows about one service by name
// alone, in one place.
//
// One table rather than three switches, deliberately. The facts below were a
// switch each, and the one that was missing — whether a lookup is possible
// without a key — was missing because nothing forced the question to be asked
// when a provider was added. Adding a fifth here means answering it: a name
// with no entry is not a valid setting value, has no display name and cannot
// look anything up, so a half-added provider fails closed rather than
// silently.
type providerFacts struct {
	// displayName is the name as a person reads it, and it is what attributes
	// a verdict on screen.
	displayName string

	// needsKey is whether a lookup is possible at all without an API key.
	//
	// True for the three commercial services, where the key is the operator's
	// account and there is nothing to ask without one. False for CIRCL, which
	// is a public catalogue with no authentication: requiring a key there
	// would disable it forever, since there is no key an operator could
	// possibly supply.
	needsKey bool
}

// providerByName is the table. Unexported and read-only; see CanLookup,
// DisplayName and ValidProvider, which are the only readers.
func providerByName(name string) (providerFacts, bool) {
	switch name {
	case ProviderVirusTotal:
		return providerFacts{displayName: "VirusTotal", needsKey: true}, true
	case ProviderMetaDefender:
		return providerFacts{displayName: "MetaDefender", needsKey: true}, true
	case ProviderPolySwarm:
		return providerFacts{displayName: "PolySwarm", needsKey: true}, true
	case ProviderCIRCL:
		// "CIRCL" and not "CIRCL hashlookup", because this name goes into
		// sentences that attribute a claim — "CIRCL has no record of this
		// SHA-256" — and the organisation is what is making it. The full
		// attribution the CC-BY licence asks for ("Hash reputation data from
		// CIRCL hashlookup, Computer Incident Response Center Luxembourg,
		// CC-BY-4.0") is UI copy, not a display name.
		return providerFacts{displayName: "CIRCL", needsKey: false}, true
	}
	return providerFacts{}, false
}

// CanLookup reports whether a provider can make a request at all.
//
// The toggles decide who is ASKED; this decides who CAN be asked. A key for
// the three commercial ones, nothing for CIRCL, and never for a name this
// build cannot talk to.
//
// The settings endpoint refuses to enable a commercial provider with no key,
// so an enabled provider that fails here is one somebody wrote into the
// settings table by hand. It fails closed rather than sending an
// unauthenticated request to a service the operator has an account with.
//
// Stated once, here, because this question used to be asked at the call site —
// the lookup was refused on an empty key before anything had asked WHICH
// provider it was for, which is correct for three of the four and leaves the
// fourth permanently dead.
//
// The key is passed in rather than read here: this package has no access to
// settings, and the caller already holds the value. Nothing is done with it
// but compare it against "" — it is not stored, logged or wrapped into an
// error.
func CanLookup(providerName, apiKey string) bool {
	p, ok := providerByName(providerName)
	if !ok {
		return false
	}
	return !p.needsKey || apiKey != ""
}

// DisplayName is the provider's name as a person reads it.
//
// Here rather than on the Provider interface because it is a property of the
// name, not of a configured client: the payload needs it wherever a verdict
// came from, including one read straight out of the cache with no client
// built.
func DisplayName(providerName string) string {
	if p, ok := providerByName(providerName); ok {
		return p.displayName
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
// Next to the constants rather than in a caller, so a fifth implementation
// cannot leave the question behind. It is the predicate ProviderNames is
// checked against: a name in that list that this returns false for is a
// provider the settings reach and no lookup can serve.
func ValidProvider(name string) bool {
	_, ok := providerByName(name)
	return ok
}

// known reports whether s is one of the six declared states.
//
// The zero value is not one of them, which is the case that matters: an
// uninitialised Reputation has State "" and would otherwise travel all the way
// to a renderer that has no branch for it and draws it as safe.
func (s State) known() bool {
	switch s {
	case Unseen, Unscanned, Clean, Detected, Known, Unavailable:
		return true
	}
	return false
}

// Reputation is one provider's answer about one hash.
//
// Detected, Total and ThreatName are meaningful only when State is Detected or
// Clean; for the other four the provider gave us no numbers, and zeroes here
// mean "not recorded", never "nothing found". The database column is NULL in
// that case for the same reason. Known is one of the four: a file answered out
// of a catalogue is never scanned, so "0 of 0 engines" there would be a
// fabricated analysis attached to the one verdict staff are entitled to find
// reassuring.
type Reputation struct {
	State      State
	Detected   int    // engines flagging it
	Total      int    // engines that ran
	ThreatName string // the provider's name for it, may be empty

	// KnownFeeds names the feeds that carry the file, and is set only when
	// State is Known.
	//
	// The names are the evidence, and they are not decoration: the state says
	// only that somebody has the hash on file, and these say who — which is
	// what decides how much the claim is worth. An Authenticode signature
	// assertion from a vendor feed and an NSRL catalogue entry are different
	// things, and NSRL catalogues hacking tools. A bare "known" with no feed
	// named is a claim from nowhere, which is the shape this feature refuses
	// everywhere else.
	//
	// Sorted and de-duplicated by the provider, because two catalogue entries
	// can match the same hash and the order is the server's. The strings are
	// the feed identifiers as the provider spells them; rendering them for a
	// person is the caller's job.
	//
	// Empty on every other state, and empty is the honest rendering of
	// "nobody vouched for this file".
	KnownFeeds []string

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
// an analyst can read the full report in their own browser, beside that
// provider's own verdict. It is therefore useful on an instance that has
// configured no API key at all. The unconditional hash link every attachment
// carries whatever is enabled is a different thing — see HashLink.
//
// Lookup returns Unavailable rather than failing open on any error. The error
// is for the operator; the State is what the caller renders.
type Provider interface {
	Name() string
	Lookup(ctx context.Context, sha256 string) (Reputation, error)
	LinkURL(sha256 string) string
}
