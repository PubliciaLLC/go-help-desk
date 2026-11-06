package reputation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"time"
)

// psBaseURL is the real service. WithBaseURL replaces it in tests.
const psBaseURL = "https://api.polyswarm.network"

// psCommunity scopes the search. The parameter is not optional: an unscoped
// search is not the search this endpoint documents.
const psCommunity = "default"

// psStateKnownGood is the one value of the response's `state` field we branch
// on. Everything else — SETTLED, AWAITING and whatever they add next — is read
// from the counts instead, so a new bounty state does not become a new arm
// here.
const psStateKnownGood = "KNOWN_GOOD"

// psToolPolyunite is the metadata entry that carries the consensus malware
// name. Looked up by this key and never by position: see psThreatName.
const psToolPolyunite = "polyunite"

// PolySwarm is the third provider, and the first that can answer out of a
// catalogue rather than out of a scan.
//
// It is also the only one of the three whose published terms contain no
// commercial-use, non-commercial, personal-use or internal-use restriction,
// which is what makes it the option for an operator whose lawyer rejects the
// other two. The caveat, recorded rather than buried: those terms are dated
// 30 April 2018 and predate the v3 API this file talks to.
//
// Free tier is 60 calls an hour — a shape neither of the other two has, which
// is why Budget grew an hourly bucket rather than reusing VirusTotal's minute
// one.
type PolySwarm struct {
	client
}

// NewPolySwarm builds the provider. As with the other two, an empty key means
// no lookup rather than a lookup that fails.
func NewPolySwarm(apiKey string, opts ...Option) *PolySwarm {
	return &PolySwarm{client: newClient(apiKey, psBaseURL, opts...)}
}

var _ Provider = (*PolySwarm)(nil)

func (p *PolySwarm) Name() string { return ProviderPolySwarm }

// LinkURL is built from the hash we already hold rather than echoed from the
// response's `permalink` field: that field arrives in two different forms, and
// one recorded response carried the literal string "None" where the hash
// belonged. Needs no key and makes no request.
func (p *PolySwarm) LinkURL(sha256 string) string {
	return "https://polyswarm.network/scan/results/file/" + sha256
}

// psSearch is the slice of the v3 hash search we read.
//
// `result` is an array of artifact instances — separate submissions of the
// same bytes — because the endpoint is a search. We read the first, which is
// safe precisely because the search is keyed by the hash: every entry
// describes the same file, and a file a vendor feed vouches for is vouched for
// in all of them.
type psSearch struct {
	Result []psInstance `json:"result"`
}

type psInstance struct {
	State string `json:"state"`

	// Detections is a POINTER because the field arrives as null, not as
	// zeroes, on an instance that has not settled. Decoded into a value type
	// it would read as 0 of 0 — a missing lookup wearing a clean verdict's
	// clothes, and the most reassuring sentence this feature can print.
	Detections *struct {
		Malicious int `json:"malicious"`
		Total     int `json:"total"`
	} `json:"detections"`

	// KnownGood is PolySwarm's spelling, kept because it is their wire field:
	// one entry per catalogue feed, null on an ordinary artifact. The feed's
	// name is the entry's tool, and the feed is what decides how much the
	// answer is worth — which is why our state is Known and not "known good".
	KnownGood []struct {
		Tool string `json:"tool"`
	} `json:"known_good"`

	// Metadata carries the enrichment tools: exiftool, hash, pefile, the
	// sandboxes, and polyunite. Order is unspecified.
	Metadata []struct {
		Tool         string `json:"tool"`
		ToolMetadata struct {
			MalwareFamily string `json:"malware_family"`
		} `json:"tool_metadata"`
	} `json:"metadata"`

	// LastScanned is when PolySwarm last analysed the file, not when we asked.
	LastScanned string `json:"last_scanned"`
}

func (p *PolySwarm) Lookup(ctx context.Context, sha256 string) (Reputation, error) {
	if p.apiKey == "" {
		return unavailable(ErrNoAPIKey)
	}

	// The hash goes in the query string rather than the path, which is what
	// this endpoint takes. The key does not: a key in a query string lands in
	// every access log and proxy between here and there.
	q := url.Values{"hash": {sha256}, "community": {psCommunity}}

	// "Authorization", and the key is the WHOLE header value. No "Bearer "
	// prefix — measured, not read: with one, the server treats the prefix as
	// part of the key and rejects it on length.
	status, body, err := p.get(ctx, "/v3/search/hash/sha256?"+q.Encode(), "Authorization")
	if err != nil {
		return unavailable(fmt.Errorf("polyswarm lookup: %w", err))
	}

	switch status {
	case http.StatusOK:
		return psVerdict(body)

	case http.StatusNoContent:
		// They answered, and the answer is that they hold nothing for this
		// hash. Not a verdict, and never Clean.
		return Reputation{State: Unseen}, nil

	case http.StatusTooManyRequests:
		// BOTH meanings of 429 land here, deliberately.
		//
		// PolySwarm returns one status for a per-minute throttle and for an
		// exhausted subscription quota, with no Retry-After and no rate-limit
		// headers to tell them apart; their own SDK cannot either. The only
		// thing that differs is free-text prose in the body, which is
		// undocumented and would break silently the day they reword it.
		//
		// So both are ErrRateLimited rather than ErrQuotaExceeded. The cost of
		// guessing this way round is a retry that fails and a debug line; the
		// cost of the other way round is refusing lookups until 00:00 UTC
		// because of a throttle that cleared in a minute. Do not "fix" this
		// by matching on the message.
		return unavailable(fmt.Errorf("polyswarm: 429: %w", ErrRateLimited))

	case http.StatusUnauthorized, http.StatusForbidden:
		// The key is not in this string. It is stored write-only and this
		// error reaches the server log.
		return unavailable(fmt.Errorf("polyswarm: rejected the API key (status %d)", status))

	default:
		return unavailable(fmt.Errorf("polyswarm: unexpected status %d", status))
	}
}

// psVerdict reads a 200. A body we cannot decode is not an answer — it is
// Unavailable, never Clean.
func psVerdict(body []byte) (Reputation, error) {
	var s psSearch
	if err := json.Unmarshal(body, &s); err != nil {
		return unavailable(fmt.Errorf("polyswarm: decoding response: %w", err))
	}
	if len(s.Result) == 0 {
		// An empty result array is the 200 form of "we have never seen this
		// hash", and means exactly what the 204 does.
		return Reputation{State: Unseen}, nil
	}
	inst := s.Result[0]

	rep := Reputation{AnalysedAt: psAnalysedAt(inst.LastScanned)}

	// Computed before the switch because the state depends on it: KNOWN_GOOD
	// with nothing to attribute it to is not Known. See the two arms below.
	feeds := psFeeds(inst)

	switch {
	// KNOWN_GOOD is read FIRST, before anything looks at whether there are
	// assertions or detections, and the order is load-bearing.
	//
	// A file answered out of a catalogue is never scanned or sandboxed — the
	// response comes straight from the hash match — so it arrives with an
	// empty assertions array and a null detections, byte for byte what an
	// instance nobody has looked at arrives with. Check for emptiness first
	// and the strongest positive signal this system can produce is reported as
	// "nobody looked".
	case inst.State == psStateKnownGood && len(feeds) > 0:
		rep.State = Known
		rep.KnownFeeds = feeds

	// KNOWN_GOOD with nothing to attribute it to is NOT Known.
	//
	// The reassurance Known carries is licensed by the feed and cannot outlive
	// it: the state says only that somebody has the hash on file, and the feed
	// name is what says who and therefore what the claim is worth. With no
	// name — every known_good entry carrying an empty tool, or no entries at
	// all — it is a claim from nowhere, which is the shape this feature
	// refuses everywhere else.
	//
	// It matters more here than elsewhere because Known is FINAL: exempt from
	// expiry and from the manual re-check, so once stored there is no path
	// back and nobody can ask again.
	//
	// Unscanned is what actually happened: PolySwarm holds the file and gave
	// no attributable verdict for it. Not Unseen — they have it. And the
	// tempting simplification later is to set the state once, before the feeds
	// are computed, and trust it; that is the bug this arm exists for.
	case inst.State == psStateKnownGood:
		rep.State = Unscanned

	// Nil-checked, because the field is null rather than zeroed on an
	// instance that has not settled. Unscanned rather than Unavailable:
	// PolySwarm holds the file and has no verdict for it, which is what
	// Unscanned means — and unlike Unavailable it is cacheable, so a file
	// nobody has bountied does not cost a lookup on every single render.
	case inst.Detections == nil || inst.Detections.Total <= 0:
		rep.State = Unscanned

	case inst.Detections.Malicious > 0:
		rep.State = Detected
		rep.Detected = inst.Detections.Malicious
		rep.Total = inst.Detections.Total
		rep.ThreatName = psThreatName(inst)

	default:
		rep.State = Clean
		rep.Detected = inst.Detections.Malicious
		rep.Total = inst.Detections.Total
	}
	return rep, nil
}

// psFeeds is the sorted, de-duplicated list of feeds carrying the file, which
// is how PolySwarm's own SDK derives its known_good_sources.
//
// Sorted because the order is theirs and we render it; de-duplicated because
// two catalogue entries can match the same hash. Entries with no tool name are
// dropped: an unnamed feed is not an attribution, and the whole point of
// carrying these is that the claim has a source and a weight.
func psFeeds(inst psInstance) []string {
	seen := make(map[string]bool, len(inst.KnownGood))
	feeds := make([]string, 0, len(inst.KnownGood))
	for _, e := range inst.KnownGood {
		if e.Tool == "" || seen[e.Tool] {
			continue
		}
		seen[e.Tool] = true
		feeds = append(feeds, e.Tool)
	}
	sort.Strings(feeds)
	if len(feeds) == 0 {
		return nil
	}
	return feeds
}

// psThreatName is polyunite's consensus label, found by its tool key.
//
// By the key and NEVER by position. The metadata array carries exiftool, hash,
// pefile and the sandboxes in an order nobody documents, and several of those
// entries have a malware_family of their own that disagrees with the rest —
// one real file has six engines calling it six different things. Picking by
// index, or "the first entry with a family", is the bug this codebase already
// fixed once in MetaDefender's scan_details.
//
// No polyunite entry means no name. An empty label on a detection is a
// complete answer; a label taken from whichever sandbox happened to be in the
// array is a wrong one.
func psThreatName(inst psInstance) string {
	for _, m := range inst.Metadata {
		if m.Tool == psToolPolyunite {
			return m.ToolMetadata.MalwareFamily
		}
	}
	return ""
}

// psAnalysedAt parses last_scanned, or returns nil.
//
// Two layouts because PolySwarm's timestamps are recorded both with and
// without a zone; the zoneless form is read as UTC, which is what their API
// serves. Anything else is nil rather than a guess — AnalysedAt is what staff
// judge whether a verdict predates a sample's appearance in the wild, and a
// wrong date there is worse than no date.
func psAnalysedAt(s string) *time.Time {
	if s == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05.999999"} {
		if t, err := time.Parse(layout, s); err == nil {
			utc := t.UTC()
			return &utc
		}
	}
	return nil
}
