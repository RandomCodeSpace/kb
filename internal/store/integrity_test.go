package store

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
)

func TestOpenRefusesDamagedDatabaseWithoutReinitializing(t *testing.T) {
	for _, kind := range []string{"empty", "non-sqlite", "truncated", "constraint"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "kb.db")
			s, err := Open(path, []byte("test-secret"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.AddTask("default", board.Task{Title: "original"}); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
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

func TestPartialCopiesRefusedWithEverySecretSource(t *testing.T) {
	for _, secretSource := range []string{"generated", "file", "environment"} {
		for _, orphan := range []string{"kb.db-wal", "kb.db-shm", "kb.db-journal", BackupIncompleteFile} {
			t.Run(secretSource+"/"+orphan, func(t *testing.T) {
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
			})
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
