package reputation_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
)

// ---------------------------------------------------------------------------
// PolySwarm, the third provider.
//
// The seam is the same functional option the other two use, so this file needs
// no machinery of its own beyond the constructor below — serveCanned, eicarSHA
// and serveSlow all come from reputation_test.go.
// ---------------------------------------------------------------------------

func newPolySwarm(t *testing.T, baseURL, apiKey string) reputation.Provider {
	t.Helper()
	return reputation.NewPolySwarm(apiKey, reputation.WithBaseURL(baseURL))
}

// ---------------------------------------------------------------------------
// Fixtures.
//
// Every body below is one `result` entry, because that is the shape the hash
// search returns: an array of artifact instances for the one hash asked about.
// ---------------------------------------------------------------------------

// psDetected is a settled instance nine of twelve engines flagged.
//
// The metadata array is the point of its shape: polyunite is neither first nor
// last, and it sits among the enrichment tools the real response carries. An
// implementation that reads metadata[0], or the last entry, or "the first one
// with a malware_family", passes on a one-element fixture and fails here.
const psDetected = `{"result":[{
  "sha256":"275a021bbfb6489e54d471899f7db9d1663fc695ec2fe2a2c4538aabf651fd0f",
  "state":"SETTLED",
  "polyscore":0.94,
  "detections":{"benign":3,"malicious":9,"total":12},
  "assertions":[{"engine":{"name":"one"},"verdict":true},{"engine":{"name":"two"},"verdict":false}],
  "metadata":[
    {"tool":"exiftool","tool_metadata":{"FileType":"EXE"}},
    {"tool":"hash","tool_metadata":{"md5":"44d88612fea8a8f36de82e1278abb02f"}},
    {"tool":"polyunite","tool_metadata":{"malware_family":"Emotet"}},
    {"tool":"pefile","tool_metadata":{"is_dll":false}},
    {"tool":"cape_sandbox_v2","tool_metadata":{"score":9}}
  ],
  "last_scanned":"2026-08-14T09:12:33Z",
  "permalink":"https://polyswarm.network/scan/results/file/None"
}]}`

// psClean is a settled instance no engine flagged.
const psClean = `{"result":[{
  "sha256":"275a021bbfb6489e54d471899f7db9d1663fc695ec2fe2a2c4538aabf651fd0f",
  "state":"SETTLED",
  "polyscore":0.12,
  "detections":{"benign":12,"malicious":0,"total":12},
  "assertions":[{"engine":{"name":"one"},"verdict":false}],
  "metadata":[],
  "last_scanned":"2026-08-14T09:12:33Z"
}]}`

// psKnownGood is PolySwarm's KNOWN_GOOD, which maps to our "known".
//
// Note what it does NOT have: assertions is empty and detections is null,
// exactly like the unscanned case below. The only thing telling them apart is
// `state`, which is why it has to be read first.
const psKnownGood = `{"result":[{
  "sha256":"275a021bbfb6489e54d471899f7db9d1663fc695ec2fe2a2c4538aabf651fd0f",
  "state":"KNOWN_GOOD",
  "polyscore":0.0,
  "detections":null,
  "assertions":[],
  "known_good":[
    {"tool":"nsrl","tool_metadata":{},"created":"2026-08-01T00:00:00Z","updated":"2026-08-01T00:00:00Z"},
    {"tool":"microsoft_windows","tool_metadata":{},"created":"2026-08-01T00:00:00Z"},
    {"tool":"nsrl","tool_metadata":{},"created":"2026-08-02T00:00:00Z"}
  ],
  "metadata":[]
}]}`

// psUnscanned is an instance PolySwarm holds but has no verdict for.
const psUnscanned = `{"result":[{
  "sha256":"275a021bbfb6489e54d471899f7db9d1663fc695ec2fe2a2c4538aabf651fd0f",
  "state":"AWAITING",
  "detections":null,
  "assertions":[],
  "metadata":[]
}]}`

// ---------------------------------------------------------------------------
// Name, link and the request we send.
// ---------------------------------------------------------------------------

// Name() is written into attachment_reputation.provider and compared against
// the attachment_reputation_provider setting. A disagreement of one character
// makes every cached verdict unreachable and every render spend a lookup.
func TestPolySwarm_Name(t *testing.T) {
	require.Equal(t, reputation.ProviderPolySwarm, reputation.NewPolySwarm("k").Name())
}

// The link is built from the hash we already hold, never echoed from the
// response's `permalink`: that field comes in two forms and one recorded
// response carried the literal string "None" where the hash belonged. It also
// needs no key, because the link half of this feature works on an instance
// that has configured no lookup at all.
func TestPolySwarm_LinkURL(t *testing.T) {
	require.Equal(t,
		"https://polyswarm.network/scan/results/file/"+eicarSHA,
		reputation.NewPolySwarm("").LinkURL(eicarSHA))
}

// The request PolySwarm actually accepts.
//
// Two things here are measured rather than read, and both fail silently if
// they regress: the key goes in Authorization with NO "Bearer " prefix — with
// one, the server reads the whole header value as the key and rejects it on
// length — and the community has to be named or the search is not scoped.
//
// The key must also not reach the query string, where it would land in every
// access log between here and them and defeat storing it write-only.
func TestPolySwarm_RequestShape(t *testing.T) {
	const key = "ps-secret-key"
	srv := serveCanned(t, http.StatusOK, psClean)

	_, err := newPolySwarm(t, srv.URL, key).Lookup(context.Background(), eicarSHA)
	require.NoError(t, err)

	req := srv.lastRequest()
	require.NotNil(t, req)
	require.Equal(t, "/v3/search/hash/sha256", req.URL.Path)
	require.Equal(t, eicarSHA, req.URL.Query().Get("hash"))
	require.Equal(t, "default", req.URL.Query().Get("community"),
		"an unscoped search is not the search this provider documents")

	require.Equal(t, key, req.Header.Get("Authorization"),
		"the key is the whole header value; a Bearer prefix is rejected on length")
	require.NotContains(t, req.Header.Get("Authorization"), "Bearer")
	require.NotContains(t, req.URL.RawQuery, key, "the key must never reach a query string")
}

// ---------------------------------------------------------------------------
// The state mapping.
// ---------------------------------------------------------------------------

func TestPolySwarm_StateMapping(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   reputation.State
	}{
		{"204 is a hash they have never seen", http.StatusNoContent, "", reputation.Unseen},
		{"an empty result array is the same thing", http.StatusOK, `{"result":[]}`, reputation.Unseen},
		{"no result key at all", http.StatusOK, `{}`, reputation.Unseen},
		{"known but unsettled", http.StatusOK, psUnscanned, reputation.Unscanned},
		{"settled with no detections", http.StatusOK, psClean, reputation.Clean},
		{"settled with detections", http.StatusOK, psDetected, reputation.Detected},
		{"carried by a named feed", http.StatusOK, psKnownGood, reputation.Known},
		{"their outage", http.StatusInternalServerError, `{"message":"oops"}`, reputation.Unavailable},
		{"a bad gateway in front of them", http.StatusBadGateway, `<html>502</html>`, reputation.Unavailable},
		{"a body that is not JSON", http.StatusOK, `not json at all`, reputation.Unavailable},
		{"a status nobody documents", http.StatusTeapot, `{}`, reputation.Unavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := newPolySwarm(t, serveCanned(t, tc.status, tc.body).URL, "k").
				Lookup(context.Background(), eicarSHA)
			require.Equal(t, tc.want, got.State)
		})
	}
}

// KNOWN_GOOD is read before the absence of assertions, and that order is the
// whole test.
//
// A file answered out of a catalogue is never scanned or sandboxed — the
// answer comes straight from the hash match — so it arrives with no assertions
// and a null detections, which is byte for byte what an instance nobody has
// looked at arrives with. An implementation that checks for emptiness first
// reports the strongest positive signal this system can produce as "nobody
// looked".
func TestPolySwarm_KnownIsReadBeforeTheEmptyAssertions(t *testing.T) {
	got, err := newPolySwarm(t, serveCanned(t, http.StatusOK, psKnownGood).URL, "k").
		Lookup(context.Background(), eicarSHA)
	require.NoError(t, err)

	require.Equal(t, reputation.Known, got.State,
		"an empty assertions array on a KNOWN_GOOD instance is not 'nobody looked'")
	require.NotEqual(t, reputation.Unscanned, got.State)
	require.NotEqual(t, reputation.Clean, got.State,
		"clean means engines ran and found nothing; this is a named feed having the hash on file")
}

// The feeds come back with the verdict, sorted and de-duplicated.
//
// They are what the state deliberately does not say. An Authenticode signature
// assertion from the microsoft_windows feed is a claim that the file is signed
// and trusted; an NSRL catalogue entry says only that it appeared in a
// software distribution, and NSRL catalogues hacking tools. A staff member
// deciding whether to trust a binary needs to see which one is speaking, and a
// bare "known" with no feed named is a claim from nowhere.
//
// Sorted because the order is the server's and we render it; de-duplicated
// because the fixture's feed appears twice, as a real one does when two
// catalogue entries match the same hash.
func TestPolySwarm_KnownCarriesTheFeeds(t *testing.T) {
	got, err := newPolySwarm(t, serveCanned(t, http.StatusOK, psKnownGood).URL, "k").
		Lookup(context.Background(), eicarSHA)
	require.NoError(t, err)

	require.Equal(t, []string{"microsoft_windows", "nsrl"}, got.KnownFeeds)
}

// Every other state carries no feeds. An empty list is the honest rendering of
// "nobody has this file catalogued", and a leftover one would attach a
// positive claim to a file nothing said anything about.
func TestPolySwarm_OnlyKnownCarriesFeeds(t *testing.T) {
	for name, body := range map[string]string{
		"detected":  psDetected,
		"clean":     psClean,
		"unscanned": psUnscanned,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := newPolySwarm(t, serveCanned(t, http.StatusOK, body).URL, "k").
				Lookup(context.Background(), eicarSHA)
			require.NoError(t, err)
			require.Empty(t, got.KnownFeeds)
		})
	}
}

// A null detections is not a zero detections.
//
// The field is absent rather than zeroed on an instance that has not settled,
// so a decoder that lands on Go's zero value reports 0 of 0 — and 0 of 0 is a
// missing lookup wearing a clean verdict's clothes. Nothing must come out of
// this arm carrying counts.
func TestPolySwarm_NullDetectionsIsNotZeroOfZero(t *testing.T) {
	got, err := newPolySwarm(t, serveCanned(t, http.StatusOK, psUnscanned).URL, "k").
		Lookup(context.Background(), eicarSHA)
	require.NoError(t, err)

	require.NotEqual(t, reputation.Clean, got.State, "a null detections is not a clean verdict")
	require.Equal(t, reputation.Unscanned, got.State)
	require.Zero(t, got.Detected)
	require.Zero(t, got.Total)
}

// An explicit zero total is the same refusal by a different route: an instance
// whose detections object is present but says no engine ran has still told us
// nothing, and "0 of 0 engines found anything" is the most reassuring sentence
// this feature can print.
func TestPolySwarm_ZeroEnginesIsNotClean(t *testing.T) {
	const body = `{"result":[{"state":"SETTLED","assertions":[],
      "detections":{"benign":0,"malicious":0,"total":0},"metadata":[]}]}`

	got, err := newPolySwarm(t, serveCanned(t, http.StatusOK, body).URL, "k").
		Lookup(context.Background(), eicarSHA)
	require.NoError(t, err)
	require.NotEqual(t, reputation.Clean, got.State, "no engine ran, so nothing is clean")
	require.Zero(t, got.Total)
}

// The counts staff act on come from detections, and they are the provider's
// own numbers rather than a length of the assertions array.
func TestPolySwarm_CountsComeFromDetections(t *testing.T) {
	got, err := newPolySwarm(t, serveCanned(t, http.StatusOK, psDetected).URL, "k").
		Lookup(context.Background(), eicarSHA)
	require.NoError(t, err)

	require.Equal(t, reputation.Detected, got.State)
	require.Equal(t, 9, got.Detected)
	require.Equal(t, 12, got.Total,
		"total is every engine that ran, not the two assertions in the fixture")
}

// ---------------------------------------------------------------------------
// The threat name.
// ---------------------------------------------------------------------------

// The name is polyunite's, looked up by the tool key.
//
// The metadata array carries exiftool, hash, pefile and sandbox entries in an
// order nobody documents, and the per-engine names disagree with each other —
// one real file has six engines calling it six different things. Picking one
// by position is the bug this codebase has already fixed once, in
// MetaDefender's scan_details.
func TestPolySwarm_ThreatNameComesFromPolyunite(t *testing.T) {
	got, err := newPolySwarm(t, serveCanned(t, http.StatusOK, psDetected).URL, "k").
		Lookup(context.Background(), eicarSHA)
	require.NoError(t, err)
	require.Equal(t, "Emotet", got.ThreatName,
		"the name must be read from the entry whose tool is polyunite, not from a position")
}

// Moving polyunite to the other end of the array must not change the answer.
// An implementation reading metadata[0] passes the test above and fails this
// one.
func TestPolySwarm_ThreatNameDoesNotDependOnTheEntryPosition(t *testing.T) {
	bodies := map[string]string{
		"first": `{"result":[{"state":"SETTLED","detections":{"malicious":9,"total":12},"metadata":[
          {"tool":"polyunite","tool_metadata":{"malware_family":"Emotet"}},
          {"tool":"exiftool","tool_metadata":{"FileType":"EXE"}}]}]}`,
		"last": `{"result":[{"state":"SETTLED","detections":{"malicious":9,"total":12},"metadata":[
          {"tool":"exiftool","tool_metadata":{"FileType":"EXE"}},
          {"tool":"polyunite","tool_metadata":{"malware_family":"Emotet"}}]}]}`,
		"among sandboxes": `{"result":[{"state":"SETTLED","detections":{"malicious":9,"total":12},"metadata":[
          {"tool":"cape_sandbox_v2","tool_metadata":{"malware_family":"NotThisOne"}},
          {"tool":"polyunite","tool_metadata":{"malware_family":"Emotet"}},
          {"tool":"triage_sandbox_v0","tool_metadata":{"malware_family":"NorThisOne"}}]}]}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			got, err := newPolySwarm(t, serveCanned(t, http.StatusOK, body).URL, "k").
				Lookup(context.Background(), eicarSHA)
			require.NoError(t, err)
			require.Equal(t, "Emotet", got.ThreatName)
		})
	}
}

// No polyunite entry means no name, not another tool's guess. A detection with
// an empty name is a complete answer; a detection named by whichever sandbox
// happened to be in the array is a wrong one.
func TestPolySwarm_NoPolyuniteEntryMeansNoThreatName(t *testing.T) {
	const body = `{"result":[{"state":"SETTLED","detections":{"malicious":9,"total":12},"metadata":[
      {"tool":"exiftool","tool_metadata":{"FileType":"EXE"}},
      {"tool":"cape_sandbox_v2","tool_metadata":{"malware_family":"NotThisOne"}}]}]}`

	got, err := newPolySwarm(t, serveCanned(t, http.StatusOK, body).URL, "k").
		Lookup(context.Background(), eicarSHA)
	require.NoError(t, err)
	require.Equal(t, reputation.Detected, got.State)
	require.Empty(t, got.ThreatName)
}

// ---------------------------------------------------------------------------
// Failure.
// ---------------------------------------------------------------------------

// Both meanings of 429 are ErrRateLimited.
//
// PolySwarm returns one status for a per-minute throttle and for an exhausted
// quota, with no Retry-After and no rate-limit headers; their own SDK cannot
// tell the two apart either. Mapping both to the throttle costs a retry that
// fails; string-matching their undocumented prose to do better would break the
// day they reword it, and break silently.
func TestPolySwarm_BothMeaningsOf429AreRateLimited(t *testing.T) {
	bodies := map[string]string{
		"a throttle":            `{"message":"Too many requests"}`,
		"a spent quota":         `{"message":"You have exceeded your monthly quota"}`,
		"no body to read":       ``,
		"prose we cannot parse": `<html>429</html>`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			got, err := newPolySwarm(t, serveCanned(t, http.StatusTooManyRequests, body).URL, "k").
				Lookup(context.Background(), eicarSHA)

			require.Equal(t, reputation.Unavailable, got.State)
			require.ErrorIs(t, err, reputation.ErrRateLimited)
			require.NotErrorIs(t, err, reputation.ErrQuotaExceeded,
				"PolySwarm does not distinguish the two, so we must not claim to")
		})
	}
}

// No key configured is not a lookup that fails: it is a lookup that never
// happens. A misconfigured instance degrades to "not checked", and no request
// leaves the process.
func TestPolySwarm_NoAPIKeyMakesNoRequest(t *testing.T) {
	srv := serveCanned(t, http.StatusOK, psClean)

	got, err := newPolySwarm(t, srv.URL, "").Lookup(context.Background(), eicarSHA)

	require.ErrorIs(t, err, reputation.ErrNoAPIKey)
	require.Equal(t, reputation.Unavailable, got.State)
	require.Zero(t, srv.count(), "an unconfigured provider must not call anybody")
}

// The key is stored write-only and every error here reaches the server log.
// It must not be in one, and a rejection is where a careless implementation
// puts it.
func TestPolySwarm_TheKeyIsNeverInAnError(t *testing.T) {
	const key = "ps-secret-key"
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError} {
		got, err := newPolySwarm(t, serveCanned(t, status, `{"message":"nope"}`).URL, key).
			Lookup(context.Background(), eicarSHA)

		require.Equal(t, reputation.Unavailable, got.State)
		require.Error(t, err)
		require.NotContains(t, err.Error(), key, "status %d leaked the API key into an error", status)
	}
}

// A third party that never answers must not pin a help desk handler open. The
// caller's context bounds it and the result is Unavailable, never Clean.
func TestPolySwarm_AHungProviderIsUnavailable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	got, err := newPolySwarm(t, serveSlow(t), "k").Lookup(ctx, eicarSHA)
	require.Error(t, err)
	require.Equal(t, reputation.Unavailable, got.State)
}

// ---------------------------------------------------------------------------
// The name a person reads, and the value the setting accepts.
// ---------------------------------------------------------------------------

// A third provider means a third accepted setting value and a third display
// name — and the display name still comes back empty for anything else, which
// is what keeps an operator's typo out of a sentence that attributes a claim.
func TestPolySwarm_DisplayNameAndValidProvider(t *testing.T) {
	require.Equal(t, "PolySwarm", reputation.DisplayName(reputation.ProviderPolySwarm))
	require.True(t, reputation.ValidProvider(reputation.ProviderPolySwarm))

	require.Empty(t, reputation.DisplayName("PolySwarm"), "the display name is not an identifier")
	require.Empty(t, reputation.DisplayName("poly-swarm"))
	require.False(t, reputation.ValidProvider("poly-swarm"))
	require.False(t, reputation.ValidProvider("polyswarm "))
}
