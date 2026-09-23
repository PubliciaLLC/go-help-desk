package reputation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// vtBaseURL is the real service. WithBaseURL replaces it in tests.
const vtBaseURL = "https://www.virustotal.com"

// VirusTotal error codes we understand. Branching on the code and never on the
// message is deliberate: messages are free text and have changed, and both
// meanings of 429 share a status, so the status alone cannot tell them apart.
const (
	vtCodeNotFound       = "NotFoundError"
	vtCodeWrongKey       = "WrongCredentialsError"
	vtCodeTooManyRequest = "TooManyRequestsError"
	vtCodeQuotaExceeded  = "QuotaExceededError"
)

// VirusTotal is the default provider.
//
// Default because of what the link does, not what the API does: VirusTotal's
// logged-out report page is the complete report, where MetaDefender's
// announces itself as a limited view and asks the reader to sign in. For a
// link whose entire job is that a person clicks it and reads it, that decides
// it.
//
// Free tier is 4 requests per minute and 500 per day, quotas resetting at
// 00:00 UTC, so the implementation will need a token bucket as well as a daily
// counter. MetaDefender needs only the counter.
type VirusTotal struct {
	client
}

// NewVirusTotal builds the provider. An empty key is the off switch: there is
// no separate enabled flag, so a caller holding no key should not build one of
// these at all.
func NewVirusTotal(apiKey string, opts ...Option) *VirusTotal {
	return &VirusTotal{client: newClient(apiKey, vtBaseURL, opts...)}
}

// Compile-time proof that the stub satisfies the contract, so a test written
// against Provider fails on its assertion rather than on the build.
var _ Provider = (*VirusTotal)(nil)

func (v *VirusTotal) Name() string { return ProviderVirusTotal }

// LinkURL needs no key and makes no request, so it works on an instance that
// has configured no lookup at all.
func (v *VirusTotal) LinkURL(sha256 string) string {
	return "https://www.virustotal.com/gui/file/" + sha256
}

// vtStats is last_analysis_stats. All EIGHT keys: Total is every engine that
// ran, and an implementation that sums only the four obvious ones reports 72
// for a file the report page calls 81.
type vtStats struct {
	Malicious        int `json:"malicious"`
	Suspicious       int `json:"suspicious"`
	Undetected       int `json:"undetected"`
	Harmless         int `json:"harmless"`
	Timeout          int `json:"timeout"`
	ConfirmedTimeout int `json:"confirmed-timeout"`
	Failure          int `json:"failure"`
	TypeUnsupported  int `json:"type-unsupported"`
}

func (s vtStats) total() int {
	return s.Malicious + s.Suspicious + s.Undetected + s.Harmless +
		s.Timeout + s.ConfirmedTimeout + s.Failure + s.TypeUnsupported
}

// vtFile is the slice of the v3 file object we read. Premium-only fields are
// absent on a free key, so every optional field is a pointer: a zero value
// decoded from a missing field would be rendered as a fact.
type vtFile struct {
	Data struct {
		Attributes struct {
			LastAnalysisStats *vtStats `json:"last_analysis_stats"`
			// UNIX SECONDS, unlike every other timestamp in this codebase.
			LastAnalysisDate            *int64 `json:"last_analysis_date"`
			PopularThreatClassification *struct {
				SuggestedThreatLabel string `json:"suggested_threat_label"`
			} `json:"popular_threat_classification"`
		} `json:"attributes"`
	} `json:"data"`
}

type vtErrorBody struct {
	Error *struct {
		Code string `json:"code"`
	} `json:"error"`
}

// vtErrorCode returns the provider's own error code, or "" when the body was
// not VirusTotal's error envelope. "" is never treated as a known code.
func vtErrorCode(body []byte) string {
	var e vtErrorBody
	if err := json.Unmarshal(body, &e); err != nil || e.Error == nil {
		return ""
	}
	return e.Error.Code
}

func (v *VirusTotal) Lookup(ctx context.Context, sha256 string) (Reputation, error) {
	if v.apiKey == "" {
		return unavailable(ErrNoAPIKey)
	}

	status, body, err := v.get(ctx, "/api/v3/files/"+sha256, "x-apikey")
	if err != nil {
		return unavailable(fmt.Errorf("virustotal lookup: %w", err))
	}

	switch status {
	case http.StatusOK:
		return vtVerdict(body)

	case http.StatusNotFound:
		// Unseen requires VirusTotal to have SAID it does not know the hash.
		// Any other 404 is a response we do not understand, and reading it as
		// "never seen" would manufacture a fact out of a parse failure.
		if code := vtErrorCode(body); code == vtCodeNotFound {
			return Reputation{State: Unseen}, nil
		}
		return unavailable(fmt.Errorf("virustotal: unrecognised 404 (code %q)", vtErrorCode(body)))

	case http.StatusTooManyRequests:
		switch code := vtErrorCode(body); code {
		case vtCodeTooManyRequest:
			return unavailable(fmt.Errorf("virustotal: %s: %w", vtCodeTooManyRequest, ErrRateLimited))
		case vtCodeQuotaExceeded:
			return unavailable(fmt.Errorf("virustotal: %s: %w", vtCodeQuotaExceeded, ErrQuotaExceeded))
		default:
			return unavailable(fmt.Errorf("virustotal: 429 with unrecognised code %q", code))
		}

	case http.StatusUnauthorized, http.StatusForbidden:
		// Note what is NOT in this error: the key. It is stored write-only and
		// this string reaches the server log.
		return unavailable(fmt.Errorf("virustotal: rejected the API key (status %d, code %q)", status, vtErrorCode(body)))

	default:
		return unavailable(fmt.Errorf("virustotal: unexpected status %d", status))
	}
}

// vtVerdict reads a 200. A body we cannot decode, or one carrying no analysis
// stats, is not an answer — it is Unavailable, never Clean.
func vtVerdict(body []byte) (Reputation, error) {
	var f vtFile
	if err := json.Unmarshal(body, &f); err != nil {
		return unavailable(fmt.Errorf("virustotal: decoding response: %w", err))
	}
	attrs := f.Data.Attributes
	if attrs.LastAnalysisStats == nil {
		return unavailable(fmt.Errorf("virustotal: response carried no last_analysis_stats"))
	}

	stats := *attrs.LastAnalysisStats
	rep := Reputation{
		// suspicious is not a detection: VirusTotal's own UI counts malicious
		// only, and inflating the number staff act on reads as corroboration
		// that is not there.
		Detected: stats.Malicious,
		Total:    stats.total(),
	}
	if rep.Total == 0 {
		// 0 of 0 is a missing lookup wearing a clean verdict's clothes.
		return unavailable(fmt.Errorf("virustotal: no engine ran"))
	}
	if rep.Detected > 0 {
		rep.State = Detected
		// The provider's own consensus label, never an engine picked out of
		// last_analysis_results: that map's iteration order is randomised, so
		// "first malicious engine wins" is a different answer on every render.
		// Absent on a free key or an unclassified file, and empty is fine.
		if c := attrs.PopularThreatClassification; c != nil {
			rep.ThreatName = c.SuggestedThreatLabel
		}
	} else {
		rep.State = Clean
	}
	if d := attrs.LastAnalysisDate; d != nil && *d > 0 {
		t := time.Unix(*d, 0).UTC()
		rep.AnalysedAt = &t
	}
	return rep, nil
}
