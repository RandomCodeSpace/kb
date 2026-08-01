package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/RandomCodeSpace/kb/internal/mcpserv"
)

// subcommands is the kb subcommand dispatch table. A bare invocation (or
// flags only) runs the web server; agents adding CLI verbs register here.
var subcommands = map[string]func(args []string) error{
	"mcp": runMCP,
}

var mcpRun = mcpserv.Run
var exitProcess = os.Exit
var fatalLogf = log.Fatalf

// dispatch runs os.Args[1] as a subcommand when one is named, reporting
// whether it handled the invocation. Unknown subcommands exit with an error
// so typos never silently start the web server.
func dispatch() bool {
	handled, code := dispatchArgs(os.Args[1:], os.Stderr)
	if code != 0 {
		exitProcess(code)
	}
	return handled
}

func dispatchArgs(args []string, stderr io.Writer) (handled bool, exitCode int) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return false, 0
	}
	cmd := args[0]
	fn, ok := subcommands[cmd]
	if !ok {
		names := make([]string, 0, len(subcommands))
		for name := range subcommands {
			names = append(names, name)
		}
		sort.Strings(names)
		fmt.Fprintf(stderr, "kb: unknown command %q (known: %s)\n", cmd, strings.Join(names, ", "))
		return true, 2
	}
	if err := fn(args[1:]); err != nil {
		fmt.Fprintf(stderr, "kb %s: %v\n", cmd, err)
		return true, 1
	}
	return true, 0
}

// runMCP serves the board over MCP stdio: kb mcp [--data DIR] [--user NAME].
func runMCP(args []string) error {
	fs := flag.NewFlagSet("kb mcp", flag.ExitOnError)
	dataDir := fs.String("data", defaultDataDir(), "board storage directory (env KB_DATA)")
	user := fs.String("user", envOr("KB_USER", "default"), "board user the tools operate on (env KB_USER)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return mcpRun(*dataDir, *user)
}

// defaultDataDir resolves the board storage directory: KB_DATA if set, else
// ~/.local/share/kb.
func defaultDataDir() string {
	dir, err := resolveDefaultDataDir()
	if err != nil {
		fatalLogf("cannot determine home directory, set KB_DATA or --data: %v", err)
	}
	return dir
}

var userHomeDir = os.UserHomeDir

func resolveDefaultDataDir() (string, error) {
	if v := os.Getenv("KB_DATA"); v != "" {
		return v, nil
	}
	home, err := userHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "kb"), nil
}
