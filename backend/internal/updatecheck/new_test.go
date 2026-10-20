package updatecheck

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// The production wiring points at GitHub's API and sends the running version.
// Nothing here makes a request.
func TestNew_IsWiredToTheRealEndpoint(t *testing.T) {
	c := New(admin.NewService(&memStore{m: map[string][]byte{}}), slog.Default())
	require.Equal(t, ReleasesURL, c.URL)
	require.Equal(t, DefaultTimeout, c.Client.Timeout)
	require.NotNil(t, c.Now)
}
