package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/sessions"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// stubStore hands SessionAuth one stored session and records what is done to it.
type stubStore struct {
	data    auth.SessionData
	deleted bool
}

func (f *stubStore) Get(_ *http.Request, name string) (*sessions.Session, error) {
	s := sessions.NewSession(f, name)
	s.IsNew = false
	s.ID = "abc"
	s.Options = &sessions.Options{}
	s.Values[auth.SessionDataKey] = f.data
	return s, nil
}

func (f *stubStore) New(r *http.Request, name string) (*sessions.Session, error) {
	return f.Get(r, name)
}

func (f *stubStore) Save(_ *http.Request, _ http.ResponseWriter, s *sessions.Session) error {
	f.deleted = s.Options != nil && s.Options.MaxAge < 0
	return nil
}

// runSessionAuth returns the actor the handler saw (nil for none).
func runSessionAuth(t *testing.T, st *stubStore) *Actor {
	t.Helper()
	var got *Actor
	h := SessionAuth(st)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = GetActor(r)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	return got
}

func TestSessionAuth_PendingSession(t *testing.T) {
	id := uuid.New()
	now := time.Now()
	pending := func(until time.Time) auth.SessionData {
		return auth.SessionData{UserID: id, Role: user.RoleStaff, PendingUntil: until}
	}

	t.Run("inside the deadline is an actor, and is left alone", func(t *testing.T) {
		st := &stubStore{data: pending(now.Add(time.Minute))}
		require.NotNil(t, runSessionAuth(t, st))
		require.False(t, st.deleted)
	})
	t.Run("past the deadline is no actor, and the row is deleted", func(t *testing.T) {
		st := &stubStore{data: pending(now.Add(-time.Second))}
		require.Nil(t, runSessionAuth(t, st))
		require.True(t, st.deleted)
	})
	t.Run("no deadline (saved before the upgrade) is no actor, and the row is deleted", func(t *testing.T) {
		st := &stubStore{data: pending(time.Time{})}
		require.Nil(t, runSessionAuth(t, st))
		require.True(t, st.deleted)
	})
	t.Run("a fully signed-in session is never touched", func(t *testing.T) {
		st := &stubStore{data: auth.SessionData{UserID: id, Role: user.RoleStaff, MFAPassed: true}}
		require.NotNil(t, runSessionAuth(t, st))
		require.False(t, st.deleted)
	})
}
