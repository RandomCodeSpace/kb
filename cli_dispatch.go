package main

import (
	"os"

	"github.com/RandomCodeSpace/kb/internal/cliapp"
)

// The kb task CLI (internal/cliapp) registers its verbs in the dispatch
// table (dispatch.go), so `kb add|list|update|move|done|rm|help` runs the
// CLI and anything else serves as before. Each verb exits with cliapp.Run's
// code directly to preserve the CLI's 0/1/2 (ok/runtime error/usage error)
// exit-code contract; cliapp writes its own error output.
func init() {
	for _, cmd := range []string{"add", "list", "update", "move", "done", "rm", "help"} {
		subcommands[cmd] = func(args []string) error {
			os.Exit(cliapp.Run(append([]string{cmd}, args...), os.Stdout, os.Stderr))
			return nil // unreachable
		}
	}
}
