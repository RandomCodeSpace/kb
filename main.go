// Command kb opens the local terminal UI by default. Task CLI and MCP
// subcommands use the same SQLite store directly.
package main

import (
	"errors"
	"fmt"
	"log"
	"os"
)

var (
	runMainRoot = runRoot
	fatalLog    = log.Fatal
)

// rootUsageText is what `kb --help` and a non-interactive bare `kb` print.
// Every registered subcommand must appear here.
const rootUsageText = `usage: kb                    open the local board in the terminal
       kb <command> [args]   work with the local board

commands:
  add, list, view, update, move, done, cancel, restore, rm, users,
  project, comment, link, unlink
             the task CLI - run "kb help" for the full reference
  mcp        expose the local board to AI agents over MCP stdio
  tui        open the local board in a full-screen terminal UI
  version    print the kb version
  help       task CLI reference
`

func main() {
	if dispatch() {
		return
	}
	if err := runMainRoot(os.Args[1:]); err != nil {
		var usageErr *rootUsageError
		if errors.As(err, &usageErr) {
			fmt.Fprintf(rootStderr, "kb: %v\n", err)
			exitProcess(2)
			return
		}
		fatalLog(err)
	}
}
