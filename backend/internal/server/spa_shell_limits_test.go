package server_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/siteicon"
	"github.com/publiciallc/go-help-desk/backend/internal/server"
)

// A database that has stopped answering must not hold the page. The shell's
// reads run on every load, bots' included, and are not cached: without a time
// limit each one waits for the server's write timeout and then fails.

type blockingIconStore struct{ siteicon.Store }

func (blockingIconStore) Get(ctx context.Context) (siteicon.Icon, bool, error) {
	<-ctx.Done()
	return siteicon.Icon{}, false, ctx.Err()
}

// blockingAdminStore answers every settings read by waiting for its context.
type blockingAdminStore struct{ admin.Store }

func (blockingAdminStore) Get(ctx context.Context, _ string) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

type failingAdminStore struct{ admin.Store }

func (failingAdminStore) Get(context.Context, string) ([]byte, error) {
	return nil, errors.New("injected settings read failure")
}

type unsetAdminStore struct{ admin.Store }

func (unsetAdminStore) Get(_ context.Context, key string) ([]byte, error) {
	return nil, fmt.Errorf("getting setting %q: %w", key, admin.ErrSettingNotFound)
}

type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func captureLogs(t *testing.T) *lockedBuffer {
	t.Helper()
	buf := &lockedBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

func TestShell_ReadsThatNeverReturnAreCutOff(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	logs := captureLogs(t)

	server.WithSiteIcons(blockingIconStore{})(h.srv)
	h.srv.SetAdminServiceForTest(admin.NewService(blockingAdminStore{}))

	start := time.Now()
	_, body := getShell(server.NewSPAHandler(shellFS(shellFixture), h.srv.ShellHead), "/")
	took := time.Since(start)

	require.Equal(t, shellWith(defaultBlock), body, "the page is served with the built-in icon and name")
	require.Less(t, took, 3*time.Second, "the reads were given up on, not waited for")
	require.Contains(t, logs.String(), "level=WARN", "the fault is logged, not swallowed")
	require.Contains(t, logs.String(), "reading the site names")
	require.Contains(t, logs.String(), "reading the site icon")
}

// A name that cannot be read is logged too, not only the icon.
func TestShell_NameReadFailureIsLogged(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	logs := captureLogs(t)
	h.srv.SetAdminServiceForTest(admin.NewService(failingAdminStore{}))
	require.Equal(t, defaultBlock, h.srv.ShellHead(context.Background()))
	require.Contains(t, logs.String(), "level=WARN")
	require.Contains(t, logs.String(), "injected settings read failure")

	// Unset is not a fault, and is not logged.
	h.srv.SetAdminServiceForTest(admin.NewService(unsetAdminStore{}))
	logs = captureLogs(t)
	require.Equal(t, defaultBlock, h.srv.ShellHead(context.Background()))
	require.NotContains(t, logs.String(), "WARN")
}
