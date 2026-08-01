package main

import (
	"errors"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/store"
)

func restoreRootSeams(t *testing.T) {
	t.Helper()
	originalLoad := loadOrCreateSecret
	originalOpen := openDataStore
	originalImport := importMarkdownDir
	originalSub := subDistFS
	originalRun := runMainServer
	originalFatal := fatalLog
	originalExit := exitProcess
	originalFatalf := fatalLogf
	originalArgs := os.Args
	originalCommands := subcommands
	originalHome := userHomeDir
	t.Cleanup(func() {
		loadOrCreateSecret = originalLoad
		openDataStore = originalOpen
		importMarkdownDir = originalImport
		subDistFS = originalSub
		runMainServer = originalRun
		fatalLog = originalFatal
		exitProcess = originalExit
		fatalLogf = originalFatalf
		os.Args = originalArgs
		subcommands = originalCommands
		userHomeDir = originalHome
	})
}

func TestRunWebServerInjectedStartupFailures(t *testing.T) {
	restoreRootSeams(t)
	resetServerEnv(t)
	want := errors.New("injected failure")

	t.Run("load secret", func(t *testing.T) {
		loadOrCreateSecret = func(string) ([]byte, error) { return nil, want }
		err := runWebServer([]string{"--data", t.TempDir()})
		if !errors.Is(err, want) || !strings.Contains(err.Error(), "load secret") {
			t.Fatalf("load-secret error = %v", err)
		}
	})

	t.Run("open store", func(t *testing.T) {
		loadOrCreateSecret = func(string) ([]byte, error) { return make([]byte, minSecretBytes), nil }
		openDataStore = func(string, []byte) (*store.Store, error) { return nil, want }
		err := runWebServer([]string{"--data", t.TempDir()})
		if !errors.Is(err, want) || !strings.Contains(err.Error(), "open store") {
			t.Fatalf("open-store error = %v", err)
		}
	})

	t.Run("invalid secret", func(t *testing.T) {
		loadOrCreateSecret = func(string) ([]byte, error) { return []byte("short"), nil }
		err := runWebServer([]string{"--data", t.TempDir()})
		if err == nil || !strings.Contains(err.Error(), "need at least") {
			t.Fatalf("invalid-secret error = %v", err)
		}
	})
}

func TestRunWebServerInjectedPostOpenFailures(t *testing.T) {
	restoreRootSeams(t)
	resetServerEnv(t)
	want := errors.New("injected failure")
	data := t.TempDir()

	importMarkdownDir = func(*store.Store, string) (int, error) { return 0, want }
	err := runWebServer([]string{"--data", data})
	if !errors.Is(err, want) || !strings.Contains(err.Error(), "import markdown") {
		t.Fatalf("import error = %v", err)
	}

	importMarkdownDir = func(*store.Store, string) (int, error) { return 0, nil }
	subDistFS = func(fs.FS, string) (fs.FS, error) { return nil, want }
	err = runWebServer([]string{"--data", data})
	if !errors.Is(err, want) || !strings.Contains(err.Error(), "embedded dist") {
		t.Fatalf("embedded-dist error = %v", err)
	}
}

func TestRunWebServerOpenExternalBindAndDefaultListener(t *testing.T) {
	restoreRootSeams(t)
	resetServerEnv(t)
	t.Setenv("KB_BIND", "0.0.0.0")
	wantStop := errors.New("stop")
	originalListener := listenHTTPServer
	t.Cleanup(func() { listenHTTPServer = originalListener })
	listenHTTPServer = func(srv *http.Server) error {
		if srv.Addr != "0.0.0.0:8124" {
			t.Fatalf("addr = %q", srv.Addr)
		}
		return wantStop
	}
	if err := runWebServer([]string{"--data", t.TempDir(), "--port", "8124"}); !errors.Is(err, wantStop) {
		t.Fatalf("external-bind error = %v", err)
	}

	listenHTTPServer = originalListener
	if err := listenHTTPServer(&http.Server{Addr: "127.0.0.1:-1"}); err == nil {
		t.Fatal("invalid listen address unexpectedly succeeded")
	}
}

func TestDispatchAndMainProcessBoundaries(t *testing.T) {
	restoreRootSeams(t)

	exitCode := 0
	exitProcess = func(code int) { exitCode = code }
	subcommands = map[string]func([]string) error{
		"bad": func([]string) error { return errors.New("failed") },
		"ok":  func([]string) error { return nil },
	}
	os.Args = []string{"kb", "bad"}
	if !dispatch() || exitCode != 1 {
		t.Fatalf("failed dispatch: exit=%d", exitCode)
	}

	os.Args = []string{"kb", "ok"}
	main()

	called := false
	runMainServer = func(args []string) error {
		called = true
		if len(args) != 0 {
			t.Fatalf("server args = %v", args)
		}
		return nil
	}
	os.Args = []string{"kb"}
	main()
	if !called {
		t.Fatal("main did not run the web server")
	}

	want := errors.New("server failed")
	var fatalArgs []any
	runMainServer = func([]string) error { return want }
	fatalLog = func(args ...any) { fatalArgs = append([]any(nil), args...) }
	main()
	if len(fatalArgs) != 1 || !errors.Is(fatalArgs[0].(error), want) {
		t.Fatalf("fatal args = %v", fatalArgs)
	}

	exitCode = 0
	runMainServer = func([]string) error { return &webFlagError{err: errors.New("bad flag")} }
	main()
	if exitCode != 2 {
		t.Fatalf("invalid flag exit = %d, want 2", exitCode)
	}

	exitCode = -1
	runMainServer = func([]string) error { return &webFlagError{err: flag.ErrHelp} }
	main()
	if exitCode != -1 {
		t.Fatalf("help invoked exit with %d", exitCode)
	}
}

func TestRegisteredCLICommandUsesProcessBoundary(t *testing.T) {
	restoreRootSeams(t)
	code := -1
	exitProcess = func(got int) { code = got }
	if err := subcommands["help"](nil); err != nil {
		t.Fatalf("help command: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
}

func TestDefaultDataDirFatalBoundary(t *testing.T) {
	restoreRootSeams(t)
	t.Setenv("KB_DATA", "")
	want := errors.New("no home")
	userHomeDir = func() (string, error) { return "", want }
	var format string
	var args []any
	fatalLogf = func(got string, values ...any) {
		format = got
		args = append([]any(nil), values...)
	}
	if got := defaultDataDir(); got != "" {
		t.Fatalf("defaultDataDir after fatal = %q", got)
	}
	if !strings.Contains(format, "cannot determine home") || len(args) != 1 || !errors.Is(args[0].(error), want) {
		t.Fatalf("fatal call = %q %v", format, args)
	}
}

func TestConfiguredLogPathThroughRunWebServer(t *testing.T) {
	restoreRootSeams(t)
	resetServerEnv(t)
	originalListener := listenHTTPServer
	originalLogWriter := logWriterForTest()
	t.Cleanup(func() {
		listenHTTPServer = originalListener
		setLogWriterForTest(originalLogWriter)
	})
	listenHTTPServer = func(*http.Server) error { return errors.New("stop") }
	logPath := filepath.Join(t.TempDir(), "kb.log")
	if err := runWebServer([]string{"--data", t.TempDir(), "--log", logPath}); err == nil {
		t.Fatal("listener stop was not returned")
	}
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("log file: %v", err)
	}
}

// Small wrappers keep the test's logger restoration explicit without exposing
// another production seam.
func logWriterForTest() interface{ Write([]byte) (int, error) }     { return log.Writer() }
func setLogWriterForTest(w interface{ Write([]byte) (int, error) }) { log.SetOutput(w) }
