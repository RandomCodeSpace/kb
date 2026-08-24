package issueimport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/ai"
	"github.com/RandomCodeSpace/kb/internal/forge"
	"github.com/RandomCodeSpace/kb/internal/store"
)

// testProject is the active project the package's tests run under. Every
// imported card carries a project now, and these tests are about the overlay,
// not about resolution, so one env-level default keeps them readable.
const testProject = "import"

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

// importReady opens the overlay on one reviewable draft, ready for a create.
func importReady(t *testing.T) (Model, *fakeStore) {
	t.Helper()
	st := &fakeStore{}
	backend := &fakeBackend{
		sources: []store.ForgeSource{{Name: "primary", Kind: "github", BaseURL: "https://github.example"}},
		preview: forge.Preview{Kind: "issue", Fetched: 1, Drafts: []forge.Draft{{
			Draft:       ai.Draft{Title: "imported", Tags: []string{"type::bug"}},
			Link:        "github#1",
			ExternalKey: "github:primary@github.example/acme/kb#1",
			URL:         "https://github.example/acme/kb/issues/1",
		}}},
	}
	m := openModel(t, backend, st)
	m.ref.SetValue("acme/kb")
	m.Update(runCmd(m.startPreview()))
	return m, st
}

// TestImportStampsExactlyOneProject pins the import write path: the flow picks
// the active project and every created card carries it, keeping its own labels.
func TestImportStampsExactlyOneProject(t *testing.T) {
	m, st := importReady(t)
	m.Update(runCmd(m.startCreate()))
	if len(st.added) != 1 {
		t.Fatalf("created %d cards (row err %q)", len(st.added), m.rows[0].err)
	}
	task := st.added[0]
	if got := projectsOf(task.Tags); len(got) != 1 || got[0] != testProject {
		t.Fatalf("tags = %v, want exactly one %s label", task.Tags, testProject)
	}
	if !strings.Contains(strings.Join(task.Tags, ","), "type::bug") {
		t.Fatalf("card lost its own labels: %v", task.Tags)
	}
}

// TestImportRefusesWithoutAProject pins that a board with no project resolved
// fails the row instead of importing a card without one.
func TestImportRefusesWithoutAProject(t *testing.T) {
	t.Setenv("KB_PROJECT", "")
	m, st := importReady(t)
	// An empty data directory has no state.json, so nothing resolves.
	m.SetDataDir(t.TempDir())
	m.Update(runCmd(m.startCreate()))
	if len(st.added) != 0 {
		t.Fatalf("imported %d cards without a project", len(st.added))
	}
	if !strings.Contains(m.rows[0].err, "kb project use") {
		t.Fatalf("row err = %q, want the project refusal", m.rows[0].err)
	}
}

// TestImportUsesTheStoredActiveProject pins that the overlay resolves the
// project from the data directory the board lives in.
func TestImportUsesTheStoredActiveProject(t *testing.T) {
	t.Setenv("KB_PROJECT", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"active_project":"stored"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m, st := importReady(t)
	m.SetDataDir(dir)
	m.Update(runCmd(m.startCreate()))
	if len(st.added) != 1 {
		t.Fatalf("created %d cards (row err %q)", len(st.added), m.rows[0].err)
	}
	if got := projectsOf(st.added[0].Tags); len(got) != 1 || got[0] != "stored" {
		t.Fatalf("project = %v, want the stored one", got)
	}
}
