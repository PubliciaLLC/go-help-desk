package reputation_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
)

// A report every engine declined to judge is not a clean bill of health.
//
// last_analysis_stats has eight keys and they are not eight ways of saying the
// same thing. Four of them are verdicts — malicious, suspicious, undetected,
// harmless — and four are engines that did NOT produce one: timeout,
// confirmed-timeout, failure, and type-unsupported, which is what VirusTotal
// says when seventy-five engines all look at a file type none of them handles.
//
// Summing all eight and calling the result "engines that ran" is right for the
// number staff see, and wrong as the test for whether anything was decided:
// 0 of 75 renders as "no engine flagged this file", which is the most
// reassuring sentence this feature can write, about a file nobody examined.
// That is precisely the state this package exists to make unreachable — see
// TestVirusTotal_NoEngineRanIsNotClean, which pins the all-zero case and which
// this extends to the case where the zeroes are only in the half that matters.
func TestVirusTotal_NoEngineReturnedAVerdictIsNotClean(t *testing.T) {
	cases := []struct {
		name  string
		stats string
	}{
		{
			// The measured one: a .dmg, say, that nothing on the panel parses.
			name:  "every engine found the type unsupported",
			stats: `"malicious":0,"suspicious":0,"undetected":0,"harmless":0,"timeout":0,"confirmed-timeout":0,"failure":0,"type-unsupported":75`,
		},
		{
			name:  "every engine timed out",
			stats: `"malicious":0,"suspicious":0,"undetected":0,"harmless":0,"timeout":40,"confirmed-timeout":35,"failure":0,"type-unsupported":0`,
		},
		{
			name:  "every engine failed",
			stats: `"malicious":0,"suspicious":0,"undetected":0,"harmless":0,"timeout":0,"confirmed-timeout":0,"failure":62,"type-unsupported":0`,
		},
		{
			name:  "a mixture of the four non-verdicts",
			stats: `"malicious":0,"suspicious":0,"undetected":0,"harmless":0,"timeout":3,"confirmed-timeout":2,"failure":5,"type-unsupported":70`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"data":{"id":"x","type":"file","attributes":{
              "last_analysis_stats":{%s},
              "last_analysis_date":%d}}}`, tc.stats, vtAnalysedAt)

			srv := serveCanned(t, http.StatusOK, body)
			got, err := newVirusTotal(t, srv.URL, "test-key").Lookup(context.Background(), eicarSHA)

			require.Error(t, err, "nothing was determined, so nothing was answered")
			require.Equal(t, reputation.Unavailable, got.State,
				"engines that declined to judge are not engines that found nothing")
			require.Zero(t, got.Detected, "a lookup with no verdict has no numbers")
			require.Zero(t, got.Total, "a lookup with no verdict has no numbers")
		})
	}
}

// And the other side of the same rule: when at least one engine DID return a
// verdict, Total still counts all eight keys.
//
// Staff are entitled to see how many engines ran, including the ones that gave
// up — "1 of 81" and "1 of 6" are different facts about the same file. The fix
// above changes what makes a report an answer; it must not change the number
// that answer reports.
func TestVirusTotal_TotalStillCountsEveryEngineThatRan(t *testing.T) {
	cases := []struct {
		name         string
		stats        string
		wantState    reputation.State
		wantDetected int
		wantTotal    int
	}{
		{
			name:         "one engine judged it, seventy-four could not",
			stats:        `"malicious":0,"suspicious":0,"undetected":1,"harmless":0,"timeout":0,"confirmed-timeout":0,"failure":0,"type-unsupported":74`,
			wantState:    reputation.Clean,
			wantDetected: 0,
			wantTotal:    75,
		},
		{
			name:         "one engine flagged it, the rest gave up",
			stats:        `"malicious":1,"suspicious":0,"undetected":0,"harmless":0,"timeout":10,"confirmed-timeout":0,"failure":0,"type-unsupported":60`,
			wantState:    reputation.Detected,
			wantDetected: 1,
			wantTotal:    71,
		},
		{
			// Suspicious is a verdict for the purpose of "did anybody decide",
			// even though it is deliberately not counted as a detection.
			name:         "only suspicious votes were returned",
			stats:        `"malicious":0,"suspicious":2,"undetected":0,"harmless":0,"timeout":0,"confirmed-timeout":0,"failure":0,"type-unsupported":50`,
			wantState:    reputation.Clean,
			wantDetected: 0,
			wantTotal:    52,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"data":{"id":"x","type":"file","attributes":{
              "last_analysis_stats":{%s},
              "last_analysis_date":%d}}}`, tc.stats, vtAnalysedAt)

			srv := serveCanned(t, http.StatusOK, body)
			got, err := newVirusTotal(t, srv.URL, "test-key").Lookup(context.Background(), eicarSHA)

			require.NoError(t, err)
			require.Equal(t, tc.wantState, got.State)
			require.Equal(t, tc.wantDetected, got.Detected)
			require.Equal(t, tc.wantTotal, got.Total,
				"Total is every engine that ran, not every engine that decided")
		})
	}
}
