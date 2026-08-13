package store

import (
	"strings"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
)

func seedFilterBoard(t *testing.T) *Store {
	t.Helper()
	s := newStore(t)
	tasks := []board.Task{
		{Title: "Fix login timeout", Desc: "auth token expires early", Tags: []string{"bug", "auth"}},
		{Title: "Design landing page", Desc: "marketing wants a hero image", Tags: []string{"ui"}},
		{Title: "Rotate auth keys", Desc: "quarterly rotation", Tags: []string{"auth", "env::prod"}, Status: board.StatusDoing},
		{Title: "Write onboarding docs", Desc: "", Tags: nil, Status: board.StatusDone},
	}
	for _, task := range tasks {
		if _, err := s.AddTask("u", task); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func filterTitles(t *testing.T, s *Store, f TaskFilter) []string {
	t.Helper()
	tasks, err := s.FilterTasks("u", f)
	if err != nil {
		t.Fatalf("FilterTasks(%+v): %v", f, err)
	}
	titles := make([]string, 0, len(tasks))
	for _, task := range tasks {
		titles = append(titles, task.Title)
	}
	return titles
}

func TestFilterTasksFreeTextAndTags(t *testing.T) {
	s := seedFilterBoard(t)

	cases := []struct {
		name   string
		filter TaskFilter
		want   []string
	}{
		{"free text over desc", TaskFilter{Search: "token expires"}, []string{"Fix login timeout"}},
		{"free text over title", TaskFilter{Search: "landing"}, []string{"Design landing page"}},
		{"last word is a prefix", TaskFilter{Search: "rotat"}, []string{"Rotate auth keys"}},
		{"words AND together", TaskFilter{Search: "auth rotation"}, []string{"Rotate auth keys"}},
		{"no match", TaskFilter{Search: "nonexistent"}, []string{}},
		{"single tag", TaskFilter{Tags: []string{"auth"}}, []string{"Fix login timeout", "Rotate auth keys"}},
		{"tags AND together", TaskFilter{Tags: []string{"auth", "bug"}}, []string{"Fix login timeout"}},
		{"scoped tag matches whole", TaskFilter{Tags: []string{"env::prod"}}, []string{"Rotate auth keys"}},
		{"scoped tag half does not match", TaskFilter{Tags: []string{"prod"}}, []string{}},
		{"text and tag combine", TaskFilter{Search: "auth", Tags: []string{"bug"}}, []string{"Fix login timeout"}},
		{"status and tag combine", TaskFilter{Status: board.StatusDoing, Tags: []string{"auth"}}, []string{"Rotate auth keys"}},
		{"empty filter lists all in board order", TaskFilter{}, []string{"Fix login timeout", "Design landing page", "Rotate auth keys", "Write onboarding docs"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := filterTitles(t, s, tt.filter)
			if len(got) != len(tt.want) {
				t.Fatalf("titles = %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("titles = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestFilterTasksRejectsBadInput(t *testing.T) {
	s := seedFilterBoard(t)
	if _, err := s.FilterTasks("u", TaskFilter{Status: "nowhere"}); err == nil {
		t.Fatal("accepted an invalid status")
	}
	if _, err := s.FilterTasks("u", TaskFilter{Tags: []string{" "}}); err == nil {
		t.Fatal("accepted a blank tag filter")
	}
	if _, err := s.FilterTasks("u", TaskFilter{Search: strings.Repeat("x", 501)}); err == nil {
		t.Fatal("accepted an oversized search query")
	}
}

func TestFilterTasksNeutralizesFTSSyntax(t *testing.T) {
	s := seedFilterBoard(t)
	// Operators and quotes must be treated as literal text, not FTS syntax.
	for _, hostile := range []string{`"unclosed`, `a AND b OR c NOT d`, `col:value`, `(paren`, `*`} {
		if _, err := s.FilterTasks("u", TaskFilter{Search: hostile}); err != nil {
			t.Fatalf("FilterTasks(%q) = %v", hostile, err)
		}
	}
	// A literal token containing a quote still matches nothing rather than erroring.
	if got := filterTitles(t, s, TaskFilter{Search: `tim"eout`}); len(got) != 0 {
		t.Fatalf("hostile search matched %v", got)
	}
}

func TestFtsSearchQueryShapes(t *testing.T) {
	if got := ftsSearchQuery(""); got != "" {
		t.Fatalf("empty = %q", got)
	}
	if got := ftsSearchQuery("auth log"); got != `"auth" AND "log"*` {
		t.Fatalf("two words = %q", got)
	}
	if got := ftsSearchQuery(`say "hi"`); got != `"say" AND """hi"""*` {
		t.Fatalf("quotes = %q", got)
	}
	long := strings.Repeat("word ", 20)
	if got := ftsSearchQuery(long); strings.Count(got, "AND") != 11 {
		t.Fatalf("token cap = %q", got)
	}
}
