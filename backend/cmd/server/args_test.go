package main

import (
	"os"
	"os/exec"
	"strings"
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

// The function is tested above; this tests that main() calls it, which is what
// keeps a stray argument from starting a server. The test binary runs main()
// itself, as a child process with the arguments in an environment variable and
// nothing else in its environment: with the guard in place the child exits 2 with
// the message; without it main() goes on to start the server, which fails on its
// missing configuration and exits 1.
func TestMainRefusesAStrayArgument(t *testing.T) {
	if args := os.Getenv("GHD_TEST_MAIN_ARGS"); args != "" {
		os.Args = append([]string{"server"}, strings.Fields(args)...)
		main()
		return // not reached: main exits
	}
	for name, args := range map[string]string{
		"the binary repeated": "/app/server prepare-reply-search",
		"an unknown word":     "serve",
	} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestMainRefusesAStrayArgument$")
			cmd.Env = []string{"GHD_TEST_MAIN_ARGS=" + args} // no DATABASE_URL, no secrets
			out, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			require.ErrorAs(t, err, &exit, "output: %s", out)
			require.Equal(t, 2, exit.ExitCode(), "output: %s", out)
			require.Contains(t, string(out), "unknown argument")
		})
	}
}
