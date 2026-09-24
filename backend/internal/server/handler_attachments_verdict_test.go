package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

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

// The verdict over HTTP, through the real router and the real auth chain.
//
// internal/server/reputation_test.go pins what the lookup does; these pin that
// a staff member listing a ticket's attachments is what triggers it, which is
// the connection an adversarial review found missing — the package was
// complete, tested, and constructed by nothing.

// A staff member opening a ticket with a quarantined attachment gets the
// verdict, and it is detailed enough for the sentence the UI has to write.
func TestListAttachments_AStaffRenderLooksUpAQuarantinedFile(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	rig := newVerdictRig(t, h, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"attributes":{
			"last_analysis_stats":{"malicious":62,"suspicious":0,"undetected":19,"harmless":0,
				"timeout":0,"confirmed-timeout":0,"failure":0,"type-unsupported":0},
			"last_analysis_date":1758565200,
			"popular_threat_classification":{"suggested_threat_label":"trojan.eicar/test"}}}}`))
	})
	rig.setKey(t, "vt-key")

	tk := rig.quarantinedTicket(t, "invoice.exe.zip")

	got := rig.list(t, h.apiKey, tk)
	require.Len(t, got, 1)
	require.NotNil(t, got[0].Reputation, "a staff render looks the hash up")
	require.Equal(t, "detected", got[0].Reputation.State)
	require.NotNil(t, got[0].Reputation.Detected)
	require.NotNil(t, got[0].Reputation.Total)
	require.Equal(t, 62, *got[0].Reputation.Detected)
	require.Equal(t, 81, *got[0].Reputation.Total)
	require.Equal(t, "trojan.eicar/test", got[0].Reputation.ThreatName)
	require.NotNil(t, got[0].Reputation.AnalysedAt)
	// The link is unchanged and still beside it.
	require.NotNil(t, got[0].ReputationURL)
	require.Equal(t, int64(1), rig.hits.Load())

	// A second render reads the cache rather than the provider.
	again := rig.list(t, h.apiKey, tk)
	require.NotNil(t, again[0].Reputation)
	require.Equal(t, int64(1), rig.hits.Load())
}

// With no key configured the feature is simply off: the page renders, the
// attachment is there, the link still works, and no verdict is claimed.
//
// This is the state every existing instance upgrades into, so it is the one
// that must not break.
func TestListAttachments_NoKeyStillRendersTheAttachment(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	rig := newVerdictRig(t, h, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("a lookup was attempted with no key configured")
	})

	tk := rig.quarantinedTicket(t, "sample.exe.zip")

	got := rig.list(t, h.apiKey, tk)
	require.Len(t, got, 1)
	require.Nil(t, got[0].Reputation, "nobody looked, so nothing is claimed")
	require.NotNil(t, got[0].ReputationURL, "a link needs no key")
	require.Equal(t, int64(0), rig.hits.Load())
}

// A provider that is down, slow or out of quota must not take a ticket page
// with it. 200, the attachments, and no verdict.
func TestListAttachments_AProviderFailureDoesNotFailThePage(t *testing.T) {
	cases := []struct {
		name    string
		respond http.HandlerFunc
	}{
		{"down", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}},
		{"out of quota", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":"QuotaExceededError"}}`))
		}},
		{"answering with nonsense", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`Currently Under Maintenance`))
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()

			rig := newVerdictRig(t, h, tc.respond)
			rig.setKey(t, "vt-key")
			tk := rig.quarantinedTicket(t, "sample.exe.zip")

			got := rig.list(t, h.apiKey, tk)
			require.Len(t, got, 1, "the attachment is still listed")
			require.NotNil(t, got[0].Reputation)
			require.Equal(t, "unavailable", got[0].Reputation.State,
				"a failed lookup renders as not checked, never as clean")
			require.Nil(t, got[0].Reputation.Detected, "no lookup, no numbers")
			require.Nil(t, got[0].Reputation.Total)
			require.NotNil(t, got[0].ReputationURL)
		})
	}
}

// The reporting customer sees their own quarantined attachment and its link,
// and their refresh does not spend the operator's daily allowance on a
// third-party service.
func TestListAttachments_ACustomerRenderSpendsNoLookup(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	rig := newVerdictRig(t, h, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("a customer's page render reached the reputation provider")
	})
	rig.setKey(t, "vt-key")

	tk := rig.ticketFor(t, h.userID)
	rig.attach(t, tk, "sample.exe.zip")

	got := rig.list(t, h.userKey, tk)
	require.Len(t, got, 1)
	require.Nil(t, got[0].Reputation)
	require.NotNil(t, got[0].ReputationURL)
	require.Equal(t, int64(0), rig.hits.Load())
}

// An ordinary attachment is not a candidate however it is rendered: the
// allowance is for the files somebody has a reason to ask about.
func TestListAttachments_AnOrdinaryFileIsNotLookedUp(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	rig := newVerdictRig(t, h, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("a clean attachment was looked up")
	})
	rig.setKey(t, "vt-key")

	tk := rig.ticketFor(t, h.staffID)
	sha := "aa" + repVerdictHash[2:]
	require.NoError(t, h.ticketStore.CreateAttachment(ctx, ticket.Attachment{
		ID: uuid.New(), TicketID: tk, Filename: "holiday.pdf",
		MimeType: "application/pdf", SizeBytes: 12, StoragePath: "/dev/null",
		SHA256: &sha,
	}))

	got := rig.list(t, h.apiKey, tk)
	require.Len(t, got, 1)
	require.Nil(t, got[0].Reputation)
	require.Equal(t, int64(0), rig.hits.Load())
}

// ── rig ─────────────────────────────────────────────────────────────────────

const repVerdictHash = "275a021bbfb6489e54d471899f7db9d1663fc695ec2fe2a2c4538aabf651fd0f"

// verdictRig is a second Server over the harness's collaborators, wired with
// the reputation lookup.
//
// The shared harness predates WithReputationLookup and cannot be edited, so
// the option is applied here instead. Everything the route touches —
// tickets, users, API keys, settings — is the harness's own, inside the
// harness transaction, so this is the same instance seen through a server that
// has the feature switched on.
type verdictRig struct {
	srv   *server.Server
	admin *admin.Service
	hits  *atomic.Int64
	h     *harness
}

func newVerdictRig(t *testing.T, h *harness, respond http.HandlerFunc) *verdictRig {
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
		server.WithReputationLookup(newMemVerdictStore(), reputation.WithBaseURL(ts.URL)),
	)

	return &verdictRig{srv: srv, admin: h.adminSvc, hits: &hits, h: h}
}

// setKey enables VirusTotal and gives it a key, which is what "the lookup is
// configured" means on this instance.
func (r *verdictRig) setKey(t *testing.T, key string) {
	t.Helper()
	require.NoError(t, r.admin.SetRaw(context.Background(),
		admin.KeyAttachmentReputationVirusTotalKey, []byte(`"`+key+`"`)))
	require.NoError(t, r.admin.SetRaw(context.Background(),
		admin.KeyAttachmentReputationVirusTotalEnabled, []byte(`true`)))
}

func (r *verdictRig) ticketFor(t *testing.T, reporter uuid.UUID) uuid.UUID {
	t.Helper()
	tk, err := r.h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "Quarantined sample", Description: "x", CategoryID: r.h.catID,
		ReporterUserID: &reporter,
	})
	require.NoError(t, err)
	return tk.ID
}

// attach writes a quarantined attachment straight to the store: the upload
// path needs a live ClamAV, and what is under test is the read.
func (r *verdictRig) attach(t *testing.T, ticketID uuid.UUID, name string) {
	t.Helper()
	sha := repVerdictHash
	virus := "Eicar-Test-Signature"
	require.NoError(t, r.h.ticketStore.CreateAttachment(context.Background(), ticket.Attachment{
		ID: uuid.New(), TicketID: ticketID, Filename: name,
		MimeType: "application/zip", SizeBytes: 42, StoragePath: "/dev/null",
		SHA256: &sha, VirusName: &virus,
	}))
}

func (r *verdictRig) quarantinedTicket(t *testing.T, name string) uuid.UUID {
	t.Helper()
	tk := r.ticketFor(t, r.h.staffID)
	r.attach(t, tk, name)
	return tk
}

func (r *verdictRig) list(t *testing.T, apiKey string, ticketID uuid.UUID) []ticket.Attachment {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/tickets/"+ticketID.String()+"/attachments", nil)
	req.Header.Set("Authorization", "ApiKey "+apiKey)
	rr := httptest.NewRecorder()
	r.srv.ServeHTTP(rr, req)

	res := rr.Result()
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode,
		"a reputation lookup must never fail the attachment list")

	var out []ticket.Attachment
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	return out
}

// memVerdictStore is the verdict cache in memory. The Postgres adapter has its
// own tests; this suite is about the route.
type memVerdictStore struct {
	mu   map[string]reputation.Reputation
	lock chan struct{}
}

func newMemVerdictStore() *memVerdictStore {
	return &memVerdictStore{mu: map[string]reputation.Reputation{}, lock: make(chan struct{}, 1)}
}

func (m *memVerdictStore) Get(_ context.Context, sha256, provider string) (reputation.Reputation, error) {
	m.lock <- struct{}{}
	defer func() { <-m.lock }()
	rep, ok := m.mu[provider+":"+sha256]
	if !ok {
		return reputation.Reputation{}, reputation.ErrNotCached
	}
	return rep, nil
}

func (m *memVerdictStore) Put(_ context.Context, sha256, provider string, rep reputation.Reputation) error {
	m.lock <- struct{}{}
	defer func() { <-m.lock }()
	m.mu[provider+":"+sha256] = rep
	return nil
}
