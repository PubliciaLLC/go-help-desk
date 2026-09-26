package slastore_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/slastore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// seedTicketAndRecord creates a reporter, a category, a ticket, an SLA policy
// and an SLA record for it, and returns the ticket id — the minimum a real
// sla_records row needs (both its foreign keys), so these tests exercise the
// actual SQL statements, not a reimplementation of them (see CLAUDE.md: "Do
// not mock the DB").
func seedTicketAndRecord(t *testing.T, ctx context.Context, ts *ticketstore.Store, cs *categorystore.Store,
	us *userstore.Store, sls *slastore.Store, responseTargetMin, resolutionTargetMin int) uuid.UUID {
	t.Helper()

	reporter := user.User{
		ID: uuid.New(), Email: "slastore-" + uuid.NewString() + "@test.local",
		DisplayName: "Reporter", Role: user.RoleUser,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, reporter))
	cat := category.Category{ID: uuid.New(), Name: "SLA store " + uuid.NewString()[:8], SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))
	newSt, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)

	tk := ticket.Ticket{
		ID:             uuid.New(),
		TrackingNumber: ticket.TrackingNumber("SS-" + uuid.NewString()[:8]),
		Subject:        "slastore test",
		Description:    "slastore test",
		CategoryID:     cat.ID,
		Priority:       ticket.PriorityMedium,
		StatusID:       newSt.ID,
		ReporterUserID: &reporter.ID,
		CreatedAt:      time.Now().UTC().Add(-4 * time.Hour),
		UpdatedAt:      time.Now().UTC(),
	}
	require.NoError(t, ts.Create(ctx, tk))

	policy := sla.Policy{
		ID: uuid.New(), Name: "SLA store test policy",
		ResponseTargetMin: responseTargetMin, ResolutionTargetMin: resolutionTargetMin,
	}
	require.NoError(t, sls.CreatePolicy(ctx, policy))
	require.NoError(t, sls.CreateRecord(ctx, sla.Record{TicketID: tk.ID, PolicyID: policy.ID}))

	return tk.ID
}

// TestSetResolved_ConcurrentCallsLeaveNoStrayBreachStamp pins #228 against the
// REAL SQL (not a Go reimplementation of its semantics): two near-simultaneous
// SetResolved calls for the same ticket — modelled as two sequential calls,
// exactly as two overlapping transactions committing one after the other
// would land — must not leave a resolution_breached_at stamp that disagrees
// with whichever call's write actually took.
//
// Before #228, SetResolved wrote only resolved_at/resolution_elapsed_at_met_seconds;
// the breach decision was a separate StampSLABreaches statement, decided by
// the CALLER from a record it read before either write landed. The losing
// call here would have computed its own (later, breaching) elapsed reading
// against that stale read and stamped resolution_breached_at with it — a
// stray stamp sitting next to the winning call's genuinely on-time
// resolved_at, impossible to clear afterward (StampSLABreaches never
// overwrites a set column). Folding the decision into SetSLAResolved's own
// statement, guarded on the PRE-UPDATE resolved_at, means the losing call's
// statement changes nothing at all once it finds resolved_at already set.
func TestSetResolved_ConcurrentCallsLeaveNoStrayBreachStamp(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()

	us := userstore.New(q)
	cs := categorystore.New(q)
	ts := ticketstore.New(q)
	sls := slastore.New(q)

	const resolutionTargetMin = 60 // one hour
	ticketID := seedTicketAndRecord(t, ctx, ts, cs, us, sls, 1000, resolutionTargetMin)

	onTimeAt := time.Now().UTC().Truncate(time.Millisecond)
	// The "on time" call: comfortably under the one-hour target.
	require.NoError(t, sls.SetResolved(ctx, ticketID, onTimeAt, 1800, resolutionTargetMin*60))

	// The "losing" near-simultaneous call: a later instant with an elapsed
	// reading that WOULD have breached, had its write been the one to land.
	lateAt := onTimeAt.Add(time.Hour)
	require.NoError(t, sls.SetResolved(ctx, ticketID, lateAt, 5000, resolutionTargetMin*60))

	rec, err := sls.GetRecord(ctx, ticketID)
	require.NoError(t, err)
	require.NotNil(t, rec.ResolvedAt)
	require.True(t, rec.ResolvedAt.Equal(onTimeAt), "the first call's write wins the COALESCE race")
	require.NotNil(t, rec.ResolutionElapsedAtMetSeconds)
	require.Equal(t, int64(1800), *rec.ResolutionElapsedAtMetSeconds, "the winning call's own elapsed reading, not the loser's")
	require.Nil(t, rec.ResolutionBreachedAt,
		"the losing call's own breaching elapsed must never land a stray stamp once its write lost the race")
}

// TestSetResolvedAndFirstResponse_FoldsBothFactsIntoOneStatement pins #233
// against the real SQL: RecordResolved's resolution fact and its #219
// response backfill must commit together, in the same statement, so a
// process death or dropped connection between them (which used to be
// possible when they were SetResolved then SetFirstResponse, two separate
// statements) cannot happen at all — there is no "between" left for it to
// land in.
func TestSetResolvedAndFirstResponse_FoldsBothFactsIntoOneStatement(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()

	us := userstore.New(q)
	cs := categorystore.New(q)
	ts := ticketstore.New(q)
	sls := slastore.New(q)

	const responseTargetMin = 30
	const resolutionTargetMin = 60
	ticketID := seedTicketAndRecord(t, ctx, ts, cs, us, sls, responseTargetMin, resolutionTargetMin)

	resolvedAt := time.Now().UTC().Truncate(time.Millisecond)
	elapsed := int64(20 * 60) // 20 minutes: under both targets

	require.NoError(t, sls.SetResolvedAndFirstResponse(ctx, ticketID, resolvedAt, elapsed,
		resolutionTargetMin*60, responseTargetMin*60))

	rec, err := sls.GetRecord(ctx, ticketID)
	require.NoError(t, err)
	require.NotNil(t, rec.ResolvedAt)
	require.True(t, rec.ResolvedAt.Equal(resolvedAt))
	require.NotNil(t, rec.ResolutionElapsedAtMetSeconds)
	require.Equal(t, elapsed, *rec.ResolutionElapsedAtMetSeconds)
	require.NotNil(t, rec.FirstResponseAt, "#219: the resolution backfills the response in the same statement")
	require.True(t, rec.FirstResponseAt.Equal(resolvedAt))
	require.NotNil(t, rec.ResponseElapsedAtMetSeconds)
	require.Equal(t, elapsed, *rec.ResponseElapsedAtMetSeconds)
	require.Nil(t, rec.ResponseBreachedAt, "on time toward both targets: no breach")
	require.Nil(t, rec.ResolutionBreachedAt)
}

// TestSetResolvedAndFirstResponse_StampsBothBreachesWhenLate is the late
// control: a resolution recorded past both targets must stamp BOTH breach
// columns, in the same call, mirroring what two separate #217/#228-folded
// SetSLAResolved/SetSLAFirstResponse calls would have produced — but as one
// statement instead of two.
func TestSetResolvedAndFirstResponse_StampsBothBreachesWhenLate(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()

	us := userstore.New(q)
	cs := categorystore.New(q)
	ts := ticketstore.New(q)
	sls := slastore.New(q)

	const responseTargetMin = 10
	const resolutionTargetMin = 20
	ticketID := seedTicketAndRecord(t, ctx, ts, cs, us, sls, responseTargetMin, resolutionTargetMin)

	resolvedAt := time.Now().UTC().Truncate(time.Millisecond)
	elapsed := int64(90 * 60) // 90 minutes: past both targets

	require.NoError(t, sls.SetResolvedAndFirstResponse(ctx, ticketID, resolvedAt, elapsed,
		resolutionTargetMin*60, responseTargetMin*60))

	rec, err := sls.GetRecord(ctx, ticketID)
	require.NoError(t, err)
	require.NotNil(t, rec.ResolutionBreachedAt, "the resolution was late")
	require.True(t, rec.ResolutionBreachedAt.Equal(resolvedAt))
	require.NotNil(t, rec.ResponseBreachedAt, "the backfilled response was, by the same instant, also late")
	require.True(t, rec.ResponseBreachedAt.Equal(resolvedAt))
}

// TestSetResolvedAndFirstResponse_LeavesAnEarlierGenuineResponseUntouched
// proves the two column pairs are decided independently within the one
// statement: a ticket that already has a real, earlier first_response_at
// (and whatever breach decision was already made for it) must keep BOTH
// completely unchanged — only the resolution pair is written.
func TestSetResolvedAndFirstResponse_LeavesAnEarlierGenuineResponseUntouched(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()

	us := userstore.New(q)
	cs := categorystore.New(q)
	ts := ticketstore.New(q)
	sls := slastore.New(q)

	const responseTargetMin = 30
	const resolutionTargetMin = 60
	ticketID := seedTicketAndRecord(t, ctx, ts, cs, us, sls, responseTargetMin, resolutionTargetMin)

	// A genuine earlier reply, on time.
	firstResponseAt := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, sls.SetFirstResponse(ctx, ticketID, firstResponseAt, int64(10*60), responseTargetMin*60))

	// The resolution comes later, past the resolution target.
	resolvedAt := firstResponseAt.Add(90 * time.Minute)
	elapsed := int64(90 * 60)
	require.NoError(t, sls.SetResolvedAndFirstResponse(ctx, ticketID, resolvedAt, elapsed,
		resolutionTargetMin*60, responseTargetMin*60))

	rec, err := sls.GetRecord(ctx, ticketID)
	require.NoError(t, err)
	require.True(t, rec.FirstResponseAt.Equal(firstResponseAt),
		"the real earlier response must not be overwritten by the resolution's own instant")
	require.Equal(t, int64(10*60), *rec.ResponseElapsedAtMetSeconds,
		"the real response's own frozen elapsed reading must survive untouched")
	require.Nil(t, rec.ResponseBreachedAt, "the real response was on time and must still read as such")
	require.NotNil(t, rec.ResolutionBreachedAt, "the resolution itself was late and must still be stamped")
	require.True(t, rec.ResolutionBreachedAt.Equal(resolvedAt))
}

// TestSetFirstResponse_StampsBreachAtomicallyWithTheFact pins the other half
// of #228 against the real SQL: a late first response must land its breach
// stamp in the SAME statement as the fact, so there is no window between them
// for a separate StampSLABreaches call to fail in and lose the signal.
func TestSetFirstResponse_StampsBreachAtomicallyWithTheFact(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()

	us := userstore.New(q)
	cs := categorystore.New(q)
	ts := ticketstore.New(q)
	sls := slastore.New(q)

	const responseTargetMin = 30
	ticketID := seedTicketAndRecord(t, ctx, ts, cs, us, sls, responseTargetMin, 1000)

	late := time.Now().UTC().Truncate(time.Millisecond)
	lateElapsed := int64(90 * 60) // 90 minutes: past the 30-minute target

	require.NoError(t, sls.SetFirstResponse(ctx, ticketID, late, lateElapsed, responseTargetMin*60))

	rec, err := sls.GetRecord(ctx, ticketID)
	require.NoError(t, err)
	require.NotNil(t, rec.FirstResponseAt)
	require.True(t, rec.FirstResponseAt.Equal(late))
	require.NotNil(t, rec.ResponseElapsedAtMetSeconds)
	require.Equal(t, lateElapsed, *rec.ResponseElapsedAtMetSeconds)
	require.NotNil(t, rec.ResponseBreachedAt, "a late response must be stamped in the very same call that records it")
	require.True(t, rec.ResponseBreachedAt.Equal(late))
}

// TestSetFirstResponse_OnTimeDoesNotStampABreach is the control: an on-time
// response must not touch response_breached_at at all.
func TestSetFirstResponse_OnTimeDoesNotStampABreach(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()

	us := userstore.New(q)
	cs := categorystore.New(q)
	ts := ticketstore.New(q)
	sls := slastore.New(q)

	const responseTargetMin = 30
	ticketID := seedTicketAndRecord(t, ctx, ts, cs, us, sls, responseTargetMin, 1000)

	onTime := time.Now().UTC().Truncate(time.Millisecond)
	onTimeElapsed := int64(10 * 60) // 10 minutes: under the 30-minute target

	require.NoError(t, sls.SetFirstResponse(ctx, ticketID, onTime, onTimeElapsed, responseTargetMin*60))

	rec, err := sls.GetRecord(ctx, ticketID)
	require.NoError(t, err)
	require.Nil(t, rec.ResponseBreachedAt)
}
