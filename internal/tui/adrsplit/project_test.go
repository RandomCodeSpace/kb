package adrsplit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/ai"
)

// testProject is the active project the package's tests run under. Every card
// the overlay creates carries a project now, and these tests are about the
// overlay, not about resolution, so one env-level default keeps them readable.
const testProject = "adr"

func TestMain(m *testing.M) {
	os.Setenv("KB_PROJECT", testProject)
	os.Exit(m.Run())
}

func projectsOf(tags []string) []string {
	var found []string
	for _, tag := range tags {
		if name, ok := strings.CutPrefix(tag, "project::"); ok {
			found = append(found, name)
		}
	}
	return found
}

// TestBatchAddStampsExactlyOneProject pins the ADR-split write path: every
// card in the batch lands with the active project, keeping its own labels.
func TestBatchAddStampsExactlyOneProject(t *testing.T) {
	m, st, _ := newTestModel()
	m.rows = rowsFromDrafts([]ai.Draft{testDraft("one"), testDraft("two")})
	m.stage = stageReview
	command := m.startAdd()
	for command != nil {
		message := commandMsg(t, command)
		added, ok := message.(cardAddedMsg)
		if !ok {
			break
		}
		command = m.finishAdd(added)
	}
	if len(st.calls) != 2 || m.failedCount != 0 {
		t.Fatalf("added=%d failed=%d status=%q", len(st.calls), m.failedCount, m.status)
	}
	for _, task := range st.calls {
		if got := projectsOf(task.Tags); len(got) != 1 || got[0] != testProject {
			t.Fatalf("task %q tags = %v, want exactly one %s label", task.Title, task.Tags, testProject)
		}
		if !strings.Contains(strings.Join(task.Tags, ","), "tui") {
			t.Fatalf("task %q lost its own labels: %v", task.Title, task.Tags)
		}
	}
}

// TestBatchAddRefusesWithoutAProject pins that a board with no project
// resolved fails the row instead of writing a card without one.
func TestBatchAddRefusesWithoutAProject(t *testing.T) {
	t.Setenv("KB_PROJECT", "")
	m, st, _ := newTestModel()
	// An empty data directory has no state.json, so nothing resolves.
	m.SetDataDir(t.TempDir())
	m.rows = rowsFromDrafts([]ai.Draft{testDraft("one")})
	m.stage = stageReview
	message := commandMsg(t, m.startAdd())
	added, ok := message.(cardAddedMsg)
	if !ok {
		t.Fatalf("batch message = %T, want cardAddedMsg", message)
	}
	m.finishAdd(added)
	if len(st.calls) != 0 {
		t.Fatalf("wrote %d cards without a project", len(st.calls))
	}
	if m.failedCount != 1 || !strings.Contains(m.rows[0].err, "kb project use") {
		t.Fatalf("failed=%d row err=%q", m.failedCount, m.rows[0].err)
	}
}

// TestSetDataDirNamesTheStateDirectory pins that the overlay resolves the
// project from the directory the board lives in.
func TestSetDataDirNamesTheStateDirectory(t *testing.T) {
	t.Setenv("KB_PROJECT", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"active_project":"stored"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m, st, _ := newTestModel()
	m.SetDataDir(dir)
	m.rows = rowsFromDrafts([]ai.Draft{testDraft("one")})
	m.stage = stageReview
	added, ok := commandMsg(t, m.startAdd()).(cardAddedMsg)
	if !ok {
		t.Fatal("batch did not report a card")
	}
	m.finishAdd(added)
	if len(st.calls) != 1 {
		t.Fatalf("added %d cards (row err %q)", len(st.calls), m.rows[0].err)
	}
	if got := projectsOf(st.calls[0].Tags); len(got) != 1 || got[0] != "stored" {
		t.Fatalf("project = %v, want the stored one", got)
	}
}
