package store

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIntegrityInspectionErrorsDoNotCreateDatabase(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "file")
	if err := os.WriteFile(blocked, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, message string }{
		{filepath.Join(dir, "invalid\x00", "kb.db"), "inspect backup marker"},
		{filepath.Join(dir, "invalid\x00"), "inspect existing database"},
		{filepath.Join(dir, strings.Repeat("x", 255)), "inspect database sidecar"},
	} {
		if err := checkPartialCopy(tc.path); err == nil || !strings.Contains(err.Error(), tc.message) {
			t.Fatalf("inspection %q: %v", tc.path, err)
		}
	}
	// An ancestor that is a regular file is ENOTDIR on Unix but may be
	// classified as not found on Windows. The public open must reject both.
	if st, err := Open(filepath.Join(blocked, "kb.db"), []byte("test-secret")); err == nil {
		st.Close()
		t.Fatal("opened a database under a regular file")
	}
	if err := prepareDatabaseFile(filepath.Join(dir, "missing", "kb.db")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing parent: %v", err)
	}
	// Publication failure must remove its own completed candidate and retain
	// unrelated evidence. The final name cannot be represented by this filesystem.
	if err := publishEmptyDatabase(filepath.Join(dir, "invalid\x00")); err == nil || !strings.Contains(err.Error(), "publish database") {
		t.Fatalf("invalid publication: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "file" {
		t.Fatalf("failed publication changed directory: %v, %v", entries, err)
	}
	if got, err := os.ReadFile(blocked); err != nil || string(got) != "keep" {
		t.Fatalf("inspection changed existing file: %q, %v", got, err)
	}
}

func TestOpenRefusesDamagedDatabaseWithoutReinitializing(t *testing.T) {
	for _, kind := range []string{"empty", "non-sqlite", "truncated", "constraint"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(backupSource(t), "kb.db")
			damageIntegrityFixture(t, path, kind)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			opened, err := Open(path, []byte("test-secret"))
			if opened != nil {
				opened.Close()
				t.Fatal("opened a damaged database")
			}
			if err == nil || !strings.Contains(err.Error(), "restore") {
				t.Fatalf("damage error=%v", err)
			}
			after, readErr := os.ReadFile(path)
			if readErr != nil || !bytes.Equal(before, after) {
				t.Fatalf("refusal changed database bytes: %v", readErr)
			}
			leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".kb-db-*"))
			if err != nil || len(leftovers) != 0 {
				t.Fatalf("refusal left database candidates: %v, %v", leftovers, err)
			}
		})
	}
}

func damageIntegrityFixture(t *testing.T, path, kind string) {
	t.Helper()
	switch kind {
	case "empty":
		if err := os.Truncate(path, 0); err != nil {
			t.Fatal(err)
		}
	case "non-sqlite":
		if err := os.WriteFile(path, []byte("not a SQLite database"), 0o600); err != nil {
			t.Fatal(err)
		}
	case "truncated":
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(path, info.Size()/2); err != nil {
			t.Fatal(err)
		}
	case "constraint":
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`PRAGMA ignore_check_constraints=ON; UPDATE label_sequence SET id=2`); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPartialCopiesRefusedWithEverySecretSource(t *testing.T) {
	for _, secretSource := range []string{"generated", "file", "environment"} {
		for _, orphan := range []string{"kb.db-wal", "kb.db-shm", "kb.db-journal", BackupIncompleteFile} {
			t.Run(secretSource+"/"+orphan, func(t *testing.T) {
				dir, path, content := partialCopyFixture(t, secretSource, orphan)
				assertPartialCopyRefused(t, dir, path, secretSource, content)
			})
		}
	}
}

func partialCopyFixture(t *testing.T, secretSource, orphan string) (string, string, []byte) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("KB_SECRET", "")
	if secretSource == "file" {
		if err := os.WriteFile(filepath.Join(dir, "secret"), bytes.Repeat([]byte("s"), 32), 0o600); err != nil {
			t.Fatal(err)
		}
	} else if secretSource == "environment" {
		t.Setenv("KB_SECRET", "external-secret-value")
	}
	path := filepath.Join(dir, orphan)
	content := []byte("partial copy evidence")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, path, content
}

func assertPartialCopyRefused(t *testing.T, dir, path, secretSource string, content []byte) {
	t.Helper()
	if _, err := LoadOrCreateSecret(dir); err == nil || !strings.Contains(err.Error(), "restore") {
		t.Fatalf("secret error=%v", err)
	}
	if st, err := Open(filepath.Join(dir, "kb.db"), []byte("external-secret-value")); err == nil {
		st.Close()
		t.Fatal("opened partial directory")
	}
	if _, err := os.Stat(filepath.Join(dir, "kb.db")); !os.IsNotExist(err) {
		t.Fatalf("created missing kb.db: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, content) {
		t.Fatalf("changed orphan: %v", err)
	}
	if secretSource != "file" {
		if _, err := os.Stat(filepath.Join(dir, "secret")); !os.IsNotExist(err) {
			t.Fatalf("created secret for partial copy: %v", err)
		}
	}
}

func TestDatabaseCandidateNeverReplacesExistingFiles(t *testing.T) {
	for _, content := range [][]byte{nil, []byte("damaged database evidence")} {
		dir := t.TempDir()
		path := filepath.Join(dir, "kb.db")
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		sentinel := filepath.Join(dir, ".kb-db-user-owned")
		if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := publishEmptyDatabase(path); err != nil {
			t.Fatal(err)
		}
		if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, content) {
			t.Fatalf("publication replaced existing database: %v", err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 2 {
			t.Fatalf("candidate cleanup changed directory: %v, %v", entries, err)
		}
		if got, err := os.ReadFile(sentinel); err != nil || string(got) != "keep" {
			t.Fatalf("candidate cleanup touched another file: %v", err)
		}
	}
}

func TestDatabaseCandidateFailureNeverPublishesOrRemovesOtherFiles(t *testing.T) {
	for _, kind := range []string{"closed", "invalid SQLite"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "kb.db")
			sentinel := filepath.Join(dir, ".kb-db-user-owned")
			if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			original := createDatabaseTemp
			t.Cleanup(func() { createDatabaseTemp = original })
			createDatabaseTemp = func(dir, pattern string) (*os.File, error) {
				return failedDatabaseCandidate(t, dir, pattern, kind), nil
			}
			err := prepareDatabaseFile(path)
			if err == nil || (kind == "closed" && !errors.Is(err, os.ErrClosed)) {
				t.Fatalf("candidate %s failure: %v", kind, err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed candidate published database: %v", err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(sentinel) {
				t.Fatalf("candidate cleanup changed unrelated files: %v, %v", entries, err)
			}
			if got, err := os.ReadFile(sentinel); err != nil || string(got) != "keep" {
				t.Fatalf("candidate failure changed sentinel: %q, %v", got, err)
			}
		})
	}
}

func failedDatabaseCandidate(t *testing.T, dir, pattern, kind string) *os.File {
	t.Helper()
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		t.Fatal(err)
	}
	if kind == "closed" {
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	} else if _, err := file.WriteString("not a SQLite database"); err != nil {
		t.Fatal(err)
	}
	return file
}
