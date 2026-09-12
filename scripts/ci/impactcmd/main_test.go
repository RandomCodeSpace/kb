package main

import (
	"path/filepath"
	"slices"
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
		{"scripts/ci/impactcmd/main.go", checks{CIContract: true}},
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
