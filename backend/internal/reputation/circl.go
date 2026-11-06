package reputation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// clBaseURL is the real service. WithBaseURL replaces it in tests.
const clBaseURL = "https://hashlookup.circl.lu"

// clTimeout is deliberately much shorter than the fifteen seconds the other
// three get.
//
// Observed latency is around 0.4s, and the service answers from a Python
// development server with no CDN, no compression and no cache headers. Three
// seconds is generous against that, and the property that matters is the other
// end of it: a CIRCL outage must degrade to Unavailable rather than hold a
// ticket page open waiting for a free, best-effort service to answer.
const clTimeout = 3 * time.Second

// CIRCL is hashlookup, run by the Computer Incident Response Center
// Luxembourg, and it is a different kind of thing from the other three.
//
// It is a CATALOGUE, not a scanner. It re-publishes hash sets — NSRL, vendor
// databases, its own collections — and answers one question: is this hash in
// one of them. So it produces exactly two verdicts, Known and Unseen, and can
// never say Clean, Detected or Unscanned. No engine runs here; there is
// nothing for an engine to have found and nothing for it to have missed.
//
// It needs NO API KEY, which is why "is the key empty" stopped being the right
// question to ask before a lookup: it is the right answer for the three
// commercial services and permanently disables this one. See CanLookup.
//
// Two properties of the service that a reader should know before changing
// anything here:
//
//   - Its Unseen is WEAKER than the other three providers'. NSRL's legacy sets
//     are SHA-1 indexed and the SHA-256 mapping was added afterwards, so a
//     file can be in NSRL and still answer 404 on a SHA-256 query. "CIRCL has
//     no record of this SHA-256" is the honest rendering; "this file is
//     unknown" is not.
//   - Asking it is a DISCLOSURE. It records the caller's IP and User-Agent,
//     and its public instance serves a /stats/top leaderboard of the most
//     queried hashes, with filenames. VirusTotal and OPSWAT log; this one
//     publishes. That belongs in the operator's decision, not in this file,
//     but it is why the admin UI says something different for this provider.
//
// Licensing: the OpenAPI document declares CC-BY and the dataset is CC-BY-4.0
// on Luxembourg's open data portal, which permits commercial use. CIRCL
// nowhere say so in their own words and publish no terms of service for
// hashlookup, so that is recorded as what it is. CC-BY requires attribution:
// "Hash reputation data from CIRCL hashlookup, Computer Incident Response
// Center Luxembourg, CC-BY-4.0."
type CIRCL struct {
	client
}

// NewCIRCL builds the provider. It takes no API key, unlike the other three,
// because there is none to take: a parameter that can only ever be ignored is
// an invitation to configure something that does nothing.
func NewCIRCL(opts ...Option) *CIRCL {
	c := newClient("", clBaseURL, opts...)
	// After newClient, which sets the fifteen seconds every other provider
	// wants. See clTimeout.
	c.http.Timeout = clTimeout
	return &CIRCL{client: c}
}

var _ Provider = (*CIRCL)(nil)

func (c *CIRCL) Name() string { return ProviderCIRCL }

// LinkURL is empty, and that is an answer rather than a gap.
//
// There is no per-hash web UI to send anybody to: the root serves a Swagger
// page and nothing else. The contract already allows an empty link and the
// caller omits the field, which is better than a link that cannot answer the
// question the reader clicked it with.
func (c *CIRCL) LinkURL(sha256 string) string { return "" }

// clRecord is the slice of a hashlookup record we read, and the three fields
// here are the only ones this package has any business believing.
//
// EVERY scalar in a real response is a string — FileSize is "97163",
// insert-timestamp is "1751766732.64" — and ProductCode and OpSystemCode are
// either a string or an object depending on which catalogue the record came
// from, which is why ProductCode is a RawMessage. Decoded as a struct it fails
// on every record that carries the string form, and a decode failure here is
// Unavailable: a whole class of files would become "we could not ask".
//
// TWO FIELDS OF THE REAL RESPONSE ARE DELIBERATELY NOT DECODED, and neither
// omission is an oversight:
//
//   - KnownMalicious. It exists, it says "malshare.com", and it is NOT a
//     detection. Measured against the live service, it is attached to
//     jquery-1.12.4.min.js, fontawesome-webfont.woff2, a 1x1 spacer GIF and
//     the hash of the two bytes "1\n". MalShare's corpus is everything ever
//     submitted to it, including benign assets carved out of malware samples,
//     so the field means "these bytes have appeared in MalShare" and nothing
//     more. Wiring it to Detected reports jQuery to staff as malware. See the
//     recorded architecture decisions in .claude/CLAUDE.md.
//   - hashlookup:trust. Also not a verdict. Read from the server's source it
//     starts at 50, adds 5 for each parent archive the file appears in and
//     subtracts 20 for KnownMalicious, capped at 100 — so any file in 14 or
//     more archives saturates at 100 whether flagged or not, which is why
//     EICAR scores 100. It counts breadth, it does not hold an opinion.
//
// Both are left undecoded rather than decoded and ignored, so that nothing in
// this package can start branching on them by accident.
type clRecord struct {
	// db and source are the catalogue the record came from, and they are the
	// trustworthy names in a hashlookup record.
	DB     string `json:"db"`
	Source string `json:"source"`

	// ProductCode's object form carries a ProductName. It is one arbitrary
	// record rather than provenance — the live service labels a jQuery file
	// "Global Discovery Vacations" — so it is carried last and never instead
	// of the two above.
	ProductCode json.RawMessage `json:"ProductCode"`
}

func (c *CIRCL) Lookup(ctx context.Context, sha256 string) (Reputation, error) {
	// Validated before the request, and the reason is that their validation is
	// looser than ours: a 64-character string containing "_" or "0x" passes
	// their length check and comes back 404. Sent unchecked, our own bad hash
	// would render to staff as "CIRCL has no record of this file" — a fact
	// about the world manufactured out of a bug of ours.
	if !validSHA256(sha256) {
		return unavailable(fmt.Errorf("circl: %q is not a sha-256", sha256))
	}

	// No key, and therefore no authentication header: the empty header name
	// tells the shared client to send none. Inventing one to carry an empty
	// string would be a credential-shaped thing in every access log between
	// here and Luxembourg that authenticates nobody.
	status, body, err := c.get(ctx, "/lookup/sha256/"+sha256, "")
	if err != nil {
		return unavailable(fmt.Errorf("circl lookup: %w", err))
	}

	switch status {
	case http.StatusOK:
		return clVerdict(body)

	case http.StatusNotFound:
		// They answered, and the answer is that this SHA-256 is not in any
		// catalogue they carry. Weaker evidence than a VirusTotal miss — see
		// the type comment — but it is the same state, and how it is worded
		// for a reader is the caller's problem.
		return Reputation{State: Unseen}, nil

	case http.StatusBadRequest:
		// They rejected the hash. We validated it above, so this is a
		// disagreement about what a SHA-256 is rather than an answer about a
		// file, and reading it as "no record" would turn our bug into their
		// finding.
		return unavailable(fmt.Errorf("circl: rejected the hash (status 400)"))

	default:
		return unavailable(fmt.Errorf("circl: unexpected status %d", status))
	}
}

// clVerdict reads a 200. A body we cannot decode is not an answer.
func clVerdict(body []byte) (Reputation, error) {
	var rec clRecord
	if err := json.Unmarshal(body, &rec); err != nil {
		return unavailable(fmt.Errorf("circl: decoding response: %w", err))
	}

	feeds := clFeeds(rec)
	if len(feeds) == 0 {
		// A record with no catalogue name in it cannot be Known.
		//
		// Known is the one state in this feature that renders as reassurance,
		// and it earns that only because a NAMED catalogue made a positive
		// claim; it is also final, exempt from expiry and from the manual
		// re-check, so an unattributed one would sit on a ticket for the life
		// of the instance. Unavailable rather than Unscanned: CIRCL holds no
		// files and runs no engines, so it cannot honestly say "we have it and
		// have no verdict". Unreachable against the live service, where every
		// record carries db and source.
		return unavailable(fmt.Errorf("circl: a record with no catalogue named in it"))
	}

	// No counts, no threat name, and no AnalysedAt.
	//
	// Nothing was analysed: the record is a catalogue entry, not a scan. The
	// response does carry insert-timestamp, and it is deliberately not read —
	// it is when CIRCL imported the record, and rendering it as "analysed on"
	// would put a date on an analysis that never happened.
	return Reputation{State: Known, KnownFeeds: feeds}, nil
}

// clFeeds is the catalogues that carry the file: db, source, and the product
// name if the record has one.
//
// In that order rather than sorted, because the order IS the ranking. db and
// source say which hash set the record came from and are the fields worth
// believing; ProductCode.ProductName is one arbitrary catalogue row, and the
// live service labels a jQuery file "Global Discovery Vacations". A reader
// takes the first name as the answer, so the arbitrary one goes last.
//
// De-duplicated because db and source are frequently the same string, and the
// empties are dropped: an unnamed catalogue is not an attribution, and the
// whole point of carrying these is that the claim has a source.
func clFeeds(rec clRecord) []string {
	seen := make(map[string]bool, 3)
	feeds := make([]string, 0, 3)
	for _, name := range []string{rec.DB, rec.Source, clProductName(rec.ProductCode)} {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		feeds = append(feeds, name)
	}
	if len(feeds) == 0 {
		return nil
	}
	return feeds
}

// clProductName reads ProductCode's object form, or returns "".
//
// The field is a bare string on some records and an object on others, from the
// same endpoint, so this cannot be a typed field and a failure to decode it is
// not a failure of the lookup: the two trustworthy names are elsewhere.
func clProductName(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var product struct {
		ProductName string `json:"ProductName"`
	}
	if err := json.Unmarshal(raw, &product); err != nil {
		return ""
	}
	return product.ProductName
}

// validSHA256 is ^[0-9a-fA-F]{64}$, spelled out rather than compiled, because
// a package-level regexp is global state this package has none of anywhere
// else.
func validSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}
