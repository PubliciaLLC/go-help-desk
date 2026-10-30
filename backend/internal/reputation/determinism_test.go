package reputation_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
)

// A response where no engine ran is not a clean bill of health.
//
// Found by mutation: deleting the guard broke nothing, because every fixture
// had engines in it. "0 of 0" is a missing lookup wearing a clean verdict's
// clothes, and it renders as the most reassuring thing this feature can say —
// which makes it the single worst state to arrive at by accident.
func TestVirusTotal_NoEngineRanIsNotClean(t *testing.T) {
	const body = `{"data":{"id":"x","type":"file","attributes":{
      "last_analysis_stats":{"malicious":0,"suspicious":0,"undetected":0,"harmless":0,
        "timeout":0,"confirmed-timeout":0,"failure":0,"type-unsupported":0},
      "last_analysis_date":1758560400}}}`

	srv := serveCanned(t, http.StatusOK, body)
	got, err := newVirusTotal(t, srv.URL, "test-key").Lookup(context.Background(), eicarSHA)

	require.Error(t, err, "a response with no engines is not an answer")
	require.Equal(t, reputation.Unavailable, got.State,
		"0 of 0 must never be Clean; nothing was checked")
	require.Zero(t, got.Detected)
	require.Zero(t, got.Total)
}

// The same file must produce the same threat name every time it is looked up.
//
// MetaDefender has no consensus field, so the name comes from the per-engine
// results — and those arrive as a JSON object, which Go decodes into a map
// with randomised iteration order. "First engine with a name" is therefore a
// different answer on different renders of the same file, and a reader
// watching the label change between page loads would reasonably conclude the
// scan had changed.
//
// The existing fixtures cannot catch this: every engine in them names the same
// threat, which the author noted was deliberate to avoid deciding the rule.
// This one makes them disagree, which is the only way the ordering is
// observable.
func TestMetaDefender_ThreatNameIsTheSameOnEveryLookup(t *testing.T) {
	// Four engines, four different names, deliberately not in alphabetical
	// order in the JSON — so a parser that kept insertion order and one that
	// sorted would disagree.
	const body = `{"scan_results":{
      "scan_details":{
        "Zillya":      {"threat_found":"Zillya.Generic","scan_result_i":1},
        "Ahnlab":      {"threat_found":"Ahnlab.Trojan","scan_result_i":1},
        "Quickheal":   {"threat_found":"Quickheal.Worm","scan_result_i":1},
        "Bitdefender": {"threat_found":"Bitdefender.Agent","scan_result_i":1}
      },
      "scan_all_result_a":"Infected",
      "start_time":"2025-09-22T18:20:00.000Z",
      "total_avs":37,"total_detected_avs":4}}`

	p := newMetaDefender(t, serveCanned(t, http.StatusOK, body).URL, "test-key")

	first, err := p.Lookup(context.Background(), eicarSHA)
	require.NoError(t, err)
	require.Equal(t, reputation.Detected, first.State)

	// Alphabetically first by engine name. The choice is arbitrary; that it is
	// the same choice every time is the requirement.
	require.Equal(t, "Ahnlab.Trojan", first.ThreatName,
		"the name must be chosen by a rule, not by map iteration order")

	// Repeated, because map iteration is randomised per range and a single
	// call can agree with a broken implementation by luck. Go reseeds the
	// hash per map, so repeated decodes of the same body order differently.
	for i := 0; i < 50; i++ {
		again, err := p.Lookup(context.Background(), eicarSHA)
		require.NoError(t, err)
		require.Equal(t, first.ThreatName, again.ThreatName,
			"lookup %d gave a different name for the same unchanged file", i+2)
	}
}

// An engine that found nothing carries an empty threat_found and must be
// skipped rather than winning on alphabetical order — otherwise the file with
// the most boring engine name shows no threat at all.
func TestMetaDefender_AnEngineThatFoundNothingDoesNotNameTheThreat(t *testing.T) {
	const body = `{"scan_results":{
      "scan_details":{
        "AAAFirstAlphabetically": {"threat_found":"","scan_result_i":0},
        "Bitdefender":            {"threat_found":"Bitdefender.Agent","scan_result_i":1}
      },
      "scan_all_result_a":"Infected",
      "start_time":"2025-09-22T18:20:00.000Z",
      "total_avs":37,"total_detected_avs":1}}`

	got, err := newMetaDefender(t, serveCanned(t, http.StatusOK, body).URL, "test-key").
		Lookup(context.Background(), eicarSHA)
	require.NoError(t, err)
	require.Equal(t, "Bitdefender.Agent", got.ThreatName,
		"an engine with no finding has no name to contribute")
}
