package server_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// #543: /setup counts every row in users, so a bare harness that shares its
// database with a neighbour is only as empty as the neighbour leaves it. The
// database package's tests commit real users to the shared database (their
// writers need separate connections, so a rolled-back transaction would hide
// the row), and `go test ./...` runs that package beside this one. A setup test
// that landed in the window answered 409.
//
// This does what a neighbour does, deterministically: it commits a user to the
// shared database first. A bare harness has to be unaffected.
func TestSetup_IgnoresUsersCommittedToTheSharedDatabase(t *testing.T) {
	shared, closeShared := testutil.NewDB(t)
	defer closeShared()
	ctx := context.Background()

	neighbour := user.User{
		ID: uuid.New(), Email: "neighbour-" + uuid.NewString() + "@test.local",
		DisplayName: "Neighbour", Role: user.RoleUser,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, userstore.New(shared.Queries).Create(ctx, neighbour))
	defer func() { _, _ = shared.SQL.Exec(`DELETE FROM users WHERE id = $1`, neighbour.ID) }()

	h, cleanup := newBareHarness(t)
	defer cleanup()

	resp := h.doUnauth(t, http.MethodPost, "/api/v1/setup", map[string]any{
		"email":        "admin@example.com",
		"display_name": "Admin",
		"password":     "correct-horse-battery",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode,
		"setup answered as if the instance had users, because another test committed one to the shared database")
}
