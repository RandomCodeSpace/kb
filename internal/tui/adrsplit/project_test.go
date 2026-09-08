package adrsplit

import (
	"strings"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/ai"
)

// testProject is the project the package's tests open the overlay under.
// Every card the overlay creates carries a project, and these tests are about
// the overlay, not about resolution, so one default keeps them readable.
const testProject = "adr"

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
// card in the batch lands under the board's project, keeping its own labels.
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

// TestBatchAddRefusesWithoutAProject pins that a board under "all" fails the
// row instead of writing a card without a project.
func TestBatchAddRefusesWithoutAProject(t *testing.T) {
	m, st, _ := newTestModel()
	m.SetProject("")
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
	if m.failedCount != 1 || !strings.Contains(m.rows[0].err, "no project given") {
		t.Fatalf("failed=%d row err=%q", m.failedCount, m.rows[0].err)
	}
}

// TestSetProjectNamesTheBatchProject pins that the project set on the overlay
// is the one every split card lands under.
func TestSetProjectNamesTheBatchProject(t *testing.T) {
	m, st, _ := newTestModel()
	m.SetProject("handed")
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
	if got := projectsOf(st.calls[0].Tags); len(got) != 1 || got[0] != "handed" {
		t.Fatalf("project = %v, want the handed one", got)
	}
}
