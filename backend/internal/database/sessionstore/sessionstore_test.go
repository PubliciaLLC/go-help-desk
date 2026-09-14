package sessionstore_test

import (
	"context"
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
	db, closeDB := testutil.NewDB(t)
	q, rollback := testutil.TxQueries(t, db)
	st := sessionstore.New(q, hashKey, blockKey, &sessions.Options{
		Path: "/", MaxAge: 3600, HttpOnly: true,
	})
	return st, q, func() { rollback(); closeDB() }
}

// purge removes a user and its sessions outright.
//
// The two expiry tests run outside the harness transaction, so anything they
// create survives the test. Setup tests assert the users table is EMPTY, so a
// leftover user here fails an unrelated test in another file — exactly the
// "no test should depend on state left by another" rule. Hard delete, not
// SoftDelete: a soft-deleted row is still a row.
func purge(t *testing.T, db *testutil.DB, userID uuid.UUID) {
	t.Helper()
	_, err := db.SQL.Exec(`DELETE FROM sessions WHERE user_id = $1`, userID)
	require.NoError(t, err)
	_, err = db.SQL.Exec(`DELETE FROM users WHERE id = $1`, userID)
	require.NoError(t, err)
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

// Expiry is checked in SQL with now(), and now() inside a transaction is the
// TRANSACTION's start time — it does not advance. So these two run against the
// database directly rather than inside the harness transaction, and clean up
// after themselves. Waiting in wall-clock time inside a tx would never expire
// anything, which is a good way to write a test that cannot fail.

func TestStore_ExpiredRowDoesNotLoad(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	// Registered before the purge below so it runs AFTER it: t.Cleanup is
	// LIFO, and a deferred close would run before either, leaving purge to
	// talk to a closed database.
	t.Cleanup(closeDB)

	short := sessionstore.New(db.Queries, hashKey, blockKey, &sessions.Options{
		Path: "/", MaxAge: 1, HttpOnly: true,
	})
	id := seedUser(t, db.Queries)
	value := saveNew(t, short, id)
	t.Cleanup(func() { purge(t, db, id) })

	require.False(t, load(t, short, value).IsNew, "it loads while live")

	require.Eventually(t, func() bool {
		return load(t, short, value).IsNew
	}, 6*time.Second, 250*time.Millisecond,
		"an expired row must stop loading without waiting for the sweeper")
}

func TestStore_DeleteExpired(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB)

	live := sessionstore.New(db.Queries, hashKey, blockKey, &sessions.Options{Path: "/", MaxAge: 3600})
	short := sessionstore.New(db.Queries, hashKey, blockKey, &sessions.Options{Path: "/", MaxAge: 1})

	keepUser, goneUser := seedUser(t, db.Queries), seedUser(t, db.Queries)
	keep := saveNew(t, live, keepUser)
	saveNew(t, short, goneUser)
	t.Cleanup(func() {
		purge(t, db, keepUser)
		purge(t, db, goneUser)
	})

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
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB)

	st := sessionstore.New(db.Queries, hashKey, blockKey, &sessions.Options{
		Path: "/", MaxAge: 3600, HttpOnly: true,
	})
	id := seedUser(t, db.Queries)
	value := saveNew(t, st, id)
	t.Cleanup(func() { purge(t, db, id) })

	require.False(t, load(t, st, value).IsNew, "it loads while the user is active")

	// Disable directly — no session deletion, which is exactly the state the
	// login race leaves behind.
	require.NoError(t, userstore.New(db.Queries).Disable(context.Background(), id))

	require.True(t, load(t, st, value).IsNew,
		"a disabled user's session must not load, even though nothing deleted it")
}

// Soft-deleted users the same way. The row survives (sessions cascade on hard
// delete only), so the lookup has to exclude it.
func TestStore_SoftDeletedUsersSessionDoesNotLoad(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB)

	st := sessionstore.New(db.Queries, hashKey, blockKey, &sessions.Options{
		Path: "/", MaxAge: 3600, HttpOnly: true,
	})
	id := seedUser(t, db.Queries)
	value := saveNew(t, st, id)
	t.Cleanup(func() { purge(t, db, id) })

	require.False(t, load(t, st, value).IsNew)

	_, err := db.SQL.Exec(`UPDATE users SET deleted_at = now() WHERE id = $1`, id)
	require.NoError(t, err)

	require.True(t, load(t, st, value).IsNew,
		"a soft-deleted user's session must not load")
}

// Re-enabling restores it — a lookup that refused every session with a user
// would pass the two tests above.
func TestStore_ReEnabledUsersSessionLoadsAgain(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB)

	st := sessionstore.New(db.Queries, hashKey, blockKey, &sessions.Options{
		Path: "/", MaxAge: 3600, HttpOnly: true,
	})
	id := seedUser(t, db.Queries)
	value := saveNew(t, st, id)
	t.Cleanup(func() { purge(t, db, id) })

	require.NoError(t, userstore.New(db.Queries).Disable(context.Background(), id))
	require.True(t, load(t, st, value).IsNew)

	_, err := db.SQL.Exec(`UPDATE users SET disabled = FALSE WHERE id = $1`, id)
	require.NoError(t, err)

	require.False(t, load(t, st, value).IsNew,
		"re-enabling the user must make their existing session work again")
}
