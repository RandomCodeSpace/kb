package theme

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Display literals belong to the theme vocabulary. Parse Go literals so
// escaped Unicode is checked too, while comments and user data are excluded.
func TestNoGlyphLiteralsOutsideTheme(t *testing.T) {
	walkTUI(t, tuiRoot(t), func(relative, source string) {
		if strings.HasSuffix(relative, "_test.go") || strings.HasPrefix(relative, "testdata/") || strings.Contains(relative, "/testdata/") {
			return
		}
		// These helpers define Unicode parsing and case conversion for user
		// content. Their code points are data rules, not display vocabulary.
		if relative == "mdparity/mdparity.go" || relative == "web_lower.go" {
			return
		}
		for _, position := range glyphLiteralPositions(t, relative, source) {
			t.Errorf("%s contains a non-ASCII display literal; use theme.Glyphs", position)
		}
	})
}

func glyphLiteralPositions(t *testing.T, name, source string) []token.Position {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, source, 0)
	if err != nil {
		t.Fatal(err)
	}
	var positions []token.Position
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || (literal.Kind != token.STRING && literal.Kind != token.CHAR) {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range value {
			if r >= 0xA0 {
				positions = append(positions, fset.Position(literal.Pos()))
				break
			}
		}
		return true
	})
	return positions
}

func TestGlyphLiteralGuardRecognizesEscapes(t *testing.T) {
	for _, literal := range []string{`"×"`, `"\u00d7"`, "`×`", `'\u00d7'`} {
		positions := glyphLiteralPositions(t, "view.go", "package view\nvar marker = "+literal)
		if len(positions) != 1 || positions[0].Line != 2 {
			t.Errorf("literal %s: positions = %v, want one on line 2", literal, positions)
		}
	}
	if positions := glyphLiteralPositions(t, "view.go", "package view\n// × is a glyph\nvar marker = \"ASCII\""); len(positions) != 0 {
		t.Errorf("ASCII or comment reported: %v", positions)
	}
}

// seamAllowlist is the set of files under internal/tui that still construct
// lipgloss styles by hand. Spec sections 6.2 and 9.2: views take a *Styles and
// never construct styles, and the seam is enforced rather than merely
// documented.
//
// The list may only shrink. Every restyle slice that migrates a view deletes
// its entry; nothing may be added. A view that cannot render without
// constructing a style has found a missing token, and the fix is a new token,
// not an exemption.
// The list is empty: issue #153 migrated the last view off hand-built styles by
// adopting bubbles/help for the ? overlay body. Nothing may be added to it.
var seamAllowlist = map[string]bool{}

// seamMarker is the construction call the seam bans.
const seamMarker = "lipgloss.NewStyle"

// timingMarkers are the scheduling calls and duration literals the timing seam
// bans outside this package. Spec section 10.3.9 rule 6: every timing value in
// the TUI is a named token on theme.Timing, scheduled through theme.Tick, so a
// duration written at a call site is a design decision nobody reviewed.
var timingMarkers = []string{
	"tea.Tick(",
	"tea.Every(",
	"time.Sleep(",
	"time.After(",
	"time.Millisecond",
	"time.Second",
}

// timingAllowlist is the set of production files under internal/tui that still
// name a duration. Spec section 10.3.8: settings.go's settingsTestTimeout is
// the deadline on a forge connection test - an I/O bound, not a feel constant,
// and theme is not where network policy lives.
//
// Like the style allowlist, it may only shrink.
var timingAllowlist = map[string]bool{"settings.go": true}

// sleepMarker is the one timing call banned in tests too. Spec section 10.3.9
// rule 5: no test under internal/tui sleeps. The other markers stay production
// only, because a teatest harness deadline is a test's own cap on how long it
// will wait for a content predicate, not a duration the TUI schedules against.
const sleepMarker = "time.Sleep("

// TestNoInlineTimingOutsideTheme walks internal/tui and fails on any scheduling
// call or duration literal in production code that the allowlist does not
// cover.
func TestNoInlineTimingOutsideTheme(t *testing.T) {
	root := tuiRoot(t)
	found := map[string]bool{}
	walkTUI(t, root, func(relative, source string) {
		if strings.HasSuffix(relative, "_test.go") {
			return
		}
		for _, marker := range timingMarkers {
			if !strings.Contains(source, marker) {
				continue
			}
			found[relative] = true
			if !timingAllowlist[relative] {
				t.Errorf("%s names %q; timing values are theme.Timing tokens scheduled through theme.Tick", relative, marker)
			}
		}
	})
	for _, relative := range sortedKeys(timingAllowlist) {
		if !found[relative] {
			t.Errorf("%s no longer names a duration; delete its timing allowlist entry", relative)
		}
	}
}

// TestTimingAllowlistOnlyShrinks pins the allowlist size so a slice cannot
// quietly trade one exemption for another.
func TestTimingAllowlistOnlyShrinks(t *testing.T) {
	const atMost = 1
	if len(timingAllowlist) > atMost {
		t.Fatalf("timing allowlist has %d entries, at most %d are allowed", len(timingAllowlist), atMost)
	}
}

// TestNoSleepsUnderTUI covers tests as well as production code: a slept test is
// the flake generator the whole timing family exists to keep out.
func TestNoSleepsUnderTUI(t *testing.T) {
	root := tuiRoot(t)
	walkTUI(t, root, func(relative, source string) {
		if strings.Contains(source, sleepMarker) {
			t.Errorf("%s sleeps; step the timer with an injected message instead", relative)
		}
	})
}

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
	const atMost = 0
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
