package server

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// #443: Markdown is formatted in a same-origin Web Worker. The policy sets no
// worker-src, so a worker falls under script-src, which must stay 'self'. If
// someone adds a worker-src, or loosens script-src to something that no longer
// admits our own scripts, the editor and renderer stop working without a
// compile error; this says so first.
func TestDefaultCSP_AllowsTheSameOriginMarkdownWorker(t *testing.T) {
	directives := map[string]string{}
	for _, d := range strings.Split(defaultCSP, ";") {
		name, value, _ := strings.Cut(strings.TrimSpace(d), " ")
		directives[name] = value
	}
	require.Equal(t, "'self'", directives["script-src"])
	if v, ok := directives["worker-src"]; ok {
		require.Contains(t, v, "'self'", "worker-src must admit our own worker")
	}
	_, hasChild := directives["child-src"]
	require.False(t, hasChild && !strings.Contains(directives["child-src"], "'self'"))
	// Nothing broader than the page's own origin is needed or allowed.
	require.NotContains(t, defaultCSP, "blob:")
	require.NotContains(t, defaultCSP, "unsafe-eval")
}

// The e2e that runs the production build under the CSP (frontend/e2e-csp)
// serves it with a copy of the policy. Keep the copy honest.
func TestFrontendCSPServerCopiesTheRealPolicy(t *testing.T) {
	src, err := os.ReadFile("../../../frontend/scripts/csp-server.mjs")
	require.NoError(t, err)
	require.Contains(t, string(src), defaultCSP)
}
