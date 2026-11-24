package userstore_test

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// The SAML sign-in against a real Postgres: how an assertion is matched to an
// account (DESIGN.md, SAML -> First sign-in provisioning and account matching).
//
// Entra answers a transient NameID request with a different NameID at every
// sign-in, and the subject used to be the only key, so the second sign-in of
// everyone looked like a new person whose address was already taken. These
// tests pin what happens instead, against the unique constraint that made it
// fail, not against a fake of it.

type samlEnv struct {
	ctx   context.Context
	svc   *user.Service
	store *userstore.Store
	audit audit.Store
	tx    *sql.Tx // nil on an isolated database
	db    *sql.DB
}

// newSAMLEnv is one rolled-back transaction: every test is its own world. It
// starts with one local administrator, because deleting or disabling anyone
// asks whether an administrator would be left.
func newSAMLEnv(t *testing.T) *samlEnv {
	t.Helper()
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB)
	q, tx, rollback := testutil.TxQueriesTx(t, db)
	t.Cleanup(rollback)
	store := userstore.New(q)
	au := auditstore.New(q)
	e := &samlEnv{
		ctx:   context.Background(),
		svc:   user.NewService(store, user.WithBcryptCost(4), user.WithAuditStore(au)),
		store: store, audit: au, tx: tx,
	}
	e.localUser(t, uniqueAddr("base-admin"))
	return e
}

// newCommittedSAMLEnv is a database of its own with a real pool, for the tests
// whose point is two connections; see testutil.NewIsolatedDB.
func newCommittedSAMLEnv(t *testing.T) *samlEnv {
	t.Helper()
	db, closeDB := testutil.NewIsolatedDB(t)
	t.Cleanup(closeDB)
	q := dbgen.New(db.SQL)
	store := userstore.NewWithDB(q, db.SQL)
	au := auditstore.New(q)
	e := &samlEnv{
		ctx:   context.Background(),
		svc:   user.NewService(store, user.WithBcryptCost(4), user.WithAuditStore(au)),
		store: store, audit: au, db: db.SQL,
	}
	e.localUser(t, uniqueAddr("base-admin"))
	return e
}

func (e *samlEnv) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	var err error
	if e.tx != nil {
		_, err = e.tx.ExecContext(e.ctx, query, args...)
	} else {
		_, err = e.db.ExecContext(e.ctx, query, args...)
	}
	require.NoError(t, err)
}

func (e *samlEnv) count(t *testing.T, query string, args ...any) (n int) {
	t.Helper()
	row := func() *sql.Row {
		if e.tx != nil {
			return e.tx.QueryRowContext(e.ctx, query, args...)
		}
		return e.db.QueryRowContext(e.ctx, query, args...)
	}()
	require.NoError(t, row.Scan(&n))
	return n
}

func (e *samlEnv) signIn(subject, email, name string) (user.User, error) {
	return e.svc.UpsertSAMLUser(e.ctx, subject, email, name, nil)
}

func (e *samlEnv) row(t *testing.T, id uuid.UUID) user.User {
	t.Helper()
	u, err := e.store.GetByIDAdmin(e.ctx, id)
	require.NoError(t, err)
	return u
}

// localUser is an account that was not created by SAML.
func (e *samlEnv) localUser(t *testing.T, email string) user.User {
	t.Helper()
	u, err := e.svc.Create(e.ctx, user.CreateUserInput{
		Email: email, DisplayName: "Local Person", Role: user.RoleAdmin, Password: "correct horse battery",
	})
	require.NoError(t, err)
	return u
}

func uniqueAddr(label string) string { return label + "-" + uuid.NewString()[:8] + "@example.com" }

// Same NameID twice: the case that always worked, kept as a pin.
func TestSAMLSignIn_SameSubjectTwice(t *testing.T) {
	e := newSAMLEnv(t)
	addr := uniqueAddr("twice")

	first, err := e.signIn("persistent-1", addr, "Entra User")
	require.NoError(t, err)
	second, err := e.signIn("persistent-1", addr, "Entra User")
	require.NoError(t, err)

	require.Equal(t, first.ID, second.ID)
	require.Equal(t, 1, e.count(t, `SELECT count(*) FROM users WHERE email = $1`, addr))
}

// Promotion changes the role and nothing about how the account is found.
func TestSAMLSignIn_PromotedThenSameSubject(t *testing.T) {
	e := newSAMLEnv(t)
	addr := uniqueAddr("promoted")
	e.localUser(t, uniqueAddr("other-admin")) // so the promotion is not the last-admin case

	first, err := e.signIn("persistent-2", addr, "Entra User")
	require.NoError(t, err)
	require.NoError(t, e.svc.SetRole(e.ctx, first.ID, user.RoleAdmin))

	again, err := e.signIn("persistent-2", addr, "Entra User")
	require.NoError(t, err)
	require.Equal(t, first.ID, again.ID)
	require.Equal(t, user.RoleAdmin, e.row(t, first.ID).Role)
}

// The reported failure: a new NameID for an address a SAML-created account
// already holds. The account is relinked, not refused.
func TestSAMLSignIn_NewSubjectSameAddressRelinks(t *testing.T) {
	cases := []struct {
		name     string
		promote  bool
		secondIn string // address as the IdP sends it the second time
	}{
		{"plain account", false, ""},
		{"promoted account keeps its role", true, ""},
		{"address matched ignoring case", false, "  Entra.USER@Example.COM "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newSAMLEnv(t)
			addr := "entra.user+" + uuid.NewString()[:8] + "@example.com"
			e.localUser(t, uniqueAddr("other-admin"))

			first, err := e.signIn("_transient-first", addr, "Old Name")
			require.NoError(t, err)
			wantRole := user.RoleUser
			if tc.promote {
				require.NoError(t, e.svc.SetRole(e.ctx, first.ID, user.RoleAdmin))
				wantRole = user.RoleAdmin
			}

			second := addr
			if tc.secondIn != "" {
				// Same person, address in the case the IdP happens to use.
				e.exec(t, `UPDATE users SET email = 'entra.user@example.com' WHERE id = $1`, first.ID)
				second = tc.secondIn
			}
			got, err := e.signIn("_transient-second", second, "New Name")
			require.NoError(t, err)

			require.Equal(t, first.ID, got.ID, "the same account, not a new one")
			require.Equal(t, wantRole, got.Role)
			row := e.row(t, first.ID)
			require.Equal(t, "_transient-second", row.SAMLSubject)
			require.Equal(t, "New Name", row.DisplayName)
			require.Equal(t, wantRole, row.Role)
			require.Equal(t, 1, e.count(t, `SELECT count(*) FROM users WHERE lower(email) = lower($1)`, row.Email))

			// And the new subject now finds it directly, with no further relink.
			third, err := e.signIn("_transient-second", second, "New Name")
			require.NoError(t, err)
			require.Equal(t, first.ID, third.ID)
		})
	}
}

// SAML must never take over a local account, an administrator least of all.
func TestSAMLSignIn_LocalAccountHoldingTheAddressIsNotRelinked(t *testing.T) {
	e := newSAMLEnv(t)
	addr := uniqueAddr("local-admin")
	local := e.localUser(t, addr)
	before := e.row(t, local.ID)

	_, err := e.signIn("some-subject", addr, "Impostor")
	require.ErrorIs(t, err, user.ErrEmailTaken)

	after := e.row(t, local.ID)
	require.Equal(t, before, after, "the local account must be untouched")
	require.Empty(t, after.SAMLSubject)
	require.Equal(t, 1, e.count(t, `SELECT count(*) FROM users WHERE email = $1`, addr))
}

// A service account is not a person; it never signs in, and nothing relinks it.
func TestSAMLSignIn_ServiceAccountHoldingTheAddressIsRefused(t *testing.T) {
	e := newSAMLEnv(t)
	addr := uniqueAddr("svc")
	id := uuid.New()
	e.exec(t, `INSERT INTO users (id, email, display_name, role, service_account) VALUES ($1, $2, 'Service', 'staff', true)`, id, addr)

	_, err := e.signIn("some-subject", addr, "Impostor")
	require.ErrorIs(t, err, user.ErrEmailTaken)
	require.Empty(t, e.row(t, id).SAMLSubject)
}

// A disabled account keeps being refused, by its subject as always and now also
// by its address, and a refusal writes nothing: disabling someone is not undone
// by their address arriving under another NameID.
func TestSAMLSignIn_DisabledAccountIsRefused(t *testing.T) {
	cases := []struct{ name, subject string }{
		{"known subject", "disabled-subject"},
		{"new subject, same address", "disabled-other-subject"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newSAMLEnv(t)
			addr := uniqueAddr("disabled")
			first, err := e.signIn("disabled-subject", addr, "Disabled Person")
			require.NoError(t, err)
			require.NoError(t, e.svc.Disable(e.ctx, first.ID))
			before := e.row(t, first.ID)

			_, err = e.signIn(tc.subject, addr, "Disabled Person")
			require.ErrorIs(t, err, user.ErrUserDisabled)
			require.Equal(t, before, e.row(t, first.ID))
		})
	}
}

// Deleting a SAML-created person means they are gone. Signing in again makes a
// new account; the old row keeps its id, so everything that points at it
// (tickets, audit history) stays attached to it, and it gives up the address.
func TestSAMLSignIn_DeletedSAMLAccountIsTombstonedAndRecreated(t *testing.T) {
	cases := []struct{ name, subject string }{
		{"same subject", "gone-subject"},
		{"new subject", "gone-subject-2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newSAMLEnv(t)
			addr := uniqueAddr("gone")
			old, err := e.signIn("gone-subject", addr, "Gone Person")
			require.NoError(t, err)

			// History that must stay attached to the old row.
			var ticketID uuid.UUID
			e.exec(t, `INSERT INTO categories (id, name) VALUES (gen_random_uuid(), 'History') ON CONFLICT DO NOTHING`)
			ticketID = uuid.New()
			e.exec(t, `INSERT INTO tickets (id, tracking_number, subject, description_format, source_channel, category_id, status_id, reporter_user_id)
				SELECT $1::uuid, 'T-'||substr($1::uuid::text, 1, 8), 'Printer', 'plain', 'web', (SELECT id FROM categories LIMIT 1), (SELECT id FROM statuses LIMIT 1), $2`,
				ticketID, old.ID)
			require.NoError(t, e.audit.Create(e.ctx, audit.Entry{
				ID: uuid.New(), EntityType: "user", EntityID: old.ID, Action: "history", CreatedAt: time.Now(),
			}))
			require.NoError(t, e.svc.SoftDelete(e.ctx, old.ID))

			got, err := e.signIn(tc.subject, addr, "Back Again")
			require.NoError(t, err)

			require.NotEqual(t, old.ID, got.ID, "a new account, not the deleted one")
			require.Equal(t, user.RoleUser, got.Role)
			require.Equal(t, addr, got.Email)
			require.Equal(t, tc.subject, got.SAMLSubject)
			require.Nil(t, e.row(t, got.ID).DeletedAt)

			gone := e.row(t, old.ID)
			require.NotNil(t, gone.DeletedAt, "still deleted")
			require.Equal(t, "deleted-"+old.ID.String()+"@invalid", gone.Email)
			require.Empty(t, gone.SAMLSubject)
			require.Equal(t, "Gone Person", gone.DisplayName)
			require.Equal(t, 1, e.count(t, `SELECT count(*) FROM tickets WHERE id = $1 AND reporter_user_id = $2`, ticketID, old.ID))
			require.Equal(t, 1, e.count(t, `SELECT count(*) FROM audit_log WHERE entity_id = $1 AND action = 'history'`, old.ID))

			// And it is an ordinary account from here on.
			again, err := e.signIn(tc.subject, addr, "Back Again")
			require.NoError(t, err)
			require.Equal(t, got.ID, again.ID)
		})
	}
}

// The new account is created as any first SAML sign-in is, so the domain
// allowlist applies; a refusal must not retire the old row.
func TestSAMLSignIn_DeletedSAMLAccountRespectsTheDomainAllowlist(t *testing.T) {
	e := newSAMLEnv(t)
	addr := uniqueAddr("domain")
	old, err := e.signIn("domain-subject", addr, "Gone Person")
	require.NoError(t, err)
	require.NoError(t, e.svc.SoftDelete(e.ctx, old.ID))
	before := e.row(t, old.ID)

	_, err = e.svc.UpsertSAMLUser(e.ctx, "domain-subject-2", addr, "Gone Person", []string{"other.example"})
	require.ErrorIs(t, err, user.ErrDomainNotAllowed)
	require.Equal(t, before, e.row(t, old.ID))
}

// Relinking is not provisioning: an existing account is not re-checked against
// the allowlist, as a known subject never was.
func TestSAMLSignIn_RelinkIgnoresTheDomainAllowlist(t *testing.T) {
	e := newSAMLEnv(t)
	addr := uniqueAddr("relink-domain")
	first, err := e.signIn("rd-1", addr, "Person")
	require.NoError(t, err)

	got, err := e.svc.UpsertSAMLUser(e.ctx, "rd-2", addr, "Person", []string{"other.example"})
	require.NoError(t, err)
	require.Equal(t, first.ID, got.ID)
}

// A deleted LOCAL account still owns its address and still blocks.
func TestSAMLSignIn_DeletedLocalAccountStillBlocks(t *testing.T) {
	e := newSAMLEnv(t)
	addr := uniqueAddr("deleted-local")
	e.localUser(t, uniqueAddr("other-admin"))
	local, err := e.svc.Create(e.ctx, user.CreateUserInput{Email: addr, DisplayName: "Old Local", Role: user.RoleStaff, Password: "correct horse battery"})
	require.NoError(t, err)
	require.NoError(t, e.svc.SoftDelete(e.ctx, local.ID))
	before := e.row(t, local.ID)

	_, err = e.signIn("any-subject", addr, "Someone")
	require.ErrorIs(t, err, user.ErrEmailTaken)
	require.Equal(t, before, e.row(t, local.ID))
}

// A relink and a tombstone are audited, and the entries carry ids and flags,
// never the NameID or the address.
func TestSAMLSignIn_RelinkAndTombstoneAreAudited(t *testing.T) {
	e := newSAMLEnv(t)
	addr := uniqueAddr("audited")
	first, err := e.signIn("audit-subject-1", addr, "Audited")
	require.NoError(t, err)

	_, err = e.signIn("audit-subject-2", addr, "Audited")
	require.NoError(t, err)
	entries, err := e.audit.ListByEntity(e.ctx, "user", first.ID, 50, 0)
	require.NoError(t, err)
	relinked := findAction(entries, "saml_relinked")
	require.NotNil(t, relinked, "a relink must be audited")
	require.NotContains(t, relinked.After, "saml_subject")
	require.Equal(t, true, relinked.After["saml_subject_changed"])
	requireNoSecrets(t, relinked, "audit-subject-1", "audit-subject-2", addr)

	require.NoError(t, e.svc.SoftDelete(e.ctx, first.ID))
	again, err := e.signIn("audit-subject-3", addr, "Audited")
	require.NoError(t, err)

	oldEntries, err := e.audit.ListByEntity(e.ctx, "user", first.ID, 50, 0)
	require.NoError(t, err)
	released := findAction(oldEntries, "email_released")
	require.NotNil(t, released, "retiring the old row's address must be audited on that row")
	require.Equal(t, again.ID.String(), released.After["replaced_by_user_id"])
	requireNoSecrets(t, released, "audit-subject-1", "audit-subject-2", "audit-subject-3", addr)

	newEntries, err := e.audit.ListByEntity(e.ctx, "user", again.ID, 50, 0)
	require.NoError(t, err)
	reprov := findAction(newEntries, "replaced_deleted_user")
	require.NotNil(t, reprov)
	require.Equal(t, first.ID.String(), reprov.After["previous_user_id"])
	requireNoSecrets(t, reprov, "audit-subject-3", addr)
}

func findAction(entries []audit.Entry, action string) *audit.Entry {
	for i := range entries {
		if entries[i].Action == action {
			return &entries[i]
		}
	}
	return nil
}

func requireNoSecrets(t *testing.T, e *audit.Entry, secrets ...string) {
	t.Helper()
	for _, m := range []map[string]any{e.Before, e.After} {
		for k, v := range m {
			s, _ := v.(string)
			for _, secret := range secrets {
				require.NotEqual(t, secret, s, "audit key %q carries a secret", k)
			}
		}
	}
}

// Two first sign-ins of one person at the same moment make one account and no
// error: the loser's insert hits the unique address, and it finds the winner.
func TestSAMLSignIn_ConcurrentFirstSignInsMakeOneAccount(t *testing.T) {
	cases := []struct {
		name    string
		subject func(i int) string // what each simultaneous request carries
	}{
		{"same NameID", func(int) string { return "race-persistent" }},
		{"a different NameID each time (transient IdP)", func(i int) string { return "race-transient-" + uuid.NewString() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newCommittedSAMLEnv(t)
			addr := uniqueAddr("race")
			ids, errs := e.concurrently(16, func(i int) (user.User, error) {
				return e.signIn(tc.subject(i), addr, "Racer")
			})
			for _, err := range errs {
				require.NoError(t, err)
			}
			require.Len(t, uniq(ids), 1, "every sign-in must land on the same account")
			require.Equal(t, 1, e.count(t, `SELECT count(*) FROM users WHERE email = $1`, addr))
		})
	}
}

// The same for a returning, deleted person: one tombstone, one new account.
func TestSAMLSignIn_ConcurrentSignInsAfterDeleteMakeOneAccount(t *testing.T) {
	e := newCommittedSAMLEnv(t)
	addr := uniqueAddr("race-deleted")
	old, err := e.signIn("race-old", addr, "Gone")
	require.NoError(t, err)
	require.NoError(t, e.svc.SoftDelete(e.ctx, old.ID))

	ids, errs := e.concurrently(16, func(i int) (user.User, error) {
		return e.signIn("race-new-"+uuid.NewString(), addr, "Back")
	})
	for _, err := range errs {
		require.NoError(t, err)
	}
	require.Len(t, uniq(ids), 1)
	require.NotContains(t, ids, old.ID)
	require.Equal(t, 1, e.count(t, `SELECT count(*) FROM users WHERE email = $1 AND deleted_at IS NULL`, addr))
	require.Equal(t, 1, e.count(t, `SELECT count(*) FROM users WHERE email = $1`, "deleted-"+old.ID.String()+"@invalid"))
}

func (e *samlEnv) concurrently(n int, fn func(i int) (user.User, error)) ([]uuid.UUID, []error) {
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		ids   []uuid.UUID
		errs  []error
		start = make(chan struct{})
	)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			u, err := fn(i)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			ids = append(ids, u.ID)
		}()
	}
	close(start)
	wg.Wait()
	return ids, errs
}

func uniq(ids []uuid.UUID) map[uuid.UUID]bool {
	m := map[uuid.UUID]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// Rows written before addresses were lower-cased (v1.2.0) can hold mixed case,
// and the unique index is case-sensitive, so "Legacy@Example.com" and
// "legacy@example.com" are different to it. The sign-in must still find them:
// a second account for the same person is the failure this branch exists to
// stop.
func TestSAMLSignIn_LegacyMixedCaseRows(t *testing.T) {
	t.Run("a live SAML-created row is relinked, not duplicated", func(t *testing.T) {
		e := newSAMLEnv(t)
		id := uuid.New()
		e.exec(t, `INSERT INTO users (id, email, display_name, role, saml_subject) VALUES ($1, 'Legacy@Example.com', 'Legacy', 'staff', 'dead-transient')`, id)

		got, err := e.signIn("persistent-legacy", "legacy@example.com", "Legacy")
		require.NoError(t, err)
		require.Equal(t, id, got.ID)
		require.Equal(t, user.RoleStaff, got.Role)
		require.Equal(t, 1, e.count(t, `SELECT count(*) FROM users WHERE lower(email) = 'legacy@example.com'`))
		require.Equal(t, "persistent-legacy", e.row(t, id).SAMLSubject)
	})

	t.Run("a deleted SAML-created row gives up its address", func(t *testing.T) {
		e := newSAMLEnv(t)
		id := uuid.New()
		e.exec(t, `INSERT INTO users (id, email, display_name, role, saml_subject, deleted_at) VALUES ($1, 'Legacy2@Example.com', 'Legacy', 'user', 'old', now())`, id)

		got, err := e.signIn("persistent-legacy2", "legacy2@example.com", "Legacy")
		require.NoError(t, err)
		require.NotEqual(t, id, got.ID)
		require.Equal(t, "deleted-"+id.String()+"@invalid", e.row(t, id).Email)
		require.Equal(t, 1, e.count(t, `SELECT count(*) FROM users WHERE lower(email) = 'legacy2@example.com'`))
	})

	t.Run("a local row is still refused", func(t *testing.T) {
		e := newSAMLEnv(t)
		e.exec(t, `INSERT INTO users (id, email, display_name, role) VALUES ($1, 'LocalLegacy@Example.com', 'Local', 'admin')`, uuid.New())

		_, err := e.signIn("some-subject", "locallegacy@example.com", "Impostor")
		require.ErrorIs(t, err, user.ErrEmailTaken)
		require.Equal(t, 1, e.count(t, `SELECT count(*) FROM users WHERE lower(email) = 'locallegacy@example.com'`))
	})
}

// The address is checked before the domain allowlist. An IdP that used to send
// the address as the NameID and sends no address attribute leaves the NameID
// standing in for it, and that is "invalid email", not "your domain is not
// allowed".
func TestSAMLSignIn_AddressIsValidatedBeforeTheAllowlist(t *testing.T) {
	cases := []struct {
		name    string
		email   string
		domains []string
		want    error
	}{
		{"opaque NameID as the address, allowlist set", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", []string{"example.com"}, user.ErrValidation},
		{"opaque NameID as the address, no allowlist", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", nil, user.ErrValidation},
		{"a good address outside the allowlist", "someone@other.example", []string{"example.com"}, user.ErrDomainNotAllowed},
		{"a good address inside the allowlist", "someone@example.com", []string{"example.com"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newSAMLEnv(t)
			_, err := e.svc.UpsertSAMLUser(e.ctx, "subject-"+uuid.NewString(), tc.email, "Person", tc.domains)
			if tc.want == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tc.want)
		})
	}
}

// Before addresses were lower-cased (v1.2.0) a person could end up with two
// rows whose addresses differ only in case; the unique index is case-sensitive
// and let both in. Nothing writes such a pair now: every write lower-cases, so
// the only twins are legacy data. Which of them a sign-in meets must not depend
// on which is older. The exact (lower-case) row is the one the unique index
// treats as holding the address, so it decides.
func TestSAMLSignIn_LegacyTwins(t *testing.T) {
	insert := func(t *testing.T, e *samlEnv, addr, role, subject string, age time.Duration, deleted bool) uuid.UUID {
		t.Helper()
		id := uuid.New()
		e.exec(t, `INSERT INTO users (id, email, display_name, role, saml_subject, created_at, deleted_at)
			VALUES ($1, $2, 'Twin', $3, $4, now() - $5::interval, CASE WHEN $6 THEN now() END)`,
			id, addr, role, subject, fmt.Sprintf("%d seconds", int(age.Seconds())), deleted)
		return id
	}

	t.Run("an older live local row with other capitals does not hide the exact SAML row", func(t *testing.T) {
		e := newSAMLEnv(t)
		insert(t, e, "Twin@Example.com", "admin", "", 48*time.Hour, false)
		saml := insert(t, e, "twin@example.com", "user", "dead-transient", time.Hour, false)

		got, err := e.signIn("persistent-twin", "twin@example.com", "Twin")
		require.NoError(t, err)
		require.Equal(t, saml, got.ID)
		require.Equal(t, "persistent-twin", e.row(t, saml).SAMLSubject)
	})

	t.Run("an exact local row is not taken over because an older SAML row has other capitals", func(t *testing.T) {
		e := newSAMLEnv(t)
		insert(t, e, "Twin2@Example.com", "user", "dead-transient", 48*time.Hour, false)
		local := insert(t, e, "twin2@example.com", "admin", "", time.Hour, false)

		_, err := e.signIn("persistent-twin2", "twin2@example.com", "Twin")
		require.ErrorIs(t, err, user.ErrEmailTaken)
		require.Empty(t, e.row(t, local).SAMLSubject)
	})

	t.Run("two deleted twins: a deleted local row keeps blocking whichever is older", func(t *testing.T) {
		for _, localOlder := range []bool{true, false} {
			t.Run(fmt.Sprintf("local older: %v", localOlder), func(t *testing.T) {
				e := newSAMLEnv(t)
				localAge, samlAge := 48*time.Hour, time.Hour
				if !localOlder {
					localAge, samlAge = samlAge, localAge
				}
				insert(t, e, "DTwin@Example.com", "user", "", localAge, true)
				samlRow := insert(t, e, "dTwin@Example.com", "user", "old", samlAge, true)

				_, err := e.signIn("persistent-dtwin", "dtwin@example.com", "Twin")
				require.ErrorIs(t, err, user.ErrEmailTaken, "local row older: %v", localOlder)
				require.Equal(t, "dTwin@Example.com", e.row(t, samlRow).Email, "nothing was released")
			})
		}
	})
}
