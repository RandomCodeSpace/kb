package mcpserv

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

func TestRunLifecycleAndDisconnects(t *testing.T) {
	original := serveMCP
	t.Cleanup(func() { serveMCP = original })

	for _, tt := range []struct {
		name    string
		serve   error
		wantErr bool
	}{
		{name: "clean return"},
		{name: "EOF is a clean disconnect", serve: io.EOF},
		{name: "SDK close is a clean disconnect", serve: &jsonrpc.Error{Code: -32004}},
		{name: "serve failure propagates", serve: errors.New("transport failed"), wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			serveMCP = func(srv *mcp.Server) error {
				called = srv != nil
				return tt.serve
			}
			data := t.TempDir()
			err := Run(data, " Alice.Work ")
			if !called {
				t.Fatal("serveMCP was not called")
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("Run error = %v, wantErr %v", err, tt.wantErr)
			}
			if _, statErr := os.Stat(filepath.Join(data, "kb.db")); statErr != nil {
				t.Fatalf("database not created: %v", statErr)
			}
		})
	}
}

func TestRunRejectsInvalidInputsBeforeServing(t *testing.T) {
	original := serveMCP
	t.Cleanup(func() { serveMCP = original })
	serveMCP = func(*mcp.Server) error {
		t.Fatal("serveMCP called for invalid input")
		return nil
	}

	if err := Run(t.TempDir(), "bad/user"); err == nil || !strings.Contains(err.Error(), "mcpserv") {
		t.Fatalf("invalid user error = %v", err)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Run(file, "tester"); err == nil || !strings.Contains(err.Error(), "create data dir") {
		t.Fatalf("invalid data dir error = %v", err)
	}
}

func TestDisconnectClassification(t *testing.T) {
	if isClientDisconnect(nil) {
		t.Fatal("nil is not a disconnect error")
	}
	if !isClientDisconnect(io.EOF) || !isClientDisconnect(errors.Join(errors.New("wrapped"), io.EOF)) {
		t.Fatal("EOF should classify as a disconnect")
	}
	if !isClientDisconnect(&jsonrpc.Error{Code: -32004}) {
		t.Fatal("wire close should classify as a disconnect")
	}
	if isClientDisconnect(&jsonrpc.Error{Code: -32001}) || isClientDisconnect(errors.New("other")) {
		t.Fatal("unrelated errors classified as disconnects")
	}
}

func TestNormalizeUserCoverageCases(t *testing.T) {
	for _, tt := range []struct {
		in, want string
		wantErr  bool
	}{
		{"", "default", false},
		{"  ", "default", false},
		{" Alice.Work ", "alice.work", false},
		{"bad/user", "", true},
	} {
		got, err := normalizeUser(tt.in)
		if got != tt.want || (err != nil) != tt.wantErr {
			t.Errorf("normalizeUser(%q) = %q, %v", tt.in, got, err)
		}
	}
}

func TestFindTaskResolution(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "kb.db")
	st, err := store.Open(dbPath, []byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	k := &kb{st: st, user: "tester"}

	first, err := st.AddTask("tester", board.Task{Title: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := k.findTask(first.ID); err != nil || got.ID != first.ID {
		t.Fatalf("exact task = %+v, %v", got, err)
	}
	// The 9-char prefix includes the UUID's first hyphen, so it can never
	// parse as an all-digit sequence reference.
	if got, err := k.findTask(first.ID[:9]); err != nil || got.ID != first.ID {
		t.Fatalf("unique prefix = %+v, %v", got, err)
	}
	if _, err := k.findTask(""); err == nil || !strings.Contains(err.Error(), "list_tasks") {
		t.Fatalf("empty prefix error = %v", err)
	}
	if _, err := k.findTask("does-not-exist"); err == nil || !strings.Contains(err.Error(), "no task matches") {
		t.Fatalf("missing prefix error = %v", err)
	}

	prefix := first.ID[:9]
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=temp_store(2)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	secondID := prefix + "0000-4000-8000-000000000001"
	if _, err := db.Exec(`INSERT INTO tasks (id, user, title, status, created_at, moved_at) VALUES (?, 'tester', 'candidate', 'todo', ?, ?)`, secondID, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := k.findTask(prefix); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous prefix error = %v", err)
	}
}

func TestHandlerValidationAndStoreFailures(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("KB_PROJECT", testProject)
	st, err := store.Open(filepath.Join(dataDir, "kb.db"), []byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	k := &kb{st: st, user: "tester", dataDir: dataDir}
	ctx := context.Background()

	if _, _, err := k.listTasks(ctx, nil, listTasksInput{Status: "bogus"}); err == nil {
		t.Fatal("listTasks accepted an invalid status")
	}
	if _, _, err := k.addTask(ctx, nil, addTaskInput{Title: "  "}); err == nil {
		t.Fatal("addTask accepted an empty title")
	}
	if _, _, err := k.addTask(ctx, nil, addTaskInput{Title: "x", Status: "bogus"}); err == nil {
		t.Fatal("addTask accepted an invalid status")
	}
	if _, _, err := k.addTask(ctx, nil, addTaskInput{Title: "x", Prio: 5}); err == nil {
		t.Fatal("addTask accepted an invalid priority")
	}
	badPrio := 0
	if _, _, err := k.updateTask(ctx, nil, updateTaskInput{ID: "missing", Prio: &badPrio}); err == nil {
		t.Fatal("updateTask accepted an invalid priority")
	}
	if _, _, err := k.moveTask(ctx, nil, moveTaskInput{ID: "missing", Status: "bogus"}); err == nil {
		t.Fatal("moveTask accepted an invalid status")
	}

	const link = "link::github#coverage"
	for i := 0; i < duplicateCheckMaxCandidates+2; i++ {
		if _, err := st.AddTask("tester", board.Task{
			Title: "duplicate candidate",
			Tags:  []string{link},
		}); err != nil {
			t.Fatal(err)
		}
	}
	_, out, err := k.duplicateCheck(ctx, nil, duplicateCheckInput{Link: link})
	if err != nil {
		t.Fatalf("duplicateCheck: %v", err)
	}
	if len(out.Candidates) != duplicateCheckMaxCandidates {
		t.Fatalf("duplicate candidates = %d, want %d", len(out.Candidates), duplicateCheckMaxCandidates)
	}

	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := k.listTasks(ctx, nil, listTasksInput{}); err == nil {
		t.Fatal("listTasks hid a closed-store error")
	}
	if _, _, err := k.searchSimilar(ctx, nil, searchSimilarInput{Query: "x"}); err == nil {
		t.Fatal("searchSimilar hid a closed-store error")
	}
	if _, _, err := k.duplicateCheck(ctx, nil, duplicateCheckInput{Link: link}); err == nil {
		t.Fatal("duplicateCheck link lookup hid a closed-store error")
	}
	if _, _, err := k.duplicateCheck(ctx, nil, duplicateCheckInput{Title: "x"}); err == nil {
		t.Fatal("duplicateCheck search hid a closed-store error")
	}
	if _, _, err := k.addTask(ctx, nil, addTaskInput{Title: "x"}); err == nil {
		t.Fatal("addTask hid a closed-store error")
	}
	if _, _, err := k.deleteTask(ctx, nil, deleteTaskInput{ID: "x"}); err == nil {
		t.Fatal("soft delete hid a closed-store error")
	}
	hard := false
	if _, _, err := k.deleteTask(ctx, nil, deleteTaskInput{ID: "x", Soft: &hard}); err == nil {
		t.Fatal("hard delete hid a closed-store error")
	}
	if _, err := k.findTask("x"); err == nil {
		t.Fatal("findTask hid a closed-store error")
	}
	if err := k.idError(store.ErrAmbiguous, "x"); err == nil || !strings.Contains(err.Error(), "retry with a longer prefix") {
		t.Fatalf("ambiguous closed-store error = %v", err)
	}
}
