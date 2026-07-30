package store

import (
	"fmt"
	"reflect"
	"sort"
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
			candidateIDs := rawCardIDs(t, s, "alice", tt.raw, 2)
			foundLiteral := false
			for _, id := range candidateIDs {
				if id == unrelated.ID {
					t.Fatalf("raw FTS query %q matched unrelated card %q", tt.raw, id)
				}
				foundLiteral = foundLiteral || id == literal.ID
			}
			if tt.matchesLiteral && !foundLiteral {
				t.Fatalf("raw FTS query %q = %v, want literal card %q", tt.raw, candidateIDs, literal.ID)
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

func TestFtsQueryTreatsControlCharactersAsTokenBoundaries(t *testing.T) {
	if got, want := FtsQuery("abc\x00def"), `"abc" OR "def"`; got != want {
		t.Fatalf("FtsQuery embedded NUL = %q, want %q", got, want)
	}
	for _, raw := range []string{"\x00", "\x00\x01\x1f\x7f"} {
		if got := FtsQuery(raw); got != "" {
			t.Fatalf("FtsQuery control-only %q = %q, want empty", raw, got)
		}
	}
}

// TestSimilarityIsSymmetricAndBounded locks down the normalization and score
// contract so duplicate filtering cannot drift with FTS ranking changes.
func TestSimilarityIsSymmetricAndBounded(t *testing.T) {
	tests := []struct {
		name, a, b string
		want       float64
	}{
		{name: "identical", a: "Add dark mode toggle", b: "Add dark mode toggle", want: 1},
		{name: "disjoint", a: "alpha beta", b: "gamma delta", want: 0},
		{name: "empty left", a: "", b: "alpha", want: 0},
		{name: "both empty", a: "", b: "", want: 0},
		{name: "partial", a: "alpha beta", b: "alpha", want: 2.0 / 3.0},
		{name: "normalized sets", a: "(DARK), dark to alpha!", b: "dark alpha", want: 1},
		{name: "short tokens normalize empty", a: "to of in", b: "to of in", want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAB := Similarity(tt.a, tt.b)
			gotBA := Similarity(tt.b, tt.a)
			if gotAB != gotBA {
				t.Fatalf("Similarity(%q, %q) = %v, reverse = %v", tt.a, tt.b, gotAB, gotBA)
			}
			if gotAB < 0 || gotAB > 1 {
				t.Fatalf("Similarity(%q, %q) = %v, want within [0,1]", tt.a, tt.b, gotAB)
			}
			if gotAB != tt.want {
				t.Fatalf("Similarity(%q, %q) = %v, want %v", tt.a, tt.b, gotAB, tt.want)
			}
		})
	}
}

// TestASharedCommonWordIsNotADuplicate records the v0.7.0 regression where
// "add dark mode toggle" returned the SSO card solely because both contained "add".
func TestASharedCommonWordIsNotADuplicate(t *testing.T) {
	s := newStore(t)
	for _, title := range []string{
		"Add SSO login for the admin portal",
		"Fix the billing export CSV encoding",
		"Upgrade Postgres to 16",
	} {
		addSearchTask(t, s, board.Task{Title: title})
	}

	requireNoHits(t, s, "alice", "add dark mode toggle")
}

// TestGenuineNearMatchesStillSurface prevents the relevance floor from making
// duplicate detection useless for ordinary rewordings.
func TestGenuineNearMatchesStillSurface(t *testing.T) {
	s := newStore(t)
	targets := map[string]board.Task{
		"dark mode": addSearchTask(t, s, board.Task{
			Title: "Add dark mode toggle",
		}),
		"SSO login for admins": addSearchTask(t, s, board.Task{
			Title: "Add SSO login for the admin portal",
		}),
		"billing CSV export encoding bug": addSearchTask(t, s, board.Task{
			Title: "Fix the billing export CSV encoding",
		}),
	}

	for query, target := range targets {
		t.Run(query, func(t *testing.T) {
			hits, err := s.SearchSimilar("alice", query, "", 3)
			if err != nil {
				t.Fatalf("SearchSimilar(%q): %v", query, err)
			}
			found := false
			for _, hit := range hits {
				found = found || hit.ID == target.ID
			}
			if !found {
				t.Fatalf("SearchSimilar(%q) = %+v, want card %q", query, hits, target.ID)
			}
		})
	}
}

// TestShortQueryMatchesLongTitleByPhrase protects short exact phrases whose
// Dice score falls below the relevance floor only because the title is long.
func TestShortQueryMatchesLongTitleByPhrase(t *testing.T) {
	s := newStore(t)
	task := addSearchTask(t, s, board.Task{
		Title: "Coordinate customer preferences across desktop and mobile with dark mode toggle",
	})
	const query = "dark mode"
	if score := Similarity(query, task.Title); score >= SimilarityFloor {
		t.Fatalf("test fixture similarity = %v, want below %v", score, SimilarityFloor)
	}

	requireSingleCardHit(t, s, "alice", query, task.ID, board.StatusTodo)
}

// TestSearchSimilarWidensCandidatesBeforeFiltering prevents an irrelevant top
// FTS hit from exhausting the caller's limit before a real match is considered.
func TestSearchSimilarWidensCandidatesBeforeFiltering(t *testing.T) {
	const query = "add dark mode toggle"
	tests := []struct {
		name       string
		noiseCount int
		limit      int
	}{
		{name: "floor", noiseCount: 23, limit: 1},
		{name: "multiplier", noiseCount: 31, limit: 4},
	}
	for _, tt := range tests {
		t.Run("cards "+tt.name, func(t *testing.T) {
			s := newStore(t)
			target := addRankedCardTarget(t, s, tt.noiseCount)
			if rank := rawCardRank(t, s, "alice", query, tt.noiseCount+1, target.ID); rank != tt.noiseCount {
				t.Fatalf("card fixture rank = %d, want %d", rank, tt.noiseCount)
			}

			hits, err := s.SearchSimilar("alice", query, "", tt.limit)
			if err != nil {
				t.Fatalf("SearchSimilar: %v", err)
			}
			if len(hits) != 1 || hits[0].Via != "card" || hits[0].ID != target.ID {
				t.Fatalf("SearchSimilar = %+v, want card %q", hits, target.ID)
			}
		})

		t.Run("imports "+tt.name, func(t *testing.T) {
			s := newStore(t)
			targetLink := addRankedImportTarget(t, s, tt.noiseCount)
			if rank := rawImportRank(t, s, "alice", query, tt.noiseCount+1, targetLink); rank != tt.noiseCount {
				t.Fatalf("import fixture rank = %d, want %d", rank, tt.noiseCount)
			}

			hits, err := s.SearchSimilar("alice", query, "", tt.limit)
			if err != nil {
				t.Fatalf("SearchSimilar: %v", err)
			}
			if len(hits) != 1 || hits[0].Via != "import" || hits[0].Link != targetLink {
				t.Fatalf("SearchSimilar = %+v, want import %q", hits, targetLink)
			}
		})
	}
}

// TestSearchSimilarOrdersBySimilarityAndPreservesBM25Ties ensures relevance
// controls the result order without making equal-score FTS ordering unstable.
func TestSearchSimilarOrdersBySimilarityAndPreservesBM25Ties(t *testing.T) {
	t.Run("higher similarity wins", func(t *testing.T) {
		s := newStore(t)
		const query = "alpha beta gamma delta epsilon zeta"
		lower := addSearchTask(t, s, board.Task{
			Title: "alpha beta gamma delta epsilon zeta one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen sixteen seventeen eighteen nineteen twenty",
		})
		higher := addSearchTask(t, s, board.Task{Title: "alpha beta"})
		if lowerScore, higherScore := Similarity(query, lower.Title), Similarity(query, higher.Title); lowerScore >= higherScore || lowerScore < SimilarityFloor {
			t.Fatalf("test fixture scores lower=%v higher=%v floor=%v", lowerScore, higherScore, SimilarityFloor)
		}
		if lowerRank, higherRank := rawCardRank(t, s, "alice", query, 2, lower.ID), rawCardRank(t, s, "alice", query, 2, higher.ID); lowerRank >= higherRank {
			t.Fatalf("test fixture BM25 ranks lower=%d higher=%d, want lower-similarity hit first", lowerRank, higherRank)
		}

		hits, err := s.SearchSimilar("alice", query, "", 2)
		if err != nil {
			t.Fatalf("SearchSimilar: %v", err)
		}
		if len(hits) != 2 || hits[0].ID != higher.ID || hits[1].ID != lower.ID {
			t.Fatalf("SearchSimilar = %+v, want similarity order %q, %q", hits, higher.ID, lower.ID)
		}
	})

	t.Run("equal similarity keeps BM25 order", func(t *testing.T) {
		s := newStore(t)
		const query = "alpha beta gamma"
		first := addSearchTask(t, s, board.Task{Title: "alpha alpha alpha beta omega"})
		second := addSearchTask(t, s, board.Task{Title: "alpha beta omega"})
		if firstScore, secondScore := Similarity(query, first.Title), Similarity(query, second.Title); firstScore != secondScore {
			t.Fatalf("test fixture scores first=%v second=%v, want equal", firstScore, secondScore)
		}
		firstRank := rawCardRank(t, s, "alice", query, 2, first.ID)
		secondRank := rawCardRank(t, s, "alice", query, 2, second.ID)
		want := []string{first.ID, second.ID}
		if secondRank < firstRank {
			want[0], want[1] = want[1], want[0]
		}

		hits, err := s.SearchSimilar("alice", query, "", 2)
		if err != nil {
			t.Fatalf("SearchSimilar: %v", err)
		}
		if len(hits) != 2 || hits[0].ID != want[0] || hits[1].ID != want[1] {
			t.Fatalf("SearchSimilar = %+v, want BM25 tie order %q, %q", hits, want[0], want[1])
		}
	})
}

func TestSearchSimilarWithEmbeddedNULHasNoSQLSyntaxError(t *testing.T) {
	hits, err := newStore(t).SearchSimilar("alice", "abc\x00def", "", 10)
	if err != nil {
		t.Fatalf("SearchSimilar embedded NUL: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("SearchSimilar embedded NUL = %+v, want no hits", hits)
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

func TestTasksByLinkCapsResultsAtTen(t *testing.T) {
	s := newStore(t)
	wantIDs := make([]string, 0, 12)
	for i := 0; i < 12; i++ {
		added := addSearchTask(t, s, board.Task{
			Title: fmt.Sprintf("exact-link-%02d", i), Tags: []string{"link::gitlab#limit"},
		})
		wantIDs = append(wantIDs, added.ID)
	}

	hits, err := s.TasksByLink("alice", "link::gitlab#limit")
	if err != nil {
		t.Fatalf("TasksByLink: %v", err)
	}
	if len(hits) != 10 {
		t.Fatalf("TasksByLink returned %d hits, want 10", len(hits))
	}
	for i, hit := range hits {
		if hit.ID != wantIDs[i] {
			t.Errorf("TasksByLink hit %d = %q, want %q", i, hit.ID, wantIDs[i])
		}
	}
}

func TestTasksByLinkUsesStableIDTieBreakForCap(t *testing.T) {
	s := newStore(t)
	wantIDs := make([]string, 0, 12)
	for i := 0; i < 12; i++ {
		added := addSearchTask(t, s, board.Task{
			Title: fmt.Sprintf("tie-link-%02d", i), Tags: []string{"link::gitlab#tie"},
		})
		id := fmt.Sprintf("tie-%02d", 11-i)
		if _, err := s.db.Exec(`UPDATE tasks SET id = ?, position = 0 WHERE id = ?`, id, added.ID); err != nil {
			t.Fatalf("tie task %d: %v", i, err)
		}
		wantIDs = append(wantIDs, id)
	}
	sort.Strings(wantIDs)

	hits, err := s.TasksByLink("alice", "link::gitlab#tie")
	if err != nil {
		t.Fatalf("TasksByLink: %v", err)
	}
	if len(hits) != 10 {
		t.Fatalf("TasksByLink returned %d hits, want 10", len(hits))
	}
	for i, hit := range hits {
		if hit.ID != wantIDs[i] {
			t.Errorf("TasksByLink tie hit %d = %q, want %q", i, hit.ID, wantIDs[i])
		}
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

func TestScopeIsolation(t *testing.T) {
	s := newStore(t)
	scopes := []string{"alice", "bob"}
	cards := make(map[string]board.Task, len(scopes))
	provenance := make(map[string][]ImportLink, len(scopes))
	for _, scope := range scopes {
		card, err := s.AddTask(scope, board.Task{
			Title: "scope title marker", Desc: "scope body marker", Tags: []string{"scope-tag-marker", "link::gitlab#1"},
		})
		if err != nil {
			t.Fatalf("AddTask(%q): %v", scope, err)
		}
		links := []ImportLink{
			{Source: "forge.example", Kind: "gitlab", ExternalKey: "shared-key", Link: "link::gitlab#shared", URL: "https://forge.example/shared", Title: "shared import title"},
			{Source: "forge.example", Kind: "gitlab", ExternalKey: scope + "-only", Link: "link::gitlab#" + scope, URL: "https://forge.example/" + scope, Title: scope + " isolated import sentinel"},
		}
		if err := s.RecordImportLinks(scope, links); err != nil {
			t.Fatalf("RecordImportLinks(%q): %v", scope, err)
		}
		cards[scope], provenance[scope] = card, links
	}

	// These scope checks make a future team-scope rollout additive rather than a cross-tenant rewrite.
	for i, scope := range scopes {
		other := scopes[1-i]
		t.Run(scope, func(t *testing.T) {
			for _, query := range []string{"scope title marker", "scope body marker"} {
				hits, err := s.SearchSimilar(scope, query, "", 10)
				if err != nil {
					t.Fatalf("SearchSimilar(%q): %v", query, err)
				}
				own, foreign := false, false
				for _, hit := range hits {
					own = own || hit.ID == cards[scope].ID
					foreign = foreign || hit.ID == cards[other].ID
				}
				if !own || foreign {
					t.Fatalf("SearchSimilar(%q) = %+v, want %q without %q", query, hits, cards[scope].ID, cards[other].ID)
				}
			}
			links, err := s.TasksByLink(scope, "link::gitlab#1")
			if err != nil || len(links) != 1 || links[0].ID != cards[scope].ID {
				t.Fatalf("TasksByLink(%q) = %+v, %v", scope, links, err)
			}
			keys := []string{"shared-key", scope + "-only", other + "-only"}
			found, err := s.ImportedAs(scope, keys)
			want := map[string]ImportLink{"shared-key": provenance[scope][0], scope + "-only": provenance[scope][1]}
			if err != nil || !reflect.DeepEqual(found, want) {
				t.Fatalf("ImportedAs(%q) = %+v, %v, want %+v", scope, found, err, want)
			}
			hits, err := s.SearchSimilar(scope, "isolated import sentinel", "", 10)
			own, foreign := false, false
			for _, hit := range hits {
				own = own || hit.Via == "import" && hit.Title == provenance[scope][1].Title
				foreign = foreign || hit.Via == "import" && hit.Title == provenance[other][1].Title
			}
			if err != nil || !own || foreign {
				t.Fatalf("SearchSimilar import(%q) = %+v, %v", scope, hits, err)
			}
		})
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

func addRankedCardTarget(t *testing.T, s *Store, noiseCount int) board.Task {
	t.Helper()
	for i := range noiseCount {
		addSearchTask(t, s, board.Task{
			Title: fmt.Sprintf("add dark-mode-toggle unrelated%02d", i),
		})
	}
	return addSearchTask(t, s, board.Task{Title: "dark mode toggle"})
}

func addRankedImportTarget(t *testing.T, s *Store, noiseCount int) string {
	t.Helper()
	links := make([]ImportLink, 0, noiseCount+1)
	for i := range noiseCount {
		links = append(links, ImportLink{
			ExternalKey: fmt.Sprintf("noise-%02d", i),
			Link:        fmt.Sprintf("link::noise-%02d", i),
			Title:       fmt.Sprintf("add dark-mode-toggle unrelated%02d", i),
		})
	}
	const targetLink = "link::target"
	links = append(links, ImportLink{
		ExternalKey: "target",
		Link:        targetLink,
		Title:       "dark mode toggle",
	})
	if err := s.RecordImportLinks("alice", links); err != nil {
		t.Fatalf("RecordImportLinks: %v", err)
	}
	return targetLink
}

func rawCardRank(t *testing.T, s *Store, scope, query string, candidateLimit int, targetID string) int {
	t.Helper()
	for rank, id := range rawCardIDs(t, s, scope, query, candidateLimit) {
		if id == targetID {
			return rank
		}
	}
	t.Fatalf("raw card candidates did not contain %q", targetID)
	return -1
}

func rawCardIDs(t *testing.T, s *Store, scope, query string, candidateLimit int) []string {
	t.Helper()
	rows, err := s.db.Query(`
		SELECT f.id
		FROM tasks_fts f
		JOIN tasks t ON t.id = f.id AND t.user = ?1
		WHERE tasks_fts MATCH ?2 AND f.scope = ?1
		ORDER BY bm25(tasks_fts, 5.0, 1.0, 3.0) LIMIT ?3`,
		scope, FtsQuery(query), candidateLimit)
	if err != nil {
		t.Fatalf("query raw card candidates: %v", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan raw card candidate: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read raw card candidates: %v", err)
	}
	return ids
}

func rawImportRank(t *testing.T, s *Store, scope, query string, candidateLimit int, targetLink string) int {
	t.Helper()
	rows, err := s.db.Query(`
		SELECT l.link
		FROM import_links_fts f
		JOIN import_links l ON l.scope = f.scope AND l.external_key = f.external_key
		WHERE import_links_fts MATCH ?2 AND f.scope = ?1
		ORDER BY bm25(import_links_fts, 5.0) LIMIT ?3`,
		scope, FtsQuery(query), candidateLimit)
	if err != nil {
		t.Fatalf("query raw import rank: %v", err)
	}
	defer rows.Close()
	rank := 0
	for rows.Next() {
		var link string
		if err := rows.Scan(&link); err != nil {
			t.Fatalf("scan raw import rank: %v", err)
		}
		if link == targetLink {
			return rank
		}
		rank++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read raw import rank: %v", err)
	}
	t.Fatalf("raw import candidates did not contain %q", targetLink)
	return -1
}
