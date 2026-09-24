package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/config"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/plugin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
	"github.com/publiciallc/go-help-desk/backend/internal/server"
)

// The manual re-check over HTTP, through the real router and the real auth
// chain (#168).
//
// A person looking at a quarantined sample and wondering whether the world has
// caught up since is exactly who should be able to find out. The three limits
// on it are what keep that from being a way to spend somebody else's API
// allowance, and each one is a test below.

const recheckDay = 24 * time.Hour

// A stale verdict is re-checked, the new answer is stored, and the control
// disarms for a week.
func TestRecheckReputation_StaffCanAskAgain(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	rig := newRecheckRig(t, h, repRespond200(vtDetectedRecheckBody))
	rig.setKey(t, "vt-key")

	tk, attID := rig.quarantined(t, "invoice.exe.zip")
	rig.seed(t, cleanRecheckVerdict(), 30*recheckDay)

	res, body := rig.recheck(t, h.apiKey, tk, attID)
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", body)
	require.Equal(t, int64(1), rig.hits.Load(), "the provider was asked exactly once")

	var att ticket.Attachment
	require.NoError(t, json.Unmarshal(body, &att))
	require.NotNil(t, att.Reputation)
	require.Equal(t, "detected", att.Reputation.State, "the answer changed, and the new one is returned")
	require.Equal(t, "VirusTotal", att.Reputation.Provider)
	require.NotNil(t, att.Reputation.FetchedAt)
	require.WithinDuration(t, time.Now(), *att.Reputation.FetchedAt, time.Minute,
		"a re-check that does not re-stamp the row re-arms the control immediately")

	// And the second attempt is refused — as final, not as too soon.
	//
	// Both refusals are true here: the first re-check was seconds ago, and the
	// verdict it stored is now a detection. Finality is the one to report,
	// because it is permanent and the other is not. "Try again in 7 days"
	// would be an invitation to come back for an answer that is never going
	// to change, and a reader who took it up would spend a lookup to be told
	// the same thing.
	res, body = rig.recheck(t, h.apiKey, tk, attID)
	require.Equal(t, http.StatusConflict, res.StatusCode, "%s", body)
	require.Contains(t, string(body), "detection_is_final")
	require.Equal(t, int64(1), rig.hits.Load(),
		"a refused re-check must not reach the provider")
}

// The floor holds whatever the setting says, including "never".
//
// An operator who turned automatic checking off to save quota did not mean
// "nobody may ever ask" — and a reader who asked yesterday may not ask again
// today just because the setting is generous.
func TestRecheckReputation_TheWeeklyFloorIsIndependentOfTheSetting(t *testing.T) {
	cases := []struct {
		name       string
		setting    string
		age        time.Duration
		wantStatus int
		wantHits   int64
	}{
		{"never, but a person asked", "never", 8 * recheckDay, http.StatusOK, 1},
		{"weekly, asked yesterday", "weekly", recheckDay, http.StatusTooManyRequests, 0},
		{"quarterly, asked a fortnight ago", "quarterly", 14 * recheckDay, http.StatusOK, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()

			rig := newRecheckRig(t, h, repRespond200(vtCleanRecheckBody))
			rig.setKey(t, "vt-key")
			require.NoError(t, h.adminSvc.SetRaw(context.Background(),
				admin.KeyAttachmentReputationRefresh, []byte(`"`+tc.setting+`"`)))

			tk, attID := rig.quarantined(t, "sample.exe.zip")
			rig.seed(t, cleanRecheckVerdict(), tc.age)

			res, body := rig.recheck(t, h.apiKey, tk, attID)
			require.Equal(t, tc.wantStatus, res.StatusCode, "%s", body)
			require.Equal(t, tc.wantHits, rig.hits.Load())
		})
	}
}

// The refusal says when it can next be asked, rather than only that it cannot.
func TestRecheckReputation_ARefusalSaysWhenItCanBeAskedAgain(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	rig := newRecheckRig(t, h, repRespond200(vtCleanRecheckBody))
	rig.setKey(t, "vt-key")

	tk, attID := rig.quarantined(t, "sample.exe.zip")
	rig.seed(t, cleanRecheckVerdict(), 2*recheckDay)

	res, body := rig.recheck(t, h.apiKey, tk, attID)
	require.Equal(t, http.StatusTooManyRequests, res.StatusCode, "%s", body)
	require.Contains(t, string(body), "checked_recently")

	secs, err := strconv.Atoi(res.Header.Get("Retry-After"))
	require.NoError(t, err, "a refusal that cannot be waited out is not actionable")
	// Checked two days ago, so five of the seven remain, give or take the
	// length of a test.
	require.InDelta(t, (5 * recheckDay).Seconds(), float64(secs), 120)
}

// A detection is not re-checked. There is nothing to learn from re-confirming
// malware, and the lookup spent on it is real.
func TestRecheckReputation_ADetectionIsNotReChecked(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	rig := newRecheckRig(t, h, repRespond200(vtCleanRecheckBody))
	rig.setKey(t, "vt-key")

	tk, attID := rig.quarantined(t, "invoice.exe.zip")
	rig.seed(t, reputation.Reputation{State: reputation.Detected, Detected: 62, Total: 81}, 400*recheckDay)

	res, body := rig.recheck(t, h.apiKey, tk, attID)
	require.Equal(t, http.StatusConflict, res.StatusCode, "%s", body)
	require.Contains(t, string(body), "detection_is_final")
	require.Equal(t, int64(0), rig.hits.Load())
}

// There is nothing to refresh on a file nobody has looked up. The ordinary
// lazy lookup covers that; a re-check that quietly became a first lookup would
// be a second way to spend the allowance, with none of the rules.
func TestRecheckReputation_AFileWithNoVerdictIsNotAWayToLookOneUp(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	rig := newRecheckRig(t, h, repRespond200(vtCleanRecheckBody))
	rig.setKey(t, "vt-key")

	tk, attID := rig.quarantined(t, "sample.exe.zip") // nothing seeded

	res, body := rig.recheck(t, h.apiKey, tk, attID)
	require.Equal(t, http.StatusConflict, res.StatusCode, "%s", body)
	require.Contains(t, string(body), "no_verdict")
	require.Equal(t, int64(0), rig.hits.Load())
}

// A customer cannot spend the operator's quota.
//
// The lazy lookup is staff-only for exactly this reason: a reporting user
// refreshing their own ticket all afternoon must not cost anybody a third
// party's allowance. A button changes nothing about that.
func TestRecheckReputation_ACustomerCannotSpendTheAllowance(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	rig := newRecheckRig(t, h, func(http.ResponseWriter, *http.Request) {
		t.Error("a customer's re-check reached the reputation provider")
	})
	rig.setKey(t, "vt-key")

	// Their own ticket, so nothing but the role is in the way.
	tk := rig.ticketFor(t, h.userID)
	attID := rig.attach(t, tk, "sample.exe.zip")
	rig.seed(t, cleanRecheckVerdict(), 30*recheckDay)

	res, body := rig.recheck(t, h.userKey, tk, attID)
	require.Equal(t, http.StatusForbidden, res.StatusCode, "%s", body)
	require.Equal(t, int64(0), rig.hits.Load())
}

// The daily budget still applies. A button is not a reason to risk getting an
// operator's API key banned.
//
// VirusTotal allows four lookups a minute, so four ordinary page renders spend
// the allowance and the fifth request — the manual one — has to be refused
// like any other.
func TestRecheckReputation_TheBudgetStillApplies(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	rig := newRecheckRig(t, h, repRespond200(vtCleanRecheckBody))
	rig.setKey(t, "vt-key")

	// Four quarantined files on one ticket, four distinct hashes, one render.
	spender := rig.ticketFor(t, h.staffID)
	for i := 0; i < 4; i++ {
		rig.attachHash(t, spender, "sample.exe.zip", recheckHashN(i))
	}
	rig.list(t, h.apiKey, spender)
	require.Equal(t, int64(4), rig.hits.Load(), "the minute's allowance is now spent")

	tk, attID := rig.quarantined(t, "invoice.exe.zip")
	rig.seed(t, cleanRecheckVerdict(), 30*recheckDay)

	res, body := rig.recheck(t, h.apiKey, tk, attID)
	require.Equal(t, http.StatusServiceUnavailable, res.StatusCode, "%s", body)
	require.Equal(t, int64(4), rig.hits.Load(), "the manual re-check must not jump the queue")
	require.NotEmpty(t, res.Header.Get("Retry-After"))
}

// An attachment id from another ticket is not found here, whatever it is.
func TestRecheckReputation_AnAttachmentFromAnotherTicketIsNotFound(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	rig := newRecheckRig(t, h, repRespond200(vtCleanRecheckBody))
	rig.setKey(t, "vt-key")

	_, attID := rig.quarantined(t, "invoice.exe.zip")
	other := rig.ticketFor(t, h.staffID)
	rig.seed(t, cleanRecheckVerdict(), 30*recheckDay)

	res, body := rig.recheck(t, h.apiKey, other, attID)
	require.Equal(t, http.StatusNotFound, res.StatusCode, "%s", body)
	require.Equal(t, int64(0), rig.hits.Load())
}

// ── rig ─────────────────────────────────────────────────────────────────────

const recheckHash = "3395856ce81f2b7382dee72602f798b642f14140db7a2a7ffb0d8b0c86ab2ad9"

func recheckHashN(i int) string {
	return strconv.Itoa(i) + strconv.Itoa(i) + recheckHash[2:]
}

const vtDetectedRecheckBody = `{"data":{"attributes":{
	"last_analysis_stats":{"malicious":62,"suspicious":0,"undetected":19,"harmless":0,
		"timeout":0,"confirmed-timeout":0,"failure":0,"type-unsupported":0},
	"last_analysis_date":1758565200,
	"popular_threat_classification":{"suggested_threat_label":"trojan.eicar/test"}}}}`

const vtCleanRecheckBody = `{"data":{"attributes":{
	"last_analysis_stats":{"malicious":0,"suspicious":0,"undetected":70,"harmless":0,
		"timeout":0,"confirmed-timeout":0,"failure":0,"type-unsupported":0},
	"last_analysis_date":1758565200}}}`

func repRespond200(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

func cleanRecheckVerdict() reputation.Reputation {
	at := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	return reputation.Reputation{State: reputation.Clean, Detected: 0, Total: 70, AnalysedAt: &at}
}

// recheckRig is a Server over the harness's collaborators with the reputation
// lookup wired, and with its verdict cache reachable from the test: every rule
// under test is about how old a stored verdict is, so the tests have to be
// able to write one that is a month old without waiting a month.
type recheckRig struct {
	srv   *server.Server
	store *memRecheckStore
	hits  *atomic.Int64
	h     *harness
}

func newRecheckRig(t *testing.T, h *harness, respond http.HandlerFunc) *recheckRig {
	t.Helper()

	var hits atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		respond(w, r)
	}))
	t.Cleanup(ts.Close)

	apiKeyLookup := authmw.APIKeyAuthFunc(func(ctx context.Context, hashed string) (auth.APIKey, user.User, error) {
		k, err := h.authStore.GetByHash(ctx, hashed)
		if err != nil {
			return auth.APIKey{}, user.User{}, err
		}
		u, err := h.userSvc.GetByID(ctx, k.UserID)
		if err != nil {
			return auth.APIKey{}, user.User{}, err
		}
		return k, u, nil
	})

	store := newMemRecheckStore()
	srv := server.New(
		&config.Config{
			SessionSecret: "test-session-secret-32-bytes-long!",
			JWTSecret:     "test-jwt-secret",
			BaseURL:       "http://localhost:8080",
		},
		h.sessions,
		h.userSvc,
		h.ticketSvc,
		h.categorySvc,
		h.groupSvc,
		nil, // tags: no route here touches them
		h.adminSvc,
		nil, // custom fields: likewise
		nil, // SLA: likewise
		plugin.NewRegistry(),
		apiKeyLookup,
		h.authStore,
		h.authStore,
		nil, // registration
		h.cannedResponses,
		server.WithReputationLookup(store, reputation.WithBaseURL(ts.URL)),
	)

	return &recheckRig{srv: srv, store: store, hits: &hits, h: h}
}

// setKey enables VirusTotal and gives it a key, which is what "the lookup is
// configured" means on this instance.
func (r *recheckRig) setKey(t *testing.T, key string) {
	t.Helper()
	require.NoError(t, r.h.adminSvc.SetRaw(context.Background(),
		admin.KeyAttachmentReputationVirusTotalKey, []byte(`"`+key+`"`)))
	require.NoError(t, r.h.adminSvc.SetRaw(context.Background(),
		admin.KeyAttachmentReputationVirusTotalEnabled, []byte(`true`)))
}

// seed writes a verdict for the rig's hash as though it had been fetched ago
// ago, which is what the database hands back.
func (r *recheckRig) seed(t *testing.T, rep reputation.Reputation, ago time.Duration) {
	t.Helper()
	rep.FetchedAt = time.Now().Add(-ago)
	require.NoError(t, r.store.put(recheckHash, reputation.ProviderVirusTotal, rep))
}

func (r *recheckRig) ticketFor(t *testing.T, reporter uuid.UUID) uuid.UUID {
	t.Helper()
	tk, err := r.h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "Quarantined sample", Description: "x", CategoryID: r.h.catID,
		ReporterUserID: &reporter,
	})
	require.NoError(t, err)
	return tk.ID
}

// attach writes a quarantined attachment straight to the store: the upload
// path needs a live ClamAV, and what is under test is the re-check.
func (r *recheckRig) attach(t *testing.T, ticketID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	return r.attachHash(t, ticketID, name, recheckHash)
}

func (r *recheckRig) attachHash(t *testing.T, ticketID uuid.UUID, name, sha string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	hash := sha
	virus := "Eicar-Test-Signature"
	require.NoError(t, r.h.ticketStore.CreateAttachment(context.Background(), ticket.Attachment{
		ID: id, TicketID: ticketID, Filename: name,
		MimeType: "application/zip", SizeBytes: 42, StoragePath: "/dev/null",
		SHA256: &hash, VirusName: &virus,
	}))
	return id
}

func (r *recheckRig) quarantined(t *testing.T, name string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	tk := r.ticketFor(t, r.h.staffID)
	return tk, r.attach(t, tk, name)
}

func (r *recheckRig) recheck(t *testing.T, apiKey string, ticketID, attID uuid.UUID) (*http.Response, []byte) {
	t.Helper()
	return r.recheckProvider(t, apiKey, ticketID, attID, "")
}

// recheckProvider names one provider's Check again control, or every enabled
// one when provider is empty.
func (r *recheckRig) recheckProvider(t *testing.T, apiKey string, ticketID, attID uuid.UUID, provider string) (*http.Response, []byte) {
	t.Helper()
	url := "/api/v1/tickets/" + ticketID.String() + "/attachments/" + attID.String() + "/reputation"
	if provider != "" {
		url += "?provider=" + provider
	}
	req := httptest.NewRequest(http.MethodPost, url, nil)
	req.Header.Set("Authorization", "ApiKey "+apiKey)
	rr := httptest.NewRecorder()
	r.srv.ServeHTTP(rr, req)

	res := rr.Result()
	defer res.Body.Close()
	return res, rr.Body.Bytes()
}

func (r *recheckRig) list(t *testing.T, apiKey string, ticketID uuid.UUID) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/tickets/"+ticketID.String()+"/attachments", nil)
	req.Header.Set("Authorization", "ApiKey "+apiKey)
	rr := httptest.NewRecorder()
	r.srv.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
}

// memRecheckStore is the verdict cache in memory, behaving like the column it
// stands in for: fetched_at is stamped by the store on every write, including
// one that changes nothing. A fake that let a write keep the old timestamp
// would make the weekly floor untestable here and would hide the bug it exists
// to prevent.
type memRecheckStore struct {
	mu   sync.Mutex
	rows map[string]reputation.Reputation
}

func newMemRecheckStore() *memRecheckStore {
	return &memRecheckStore{rows: map[string]reputation.Reputation{}}
}

func (m *memRecheckStore) Get(_ context.Context, sha256, provider string) (reputation.Reputation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rep, ok := m.rows[provider+":"+sha256]
	if !ok {
		return reputation.Reputation{}, reputation.ErrNotCached
	}
	return rep, nil
}

func (m *memRecheckStore) Put(_ context.Context, sha256, provider string, rep reputation.Reputation) error {
	rep.FetchedAt = time.Now()
	return m.put(sha256, provider, rep)
}

func (m *memRecheckStore) put(sha256, provider string, rep reputation.Reputation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[provider+":"+sha256] = rep
	return nil
}
