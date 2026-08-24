package main

import (
	"os"

	"github.com/RandomCodeSpace/kb/internal/cliapp"
)

// The kb task CLI (internal/cliapp) registers its verbs in the dispatch
// table (dispatch.go), so `kb add|list|update|move|done|cancel|restore|rm|help`
// runs the CLI while the bare command opens the TUI. Each verb exits with
// cliapp.Run's code directly to preserve the CLI's 0/1/2 (ok/runtime
// error/usage error) exit-code contract; cliapp writes its own error output.
//
// cliVerbs must list every verb cliapp.Run dispatches; a verb missing here is
// unreachable from the binary however well it is implemented and documented.
// TestEveryCLIVerbIsDispatched pins the two lists together.
var cliVerbs = []string{"add", "list", "view", "update", "move", "done", "cancel", "restore", "rm", "users", "project", "comment", "link", "unlink", "help"}

func init() {
	for _, cmd := range cliVerbs {
		subcommands[cmd] = func(args []string) error {
			exitProcess(cliapp.Run(append([]string{cmd}, args...), os.Stdout, os.Stderr))
			return nil // unreachable
		}
	}
}
