//go:build linux || darwin

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
)

type crashProbeModel struct{ mode string }

func (m crashProbeModel) Init() tea.Cmd {
	return nil
}

func crashProbeCommand() tea.Msg { panic("crash trace command probe") }

func (m crashProbeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.KeyPressMsg); ok {
		if m.mode == "command" {
			return m, crashProbeCommand
		}
		panic("crash trace update probe")
	}
	return m, nil
}

func (m crashProbeModel) View() tea.View {
	v := tea.NewView("crash probe")
	v.AltScreen = true
	return v
}

func TestCrashTracePanicProcess(t *testing.T) {
	if mode := os.Getenv("KB_CRASH_TRACE_PROBE"); mode != "" {
		before, err := term.GetState(os.Stdin.Fd())
		if err != nil {
			t.Fatal(err)
		}
		err = runWithCrashTrace(os.Getenv("KB_CRASH_TRACE_DATA"), "crash-test-version", func() error {
			_, err := tea.NewProgram(crashProbeModel{mode: mode}, tea.WithoutSignals()).Run()
			return err
		})
		if !errors.Is(err, tea.ErrProgramPanic) {
			t.Fatalf("run error = %v", err)
		}
		after, err := term.GetState(os.Stdin.Fd())
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("terminal state not restored: before=%+v after=%+v error=%v", before, after, err)
		}
		fmt.Println("TERMINAL_RESTORED")
		return
	}
	for _, mode := range []string{"update", "command"} {
		t.Run(mode, func(t *testing.T) {
			data := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCrashTracePanicProcess$", "-test.count=1")
			cmd.Env = append(os.Environ(), "KB_CRASH_TRACE_PROBE="+mode, "KB_CRASH_TRACE_DATA="+data, "TERM=xterm-256color", "TEA_DEBUG=false")
			terminal, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
			if err != nil {
				t.Fatal(err)
			}
			defer terminal.Close()
			var captured lockedPTYOutput
			readDone := make(chan struct{})
			go func() {
				captured.writeFrom(terminal)
				close(readDone)
			}()
			waitForPTYMarker(t, &captured, "\x1b[?1049h")
			if _, err := terminal.Write([]byte("p")); err != nil {
				t.Fatal(err)
			}
			if err := cmd.Wait(); err != nil {
				t.Fatalf("panic probe: %v\n%s", err, captured.string())
			}
			<-readDone
			output := captured.string()
			for _, marker := range []string{"TERMINAL_RESTORED", "crash trace " + mode + " probe", "crash trace saved to", "\x1b[?1049l"} {
				if !strings.Contains(string(output), marker) {
					t.Fatalf("missing %q in process output:\n%s", marker, output)
				}
			}
			if strings.Contains(string(output), "capture incomplete") {
				t.Fatalf("incomplete capture:\n%s", output)
			}
			files, err := filepath.Glob(filepath.Join(data, "kb-crash-*.log"))
			if err != nil || len(files) != 1 {
				t.Fatalf("crash files = %v, %v", files, err)
			}
			trace, err := os.ReadFile(files[0])
			if err != nil {
				t.Fatal(err)
			}
			frame := "crashProbeModel.Update"
			if mode == "command" {
				frame = "crashProbeCommand"
			}
			for _, marker := range []string{"crash-test-version", "crash trace " + mode + " probe", "goroutine ", frame, "crash_trace_pty_test.go:"} {
				if !strings.Contains(string(trace), marker) {
					t.Fatalf("missing %q in saved trace:\n%s", marker, trace)
				}
			}
			info, err := os.Stat(files[0])
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("crash file permissions: %v, %v", info, err)
			}
		})
	}
}
