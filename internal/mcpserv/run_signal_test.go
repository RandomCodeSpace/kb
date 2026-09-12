//go:build linux || darwin

package mcpserv

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRunSIGTERMClosesStore(t *testing.T) {
	const helperEnv = "KB_MCP_SIGNAL_TEST_DATA"
	if dir := os.Getenv(helperEnv); dir != "" {
		serveMCP = func(ctx context.Context, _ *mcp.Server) error {
			if _, err := os.Stat(filepath.Join(dir, "kb.db-wal")); err != nil {
				return fmt.Errorf("WAL not open: %w", err)
			}
			fmt.Println("ready")
			<-ctx.Done()
			return ctx.Err()
		}
		if err := Run(dir, "default", "test-version"); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, "kb.db-wal")); !os.IsNotExist(err) {
			t.Fatalf("store did not close WAL: %v", err)
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRunSIGTERMClosesStore$", "-test.count=1")
	cmd.Env = append(os.Environ(), helperEnv+"="+t.TempDir(), "KB_SECRET=mcp-signal-test-secret")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ready, readErr := bufio.NewReader(stdout).ReadString('\n')
	if readErr != nil || ready != "ready\n" {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("helper not ready: %q err=%v stderr=%q", ready, readErr, stderr.String())
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("SIGTERM shutdown: %v; stderr=%q", err, stderr.String())
	}
}
