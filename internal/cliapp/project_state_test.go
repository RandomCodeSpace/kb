package cliapp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SetActiveProject is the write half of ActiveProject: what it stores is what
// `kb project current` and every local surface then resolve.
func TestSetActiveProjectRoundTrips(t *testing.T) {
	dir := noProjectEnv(t)
	if err := SetActiveProject(dir, "  web  "); err != nil {
		t.Fatalf("SetActiveProject: %v", err)
	}
	name, ok, err := ActiveProject(dir)
	if err != nil || !ok || name != "web" {
		t.Fatalf("ActiveProject = %q, %v, %v; want web, true, nil", name, ok, err)
	}
	if err := SetActiveProject(dir, "docs"); err != nil {
		t.Fatalf("second SetActiveProject: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, cliStateFile))
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != `{"active_project":"docs"}` {
		t.Fatalf("state file = %s", got)
	}
}

func TestSetActiveProjectRejectsInvalidName(t *testing.T) {
	dir := noProjectEnv(t)
	if err := SetActiveProject(dir, "bad name"); err == nil ||
		!strings.Contains(err.Error(), "whitespace") {
		t.Fatalf("SetActiveProject(bad name) = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, cliStateFile)); !os.IsNotExist(err) {
		t.Fatalf("invalid name wrote a state file: %v", err)
	}
}

func TestSetActiveProjectReportsDataDirFailure(t *testing.T) {
	t.Setenv("KB_PROJECT", "")
	t.Setenv("KB_DATA", "")
	t.Setenv("HOME", "")
	if err := SetActiveProject("", "web"); err == nil ||
		!strings.Contains(err.Error(), "home directory") {
		t.Fatalf("SetActiveProject with no data dir = %v", err)
	}
}

func TestSetActiveProjectReportsUnreadableState(t *testing.T) {
	dir := noProjectEnv(t)
	if err := os.WriteFile(filepath.Join(dir, cliStateFile), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetActiveProject(dir, "web"); err == nil ||
		!strings.Contains(err.Error(), "decode cli state") {
		t.Fatalf("SetActiveProject over broken state = %v", err)
	}
}
