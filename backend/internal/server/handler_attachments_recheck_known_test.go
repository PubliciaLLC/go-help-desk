package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
)

// A "known" verdict is not re-checked either, and the refusal has to say so in
// its own words (#168).
//
// Two verdicts are final and they are opposites. A detection is final because
// engines do not un-flag a file; a "known" file is final because a hash does
// not fall out of a vendor catalogue. Both refusals travel as the same error
// code, because to a client they are the same outcome — but the sentence the
// reader sees cannot be the same sentence.
//
// The failure this pins is a specific one: telling a staff member that a file
// NSRL has on record is "already identified as malicious" is worse than
// refusing with no explanation at all. It is false, it is alarming, and it is
// about the one verdict in this feature the reader is entitled to find
// reassuring.
func TestRecheckReputation_AKnownVerdictIsNotReChecked(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	rig := newRecheckRig(t, h, repRespond200(vtCleanRecheckBody))
	rig.setKey(t, "vt-key")

	tk, attID := rig.quarantined(t, "setup.exe.zip")
	rig.seed(t, reputation.Reputation{
		State:      reputation.Known,
		KnownFeeds: []string{"nsrl"},
	}, 400*recheckDay)

	res, body := rig.recheck(t, h.apiKey, tk, attID)

	require.Equal(t, http.StatusConflict, res.StatusCode, "%s", body)
	require.Contains(t, string(body), "detection_is_final",
		"the code is the same outcome to a client: this verdict cannot change")
	require.Equal(t, int64(0), rig.hits.Load(),
		"re-confirming a vendor catalogue is a lookup spent to learn nothing")

	require.NotContains(t, strings.ToLower(string(body)), "malicious",
		"a file a named feed has on record must not be described as malware")
	require.Contains(t, strings.ToLower(string(body)), "known",
		"the refusal has to say which of the two final verdicts it is refusing on")
	require.Contains(t, strings.ToLower(string(body)), "catalogue",
		"and it has to say what 'known' rests on, which is a feed having the hash on file")
}

// The companion, so the fix above cannot be "stop saying malicious anywhere":
// a real detection still says what it is.
func TestRecheckReputation_ADetectionsRefusalStillSaysMalicious(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	rig := newRecheckRig(t, h, repRespond200(vtCleanRecheckBody))
	rig.setKey(t, "vt-key")

	tk, attID := rig.quarantined(t, "invoice.exe.zip")
	rig.seed(t, reputation.Reputation{State: reputation.Detected, Detected: 62, Total: 81}, 400*recheckDay)

	res, body := rig.recheck(t, h.apiKey, tk, attID)

	require.Equal(t, http.StatusConflict, res.StatusCode, "%s", body)
	require.Contains(t, strings.ToLower(string(body)), "malicious")
}
