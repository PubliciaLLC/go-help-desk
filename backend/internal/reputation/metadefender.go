package reputation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"
)

// mdBaseURL is the real service. WithBaseURL replaces it in tests.
const mdBaseURL = "https://api.metadefender.com"

// OPSWAT error codes we understand. Numbers, not strings — a decoder written
// for VirusTotal's envelope does not decode this one at all, which is the
// point: a body we cannot decode is Unavailable, not a guess from the status.
const (
	mdCodeHashNotFound = 404003
	mdCodePrivateFile  = 404011 // known hash, scanned privately, no verdict held
	mdCodeBadKey       = 401000
	mdCodeQuotaGone    = 429000
	mdCodeRateLimited  = 429001
)

// MetaDefender is OPSWAT's service, offered because the operator supplies the
// API key and should therefore be the one choosing whose terms they accept.
//
// OPSWAT publish no free-tier figure — their public-API documentation says
// only "a limited number of API calls per day" — so the 4,000 a day in
// budget.go is a cap WE chose, not a limit they gave. They do not throttle
// single hash lookups by the minute, so it needs a daily counter and no token
// bucket. It has a state VirusTotal does not — 404011, a file scanned
// privately and not stored — which maps to Unscanned.
type MetaDefender struct {
	client
}

// NewMetaDefender builds the provider. As with VirusTotal, an empty key means
// no lookup rather than a lookup that fails.
func NewMetaDefender(apiKey string, opts ...Option) *MetaDefender {
	return &MetaDefender{client: newClient(apiKey, mdBaseURL, opts...)}
}

var _ Provider = (*MetaDefender)(nil)

func (m *MetaDefender) Name() string { return ProviderMetaDefender }

func (m *MetaDefender) LinkURL(sha256 string) string {
	return "https://metadefender.com/results/hash/" + sha256
}

// mdHash is the slice of the v4 hash response we read. The counts are two
// explicit fields rather than a map to sum, and start_time is RFC3339 rather
// than Unix seconds — the opposite of VirusTotal in both cases, so the two
// parsers share nothing but the HTTP call.
type mdHash struct {
	ScanResults *struct {
		ScanDetails map[string]struct {
			ThreatFound string `json:"threat_found"`
		} `json:"scan_details"`
		ScanAllResultA     string `json:"scan_all_result_a"`
		StartTime          string `json:"start_time"`
		TotalAVs           int    `json:"total_avs"`
		TotalDetectedAVs   int    `json:"total_detected_avs"`
		ProgressPercentage *int   `json:"progress_percentage"`
	} `json:"scan_results"`
}

type mdErrorBody struct {
	Error *struct {
		Code int `json:"code"`
	} `json:"error"`
}

// mdErrorCode returns OPSWAT's own error code, or 0 when the body was not
// their error envelope. 0 is never a known code.
func mdErrorCode(body []byte) int {
	var e mdErrorBody
	if err := json.Unmarshal(body, &e); err != nil || e.Error == nil {
		return 0
	}
	return e.Error.Code
}

func (m *MetaDefender) Lookup(ctx context.Context, sha256 string) (Reputation, error) {
	if m.apiKey == "" {
		return unavailable(ErrNoAPIKey)
	}

	status, body, err := m.get(ctx, "/v4/hash/"+sha256, "apikey")
	if err != nil {
		return unavailable(fmt.Errorf("metadefender lookup: %w", err))
	}

	switch status {
	case http.StatusOK:
		return mdVerdict(body)

	case http.StatusNotFound:
		// The two 404s mean opposite things about whether OPSWAT has ever
		// heard of the file, and the status cannot tell them apart. Any third
		// code is a response we do not understand.
		switch code := mdErrorCode(body); code {
		case mdCodeHashNotFound:
			return Reputation{State: Unseen}, nil
		case mdCodePrivateFile:
			return Reputation{State: Unscanned}, nil
		default:
			return unavailable(fmt.Errorf("metadefender: unrecognised 404 (code %d)", code))
		}

	case http.StatusTooManyRequests:
		switch code := mdErrorCode(body); code {
		case mdCodeRateLimited:
			return unavailable(fmt.Errorf("metadefender: %d: %w", mdCodeRateLimited, ErrRateLimited))
		case mdCodeQuotaGone:
			return unavailable(fmt.Errorf("metadefender: %d: %w", mdCodeQuotaGone, ErrQuotaExceeded))
		default:
			return unavailable(fmt.Errorf("metadefender: 429 with unrecognised code %d", code))
		}

	case http.StatusUnauthorized, http.StatusForbidden:
		// The key is not in this string, deliberately. See VirusTotal.Lookup.
		return unavailable(fmt.Errorf("metadefender: rejected the API key (status %d, code %d)", status, mdErrorCode(body)))

	default:
		return unavailable(fmt.Errorf("metadefender: unexpected status %d", status))
	}
}

// mdVerdict reads a 200.
func mdVerdict(body []byte) (Reputation, error) {
	var h mdHash
	if err := json.Unmarshal(body, &h); err != nil {
		return unavailable(fmt.Errorf("metadefender: decoding response: %w", err))
	}
	r := h.ScanResults
	if r == nil {
		return unavailable(fmt.Errorf("metadefender: response carried no scan_results"))
	}
	// A scan still running is not a verdict, and caching one would freeze it:
	// a stored verdict is never re-fetched.
	if r.ProgressPercentage != nil && *r.ProgressPercentage < 100 {
		return unavailable(fmt.Errorf("metadefender: scan still in progress (%d%%)", *r.ProgressPercentage))
	}
	if r.TotalAVs <= 0 {
		// 0 of 0 is a missing lookup wearing a clean verdict's clothes.
		return unavailable(fmt.Errorf("metadefender: no engine ran"))
	}

	rep := Reputation{
		Detected: r.TotalDetectedAVs,
		Total:    r.TotalAVs,
	}
	if rep.Detected > 0 {
		rep.State = Detected
		rep.ThreatName = mdThreatName(r.ScanDetails)
	} else {
		// scan_all_result_a on a clean file reads "No Threat Detected", which
		// is a sentence and not a threat name. Nothing was found, so there is
		// nothing to name.
		rep.State = Clean
	}
	if t, err := time.Parse(time.RFC3339, r.StartTime); err == nil {
		utc := t.UTC()
		rep.AnalysedAt = &utc
	}
	return rep, nil
}

// mdThreatName picks the family name to show staff out of the per-engine
// results.
//
// OPSWAT has no consensus field to read. scan_all_result_a is the overall
// verdict — the literal word "Infected" — which tells a reader nothing they
// did not already know from the row being red. The useful name is per engine,
// in scan_details.threat_found.
//
// Sorted by engine name before choosing, which is the whole point. scan_details
// is a map and Go randomises map iteration, so "the first engine with a name"
// is a different answer on different renders of the same file. A user watching
// the label change between page loads would reasonably conclude the scan
// changed. Sorting makes the choice arbitrary but stable, which is what a
// label needs to be.
//
// Engines that found nothing carry an empty threat_found and are skipped.
func mdThreatName(details map[string]struct {
	ThreatFound string `json:"threat_found"`
}) string {
	engines := make([]string, 0, len(details))
	for name := range details {
		engines = append(engines, name)
	}
	sort.Strings(engines)
	for _, name := range engines {
		if t := details[name].ThreatFound; t != "" {
			return t
		}
	}
	return ""
}
