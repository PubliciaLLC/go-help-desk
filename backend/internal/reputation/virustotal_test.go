package reputation_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
)

// ---------------------------------------------------------------------------
// Canned VirusTotal v3 bodies.
//
// Shaped like the real thing rather than trimmed to what a parser needs: the
// fields a mis-written parser trips over are the ones a minimal fixture leaves
// out. In particular last_analysis_stats carries all EIGHT keys — a seven-key
// fixture passes against an implementation that forgets confirmed-timeout and
// then reports 62/80 for a file the report page calls 62/81.
// ---------------------------------------------------------------------------

// vtAnalysedAt is last_analysis_date as VirusTotal sends it: UNIX SECONDS, not
// RFC3339 and not milliseconds. 2025-09-22T18:20:00Z.
const vtAnalysedAt = 1758560400

// A hit. 62 malicious + 0 suspicious of 81 engines.
//
// Every malicious engine reports the same string, and
// popular_threat_classification agrees with them, so this fixture does not
// silently decide which field ThreatName comes from — see the open question in
// the report. It is deterministic either way, which a map with differing
// values would not be: Go randomises map iteration order.
const vtDetectedBody = `{
  "data": {
    "id": "275a021bbfb6489e54d471899f7db9d1663fc695ec2fe2a2c4538aabf651fd0f",
    "type": "file",
    "links": {"self": "https://www.virustotal.com/api/v3/files/275a021b"},
    "attributes": {
      "md5": "44d88612fea8a8f36de82e1278abb02f",
      "sha1": "3395856ce81f2b7382dee72602f798b642f14140",
      "sha256": "275a021bbfb6489e54d471899f7db9d1663fc695ec2fe2a2c4538aabf651fd0f",
      "meaningful_name": "invoice.exe",
      "size": 68,
      "type_description": "Win32 EXE",
      "last_analysis_date": 1758560400,
      "first_submission_date": 1580000000,
      "reputation": -412,
      "last_analysis_stats": {
        "malicious": 62,
        "suspicious": 0,
        "undetected": 10,
        "harmless": 0,
        "timeout": 1,
        "confirmed-timeout": 1,
        "failure": 2,
        "type-unsupported": 5
      },
      "popular_threat_classification": {
        "suggested_threat_label": "Trojan.GenericKD.12345",
        "popular_threat_category": [{"count": 40, "value": "trojan"}],
        "popular_threat_name": [{"count": 22, "value": "generickd"}]
      },
      "last_analysis_results": {
        "Bitdefender": {"category": "malicious", "engine_name": "Bitdefender", "engine_version": "7.2", "result": "Trojan.GenericKD.12345", "method": "blacklist"},
        "Kaspersky": {"category": "malicious", "engine_name": "Kaspersky", "engine_version": "22.0", "result": "Trojan.GenericKD.12345", "method": "blacklist"},
        "Microsoft": {"category": "malicious", "engine_name": "Microsoft", "engine_version": "1.1", "result": "Trojan.GenericKD.12345", "method": "blacklist"},
        "Zoner": {"category": "undetected", "engine_name": "Zoner", "engine_version": "2.2", "result": null, "method": "blacklist"},
        "Avast-Mobile": {"category": "type-unsupported", "engine_name": "Avast-Mobile", "engine_version": "250922", "result": null, "method": "blacklist"},
        "Trustlook": {"category": "confirmed-timeout", "engine_name": "Trustlook", "engine_version": "1.0", "result": null, "method": "blacklist"}
      }
    }
  }
}`

// A miss: the analysis completed and nothing flagged it. 0 of 78.
const vtCleanBody = `{
  "data": {
    "id": "275a021bbfb6489e54d471899f7db9d1663fc695ec2fe2a2c4538aabf651fd0f",
    "type": "file",
    "attributes": {
      "meaningful_name": "quarterly-report.pdf",
      "size": 184320,
      "last_analysis_date": 1758560400,
      "last_analysis_stats": {
        "malicious": 0,
        "suspicious": 0,
        "undetected": 70,
        "harmless": 2,
        "timeout": 1,
        "confirmed-timeout": 1,
        "failure": 1,
        "type-unsupported": 3
      },
      "last_analysis_results": {
        "Bitdefender": {"category": "undetected", "engine_name": "Bitdefender", "result": null, "method": "blacklist"}
      }
    }
  }
}`

// vtError builds VirusTotal's error envelope. The code is a STRING here; the
// spec is explicit that the branch is on the code and never on the message,
// because messages are free text and have changed.
func vtError(code, message string) string {
	return fmt.Sprintf(`{"error": {"code": %q, "message": %q}}`, code, message)
}

// ---------------------------------------------------------------------------

// Each of the five states, from a body VirusTotal actually sends.
func TestVirusTotal_Lookup_States(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   reputation.State
		why    string
	}{
		{
			name: "engines flagged it", status: http.StatusOK, body: vtDetectedBody,
			want: reputation.Detected,
		},
		{
			name: "analysis completed, nothing flagged it", status: http.StatusOK, body: vtCleanBody,
			want: reputation.Clean,
			why:  "Clean is reachable only from a completed lookup",
		},
		{
			name:   "never seen this hash",
			status: http.StatusNotFound,
			body:   vtError("NotFoundError", `File "275a021b" not found`),
			want:   reputation.Unseen,
			why:    "no record is not a verdict, and must never render as clean",
		},
		{
			name:   "bad api key",
			status: http.StatusUnauthorized,
			body:   vtError("WrongCredentialsError", "Wrong API key"),
			want:   reputation.Unavailable,
		},
		{
			name:   "rate limited, wait a minute",
			status: http.StatusTooManyRequests,
			body:   vtError("TooManyRequestsError", "Too many requests"),
			want:   reputation.Unavailable,
		},
		{
			name:   "daily quota gone until 00:00 UTC",
			status: http.StatusTooManyRequests,
			body:   vtError("QuotaExceededError", "Quota exceeded"),
			want:   reputation.Unavailable,
		},
		{
			name: "server error", status: http.StatusInternalServerError, body: `{"error":{"code":"TransientError","message":"Transient"}}`,
			want: reputation.Unavailable,
		},
		{
			// VirusTotal has no Unscanned equivalent, so the only 404 code it
			// sends that we understand is NotFoundError. Any other 404 is a
			// response we do not understand, and a response we do not
			// understand is not "this file has never been seen".
			name:   "404 with a code we do not recognise",
			status: http.StatusNotFound,
			body:   vtError("SomethingElseError", "Nope"),
			want:   reputation.Unavailable,
			why:    "treating an unknown 404 as Unseen invents a fact from a parse failure",
		},
		{
			name: "404 with no parseable body at all", status: http.StatusNotFound, body: `not json`,
			want: reputation.Unavailable,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := serveCanned(t, tc.status, tc.body)
			got, _ := newVirusTotal(t, srv.URL, "test-key").Lookup(context.Background(), eicarSHA)
			require.Equal(t, tc.want, got.State, tc.why)
		})
	}
}

// The counts are what an analyst reads before deciding whether to open the
// file, so getting the denominator wrong is not cosmetic.
//
// last_analysis_stats has eight keys. Total is every engine that ran, which
// includes the three a minimal fixture omits: timeout, confirmed-timeout and
// failure. 62 + 0 + 10 + 0 + 1 + 1 + 2 + 5 = 81. An implementation that sums
// only malicious/suspicious/undetected/harmless reports 72; one that forgets
// confirmed-timeout alone reports 80. Both are wrong and both pass a fixture
// that leaves those keys out.
func TestVirusTotal_Lookup_CountsEveryEngineThatRan(t *testing.T) {
	srv := serveCanned(t, http.StatusOK, vtDetectedBody)
	got, err := newVirusTotal(t, srv.URL, "test-key").Lookup(context.Background(), eicarSHA)
	require.NoError(t, err)

	require.Equal(t, reputation.Detected, got.State)
	require.Equal(t, 62, got.Detected)
	require.Equal(t, 81, got.Total, "all eight stats keys, confirmed-timeout included")
	require.Equal(t, "Trojan.GenericKD.12345", got.ThreatName,
		"an analyst acts on the family name; 'infected' tells them nothing")
}

// A clean verdict still carries its denominator: "0 of 78" is a statement, "0
// of 0" is a missing lookup wearing a clean verdict's clothes.
func TestVirusTotal_Lookup_CleanKeepsItsDenominator(t *testing.T) {
	srv := serveCanned(t, http.StatusOK, vtCleanBody)
	got, err := newVirusTotal(t, srv.URL, "test-key").Lookup(context.Background(), eicarSHA)
	require.NoError(t, err)

	require.Equal(t, reputation.Clean, got.State)
	require.Equal(t, 0, got.Detected)
	require.Equal(t, 78, got.Total, "0 of 0 is not a clean verdict, it is no verdict")
	require.Empty(t, got.ThreatName)
}

// last_analysis_date is UNIX SECONDS. Not RFC3339, which is what every other
// timestamp crossing this codebase looks like, and not milliseconds.
//
// A time.Time zero value, a 1970 date from a milliseconds misreading, or a
// nil from a failed RFC3339 parse all fail here. Staff read this field to
// judge whether a clean verdict predates the sample's first appearance in the
// wild, so a wrong date is worse than no date.
func TestVirusTotal_Lookup_AnalysisDateIsUnixSeconds(t *testing.T) {
	srv := serveCanned(t, http.StatusOK, vtDetectedBody)
	got, err := newVirusTotal(t, srv.URL, "test-key").Lookup(context.Background(), eicarSHA)
	require.NoError(t, err)

	require.NotNil(t, got.AnalysedAt, "the body carried last_analysis_date")
	want := time.Unix(vtAnalysedAt, 0).UTC()
	require.True(t, got.AnalysedAt.UTC().Equal(want),
		"want %s, got %s", want, got.AnalysedAt.UTC())
	// Belt and braces against a milliseconds reading, which lands in 1970 and
	// would otherwise only be caught by the exact comparison above.
	require.Equal(t, 2025, got.AnalysedAt.UTC().Year())
}

// The request itself: wrong path or wrong header name and every lookup is a
// 401 or a 404 that the code above would dutifully classify.
//
// Also pins that the key travels in a header and never in the URL — a key in a
// query string lands in access logs, which defeats storing it write-only.
func TestVirusTotal_Lookup_SendsTheDocumentedRequest(t *testing.T) {
	srv := serveCanned(t, http.StatusOK, vtCleanBody)
	_, err := newVirusTotal(t, srv.URL, "s3cret-key").Lookup(context.Background(), eicarSHA)
	require.NoError(t, err)

	req := srv.lastRequest()
	require.NotNil(t, req, "no request reached the server")
	require.Equal(t, http.MethodGet, req.Method)
	require.Equal(t, "/api/v3/files/"+eicarSHA, req.URL.Path)
	require.Equal(t, "s3cret-key", req.Header.Get("x-apikey"),
		"VirusTotal reads the key from x-apikey")
	require.Empty(t, req.URL.RawQuery, "the key must not travel in the URL")
}

// Both meanings of 429 are Unavailable to the caller, but they are not the
// same event: TooManyRequestsError clears in a minute, QuotaExceededError not
// until 00:00 UTC. An implementation that cannot tell them apart cannot back
// off correctly, and the status code alone does not tell it.
//
// OPEN QUESTION (see report): the spec does not say how the implementation
// should expose the difference. Sentinel errors checked with errors.Is would
// match antivirus.ErrNotConfigured, which is this codebase's precedent, but
// naming them here would be inventing an API — and a test referencing symbols
// that do not exist fails to build instead of failing an assertion. So this
// pins the weaker, decidable thing: the two must not collapse into one
// indistinguishable error, and the operator-facing reason must name the
// provider's own code, which is the thing the spec says to branch on.
func TestVirusTotal_Lookup_RateLimitAndQuotaAreDistinguishable(t *testing.T) {
	lookup := func(body string) error {
		srv := serveCanned(t, http.StatusTooManyRequests, body)
		_, err := newVirusTotal(t, srv.URL, "test-key").Lookup(context.Background(), eicarSHA)
		return err
	}

	rateLimited := lookup(vtError("TooManyRequestsError", "Too many requests"))
	quotaGone := lookup(vtError("QuotaExceededError", "Quota exceeded"))

	require.Error(t, rateLimited)
	require.Error(t, quotaGone)
	require.NotEqual(t, rateLimited.Error(), quotaGone.Error(),
		"a minute-long throttle and a day-long quota exhaustion are different events")
	require.Contains(t, rateLimited.Error(), "TooManyRequestsError")
	require.Contains(t, quotaGone.Error(), "QuotaExceededError")
}

// Never log the key. The error is written to the server log at the boundary,
// so a reason that embeds the request — or the key — undoes storing it
// write-only.
func TestVirusTotal_Lookup_ErrorNeverCarriesTheKey(t *testing.T) {
	const key = "vt-live-key-do-not-log"
	srv := serveCanned(t, http.StatusUnauthorized, vtError("WrongCredentialsError", "Wrong API key"))
	_, err := newVirusTotal(t, srv.URL, key).Lookup(context.Background(), eicarSHA)
	require.Error(t, err)
	require.NotContains(t, err.Error(), key, "the API key is write-only; it must not reach a log")
}
