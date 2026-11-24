package ticketstore

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The dashboard counts with SQL that sqlc cannot share with the list, so the
// text is copied and the copies are pinned here (DESIGN.md, Dashboard: "The list
// query's WHERE is the one rule"). This needs no database: it reads the file.

const (
	visibilityBegin = "-- visibility: begin"
	visibilityEnd   = "-- visibility: end"
)

var spaces = regexp.MustCompile(`\s+`)

// bare drops every comment and collapses whitespace, so two statements compare
// by what they say and not by how they are laid out or what they remark on.
func bare(sql string) string {
	var b strings.Builder
	for _, line := range strings.Split(sql, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	s := strings.TrimSpace(spaces.ReplaceAllString(b.String(), " "))
	return strings.TrimSpace(strings.TrimSuffix(s, ";"))
}

// queryNamed is the text of one named query, from its "-- name:" line to the
// next one.
func queryNamed(src, name string) (string, bool) {
	marker := "-- name: " + name + "\n"
	start := strings.Index(src, marker)
	if start < 0 {
		return "", false
	}
	rest := src[start+len(marker):]
	if end := strings.Index(rest, "\n-- name: "); end >= 0 {
		rest = rest[:end]
	}
	return rest, true
}

// visibilityBlocks is every block between the two marker lines, in file order.
// An unclosed or unopened marker is an error: a half-marked block would be
// compared as less than it is.
func visibilityBlocks(src string) ([]string, error) {
	var (
		out  []string
		cur  []string
		open bool
	)
	for _, line := range strings.Split(src, "\n") {
		switch strings.TrimSpace(line) {
		case visibilityBegin:
			if open {
				return nil, errString("a visibility block begins inside another")
			}
			open, cur = true, nil
		case visibilityEnd:
			if !open {
				return nil, errString("a visibility block ends without beginning")
			}
			open = false
			out = append(out, bare(strings.Join(cur, "\n")))
		default:
			if open {
				cur = append(cur, line)
			}
		}
	}
	if open {
		return nil, errString("a visibility block never ends")
	}
	return out, nil
}

type errString string

func (e errString) Error() string { return string(e) }

// checkDashboardSQL says why src breaks the rule, or returns nil.
func checkDashboardSQL(src string) error {
	list, ok := queryNamed(src, "ListTicketsFiltered :many")
	if !ok {
		return errString("ListTicketsFiltered is missing")
	}
	count, ok := queryNamed(src, "CountTicketsFiltered :one")
	if !ok {
		return errString("CountTicketsFiltered is missing")
	}

	// The select list is replaced by count(*) and ORDER BY to the end is gone.
	const sel, from, order = "SELECT t.id, ", " FROM tickets t ", " ORDER BY CASE WHEN sqlc.arg(sort_key)"
	l := bare(list)
	i := strings.Index(l, sel)
	j := strings.Index(l, from)
	k := strings.Index(l, order)
	if i < 0 || j < i || k < j {
		return errString("ListTicketsFiltered no longer has the shape this test reads (select list, FROM tickets t, ORDER BY)")
	}
	want := l[:i] + "SELECT count(*)" + l[j:k]
	if got := bare(count); got != want {
		return errString("CountTicketsFiltered is not ListTicketsFiltered with count(*) and no ORDER BY/LIMIT:\n got: " + got + "\nwant: " + want)
	}

	blocks, err := visibilityBlocks(src)
	if err != nil {
		return err
	}
	// The list carries the rule; every other block must be the same text.
	listBlocks, err := visibilityBlocks(list)
	if err != nil {
		return err
	}
	if len(listBlocks) != 1 {
		return errString("ListTicketsFiltered must have exactly one visibility block")
	}
	for n, b := range blocks {
		if b != listBlocks[0] {
			return fmt.Errorf("visibility block %d differs from ListTicketsFiltered's", n+1)
		}
	}
	return nil
}

func TestSQL_DashboardQueriesShareTheListRule(t *testing.T) {
	path := filepath.Join("..", "..", "..", "queries", "tickets.sql")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	src := string(raw)

	require.NoError(t, checkDashboardSQL(src))

	// The check is only worth having if it fails when a copy drifts.
	list, _ := queryNamed(src, "ListTicketsFiltered :many")
	count, _ := queryNamed(src, "CountTicketsFiltered :one")
	blocks, err := visibilityBlocks(list)
	require.NoError(t, err)
	require.Len(t, blocks, 1)

	drift := []struct {
		name   string
		mutate func(src string) string
	}{
		{"a filter changed in the count copy", func(s string) string {
			return strings.Replace(s, count, strings.Replace(count, "t.priority = ANY", "t.priority <> ANY", 1), 1)
		}},
		{"a filter dropped from the count copy", func(s string) string {
			return strings.Replace(s, count, strings.Replace(count, "AND (sqlc.arg(sla)::text = ''", "AND (TRUE OR sqlc.arg(sla)::text = ''", 1), 1)
		}},
		{"a filter changed in the list", func(s string) string {
			return strings.Replace(s, list, strings.Replace(list, "t.updated_at < sqlc", "t.updated_at <= sqlc", 1), 1)
		}},
		{"the list's visibility changed by one character", func(s string) string {
			return strings.Replace(s, list, strings.Replace(list, "AND t.reporter_user_id = sqlc.arg(actor_id)::uuid", "AND t.reporter_user_id <> sqlc.arg(actor_id)::uuid", 1), 1)
		}},
		{"the count copy's visibility changed by one character", func(s string) string {
			return strings.Replace(s, count, strings.Replace(count, "AND t.reporter_user_id = sqlc.arg(actor_id)::uuid", "AND t.reporter_user_id <> sqlc.arg(actor_id)::uuid", 1), 1)
		}},
		{"a second visibility block that differs", func(s string) string {
			return s + "\n-- name: Extra :one\n-- visibility: begin\nSELECT 1\n-- visibility: end\n"
		}},
		{"a visibility block that never ends", func(s string) string {
			return strings.Replace(s, visibilityEnd, "-- visibility: over", 1)
		}},
	}
	for _, tc := range drift {
		t.Run(tc.name, func(t *testing.T) {
			mutated := tc.mutate(src)
			require.NotEqual(t, src, mutated, "the mutation must change the file")
			require.Error(t, checkDashboardSQL(mutated))
		})
	}
}
