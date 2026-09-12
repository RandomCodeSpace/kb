package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	tea "charm.land/bubbletea/v2"
)

var openCrashTracePipe = os.Pipe

// runWithCrashTrace belongs at the single TUI process boundary: Bubble Tea
// writes recovered panics directly to os.Stderr, without a writer option.
// Keep its recovery enabled so it still restores the terminal.
func runWithCrashTrace(dataDir, version string, run func() error) (runErr error) {
	file, err := os.CreateTemp(dataDir, "kb-crash-*.log")
	if err != nil {
		fmt.Fprintf(tuiStderr, "kb: cannot capture TUI crash trace: %v\n", err)
		return run()
	}
	reader, writer, err := openCrashTracePipe()
	if err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		fmt.Fprintf(tuiStderr, "kb: cannot capture TUI crash trace: %v\n", err)
		return run()
	}
	_, headerErr := fmt.Fprintf(file, "kb %s | %s/%s | %s\n\n", version, runtime.GOOS, runtime.GOARCH, time.Now().UTC().Format(time.RFC3339))
	stderr := os.Stderr
	trace := &crashTraceWriter{file: file, stderr: stderr, complete: make(chan struct{}), err: headerErr}
	done := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(trace, reader)
		done <- errors.Join(copyErr, trace.err, reader.Close())
	}()
	os.Stderr = writer
	defer func() {
		panicked := errors.Is(runErr, tea.ErrProgramPanic)
		var captureErr error
		if panicked {
			// Command goroutines may still be printing after Run returns its
			// panic error. Wait for the actual stack, not the returned error.
			timer := time.NewTimer(2 * time.Second)
			select {
			case <-trace.complete:
			case <-timer.C:
				captureErr = errors.New("timed out waiting for the complete Bubble Tea panic trace")
			}
			timer.Stop()
		}
		os.Stderr = stderr
		captureErr = errors.Join(captureErr, writer.Close(), <-done, file.Close())
		if panicked {
			fmt.Fprintf(tuiStderr, "kb: crash trace saved to %s\n", file.Name())
		} else {
			captureErr = errors.Join(captureErr, os.Remove(file.Name()))
		}
		if captureErr != nil {
			fmt.Fprintf(tuiStderr, "kb: TUI crash capture incomplete: %v\n", captureErr)
		}
	}()
	return run()
}

type crashTraceWriter struct {
	file     io.Writer
	stderr   io.Writer
	complete chan struct{}
	err      error
	tail     []byte
	inStack  bool
	finished bool
}

func (w *crashTraceWriter) Write(p []byte) (int, error) {
	// A broken stderr must not stop the file, nor a full disk silence stderr.
	if _, err := w.file.Write(p); err != nil && w.err == nil {
		w.err = err
	}
	_, _ = w.stderr.Write(p)
	w.observe(p)
	return len(p), nil
}

func (w *crashTraceWriter) observe(p []byte) {
	if w.finished {
		return
	}
	// This is Bubble Tea v2.0.9 recovery output. debug.Stack is printed in
	// one write and ends with a blank line; keep only framing across chunks.
	const start = "Restoring terminal...\r\n\r\ngoroutine "
	const end = "\r\n\r\n"
	data := append(w.tail, p...)
	if !w.inStack {
		if index := bytes.Index(data, []byte(start)); index >= 0 {
			w.inStack = true
			data = data[index+len(start):]
		}
	}
	if w.inStack && bytes.Contains(data, []byte(end)) {
		w.finished = true
		close(w.complete)
		w.tail = nil
		return
	}
	keep := len(start) - 1
	if w.inStack {
		keep = len(end) - 1
	}
	if len(data) > keep {
		data = data[len(data)-keep:]
	}
	w.tail = append(w.tail[:0], data...)
}
