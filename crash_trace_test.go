package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestCrashTraceCleanExit(t *testing.T) {
	for _, runErr := range []error{nil, errors.New("ordinary failure")} {
		t.Run(fmt.Sprint(runErr), func(t *testing.T) {
			data := t.TempDir()
			original := os.Stderr
			if got := runWithCrashTrace(data, "test", func() error { return runErr }); !errors.Is(got, runErr) {
				t.Fatalf("run error = %v, want %v", got, runErr)
			}
			if os.Stderr != original {
				t.Fatal("stderr was not restored")
			}
			files, err := os.ReadDir(data)
			if err != nil || len(files) != 0 {
				t.Fatalf("clean exit left artifacts: %v, %v", files, err)
			}
		})
	}
}

func TestCrashTraceUnavailableDirectoryStillRuns(t *testing.T) {
	restoreTUISeams(t)
	var warning bytes.Buffer
	tuiStderr = &warning
	want := tea.ErrProgramPanic
	called := false
	err := runWithCrashTrace(filepath.Join(t.TempDir(), "missing"), "test", func() error {
		called = true
		return want
	})
	if !called || !errors.Is(err, want) || !strings.Contains(warning.String(), "cannot capture TUI crash trace") {
		t.Fatalf("called=%v error=%v warning=%q", called, err, warning.String())
	}
}

type failedCrashWriter struct{}

func (failedCrashWriter) Write([]byte) (int, error) { return 0, os.ErrPermission }

func TestCrashTraceWriteFailureStillForwardsAndCompletes(t *testing.T) {
	var stderr bytes.Buffer
	writer := &crashTraceWriter{file: failedCrashWriter{}, stderr: &stderr, complete: make(chan struct{})}
	trace := "Caught panic:\r\n\r\nboom\r\n\r\nRestoring terminal...\r\n\r\ngoroutine 1 [running]:\r\nmain.probe()\r\n\tprobe.go:10 +0x1\r\n\r\n"
	// Force framing across read boundaries, including CRLF pairs.
	for _, b := range []byte(trace) {
		if _, err := writer.Write([]byte{b}); err != nil {
			t.Fatal(err)
		}
	}
	if !errors.Is(writer.err, os.ErrPermission) || stderr.String() != trace {
		t.Fatalf("error=%v stderr=%q", writer.err, stderr.String())
	}
	select {
	case <-writer.complete:
	default:
		t.Fatal("did not detect the complete stack")
	}
}

func TestCrashTraceWaitsForDelayedStackAndKeepsPriorReport(t *testing.T) {
	restoreTUISeams(t)
	data := t.TempDir()
	var warnings bytes.Buffer
	tuiStderr = &warnings
	stderr, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()
	original := os.Stderr
	os.Stderr = stderr
	defer func() { os.Stderr = original }()
	const trace = "Caught panic:\r\n\r\nlate panic\r\n\r\nRestoring terminal...\r\n\r\ngoroutine 1 [running]:\r\nmain.originalPanic()\r\n\tmain.go:42\r\n\r\n"
	for range 2 {
		err := runWithCrashTrace(data, "test-version", func() error {
			target := os.Stderr
			go func() {
				time.Sleep(10 * time.Millisecond)
				fmt.Fprint(target, trace)
			}()
			return tea.ErrProgramPanic
		})
		if !errors.Is(err, tea.ErrProgramPanic) {
			t.Fatal(err)
		}
	}
	files, err := filepath.Glob(filepath.Join(data, "kb-crash-*.log"))
	if err != nil || len(files) != 2 {
		t.Fatalf("files=%v error=%v", files, err)
	}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Contains(data, []byte(trace)) {
			t.Fatalf("trace=%q error=%v", data, err)
		}
	}
	if strings.Contains(warnings.String(), "incomplete") {
		t.Fatal(warnings.String())
	}
	if _, err := stderr.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	forwarded, err := os.ReadFile(stderr.Name())
	if err != nil || string(forwarded) != trace+trace {
		t.Fatalf("stderr=%q error=%v", forwarded, err)
	}
}

func TestCrashTraceMissingStackReportsIncompleteCapture(t *testing.T) {
	restoreTUISeams(t)
	var warnings bytes.Buffer
	tuiStderr = &warnings
	data := t.TempDir()
	if err := runWithCrashTrace(data, "test", func() error { return tea.ErrProgramPanic }); !errors.Is(err, tea.ErrProgramPanic) {
		t.Fatal(err)
	}
	if !strings.Contains(warnings.String(), "timed out waiting for the complete Bubble Tea panic trace") {
		t.Fatalf("missing incomplete-capture warning: %q", warnings.String())
	}
	files, err := filepath.Glob(filepath.Join(data, "kb-crash-*.log"))
	if err != nil || len(files) != 1 {
		t.Fatalf("partial crash files = %v, %v", files, err)
	}
}
