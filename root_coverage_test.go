package main

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func restoreRootSeams(t *testing.T) {
	t.Helper()
	originalRun := runMainRoot
	originalFatal := fatalLog
	originalExit := exitProcess
	originalFatalf := fatalLogf
	originalArgs := os.Args
	originalCommands := subcommands
	originalHome := userHomeDir
	t.Cleanup(func() {
		runMainRoot = originalRun
		fatalLog = originalFatal
		exitProcess = originalExit
		fatalLogf = originalFatalf
		os.Args = originalArgs
		subcommands = originalCommands
		userHomeDir = originalHome
	})
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
	runMainRoot = func(args []string) error {
		called = true
		if len(args) != 0 {
			t.Fatalf("root args = %v", args)
		}
		return nil
	}
	os.Args = []string{"kb"}
	main()
	if !called {
		t.Fatal("main did not run the root command")
	}

	want := errors.New("root failed")
	var fatalArgs []any
	runMainRoot = func([]string) error { return want }
	fatalLog = func(args ...any) { fatalArgs = append([]any(nil), args...) }
	main()
	if len(fatalArgs) != 1 || !errors.Is(fatalArgs[0].(error), want) {
		t.Fatalf("fatal args = %v", fatalArgs)
	}

	exitCode = 0
	runMainRoot = func([]string) error { return &rootUsageError{message: "bad flag"} }
	main()
	if exitCode != 2 {
		t.Fatalf("invalid flag exit = %d, want 2", exitCode)
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
