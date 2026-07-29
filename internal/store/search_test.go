package store

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
)

func TestFtsQueryEscapingTreatsHostileInputAsLiteralTerms(t *testing.T) {
	s := newStore(t)
	unrelated := addSearchTask(t, s, board.Task{Title: "unrelated sentinel"})
	literal := addSearchTask(t, s, board.Task{Title: "AND OR NOT NEAR a b title x"})
	tests := []struct {
		raw            string
		want           string
		matchesLiteral bool
	}{
		{`"`, `""""`, false},
		{`""`, `""""""`, false},
		{`AND OR NOT`, `"AND" OR "OR" OR "NOT"`, true},
		{`NEAR(a,b)`, `"NEAR(a,b)"`, true},
		{`title:x`, `"title:x"`, true},
		{`*`, `"*"`, false},
		{`-x`, `"-x"`, true},
		{`(`, `"("`, false},
		{`😀`, `"😀"`, false},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			if got := FtsQuery(tt.raw); got != tt.want {
				t.Fatalf("FtsQuery(%q) = %q, want %q", tt.raw, got, tt.want)
			}
			hits, err := s.SearchSimilar("alice", tt.raw, "", 10)
			if err != nil {
				t.Fatalf("SearchSimilar(%q): %v", tt.raw, err)
			}
			foundLiteral := false
			for _, hit := range hits {
				if hit.ID == unrelated.ID {
					t.Fatalf("SearchSimilar(%q) matched unrelated card %+v", tt.raw, hit)
				}
				foundLiteral = foundLiteral || hit.ID == literal.ID
			}
			if tt.matchesLiteral && !foundLiteral {
				t.Fatalf("SearchSimilar(%q) = %+v, want literal card %q", tt.raw, hits, literal.ID)
			}
		})
	}
}

func TestFtsQueryCapsInputAndEmptySearchDoesNoWork(t *testing.T) {
	var tokens, quoted []string
	for i := range 200 {
		tokens = append(tokens, fmt.Sprintf("tok%03d", i))
	}
	for _, token := range tokens[:12] {
		quoted = append(quoted, `"`+token+`"`)
	}
	if got, want := FtsQuery(strings.Join(tokens, " ")), strings.Join(quoted, " OR "); got != want {
		t.Fatalf("FtsQuery(200 tokens) = %q, want %q", got, want)
	}
	for _, raw := range []string{"", " \t\n "} {
		if got := FtsQuery(raw); got != "" {
			t.Fatalf("FtsQuery(%q) = %q, want empty", raw, got)
		}
		hits, err := newStore(t).SearchSimilar("alice", raw, "", 3)
		if err != nil || hits != nil {
			t.Fatalf("SearchSimilar(%q) = %#v, %v, want nil, nil", raw, hits, err)
		}
	}
	if hits, err := newStore(t).SearchSimilar("alice", "card", "", 0); err != nil || hits != nil {
		t.Fatalf("SearchSimilar(limit=0) = %#v, %v, want nil, nil", hits, err)
	}
}

func TestFTSWriteThroughTracksEveryTaskWritePath(t *testing.T) {
	s := newStore(t)
	added := addSearchTask(t, s, board.Task{Title: "addunique marker"})
	requireSingleCardHit(t, s, "alice", "addunique", added.ID, board.StatusTodo)

	updated, err := s.UpdateAndMoveTask("alice", added.ID, TaskPatch{Title: sptr("updateunique marker")}, nil, nil)
	if err != nil {
		t.Fatalf("UpdateAndMoveTask: %v", err)
	}
	requireNoHits(t, s, "alice", "addunique")
	requireSingleCardHit(t, s, "alice", "updateunique", updated.ID, board.StatusTodo)

	var before int
	if err := s.db.QueryRow(`SELECT count(*) FROM tasks_fts WHERE id = ?`, added.ID).Scan(&before); err != nil {
		t.Fatalf("count FTS before move: %v", err)
	}
	moved, err := s.MoveTask("alice", added.ID, board.StatusDoing)
	if err != nil {
		t.Fatalf("MoveTask: %v", err)
	}
	var after int
	if err := s.db.QueryRow(`SELECT count(*) FROM tasks_fts WHERE id = ?`, added.ID).Scan(&after); err != nil {
		t.Fatalf("count FTS after move: %v", err)
	}
	if before != 1 || after != before {
		t.Fatalf("status-only move changed FTS row count from %d to %d", before, after)
	}
	requireSingleCardHit(t, s, "alice", "updateunique", moved.ID, board.StatusDoing)

	if _, err := s.DeleteTask("alice", added.ID); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	requireNoHits(t, s, "alice", "updateunique")

	err = s.ReplaceBoard("alice", board.Board{Title: "Board", Tasks: []board.Task{
		{Title: "replacement marker", Status: board.StatusTodo},
	}})
	if err != nil {
		t.Fatalf("ReplaceBoard: %v", err)
	}
	requireSingleCardHit(t, s, "alice", "replacement marker", "", board.StatusTodo)
}

func TestTasksByLinkMatchesACompleteTagOnly(t *testing.T) {
	s := newStore(t)
	exact := addSearchTask(t, s, board.Task{Title: "exact", Tags: []string{"link::gitlab#12"}})
	addSearchTask(t, s, board.Task{Title: "prefix trap", Tags: []string{"link::gitlab#123"}})
	hits, err := s.TasksByLink("alice", "link::gitlab#12")
	if err != nil {
		t.Fatalf("TasksByLink: %v", err)
	}
	want := []SimilarHit{{ID: exact.ID, Title: exact.Title, Status: string(exact.Status), Via: "card", Link: "link::gitlab#12"}}
	if !reflect.DeepEqual(hits, want) {
		t.Fatalf("TasksByLink = %+v, want %+v", hits, want)
	}
}

func TestImportLinksUpsertRefreshesAllFieldsAndSupportsBatchLookup(t *testing.T) {
	s := newStore(t)
	first := ImportLink{
		Source: "gitlab.com", Kind: "gitlab", ExternalKey: "gitlab.com/acme/app#12",
		Link: "link::gitlab#12", URL: "https://gitlab.com/acme/app/-/issues/12", Title: "oldunique import title",
	}
	second := ImportLink{
		Source: "github.com", Kind: "github", ExternalKey: "github.com/acme/app#13",
		Link: "link::github#13", URL: "https://github.com/acme/app/issues/13", Title: "second import title",
	}
	if err := s.RecordImportLinks("alice", []ImportLink{first, second}); err != nil {
		t.Fatalf("RecordImportLinks initial: %v", err)
	}
	var importedAt, refreshedAt string
	if err := s.db.QueryRow(`SELECT imported_at FROM import_links WHERE scope = ? AND external_key = ?`, "alice", first.ExternalKey).Scan(&importedAt); err != nil {
		t.Fatalf("read initial imported_at: %v", err)
	}
	updated := ImportLink{
		Source: "enterprise", Kind: "github", ExternalKey: first.ExternalKey,
		Link: "link::github#99", URL: "https://forge.example/acme/app/issues/99", Title: "updatedunique import title",
	}
	if err := s.RecordImportLinks("alice", []ImportLink{updated}); err != nil {
		t.Fatalf("RecordImportLinks update: %v", err)
	}
	if err := s.db.QueryRow(`SELECT imported_at FROM import_links WHERE scope = ? AND external_key = ?`, "alice", first.ExternalKey).Scan(&refreshedAt); err != nil {
		t.Fatalf("read refreshed imported_at: %v", err)
	}
	if refreshedAt == importedAt {
		t.Fatalf("upsert retained imported_at %q", importedAt)
	}
	got, err := s.ImportedAs("alice", []string{first.ExternalKey, second.ExternalKey, "missing"})
	if err != nil {
		t.Fatalf("ImportedAs: %v", err)
	}
	want := map[string]ImportLink{first.ExternalKey: updated, second.ExternalKey: second}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ImportedAs = %+v, want %+v", got, want)
	}
	hits, err := s.SearchSimilar("alice", "updatedunique", "", 3)
	if err != nil {
		t.Fatalf("SearchSimilar import: %v", err)
	}
	if len(hits) != 1 || hits[0].Via != "import" || hits[0].Link != updated.Link || hits[0].Title != updated.Title {
		t.Fatalf("SearchSimilar import = %+v", hits)
	}
}

func TestImportedAsEmptyInputReturnsInitializedMap(t *testing.T) {
	got, err := newStore(t).ImportedAs("alice", nil)
	if err != nil {
		t.Fatalf("ImportedAs(nil): %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("ImportedAs(nil) = %#v, want initialized empty map", got)
	}
}

func TestRecordImportLinksRejectsMalformedBatchWithoutPartialWrites(t *testing.T) {
	tests := []ImportLink{
		{ExternalKey: strings.Repeat("k", 2049), URL: "https://example.test", Title: "title"},
		{ExternalKey: "key", URL: strings.Repeat("u", 2049), Title: "title"},
		{ExternalKey: "key", URL: "https://example.test", Title: strings.Repeat("t", 501)},
		{ExternalKey: "key\nbreak", URL: "https://example.test", Title: "title"},
		{ExternalKey: "key", URL: "https://example.test\rbreak", Title: "title"},
		{ExternalKey: "key", URL: "https://example.test", Title: "title\nbreak"},
	}
	for i, bad := range tests {
		t.Run(fmt.Sprintf("case-%d", i), func(t *testing.T) {
			s := newStore(t)
			valid := ImportLink{Source: "gitlab.com", Kind: "gitlab", ExternalKey: "valid", Link: "link::gitlab#1", URL: "https://example.test/1", Title: "valid"}
			if err := s.RecordImportLinks("alice", []ImportLink{valid, bad}); err == nil {
				t.Fatal("RecordImportLinks accepted malformed input")
			}
			got, err := s.ImportedAs("alice", []string{"valid", bad.ExternalKey})
			if err != nil {
				t.Fatalf("ImportedAs after rejection: %v", err)
			}
			if len(got) != 0 {
				t.Fatalf("rejected batch wrote rows: %+v", got)
			}
		})
	}
}

func TestSearchSimilarReturnsCardsBeforeImportsAndCapsMergedResults(t *testing.T) {
	s := newStore(t)
	card := addSearchTask(t, s, board.Task{Title: "shared marker card"})
	if err := s.RecordImportLinks("alice", []ImportLink{
		{ExternalKey: "one", Link: "link::one", Title: "shared marker import one"},
		{ExternalKey: "two", Link: "link::two", Title: "shared marker import two"},
	}); err != nil {
		t.Fatalf("RecordImportLinks: %v", err)
	}
	hits, err := s.SearchSimilar("alice", "shared marker", "", 2)
	if err != nil {
		t.Fatalf("SearchSimilar: %v", err)
	}
	if len(hits) != 2 || hits[0].Via != "card" || hits[0].ID != card.ID || hits[1].Via != "import" {
		t.Fatalf("SearchSimilar merge = %+v, want card first and total cap 2", hits)
	}
}

func requireNoHits(t *testing.T, s *Store, scope, query string) {
	t.Helper()
	hits, err := s.SearchSimilar(scope, query, "", 10)
	if err != nil {
		t.Fatalf("SearchSimilar(%q): %v", query, err)
	}
	if len(hits) != 0 {
		t.Fatalf("SearchSimilar(%q) = %+v, want no hits", query, hits)
	}
}

func addSearchTask(t *testing.T, s *Store, task board.Task) board.Task {
	t.Helper()
	added, err := s.AddTask("alice", task)
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	return added
}

func requireSingleCardHit(t *testing.T, s *Store, scope, query, id string, status board.Status) {
	t.Helper()
	hits, err := s.SearchSimilar(scope, query, "", 10)
	if err != nil {
		t.Fatalf("SearchSimilar(%q): %v", query, err)
	}
	if len(hits) != 1 || hits[0].Via != "card" || hits[0].Status != string(status) || (id != "" && hits[0].ID != id) {
		t.Fatalf("SearchSimilar(%q) = %+v, want one card id=%q status=%q", query, hits, id, status)
	}
}
