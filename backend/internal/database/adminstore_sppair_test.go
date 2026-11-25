package database_test

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/adminstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// The service-provider pair is written as one: both rows and the audit entry
// commit together, or none does, and a first-save generation yields to a pair
// somebody stored first. Real Postgres, on connections of their own (no
// harness transaction), because the property under test is what the database
// does when two transactions arrive together.

type spPairFx struct {
	ctx   context.Context
	store *adminstore.Store
	svc   *admin.Service
	au    *auditstore.Store
}

func newSPPairFx(t *testing.T) *spPairFx {
	t.Helper()
	db, cleanup := testutil.NewIsolatedDB(t)
	t.Cleanup(cleanup)
	store := adminstore.NewWithDB(db.Queries, db.SQL)
	return &spPairFx{ctx: context.Background(), store: store, svc: admin.NewService(store), au: auditstore.New(db.Queries)}
}

func (f *spPairFx) pair(t *testing.T) (cert, key string) {
	t.Helper()
	_, cert, key = f.svc.GetSAMLConfig(f.ctx)
	return cert, key
}

func (f *spPairFx) entry(action string) audit.Entry {
	return audit.Entry{
		ID: uuid.New(), EntityType: "setting", EntityID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Action: action, After: map[string]any{"source": "generated"},
	}
}

func (f *spPairFx) audited(t *testing.T) int {
	t.Helper()
	es, err := f.au.ListByEntity(f.ctx, "setting", uuid.MustParse("11111111-1111-1111-1111-111111111111"), 100, 0)
	require.NoError(t, err)
	return len(es)
}

func TestSetSAMLSPPair(t *testing.T) {
	cases := []struct {
		name string
		// stored is what the settings hold before: cert, key.
		storedCert, storedKey string
		ifAbsent              bool
		wantWritten           bool
		wantCert, wantKey     string
		wantAudited           int
	}{
		{name: "nothing stored, first save", ifAbsent: true, wantWritten: true, wantCert: "new-cert", wantKey: "new-key", wantAudited: 1},
		{name: "a pair is stored, first save yields", storedCert: "old-cert", storedKey: "old-key", ifAbsent: true, wantCert: "old-cert", wantKey: "old-key"},
		{name: "only a certificate is stored, first save yields", storedCert: "old-cert", ifAbsent: true, wantCert: "old-cert"},
		{name: "only a key is stored, first save yields", storedKey: "old-key", ifAbsent: true, wantKey: "old-key"},
		{name: "regenerate over a stored pair", storedCert: "old-cert", storedKey: "old-key", wantWritten: true, wantCert: "new-cert", wantKey: "new-key", wantAudited: 1},
		{name: "regenerate with nothing stored", wantWritten: true, wantCert: "new-cert", wantKey: "new-key", wantAudited: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSPPairFx(t)
			require.NoError(t, f.svc.SetString(f.ctx, admin.KeySAMLCertPEM, tc.storedCert))
			require.NoError(t, f.svc.SetString(f.ctx, admin.KeySAMLKeyPEM, tc.storedKey))

			written, err := f.svc.SetSAMLSPPair(f.ctx, "new-cert", "new-key", tc.ifAbsent, f.entry("saml_cert_generated"))
			require.NoError(t, err)
			require.Equal(t, tc.wantWritten, written)

			cert, key := f.pair(t)
			require.Equal(t, tc.wantCert, cert)
			require.Equal(t, tc.wantKey, key)
			require.Equal(t, tc.wantAudited, f.audited(t), "an entry exists exactly when the pair was written")
		})
	}
}

// The entry is part of the change: one that cannot be written takes the pair
// with it, and the stored pair stays as it was.
func TestSetSAMLSPPair_ARefusedAuditEntryLeavesThePairAlone(t *testing.T) {
	f := newSPPairFx(t)
	require.NoError(t, f.svc.SetString(f.ctx, admin.KeySAMLCertPEM, "old-cert"))
	require.NoError(t, f.svc.SetString(f.ctx, admin.KeySAMLKeyPEM, "old-key"))

	first := f.entry("saml_cert_regenerated")
	_, err := f.svc.SetSAMLSPPair(f.ctx, "c1", "k1", false, first)
	require.NoError(t, err)

	// The same entry id again: the audit insert violates its primary key.
	_, err = f.svc.SetSAMLSPPair(f.ctx, "c2", "k2", false, first)
	require.Error(t, err)

	cert, key := f.pair(t)
	require.Equal(t, "c1", cert)
	require.Equal(t, "k1", key)
}

// The property the conditional write exists for: several servers saving at
// once, none holding another's lock, and exactly one pair is written.
func TestSetSAMLSPPair_IfAbsentIsAtomic(t *testing.T) {
	f := newSPPairFx(t)
	const racers = 12

	var wg sync.WaitGroup
	start := make(chan struct{})
	written := make([]bool, racers)
	errs := make([]error, racers)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			id := uuid.NewString()
			cert, key := "cert-"+id, "key-"+id
			written[i], errs[i] = f.svc.SetSAMLSPPair(f.ctx, cert, key, true, f.entry("saml_cert_generated"))
		}()
	}
	close(start)
	wg.Wait()

	winners := 0
	for i := range written {
		require.NoError(t, errs[i])
		if written[i] {
			winners++
		}
	}
	require.Equal(t, 1, winners, "exactly one save generates")
	require.Equal(t, 1, f.audited(t))

	// One winner's pair, not the certificate of one and the key of another.
	cert, key := f.pair(t)
	require.Equal(t, cert[len("cert-"):], key[len("key-"):])
}
