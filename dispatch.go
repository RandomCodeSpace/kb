package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"

	term "github.com/charmbracelet/x/term"

	"github.com/RandomCodeSpace/kb/internal/mcpserv"
)

// subcommands is the kb subcommand dispatch table. A bare invocation is
// handled separately because it depends on whether stdin and stdout are TTYs.
var subcommands = map[string]func(args []string) error{
	"mcp":     runMCP,
	"serve":   runWebServer,
	"tui":     runTUI,
	"version": runVersion,
}

var mcpRun = mcpserv.Run
var exitProcess = os.Exit
var fatalLogf = log.Fatalf

var (
	rootStdout       io.Writer = os.Stdout
	rootStderr       io.Writer = os.Stderr
	stdinIsTerminal            = func() bool { return term.IsTerminal(os.Stdin.Fd()) }
	stdoutIsTerminal           = func() bool { return term.IsTerminal(os.Stdout.Fd()) }
)

type rootUsageError struct{ message string }

func (e *rootUsageError) Error() string { return e.message }

// runRoot handles only the commandless surface. It never opens the data store
// when either standard stream is non-interactive.
func runRoot(args []string) error {
	if len(args) != 0 {
		if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
			fmt.Fprint(rootStdout, rootUsageText)
			return nil
		}
		return &rootUsageError{message: "root flags are no longer accepted; use `kb serve --port ... --data ... --log ...` for the optional API server"}
	}
	if !stdinIsTerminal() || !stdoutIsTerminal() {
		fmt.Fprint(rootStdout, rootUsageText)
		return nil
	}
	return runTUI(nil)
}

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
		var flagErr *webFlagError
		if errors.As(err, &flagErr) {
			if errors.Is(flagErr, flag.ErrHelp) {
				return true, 0
			}
			fmt.Fprintf(stderr, "kb %s: %v\n", cmd, err)
			return true, 2
		}
		fmt.Fprintf(stderr, "kb %s: %v\n", cmd, err)
		return true, 1
	}
	return true, 0
}

var readBuildInfo = debug.ReadBuildInfo
var versionOut io.Writer = os.Stdout

// runVersion prints the build's version: kb version [--json]. go-install
// builds carry the module version; release binaries are built at the tagged
// commit with a clean tree, so the toolchain stamps the same value there. A
// plain go build in a checkout reports devel plus the commit it was built
// from.
func runVersion(args []string) error {
	fs := flag.NewFlagSet("kb version", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "print the version as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("version takes no arguments")
	}
	info, ok := readBuildInfo()
	if *jsonOut {
		encoded, err := json.MarshalIndent(versionJSON(info, ok), "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(versionOut, string(encoded))
		return nil
	}
	fmt.Fprintln(versionOut, versionString(info, ok))
	return nil
}

// versionParts extracts the display version, the 12-char revision, and the
// dirty flag from build info. version is "unknown" without build info and
// "devel" for unstamped builds.
func versionParts(info *debug.BuildInfo, ok bool) (version, revision string, modified bool) {
	if !ok || info == nil {
		return "unknown", "", false
	}
	version = info.Main.Version
	if version == "" || version == "(devel)" {
		version = "devel"
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	return version, revision, modified
}

type versionInfoJSON struct {
	Version  string `json:"version"`
	Revision string `json:"revision,omitempty"`
	Modified bool   `json:"modified,omitempty"`
}

func versionJSON(info *debug.BuildInfo, ok bool) versionInfoJSON {
	version, revision, modified := versionParts(info, ok)
	return versionInfoJSON{Version: version, Revision: revision, Modified: modified}
}

func versionString(info *debug.BuildInfo, ok bool) string {
	version, revision, modified := versionParts(info, ok)
	if version == "unknown" {
		return "kb (unknown build)"
	}
	out := "kb " + version
	if revision != "" {
		out += " (" + revision
		if modified {
			out += ", modified"
		}
		out += ")"
	}
	return out
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
