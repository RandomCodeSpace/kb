package main

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/store"
)

func resetServerEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"KB_PORT", "KB_DATA", "KB_LOG_FILE", "KB_TOKEN", "KB_AZURE_TENANT_ID",
		"KB_AZURE_CLIENT_ID", "KB_ALLOWED_HOSTS", "KB_BIND",
	} {
		t.Setenv(key, "")
	}
}

func TestRunWebServerWiringAndAuthModes(t *testing.T) {
	original := listenHTTPServer
	t.Cleanup(func() { listenHTTPServer = original })
	wantStop := errors.New("listener stopped")

	tests := []struct {
		name     string
		env      map[string]string
		wantAddr string
	}{
		{name: "open binds loopback", wantAddr: "127.0.0.1:8123"},
		{name: "token may bind all interfaces", env: map[string]string{"KB_TOKEN": "secret"}, wantAddr: ":8123"},
		{name: "entra may bind explicitly", env: map[string]string{
			"KB_AZURE_TENANT_ID": "tenant", "KB_AZURE_CLIENT_ID": "client", "KB_BIND": "localhost",
		}, wantAddr: "localhost:8123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetServerEnv(t)
			for key, value := range tt.env {
				t.Setenv(key, value)
			}
			data := t.TempDir()
			var got *http.Server
			listenHTTPServer = func(srv *http.Server) error {
				got = srv
				return wantStop
			}
			err := runWebServer([]string{"--data", data, "--port", "8123"})
			if !errors.Is(err, wantStop) {
				t.Fatalf("runWebServer error = %v, want listener stop", err)
			}
			if got == nil || got.Addr != tt.wantAddr || got.Handler == nil {
				t.Fatalf("server = %#v, want addr %q and handler", got, tt.wantAddr)
			}
			for _, name := range []string{"secret", "kb.db"} {
				if _, err := os.Stat(filepath.Join(data, name)); err != nil {
					t.Errorf("startup artifact %s: %v", name, err)
				}
			}
		})
	}
}

func TestRunWebServerReportsStartupErrors(t *testing.T) {
	original := listenHTTPServer
	t.Cleanup(func() { listenHTTPServer = original })
	listenHTTPServer = func(*http.Server) error { return nil }

	t.Run("flag parse", func(t *testing.T) {
		resetServerEnv(t)
		var output bytes.Buffer
		err := runWebServerWithFlagOutput([]string{"--unknown"}, &output)
		var flagErr *webFlagError
		if !errors.As(err, &flagErr) || !strings.Contains(output.String(), "usage: kb serve") {
			t.Fatalf("unknown flag error/output = %v / %q", err, output.String())
		}
	})

	t.Run("help", func(t *testing.T) {
		resetServerEnv(t)
		var output bytes.Buffer
		err := runWebServerWithFlagOutput([]string{"-h"}, &output)
		if !errors.Is(err, flag.ErrHelp) || !strings.Contains(output.String(), "usage: kb serve") {
			t.Fatalf("help error/output = %v / %q", err, output.String())
		}
		// The serve flags must still be printed after the overview.
		for _, want := range []string{"-port", "-data", "-log"} {
			if !strings.Contains(output.String(), want) {
				t.Errorf("--help does not document the %s serve flag", want)
			}
		}
	})

	t.Run("unpaired entra configuration", func(t *testing.T) {
		resetServerEnv(t)
		t.Setenv("KB_AZURE_TENANT_ID", "tenant")
		err := runWebServer([]string{"--data", t.TempDir()})
		if err == nil || !strings.Contains(err.Error(), "must be set together") {
			t.Fatalf("tenant-only error = %v", err)
		}
	})

	t.Run("unopenable log", func(t *testing.T) {
		resetServerEnv(t)
		path := filepath.Join(t.TempDir(), "missing", "kb.log")
		err := runWebServer([]string{"--data", t.TempDir(), "--log", path})
		if err == nil || !strings.Contains(err.Error(), "configure log file") {
			t.Fatalf("log error = %v", err)
		}
	})

	t.Run("data path is a file", func(t *testing.T) {
		resetServerEnv(t)
		path := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := runWebServer([]string{"--data", path})
		if err == nil || !strings.Contains(err.Error(), "create data dir") {
			t.Fatalf("data-dir error = %v", err)
		}
	})

	t.Run("short existing secret", func(t *testing.T) {
		resetServerEnv(t)
		data := t.TempDir()
		if err := os.WriteFile(filepath.Join(data, "secret"), []byte("short"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := runWebServer([]string{"--data", data})
		if err == nil || !strings.Contains(err.Error(), "secret file") {
			t.Fatalf("secret error = %v", err)
		}
	})
}

func TestMainRootProcessContract(t *testing.T) {
	for _, tt := range []struct {
		name, arg, want string
		wantCode        int
	}{
		{name: "help", arg: "-h", wantCode: 0, want: "usage: kb"},
		{name: "legacy port", arg: "--port", wantCode: 2, want: "use `kb serve"},
		{name: "unknown", arg: "--unknown", wantCode: 2, want: "use `kb serve"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestMainFlagProcessHelper$")
			cmd.Env = append(os.Environ(), "KB_TEST_MAIN_FLAG="+tt.arg)
			output, err := cmd.CombinedOutput()
			code := 0
			if err != nil {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) {
					t.Fatalf("run helper: %v", err)
				}
				code = exitErr.ExitCode()
			}
			if code != tt.wantCode || !strings.Contains(string(output), tt.want) {
				t.Fatalf("exit/output = %d / %q, want %d containing %q", code, output, tt.wantCode, tt.want)
			}
		})
	}
}

func TestRunRootTTYMatrixDoesNotOpenDataForNonTTY(t *testing.T) {
	restoreRootSeams(t)
	restoreTUISeams(t)
	originalInTTY, originalOutTTY := stdinIsTerminal, stdoutIsTerminal
	originalStdout, originalStderr := rootStdout, rootStderr
	t.Cleanup(func() {
		stdinIsTerminal, stdoutIsTerminal = originalInTTY, originalOutTTY
		rootStdout, rootStderr = originalStdout, originalStderr
	})

	var output bytes.Buffer
	rootStdout = &output
	data := filepath.Join(t.TempDir(), "must-not-exist")
	t.Setenv("KB_DATA", data)
	opened := 0
	openTUIStore = func(string, io.Writer) (*store.Store, error) {
		opened++
		return nil, errors.New("opened")
	}

	if err := runRoot([]string{"--help"}); err != nil || !strings.Contains(output.String(), "serve      run") {
		t.Fatalf("root help = %v / %q", err, output.String())
	}
	output.Reset()
	err := runRoot([]string{"--port", "9000"})
	var usageErr *rootUsageError
	if !errors.As(err, &usageErr) || !strings.Contains(err.Error(), "kb serve") {
		t.Fatalf("legacy root flags = %v, want usage guidance", err)
	}

	for _, tc := range []struct {
		name       string
		stdin, out bool
	}{
		{"stdin pipe", false, true},
		{"stdout pipe", true, false},
		{"both pipes", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdinIsTerminal = func() bool { return tc.stdin }
			stdoutIsTerminal = func() bool { return tc.out }
			output.Reset()
			if err := runRoot(nil); err != nil {
				t.Fatalf("runRoot: %v", err)
			}
			if !strings.Contains(output.String(), "usage: kb") {
				t.Fatalf("non-TTY output = %q", output.String())
			}
		})
	}
	if opened != 0 {
		t.Fatalf("non-TTY root opened the data store %d times", opened)
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Fatalf("non-TTY root created data path: %v", err)
	}

	stdinIsTerminal = func() bool { return true }
	stdoutIsTerminal = func() bool { return true }
	if err := runRoot(nil); err == nil || err.Error() != "opened" {
		t.Fatalf("TTY root error = %v, want TUI open seam", err)
	}
	if opened != 1 {
		t.Fatalf("TTY root opened store %d times, want 1", opened)
	}
}

func TestMainFlagProcessHelper(t *testing.T) {
	arg := os.Getenv("KB_TEST_MAIN_FLAG")
	if arg == "" {
		return
	}
	os.Args = []string{"kb", arg}
	main()
}

// `kb --help` is answered by the serve FlagSet, which knows nothing about the
// dispatch table — the overview text is maintained by hand. This is the guard
// that keeps it complete: every registered subcommand must be named in it.
func TestRootUsageNamesEverySubcommand(t *testing.T) {
	for name := range subcommands {
		if !strings.Contains(rootUsageText, name) {
			t.Errorf("root usage text does not mention the %q subcommand", name)
		}
	}
}

func TestSmallStartupHelpers(t *testing.T) {
	t.Setenv("KB_TEST_ENV", "configured")
	if got := envOr("KB_TEST_ENV", "fallback"); got != "configured" {
		t.Fatalf("envOr configured = %q", got)
	}
	t.Setenv("KB_TEST_ENV", "")
	if got := envOr("KB_TEST_ENV", "fallback"); got != "fallback" {
		t.Fatalf("envOr fallback = %q", got)
	}
	if got := logSafe("one\n\rtwo\x00\tthree"); got != "onetwothree" {
		t.Fatalf("logSafe = %q", got)
	}
	if err := (emptyCloser{}).Close(); err != nil {
		t.Fatalf("empty closer: %v", err)
	}
}
