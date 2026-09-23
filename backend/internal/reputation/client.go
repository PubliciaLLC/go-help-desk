package reputation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Sentinel reasons, checked with errors.Is the way antivirus.ErrNotConfigured
// is.
//
// Rate limiting and quota exhaustion both render as Unavailable — to a reader
// they mean the same thing, "we do not know" — but they are not the same event
// and the budget must not confuse them. A per-minute throttle clears in a
// minute; a spent daily quota does not clear until 00:00 UTC, and retrying
// against it all afternoon is a hot loop against someone else's service.
var (
	ErrRateLimited   = errors.New("reputation: rate limited")
	ErrQuotaExceeded = errors.New("reputation: quota exceeded")
	ErrNoAPIKey      = errors.New("reputation: no api key configured")
)

// Option configures a provider. The shape matches user.NewService(store,
// user.WithBcryptCost(...)), which is this codebase's existing answer to
// "a constructor needs a seam for tests".
type Option func(*client)

// WithBaseURL points a provider at a different origin. Production callers pass
// nothing and get the real service; a test passes an httptest server.
//
// Not wired to a setting, and it must not become one without going through
// internal/safehttp: a base URL an operator can type is a request forgery
// primitive, which is the whole reason that package exists.
func WithBaseURL(u string) Option {
	return func(c *client) {
		if u != "" {
			c.baseURL = u
		}
	}
}

// client is the HTTP half both providers share: one endpoint, one header, one
// decode. No third-party library — neither service has a Go client worth the
// dependency, and vt-go in particular has no context support and a default
// client with no timeout, which is disqualifying for a server.
type client struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

func newClient(apiKey, baseURL string, opts ...Option) client {
	c := client{
		apiKey:  apiKey,
		baseURL: baseURL,
		// An explicit timeout as well as the caller's context: the context
		// bounds the handler, this bounds a caller that forgot to set one.
		http: &http.Client{Timeout: 15 * time.Second},
	}
	for _, opt := range opts {
		opt(&c)
	}
	return c
}

// maxBody caps what we will read from a third party. A VirusTotal report with
// eighty engines in last_analysis_results is tens of kilobytes; a megabyte is
// far beyond any real answer and well short of anything that could hurt us.
const maxBody = 1 << 20

// get performs the single GET both providers make.
//
// The key travels in header, never in the URL: a key in a query string lands
// in every access log and proxy between here and there, which defeats storing
// it write-only. Nothing in the returned error carries the key either — the
// error is logged at the boundary.
func (c *client) get(ctx context.Context, path, header string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set(header, c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("requesting: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("reading response: %w", err)
	}
	return resp.StatusCode, body, nil
}

// unavailable is the only shape a failed lookup may return: the state the
// renderer branches on, plus the reason the operator needs. Counts stay zero
// and AnalysedAt stays nil, because a lookup that did not happen has no numbers
// and no date — inventing either is how "0 of 0 engines found anything" reaches
// a screen.
func unavailable(err error) (Reputation, error) {
	return Reputation{State: Unavailable}, err
}
