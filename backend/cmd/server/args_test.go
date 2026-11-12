package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The image's entrypoint is the server binary, so `docker run <image> /app/server
// prepare-reply-search` passes "/app/server" as the first argument. The server
// used to ignore its arguments and start: against a 1.2 database that
// migrated it to 1.3 and stopped 1.2 from starting. A server given arguments it
// does not know must refuse instead.
func TestServerArgsProblem(t *testing.T) {
	ok := [][]string{
		nil,
		{"reset-factors"},
		{"reset-factors", "a@example.com"},
		{"unlock", "a@example.com"},
		{"prepare-reply-search"},
		{"prepare-reply-search", "--maintenance-work-mem", "2GB"},
	}
	for _, args := range ok {
		require.Empty(t, serverArgsProblem(args), "%v", args)
	}
	bad := map[string][]string{
		"the binary repeated":   {"/app/server", "prepare-reply-search"},
		"the binary alone":      {"/app/server"},
		"an unknown word":       {"serve"},
		"a flag":                {"--foo"},
		"a command in capitals": {"Unlock", "a@example.com"},
	}
	for name, args := range bad {
		t.Run(name, func(t *testing.T) {
			msg := serverArgsProblem(args)
			require.NotEmpty(t, msg)
			require.Contains(t, msg, "prepare-reply-search", "it names what is available")
		})
	}
	require.Contains(t, serverArgsProblem([]string{"/app/server", "prepare-reply-search"}), "entrypoint",
		"and says why the repeated binary is wrong")
}
