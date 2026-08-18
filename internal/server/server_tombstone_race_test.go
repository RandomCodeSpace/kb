package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

// TestTombstonePostRestoreCommitOrders uses separate Store handles so the
// assertion covers SQLite's cross-process serialization, not just one Go
// object's call ordering.
func TestTombstonePostRestoreCommitOrders(t *testing.T) {
	for _, postFirst := range []bool{true, false} {
		name := "restore commits before post"
		if postFirst {
			name = "post commits before restore"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "kb.db")
			primary, err := store.Open(path, []byte("test-secret"))
			if err != nil {
				t.Fatalf("open primary store: %v", err)
			}
			t.Cleanup(func() { _ = primary.Close() })
			secondary, err := store.Open(path, []byte("test-secret"))
			if err != nil {
				t.Fatalf("open secondary store: %v", err)
			}
			t.Cleanup(func() { _ = secondary.Close() })

			task, err := primary.AddTask("default", board.Task{Title: "Race target", Status: board.StatusCancelled})
			if err != nil {
				t.Fatalf("AddTask: %v", err)
			}
			h := New(Config{}, primary)
			post := func() *httptest.ResponseRecorder {
				return doReq(t, h, http.MethodPost, "/api/tombstones", tombstoneBody(t, task.ID, "Race reason"), nil)
			}
			restore := func() {
				if _, err := secondary.MoveTask("default", task.ID, board.StatusTodo); err != nil {
					t.Fatalf("restore task: %v", err)
				}
			}

			if postFirst {
				if w := post(); w.Code != http.StatusNoContent {
					t.Fatalf("POST before restore = %d %q", w.Code, w.Body)
				}
				restore()
			} else {
				restore()
				if w := post(); w.Code != http.StatusConflict {
					t.Fatalf("POST after restore = %d %q, want 409", w.Code, w.Body)
				}
			}

			finalBoard, err := primary.Board("default")
			if err != nil || len(finalBoard.Tasks) != 1 || finalBoard.Tasks[0].ID != task.ID || finalBoard.Tasks[0].Status != board.StatusTodo {
				t.Fatalf("final board = %+v, %v, want one active race target", finalBoard, err)
			}
			if got, found, err := primary.Tombstone("default", task.ID); err != nil || found {
				t.Fatalf("final tombstone = %+v, %t, %v, want absent", got, found, err)
			}
		})
	}
}
