package auditstore

import "github.com/publiciallc/go-help-desk/backend/internal/dbgen"

// NewWithPurgeBatchForTest is New with a smaller purge batch, so a test can
// see several batches without writing thousands of rows. It lives here, in a
// test file, because the external test package that needs testutil cannot reach
// the unexported field (testutil imports this package).
func NewWithPurgeBatchForTest(q *dbgen.Queries, batch int32) *Store {
	s := New(q)
	s.purgeBatch = batch
	return s
}

// NewWithDiffWindowForTest is New with a smaller change-details search window,
// for the same reason.
func NewWithDiffWindowForTest(q *dbgen.Queries, window int32) *Store {
	s := New(q)
	s.diffWindow = window
	return s
}
