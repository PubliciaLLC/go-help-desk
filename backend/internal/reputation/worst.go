package reputation

import "time"

// ProviderNames is every provider this build can talk to, in the order the
// product presents them.
//
// Exported because "which providers are there" is now a question three
// packages ask — the settings validator, the settings reader and the lookup
// loop — and each answering it with its own literal slice is how one of them
// comes to be missing the fifth. The order is canonical rather than
// incidental: it breaks ties in Worst, so two providers returning equally
// serious verdicts always summarise to the same one.
func ProviderNames() []string {
	return []string{ProviderVirusTotal, ProviderMetaDefender, ProviderPolySwarm, ProviderCIRCL}
}

// NeedsKey reports whether a lookup at this provider is possible at all
// without an API key.
//
// The settings endpoint reads it to decide whether enabling a provider
// requires a key alongside. CIRCL is the false one, and an empty key box an
// operator feels obliged to fill is worse than no box at all.
//
// An unknown name answers true: a provider this build cannot talk to cannot
// be turned on by supplying nothing.
func NeedsKey(providerName string) bool {
	p, ok := providerByName(providerName)
	if !ok {
		return true
	}
	return p.needsKey
}

// severity ranks a verdict for the summary shown on the attachment row when
// several providers have answered.
//
//	detected  >  unseen  >  unscanned  >  clean  >  known
//
// Two of those orderings are not obvious and both are deliberate.
//
// UNSEEN OUTRANKS CLEAN. Only quarantined files are looked up here, so every
// hash reaching this table is one the local scanner has already called
// malicious. A file no reputation service has ever seen is a novel sample,
// which is more concerning than one seventy engines have examined and passed —
// not less. Ordered the other way round it would bury exactly the case worth a
// second look underneath a reassuring word.
//
// KNOWN IS THE FLOOR, because it is the only positive claim in the set.
// Everything above it is some flavour of "no findings"; this one is a named
// catalogue asserting the file is on record.
//
// UNAVAILABLE IS ABSENT, and that is the point of the second return value. It
// is not a verdict, it is a failed lookup, and it must never displace a real
// answer: VirusTotal timing out while CIRCL says "known" shows "known".
func severity(s State) (int, bool) {
	switch s {
	case Detected:
		return 5, true
	case Unseen:
		return 4, true
	case Unscanned:
		return 3, true
	case Clean:
		return 2, true
	case Known:
		return 1, true
	}
	return 0, false
}

// Worst is the index of the most serious verdict in states, or -1 when none of
// them is a verdict at all.
//
// An index rather than a State, because the caller needs to know WHICH
// provider produced the summary: a verdict attributed to nobody is the shape
// this feature refuses everywhere else, and the expanded view has to be able
// to point at the line the inline answer came from.
//
// Ties go to the earliest entry, so a caller passing providers in
// ProviderNames order gets a stable answer rather than one that depends on map
// iteration.
//
// -1 and not "unavailable" as a State: "every provider failed" and "nobody is
// configured" are different facts, and only the caller knows which it is
// holding.
func Worst(states []State) int {
	best, bestRank := -1, 0
	for i, s := range states {
		rank, ok := severity(s)
		if !ok || rank <= bestRank {
			continue
		}
		best, bestRank = i, rank
	}
	return best
}

// Recheckable reports whether a stored verdict is one Refresh would actually
// attempt, so that the UI can arm or disable a Check again control per
// provider without asking and being refused.
//
// The same two rules Refresh applies, and deliberately not a third: a verdict
// that cannot change is never re-checked, and one checked inside the floor is
// not re-checked either. The budget is not consulted, because it cannot be
// predicted — an allowance can be spent between the render and the click, and
// a control that promised otherwise would be lying either way round.
//
// fetchedAt is the time the payload reports, not the provider's analysis time:
// a verdict fetched by the very call that is rendering it has no stored fetch
// time, and treating that as the zero value would arm the control a second
// after a lookup.
func Recheckable(state State, fetchedAt, now time.Time) bool {
	if _, ok := severity(state); !ok {
		// Unavailable, and the zero State. Nothing is cached behind either, so
		// a re-check would be refused as "nothing to re-check".
		return false
	}
	if final(state) {
		return false
	}
	return now.Sub(fetchedAt) >= ManualRefreshFloor
}

// HashLink is where any analyst can read a public report on a hash, and it is
// deliberately not a function of the enabled providers.
//
// A link is not a lookup. A lookup is this server sending a customer's file
// hash to a third party — the operator's decision, their allowance, and what
// the per-provider toggles govern. A link sends nothing from this server: it
// is an anchor the analyst clicks in their own browser, under their own
// account or none, exactly as if they had copied the hash off the page and
// pasted it themselves, which they can do anyway because the hash is right
// there with a copy control.
//
// VirusTotal specifically, and not the others, because its page is the one
// every analyst already knows: no account needed and the full report renders
// logged out. MetaDefender's public page announces itself as a reduced view
// and CIRCL has no per-hash web UI at all, so neither can serve as the
// universal fallback.
func HashLink(sha256 string) string {
	if sha256 == "" {
		return ""
	}
	return (&VirusTotal{}).LinkURL(sha256)
}
