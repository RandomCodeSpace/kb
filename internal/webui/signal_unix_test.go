//go:build unix

package webui

import (
	"io"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestRunStopsOnSignal(t *testing.T) {
	stdout := &syncBuffer{}
	done := make(chan error, 1)
	go func() {
		done <- Run(Options{DataDir: t.TempDir(), User: "default"}, stdout, io.Discard)
	}()
	waitForURL(t, stdout)
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop on SIGINT")
	}
}
