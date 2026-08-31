package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/store"
)

func TestMainRootProcessContract(t *testing.T) {
	for _, tt := range []struct {
		name, arg, want string
		wantCode        int
	}{
		{name: "help", arg: "-h", wantCode: 0, want: "usage: kb"},
		{name: "unknown", arg: "--unknown", wantCode: 2, want: "root flags are not accepted"},
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

	if err := runRoot([]string{"--help"}); err != nil || !strings.Contains(output.String(), "mcp        expose") || strings.Contains(output.String(), "kb serve") {
		t.Fatalf("root help = %v / %q", err, output.String())
	}
	output.Reset()
	err := runRoot([]string{"--port", "9000"})
	var usageErr *rootUsageError
	if !errors.As(err, &usageErr) || !strings.Contains(err.Error(), "root flags are not accepted") {
		t.Fatalf("root flags = %v, want local usage guidance", err)
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

func TestRootUsageNamesEverySubcommand(t *testing.T) {
	for name := range subcommands {
		if !strings.Contains(rootUsageText, name) {
			t.Errorf("root usage text does not mention the %q subcommand", name)
		}
	}
	if strings.Contains(rootUsageText, "serve") {
		t.Fatal("root usage still advertises removed hosting")
	}
}
