package cmdpalette

import (
	"github.com/sahilm/fuzzy"

	"github.com/RandomCodeSpace/kb/internal/tui/action"
)

// Entry is one row the palette offers: a registry action and, when a query
// narrowed the list, the byte offsets in its Name that the query matched.
//
// Matched carries byte offsets because that is what sahilm/fuzzy reports;
// widget.MatchRuns is where they become display columns. Nothing between here
// and there interprets them.
type Entry struct {
	Action  action.Action
	Matched []int
}

// Filter is the palette's whole search, and it is a pure function of its two
// arguments: same actions and same query, same result, no styles and no model
// state involved. That is deliberate — the ranking is the part most likely to
// need tuning, and it should be tunable against a table test rather than
// against a rendered frame.
//
// An empty query is not a search: it lists every action in registry order, so
// opening the palette and pressing enter runs the first thing the help pane
// also lists first. A non-empty query ranks by fuzzy score, best first, across
// every group at once — a ranked list that still carried group headers would be
// claiming an order it does not have.
func Filter(actions []action.Action, query string) []Entry {
	if query == "" {
		entries := make([]Entry, 0, len(actions))
		for _, entry := range actions {
			entries = append(entries, Entry{Action: entry})
		}
		return entries
	}
	names := make([]string, 0, len(actions))
	for _, entry := range actions {
		names = append(names, entry.Name)
	}
	// Match.Index is the position in the slice handed to Find, and that slice
	// was built one-for-one from actions two lines above, so it indexes actions
	// directly.
	matches := fuzzy.Find(query, names)
	entries := make([]Entry, 0, len(matches))
	for _, match := range matches {
		entries = append(entries, Entry{
			Action:  actions[match.Index],
			Matched: append([]int(nil), match.MatchedIndexes...),
		})
	}
	return entries
}

// Grouped reports whether a result list should carry its section bands: only an
// unfiltered list, which is in registry order and so is genuinely grouped.
func Grouped(query string) bool { return query == "" }
