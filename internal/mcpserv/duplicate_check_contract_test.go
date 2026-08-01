package mcpserv

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
	"modernc.org/sqlite"
)

const duplicateCheckUser = "tester"

func TestDuplicateCheckFiltersWeakSimilarityAndKeepsRankedMatches(t *testing.T) {
	_, st := connectWithStore(t)
	const query = "dark mode toggle"
	fixtures := []struct {
		title    string
		accepted bool
	}{
		{title: "dark mode toggle", accepted: true},
		{title: "dark mode preferences", accepted: true},
		{title: "dark account billing", accepted: false},
	}
	ids := make([]string, len(fixtures))
	for i, fixture := range fixtures {
		score := store.Similarity(query, fixture.title)
		if fixture.accepted != (score >= store.SimilarityFloor) {
			t.Fatalf("fixture %q score=%v floor=%v accepted=%v", fixture.title, score, store.SimilarityFloor, fixture.accepted)
		}
		added, err := st.AddTask(duplicateCheckUser, board.Task{Title: fixture.title})
		if err != nil {
			t.Fatalf("seed candidate %q: %v", fixture.title, err)
		}
		ids[i] = added.ID
	}
	if high, low := store.Similarity(query, fixtures[0].title), store.Similarity(query, fixtures[1].title); high <= low {
		t.Fatalf("accepted fixture scores high=%v low=%v, want distinct descending scores", high, low)
	}
	before, err := st.ReadBoardSnapshot(duplicateCheckUser)
	if err != nil {
		t.Fatalf("snapshot before: %v", err)
	}
	k := &kb{st: st, user: duplicateCheckUser}

	_, out, err := k.duplicateCheck(context.Background(), nil, duplicateCheckInput{Title: query})
	if err != nil {
		t.Fatalf("duplicateCheck: %v", err)
	}
	if len(out.Candidates) != 2 || out.Candidates[0].ID != ids[0] || out.Candidates[1].ID != ids[1] {
		t.Fatalf("candidates = %+v, want exact order [%q %q] and filtered %q", out.Candidates, ids[0], ids[1], ids[2])
	}
	if after, err := st.ReadBoardSnapshot(duplicateCheckUser); err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("duplicate check mutated board: before=%+v after=%+v err=%v", before, after, err)
	}
}

func TestDuplicateCheckEmptyInputReturnsEmptyCandidatesWithoutMutation(t *testing.T) {
	_, st := connectWithStore(t)
	if _, err := st.AddTask(duplicateCheckUser, board.Task{Title: "Existing"}); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	before, err := st.ReadBoardSnapshot(duplicateCheckUser)
	if err != nil {
		t.Fatalf("snapshot before: %v", err)
	}
	k := &kb{st: st, user: duplicateCheckUser}

	_, out, err := k.duplicateCheck(context.Background(), nil, duplicateCheckInput{})
	if err != nil {
		t.Fatalf("duplicateCheck empty input: %v", err)
	}
	if out.Candidates == nil || len(out.Candidates) != 0 {
		t.Fatalf("empty candidates = %#v, want non-nil empty slice", out.Candidates)
	}
	after, err := st.ReadBoardSnapshot(duplicateCheckUser)
	if err != nil {
		t.Fatalf("snapshot after: %v", err)
	}
	if !reflect.DeepEqual(after, before) {
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
	if _, err := st.AddTask(duplicateCheckUser, board.Task{Title: "Exact candidate", Tags: []string{"link::issue#1"}}); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	before, err := st.ReadBoardSnapshot(duplicateCheckUser)
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
	k := &kb{st: st, user: duplicateCheckUser}

	_, out, err := k.duplicateCheck(context.Background(), nil, duplicateCheckInput{
		Title: "Exact candidate",
		Link:  "link::issue#1",
	})
	if err == nil {
		t.Fatal("duplicateCheck hid the similarity search error")
	}
	if !strings.Contains(err.Error(), "store: search tasks:") {
		t.Fatalf("duplicateCheck error = %q, want search wrapper", err)
	}
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) || sqliteErr.Code() != 1 {
		t.Fatalf("duplicateCheck error = %v, want wrapped SQLite error code 1", err)
	}
	if out.Candidates != nil {
		t.Fatalf("error returned partial candidates: %+v", out.Candidates)
	}
	after, snapshotErr := st.ReadBoardSnapshot(duplicateCheckUser)
	if snapshotErr != nil {
		t.Fatalf("snapshot after: %v", snapshotErr)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("failed duplicate check mutated board: before=%+v after=%+v", before, after)
	}
}
