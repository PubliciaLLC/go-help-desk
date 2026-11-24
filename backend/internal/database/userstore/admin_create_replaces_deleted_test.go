package userstore_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// The administrator's "New user" form: an address held only by deleted rows is
// released and the account is created, in one transaction, so the old row
// keeps its id and everything pointing at it. An address held by a live row is
// still a 409 (user.ErrEmailTaken). Shares ReleaseDeletedEmail with the SAML
// sign-in; see saml_signin_test.go for that side.

func (e *samlEnv) adminCreate(addr, name string, actor *uuid.UUID) (user.User, error) {
	return e.svc.CreateByAdmin(e.ctx, user.CreateUserInput{
		Email: addr, DisplayName: name, Role: user.RoleStaff, Password: "correct horse battery",
	}, actor)
}

// seedDeleted makes a deleted row of the given kind holding addr.
func (e *samlEnv) seedDeleted(t *testing.T, kind, addr string) user.User {
	t.Helper()
	var u user.User
	var err error
	switch kind {
	case "local":
		u, err = e.svc.Create(e.ctx, user.CreateUserInput{Email: addr, DisplayName: "Old Local", Role: user.RoleStaff, Password: "correct horse battery"})
	case "saml":
		u, err = e.svc.Create(e.ctx, user.CreateUserInput{Email: addr, DisplayName: "Old SAML", Role: user.RoleUser, SAMLSubject: "old-" + uuid.NewString()})
	}
	require.NoError(t, err)
	require.NoError(t, e.svc.SoftDelete(e.ctx, u.ID))
	return u
}

func TestAdminCreate_OverDeletedRowReleasesTheAddress(t *testing.T) {
	cases := []struct {
		name, kind, asTyped string
	}{
		{"deleted local account", "local", ""},
		{"deleted SAML-created account", "saml", ""},
		{"address typed in another case", "local", "  Typed.Mixed@Example.COM "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newSAMLEnv(t)
			addr := uniqueAddr("admin-over-deleted")
			typed := addr
			if tc.asTyped != "" {
				addr = "typed.mixed@example.com"
				typed = tc.asTyped
			}
			old := e.seedDeleted(t, tc.kind, addr)
			actor := e.localUser(t, uniqueAddr("the-admin")).ID

			// History that must stay on the old row.
			ticketID := uuid.New()
			e.exec(t, `INSERT INTO categories (id, name) VALUES (gen_random_uuid(), 'History') ON CONFLICT DO NOTHING`)
			e.exec(t, `INSERT INTO tickets (id, tracking_number, subject, description_format, source_channel, category_id, status_id, reporter_user_id)
				SELECT $1::uuid, 'T-'||substr($1::uuid::text, 1, 8), 'Printer', 'plain', 'web', (SELECT id FROM categories LIMIT 1), (SELECT id FROM statuses LIMIT 1), $2`,
				ticketID, old.ID)
			require.NoError(t, e.audit.Create(e.ctx, audit.Entry{
				ID: uuid.New(), EntityType: "user", EntityID: old.ID, Action: "history", CreatedAt: time.Now(),
			}))

			got, err := e.adminCreate(typed, "Fresh Person", &actor)
			require.NoError(t, err)

			require.NotEqual(t, old.ID, got.ID)
			require.Equal(t, addr, got.Email)
			require.Nil(t, e.row(t, got.ID).DeletedAt)
			gone := e.row(t, old.ID)
			require.NotNil(t, gone.DeletedAt)
			require.Equal(t, "deleted-"+old.ID.String()+"@invalid", gone.Email)
			require.Empty(t, gone.SAMLSubject)
			require.Equal(t, old.DisplayName, gone.DisplayName)
			require.Equal(t, 1, e.count(t, `SELECT count(*) FROM tickets WHERE id = $1 AND reporter_user_id = $2`, ticketID, old.ID))
			require.Equal(t, 1, e.count(t, `SELECT count(*) FROM audit_log WHERE entity_id = $1 AND action = 'history'`, old.ID))

			oldEntries, err := e.audit.ListByEntity(e.ctx, "user", old.ID, 50, 0)
			require.NoError(t, err)
			released := findAction(oldEntries, "email_released")
			require.NotNil(t, released, "the tombstone must be audited on the old row")
			require.Equal(t, got.ID.String(), released.After["replaced_by_user_id"])
			require.Equal(t, actor, *released.ActorID, "the administrator who did it")
			requireNoSecrets(t, released, addr, "correct horse battery")
		})
	}
}

func TestAdminCreate_OverLiveRowIsStillRefused(t *testing.T) {
	// A real pool: the refused insert aborts its transaction, which the
	// rolled-back-transaction harness cannot survive.

	cases := []struct{ name, kind string }{{"live local account", "local"}, {"live SAML-created account", "saml"}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newCommittedSAMLEnv(t)
			addr := uniqueAddr("admin-over-live")
			var holder user.User
			var err error
			if tc.kind == "local" {
				holder = e.localUser(t, addr)
			} else {
				holder, err = e.signIn("live-subject", addr, "Live")
				require.NoError(t, err)
			}
			before := e.row(t, holder.ID)

			_, err = e.adminCreate(addr, "Second", nil)
			require.ErrorIs(t, err, user.ErrEmailTaken)
			require.Equal(t, before, e.row(t, holder.ID), "a live row must not be touched")
		})
	}
}

// The unique constraint is on users.email as written, so two rows cannot hold
// one address, deleted or not. This is why "over two deleted rows" has no case:
// the database refuses to make one.
func TestUsersEmail_TwoRowsCannotHoldTheSameAddress(t *testing.T) {
	e := newCommittedSAMLEnv(t)
	addr := uniqueAddr("dup")
	a := e.seedDeleted(t, "local", addr)
	_ = a
	_, err := e.db.ExecContext(e.ctx, `INSERT INTO users (id, email, display_name, role, deleted_at) VALUES ($1, $2, 'Dup', 'user', now())`, uuid.New(), addr)
	require.Error(t, err, "a second row with the same address, even a deleted one, must be refused")
}

// An administrator creating the same address twice at once, and an
// administrator racing a first SSO sign-in: one live account either way, and
// the loser is told "taken" (409), never a 500.
func TestAdminCreate_RacesLeaveOneLiveAccount(t *testing.T) {
	t.Run("two administrators", func(t *testing.T) {
		e := newCommittedSAMLEnv(t)
		addr := uniqueAddr("race-admin")
		old := e.seedDeleted(t, "local", addr)
		ids, errs := e.concurrently(8, func(i int) (user.User, error) { return e.adminCreate(addr, "Racer", nil) })
		require.Len(t, ids, 1, "exactly one create wins")
		for _, err := range errs {
			require.ErrorIs(t, err, user.ErrEmailTaken)
		}
		require.Equal(t, 1, e.count(t, `SELECT count(*) FROM users WHERE email = $1 AND deleted_at IS NULL`, addr))
		require.Equal(t, 1, e.count(t, `SELECT count(*) FROM users WHERE email = $1`, "deleted-"+old.ID.String()+"@invalid"))
	})

	t.Run("administrator and a first SSO sign-in", func(t *testing.T) {
		e := newCommittedSAMLEnv(t)
		addr := uniqueAddr("race-sso")
		old := e.seedDeleted(t, "saml", addr)
		var winners []uuid.UUID
		ids, errs := e.concurrently(8, func(i int) (user.User, error) {
			if i%2 == 0 {
				return e.adminCreate(addr, "Admin Made", nil)
			}
			return e.signIn("sso-"+uuid.NewString(), addr, "SSO Made")
		})
		winners = ids
		for _, err := range errs {
			require.ErrorIs(t, err, user.ErrEmailTaken, "a lost race is a refusal, not a fault")
		}
		require.NotEmpty(t, winners)
		require.Len(t, uniq(winners), 1, "everyone who got in is in the same account")
		require.Equal(t, 1, e.count(t, `SELECT count(*) FROM users WHERE email = $1 AND deleted_at IS NULL`, addr))
		require.Equal(t, 1, e.count(t, `SELECT count(*) FROM users WHERE email = $1`, "deleted-"+old.ID.String()+"@invalid"))
	})
}

// The release frees every identity the old row held that is unique across all
// rows. oidc_subject is (migration 000017, deleted rows included), so a row
// that kept it made a person who could never sign in with OIDC again: adoption
// found the new account by address and hit the unique index.
func TestAdminCreate_OverADeletedOIDCAccountLeavesTheOIDCSubjectFree(t *testing.T) {
	e := newCommittedSAMLEnv(t)
	addr := uniqueAddr("oidc-gone")
	sub := "oidc-sub-" + uuid.NewString()

	first, err := e.svc.UpsertOIDCUser(e.ctx, sub, addr, "Oidc Person")
	require.NoError(t, err)
	require.NoError(t, e.svc.SoftDelete(e.ctx, first.ID))

	created, err := e.svc.CreateByAdmin(e.ctx, user.CreateUserInput{Email: addr, DisplayName: "Oidc Person", Role: user.RoleUser}, nil)
	require.NoError(t, err)
	require.Empty(t, e.row(t, first.ID).OIDCSubject, "the retired row must not keep a subject nobody else can claim")

	again, err := e.svc.UpsertOIDCUser(e.ctx, sub, addr, "Oidc Person")
	require.NoError(t, err, "the person can sign in with OIDC again")
	require.Equal(t, created.ID, again.ID)
}

// To bring a deleted SAML user back, they sign in through SAML. The New user
// form makes a LOCAL account, which SAML refuses to take over (DESIGN.md, SAML
// step 5). Pinned so it is a documented consequence and not a surprise.
func TestAdminCreate_OverADeletedSAMLAccountMakesALocalAccountSAMLRefuses(t *testing.T) {
	e := newCommittedSAMLEnv(t)
	addr := uniqueAddr("saml-gone")
	old, err := e.signIn("p-1", addr, "Person")
	require.NoError(t, err)
	require.NoError(t, e.svc.SoftDelete(e.ctx, old.ID))

	created, err := e.adminCreate(addr, "Person", nil)
	require.NoError(t, err)
	require.Empty(t, e.row(t, created.ID).SAMLSubject, "a local account")

	_, err = e.signIn("p-1", addr, "Person")
	require.ErrorIs(t, err, user.ErrEmailTaken)
}

// Rows written before addresses were lower-cased (v1.2.0) can hold mixed case.
// The unique index is case-sensitive, so it does not stop the form from making
// a second live account beside one: the create looks for that itself.
func TestAdminCreate_LegacyMixedCaseRows(t *testing.T) {
	t.Run("a deleted mixed-case row is released", func(t *testing.T) {
		e := newSAMLEnv(t)
		id := uuid.New()
		e.exec(t, `INSERT INTO users (id, email, display_name, role, deleted_at) VALUES ($1, 'Legacy3@Example.com', 'Old', 'user', now())`, id)

		got, err := e.adminCreate("legacy3@example.com", "New", nil)
		require.NoError(t, err)
		require.NotEqual(t, id, got.ID)
		require.Equal(t, "deleted-"+id.String()+"@invalid", e.row(t, id).Email)
	})

	t.Run("a live mixed-case row is not duplicated", func(t *testing.T) {
		e := newSAMLEnv(t)
		e.exec(t, `INSERT INTO users (id, email, display_name, role) VALUES ($1, 'Legacy4@Example.com', 'Live', 'user')`, uuid.New())

		_, err := e.adminCreate("legacy4@example.com", "Second", nil)
		require.ErrorIs(t, err, user.ErrEmailTaken)
		require.Equal(t, 1, e.count(t, `SELECT count(*) FROM users WHERE lower(email) = 'legacy4@example.com'`))
	})
}
