package project

import (
	"reflect"
	"strings"
	"testing"
)

func TestValidateNameAcceptsLabelValuesAndRejectsSmuggledScopes(t *testing.T) {
	if got, err := ValidateName("  kb  "); got != "kb" || err != nil {
		t.Fatalf("ValidateName trimmed = %q, %v", got, err)
	}
	for _, tc := range []struct{ name, want string }{
		{"", "must not be empty"},
		{"   ", "must not be empty"},
		{"two words", "whitespace"},
		{"#tag", "must not start with '#'"},
		{"a::b", `must not contain "::"`},
	} {
		got, err := ValidateName(tc.name)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("ValidateName(%q) = %q, %v; want an error mentioning %q", tc.name, got, err, tc.want)
		}
	}
}

func TestLabelAndSplitTagsRoundTripInOrder(t *testing.T) {
	if Label("kb") != "project::kb" {
		t.Fatalf("Label = %q", Label("kb"))
	}
	named, rest := SplitTags([]string{"tui", "project::kb", "type::feature", "project::web"})
	if !reflect.DeepEqual(named, []string{"kb", "web"}) {
		t.Fatalf("projects = %v", named)
	}
	if !reflect.DeepEqual(rest, []string{"tui", "type::feature"}) {
		t.Fatalf("rest = %v", rest)
	}
	if named, rest := SplitTags(nil); named != nil || rest != nil {
		t.Fatalf("empty split = %v, %v", named, rest)
	}
}

func TestOfReportsOnlyAnUnambiguousProject(t *testing.T) {
	if got := Of([]string{"tui", "project::kb"}); got != "kb" {
		t.Fatalf("Of one = %q", got)
	}
	if got := Of([]string{"tui"}); got != "" {
		t.Fatalf("Of none = %q", got)
	}
	if got := Of([]string{"project::kb", "project::web"}); got != "" {
		t.Fatalf("Of two = %q", got)
	}
}

// TestEnsureLeavesExactlyOneProject is the invariant every write surface leans
// on: whatever the tags carried, what comes back names one project once.
func TestEnsureLeavesExactlyOneProject(t *testing.T) {
	for _, tags := range [][]string{
		nil,
		{"tui"},
		{"project::old", "tui"},
		{"project::old", "tui", "project::older"},
	} {
		got, err := Ensure(tags, " kb ")
		if err != nil {
			t.Fatalf("Ensure(%v) = %v", tags, err)
		}
		named, rest := SplitTags(got)
		if !reflect.DeepEqual(named, []string{"kb"}) {
			t.Fatalf("Ensure(%v) projects = %v", tags, named)
		}
		for _, tag := range rest {
			if strings.HasPrefix(tag, LabelPrefix) {
				t.Fatalf("Ensure(%v) kept a project label in %v", tags, rest)
			}
		}
	}
	if _, err := Ensure([]string{"tui"}, ""); err == nil {
		t.Fatal("Ensure accepted an empty project")
	}
}

func TestLeadPutsTheProjectPillFirst(t *testing.T) {
	got := Lead([]string{"tui", "type::feature", "project::kb"})
	if !reflect.DeepEqual(got, []string{"project::kb", "tui", "type::feature"}) {
		t.Fatalf("Lead = %v", got)
	}
	// A broken task carrying two keeps both, still ahead of the plain labels.
	got = Lead([]string{"tui", "project::kb", "project::web"})
	if !reflect.DeepEqual(got, []string{"project::kb", "project::web", "tui"}) {
		t.Fatalf("Lead of two = %v", got)
	}
	unchanged := []string{"tui", "type::feature"}
	if got := Lead(unchanged); !reflect.DeepEqual(got, unchanged) {
		t.Fatalf("Lead without a project = %v", got)
	}
	if got := Lead(nil); len(got) != 0 {
		t.Fatalf("Lead(nil) = %v", got)
	}
}

// TestInboxIsTheBackfillName pins the constant the CLI backfill spells, so a
// rename here cannot silently split the two surfaces.
func TestInboxIsTheBackfillName(t *testing.T) {
	if Inbox != "inbox" || Scope != "project" || LabelPrefix != "project::" {
		t.Fatalf("vocabulary drifted: %q %q %q", Inbox, Scope, LabelPrefix)
	}
}
