package database_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// seedAuditActor creates an account with the given role and name. The email
// carries a random part, so a long-lived test database never collides.
func seedAuditActor(t *testing.T, ctx context.Context, us *userstore.Store, role user.Role, display, emailLocal string) uuid.UUID {
	t.Helper()
	u := user.User{
		ID: uuid.New(), Email: emailLocal + "-" + uuid.NewString()[:8] + "@test.local",
		DisplayName: display, Role: role,
		CreatedAt: time.Now().UTC().Truncate(time.Millisecond), UpdatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
	require.NoError(t, us.Create(ctx, u))
	return u.ID
}

// inDetailsRange adds the date range inside which q looks in the change details
// (audit.Filter.SearchesDetails), around entries written a moment ago.
func inDetailsRange(f audit.Filter) audit.Filter {
	from, to := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	f.From, f.To = &from, &to
	return f
}

// #629(a): q looks inside the change details, but only at what audit.Redact
// would show. Every case is scoped to one actor, so rows this database already
// holds cannot change the answer.
func TestAuditStore_Search_QReachesShownDiffValuesOnly(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, tx, rollback := testutil.TxQueriesTx(t, db)
	defer rollback()
	ctx := context.Background()
	aus := auditstore.New(q)
	us := userstore.New(q)

	actor := seedAuditActor(t, ctx, us, user.RoleStaff, "Plain Actor", "plain")
	write := func(action string, before, after map[string]any) {
		require.NoError(t, aus.Create(ctx, audit.Entry{
			ID: uuid.New(), ActorID: &actor, EntityType: "widget", EntityID: uuid.New(), Action: action,
			Before: before, After: after,
		}))
	}
	write("shown_after", nil, map[string]any{"scope_path": "Hardware › Laptop"})
	write("shown_before", map[string]any{"field_name": "Cost centre"}, nil)
	write("key_spelled_differently", nil, map[string]any{"Scope-Path": "Network › Switch"})
	write("hidden", nil, map[string]any{
		// Ticket text, a secret-looking key, a container and a key the allow-list
		// does not name: none may be reachable by search.
		"subject": "Payroll breach", "api_key": "sk-live-123", "password_hash": "zzhash",
		"nested": map[string]any{"scope_path": "Buried"}, "scope_path_extra": "Almost", "scopé_path": "Accent",
		// A shown key holding a container: Redact leaves it out, so search must too.
		"field_name": []any{"Listed"}, "option_value": map[string]any{"x": "Mapped"},
	})
	write("scalar_types", nil, map[string]any{"sort_order": 4242, "active": true, "scope_path": nil})

	hits := func(text string) []string {
		pg, err := aus.Search(ctx, inDetailsRange(audit.Filter{ActorID: &actor, Q: text}), 50, 0)
		require.NoError(t, err)
		require.Equal(t, len(pg.Entries), pg.Total, "the count describes the page")
		var actions []string
		for _, e := range pg.Entries {
			actions = append(actions, e.Action)
		}
		return actions
	}

	require.ElementsMatch(t, []string{"shown_after"}, hits("laptop"), "a shown value, case-insensitively")
	require.ElementsMatch(t, []string{"shown_before"}, hits("COST CEN"), "the before side is searched too")
	// Keys are looked up by the spelling the writers use. Redact would show
	// "Scope-Path" too, but a search that finds less than the view shows is
	// safe, and exact spellings are what make the search affordable.
	require.Empty(t, hits("switch"), "an unusual spelling of a shown key is not searched")

	// A diff that is itself a list is not searched either: Redact shows nothing
	// of it.
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_log (id, actor_id, entity_type, entity_id, action, after)
		VALUES ($1, $2, 'widget', $3, 'list_diff', '[{"scope_path": "Inlist"}]')`, uuid.New(), actor, uuid.New())
	require.NoError(t, err)
	require.Empty(t, hits("Inlist"))

	for _, hidden := range []string{"Payroll", "sk-live", "zzhash", "Buried", "Listed", "Mapped", "Almost", "Accent"} {
		require.Empty(t, hits(hidden), "%q is in an entry but Redact would not show it, so a search must not find it", hidden)
	}
}

// The same rule stated once for every key the view can show, and once for the
// values Redact hides: whatever Redact leaves out of an entry, search leaves
// out too. A hit on a value the view does not render is an oracle.
func TestAuditStore_Search_ParityWithRedact(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()
	aus := auditstore.New(q)
	us := userstore.New(q)

	actor := seedAuditActor(t, ctx, us, user.RoleStaff, "Parity Actor", "parity")
	cases := map[string]map[string]any{}
	for i, k := range audit.SearchableKeys() {
		cases[fmt.Sprintf("shown%d", i)] = map[string]any{k: fmt.Sprintf("zzneedle%d", i)}
	}
	hiddenKeys := []string{"subject", "body", "name", "value", "title", "description", "email", "display_name",
		"password", "password_hash", "token", "secret", "api_key", "smtp_pass", "recovery_codes"}
	for i, k := range hiddenKeys {
		cases[fmt.Sprintf("hidden%d", i)] = map[string]any{k: fmt.Sprintf("zzneedle-h%d", i)}
	}
	for name, after := range cases {
		require.NoError(t, aus.Create(ctx, audit.Entry{
			ID: uuid.New(), ActorID: &actor, EntityType: "parity", EntityID: uuid.New(), Action: name, After: after,
		}))
	}

	for name, after := range cases {
		_, shown := audit.Redact(nil, after)
		// What Redact renders as the value itself, not a placeholder.
		visible := false
		for k, v := range after {
			if shown[k] == v {
				visible = true
			}
		}
		needle := fmt.Sprint(firstValue(after))
		pg, err := aus.Search(ctx, inDetailsRange(audit.Filter{ActorID: &actor, Q: needle}), 200, 0)
		require.NoError(t, err)
		found := false
		for _, e := range pg.Entries {
			if e.Action == name {
				found = true
			}
		}
		require.Equal(t, visible, found, "%s %v: found=%v but Redact shows it=%v", name, after, found, visible)
	}
}

func firstValue(m map[string]any) any {
	for _, v := range m {
		return v
	}
	return nil
}

// "%" and "_" in q are text, not wildcards (#629).
func TestAuditStore_Search_QWildcardsAreLiteral(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()
	aus := auditstore.New(q)
	us := userstore.New(q)

	actor := seedAuditActor(t, ctx, us, user.RoleStaff, "Wild Actor", "wild")
	for _, a := range []string{"disk_full", "diskXfull", "ten_percent", "100% done", "100 done", `back\slash`} {
		require.NoError(t, aus.Create(ctx, audit.Entry{
			ID: uuid.New(), ActorID: &actor, EntityType: "wild", EntityID: uuid.New(), Action: a,
			After: map[string]any{"scope_path": a},
		}))
	}
	hits := func(text string) int {
		pg, err := aus.Search(ctx, audit.Filter{ActorID: &actor, Q: text}, 50, 0)
		require.NoError(t, err)
		return pg.Total
	}
	require.Equal(t, 1, hits("disk_full"), "an underscore is not 'any one character'")
	require.Equal(t, 1, hits("100%"), "a percent sign is not 'anything'")
	require.Equal(t, 1, hits(`back\slash`), "a backslash is not an escape")
	require.Equal(t, 0, hits("%done%"), "the pattern is the whole text, wildcards included")
}

// #629(b), D34: q also matches the actor's display name and email, and leaves
// requesters out while the mask is on, so the search is not a way round it.
func TestAuditStore_Search_QMatchesActorNameAndEmail(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()
	aus := auditstore.New(q)
	us := userstore.New(q)

	tag := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	staff := seedAuditActor(t, ctx, us, user.RoleStaff, "Sam "+tag+" Staffer", "sam-"+tag)
	admin := seedAuditActor(t, ctx, us, user.RoleAdmin, "Ada Admin", "ada-"+tag)
	requester := seedAuditActor(t, ctx, us, user.RoleUser, "Rae "+tag+" Requester", "rae-"+tag)
	gone := seedAuditActor(t, ctx, us, user.RoleStaff, "Gil "+tag+" Gone", "gil-"+tag)
	for _, a := range []uuid.UUID{staff, admin, requester, gone} {
		a := a
		require.NoError(t, aus.Create(ctx, audit.Entry{
			ID: uuid.New(), ActorID: &a, EntityType: "ticket", EntityID: uuid.New(), Action: "created",
		}))
	}
	// A system entry has no actor and matches no name.
	require.NoError(t, aus.Create(ctx, audit.Entry{ID: uuid.New(), EntityType: "ticket", EntityID: uuid.New(), Action: "created"}))
	require.NoError(t, us.SoftDelete(ctx, gone))

	actors := func(f audit.Filter) []uuid.UUID {
		pg, err := aus.Search(ctx, f, 50, 0)
		require.NoError(t, err)
		require.Equal(t, len(pg.Entries), pg.Total)
		var out []uuid.UUID
		for _, e := range pg.Entries {
			out = append(out, *e.ActorID)
		}
		return out
	}

	require.ElementsMatch(t, []uuid.UUID{staff, requester}, actors(audit.Filter{Q: " " + tag + " "}),
		"the display name matches, whatever the role")
	require.ElementsMatch(t, []uuid.UUID{staff}, actors(audit.Filter{Q: "sam-" + tag}), "the email matches")
	require.ElementsMatch(t, []uuid.UUID{requester}, actors(audit.Filter{Q: "RAE-" + tag}), "case-insensitively")
	require.ElementsMatch(t, []uuid.UUID{staff}, actors(audit.Filter{Q: " " + tag + " ", MaskRequesterActors: true}),
		"with the mask on, a requester is not found by name")
	require.Empty(t, actors(audit.Filter{Q: "rae-" + tag, MaskRequesterActors: true}), "nor by email")
	require.ElementsMatch(t, []uuid.UUID{admin}, actors(audit.Filter{Q: "ada-" + tag, MaskRequesterActors: true}),
		"staff and administrators are still found with the mask on")
	require.Empty(t, actors(audit.Filter{Q: "gil-" + tag}),
		"a deleted account's name is not shown, so it is not searchable")
}

// The mask only narrows the name match. A requester's entry is still found by
// what it says (entity, action, change), and the actor_id filter is untouched.
func TestAuditStore_Search_MaskLeavesTheRestOfQAlone(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()
	aus := auditstore.New(q)
	us := userstore.New(q)

	requester := seedAuditActor(t, ctx, us, user.RoleUser, "Quinn Requester", "quinn")
	require.NoError(t, aus.Create(ctx, audit.Entry{
		ID: uuid.New(), ActorID: &requester, EntityType: "ticket", EntityID: uuid.New(), Action: "zz_requester_action",
		After: map[string]any{"scope_path": "zz-requester-path"},
	}))
	for _, text := range []string{"zz_requester_action", "zz-requester-path"} {
		pg, err := aus.Search(ctx, inDetailsRange(audit.Filter{ActorID: &requester, Q: text, MaskRequesterActors: true}), 10, 0)
		require.NoError(t, err)
		require.Equal(t, 1, pg.Total, text)
	}
}

// q is matched as text in the change details: a regular-expression character in
// it is a character, not a pattern. The details are searched with a jsonpath
// like_regex, where the "q" flag is what makes the text literal; without it
// "a.c" would find "abc" and "(" would be a syntax error.
func TestAuditStore_Search_QIsLiteralInChangeDetails(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()
	aus := auditstore.New(q)
	us := userstore.New(q)

	actor := seedAuditActor(t, ctx, us, user.RoleStaff, "Literal Actor", "literal")
	for action, path := range map[string]string{
		"dot_plain": "zzabc", "dot_literal": "zza.c", "paren": "zz(a|b)", "star": "zzaaa", "star_literal": "zza*",
		"anchor": "zz^start", "class_literal": "zz[ab]", "class_plain": "zza",
	} {
		require.NoError(t, aus.Create(ctx, audit.Entry{
			ID: uuid.New(), ActorID: &actor, EntityType: "literal", EntityID: uuid.New(), Action: action,
			After: map[string]any{"scope_path": path},
		}))
	}
	hits := func(text string) []string {
		pg, err := aus.Search(ctx, inDetailsRange(audit.Filter{ActorID: &actor, Q: text}), 50, 0)
		require.NoError(t, err, text)
		var actions []string
		for _, e := range pg.Entries {
			actions = append(actions, e.Action)
		}
		return actions
	}

	require.ElementsMatch(t, []string{"dot_literal"}, hits("zza.c"), "a dot is a dot, not any character")
	require.ElementsMatch(t, []string{"paren"}, hits("zz(a|b)"), "parentheses and a bar are text")
	require.ElementsMatch(t, []string{"star_literal"}, hits("zza*"), "a star is a star, not a repeat")
	require.ElementsMatch(t, []string{"anchor"}, hits("zz^start"), "a caret is a caret, not an anchor")
	require.ElementsMatch(t, []string{"class_literal"}, hits("zz[ab]"), "brackets are text, not a character class")
}
