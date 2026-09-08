package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/RandomCodeSpace/kb/internal/webui"
)

var webRun = webui.Run

// runWeb serves the board to a local browser: kb web [--data DIR]
// [--addr HOST:PORT] [--no-open] [--unsafe-listen]. It is the only kb command
// that opens a TCP listener, and it binds to loopback unless --unsafe-listen
// is given.
func runWeb(args []string) error {
	return runWebWithFlagOutput(args, os.Stderr)
}

// runWebWithFlagOutput keeps flag failures returnable so an unknown flag is a
// testable usage error instead of a process exit.
func runWebWithFlagOutput(args []string, output io.Writer) error {
	fs := flag.NewFlagSet("kb web", flag.ContinueOnError)
	fs.SetOutput(output)
	dataDir := fs.String("data", defaultDataDir(), "board storage directory (env KB_DATA)")
	addr := fs.String("addr", "127.0.0.1:0", "listen address; port 0 picks a free port")
	noOpen := fs.Bool("no-open", false, "do not open a browser")
	unsafeListen := fs.Bool("unsafe-listen", false, "allow a non-loopback --addr; exposes the board to the network with no authentication")
	if err := fs.Parse(args); err != nil {
		return &commandFlagError{err: err}
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("web takes no arguments")
	}
	version, _, _ := versionParts(readBuildInfo())
	return webRun(webui.Options{
		DataDir:     *dataDir,
		User:        defaultBoardUser,
		Version:     version,
		Addr:        *addr,
		OpenBrowser: !*noOpen,
		AllowRemote: *unsafeListen,
	}, rootStdout, rootStderr)
}
