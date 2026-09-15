package sessionstore_test

import (
	"context"
	"database/sql"
	"encoding/gob"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/securecookie"
	"github.com/gorilla/sessions"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/sessionstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// Against a real Postgres, not a fake: the store IS its SQL, and the
// behaviours that matter here — an expired row not authenticating, a deleted
// row not authenticating — are properties of the query, which a fake would
// simply restate.

const cookieName = "ghd_session"

// SessionData travels as gob, and gob needs the concrete type registered
// before it can encode an interface value. cmd/server and the server test
// harness both do this explicitly; the store does not do it in an init(),
// because this project removed a gob.Register init() deliberately (no init
// with side effects). So each caller registers, and so does this test.
func TestMain(m *testing.M) {
	gob.Register(auth.SessionData{})
	os.Exit(m.Run())
}

var (
	hashKey  = securecookie.GenerateRandomKey(32)
	blockKey = securecookie.GenerateRandomKey(32)
)

func newStore(t *testing.T) (*sessionstore.Store, *dbgen.Queries, func()) {
	t.Helper()
	st, q, _, cleanup := newStoreWithLifetime(t, 3600)
	return st, q, cleanup
}

// newStoreWithLifetime is newStore with a chosen session lifetime, and it hands
// back the transaction so a test can hold one open on purpose.
//
// Everything runs inside a transaction that is rolled back. The expiry tests
// used to run against the database directly, because expiry was compared with
// now(), which is frozen at the transaction's start and so would never advance
// past a session's lifetime. Expiry is compared with clock_timestamp() now,
// which does advance, so that reason is gone — and with it the committed rows
// those tests left behind, which other packages could see mid-run.
func newStoreWithLifetime(t *testing.T, maxAge int) (*sessionstore.Store, *dbgen.Queries, *sql.Tx, func()) {
	t.Helper()
	db, closeDB := testutil.NewDB(t)
	tx, err := db.SQL.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	q := dbgen.New(tx)
	st := sessionstore.New(q, hashKey, blockKey, &sessions.Options{
		Path: "/", MaxAge: maxAge, HttpOnly: true,
	})
	return st, q, tx, func() { _ = tx.Rollback(); closeDB() }
}

// seedUser creates a real user: sessions.user_id is a foreign key, so a
// made-up uuid cannot be saved.
func seedUser(t *testing.T, q *dbgen.Queries) uuid.UUID {
	t.Helper()
	u := user.User{
		ID:          uuid.New(),
		Email:       "session-" + uuid.NewString() + "@test.local",
		DisplayName: "Session Test",
		Role:        user.RoleStaff,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	require.NoError(t, userstore.New(q).Create(context.Background(), u))
	return u.ID
}

// saveNew writes a session for a user and returns the Set-Cookie value.
func saveNew(t *testing.T, st *sessionstore.Store, userID uuid.UUID) string {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	sess, err := st.New(r, cookieName)
	require.NoError(t, err)
	sess.Values[auth.SessionDataKey] = auth.SessionData{UserID: userID, Role: user.RoleStaff, MFAPassed: true}
	require.NoError(t, st.Save(r, w, sess))

	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	return cookies[0].Value
}

// load replays a cookie value the way a later request would.
func load(t *testing.T, st *sessionstore.Store, value string) *sessions.Session {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: cookieName, Value: value})
	sess, err := st.New(r, cookieName)
	require.NoError(t, err)
	return sess
}

func TestStore_RoundTrip(t *testing.T) {
	st, q, cleanup := newStore(t)
	defer cleanup()

	id := seedUser(t, q)
	value := saveNew(t, st, id)

	sess := load(t, st, value)
	require.False(t, sess.IsNew, "an existing session must load")
	data, ok := sess.Values[auth.SessionDataKey].(auth.SessionData)
	require.True(t, ok)
	require.Equal(t, id, data.UserID)
	require.True(t, data.MFAPassed)
}

// Every way of not having a valid session must look identical, so that a
// caller cannot tell an id that never existed from one that was revoked.
func TestStore_UnusableCookiesAllYieldAFreshSession(t *testing.T) {
	st, q, cleanup := newStore(t)
	defer cleanup()

	t.Run("no cookie", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		sess, err := st.New(r, cookieName)
		require.NoError(t, err)
		require.True(t, sess.IsNew)
	})

	t.Run("garbage cookie", func(t *testing.T) {
		require.True(t, load(t, st, "not-a-signed-value").IsNew)
	})

	// Signed by a different key: proves the signature is actually checked, so
	// an attacker cannot submit ids of their own choosing and probe the table.
	t.Run("cookie signed with the wrong key", func(t *testing.T) {
		other := securecookie.CodecsFromPairs(
			securecookie.GenerateRandomKey(32), securecookie.GenerateRandomKey(32))
		forged, err := securecookie.EncodeMulti(cookieName, "some-id", other...)
		require.NoError(t, err)
		require.True(t, load(t, st, forged).IsNew)
	})

	t.Run("well-formed id with no row", func(t *testing.T) {
		value := saveNew(t, st, seedUser(t, q))
		sess := load(t, st, value)
		require.False(t, sess.IsNew)

		require.NoError(t, st.Delete(context.Background(), sess.ID))
		require.True(t, load(t, st, value).IsNew, "a deleted session must not load")
	})
}

// Logout. gorilla signals deletion with a negative MaxAge; the row has to go,
// or logout only clears the browser's copy — the bug this store exists to fix.
func TestStore_NegativeMaxAgeDeletesTheRow(t *testing.T) {
	st, q, cleanup := newStore(t)
	defer cleanup()

	value := saveNew(t, st, seedUser(t, q))
	sess := load(t, st, value)
	require.False(t, sess.IsNew)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	sess.Options.MaxAge = -1
	require.NoError(t, st.Save(r, w, sess))

	require.True(t, load(t, st, value).IsNew, "the row must be gone, not just the cookie")
}

func TestStore_DeleteForUser(t *testing.T) {
	st, q, cleanup := newStore(t)
	defer cleanup()

	victim, bystander := seedUser(t, q), seedUser(t, q)
	a := saveNew(t, st, victim)
	b := saveNew(t, st, victim) // a second device
	c := saveNew(t, st, bystander)

	require.NoError(t, st.DeleteForUser(context.Background(), victim))

	require.True(t, load(t, st, a).IsNew, "every session for the user must go")
	require.True(t, load(t, st, b).IsNew)
	require.False(t, load(t, st, c).IsNew, "another user's session must survive")
}

func TestStore_ExpiredRowDoesNotLoad(t *testing.T) {
	short, q, _, cleanup := newStoreWithLifetime(t, 1)
	defer cleanup()

	id := seedUser(t, q)
	value := saveNew(t, short, id)

	require.False(t, load(t, short, value).IsNew, "it loads while live")

	require.Eventually(t, func() bool {
		return load(t, short, value).IsNew
	}, 6*time.Second, 250*time.Millisecond,
		"an expired row must stop loading without waiting for the sweeper")
}

// Running inside a transaction also pins the sweep's clock. now() is frozen at
// the transaction's start, so a sweep using it would never see the short
// session as expired and this test would not finish.
func TestStore_DeleteExpired(t *testing.T) {
	live, q, _, cleanup := newStoreWithLifetime(t, 3600)
	defer cleanup()

	// A second store over the same transaction, with a short lifetime.
	short := sessionstore.New(q, hashKey, blockKey, &sessions.Options{Path: "/", MaxAge: 1})

	keepUser, goneUser := seedUser(t, q), seedUser(t, q)
	keep := saveNew(t, live, keepUser)
	saveNew(t, short, goneUser)

	require.Eventually(t, func() bool {
		n, err := live.DeleteExpired(context.Background())
		require.NoError(t, err)
		return n >= 1
	}, 6*time.Second, 250*time.Millisecond, "the expired row must be swept")

	require.False(t, load(t, live, keep).IsNew, "the sweep must not touch live sessions")
}

// The user id is denormalised into its own column so revocation can find every
// session without decoding each row. A session with no user — the OIDC flow
// writes one before anyone has authenticated — must still save.
func TestStore_SessionWithoutAUserSaves(t *testing.T) {
	st, _, cleanup := newStore(t)
	defer cleanup()

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	sess, err := st.New(r, cookieName)
	require.NoError(t, err)
	sess.Values["oidc_nonce"] = "abc123"
	require.NoError(t, st.Save(r, w, sess))

	value := w.Result().Cookies()[0].Value
	loaded := load(t, st, value)
	require.False(t, loaded.IsNew)
	require.Equal(t, "abc123", loaded.Values["oidc_nonce"])
}

// The store decides whether a disabled user's session loads, rather than
// relying on the disable handler having deleted it. That is deliberate: a
// disable landing between the password check and the session INSERT deletes
// nothing, because the row does not exist yet, and nothing revokes it
// afterwards. Deciding it in the lookup removes the window instead of
// narrowing it.
func TestStore_DisabledUsersSessionDoesNotLoad(t *testing.T) {
	st, q, _, cleanup := newStoreWithLifetime(t, 3600)
	defer cleanup()

	id := seedUser(t, q)
	value := saveNew(t, st, id)

	require.False(t, load(t, st, value).IsNew, "it loads while the user is active")

	// Disable directly — no session deletion, which is exactly the state the
	// login race leaves behind.
	require.NoError(t, q.DisableUser(context.Background(), id))

	require.True(t, load(t, st, value).IsNew,
		"a disabled user's session must not load, even though nothing deleted it")
}

// Soft-deleted users the same way. The row survives (sessions cascade on hard
// delete only), so the lookup has to exclude it.
func TestStore_SoftDeletedUsersSessionDoesNotLoad(t *testing.T) {
	st, q, _, cleanup := newStoreWithLifetime(t, 3600)
	defer cleanup()

	id := seedUser(t, q)
	value := saveNew(t, st, id)

	require.False(t, load(t, st, value).IsNew)

	require.NoError(t, q.SoftDeleteUser(context.Background(), id))

	require.True(t, load(t, st, value).IsNew,
		"a soft-deleted user's session must not load")
}

// Re-enabling restores it — a lookup that refused every session with a user
// would pass the two tests above.
func TestStore_ReEnabledUsersSessionLoadsAgain(t *testing.T) {
	st, q, _, cleanup := newStoreWithLifetime(t, 3600)
	defer cleanup()

	id := seedUser(t, q)
	value := saveNew(t, st, id)

	require.NoError(t, q.DisableUser(context.Background(), id))
	require.True(t, load(t, st, value).IsNew)

	require.NoError(t, q.EnableUser(context.Background(), id))

	require.False(t, load(t, st, value).IsNew,
		"re-enabling the user must make their existing session work again")
}

// A session's lifetime must not depend on how long the writing transaction has
// been open.
//
// expires_at is computed by the database, and with now() that is the
// TRANSACTION's start time, not the write's. Measured directly against
// Postgres: a 3-second lifetime written 2 seconds into a transaction yields
// 0.99 seconds. A transaction older than the lifetime writes a row that is
// already expired — a login that reports success and then does not work.
func TestStore_LifetimeIsUnaffectedByTransactionAge(t *testing.T) {
	_, q, tx, cleanup := newStoreWithLifetime(t, 3600)
	defer cleanup()

	id := seedUser(t, q)

	// Outlive the lifetime we are about to request, without ending the transaction.
	const lifetime = 2
	time.Sleep(3 * time.Second)

	sessionID := "tx-age-" + uuid.NewString()
	require.NoError(t, q.UpsertSession(context.Background(), dbgen.UpsertSessionParams{
		ID:              sessionID,
		UserID:          uuid.NullUUID{UUID: id, Valid: true},
		Data:            []byte("x"),
		LifetimeSeconds: lifetime,
	}))

	var expiresAt, realNow time.Time
	require.NoError(t, tx.QueryRow(
		`SELECT expires_at, clock_timestamp() FROM sessions WHERE id = $1`, sessionID).
		Scan(&expiresAt, &realNow))

	require.True(t, expiresAt.After(realNow),
		"a session written by an old transaction must not be born expired")
	require.InDelta(t, float64(lifetime), expiresAt.Sub(realNow).Seconds(), 0.5,
		"the writing transaction's age leaked into the session lifetime")
}

// The read side has the same hazard inverted, and it fails OPEN: now() inside a
// long transaction is EARLIER than real time, so a session that expired while
// the transaction was open would still satisfy the filter and keep
// authenticating.
func TestStore_ExpiredSessionDoesNotLoadInsideALongTransaction(t *testing.T) {
	_, q, _, cleanup := newStoreWithLifetime(t, 3600)
	defer cleanup()

	id := seedUser(t, q)

	sessionID := "read-clock-" + uuid.NewString()
	require.NoError(t, q.UpsertSession(context.Background(), dbgen.UpsertSessionParams{
		ID:              sessionID,
		UserID:          uuid.NullUUID{UUID: id, Valid: true},
		Data:            []byte("x"),
		LifetimeSeconds: 1,
	}))

	time.Sleep(2 * time.Second)

	_, err := q.GetSession(context.Background(), sessionID)
	require.ErrorIs(t, err, sql.ErrNoRows,
		"an expired session must stop loading however long the reading transaction has been open")
}
