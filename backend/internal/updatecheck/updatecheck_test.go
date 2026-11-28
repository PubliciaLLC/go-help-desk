package updatecheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// memStore is the settings table. It outlives a Checker, so a test can build a
// second Checker over the same store and call that a restart.
type memStore struct {
	mu      sync.Mutex
	m       map[string][]byte
	listErr error // makes List fail, as a database outage would
	onSet   func(key string)
}

func (s *memStore) Get(_ context.Context, k string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[k]
	if !ok {
		return nil, fmt.Errorf("no such setting")
	}
	return v, nil
}
func (s *memStore) Set(ctx context.Context, k string, v []byte) error {
	// What pgx does: a cancelled context fails the write.
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[k] = v
	if s.onSet != nil {
		s.onSet(k)
	}
	return nil
}
func (s *memStore) List(context.Context) (map[string][]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := make(map[string][]byte, len(s.m))
	for k, v := range s.m {
		out[k] = v
	}
	return out, nil
}

// fakeGitHub counts every request it gets and records the last one. Nothing in
// these tests reaches the real API.
type fakeGitHub struct {
	*httptest.Server
	calls  atomic.Int32
	status int
	body   string
	delay  time.Duration
	last   atomic.Pointer[http.Request]

	onRequest func()
}

func newFakeGitHub(t *testing.T, status int, body string) *fakeGitHub {
	f := &fakeGitHub{status: status, body: body}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		f.last.Store(r)
		if f.onRequest != nil {
			f.onRequest()
		}
		time.Sleep(f.delay)
		w.WriteHeader(f.status)
		_, _ = w.Write([]byte(f.body))
	}))
	t.Cleanup(f.Close)
	return f
}

func release(tag string, pre, draft bool) string {
	return fmt.Sprintf(`{"tag_name":%q,"prerelease":%t,"draft":%t,"html_url":"https://evil.example/x"}`, tag, pre, draft)
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func setup(t *testing.T, gh *fakeGitHub, enabled *bool) (*Checker, *memStore, *clock) {
	t.Helper()
	store := &memStore{m: map[string][]byte{}}
	svc := admin.NewService(store)
	if enabled != nil {
		require.NoError(t, svc.SetBool(context.Background(), admin.KeyUpdateCheckEnabled, *enabled))
	}
	clk := &clock{t: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)}
	return newChecker(svc, store, gh, clk), store, clk
}

func newChecker(svc *admin.Service, store *memStore, gh *fakeGitHub, clk *clock) *Checker {
	return &Checker{
		Settings: svc, Client: gh.Client(), URL: gh.URL,
		Now: clk.now, Log: slog.New(slog.DiscardHandler),
	}
}

func on() *bool  { b := true; return &b }
func off() *bool { b := false; return &b }

func latest(t *testing.T, c *Checker) Record {
	t.Helper()
	rec, err := Load(context.Background(), c.Settings)
	require.NoError(t, err)
	return rec
}

func TestCheck_NewerVersionIsRecorded(t *testing.T) {
	gh := newFakeGitHub(t, 200, release("v1.4.0", false, false))
	c, _, clk := setup(t, gh, on())

	require.True(t, c.Check(context.Background()))
	rec := latest(t, c)
	require.Equal(t, "1.4.0", rec.Latest)
	require.Equal(t, clk.t, rec.CheckedAt)

	n, ok := Notice(context.Background(), c.Settings, "1.3.0")
	require.True(t, ok)
	require.Equal(t, "1.4.0", n.Latest)
	require.Equal(t, "1.3.0", n.Current)
	// The link is built from the tag, never taken from the response body.
	require.Equal(t, "https://github.com/PubliciaLLC/go-help-desk/releases/tag/v1.4.0", n.URL)
}

func TestCheck_SameAndOlderGiveNoNotice(t *testing.T) {
	for _, tag := range []string{"v1.3.0", "v1.2.0"} {
		gh := newFakeGitHub(t, 200, release(tag, false, false))
		c, _, _ := setup(t, gh, on())
		require.True(t, c.Check(context.Background()))
		_, ok := Notice(context.Background(), c.Settings, "1.3.0")
		require.False(t, ok, tag)
	}
}

func TestCheck_PrereleaseAndDraftAreIgnored(t *testing.T) {
	for name, body := range map[string]string{
		"flagged prerelease": release("v1.4.0", true, false),
		"flagged draft":      release("v1.4.0", false, true),
		"beta tag":           release("v1.4.0-beta.1", false, false),
	} {
		t.Run(name, func(t *testing.T) {
			gh := newFakeGitHub(t, 200, body)
			c, _, _ := setup(t, gh, on())
			require.True(t, c.Check(context.Background()))
			require.Empty(t, latest(t, c).Latest)
			_, ok := Notice(context.Background(), c.Settings, "1.3.0")
			require.False(t, ok)
		})
	}
}

func TestCheck_FailuresAreQuietAndConsumeTheWindow(t *testing.T) {
	cases := map[string]func(*fakeGitHub){
		"5xx":     func(g *fakeGitHub) { g.status = 503 },
		"403":     func(g *fakeGitHub) { g.status = 403 },
		"garbage": func(g *fakeGitHub) { g.body = "<html>" },
		"no tag":  func(g *fakeGitHub) { g.body = `{}` },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			gh := newFakeGitHub(t, 200, release("v1.4.0", false, false))
			mutate(gh)
			c, _, clk := setup(t, gh, on())
			require.True(t, c.Check(context.Background()))
			require.Empty(t, latest(t, c).Latest)
			// A failed check waits for the next window, like any other outcome.
			require.Equal(t, clk.t, latest(t, c).CheckedAt)
			clk.t = clk.t.Add(time.Hour)
			require.False(t, c.Check(context.Background()))
			require.EqualValues(t, 1, gh.calls.Load())
		})
	}
}

func TestCheck_FailureKeepsThePreviousAnswer(t *testing.T) {
	gh := newFakeGitHub(t, 200, release("v1.4.0", false, false))
	c, _, clk := setup(t, gh, on())
	require.True(t, c.Check(context.Background()))

	gh.status = 500
	clk.t = clk.t.Add(Interval)
	require.True(t, c.Check(context.Background()))
	require.Equal(t, "1.4.0", latest(t, c).Latest)
}

func TestCheck_TimeoutIsQuiet(t *testing.T) {
	gh := newFakeGitHub(t, 200, release("v1.4.0", false, false))
	gh.delay = 300 * time.Millisecond
	c, _, _ := setup(t, gh, on())
	c.Timeout = 50 * time.Millisecond

	start := time.Now()
	require.True(t, c.Check(context.Background()))
	require.Less(t, time.Since(start), 250*time.Millisecond)
	require.Empty(t, latest(t, c).Latest)
}

func TestCheck_AtMostOncePerInterval_AcrossRestarts(t *testing.T) {
	gh := newFakeGitHub(t, 200, release("v1.4.0", false, false))
	c, store, clk := setup(t, gh, on())

	require.True(t, c.Check(context.Background()))
	require.False(t, c.Check(context.Background()))

	// A restart is a new Checker over the same settings table.
	clk.t = clk.t.Add(6*24*time.Hour + 23*time.Hour)
	restarted := newChecker(admin.NewService(store), store, gh, clk)
	require.False(t, restarted.Check(context.Background()))
	require.EqualValues(t, 1, gh.calls.Load())

	clk.t = clk.t.Add(time.Hour)
	require.True(t, restarted.Check(context.Background()))
	require.EqualValues(t, 2, gh.calls.Load())
}

func TestCheck_OffMeansNoRequestEver(t *testing.T) {
	// nil: the default, the setting was never written. off(): written false.
	for name, enabled := range map[string]*bool{"default": nil, "explicit off": off()} {
		t.Run(name, func(t *testing.T) {
			gh := newFakeGitHub(t, 200, release("v1.4.0", false, false))
			c, _, clk := setup(t, gh, enabled)
			for range 3 {
				require.False(t, c.Check(context.Background()))
				clk.t = clk.t.Add(30 * 24 * time.Hour)
			}
			require.Zero(t, gh.calls.Load())
			require.True(t, latest(t, c).CheckedAt.IsZero())
		})
	}
}

// What leaves the server, exactly: a GET with a fixed User-Agent and the
// constant Accept header. No version (issue #170 asks for none: a version in
// the User-Agent turns "an instance exists here" into "an instance running
// 1.3.0 exists here"), no identifier, no query string, no credentials. The
// wording at setup, in the wizard and in Settings promises this.
func TestCheck_RequestIsExactlyTheFixedOne(t *testing.T) {
	gh := newFakeGitHub(t, 200, release("v1.4.0", false, false))
	c, _, _ := setup(t, gh, on())
	c.URL = gh.URL + "/repos/PubliciaLLC/go-help-desk/releases/latest"
	require.True(t, c.Check(context.Background()))

	r := gh.last.Load()
	require.Equal(t, http.MethodGet, r.Method)
	require.Equal(t, "/repos/PubliciaLLC/go-help-desk/releases/latest", r.URL.Path)
	require.Empty(t, r.URL.RawQuery)
	require.Equal(t, "go-help-desk", r.UserAgent())
	require.Equal(t, "application/vnd.github+json", r.Header.Get("Accept"))
	require.Empty(t, r.Header.Get("Authorization"))
	require.Empty(t, r.Header.Get("Cookie"))
	// Nothing but the three headers Go itself sends.
	got := map[string]bool{}
	for k := range r.Header {
		got[k] = true
	}
	require.Equal(t, map[string]bool{"User-Agent": true, "Accept": true, "Accept-Encoding": true}, got)
	require.Zero(t, r.ContentLength)
}

func TestCheck_TagIsCanonicalisedAndStrict(t *testing.T) {
	for name, tag := range map[string]string{
		"path escape in build metadata": "v99.0.0+/../../../../attacker/repo/releases",
		"query in build metadata":       "v99.0.0+?x=<script>",
		"fragment in build metadata":    "v99.0.0+#frag",
		"plain build metadata":          "v99.0.0+abc",
		"leading zeros":                 "v099.00.0",
		"prerelease":                    "v99.0.0-rc.1",
		"two parts":                     "v99.0",
		"not a version":                 "latest",
		"huge number":                   "v99999999999999999999.0.0",
	} {
		t.Run(name, func(t *testing.T) {
			gh := newFakeGitHub(t, 200, release(tag, false, false))
			c, _, _ := setup(t, gh, on())
			require.True(t, c.Check(context.Background()))
			require.Empty(t, latest(t, c).Latest, "a tag that is not exactly vMAJOR.MINOR.PATCH is not stored")
			_, ok := Notice(context.Background(), c.Settings, "1.3.0")
			require.False(t, ok)
		})
	}

	// Without the v, and the stored form is the numbers, not the tag's text.
	gh := newFakeGitHub(t, 200, release("1.4.0", false, false))
	c, _, _ := setup(t, gh, on())
	require.True(t, c.Check(context.Background()))
	require.Equal(t, "1.4.0", latest(t, c).Latest)
}

func TestNotice_DoesNotTrustWhatIsStored(t *testing.T) {
	// A settings row edited by hand, or written by an older build: the link is
	// built from the canonical numbers or not at all.
	for _, stored := range []string{"99.0.0+/../../x/y", "v99.0.0", "099.0.0", "99.0.0-rc.1", "<script>"} {
		gh := newFakeGitHub(t, 200, "")
		c, st, _ := setup(t, gh, on())
		raw, _ := json.Marshal(Record{CheckedAt: time.Now(), Latest: stored})
		st.m[admin.KeyUpdateCheckResult] = raw
		_, ok := Notice(context.Background(), c.Settings, "1.3.0")
		require.False(t, ok, stored)
	}
}

func TestCheck_WindowIsClaimedBeforeTheRequest(t *testing.T) {
	// A process killed mid-request (SIGTERM, a crash loop) must not ask again
	// on restart: by the time GitHub sees the request, checked_at is stored.
	var atRequest Record
	gh := newFakeGitHub(t, 200, release("v1.4.0", false, false))
	c, st, clk := setup(t, gh, on())
	gh.onRequest = func() { atRequest = latest(t, c) }
	require.True(t, c.Check(context.Background()))
	require.Equal(t, clk.t, atRequest.CheckedAt)
	require.Empty(t, atRequest.Latest, "the answer comes after the claim")
	_ = st
}

func TestCheck_ClaimSurvivesACancelledContext(t *testing.T) {
	gh := newFakeGitHub(t, 200, release("v1.4.0", false, false))
	c, _, clk := setup(t, gh, on())
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // SIGTERM arrived
	require.True(t, c.Check(ctx))
	require.Equal(t, clk.t, latest(t, c).CheckedAt, "the claim is written detached from the caller's context")
}

func TestCheck_UnreadableRecordSkipsTheTick(t *testing.T) {
	gh := newFakeGitHub(t, 200, release("v1.4.0", false, false))
	c, st, _ := setup(t, gh, on())
	st.listErr = errors.New("connection refused")
	require.False(t, c.Check(context.Background()), "a failed read is not an empty record")
	require.Zero(t, gh.calls.Load())

	st.listErr = nil
	require.True(t, c.Check(context.Background()))
}

func TestCheck_FailureIsLoggedAtInfoOnce(t *testing.T) {
	gh := newFakeGitHub(t, 500, "")
	c, _, _ := setup(t, gh, on())
	var buf bytes.Buffer
	c.Log = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	require.True(t, c.Check(context.Background()))

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 1)
	require.Contains(t, lines[0], `"level":"INFO"`)
}

func TestNotice_HiddenWhenTheCheckIsTurnedOff(t *testing.T) {
	gh := newFakeGitHub(t, 200, release("v1.4.0", false, false))
	c, _, _ := setup(t, gh, on())
	require.True(t, c.Check(context.Background()))
	require.NoError(t, c.Settings.SetBool(context.Background(), admin.KeyUpdateCheckEnabled, false))
	_, ok := Notice(context.Background(), c.Settings, "1.3.0")
	require.False(t, ok, "a stale answer must not outlive the operator's choice")
}

func TestReleaseURLConstant(t *testing.T) {
	require.Equal(t, "https://api.github.com/repos/PubliciaLLC/go-help-desk/releases/latest", ReleasesURL)
}
