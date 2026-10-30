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
// Canned MetaDefender v4 bodies.
//
// Deliberately a different shape from VirusTotal's in every respect that
// matters: the counts are two explicit fields rather than a stats map to be
// summed, start_time is an RFC3339 string rather than Unix seconds, and the
// error code is a NUMBER rather than a string. A parser that works for one
// cannot accidentally work for the other.
// ---------------------------------------------------------------------------

// mdAnalysedAt is scan_results.start_time. The same instant as VirusTotal's
// fixture, written the way OPSWAT writes it — RFC3339 with milliseconds and a
// Z — so a test that confuses the two formats fails rather than coincidentally
// agreeing.
const mdAnalysedAt = "2025-09-22T18:20:00.000Z"

// A hit: 21 of 37 engines.
//
// Every engine in scan_details names the same threat, so the fixture does not
// silently decide which entry ThreatName comes from — Go randomises map
// iteration order, and scan_details is a map keyed by engine name.
const mdDetectedBody = `{
  "file_id": "bzIyMDkyMnZOWXFIVHhrTA",
  "data_id": "MjA5MjJ2Tllx",
  "scan_results": {
    "scan_details": {
      "Ahnlab": {"threat_found": "EICAR_Test_File", "scan_result_i": 1, "def_time": "2025-09-22T09:00:00.000Z", "scan_time": 12},
      "Bitdefender": {"threat_found": "EICAR_Test_File", "scan_result_i": 1, "def_time": "2025-09-22T08:00:00.000Z", "scan_time": 40},
      "ClamAV": {"threat_found": "EICAR_Test_File", "scan_result_i": 1, "def_time": "2025-09-22T07:00:00.000Z", "scan_time": 8},
      "Zillya": {"threat_found": "", "scan_result_i": 0, "def_time": "2025-09-21T22:00:00.000Z", "scan_time": 3}
    },
    "rescan_available": true,
    "scan_all_result_a": "Infected",
    "scan_all_result_i": 1,
    "start_time": "2025-09-22T18:20:00.000Z",
    "total_avs": 37,
    "total_detected_avs": 21,
    "total_time": 2210,
    "progress_percentage": 100
  },
  "file_info": {
    "display_name": "invoice.exe",
    "file_size": 68,
    "file_type_category": "E",
    "md5": "44d88612fea8a8f36de82e1278abb02f",
    "sha1": "3395856ce81f2b7382dee72602f798b642f14140",
    "sha256": "275a021bbfb6489e54d471899f7db9d1663fc695ec2fe2a2c4538aabf651fd0f",
    "upload_timestamp": "2025-09-22T18:19:00.000Z"
  },
  "process_info": {"result": "Infected", "profile": "File scan", "blocked_reason": "Infected"}
}`

// A miss: the scan ran, 0 of 37.
const mdCleanBody = `{
  "file_id": "bzIyMDkyMmNsZWFuMDAx",
  "data_id": "MjA5MjJjbGVhbg",
  "scan_results": {
    "scan_details": {
      "Ahnlab": {"threat_found": "", "scan_result_i": 0, "def_time": "2025-09-22T09:00:00.000Z", "scan_time": 10},
      "Bitdefender": {"threat_found": "", "scan_result_i": 0, "def_time": "2025-09-22T08:00:00.000Z", "scan_time": 33}
    },
    "rescan_available": true,
    "scan_all_result_a": "No Threat Detected",
    "scan_all_result_i": 0,
    "start_time": "2025-09-22T18:20:00.000Z",
    "total_avs": 37,
    "total_detected_avs": 0,
    "total_time": 1900,
    "progress_percentage": 100
  },
  "file_info": {
    "display_name": "quarterly-report.pdf",
    "file_size": 184320,
    "file_type_category": "D",
    "sha256": "275a021bbfb6489e54d471899f7db9d1663fc695ec2fe2a2c4538aabf651fd0f"
  },
  "process_info": {"result": "Allowed", "profile": "File scan"}
}`

// mdError builds OPSWAT's error envelope. The code is a NUMBER and the
// messages are an ARRAY — an implementation that copied VirusTotal's
// string-code decoder will not decode this at all, and must then report
// Unavailable rather than guessing from the status.
func mdError(code int, message string) string {
	return fmt.Sprintf(`{"error": {"code": %d, "messages": [%q]}}`, code, message)
}

// ---------------------------------------------------------------------------

// All five states, MetaDefender being the only provider that can reach the
// fifth one from a real response.
func TestMetaDefender_Lookup_States(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   reputation.State
		why    string
	}{
		{
			name: "engines flagged it", status: http.StatusOK, body: mdDetectedBody,
			want: reputation.Detected,
		},
		{
			name: "scan ran, nothing flagged it", status: http.StatusOK, body: mdCleanBody,
			want: reputation.Clean,
		},
		{
			name:   "never seen this hash",
			status: http.StatusNotFound,
			body:   mdError(404003, "The hash was not found"),
			want:   reputation.Unseen,
		},
		{
			// 404011: "scanned as a private file and we do not store the
			// file". They know the hash and hold no verdict. Collapsing this
			// into Unseen loses a real distinction; collapsing it into Clean
			// would be a lie.
			name:   "known hash, scanned privately, no verdict held",
			status: http.StatusNotFound,
			body:   mdError(404011, "The hash was scanned as a private file and we do not store the file"),
			want:   reputation.Unscanned,
			why:    "404011 is neither unseen nor clean",
		},
		{
			name:   "bad api key",
			status: http.StatusUnauthorized,
			body:   mdError(401000, "Invalid apikey"),
			want:   reputation.Unavailable,
		},
		{
			name:   "rate limited, try again shortly",
			status: http.StatusTooManyRequests,
			body:   mdError(429001, "Too many requests, please try again later"),
			want:   reputation.Unavailable,
		},
		{
			name:   "daily quota gone until 00:00 UTC",
			status: http.StatusTooManyRequests,
			body:   mdError(429000, "API key limit exceeded"),
			want:   reputation.Unavailable,
		},
		{
			name: "server error", status: http.StatusInternalServerError, body: mdError(500000, "Internal server error"),
			want: reputation.Unavailable,
		},
		{
			// Any other 404 code is a response we do not understand. Reading
			// it as Unseen would manufacture a fact out of a parse failure.
			name:   "404 with a code we do not recognise",
			status: http.StatusNotFound,
			body:   mdError(404999, "Something else"),
			want:   reputation.Unavailable,
		},
		{
			name: "404 with no parseable body at all", status: http.StatusNotFound, body: `<html>404</html>`,
			want: reputation.Unavailable,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := serveCanned(t, tc.status, tc.body)
			got, _ := newMetaDefender(t, srv.URL, "test-key").Lookup(context.Background(), eicarSHA)
			require.Equal(t, tc.want, got.State, tc.why)
		})
	}
}

// The two 404 codes share a status and mean opposite things about whether the
// provider has ever heard of the file. An implementation branching on the
// status rather than the code returns the same state for both, and this is the
// test that notices.
func TestMetaDefender_Lookup_TheTwo404sDoNotCollapse(t *testing.T) {
	lookup := func(body string) reputation.State {
		srv := serveCanned(t, http.StatusNotFound, body)
		got, _ := newMetaDefender(t, srv.URL, "test-key").Lookup(context.Background(), eicarSHA)
		return got.State
	}
	unseen := lookup(mdError(404003, "The hash was not found"))
	unscanned := lookup(mdError(404011, "Not stored"))

	require.Equal(t, reputation.Unseen, unseen)
	require.Equal(t, reputation.Unscanned, unscanned)
	require.NotEqual(t, unseen, unscanned,
		"branching on the 404 status instead of the code loses the distinction entirely")
}

// The counts come from two explicit fields, not from summing a map. Pinned
// because an implementation shared with VirusTotal's stats-summing code is a
// plausible and wrong way to write this.
func TestMetaDefender_Lookup_ReadsTheCountsAndTheThreatName(t *testing.T) {
	srv := serveCanned(t, http.StatusOK, mdDetectedBody)
	got, err := newMetaDefender(t, srv.URL, "test-key").Lookup(context.Background(), eicarSHA)
	require.NoError(t, err)

	require.Equal(t, reputation.Detected, got.State)
	require.Equal(t, 21, got.Detected, "total_detected_avs")
	require.Equal(t, 37, got.Total, "total_avs")
	require.Equal(t, "EICAR_Test_File", got.ThreatName)
}

// A clean verdict keeps its denominator, for the same reason it does on the
// VirusTotal side: "0 of 37" is a statement and "0 of 0" is a missing lookup.
func TestMetaDefender_Lookup_CleanKeepsItsDenominator(t *testing.T) {
	srv := serveCanned(t, http.StatusOK, mdCleanBody)
	got, err := newMetaDefender(t, srv.URL, "test-key").Lookup(context.Background(), eicarSHA)
	require.NoError(t, err)

	require.Equal(t, reputation.Clean, got.State)
	require.Equal(t, 0, got.Detected)
	require.Equal(t, 37, got.Total)
	require.Empty(t, got.ThreatName, "nothing was found; naming a threat here would be a fabrication")
}

// start_time is RFC3339 with milliseconds — the opposite of VirusTotal's Unix
// seconds. An implementation that ran strconv over it, or that fed it to
// time.Unix, lands nowhere near 2025.
func TestMetaDefender_Lookup_AnalysisDateIsRFC3339(t *testing.T) {
	srv := serveCanned(t, http.StatusOK, mdDetectedBody)
	got, err := newMetaDefender(t, srv.URL, "test-key").Lookup(context.Background(), eicarSHA)
	require.NoError(t, err)

	require.NotNil(t, got.AnalysedAt, "the body carried scan_results.start_time")
	want, perr := time.Parse(time.RFC3339, mdAnalysedAt)
	require.NoError(t, perr, "bad test fixture")
	require.True(t, got.AnalysedAt.UTC().Equal(want.UTC()),
		"want %s, got %s", want.UTC(), got.AnalysedAt.UTC())
}

// Wrong path or wrong header name and every lookup becomes a 401 or a 404 that
// the state table above would classify with a straight face. The key travels
// in a header called apikey — not x-apikey, which is VirusTotal's — and never
// in the URL, where it would land in access logs.
func TestMetaDefender_Lookup_SendsTheDocumentedRequest(t *testing.T) {
	srv := serveCanned(t, http.StatusOK, mdCleanBody)
	_, err := newMetaDefender(t, srv.URL, "s3cret-key").Lookup(context.Background(), eicarSHA)
	require.NoError(t, err)

	req := srv.lastRequest()
	require.NotNil(t, req, "no request reached the server")
	require.Equal(t, http.MethodGet, req.Method)
	require.Equal(t, "/v4/hash/"+eicarSHA, req.URL.Path)
	require.Equal(t, "s3cret-key", req.Header.Get("apikey"),
		"MetaDefender reads the key from apikey")
	require.Empty(t, req.Header.Get("x-apikey"), "x-apikey is VirusTotal's header, not OPSWAT's")
	require.Empty(t, req.URL.RawQuery, "the key must not travel in the URL")
}

// Same argument as on the VirusTotal side: 429001 clears in moments, 429000
// not until 00:00 UTC, and the status cannot tell them apart. See the OPEN
// QUESTION on TestVirusTotal_Lookup_RateLimitAndQuotaAreDistinguishable for
// why this pins the operator-facing reason rather than a sentinel error.
func TestMetaDefender_Lookup_RateLimitAndQuotaAreDistinguishable(t *testing.T) {
	lookup := func(body string) error {
		srv := serveCanned(t, http.StatusTooManyRequests, body)
		_, err := newMetaDefender(t, srv.URL, "test-key").Lookup(context.Background(), eicarSHA)
		return err
	}

	rateLimited := lookup(mdError(429001, "Too many requests, please try again later"))
	quotaGone := lookup(mdError(429000, "API key limit exceeded"))

	require.Error(t, rateLimited)
	require.Error(t, quotaGone)
	require.NotEqual(t, rateLimited.Error(), quotaGone.Error(),
		"a moment's throttle and a day's quota exhaustion are different events")
	require.Contains(t, rateLimited.Error(), "429001")
	require.Contains(t, quotaGone.Error(), "429000")
}

// Never log the key.
func TestMetaDefender_Lookup_ErrorNeverCarriesTheKey(t *testing.T) {
	const key = "md-live-key-do-not-log"
	srv := serveCanned(t, http.StatusUnauthorized, mdError(401000, "Invalid apikey"))
	_, err := newMetaDefender(t, srv.URL, key).Lookup(context.Background(), eicarSHA)
	require.Error(t, err)
	require.NotContains(t, err.Error(), key, "the API key is write-only; it must not reach a log")
}
