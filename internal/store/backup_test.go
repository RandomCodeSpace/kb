package store

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/kb/internal/board"
)

func backupSource(t *testing.T) string {
	t.Helper()
	t.Setenv("KB_SECRET", "")
	dir := t.TempDir()
	secret, err := LoadOrCreateSecret(dir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(dir, "kb.db"), secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTask("default", board.Task{Title: "original", Tags: []string{"project::backup"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "zz-last.txt"), []byte("portable user file"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func assertBackupSourceUsable(t *testing.T, source string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(source, "kb.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var mode, title string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("source mode=%q err=%v", mode, err)
	}
	if err := db.QueryRow(`SELECT title FROM tasks WHERE user='default'`).Scan(&title); err != nil || title != "original" {
		t.Fatalf("source title=%q err=%v", title, err)
	}
	if _, err := db.Exec(`UPDATE tasks SET title=title WHERE user='default'`); err != nil {
		t.Fatalf("source no longer writable: %v", err)
	}
}

func TestBackupRefusesExistingAndNestedDestinations(t *testing.T) {
	source := backupSource(t)
	for _, destination := range []string{source, filepath.Join(source, "nested"), t.TempDir()} {
		if err := BackupDirectory(source, destination); err == nil {
			t.Fatalf("accepted destination %s", destination)
		}
	}
	destination := filepath.Join(t.TempDir(), "existing")
	want := []byte("do not overwrite")
	if err := os.WriteFile(destination, want, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := BackupDirectory(source, destination); !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing file error=%v", err)
	}
	if got, err := os.ReadFile(destination); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("destination changed: %v", err)
	}
	assertBackupSourceUsable(t, source)
}

func TestBackupRefusesDatabaseAliasesAndSymlinks(t *testing.T) {
	source := backupSource(t)
	alias := filepath.Join(source, "database-alias")
	if err := os.Link(filepath.Join(source, "kb.db"), alias); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "backup")
	if err := BackupDirectory(source, destination); err == nil || !strings.Contains(err.Error(), "another link") {
		t.Fatalf("alias error=%v", err)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("zz-last.txt", alias); err != nil {
		t.Fatal(err)
	}
	if err := BackupDirectory(source, destination); err == nil || !strings.Contains(err.Error(), "symbolic links") {
		t.Fatalf("symlink error=%v", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("refusal created destination: %v", err)
	}
	assertBackupSourceUsable(t, source)
}

func TestBackupFailureKeepsMarkerAndRestoresSource(t *testing.T) {
	source := backupSource(t)
	destination := filepath.Join(t.TempDir(), "backup")
	want := errors.New("injected copy failure")
	err := backupDirectory(source, destination, func(input io.Reader, target string) error {
		if filepath.Base(target) == "zz-last.txt" {
			return want
		}
		return copyBackupFile(input, target)
	})
	if !errors.Is(err, want) {
		t.Fatalf("copy failure=%v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, BackupIncompleteFile)); err != nil {
		t.Fatalf("incomplete marker missing: %v", err)
	}
	if _, err := LoadOrCreateSecret(destination); err == nil || !strings.Contains(err.Error(), "incomplete backup") {
		t.Fatalf("incomplete restore error=%v", err)
	}
	if err := BackupDirectory(source, destination); !errors.Is(err, os.ErrExist) {
		t.Fatalf("retry overwrites incomplete backup: %v", err)
	}
	assertBackupSourceUsable(t, source)
}

// Each helper is a separate process so a raw-file close cannot hide a lost
// POSIX lock behind SQLite's bookkeeping within the current process.
func TestBackupProcessHelper(t *testing.T) {
	mode := os.Getenv("KB_TEST_BACKUP_MODE")
	if mode == "" {
		return
	}
	path := os.Getenv("KB_TEST_BACKUP_PATH")
	if mode == "crash" {
		secret, err := LoadOrCreateSecret(filepath.Dir(path))
		if err != nil {
			t.Fatal(err)
		}
		s, err := Open(path, secret)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.AddTask("default", board.Task{Title: "committed before crash"}); err != nil {
			t.Fatal(err)
		}
		os.Exit(0) // Leave the committed WAL exactly as a stopped/crashed writer would.
	}
	if mode == "open" {
		fmt.Println("ready")
		_, _ = io.Copy(io.Discard, os.Stdin)
		s, err := Open(path, []byte("concurrent-first-open-secret"))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		return
	}
	dsn, err := SQLiteDSN(path, "busy_timeout(0)")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if mode == "try" {
		_, err := db.Exec(`UPDATE tasks SET title='unexpected concurrent write' WHERE user='default'`)
		if !isSQLiteBusy(err) {
			t.Fatalf("writer escaped backup lock: %v", err)
		}
		return
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM tasks`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	fmt.Println("ready")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func backupProcess(t *testing.T, path, mode string) *exec.Cmd {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBackupProcessHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), "KB_TEST_BACKUP_PATH="+path, "KB_TEST_BACKUP_MODE="+mode)
	return cmd
}

func startBackupProcess(t *testing.T, path, mode string) (release func(), wait func()) {
	t.Helper()
	cmd := backupProcess(t, path, mode)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil || line != "ready\n" {
		t.Fatalf("helper startup=%q err=%v stderr=%s", line, err, stderr.String())
	}
	return func() {
			if err := input.Close(); err != nil {
				t.Fatal(err)
			}
		}, func() {
			if err := cmd.Wait(); err != nil {
				t.Fatalf("helper exit=%v stderr=%s", err, stderr.String())
			}
		}
}

func TestBackupRefusesAnOpenProcess(t *testing.T) {
	source := backupSource(t)
	release, wait := startBackupProcess(t, filepath.Join(source, "kb.db"), "hold")
	destination := filepath.Join(t.TempDir(), "backup")
	err := BackupDirectory(source, destination)
	release()
	wait()
	if !errors.Is(err, ErrBackupBusy) {
		t.Fatalf("active source error=%v", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("busy backup created destination: %v", err)
	}
	assertBackupSourceUsable(t, source)
}

func TestBackupHoldsExclusionAfterDatabaseBytesAreCopied(t *testing.T) {
	source := backupSource(t)
	destination := filepath.Join(t.TempDir(), "backup")
	checked := false
	err := backupDirectory(source, destination, func(input io.Reader, target string) error {
		if filepath.Base(target) == "zz-last.txt" {
			checked = true
			cmd := backupProcess(t, filepath.Join(source, "kb.db"), "try")
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("lock probe: %s: %w", out, err)
			}
		}
		return copyBackupFile(input, target)
	})
	if err != nil || !checked {
		t.Fatalf("backup=%v probeRan=%v", err, checked)
	}
	if _, err := os.Stat(filepath.Join(destination, BackupIncompleteFile)); !os.IsNotExist(err) {
		t.Fatalf("complete copy still marked incomplete: %v", err)
	}
	assertBackupSourceUsable(t, source)
}

func TestFreshDatabasePublicationAcrossProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb.db")
	releaseFirst, waitFirst := startBackupProcess(t, path, "open")
	releaseSecond, waitSecond := startBackupProcess(t, path, "open")
	// Both children are ready before either can initialize the same filename.
	releaseFirst()
	releaseSecond()
	waitFirst()
	waitSecond()
	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".kb-db-*"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("publication candidates remain: %v err=%v", leftovers, err)
	}
	s, err := Open(path, []byte("concurrent-first-open-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestBackupCheckpointsCommittedWALFromStoppedProcess(t *testing.T) {
	source := backupSource(t)
	path := filepath.Join(source, "kb.db")
	cmd := backupProcess(t, path, "crash")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("crashed writer: %s, %v", out, err)
	}
	if info, err := os.Stat(path + "-wal"); err != nil || info.Size() == 0 {
		t.Fatalf("committed WAL absent: %v", err)
	}
	destination := filepath.Join(t.TempDir(), "backup")
	if err := BackupDirectory(source, destination); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{source, destination} {
		secret, err := LoadOrCreateSecret(dir)
		if err != nil {
			t.Fatal(err)
		}
		s, err := Open(filepath.Join(dir, "kb.db"), secret)
		if err != nil {
			t.Fatal(err)
		}
		task, err := s.Task("default", "2")
		if err != nil || task.Title != "committed before crash" {
			t.Fatalf("lost committed WAL task in %s: %+v, %v", dir, task, err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBackupRejectsIncompleteSourcesBeforeCreatingDestination(t *testing.T) {
	for _, kind := range []string{"missing database", "empty database", "damaged database", "missing secret", "incomplete backup"} {
		t.Run(kind, func(t *testing.T) {
			source := backupSource(t)
			path := filepath.Join(source, "kb.db")
			switch kind {
			case "missing database":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "empty database":
				if err := os.Truncate(path, 0); err != nil {
					t.Fatal(err)
				}
			case "damaged database":
				if err := os.WriteFile(path, []byte("damaged data"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "missing secret":
				if err := os.Remove(filepath.Join(source, "secret")); err != nil {
					t.Fatal(err)
				}
			case "incomplete backup":
				if err := os.WriteFile(filepath.Join(source, BackupIncompleteFile), []byte("unfinished"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			destination := filepath.Join(t.TempDir(), "backup")
			if err := BackupDirectory(source, destination); err == nil {
				t.Fatal("accepted incomplete source")
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatalf("refusal created destination: %v", err)
			}
		})
	}
}
