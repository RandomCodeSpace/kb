package main

import (
	"bytes"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestClassifyMigrationFixtures(t *testing.T) {
	const module = "example.test/kb"
	repo := t.TempDir()
	packages := []packageInfo{{ImportPath: module + "/internal/store", Dir: filepath.Join(repo, "internal/store")}}
	for _, tc := range []struct {
		path          string
		wantMigration bool
		wantOwner     bool
	}{
		{"internal/store/testdata/migrations/v0.2.0/kb.db", true, true},
		{"internal/store/testdata/migrations/manifest.json", true, true},
		{"internal/store/released_fixtures_test.go", true, true},
		{"scripts/generate-migration-fixtures.py", true, false},
		{"internal/store/testdata/ordinary.json", false, true},
		{"internal/store/testdata/migrations-backup/kb.db", false, true},
	} {
		t.Run(tc.path, func(t *testing.T) {
			got := classify(repo, "base", "head", module, []change{{Status: "M", Paths: []string{tc.path}}}, packages)
			if got.Checks.MigrationRecovery != tc.wantMigration {
				t.Errorf("migration recovery = %v, want %v", got.Checks.MigrationRecovery, tc.wantMigration)
			}
			if got.Checks.FocusedQuality != tc.wantOwner {
				t.Errorf("focused quality = %v, want %v", got.Checks.FocusedQuality, tc.wantOwner)
			}
			if hasOwner := slices.Contains(got.Go.Owners, module+"/internal/store"); hasOwner != tc.wantOwner {
				t.Errorf("store owner = %v, want %v", hasOwner, tc.wantOwner)
			}
			if hasReason := slices.Contains(got.Reasons["migration_recovery"], tc.path); hasReason != tc.wantMigration {
				t.Errorf("migration reason = %v, want %v", hasReason, tc.wantMigration)
			}
			if len(got.Unclassified) != 0 {
				t.Errorf("unclassified paths = %v", got.Unclassified)
			}
		})
	}
}

func TestClassifyReadinessContracts(t *testing.T) {
	for _, tc := range []struct {
		path string
		want checks
	}{
		{"CONTEXT.md", checks{DocsContract: true}},
		{"scripts/check-go-vuln.sh", checks{BinaryReleaseContract: true, CIContract: true}},
		{"scripts/ci/impactcmd/main.go", checks{CIContract: true, WebSmoke: true}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			repo := t.TempDir()
			packages := []packageInfo{{ImportPath: "example.test/kb/scripts/ci/impactcmd", Dir: filepath.Join(repo, "scripts/ci/impactcmd")}}
			got := classify(repo, "base", "head", "example.test/kb", []change{{Status: "M", Paths: []string{tc.path}}}, packages)
			if got.Checks != tc.want {
				t.Errorf("checks = %+v, want %+v", got.Checks, tc.want)
			}
			if len(got.Go.Owners) != 0 {
				t.Errorf("non-application change selected coverage owners: %v", got.Go.Owners)
			}
			if len(got.Unclassified) != 0 {
				t.Errorf("unclassified paths = %v", got.Unclassified)
			}
		})
	}
}

func TestRenderingChangesSelectPerformance(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"internal/tui/board_view.go", true},
		{"internal/tui/board_view_test.go", true},
		{"internal/tui/widget/card.go", true},
		{"internal/tui/widget/spin/spin.go", true},
		{"internal/tui/carddetail/model.go", true},
		{"internal/tui/carddetail/testdata/view.golden", true},
		{"internal/tui/theme/styles.go", true},
		{"internal/tui/theme/audit_test.go", true},
		{"internal/tui/preferences.go", false},
		{"internal/tui/widget_extra.go", false},
		{"README.md", false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			got := classify(t.TempDir(), "base", "head", "example.test/kb", []change{{Status: "M", Paths: []string{tc.path}}}, nil)
			if got.Checks.TUIPerformance != tc.want || slices.Contains(got.Reasons["tui_performance"], tc.path) != tc.want {
				t.Fatalf("performance = %v, reasons = %v, want %v", got.Checks.TUIPerformance, got.Reasons, tc.want)
			}
		})
	}
}

func TestBrowserChangesSelectSmoke(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"internal/webui/static/app.js", true},
		{"internal/webui/tailwind/app.css", true},
		{"internal/webui/server.go", true},
		{"internal/webui/e2e/package-lock.json", true},
		{"internal/webui/e2e/board.spec.js", true},
		{"scripts/build-web-css.sh", true},
		{"scripts/ci/impactcmd/main.go", true},
		{"scripts/ci/impactcmd/main_test.go", true},
		{".github/workflows/quality.yml", true},
		{"internal/webui_extra.go", false},
		{"README.md", false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			got := classify(t.TempDir(), "base", "head", "example.test/kb", []change{{Status: "M", Paths: []string{tc.path}}}, nil)
			if got.Checks.WebSmoke != tc.want || slices.Contains(got.Reasons["web_smoke"], tc.path) != tc.want {
				t.Fatalf("web smoke = %v, reasons = %v, want %v", got.Checks.WebSmoke, got.Reasons, tc.want)
			}
			assertBrowserCheckOutputs(t, got, tc.want)
			if len(got.Unclassified) != 0 {
				t.Fatalf("unclassified = %v", got.Unclassified)
			}
		})
	}
}

func assertBrowserCheckOutputs(t *testing.T, got manifest, want bool) {
	t.Helper()
	for format, prefix := range map[string]string{"github": "web_smoke=", "plan": "check\tweb_smoke\t"} {
		var output bytes.Buffer
		if err := writeManifest(&output, got, format); err != nil {
			t.Fatalf("write %s manifest: %v", format, err)
		}
		var records []string
		for _, line := range strings.Split(output.String(), "\n") {
			if strings.HasPrefix(line, prefix) {
				records = append(records, line)
			}
		}
		wantLine := prefix + strconv.FormatBool(want)
		if len(records) != 1 || records[0] != wantLine {
			t.Errorf("%s web smoke records = %v, want [%s]", format, records, wantLine)
		}
	}
}
