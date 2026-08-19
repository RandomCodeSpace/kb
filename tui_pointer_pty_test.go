//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/creack/pty"

	"github.com/RandomCodeSpace/kb/internal/ai"
	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
	"github.com/RandomCodeSpace/kb/internal/tui/cardeditor"
)

type lockedPTYOutput struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (o *lockedPTYOutput) writeFrom(reader io.Reader) {
	chunk := make([]byte, 4096)
	for {
		count, err := reader.Read(chunk)
		if count > 0 {
			o.mu.Lock()
			_, _ = o.buf.Write(chunk[:count])
			o.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (o *lockedPTYOutput) contains(marker string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return bytes.Contains(o.buf.Bytes(), []byte(marker))
}

func (o *lockedPTYOutput) string() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.String()
}

func (o *lockedPTYOutput) length() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.Len()
}

func (o *lockedPTYOutput) containsAfter(offset int, marker string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	content := o.buf.Bytes()
	if offset > len(content) {
		return false
	}
	return bytes.Contains(content[offset:], []byte(marker))
}

func waitForPTYMarker(t *testing.T, output *lockedPTYOutput, marker string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if output.contains(marker) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("PTY output did not contain %q:\n%s", marker, output.string())
}

func waitForPTYMarkerAfter(t *testing.T, output *lockedPTYOutput, offset int, marker string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if output.containsAfter(offset, marker) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("PTY output after byte %d did not contain %q:\n%s", offset, marker, output.string())
}

func editorSavePoint(t *testing.T, st *store.Store, task board.Task, width, height int) (int, int) {
	t.Helper()
	editor := cardeditor.New(st, "pointer-pty")
	editor.SetAIRunner(ai.NewRunner(st, "", nil, nil), context.Background())
	editor.OpenEdit(task)
	for row, line := range strings.Split(ansi.Strip(editor.View(width, height)), "\n") {
		if column := strings.Index(line, "[Save card]"); column >= 0 {
			return ansi.StringWidth(line[:column]) + 1, row + 1
		}
	}
	t.Fatalf("save control is not visible at %dx%d:\n%s", width, height, editor.View(width, height))
	return 0, 0
}

func waitForTaskStatus(t *testing.T, st *store.Store, user, taskID string, status board.Status) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		task, err := st.Task(user, taskID)
		if err == nil && task.Status == status {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	task, err := st.Task(user, taskID)
	t.Fatalf("task status = %s, %v; want %s", task.Status, err, status)
}

func waitForTaskTitle(t *testing.T, st *store.Store, user, taskID, title string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		task, err := st.Task(user, taskID)
		if err == nil && task.Title == title {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	task, err := st.Task(user, taskID)
	t.Fatalf("task title = %q, %v; want %q", task.Title, err, title)
}

func TestPTYRawSGRDragPersistsCardMove(t *testing.T) {
	if testing.Short() {
		t.Skip("compiled PTY acceptance test")
	}
	temp := t.TempDir()
	binary := filepath.Join(temp, "kb")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOFLAGS=-buildvcs=false")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build kb: %v\n%s", err, output)
	}

	data := filepath.Join(temp, "data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(data, "kb.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	task, err := st.AddTask("pointer-pty", board.Task{Title: "Drag through raw SGR", Status: board.StatusTodo, Prio: 3})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "tui", "--data", data, "--user", "pointer-pty")
	command.Env = append(os.Environ(), "TERM=xterm-256color")
	const terminalWidth, terminalHeight = 120, 40
	terminal, err := pty.StartWithSize(command, &pty.Winsize{Rows: terminalHeight, Cols: terminalWidth})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = terminal.Close() })
	var output lockedPTYOutput
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("PTY transcript:\n%s", output.string())
		}
	})
	go output.writeFrom(terminal)
	waitForPTYMarker(t, &output, "Drag through raw SGR")

	// SGR coordinates are one-based. The board spends four chrome rows (top
	// bar, two toolbar rows, page padding), then each column spends a header
	// band and a meta line, so the first card in Todo is row 7; Doing starts
	// after the page margin and the first 39-cell column. Exercise the terminal
	// decoder, not Model.Update with fabricated mouse messages.
	if _, err := io.WriteString(terminal, "\x1b[<0;2;7M"); err != nil {
		t.Fatal(err)
	}
	waitForPTYMarker(t, &output, "Lifted Drag through raw SGR")
	moveOffset := output.length()
	if _, err := io.WriteString(terminal, "\x1b[<32;45;7M"); err != nil {
		t.Fatal(err)
	}
	// The preview redraws the card under Doing. Its title is the only stable
	// marker: the band's count is right-aligned away from the column name.
	waitForPTYMarkerAfter(t, &output, moveOffset, "Drag through raw SGR")
	if _, err := io.WriteString(terminal, "\x1b[<0;45;7m"); err != nil {
		t.Fatal(err)
	}
	waitForTaskStatus(t, st, "pointer-pty", task.ID, board.StatusDoing)
	editOffset := output.length()
	if _, err := io.WriteString(terminal, "e"); err != nil {
		t.Fatal(err)
	}
	waitForPTYMarkerAfter(t, &output, editOffset, "EDIT CARD")
	boardOffset := output.length()
	if _, err := io.WriteString(terminal, "X\x13"); err != nil {
		t.Fatal(err)
	}
	waitForTaskTitle(t, st, "pointer-pty", task.ID, "Drag through raw SGRX")
	waitForPTYMarkerAfter(t, &output, boardOffset, "DOING")

	editOffset = output.length()
	if _, err := io.WriteString(terminal, "e"); err != nil {
		t.Fatal(err)
	}
	waitForPTYMarkerAfter(t, &output, editOffset, "EDIT CARD")
	// Exercise the non-Ctrl+S fallback through Bubble Tea's enhanced-key
	// sequence for Ctrl+Enter. Move to the end explicitly; cursor placement is
	// otherwise a terminal-input concern unrelated to the save contract.
	boardOffset = output.length()
	if _, err := io.WriteString(terminal, "\x1b[FY\x1b[13;5u"); err != nil {
		t.Fatal(err)
	}
	waitForTaskTitle(t, st, "pointer-pty", task.ID, "Drag through raw SGRXY")
	waitForPTYMarkerAfter(t, &output, boardOffset, "DOING")

	editOffset = output.length()
	if _, err := io.WriteString(terminal, "e"); err != nil {
		t.Fatal(err)
	}
	waitForPTYMarkerAfter(t, &output, editOffset, "EDIT CARD")
	if _, err := io.WriteString(terminal, "\x1b[FZ"); err != nil {
		t.Fatal(err)
	}
	current, err := st.Task("pointer-pty", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	saveX, saveY := editorSavePoint(t, st, current, terminalWidth, terminalHeight)
	pressOffset := output.length()
	if _, err := fmt.Fprintf(terminal, "\x1b[<0;%d;%dM", saveX, saveY); err != nil {
		t.Fatal(err)
	}
	// Press feedback rerenders the editor and replaces its immutable pointer
	// handler. Releasing through that new handler is the protocol regression.
	// Body controls use a same-width literal marker so sanitization and narrow
	// terminal clipping cannot erase the feedback. The terminal diff moves to
	// that cell and writes the marker before release.
	waitForPTYMarkerAfter(t, &output, pressOffset, "H!")
	if _, err := fmt.Fprintf(terminal, "\x1b[<0;%d;%dm", saveX, saveY); err != nil {
		t.Fatal(err)
	}
	waitForTaskTitle(t, st, "pointer-pty", task.ID, "Drag through raw SGRXYZ")
	if _, err := io.WriteString(terminal, "q"); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("TUI exit: %v\n%s", err, output.string())
	}
	if ctx.Err() != nil {
		t.Fatal(fmt.Errorf("TUI timeout: %w", ctx.Err()))
	}
}
