package main

import (
	"flag"
	"fmt"
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

// dispatch runs os.Args[1] as a subcommand when one is named, reporting
// whether it handled the invocation. Unknown subcommands exit with an error
// so typos never silently start the web server.
func dispatch() bool {
	if len(os.Args) < 2 || strings.HasPrefix(os.Args[1], "-") {
		return false
	}
	cmd := os.Args[1]
	fn, ok := subcommands[cmd]
	if !ok {
		names := make([]string, 0, len(subcommands))
		for name := range subcommands {
			names = append(names, name)
		}
		sort.Strings(names)
		fmt.Fprintf(os.Stderr, "kb: unknown command %q (known: %s)\n", cmd, strings.Join(names, ", "))
		os.Exit(2)
	}
	if err := fn(os.Args[2:]); err != nil {
		fmt.Fprintf(os.Stderr, "kb %s: %v\n", cmd, err)
		os.Exit(1)
	}
	return true
}

// runMCP serves the board over MCP stdio: kb mcp [--data DIR] [--user NAME].
func runMCP(args []string) error {
	fs := flag.NewFlagSet("kb mcp", flag.ExitOnError)
	dataDir := fs.String("data", defaultDataDir(), "board storage directory (env KB_DATA)")
	user := fs.String("user", envOr("KB_USER", "default"), "board user the tools operate on (env KB_USER)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return mcpserv.Run(*dataDir, *user)
}

// defaultDataDir resolves the board storage directory: KB_DATA if set, else
// ~/.local/share/kb.
func defaultDataDir() string {
	if v := os.Getenv("KB_DATA"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("cannot determine home directory, set KB_DATA or --data: %v", err)
	}
	return filepath.Join(home, ".local", "share", "kb")
}
