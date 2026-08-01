package mcpserv

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

func TestDuplicateCheckFiltersWeakSimilarityAndKeepsRankedMatches(t *testing.T) {
	_, st := connectWithStore(t)
	weak, err := st.AddTask("tester", board.Task{Title: "Add SSO login for the admin portal"})
	if err != nil {
		t.Fatalf("seed weak candidate: %v", err)
	}
	strong, err := st.AddTask("tester", board.Task{Title: "Add dark mode toggle"})
	if err != nil {
		t.Fatalf("seed strong candidate: %v", err)
	}
	k := &kb{st: st, user: "tester"}

	_, out, err := k.duplicateCheck(context.Background(), nil, duplicateCheckInput{Title: "dark mode toggle"})
	if err != nil {
		t.Fatalf("duplicateCheck: %v", err)
	}
	if len(out.Candidates) != 1 || out.Candidates[0].ID != strong.ID {
		t.Fatalf("candidates = %+v, want ranked strong match %q only; weak=%q", out.Candidates, strong.ID, weak.ID)
	}
}

func TestDuplicateCheckEmptyInputReturnsEmptyCandidatesWithoutMutation(t *testing.T) {
	_, st := connectWithStore(t)
	if _, err := st.AddTask("tester", board.Task{Title: "Existing"}); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	before, err := st.ReadBoardSnapshot("tester")
	if err != nil {
		t.Fatalf("snapshot before: %v", err)
	}
	k := &kb{st: st, user: "tester"}

	_, out, err := k.duplicateCheck(context.Background(), nil, duplicateCheckInput{})
	if err != nil {
		t.Fatalf("duplicateCheck empty input: %v", err)
	}
	if out.Candidates == nil || len(out.Candidates) != 0 {
		t.Fatalf("empty candidates = %#v, want non-nil empty slice", out.Candidates)
	}
	after, err := st.ReadBoardSnapshot("tester")
	if err != nil {
		t.Fatalf("snapshot after: %v", err)
	}
	if after.Revision != before.Revision || len(after.Board.Tasks) != 1 || after.Board.Tasks[0].ID != before.Board.Tasks[0].ID {
		t.Fatalf("duplicate check mutated board: before=%+v after=%+v", before, after)
	}
}

func TestDuplicateCheckSearchErrorReturnsNoPartialCandidatesOrMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb.db")
	st, err := store.Open(path, []byte("test-secret"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.AddTask("tester", board.Task{Title: "Exact candidate", Tags: []string{"link::issue#1"}}); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	before, err := st.ReadBoardSnapshot("tester")
	if err != nil {
		t.Fatalf("snapshot before: %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open fixture database: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE tasks_fts`); err != nil {
		_ = db.Close()
		t.Fatalf("break search fixture: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close fixture database: %v", err)
	}
	k := &kb{st: st, user: "tester"}

	_, out, err := k.duplicateCheck(context.Background(), nil, duplicateCheckInput{
		Title: "Exact candidate",
		Link:  "link::issue#1",
	})
	if err == nil {
		t.Fatal("duplicateCheck hid the similarity search error")
	}
	if out.Candidates != nil {
		t.Fatalf("error returned partial candidates: %+v", out.Candidates)
	}
	after, snapshotErr := st.ReadBoardSnapshot("tester")
	if snapshotErr != nil {
		t.Fatalf("snapshot after: %v", snapshotErr)
	}
	if after.Revision != before.Revision || len(after.Board.Tasks) != 1 || after.Board.Tasks[0].ID != before.Board.Tasks[0].ID {
		t.Fatalf("failed duplicate check mutated board: before=%+v after=%+v", before, after)
	}
}
