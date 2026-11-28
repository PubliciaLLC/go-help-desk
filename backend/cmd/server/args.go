package main

import (
	"fmt"
	"strings"
)

// serverSubcommands are the arguments the binary understands. Anything else is
// a mistake, and a server must not start on a mistake.
var serverSubcommands = []string{"reset-factors", "unlock", "prepare-reply-search"}

// serverArgsProblem says what is wrong with the command line, or "" if it is fine.
//
// The binary used to ignore arguments it did not know and start the server. The
// image's entrypoint is already /app/server, so
// `docker run <image> /app/server prepare-reply-search` hands the server
// "/app/server" as its first argument: it started, and against a 1.2 database it
// migrated it to 1.3 and stopped 1.2 from starting again. Refusing is safe
// because nothing legitimate passes the server an argument: it is configured by
// its environment.
func serverArgsProblem(args []string) string {
	if len(args) == 0 {
		return ""
	}
	for _, c := range serverSubcommands {
		if args[0] == c {
			return ""
		}
	}
	msg := fmt.Sprintf("unknown argument %q. The server takes no arguments, and these commands: %s.", args[0], strings.Join(serverSubcommands, ", "))
	if strings.HasPrefix(args[0], "/") {
		msg += " The container's entrypoint is already the server binary: leave /app/server out, for example `docker run --rm <image> prepare-reply-search`."
	}
	return msg
}
