package database_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

const (
	migration058Up   = "migrations/000058_email_template_html.up.sql"
	migration058Down = "migrations/000058_email_template_html.down.sql"
)

// Migration 000058 adds the optional HTML part to the edited email templates
// (#158). Run verbatim in a rolled-back transaction, like 000057's test.
//
//   - down removes the column, up brings it back;
//   - a row that was there before reads NULL, which means "no HTML part", so
//     upgrading changes no mail;
//   - the column holds text.
func TestMigration058_EmailTemplateHTML(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()
	tx, err := db.SQL.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	hasColumn := func() bool {
		var ok bool
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
			WHERE table_name = 'email_template_overrides' AND column_name = 'html' AND data_type = 'text' AND is_nullable = 'YES')`).Scan(&ok))
		return ok
	}
	require.True(t, hasColumn(), "the migration has run in the test database")

	execMigrationFile(t, ctx, tx, migration058Down, nil)
	require.False(t, hasColumn(), "down removes the column")

	_, err = tx.ExecContext(ctx, `INSERT INTO email_template_overrides (name, subject, body) VALUES ('ticket_created', 's', 'b')`)
	require.NoError(t, err, "a row saved under 000057")
	execMigrationFile(t, ctx, tx, migration058Up, nil)
	require.True(t, hasColumn())

	var html *string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT html FROM email_template_overrides WHERE name = 'ticket_created'`).Scan(&html))
	require.Nil(t, html, "an old row has no HTML part, so its mail stays plain text")

	_, err = tx.ExecContext(ctx, `UPDATE email_template_overrides SET html = '<p>x</p>' WHERE name = 'ticket_created'`)
	require.NoError(t, err)
}
