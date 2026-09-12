package store

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestOpenSQLitePaths(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, tc := range []struct {
		name     string
		path     string
		unixOnly bool
	}{
		{"absolute", filepath.Join(t.TempDir(), "kb.db"), false},
		{"relative", filepath.Join("reldata", "kb.db"), false},
		{"windows_shaped_on_unix", `C:\kb\kb.db`, true},
		{"question", filepath.Join("question?mark", "kb.db"), true},
		{"fragment", filepath.Join("hash#mark", "kb.db"), false},
		{"percent", filepath.Join("percent%20mark", "kb.db"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.unixOnly && runtime.GOOS == "windows" {
				t.Skip("filename requires Unix filesystem semantics")
			}
			if err := os.MkdirAll(filepath.Dir(tc.path), 0o700); err != nil {
				t.Fatal(err)
			}
			st, err := Open(tc.path, []byte("test-secret"))
			if err != nil {
				t.Fatalf("Open(%q): %v", tc.path, err)
			}
			for pragma, want := range map[string]string{
				"journal_mode": "wal", "foreign_keys": "1", "temp_store": "2",
				"synchronous": "2", "busy_timeout": "5000",
			} {
				var got string
				if err := st.db.QueryRow("PRAGMA " + pragma).Scan(&got); err != nil {
					_ = st.Close()
					t.Fatal(err)
				}
				if got != want {
					t.Errorf("PRAGMA %s = %q, want %q", pragma, got, want)
				}
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.HasPrefix(data, []byte("SQLite format 3\x00")) {
				t.Fatalf("intended path %q does not contain the SQLite database", tc.path)
			}
		})
	}
}
