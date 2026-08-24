package cmdpalette

import (
	"testing"

	"github.com/RandomCodeSpace/kb/internal/tui/action"
)

// full is a board built with every optional feature, which is the widest
// registry the palette can offer.
var full = action.Features{Editor: true, Settings: true, ADR: true, Issues: true}

// names is the label list of a result set, which is what the assertions below
// actually care about.
func names(entries []Entry) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Action.Name)
	}
	return out
}

// TestFilterWithoutAQueryListsTheRegistryInOrder is the palette's opening
// state: no query is not a search, so pressing enter straight away runs the
// action the help pane also lists first.
func TestFilterWithoutAQueryListsTheRegistryInOrder(t *testing.T) {
	listed := action.Listed(full)
	entries := Filter(listed, "")
	if len(entries) != len(listed) {
		t.Fatalf("empty query listed %d of %d actions", len(entries), len(listed))
	}
	for index, entry := range entries {
		if entry.Action.ID != listed[index].ID {
			t.Errorf("row %d is action %d, want %d", index, entry.Action.ID, listed[index].ID)
		}
		if entry.Matched != nil {
			t.Errorf("row %d carries match offsets for an empty query", index)
		}
	}
}

// TestFilterRanksByFuzzyScore pins the behavior a user actually feels: the thing
// they were reaching for is first.
func TestFilterRanksByFuzzyScore(t *testing.T) {
	listed := action.Listed(full)
	for _, test := range []struct {
		name  string
		query string
		want  string
	}{
		{name: "prefix", query: "ship", want: "ship card"},
		{name: "initials", query: "nc", want: "new card"},
		{name: "interior word", query: "forge", want: "import forge issue"},
		{name: "initials across words", query: "pd", want: "permanently delete"},
		{name: "adjacent beats scattered", query: "lab", want: "label filter"},
	} {
		t.Run(test.name, func(t *testing.T) {
			entries := Filter(listed, test.query)
			if len(entries) == 0 {
				t.Fatalf("query %q matched nothing", test.query)
			}
			if entries[0].Action.Name != test.want {
				t.Errorf("query %q ranked %q first, want %q; full order %v",
					test.query, entries[0].Action.Name, test.want, names(entries))
			}
		})
	}
}

// TestFilterReportsMatchOffsets is the contract widget.MatchRuns consumes: byte
// offsets into the action's own Name, in range, one per matched rune.
func TestFilterReportsMatchOffsets(t *testing.T) {
	entries := Filter(action.Listed(full), "ship")
	if len(entries) == 0 {
		t.Fatal("no match for ship")
	}
	top := entries[0]
	if len(top.Matched) != 4 {
		t.Fatalf("matched %v, want four offsets", top.Matched)
	}
	for _, offset := range top.Matched {
		if offset < 0 || offset >= len(top.Action.Name) {
			t.Errorf("offset %d is outside %q", offset, top.Action.Name)
		}
	}
	for _, offset := range top.Matched {
		if top.Action.Name[offset] == ' ' {
			t.Errorf("offset %d lands on a space in %q", offset, top.Action.Name)
		}
	}
}

// TestFilterMissesEntirely is the empty state's cause: a query nothing matches
// returns nothing, and the view is what turns that into a row.
func TestFilterMissesEntirely(t *testing.T) {
	if entries := Filter(action.Listed(full), "zzzzzzq"); len(entries) != 0 {
		t.Errorf("nonsense query matched %v", names(entries))
	}
	if entries := Filter(nil, "ship"); len(entries) != 0 {
		t.Errorf("an empty registry matched %v", names(entries))
	}
	if entries := Filter(nil, ""); len(entries) != 0 {
		t.Errorf("an empty registry listed %v", names(entries))
	}
}

// TestFilterIsCaseInsensitive keeps the search usable with caps lock on.
func TestFilterIsCaseInsensitive(t *testing.T) {
	listed := action.Listed(full)
	lower := names(Filter(listed, "ship"))
	upper := names(Filter(listed, "SHIP"))
	if len(lower) == 0 || len(upper) == 0 {
		t.Fatalf("lower %v upper %v", lower, upper)
	}
	if lower[0] != upper[0] {
		t.Errorf("SHIP ranked %q first, ship ranked %q", upper[0], lower[0])
	}
}

// TestFilterNarrowsWithTheBoard keeps the palette honest about a board built
// without a card editor: it offers what the help pane calls enabled.
func TestFilterNarrowsWithTheBoard(t *testing.T) {
	for _, entry := range Filter(action.Listed(action.Features{}), "card") {
		if entry.Action.Need != action.Always {
			t.Errorf("bare board offered %q, which needs feature %d", entry.Action.Name, entry.Action.Need)
		}
	}
}

// TestGroupedOnlyWithoutAQuery is why a ranked list drops its section bands: a
// score-ordered list carrying group headers would be claiming an order it does
// not have.
func TestGroupedOnlyWithoutAQuery(t *testing.T) {
	if !Grouped("") {
		t.Error("an unfiltered list is not grouped")
	}
	if Grouped("ship") {
		t.Error("a ranked list kept its section bands")
	}
}
