package theme

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// seamAllowlist is the set of files under internal/tui that still construct
// lipgloss styles by hand. Spec sections 6.2 and 9.2: views take a *Styles and
// never construct styles, and the seam is enforced rather than merely
// documented.
//
// The list may only shrink. Every restyle slice that migrates a view deletes
// its entry; nothing may be added. A view that cannot render without
// constructing a style has found a missing token, and the fix is a new token,
// not an exemption.
var seamAllowlist = map[string]bool{
	"adrsplit/view.go":    true,
	"board_view.go":       true,
	"carddetail/model.go": true,
	"help.go":             true,
	"issueimport/view.go": true,
}

// seamMarker is the construction call the seam bans.
const seamMarker = "lipgloss.NewStyle"

// TestNoStyleConstructionOutsideTheme walks internal/tui and fails on any style
// construction outside this package that the allowlist does not cover.
func TestNoStyleConstructionOutsideTheme(t *testing.T) {
	root := tuiRoot(t)
	found := map[string]bool{}
	walkTUI(t, root, func(relative, source string) {
		if !strings.Contains(source, seamMarker) {
			return
		}
		found[relative] = true
		if !seamAllowlist[relative] {
			t.Errorf("%s constructs a lipgloss style; views take a *theme.Styles and never build one", relative)
		}
	})
	for _, relative := range sortedKeys(seamAllowlist) {
		if !found[relative] {
			t.Errorf("%s no longer constructs a style; delete its seam allowlist entry", relative)
		}
	}
}

// TestSeamAllowlistOnlyShrinks pins the allowlist size so a migration slice
// cannot quietly trade one exemption for another.
func TestSeamAllowlistOnlyShrinks(t *testing.T) {
	const atMost = 5
	if len(seamAllowlist) > atMost {
		t.Fatalf("seam allowlist has %d entries, at most %d are allowed", len(seamAllowlist), atMost)
	}
}

// TestThemeIsTheOnlyStyleFactory keeps the exemption from applying to this
// package's own tree: only the theme package builds styles, and the widget
// package next door must not.
func TestThemeIsTheOnlyStyleFactory(t *testing.T) {
	root := tuiRoot(t)
	walkTUI(t, root, func(relative, source string) {
		if strings.HasPrefix(relative, "widget/") && strings.Contains(source, seamMarker) {
			t.Errorf("%s constructs a lipgloss style; widgets compose cached styles only", relative)
		}
	})
}

// tuiRoot is internal/tui, the parent of this package's directory.
func tuiRoot(t *testing.T) string {
	t.Helper()
	working, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	return filepath.Dir(working)
}

// walkTUI visits every Go source file under internal/tui except this package's
// own, which is the one place styles are allowed to be built.
func walkTUI(t *testing.T, root string, visit func(relative, source string)) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && filepath.Base(path) == "theme" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		visit(filepath.ToSlash(relative), string(source))
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
