package sessionstore_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/sessions"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/sessionstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// The cookie's attributes come from the store's configuration and from nowhere
// else — in particular not from session.Options, which a caller can replace
// wholesale and which handleLogout does.
//
// Secure is configured true here deliberately. The server test harness leaves
// it false, so an assertion over there that compares the issued and deleted
// cookies passes whether or not Secure is carried through: false equals false.
// This is the case that can actually fail.
func storeWithSecureCookies(t *testing.T) (*sessionstore.Store, *dbgen.Queries, func()) {
	t.Helper()
	db, closeDB := testutil.NewDB(t)
	tx, err := db.SQL.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	q := dbgen.New(tx)
	st := sessionstore.New(q, hashKey, blockKey, &sessions.Options{
		Path:     "/",
		Domain:   "help.example.com",
		MaxAge:   3600,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	return st, q, func() { _ = tx.Rollback(); closeDB() }
}

func TestCookie_CarriesTheConfiguredAttributes(t *testing.T) {
	st, q, cleanup := storeWithSecureCookies(t)
	defer cleanup()
	uid := seedUser(t, q)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/local/login", nil)
	rec := httptest.NewRecorder()
	sess, err := st.Get(req, "ghd_session")
	require.NoError(t, err)
	sess.Values["user_id"] = uid.String()
	require.NoError(t, st.Save(req, rec, sess))

	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1)
	c := cookies[0]

	require.True(t, c.Secure, "an HTTPS instance must mark the session cookie Secure")
	require.True(t, c.HttpOnly)
	require.Equal(t, "/", c.Path)
	require.Equal(t, "help.example.com", c.Domain)
	require.Equal(t, http.SameSiteLaxMode, c.SameSite)
	require.Equal(t, 3600, c.MaxAge)
	require.False(t, c.Expires.IsZero(), "a cookie that lasts an hour needs an expiry, not just Max-Age")
	require.WithinDuration(t, time.Now().Add(time.Hour), c.Expires, time.Minute)
}

// Deleting a cookie only works if the deletion matches the cookie: same path,
// same domain. The attributes therefore have to survive a caller that replaced
// session.Options with nothing but a negative MaxAge, which is exactly what
// logging out does.
func TestCookie_DeletionMatchesTheCookieItDeletes(t *testing.T) {
	st, q, cleanup := storeWithSecureCookies(t)
	defer cleanup()
	uid := seedUser(t, q)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/local/login", nil)
	issue := httptest.NewRecorder()
	sess, err := st.Get(req, "ghd_session")
	require.NoError(t, err)
	sess.Values["user_id"] = uid.String()
	require.NoError(t, st.Save(req, issue, sess))
	set := issue.Result().Cookies()[0]

	// What handleLogout does.
	sess.Options = &sessions.Options{MaxAge: -1}
	del := httptest.NewRecorder()
	require.NoError(t, st.Save(req, del, sess))

	cleared := del.Result().Cookies()[0]
	require.Equal(t, "", cleared.Value)
	require.Less(t, cleared.MaxAge, 0)
	// Not merely Before(now): the zero time.Time is also before now, so that
	// alone passes when no Expires is set at all — which is what a mutant
	// dropping the delete branch does.
	require.False(t, cleared.Expires.IsZero(), "the deletion must carry an expiry")
	require.WithinDuration(t, time.Unix(1, 0), cleared.Expires, time.Second,
		"deleting means an expiry in the past, matching the form gorilla used")

	require.Equal(t, set.Path, cleared.Path, "a deletion at another path deletes nothing")
	require.Equal(t, set.Domain, cleared.Domain)
	require.Equal(t, set.Secure, cleared.Secure)
	require.True(t, cleared.Secure, "and Secure must actually be true here, not merely equal")
	require.Equal(t, set.HttpOnly, cleared.HttpOnly)
	require.Equal(t, set.SameSite, cleared.SameSite)
}

// The nil-options guard in New. Nothing in production passes nil, but the
// branch exists, so it gets a test rather than shipping unexercised.
func TestNew_NilOptionsGetsUsableDefaults(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	tx, err := db.SQL.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	q := dbgen.New(tx)
	uid := seedUser(t, q)

	st := sessionstore.New(q, hashKey, blockKey, nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	sess, err := st.Get(req, "ghd_session")
	require.NoError(t, err)
	sess.Values["user_id"] = uid.String()
	require.NoError(t, st.Save(req, rec, sess))

	c := rec.Result().Cookies()[0]
	require.Equal(t, "/", c.Path, "a cookie scoped to the request path is not a session cookie")
	require.True(t, c.HttpOnly)
	require.Equal(t, http.SameSiteLaxMode, c.SameSite)
}
