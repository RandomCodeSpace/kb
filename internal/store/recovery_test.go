package store

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
)

func TestColdCopyRecoveryRoundTrip(t *testing.T) {
	for _, test := range []struct {
		name      string
		envSecret string
	}{
		{name: "generated secret"},
		{name: "external secret", envSecret: "recovery-external-secret-value"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("KB_SECRET", test.envSecret)
			root := t.TempDir()
			sourceDir := filepath.Join(root, "source")
			restoredDir := filepath.Join(root, "restored-at-a-different-path")
			if err := os.MkdirAll(sourceDir, 0o700); err != nil {
				t.Fatal(err)
			}
			secret, err := LoadOrCreateSecret(sourceDir)
			if err != nil {
				t.Fatal(err)
			}
			source, err := Open(filepath.Join(sourceDir, "kb.db"), secret)
			if err != nil {
				t.Fatal(err)
			}

			blocker, err := source.AddTask("default", board.Task{
				Title: "Portable blocker", Desc: "survives a cold copy", Prio: board.PrioHigh,
				Tags:   []string{"project::recovery", "durable"},
				Checks: []board.Check{{Text: "copy the whole directory", Done: true}},
			})
			if err != nil {
				t.Fatal(err)
			}
			blocked, err := source.AddTask("default", board.Task{
				Title: "Portable blocked task", Status: board.StatusDoing,
				Tags: []string{"project::recovery"}, Blocked: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			comment, err := source.AddComment("default", strconv.Itoa(blocker.Seq), "tester", "cold-copy comment")
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := source.Link("default", strconv.Itoa(blocker.Seq), strconv.Itoa(blocked.Seq)); err != nil {
				t.Fatal(err)
			}
			reason := "kept as recovery history"
			cancelled, err := source.AddTask("default", board.Task{Title: "Cancelled durable task", Tags: []string{"project::archive"}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := source.CancelTask("default", strconv.Itoa(cancelled.Seq), &reason); err != nil {
				t.Fatal(err)
			}
			imported, err := source.AddTaskWithImportLink("default", board.Task{
				Title: "Imported durable task", Tags: []string{"project::recovery"},
			}, ImportLink{
				Source: "origin", Kind: "github", ExternalKey: "github:example/repo#42",
				Link: "example/repo#42", URL: "https://github.com/example/repo/issues/42", Title: "Upstream issue",
			}, NewImportBaseline("Upstream issue", "Recovered upstream body", "2026-08-31T00:00:00Z"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := source.AddTask("foreign", board.Task{Title: "Foreign namespace task", Tags: []string{"project::foreign"}}); err != nil {
				t.Fatal(err)
			}

			aiURL, aiModel, aiKey := "https://ai.example.test/v1", "recovery-model", "ai-secret"
			if _, err := source.SetAISettings("default", &aiURL, &aiModel, &aiKey); err != nil {
				t.Fatal(err)
			}
			forgeURL, forgePAT := "https://github.example.test", "forge-secret"
			if _, err := source.SetForgeSource("default", "origin", "github", &forgeURL, &forgePAT); err != nil {
				t.Fatal(err)
			}
			if err := writeRecoveryFiles(sourceDir); err != nil {
				t.Fatal(err)
			}

			wantDefault, err := source.Board("default")
			if err != nil {
				t.Fatal(err)
			}
			wantForeign, err := source.Board("foreign")
			if err != nil {
				t.Fatal(err)
			}
			if err := source.Close(); err != nil {
				t.Fatal(err)
			}
			if err := copyRecoveryTree(sourceDir, restoredDir); err != nil {
				t.Fatal(err)
			}

			restoredSecret, err := LoadOrCreateSecret(restoredDir)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(restoredSecret, secret) {
				t.Fatalf("restored secret differs from original")
			}
			restored, err := Open(filepath.Join(restoredDir, "kb.db"), restoredSecret)
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()

			var integrity string
			if err := restored.db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
				t.Fatalf("restored integrity = %q, %v", integrity, err)
			}
			var schemaVersion int
			if err := restored.db.QueryRow(`SELECT v FROM meta WHERE k='schema_version'`).Scan(&schemaVersion); err != nil || schemaVersion != len(migrations) {
				t.Fatalf("restored schema = %d, %v, want %d", schemaVersion, err, len(migrations))
			}
			gotDefault, err := restored.Board("default")
			if err != nil || !reflect.DeepEqual(gotDefault, wantDefault) {
				t.Fatalf("restored default board differs: %+v, %v", gotDefault, err)
			}
			gotForeign, err := restored.Board("foreign")
			if err != nil || !reflect.DeepEqual(gotForeign, wantForeign) {
				t.Fatalf("restored foreign board differs: %+v, %v", gotForeign, err)
			}
			comments, err := restored.Comments("default", strconv.Itoa(blocker.Seq))
			if err != nil || len(comments) != 1 || comments[0].ID != comment.ID || comments[0].Body != comment.Body {
				t.Fatalf("restored comments = %+v, %v", comments, err)
			}
			links, err := restored.TaskLinks("default", blocker.ID)
			if err != nil || len(links.Blocks) != 1 || links.Blocks[0].ID != blocked.ID {
				t.Fatalf("restored blocker links = %+v, %v", links, err)
			}
			hits, err := restored.SearchSimilar("default", "Portable blocker", "", nil, 5)
			if err != nil || len(hits) == 0 || hits[0].ID != blocker.ID {
				t.Fatalf("restored search hits = %+v, %v", hits, err)
			}
			if got, err := restored.AIKey("default"); err != nil || got != aiKey {
				t.Fatalf("restored AI key = %q, %v", got, err)
			}
			kind, baseURL, pat, err := restored.ForgePAT("default", "origin")
			if err != nil || kind != "github" || baseURL != forgeURL || pat != forgePAT {
				t.Fatalf("restored forge = %q, %q, %q, %v", kind, baseURL, pat, err)
			}
			var provenanceTitle string
			if err := restored.db.QueryRow(`SELECT title FROM import_links WHERE scope=? AND external_key=?`,
				"default", "github:example/repo#42").Scan(&provenanceTitle); err != nil || provenanceTitle != "Upstream issue" {
				t.Fatalf("restored provenance title = %q, %v", provenanceTitle, err)
			}
			var tombstones int
			if err := restored.db.QueryRow(`SELECT COUNT(*) FROM tombstones WHERE scope=? AND task_id=?`,
				"default", cancelled.ID).Scan(&tombstones); err != nil || tombstones != 1 {
				t.Fatalf("restored tombstones = %d, %v", tombstones, err)
			}
			assertRecoveryFiles(t, sourceDir, restoredDir)

			newTask, err := restored.AddTask("default", board.Task{Title: "Written after restore", Tags: []string{"project::recovery"}})
			if err != nil || newTask.Seq <= imported.Seq {
				t.Fatalf("post-restore task = %+v, %v", newTask, err)
			}
			newComment, err := restored.AddComment("default", strconv.Itoa(newTask.Seq), "tester", "after restore")
			if err != nil || newComment.ID <= comment.ID {
				t.Fatalf("post-restore comment = %+v, %v", newComment, err)
			}
			if got, err := restored.Task("default", strconv.Itoa(newTask.Seq)); err != nil || got.ID != newTask.ID {
				t.Fatalf("post-restore read = %+v, %v", got, err)
			}
		})
	}
}

var recoveryFiles = map[string][]byte{
	"state.json":                      []byte("{\"project\":\"recovery\"}\n"),
	".kb-tui/preferences.json":        []byte("{\"show_cancelled\":true,\"project\":\"recovery\"}\n"),
	"skills/custom-recovery-skill.md": []byte("# Recovery skill\n\nKeep this file.\n"),
	"unknown-user-file.bin":           {0x00, 0x01, 0xfe, 0xff},
}

func writeRecoveryFiles(root string) error {
	for name, content := range recoveryFiles {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func assertRecoveryFiles(t *testing.T, source, restored string) {
	t.Helper()
	for name := range recoveryFiles {
		want, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(restored, filepath.FromSlash(name)))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("restored file %s differs: %x, %v", name, got, err)
		}
	}
}

func copyRecoveryTree(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o700)
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		info, err := entry.Info()
		if err != nil {
			return err
		}
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(output, input); err != nil {
			_ = output.Close()
			return err
		}
		return output.Close()
	})
}
