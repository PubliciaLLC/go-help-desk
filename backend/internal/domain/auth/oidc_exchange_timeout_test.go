package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The token exchange is the one call a sign-in makes to the identity provider
// on a request goroutine, and go-oidc/oauth2 use http.DefaultClient (no
// timeout) unless the context carries one. The client NewOIDCProvider put on
// its own context covers discovery and the key fetch, not this.
//
// Internal test: the timeout is shortened through the unexported field, because
// waiting out the real fifteen seconds in a unit test is not an option.
func TestExchange_GivesUpOnAnIdentityProviderThatNeverAnswers(t *testing.T) {
	release := make(chan struct{})
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                 srv.URL,
				"authorization_endpoint": srv.URL + "/authorize",
				"token_endpoint":         srv.URL + "/token",
				"jwks_uri":               srv.URL + "/keys",
			})
		case "/token":
			// Accepts the connection and never replies, until the test is over.
			select {
			case <-release:
			case <-time.After(10 * time.Second):
			}
		}
	}))
	defer srv.Close()
	defer close(release)

	p, err := NewOIDCProvider(context.Background(), OIDCConfig{
		Enabled: true, IssuerURL: srv.URL, ClientID: "cid", ClientSecret: "secret",
	})
	require.NoError(t, err)
	p.fetchTimeout = 200 * time.Millisecond

	start := time.Now()
	_, err = p.Exchange(context.Background(), "the-code", "the-verifier")
	require.Error(t, err)
	require.Less(t, time.Since(start), 3*time.Second,
		"the exchange waited on a provider that never answered")
}

func TestExchange_DefaultsToTheDocumentedTimeout(t *testing.T) {
	require.Equal(t, 15*time.Second, (&OIDCProvider{}).exchangeTimeout())
}
