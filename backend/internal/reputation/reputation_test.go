package reputation_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
)

// The hash used throughout. Real EICAR SHA-256, so that anyone reading a
// canned response body can recognise what it is about.
const eicarSHA = "275a021bbfb6489e54d471899f7db9d1663fc695ec2fe2a2c4538aabf651fd0f"

// ---------------------------------------------------------------------------
// The seam.
//
// FINDING (reported, not fixed here): neither provider has any way to be
// pointed at a test server. NewVirusTotal/NewMetaDefender take an API key and
// nothing else, and the endpoint is not yet expressed anywhere, so a test
// cannot serve a canned response to one without either hitting the real
// service or reaching inside the package.
//
// The two constructors below are the single place that needs to change once
// the providers grow a base-URL seam (a functional option would match
// user.NewService(..., user.WithBcryptCost(...)), which is this codebase's
// existing shape for exactly this). Deliberately NOT adding the field here:
// writing the production API is the implementer's job, and a test file that
// referenced a symbol which does not exist would fail to build, which is not
// the same thing as failing an assertion.
//
// Until then every test below compiles, runs, reaches a stub that returns
// Reputation{}, and fails on the state assertion — which is the red we want.
// ---------------------------------------------------------------------------

// newVirusTotal returns a VirusTotal provider that talks to baseURL.
//
// TODO(#168): replace the body with
//
//	return reputation.NewVirusTotal(apiKey, reputation.WithBaseURL(baseURL))
//
// (or whatever the seam is named) as the first step of making these pass.
func newVirusTotal(t *testing.T, baseURL, apiKey string) reputation.Provider {
	t.Helper()
	return reputation.NewVirusTotal(apiKey, reputation.WithBaseURL(baseURL))
}

// newMetaDefender is the same seam for OPSWAT. See newVirusTotal.
func newMetaDefender(t *testing.T, baseURL, apiKey string) reputation.Provider {
	t.Helper()
	return reputation.NewMetaDefender(apiKey, reputation.WithBaseURL(baseURL))
}

// ---------------------------------------------------------------------------
// Test server helpers.
// ---------------------------------------------------------------------------

// canned serves one status and one body to every request, and records the last
// request it saw so a test can assert on the path and headers we sent.
type canned struct {
	URL string

	mu       sync.Mutex
	requests int
	last     *http.Request
}

func serveCanned(t *testing.T, status int, body string) *canned {
	t.Helper()
	c := &canned{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.requests++
		c.last = r.Clone(context.Background())
		c.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c.URL = srv.URL
	return c
}

func (c *canned) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests
}

func (c *canned) lastRequest() *http.Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

// serveSlow never answers within the life of a test. Used to prove that a hung
// third party produces Unavailable and does not hold the caller open — a help
// desk handler must not be pinned to someone else's outage.
func serveSlow(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// ---------------------------------------------------------------------------
// Name and LinkURL: no key, no network.
// ---------------------------------------------------------------------------

// Name() is what gets written into attachment_reputation.provider and compared
// against the attachment_reputation_provider setting. If it disagrees with the
// constants by so much as a capital letter, a cached verdict becomes
// permanently unreachable and every page render spends a lookup.
func TestProviderNames(t *testing.T) {
	require.Equal(t, reputation.ProviderVirusTotal, reputation.NewVirusTotal("k").Name())
	require.Equal(t, reputation.ProviderMetaDefender, reputation.NewMetaDefender("k").Name())
}

// LinkURL is the half of this feature that works on an instance which has
// configured no API key at all, so it is pinned with an empty key: it must
// neither require one nor make a request.
func TestLinkURL(t *testing.T) {
	cases := []struct {
		name string
		p    reputation.Provider
		want string
	}{
		{
			name: "virustotal",
			p:    reputation.NewVirusTotal(""),
			want: "https://www.virustotal.com/gui/file/" + eicarSHA,
		},
		{
			name: "metadefender",
			p:    reputation.NewMetaDefender(""),
			want: "https://metadefender.com/results/hash/" + eicarSHA,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.p.LinkURL(eicarSHA))
		})
	}
}

// ---------------------------------------------------------------------------
// The invariant the whole package exists for.
// ---------------------------------------------------------------------------

// A lookup that did not complete must report Unavailable. Not Clean, and — the
// case a zero-value return produces — not the empty string, which no renderer
// has a branch for and which therefore falls through to whatever the "nothing
// wrong here" arm draws.
//
// This is the same defect the ClamAV scanner has had fixed twice on this
// project (c47db36, abbbabe). Asserting only "an error came back" would not
// catch it: the error is for the operator, the State is what reaches the
// screen.
func TestLookup_NeverReportsCleanWithoutAnAnswer(t *testing.T) {
	type providerCase struct {
		name string
		new  func(t *testing.T, baseURL, apiKey string) reputation.Provider
		// Bodies shaped like that provider's own error envelope, so a
		// half-written parser cannot accidentally succeed on the other's.
		badKey     string
		rateLimit  string
		quotaGone  string
		serverFail string
	}
	providers := []providerCase{
		{
			name:       "virustotal",
			new:        newVirusTotal,
			badKey:     vtError("WrongCredentialsError", "Wrong API key"),
			rateLimit:  vtError("TooManyRequestsError", "Too many requests"),
			quotaGone:  vtError("QuotaExceededError", "Quota exceeded"),
			serverFail: `<html><body>502 Bad Gateway</body></html>`,
		},
		{
			name:       "metadefender",
			new:        newMetaDefender,
			badKey:     mdError(401000, "Invalid apikey"),
			rateLimit:  mdError(429001, "Too many requests, please try again later"),
			quotaGone:  mdError(429000, "API key limit exceeded"),
			serverFail: `<html><body>502 Bad Gateway</body></html>`,
		},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			failures := []struct {
				name   string
				status int
				body   string
			}{
				{"bad key", http.StatusUnauthorized, p.badKey},
				{"rate limited", http.StatusTooManyRequests, p.rateLimit},
				{"quota exhausted", http.StatusTooManyRequests, p.quotaGone},
				{"server error", http.StatusBadGateway, p.serverFail},
				{"service unavailable", http.StatusServiceUnavailable, ""},
				// A 200 we cannot decode is not an answer. Truncated bodies
				// and HTML interstitials from a proxy both land here.
				{"unparseable body", http.StatusOK, `{"data": {"attributes": `},
				{"empty body with 200", http.StatusOK, ``},
				{"html where json was promised", http.StatusOK, `<!doctype html><title>Sign in</title>`},
			}
			for _, f := range failures {
				t.Run(f.name, func(t *testing.T) {
					srv := serveCanned(t, f.status, f.body)
					got, err := p.new(t, srv.URL, "test-key").Lookup(context.Background(), eicarSHA)

					require.Equal(t, reputation.Unavailable, got.State,
						"a lookup that did not complete must say so; got %q", got.State)
					require.Error(t, err, "the operator still needs the reason")
					// Zeroes here would render as "0 of 0 engines found
					// anything", which is the false reassurance this package
					// exists to prevent — so they must be zero AND the state
					// must be the thing the renderer branches on.
					require.Zero(t, got.Detected)
					require.Zero(t, got.Total)
					require.Nil(t, got.AnalysedAt,
						"an analysis date on a failed lookup is a date we invented")
				})
			}

			// A hung provider: the context runs out before the body arrives.
			t.Run("timeout", func(t *testing.T) {
				url := serveSlow(t)
				ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
				defer cancel()

				done := make(chan reputation.Reputation, 1)
				go func() {
					got, _ := p.new(t, url, "test-key").Lookup(ctx, eicarSHA)
					done <- got
				}()

				select {
				case got := <-done:
					require.Equal(t, reputation.Unavailable, got.State,
						"a timed-out lookup is not a clean file")
				case <-time.After(5 * time.Second):
					t.Fatal("Lookup outlived its context; a hung provider must not hold a handler open")
				}
			})

			// No key is the off switch — there is no separate enabled flag —
			// so a keyless provider must not reach the network at all, and
			// must not report a verdict it never asked for.
			t.Run("no api key", func(t *testing.T) {
				srv := serveCanned(t, http.StatusOK, `{}`)
				got, err := p.new(t, srv.URL, "").Lookup(context.Background(), eicarSHA)
				require.Equal(t, reputation.Unavailable, got.State)
				require.Error(t, err)
				require.Zero(t, srv.count(), "a provider with no key must not call out")
			})
		})
	}
}

// Every state the providers can produce must be one of the five declared
// constants. A sixth value — most plausibly "" from a zero-value return on a
// path somebody forgot — has no branch anywhere downstream.
func TestLookup_StateIsAlwaysOneOfTheFive(t *testing.T) {
	valid := map[reputation.State]bool{
		reputation.Unseen:      true,
		reputation.Unscanned:   true,
		reputation.Clean:       true,
		reputation.Detected:    true,
		reputation.Unavailable: true,
	}

	cases := []struct {
		name   string
		new    func(t *testing.T, baseURL, apiKey string) reputation.Provider
		status int
		body   string
	}{
		{"vt detected", newVirusTotal, http.StatusOK, vtDetectedBody},
		{"vt clean", newVirusTotal, http.StatusOK, vtCleanBody},
		{"vt unseen", newVirusTotal, http.StatusNotFound, vtError("NotFoundError", "File not found")},
		{"vt garbage", newVirusTotal, http.StatusOK, `{`},
		{"md detected", newMetaDefender, http.StatusOK, mdDetectedBody},
		{"md clean", newMetaDefender, http.StatusOK, mdCleanBody},
		{"md unseen", newMetaDefender, http.StatusNotFound, mdError(404003, "The hash was not found")},
		{"md unscanned", newMetaDefender, http.StatusNotFound, mdError(404011, "Not Found")},
		{"md garbage", newMetaDefender, http.StatusOK, `{`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := serveCanned(t, tc.status, tc.body)
			got, _ := tc.new(t, srv.URL, "test-key").Lookup(context.Background(), eicarSHA)
			require.True(t, valid[got.State],
				"State %q is not one of the five; nothing downstream knows how to draw it", got.State)
		})
	}
}
