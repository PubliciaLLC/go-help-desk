package database_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/outboxstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/passwordresetstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/passwordreset"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/server/notify"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

type noResetMail struct{}

func (noResetMail) SendPasswordResetEmail(string, string, string) error { return nil }

// workOn is what this transaction has done to the named tables: rows read by
// scans, and rows inserted, updated or deleted.
func workOn(t *testing.T, tx *sql.Tx, tables ...string) map[string]int64 {
	t.Helper()
	rows, err := tx.Query(`
		SELECT relname,
		       coalesce(seq_scan, 0) + coalesce(idx_scan, 0),
		       n_tup_ins + n_tup_upd + n_tup_del
		FROM pg_stat_xact_user_tables
		WHERE relname = ANY($1)`, "{"+strings.Join(tables, ",")+"}")
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

// #451, in the style of #348's signup test: "forgot password" answers 202 for
// an address with an account and one without, so it cannot be used to learn who
// has an account. The answer is the same; the work must be too, or the time it
// takes says which. A request does the same database work for both, counted
// here by Postgres itself: it writes one outbox row, and reads and writes
// nothing else. Whether to mail is decided at send time, off the request.
func TestPasswordResetRequest_KnownAndUnknownAddressesDoTheSameDatabaseWork(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, tx, rollback := testutil.TxQueriesTx(t, db)
	defer rollback()
	ctx := context.Background()

	users := user.NewService(userstore.New(q), user.WithBcryptCost(4))
	_, err := users.Create(ctx, user.CreateUserInput{
		Email: "known@reset.test", DisplayName: "Has an account", Role: user.RoleUser, Password: "a-long-enough-password",
	})
	require.NoError(t, err)

	queue := notify.NewOutboxDispatcher(outboxstore.New(q), []string{"email", "webhook", "verification"}, nil)
	svc := passwordreset.NewService(passwordresetstore.New(q), users, noResetMail{}, queue, auditstore.New(q),
		func(context.Context) bool { return false }, "https://desk.example", nil)

	tables := []string{"users", "password_reset_tokens", "notification_outbox", "sessions"}
	request := func(email string) map[string]int64 {
		before := workOn(t, tx, tables...)
		require.NoError(t, svc.Request(ctx, email))
		after := workOn(t, tx, tables...)
		out := map[string]int64{}
		for k, v := range after {
			out[k] = v - before[k]
		}
		return out
	}
	// Warm up, so plan caching and first-use effects are not part of either.
	request("warmup-" + uuid.NewString()[:8] + "@reset.test")

	known := request("known@reset.test")
	unknown := request("unknown-" + uuid.NewString()[:8] + "@reset.test")

	require.NotZero(t, known["notification_outbox writes"], "statistics are not being counted")
	require.Equal(t, known, unknown, "a known and an unknown address did different database work")
	require.Zero(t, known["users scans"], "the request looked an account up")
	require.Zero(t, known["password_reset_tokens writes"], "the request issued a token")
	require.EqualValues(t, 1, known["notification_outbox writes"], "one outbox row per request")
}
