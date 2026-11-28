package reputation_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
)

// A provider that answers with a redirect does not get to send the operator's
// API key somewhere else, and does not get to make this server fetch a URL of
// its choosing.
//
// Go's default redirect policy strips the Authorization header when the host
// changes and leaves every other header alone. Two of the four providers
// authenticate with a header of their own — VirusTotal's x-apikey and
// MetaDefender's apikey — so under that policy a 302 sends the operator's key
// to whatever host the redirect names, over plain http if it names one. Only
// a provider that is hostile or compromised can send that redirect, and it
// already holds the key; what it must not be able to do is pass the key on to
// a third party, or use this server as a way to reach an address on the
// operator's own network.
//
// None of the four APIs redirects on a hash lookup, so nothing legitimate is
// lost. The lookup comes back "unavailable", which is the honest reading of a
// provider that did not answer the question.
func TestProviders_DoNotFollowARedirect(t *testing.T) {
	var elsewhere struct {
		sync.Mutex
		hits    int
		headers http.Header
	}
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhere.Lock()
		elsewhere.hits++
		elsewhere.headers = r.Header.Clone()
		elsewhere.Unlock()
		w.Header().Set("Content-Type", "application/json")
		// A body the real parser would be happy with, so that a followed
		// redirect produces a verdict rather than a decode error. Without
		// this the test could pass for the wrong reason.
		_, _ = w.Write([]byte(`{"data":{"attributes":{"last_analysis_stats":{"malicious":0,"undetected":70}}}}`))
	}))
	defer dest.Close()

	var redirects int
	var mu sync.Mutex
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		redirects++
		mu.Unlock()
		http.Redirect(w, r, dest.URL+"/redirected", http.StatusFound)
	}))
	defer src.Close()

	const key = "SECRET-KEY-VALUE"

	cases := []struct {
		name     string
		provider reputation.Provider
	}{
		{"VirusTotal, which authenticates with x-apikey", reputation.NewVirusTotal(key, reputation.WithBaseURL(src.URL))},
		{"MetaDefender, which authenticates with apikey", reputation.NewMetaDefender(key, reputation.WithBaseURL(src.URL))},
		{"PolySwarm", reputation.NewPolySwarm(key, reputation.WithBaseURL(src.URL))},
		{"CIRCL, which authenticates with nothing", reputation.NewCIRCL(reputation.WithBaseURL(src.URL))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := func() int {
				elsewhere.Lock()
				defer elsewhere.Unlock()
				return elsewhere.hits
			}()

			rep, err := tc.provider.Lookup(context.Background(), eicarSHA)

			require.Equal(t, before, func() int {
				elsewhere.Lock()
				defer elsewhere.Unlock()
				return elsewhere.hits
			}(), "the redirect was followed: this server fetched an address the provider chose")

			// The answer is "no answer". Specifically not a verdict: a
			// redirect body is not a verdict, and rendering one as clean is
			// the failure the whole package exists to prevent.
			require.Equal(t, reputation.Unavailable, rep.State,
				"a redirect is not an answer, and must not read as one (err=%v)", err)
		})
	}

	require.Equal(t, 4, redirects, "each provider should have made exactly one request")

	elsewhere.Lock()
	defer elsewhere.Unlock()
	for _, h := range []string{"X-Apikey", "Apikey", "Authorization"} {
		require.Empty(t, elsewhere.headers.Get(h),
			"%s reached the redirect target", h)
	}
}
