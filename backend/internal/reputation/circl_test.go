package reputation_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
)

// ---------------------------------------------------------------------------
// CIRCL hashlookup, the fourth provider and the first that needs no key.
//
// It is a catalogue and not a scanner: it can say "this hash is on record" and
// "this hash is not on record", and nothing else. clean, detected and
// unscanned are states it must never produce, because it has no engines and no
// opinion — every record is somebody else's catalogue entry re-published.
//
// The seam is the same functional option the other three use; serveCanned,
// serveSlow and eicarSHA all come from reputation_test.go.
// ---------------------------------------------------------------------------

func newCIRCL(t *testing.T, baseURL string) reputation.Provider {
	t.Helper()
	return reputation.NewCIRCL(reputation.WithBaseURL(baseURL))
}

// ---------------------------------------------------------------------------
// Fixtures, shaped like the live service rather than like a schema.
//
// EVERY scalar is a string — FileSize is "97163", insert-timestamp is
// "1751766732.64" — and ProductCode and OpSystemCode are either a string or an
// object depending on which catalogue the record came from. A plain struct
// with an int or a nested struct fails on half the corpus.
// ---------------------------------------------------------------------------

// clKnownBody is a record carrying the object form of ProductCode.
const clKnownBody = `{
  "CRC32":"9BA5AF41",
  "FileName":"jquery-1.12.4.min.js",
  "FileSize":"97163",
  "MD5":"e0827ba63dd2e19d9d9bb7ec74e63e7f",
  "OpSystemCode":{"OpSystemCode":"362","OpSystemName":"Windows","OpSystemVersion":"none"},
  "ProductCode":{"ApplicationType":"Vacation","Language":"English","MfgCode":"12345",
    "OpSystemCode":"868","ProductCode":"190157",
    "ProductName":"Global Discovery Vacations","ProductVersion":"2016"},
  "SHA-1":"b8b2b0f9b1f0f7b1e0f0f7b1e0f0f7b1e0f0f7b1",
  "SHA-256":"275a021bbfb6489e54d471899f7db9d1663fc695ec2fe2a2c4538aabf651fd0f",
  "SpecialCode":"",
  "db":"nsrl_modern_rds",
  "hashlookup:trust":100,
  "insert-timestamp":"1751766732.64",
  "source":"NSRL"
}`

// clStringCodesBody is the OTHER wire shape of the same fields: ProductCode
// and OpSystemCode as bare strings. Both forms come out of the same endpoint.
const clStringCodesBody = `{
  "FileName":"spacer.gif",
  "FileSize":"43",
  "OpSystemCode":"362",
  "ProductCode":"190157",
  "SHA-256":"275a021bbfb6489e54d471899f7db9d1663fc695ec2fe2a2c4538aabf651fd0f",
  "db":"hashlookup",
  "hashlookup:trust":100,
  "insert-timestamp":"1751766732.64",
  "source":"hashlookup"
}`

// clKnownMaliciousBody is the trap, and it is measured rather than invented: a
// 1x1 spacer GIF, jquery, fontawesome and the two bytes "1\n" all come back
// from the live service carrying KnownMalicious "malshare.com" and a trust of
// 100.
const clKnownMaliciousBody = `{
  "FileName":"jquery-1.12.4.min.js",
  "FileSize":"97163",
  "KnownMalicious":"malshare.com",
  "SHA-256":"275a021bbfb6489e54d471899f7db9d1663fc695ec2fe2a2c4538aabf651fd0f",
  "db":"nsrl_modern_rds",
  "hashlookup:trust":100,
  "source":"NSRL"
}`

// clNotFound is the 404 body. Distinct code AND distinct body from the 400
// below, which is what makes "not on record" expressible at all.
const clNotFound = `{"message":"Non existing SHA-256","query":"` + eicarSHA + `"}`

// clBadHash is the 400 for a hash the service itself rejects.
const clBadHash = `{"message":"SHA-256 value incorrect, expecting a SHA-256 value in hex format"}`

// ---------------------------------------------------------------------------
// The mapping.
// ---------------------------------------------------------------------------

// A 200 is "known": a named catalogue has this exact hash on file.
//
// No counts, ever. CIRCL runs no engines, so "0 of 0" there would be a
// fabricated analysis attached to the one verdict staff are entitled to find
// reassuring — and the feeds are what says how much that reassurance is worth.
func TestCIRCL_ARecordIsKnown(t *testing.T) {
	srv := serveCanned(t, http.StatusOK, clKnownBody)

	got, err := newCIRCL(t, srv.URL).Lookup(context.Background(), eicarSHA)
	require.NoError(t, err)

	require.Equal(t, reputation.Known, got.State,
		"a catalogue hit is known; it is not clean, because nothing was scanned")
	require.Contains(t, got.KnownFeeds, "nsrl_modern_rds", "db is the trustworthy field")
	require.Contains(t, got.KnownFeeds, "NSRL", "source is the other one")
	require.Zero(t, got.Detected, "no engine ran")
	require.Zero(t, got.Total)
	require.Empty(t, got.ThreatName, "a catalogue has no name for a threat it never found")
	require.Nil(t, got.AnalysedAt,
		"nothing was analysed, so an analysis date would be a fact we invented")
}

// A 404 is "unseen", and nothing else is.
func TestCIRCL_NotOnRecordIsUnseen(t *testing.T) {
	srv := serveCanned(t, http.StatusNotFound, clNotFound)

	got, err := newCIRCL(t, srv.URL).Lookup(context.Background(), eicarSHA)
	require.NoError(t, err)
	require.Equal(t, reputation.Unseen, got.State)
	require.Empty(t, got.KnownFeeds)
}

// Three states CIRCL can never produce, whatever it answers.
//
// It has no engines: there is no configuration of its responses that licenses
// "engines ran and found nothing", "an engine flagged it", or "the provider
// holds the file and has no verdict". An implementation that reaches for one
// of them is describing a scan that did not happen.
func TestCIRCL_NeverClaimsAScanHappened(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"a record", http.StatusOK, clKnownBody},
		{"a record with string codes", http.StatusOK, clStringCodesBody},
		{"a record MalShare has seen", http.StatusOK, clKnownMaliciousBody},
		{"not on record", http.StatusNotFound, clNotFound},
		{"a hash they rejected", http.StatusBadRequest, clBadHash},
		{"their server broke", http.StatusInternalServerError, `{"message":"error"}`},
		{"a bad gateway with an HTML body", http.StatusBadGateway, `<html>502</html>`},
		{"a 200 that is not JSON", http.StatusOK, `<html>maintenance</html>`},
		{"a 200 that is truncated JSON", http.StatusOK, `{"db":"nsrl`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := serveCanned(t, tc.status, tc.body)
			got, _ := newCIRCL(t, srv.URL).Lookup(context.Background(), eicarSHA)

			require.NotEqual(t, reputation.Clean, got.State, "CIRCL runs no engines")
			require.NotEqual(t, reputation.Detected, got.State)
			require.NotEqual(t, reputation.Unscanned, got.State,
				"unscanned means the provider holds the file and has no verdict; CIRCL holds no files")
			require.True(t, got.State == reputation.Known ||
				got.State == reputation.Unseen ||
				got.State == reputation.Unavailable,
				"state %q is outside the three CIRCL can express", got.State)
		})
	}
}

// Everything that is not a 200 or a 404 is unavailable, with a reason.
func TestCIRCL_FailuresAreUnavailable(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"internal error", http.StatusInternalServerError, `{"message":"error"}`},
		{"bad gateway", http.StatusBadGateway, `<html>502</html>`},
		{"service unavailable", http.StatusServiceUnavailable, ``},
		{"they rejected the hash", http.StatusBadRequest, clBadHash},
		{"a 200 we cannot parse", http.StatusOK, `<html>maintenance</html>`},
		{"a 200 with nothing to attribute", http.StatusOK, `{"FileName":"x","hashlookup:trust":100}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := serveCanned(t, tc.status, tc.body)
			got, err := newCIRCL(t, srv.URL).Lookup(context.Background(), eicarSHA)

			require.Error(t, err, "the operator needs the reason")
			require.Equal(t, reputation.Unavailable, got.State)
		})
	}
}

// A hung CIRCL degrades to unavailable quickly, without a deadline of the
// caller's.
//
// Their instance answers from a development server with no CDN and no cache,
// and observed latency is under half a second. The other three providers wait
// fifteen seconds; a help desk page must not.
func TestCIRCL_AHungServiceGivesUpQuickly(t *testing.T) {
	start := time.Now()
	got, err := newCIRCL(t, serveSlow(t)).Lookup(context.Background(), eicarSHA)
	elapsed := time.Since(start)

	require.Error(t, err)
	require.Equal(t, reputation.Unavailable, got.State)
	require.Less(t, elapsed, 5*time.Second,
		"a CIRCL outage held the caller for %v; the timeout is meant to be seconds", elapsed)
}

// ---------------------------------------------------------------------------
// The trap.
// ---------------------------------------------------------------------------

// KnownMalicious is not a detection, and must never be wired to one.
//
// The field exists, it says "malshare.com", and the live service attaches it
// to jquery-1.12.4.min.js, fontawesome-webfont.woff2, a 1x1 spacer GIF and the
// hash of the two bytes "1\n". MalShare's corpus is everything ever submitted
// to it, including benign assets carved out of malware samples, so the field
// means "these bytes have appeared in MalShare" and nothing more. Wiring it to
// detected reports jQuery to staff as malware.
func TestCIRCL_KnownMaliciousIsNotADetection(t *testing.T) {
	srv := serveCanned(t, http.StatusOK, clKnownMaliciousBody)

	got, err := newCIRCL(t, srv.URL).Lookup(context.Background(), eicarSHA)
	require.NoError(t, err)

	require.Equal(t, reputation.Known, got.State,
		"KnownMalicious was read as a verdict; it is a MalShare membership flag on a corpus that contains jQuery")
	require.Zero(t, got.Detected, "no engine flagged anything, because no engine ran")
	require.Zero(t, got.Total)
	require.Empty(t, got.ThreatName,
		`"malshare.com" is a corpus name, not what somebody called a threat`)
}

// hashlookup:trust is not a verdict either.
//
// It starts at 50, adds 5 per parent archive and subtracts 20 for
// KnownMalicious, capped at 100 — a breadth counter, not an opinion. EICAR
// scores 100. Nothing in this package may branch on it, in either direction.
func TestCIRCL_TrustScoreDoesNotChangeTheVerdict(t *testing.T) {
	for _, trust := range []string{"0", "20", "50", "100"} {
		t.Run("trust "+trust, func(t *testing.T) {
			body := strings.Replace(clKnownBody, `"hashlookup:trust":100`,
				`"hashlookup:trust":`+trust, 1)
			srv := serveCanned(t, http.StatusOK, body)

			got, err := newCIRCL(t, srv.URL).Lookup(context.Background(), eicarSHA)
			require.NoError(t, err)
			require.Equal(t, reputation.Known, got.State,
				"the trust score decided the verdict; it counts archives, it does not judge files")
			require.Zero(t, got.Detected)
		})
	}
}

// ---------------------------------------------------------------------------
// The feeds.
// ---------------------------------------------------------------------------

// The feeds are db, source and ProductCode.ProductName, de-duplicated and with
// the empties dropped.
//
// ProductName is one arbitrary record rather than provenance — the live
// service labels a jQuery file "Global Discovery Vacations" — so it is carried
// after the two fields that are trustworthy, never instead of them.
func TestCIRCL_Feeds(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{
			"db, source and the product name",
			clKnownBody,
			[]string{"nsrl_modern_rds", "NSRL", "Global Discovery Vacations"},
		},
		{
			"a repeated name is carried once",
			`{"db":"hashlookup","source":"hashlookup","SHA-256":"x"}`,
			[]string{"hashlookup"},
		},
		{
			"a string ProductCode has no product name to read",
			clStringCodesBody,
			[]string{"hashlookup"},
		},
		{
			"empty fields are not feeds",
			`{"db":"nsrl","source":"","ProductCode":{"ProductName":""}}`,
			[]string{"nsrl"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := serveCanned(t, http.StatusOK, tc.body)
			got, err := newCIRCL(t, srv.URL).Lookup(context.Background(), eicarSHA)
			require.NoError(t, err)
			require.Equal(t, reputation.Known, got.State)
			require.Equal(t, tc.want, got.KnownFeeds,
				"the feed names are the evidence; order is db, source, then the arbitrary one")
		})
	}
}

// A record nobody can be named for is not reassurance.
//
// known is the one state here that renders as reassurance, and it earns that
// only because a named catalogue made a positive claim. A 200 carrying neither
// db nor source nor a product name has no name behind it, so it is unavailable
// rather than a claim from nowhere — and it is unreachable against the live
// service, where every record carries both.
func TestCIRCL_ARecordWithNoNameBehindItIsNotKnown(t *testing.T) {
	srv := serveCanned(t, http.StatusOK, `{"FileName":"x.dll","FileSize":"68","hashlookup:trust":100}`)

	got, err := newCIRCL(t, srv.URL).Lookup(context.Background(), eicarSHA)
	require.Error(t, err)
	require.Equal(t, reputation.Unavailable, got.State,
		"known with no feed named is a claim from nowhere, and known never expires")
}

// ---------------------------------------------------------------------------
// The hash we send.
// ---------------------------------------------------------------------------

// A hash that is not 64 hex characters is our bug, and is refused before the
// request.
//
// Their length check passes a 64-character string containing "_" or "0x" and
// then answers 404 — so sending one unchecked would read as "CIRCL has no
// record of this file" when the truth is that we asked a malformed question.
func TestCIRCL_RefusesAMalformedHashWithoutAsking(t *testing.T) {
	bad := []string{
		"",
		"notahash",
		strings.Repeat("0", 63),
		strings.Repeat("0", 65),
		strings.Repeat("0", 63) + "_",
		"0x" + strings.Repeat("a", 62),
		strings.Repeat("g", 64),
		eicarSHA + "\n",
		"../../etc/passwd",
	}
	for _, h := range bad {
		t.Run(h, func(t *testing.T) {
			srv := serveCanned(t, http.StatusOK, clKnownBody)
			got, err := newCIRCL(t, srv.URL).Lookup(context.Background(), h)

			require.Error(t, err)
			require.Equal(t, reputation.Unavailable, got.State,
				"a malformed hash must not read as a verdict of any kind")
			require.Zero(t, srv.count(), "a hash we know is wrong is not worth asking about")
		})
	}
}

// Hex is hex in either case, and both halves of the request are what they
// should be: the hash in the path, and no credential anywhere.
func TestCIRCL_AsksWithNoCredential(t *testing.T) {
	srv := serveCanned(t, http.StatusOK, clKnownBody)
	upper := strings.ToUpper(eicarSHA)

	got, err := newCIRCL(t, srv.URL).Lookup(context.Background(), upper)
	require.NoError(t, err)
	require.Equal(t, reputation.Known, got.State, "uppercase hex is a valid SHA-256")

	req := srv.lastRequest()
	require.NotNil(t, req)
	require.Equal(t, "/lookup/sha256/"+upper, req.URL.Path)
	require.Empty(t, req.Header.Get("Authorization"),
		"CIRCL needs no key, so we must not invent a header to send one in")
	require.Empty(t, req.Header.Get("x-apikey"))
	require.Empty(t, req.URL.RawQuery, "nothing of ours belongs in their query string")
}

// ---------------------------------------------------------------------------
// The name a person reads, the value the setting takes, and the link.
// ---------------------------------------------------------------------------

func TestCIRCL_NameAndDisplayName(t *testing.T) {
	require.Equal(t, reputation.ProviderCIRCL, reputation.NewCIRCL().Name())
	require.Equal(t, "circl", reputation.ProviderCIRCL,
		"the column, the setting and the constant have to agree on a spelling")
	require.NotEmpty(t, reputation.DisplayName(reputation.ProviderCIRCL),
		"a verdict with no attribution is a claim from nowhere")
	require.True(t, reputation.ValidProvider(reputation.ProviderCIRCL))
	require.False(t, reputation.ValidProvider("CIRCL"))
	require.False(t, reputation.ValidProvider("hashlookup"))
}

// There is no per-hash web UI to link to — the root serves a Swagger page —
// so the link is empty and the caller omits it rather than sending staff
// somewhere that cannot answer their question.
func TestCIRCL_HasNoPerHashLink(t *testing.T) {
	require.Empty(t, reputation.NewCIRCL().LinkURL(eicarSHA))
}
