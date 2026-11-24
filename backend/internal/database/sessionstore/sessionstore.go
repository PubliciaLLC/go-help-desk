// Package sessionstore keeps session state in Postgres instead of in the
// cookie.
//
// The cookie carries nothing but an opaque id; everything else lives in a row.
// That is what makes a session revocable: disabling a user, changing a role,
// resetting MFA or logging out becomes a DELETE, and takes effect on the next
// request rather than whenever the cookie happens to expire.
//
// It implements gorilla/sessions' Store, so the authentication middleware and
// every handler keep the API they already use.
package sessionstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/gob"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/securecookie"
	"github.com/gorilla/sessions"

	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
)

// sessionIDBytes is the entropy in a session id.
//
// The id is a bearer credential — whoever holds it is the session — so it is
// sized like one. 32 bytes matches the API-key tokens elsewhere in this
// codebase.
const sessionIDBytes = 32

// Store is a gorilla/sessions Store backed by the sessions table.
type Store struct {
	q       *dbgen.Queries
	codecs  []securecookie.Codec
	options *sessions.Options
}

// New returns a Store. The keys sign and encrypt the cookie that carries the
// session id.
//
// The id is signed even though it is opaque and useless on its own: an
// unsigned id lets an attacker submit arbitrary values, which turns every
// request into a probe of the sessions table. Signing means only ids this
// server issued are ever looked up.
func New(q *dbgen.Queries, hashKey, blockKey []byte, opts *sessions.Options) *Store {
	if opts == nil {
		opts = &sessions.Options{Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode}
	}
	return &Store{
		q:       q,
		codecs:  securecookie.CodecsFromPairs(hashKey, blockKey),
		options: opts,
	}
}

// cookie builds the session cookie from the store's configured options.
//
// Deliberately not from session.Options. A caller can replace that wholesale,
// and one does: handleLogout sets it to &sessions.Options{MaxAge: -1}, which
// drops Path, HttpOnly, SameSite and Secure along with everything else. The
// deletion cookie was then written with an empty Path, so the browser scoped it
// to the request's own directory — /api/v1/auth/local — and a cookie set at "/"
// is not deleted by a Set-Cookie at a different path. Logging out cleared the
// session row but left the cookie in the browser for the rest of its seven
// days.
//
// MaxAge is the one attribute a caller legitimately varies, so it is the one
// taken from the session. Everything that decides where the cookie goes and how
// it is protected comes from the configuration.
func (s *Store) cookie(name, value string, maxAge int) *http.Cookie {
	c := &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     s.options.Path,
		Domain:   s.options.Domain,
		MaxAge:   maxAge,
		Secure:   s.options.Secure,
		HttpOnly: s.options.HttpOnly,
		SameSite: s.options.SameSite,
	}
	// Expires as well as Max-Age, which is what sessions.NewCookie did and what
	// this replaced. Every current browser prefers Max-Age and would be fine
	// without it, but dropping an attribute that was going out yesterday is a
	// change nobody asked for, and a cookie with no expiry at all is a session
	// cookie — a different thing from one that lasts seven days.
	switch {
	case maxAge > 0:
		c.Expires = time.Now().Add(time.Duration(maxAge) * time.Second)
	case maxAge < 0:
		// Any time in the past deletes it. Matching gorilla's choice exactly so
		// the wire form is unchanged.
		c.Expires = time.Unix(1, 0)
	}
	return c
}

// Get returns the session for the request, from gorilla's per-request cache
// when it has already been loaded.
func (s *Store) Get(r *http.Request, name string) (*sessions.Session, error) {
	return sessions.GetRegistry(r).Get(s, name)
}

// New loads a session from the database, or returns an empty one.
//
// An unreadable cookie, an unknown id and an expired row are all treated the
// same way: a fresh session. Distinguishing them would tell a caller whether an
// id had ever existed.
func (s *Store) New(r *http.Request, name string) (*sessions.Session, error) {
	session := sessions.NewSession(s, name)
	opts := *s.options
	session.Options = &opts
	session.IsNew = true

	c, err := r.Cookie(name)
	if err != nil {
		return session, nil
	}

	var id string
	if err := securecookie.DecodeMulti(name, c.Value, &id, s.codecs...); err != nil {
		return session, nil
	}

	row, err := s.q.GetSession(r.Context(), id)
	if err != nil {
		// Includes sql.ErrNoRows, which is the normal outcome after a session
		// is revoked or expires. Not an error the caller can do anything with.
		return session, nil
	}

	// GetSession embeds the session row because it joins users to exclude
	// disabled and deleted accounts; the join columns are not selected.
	if err := gob.NewDecoder(bytes.NewReader(row.Session.Data)).Decode(&session.Values); err != nil {
		// gob leaves the map partly filled on a failed decode. The middleware
		// checks IsNew first so this is not reachable today, but a handler
		// reading Values without that check would see fragments of a session
		// that failed to load.
		session.Values = map[any]any{}
		return session, nil
	}
	session.ID = id
	session.IsNew = false
	return session, nil
}

// Save writes the session to the database and the id to the cookie.
//
// A negative MaxAge deletes the row, which is what makes logout mean
// something: the cookie is cleared AND the session stops existing, so a copy
// taken beforehand is equally dead.
func (s *Store) Save(r *http.Request, w http.ResponseWriter, session *sessions.Session) error {
	if session.Options != nil && session.Options.MaxAge < 0 {
		if session.ID != "" {
			if err := s.q.DeleteSession(r.Context(), session.ID); err != nil {
				return fmt.Errorf("deleting session: %w", err)
			}
		}
		http.SetCookie(w, s.cookie(session.Name(), "", -1))
		return nil
	}

	if session.ID == "" {
		id, err := newSessionID()
		if err != nil {
			return err
		}
		session.ID = id
	}

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(session.Values); err != nil {
		return fmt.Errorf("encoding session: %w", err)
	}

	maxAge := s.options.MaxAge
	if session.Options != nil && session.Options.MaxAge > 0 {
		maxAge = session.Options.MaxAge
	}

	if err := s.q.UpsertSession(r.Context(), dbgen.UpsertSessionParams{
		ID: session.ID,
		// Denormalised out of the payload so revocation can find every session
		// a user holds without decoding each row.
		UserID: userIDFrom(session),
		Data:   buf.Bytes(),
		// A duration, not a deadline: the database computes expires_at from
		// clock_timestamp(), which is the same clock GetSession and the expiry
		// sweep compare against. Sending an absolute time from here made a
		// session's lifetime depend on two clocks agreeing.
		LifetimeSeconds: int32(maxAge),
	}); err != nil {
		return fmt.Errorf("saving session: %w", err)
	}

	encoded, err := securecookie.EncodeMulti(session.Name(), session.ID, s.codecs...)
	if err != nil {
		return fmt.Errorf("encoding session cookie: %w", err)
	}
	http.SetCookie(w, s.cookie(session.Name(), encoded, maxAge))
	return nil
}

// Delete removes one session by id.
//
// Used to rotate the id when a session gains authority: the old row goes and a
// fresh id is minted, so an id an attacker planted before login cannot be the
// id that ends up authenticated.
func (s *Store) Delete(ctx context.Context, id string) error {
	return s.q.DeleteSession(ctx, id)
}

// DeleteForUser revokes every session a user holds.
//
// Called after the events that make an existing session wrong: disable, role
// change, password change, MFA reset.
func (s *Store) DeleteForUser(ctx context.Context, userID uuid.UUID) error {
	return s.q.DeleteSessionsForUser(ctx, database.NullUUID(&userID))
}

// DeleteExpired removes rows past their expiry. Sessions are already refused
// on read, so this is housekeeping rather than a security control.
func (s *Store) DeleteExpired(ctx context.Context) (int64, error) {
	return s.q.DeleteExpiredSessions(ctx)
}

func newSessionID() (string, error) {
	b := make([]byte, sessionIDBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating session id: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// userIDFrom pulls the user out of the session payload for the indexed column.
func userIDFrom(session *sessions.Session) uuid.NullUUID {
	data, ok := session.Values[auth.SessionDataKey].(auth.SessionData)
	if !ok || data.UserID == uuid.Nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: data.UserID, Valid: true}
}
