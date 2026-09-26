package database_test

import "strings"

// splitSQLStatements splits SQL source on ';' statement boundaries, treating
// text between a matching pair of '$$' dollar-quote markers as atomic — so a
// DO $$ ... END $$; block's internal semicolons are never mistaken for
// statement boundaries. Plain migration files with no dollar-quoted blocks
// split exactly as a naive strings.Split(s, ";") would.
//
// This mirrors how the real migration runner (golang-migrate) sees a
// migration file: it execs the whole file as one string, which Postgres'
// own parser reads correctly regardless of what is inside a dollar-quoted
// body. These tests instead run a migration's statements one at a time
// (skipping the ones that already applied when the test database's schema
// was built, or asserting on one statement's effect in isolation), so they
// need their own splitter — and it has to agree with Postgres about where a
// statement actually ends.
func splitSQLStatements(s string) []string {
	var stmts []string
	var cur strings.Builder
	inDollar := false
	for i := 0; i < len(s); i++ {
		if s[i] == '$' && i+1 < len(s) && s[i+1] == '$' {
			inDollar = !inDollar
			cur.WriteByte('$')
			cur.WriteByte('$')
			i++
			continue
		}
		if s[i] == ';' && !inDollar {
			stmts = append(stmts, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteByte(s[i])
	}
	if strings.TrimSpace(cur.String()) != "" {
		stmts = append(stmts, cur.String())
	}
	return stmts
}
