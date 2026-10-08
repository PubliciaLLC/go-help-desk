package database_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/registrationstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/registration"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

type queued struct{ n int }

func (q *queued) Dispatch(context.Context, notification.Event) error { q.n++; return nil }

type noMail struct{}

func (noMail) SendVerificationEmail(string, string, string) error { return nil }

// tableWork is what this transaction has done to the tables signup touches:
// rows read by scans, and rows inserted, updated or deleted.
func tableWork(t *testing.T, tx *sql.Tx) map[string]int64 {
	t.Helper()
	rows, err := tx.Query(`
		SELECT relname,
		       coalesce(seq_scan, 0) + coalesce(idx_scan, 0),
		       n_tup_ins + n_tup_upd + n_tup_del
		FROM pg_stat_xact_user_tables
		WHERE relname IN ('users', 'pending_registrations')`)
	require.NoError(t, err)
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var name string
		var scans, writes int64
		require.NoError(t, rows.Scan(&name, &scans, &writes))
		out[name+" scans"], out[name+" writes"] = scans, writes
	}
	require.NoError(t, rows.Err())
	return out
}

func diff(before, after map[string]int64) map[string]int64 {
	out := map[string]int64{}
	for k, v := range after {
		out[k] = v - before[k]
	}
	return out
}

// #348: signup answers 202 for an address with an account and one without,
// so it cannot be used to learn who has an account — but a fresh address also
// wrote a pending row and dialled the mail server on the request, while a
// taken one returned at once, and the timing said which. The request now does
// the same database work for both, counted here by Postgres itself, and the
// mail is decided at send time.
func TestSignup_FreshAndTakenAddressesDoTheSameDatabaseWork(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, tx, rollback := testutil.TxQueriesTx(t, db)
	defer rollback()
	ctx := context.Background()

	users := user.NewService(userstore.New(q))
	_, err := users.Create(ctx, user.CreateUserInput{
		Email: "taken@signup.test", DisplayName: "Has an account", Role: user.RoleUser, Password: "a-long-enough-password",
	})
	require.NoError(t, err)

	queue := &queued{}
	svc := registration.NewService(registrationstore.New(q), users, noMail{}, "https://desk.example",
		registration.WithQueue(queue))

	register := func(email string) map[string]int64 {
		before := tableWork(t, tx)
		_ = svc.Register(ctx, email, "Someone", nil, true)
		return diff(before, tableWork(t, tx))
	}
	// Warm up, so plan caching and first-use effects are not part of either.
	register("warmup-" + uuid.NewString()[:8] + "@signup.test")

	fresh := register("fresh-" + uuid.NewString()[:8] + "@signup.test")
	taken := register("taken@signup.test")

	require.NotZero(t, fresh["pending_registrations writes"], "statistics are not being counted")
	require.Equal(t, fresh, taken, "a fresh and a taken address did different database work")
	require.Equal(t, 3, queue.n, "every signup, taken or not, queues one event")
}
