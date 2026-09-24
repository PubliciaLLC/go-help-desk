package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
)

// The lazy reputation lookup: cache, budget and provider, wired to a setting
// the operator can change while the server runs.
//
// White-box because the seam being tested is the wiring itself. The complaint
// that produced this work was that internal/reputation was complete, tested
// and reachable from nothing; a test that goes through the router proves the
// route, and a test that goes through this proves the thing that was missing.

const (
	repTestKey  = "vt-test-key-do-not-log-me"
	repTestHash = "275a021bbfb6489e54d471899f7db9d1663fc695ec2fe2a2c4538aabf651fd0f"
)

// A quarantined attachment gets a verdict, and the verdict reaches the wire
// with enough detail for staff to act on: the count, the total, the name and
// the date the provider last looked.
func TestAddReputation_PutsACompletedVerdictOnTheWire(t *testing.T) {
	cases := []struct {
		name       string
		respond    http.HandlerFunc
		wantState  string
		wantCounts bool
		wantName   string
	}{
		{
			name:       "engines flagged it",
			respond:    repRespond(http.StatusOK, vtDetectedBody),
			wantState:  "detected",
			wantCounts: true,
			wantName:   "trojan.eicar/test",
		},
		{
			name:       "no engine flagged it",
			respond:    repRespond(http.StatusOK, vtCleanBody),
			wantState:  "clean",
			wantCounts: true,
		},
		{
			// "VirusTotal has never seen this file" is a different sentence
			// from "VirusTotal found nothing wrong with it", and a reader must
			// be able to tell which one they are being told.
			name:      "the provider has never seen it",
			respond:   repRespond(http.StatusNotFound, `{"error":{"code":"NotFoundError"}}`),
			wantState: "unseen",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newRepRig(t, tc.respond)
			rig.setKey(t, repTestKey)

			att := quarantinedAttachment(repTestHash)
			rig.srv.addReputation(context.Background(), &att)

			require.Equal(t, int64(1), rig.hits.Load(), "exactly one lookup")
			require.NotNil(t, att.Reputation, "a completed lookup has something to show")
			require.Equal(t, tc.wantState, att.Reputation.State)
			require.Equal(t, tc.wantName, att.Reputation.ThreatName)

			if tc.wantCounts {
				require.NotNil(t, att.Reputation.Detected)
				require.NotNil(t, att.Reputation.Total)
				require.NotNil(t, att.Reputation.AnalysedAt,
					"a completed analysis says when it ran")
			} else {
				// 0 of 0 engines reads as "nothing found anything", which is
				// exactly the thing a file nobody analysed must not say.
				require.Nil(t, att.Reputation.Detected, "no analysis, no numbers")
				require.Nil(t, att.Reputation.Total, "no analysis, no numbers")
			}

			// And it survives the trip through JSON, which is the only part
			// the UI ever sees.
			body, err := json.Marshal(att)
			require.NoError(t, err)
			var out struct {
				Reputation *ticket.AttachmentReputation `json:"reputation"`
			}
			require.NoError(t, json.Unmarshal(body, &out))
			require.NotNil(t, out.Reputation)
			require.Equal(t, tc.wantState, out.Reputation.State)
		})
	}
}

// 62 of 81, as the spec puts it, and both halves have to be right: Detected
// counts malicious only, Total counts every engine that ran.
func TestAddReputation_CarriesTheEngineCounts(t *testing.T) {
	rig := newRepRig(t, repRespond(http.StatusOK, vtDetectedBody))
	rig.setKey(t, repTestKey)

	att := quarantinedAttachment(repTestHash)
	rig.srv.addReputation(context.Background(), &att)

	require.NotNil(t, att.Reputation)
	require.NotNil(t, att.Reputation.Detected)
	require.NotNil(t, att.Reputation.Total)
	require.Equal(t, 62, *att.Reputation.Detected)
	require.Equal(t, 81, *att.Reputation.Total)
}

// No key configured is the feature switched off. No request, no verdict, no
// error — and the page renders, because the link beside it needs no key.
func TestAddReputation_NoKeyMeansNoLookup(t *testing.T) {
	rig := newRepRig(t, repRespond(http.StatusOK, vtDetectedBody))

	for _, key := range []string{"", "   "} {
		rig.setKey(t, key)
		att := quarantinedAttachment(repTestHash)
		rig.srv.addReputation(context.Background(), &att)

		require.Equal(t, int64(0), rig.hits.Load(), "key %q must send no request", key)
		require.Nil(t, att.Reputation, "nobody looked, so there is nothing to say")
	}
}

// Nor when there is nowhere to cache a verdict: an instance wired without the
// store has the feature off in exactly the same way.
func TestAddReputation_NoStoreMeansNoLookup(t *testing.T) {
	rig := newRepRig(t, repRespond(http.StatusOK, vtDetectedBody))
	rig.setKey(t, repTestKey)
	rig.srv.repStore = nil

	att := quarantinedAttachment(repTestHash)
	rig.srv.addReputation(context.Background(), &att)

	require.Equal(t, int64(0), rig.hits.Load())
	require.Nil(t, att.Reputation)
}

// Only a quarantined attachment that has a hash is a candidate.
//
// Every attachment carries a hash now, and looking up all of them would spend
// a free tier of 500 a day on holiday-request PDFs.
func TestAddReputation_LooksUpOnlyQuarantinedAttachmentsWithAHash(t *testing.T) {
	virus := "Eicar-Test-Signature"
	hash := repTestHash
	empty := ""

	cases := []struct {
		name string
		att  ticket.Attachment
	}{
		{
			name: "a clean file, hashed",
			att:  ticket.Attachment{ID: uuid.New(), Filename: "report.pdf", SHA256: &hash},
		},
		{
			name: "quarantined before the hash column existed",
			att:  ticket.Attachment{ID: uuid.New(), Filename: "old.zip", VirusName: &virus},
		},
		{
			name: "quarantined with an empty hash",
			att:  ticket.Attachment{ID: uuid.New(), Filename: "odd.zip", VirusName: &virus, SHA256: &empty},
		},
		{
			name: "an attachment nobody inspected",
			att:  ticket.Attachment{ID: uuid.New(), Filename: "ancient.pdf"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newRepRig(t, repRespond(http.StatusOK, vtDetectedBody))
			rig.setKey(t, repTestKey)

			att := tc.att
			rig.srv.addReputation(context.Background(), &att)

			require.Equal(t, int64(0), rig.hits.Load())
			require.Nil(t, att.Reputation)
		})
	}
}

// The Budget is one instance for the life of the process, and this is the test
// that says so.
//
// VirusTotal allows four lookups a minute. Five distinct hashes inside one
// minute must therefore produce four requests and one "not checked": a Budget
// built per call — the obvious mistake, since the provider and the key beside
// it genuinely are built per call — hands every request a fresh allowance and
// the fifth sails through. The clock is pinned so the minute cannot roll over
// underneath the assertion.
func TestAddReputation_SpendsOneBudgetAcrossCalls(t *testing.T) {
	rig := newRepRig(t, repRespond(http.StatusOK, vtCleanBody))
	rig.setKey(t, repTestKey)

	fixed := time.Date(2026, 9, 23, 11, 30, 30, 0, time.UTC)
	rig.srv.repBudget.Now = func() time.Time { return fixed }

	hashes := []string{
		"11" + repTestHash[2:],
		"22" + repTestHash[2:],
		"33" + repTestHash[2:],
		"44" + repTestHash[2:],
		"55" + repTestHash[2:],
	}
	var got []string
	for _, h := range hashes {
		att := quarantinedAttachment(h)
		rig.srv.addReputation(context.Background(), &att)
		require.NotNil(t, att.Reputation)
		got = append(got, att.Reputation.State)
	}

	require.Equal(t, int64(4), rig.hits.Load(),
		"the fifth lookup in a minute must not reach VirusTotal")
	require.Equal(t, []string{"clean", "clean", "clean", "clean", "unavailable"}, got,
		"the fifth is a lookup that did not happen, and must not render as clean")
	require.Len(t, rig.store.all(), 4, "a refusal is not a verdict and is never cached")
}

// A stored verdict is never fetched again. A page render costs one lookup per
// hash, ever, which is what makes a lazy design safe on a free tier.
func TestAddReputation_DoesNotRefetchACachedVerdict(t *testing.T) {
	rig := newRepRig(t, repRespond(http.StatusOK, vtDetectedBody))
	rig.setKey(t, repTestKey)

	first := quarantinedAttachment(repTestHash)
	rig.srv.addReputation(context.Background(), &first)
	require.Equal(t, int64(1), rig.hits.Load())
	require.NotNil(t, first.Reputation)

	second := quarantinedAttachment(repTestHash)
	rig.srv.addReputation(context.Background(), &second)
	require.Equal(t, int64(1), rig.hits.Load(), "the second render reads the cache")
	require.NotNil(t, second.Reputation)
	require.Equal(t, first.Reputation.State, second.Reputation.State)
}

// A lookup that did not complete is never a clean result, and never a stored
// one either.
//
// This project has shipped a scanner that called files clean without scanning
// them twice. Every failure below has to land on "not checked yet", and
// nothing may be written to the cache — a cached failure would freeze into a
// permanent non-answer, since a stored verdict is never re-fetched.
func TestAddReputation_AFailedLookupIsNeverClean(t *testing.T) {
	cases := []struct {
		name    string
		respond http.HandlerFunc
	}{
		{"the provider is broken", repRespond(http.StatusInternalServerError, `{}`)},
		{"the key was rejected", repRespond(http.StatusUnauthorized, `{"error":{"code":"WrongCredentialsError"}}`)},
		{"rate limited", repRespond(http.StatusTooManyRequests, `{"error":{"code":"TooManyRequestsError"}}`)},
		{"the daily quota is gone", repRespond(http.StatusTooManyRequests, `{"error":{"code":"QuotaExceededError"}}`)},
		{"a 404 we do not understand", repRespond(http.StatusNotFound, `{"error":{"code":"SomethingElse"}}`)},
		{"a 200 carrying no analysis", repRespond(http.StatusOK, `{"data":{"attributes":{}}}`)},
		{"a body that is not JSON", repRespond(http.StatusOK, `<html>maintenance</html>`)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newRepRig(t, tc.respond)
			rig.setKey(t, repTestKey)

			att := quarantinedAttachment(repTestHash)
			rig.srv.addReputation(context.Background(), &att)

			// The verdict is "unavailable" and nothing else. It carries no
			// counts, no threat name and no feeds, because a lookup that did
			// not complete says nothing — least of all that the file is fine.
			//
			// It is reported rather than omitted so that an operator whose key
			// has been rejected can see it. That is the change per-provider
			// toggles brought: with four services, one failing silently behind
			// three that answered is a dead integration nobody notices.
			require.NotNil(t, att.Reputation)
			require.Equal(t, "unavailable", att.Reputation.State)
			require.Nil(t, att.Reputation.Detected, "no lookup, no numbers")
			require.Nil(t, att.Reputation.Total)
			require.Empty(t, att.Reputation.ThreatName)
			require.Empty(t, att.Reputation.KnownFeeds)
			require.Len(t, att.Reputation.Providers, 1)
			require.Equal(t, "unavailable", att.Reputation.Providers[0].State)
			require.False(t, att.Reputation.Providers[0].Recheckable,
				"there is nothing cached to re-check")
			require.Empty(t, rig.store.all(), "a transient failure must not be cached")
		})
	}
}

// A hung provider does not hold the handler open. The context is the inbound
// request's, so when the reader goes away the lookup goes with it — and what
// comes back is still a rendered attachment with no verdict.
func TestAddReputation_ACancelledRequestDoesNotHang(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	rig := newRepRig(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
	})
	rig.setKey(t, repTestKey)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	att := quarantinedAttachment(repTestHash)
	done := make(chan struct{})
	go func() {
		rig.srv.addReputation(ctx, &att)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a dead provider held the attachment list open")
	}
	require.NotNil(t, att.Reputation)
	require.Equal(t, "unavailable", att.Reputation.State,
		"a reader who went away leaves a lookup that did not happen, never a clean file")
	require.Empty(t, rig.store.all())
}

// The key is sent to the provider and to nowhere else.
//
// It is stored write-only precisely so that nobody can read it back; a log
// line or an error string carrying it undoes that, and both of those are
// written by the code under test rather than by internal/reputation.
func TestAddReputation_NeverLogsTheAPIKey(t *testing.T) {
	var gotHeader string
	rig := newRepRig(t, func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("x-apikey")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"WrongCredentialsError"}}`))
	})
	rig.setKey(t, repTestKey)

	var logged bytes.Buffer
	restore := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(restore) })

	att := quarantinedAttachment(repTestHash)
	rig.srv.addReputation(context.Background(), &att)

	require.Equal(t, repTestKey, gotHeader, "the key travels in the header, or nothing works")
	require.NotContains(t, logged.String(), repTestKey,
		"a write-only setting that appears in the log is not write-only")
	require.NotEmpty(t, logged.String(),
		"the operator still has to be told the lookup failed")
}

// A spent allowance is not an incident, and must not log like one.
//
// The budget refuses every lookup for the rest of the day once VirusTotal's
// 500 are gone. At WARN that is one line per quarantined attachment per page
// view until midnight UTC, which buries the lines an operator does need — the
// rejected key, the provider that is down — in a log nobody then reads. The
// comment above scanUpload is about that exact failure.
func TestAddReputation_DoesNotWarnOnceThePageAllowanceIsSpent(t *testing.T) {
	rig := newRepRig(t, repRespond(http.StatusOK, vtCleanBody))
	rig.setKey(t, repTestKey)

	fixed := time.Date(2026, 9, 23, 11, 30, 30, 0, time.UTC)
	rig.srv.repBudget.Now = func() time.Time { return fixed }

	// Spend the minute's four.
	for i, h := range []string{"11", "22", "33", "44"} {
		att := quarantinedAttachment(h + repTestHash[2:])
		rig.srv.addReputation(context.Background(), &att)
		require.NotNil(t, att.Reputation, "lookup %d should have been allowed", i)
	}

	var warned bytes.Buffer
	restore := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&warned, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(restore) })

	att := quarantinedAttachment("55" + repTestHash[2:])
	rig.srv.addReputation(context.Background(), &att)

	require.NotNil(t, att.Reputation)
	require.Equal(t, "unavailable", att.Reputation.State)
	require.Empty(t, warned.String(),
		"a refusal that clears on its own is not worth a warning on every render")
}

// Enabling a second provider makes the next render ask both, with no restart.
//
// The toggles are read per call for this reason; only the Budget is held. A
// set of providers captured at startup is the defect this codebase already
// documents for the ticket prefix and the scanner address.
//
// And it is the point of per-provider toggles rather than one selected
// service: VirusTotal counts engines and MetaDefender is a different corpus,
// so a reader gets both answers rather than whichever one was picked last.
func TestAddReputation_FollowsTheEnabledProvidersWithoutARestart(t *testing.T) {
	var paths []string
	var mu sync.Mutex
	rig := newRepRig(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
		// Shaped for whichever provider asked; neither answer matters here.
		_, _ = w.Write([]byte(`{"error":{"code":"NotFoundError","messages":["Not Found"]}}`))
	})
	rig.setKey(t, repTestKey)

	att := quarantinedAttachment(repTestHash)
	rig.srv.addReputation(context.Background(), &att)

	rig.enable(t, reputation.ProviderVirusTotal, reputation.ProviderMetaDefender)
	other := quarantinedAttachment("ab" + repTestHash[2:])
	rig.srv.addReputation(context.Background(), &other)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{
		"/api/v3/files/" + repTestHash,
		"/api/v3/files/ab" + repTestHash[2:],
		"/v4/hash/ab" + repTestHash[2:],
	}, paths, "the toggles changed and the next render must follow them")
}

// A verdict belongs to the provider that gave it. Enabling a second one must
// not hand the first one's answer back under the new name, and the cache key
// is what makes that impossible.
func TestAddReputation_DoesNotServeOneProvidersVerdictAsAnothers(t *testing.T) {
	rig := newRepRig(t, repRespond(http.StatusOK, vtDetectedBody))
	rig.setKey(t, repTestKey)

	att := quarantinedAttachment(repTestHash)
	rig.srv.addReputation(context.Background(), &att)
	require.NotNil(t, att.Reputation)
	require.Equal(t, int64(1), rig.hits.Load())

	rig.enable(t, reputation.ProviderMetaDefender)
	other := quarantinedAttachment(repTestHash)
	rig.srv.addReputation(context.Background(), &other)

	require.Equal(t, int64(2), rig.hits.Load(),
		"the other provider was never asked, so its verdict cannot be cached")
}

// New always builds a Budget, wired or not, so nothing downstream has to check
// for nil — and the option is what turns the lookup on.
func TestNew_AlwaysHoldsOneBudget(t *testing.T) {
	store := newMemRepStore()
	srv := newBareServer(t)
	require.NotNil(t, srv.repBudget)
	require.Nil(t, srv.repStore, "no option means the lookup is off")

	wired := newBareServer(t, WithReputationLookup(store, reputation.WithBaseURL("http://example.invalid")))
	require.NotNil(t, wired.repBudget)
	require.Equal(t, reputation.Store(store), wired.repStore)
	require.Len(t, wired.repOpts, 1)
}

// ── rig ─────────────────────────────────────────────────────────────────────

// vtDetectedBody is a VirusTotal v3 file report: 62 malicious of 81 engines
// that ran, which is the example the build spec uses.
const vtDetectedBody = `{"data":{"attributes":{
	"last_analysis_stats":{"malicious":62,"suspicious":2,"undetected":15,"harmless":0,
		"timeout":1,"confirmed-timeout":1,"failure":0,"type-unsupported":0},
	"last_analysis_date":1758565200,
	"popular_threat_classification":{"suggested_threat_label":"trojan.eicar/test"}}}}`

const vtCleanBody = `{"data":{"attributes":{
	"last_analysis_stats":{"malicious":0,"suspicious":0,"undetected":70,"harmless":0,
		"timeout":0,"confirmed-timeout":0,"failure":0,"type-unsupported":0},
	"last_analysis_date":1758565200}}}`

func repRespond(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func quarantinedAttachment(sha string) ticket.Attachment {
	virus := "Eicar-Test-Signature"
	hash := sha
	return ticket.Attachment{
		ID:        uuid.New(),
		TicketID:  uuid.New(),
		Filename:  "invoice.exe.zip",
		MimeType:  "application/zip",
		SHA256:    &hash,
		VirusName: &virus,
	}
}

type repRig struct {
	srv   *Server
	admin *admin.Service
	store *memRepStore
	hits  *atomic.Int64
}

func newRepRig(t *testing.T, respond http.HandlerFunc) *repRig {
	t.Helper()

	var hits atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		respond(w, r)
	}))
	t.Cleanup(ts.Close)

	adminSvc := admin.NewService(newMemSettings())
	store := newMemRepStore()
	srv := newBareServer(t, WithReputationLookup(store, reputation.WithBaseURL(ts.URL)))
	srv.adminSvc = adminSvc

	rig := &repRig{srv: srv, admin: adminSvc, store: store, hits: &hits}
	// VirusTotal alone, which is what the single-provider default was. Tests
	// that want a different set say so with enable or setProvider.
	rig.enable(t, reputation.ProviderVirusTotal)
	return rig
}

// setKey gives every commercial provider the same key, without touching which
// of them are enabled.
//
// All three rather than one, because these tests care about "this instance has
// a usable key" and not about which box it was pasted into; the toggles are
// what decide who is asked, and setProvider is what moves those.
func (r *repRig) setKey(t *testing.T, key string) {
	t.Helper()
	raw, err := json.Marshal(key)
	require.NoError(t, err)
	for _, k := range []string{
		admin.KeyAttachmentReputationVirusTotalKey,
		admin.KeyAttachmentReputationMetaDefenderKey,
		admin.KeyAttachmentReputationPolySwarmKey,
	} {
		require.NoError(t, r.admin.SetRaw(context.Background(), k, raw))
	}
}

// enable turns the named providers on and every other one off.
func (r *repRig) enable(t *testing.T, providers ...string) {
	t.Helper()
	want := map[string]bool{}
	for _, p := range providers {
		want[p] = true
	}
	for _, p := range admin.ReputationProviders() {
		enabledKey, _, ok := admin.ReputationSettingKeys(p)
		require.True(t, ok)
		raw := []byte("false")
		if want[p] {
			raw = []byte("true")
		}
		require.NoError(t, r.admin.SetRaw(context.Background(), enabledKey, raw))
	}
}

// newBareServer builds a Server with nothing but the reputation wiring. Every
// other collaborator is nil, which is safe because none of the routes are
// called: only the helpers under test are.
func newBareServer(t *testing.T, opts ...Option) *Server {
	t.Helper()
	s := &Server{repBudget: reputation.NewBudget()}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// memSettings is admin.Store in a map.
type memSettings struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newMemSettings() *memSettings { return &memSettings{data: map[string][]byte{}} }

func (m *memSettings) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return v, nil
}

func (m *memSettings) Set(_ context.Context, key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = value
	return nil
}

func (m *memSettings) List(_ context.Context) (map[string][]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string][]byte, len(m.data))
	for k, v := range m.data {
		out[k] = v
	}
	return out, nil
}

// memRepStore is reputation.Store in a map. The database adapter is tested
// against a real Postgres in internal/database; what matters here is which
// calls the wiring makes.
type memRepStore struct {
	mu   sync.Mutex
	data map[string]reputation.Reputation
}

func newMemRepStore() *memRepStore {
	return &memRepStore{data: map[string]reputation.Reputation{}}
}

func (m *memRepStore) Get(_ context.Context, sha256, provider string) (reputation.Reputation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rep, ok := m.data[provider+":"+sha256]
	if !ok {
		return reputation.Reputation{}, reputation.ErrNotCached
	}
	return rep, nil
}

func (m *memRepStore) Put(_ context.Context, sha256, provider string, rep reputation.Reputation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[provider+":"+sha256] = rep
	return nil
}

func (m *memRepStore) all() map[string]reputation.Reputation {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]reputation.Reputation, len(m.data))
	for k, v := range m.data {
		out[k] = v
	}
	return out
}
