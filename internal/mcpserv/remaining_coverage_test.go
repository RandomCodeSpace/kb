package mcpserv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRunRemainingFilesystemFailures(t *testing.T) {
	original := serveMCP
	t.Cleanup(func() { serveMCP = original })
	serveMCP = func(*mcp.Server) error {
		t.Fatal("serveMCP called after setup failure")
		return nil
	}

	t.Run("secret load", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("KB_SECRET", "")
		if err := os.Mkdir(filepath.Join(dir, "secret"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := Run(dir, "tester", "test-version"); err == nil || !strings.Contains(err.Error(), "read secret") {
			t.Fatalf("Run secret error = %v", err)
		}
	})

	t.Run("store open", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("KB_SECRET", "test-secret")
		if err := os.Mkdir(filepath.Join(dir, "kb.db"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := Run(dir, "tester", "test-version"); err == nil {
			t.Fatal("Run accepted a directory as kb.db")
		}
	})

	t.Run("legacy import", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("KB_SECRET", "test-secret")
		if err := os.Symlink("missing-target", filepath.Join(dir, "broken.md")); err != nil {
			t.Fatal(err)
		}
		if err := Run(dir, "tester", "test-version"); err == nil || !strings.Contains(err.Error(), "store: read") {
			t.Fatalf("Run import error = %v", err)
		}
	})
}
