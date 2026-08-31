package store

import (
	"sync"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
)

func TestAddTaskWithImportLinkIsAtomicAndDeletionKeepsProvenance(t *testing.T) {
	s := newStore(t)
	link := ImportLink{Source: "primary", Kind: "github", ExternalKey: "github:primary@example.test/acme/kb#93", Link: "github#93", URL: "https://example.test/acme/kb/issues/93", Title: "Imported"}
	baseline := NewImportBaseline("Imported upstream", "original body", "2026-08-31T00:00:00Z")
	created, err := s.AddTaskWithImportLink("alice", board.Task{Title: "Imported", Tags: []string{"link::github#93", "import::" + link.ExternalKey}}, link, baseline)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CancelTask("alice", created.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteCancelledTask("alice", created.ID); err != nil {
		t.Fatal(err)
	}
	provenance, err := s.ImportedAs("alice", []string{link.ExternalKey})
	if err != nil || provenance[link.ExternalKey].URL != link.URL {
		t.Fatalf("provenance after deletion = %+v, %v", provenance, err)
	}

	bad := link
	bad.URL = "bad\nurl"
	if _, err := s.AddTaskWithImportLink("alice", board.Task{Title: "Must roll back"}, bad, baseline); err == nil {
		t.Fatal("invalid provenance created a task")
	}
	tasks, err := s.ListTasks("alice", "")
	if err != nil || len(tasks) != 0 {
		t.Fatalf("rolled-back tasks = %+v, %v", tasks, err)
	}
	stored, present, err := s.ImportBaseline("alice", link.ExternalKey)
	if err != nil || !present || stored != baseline {
		t.Fatalf("atomic baseline = %+v, %t, %v", stored, present, err)
	}
	later := NewImportBaseline("Later upstream", "changed body", "2026-09-01T00:00:00Z")
	if _, err := s.AddTaskWithImportLink("alice", board.Task{Title: "Reimported"}, link, later); err != nil {
		t.Fatal(err)
	}
	stored, present, err = s.ImportBaseline("alice", link.ExternalKey)
	if err != nil || !present || stored != baseline {
		t.Fatalf("reimport replaced established baseline = %+v, %t, %v", stored, present, err)
	}
}

func TestAddTaskWithImportLinkRollsBackWhenBaselineWriteFails(t *testing.T) {
	s := newStore(t)
	if _, err := s.db.Exec(`CREATE TRIGGER fail_import_baseline BEFORE UPDATE OF baseline_title ON import_links BEGIN SELECT RAISE(ABORT, 'baseline failed'); END`); err != nil {
		t.Fatal(err)
	}
	link := ImportLink{Source: "primary", Kind: "github", ExternalKey: "github:primary@example.test/acme/kb#94", Link: "github#94", URL: "https://example.test/acme/kb/issues/94", Title: "Imported"}
	baseline := NewImportBaseline("Imported", "body", "2026-08-31T00:00:00Z")
	if _, err := s.AddTaskWithImportLink("alice", board.Task{Title: "Must roll back"}, link, baseline); err == nil {
		t.Fatal("baseline failure accepted the import")
	}
	if tasks, err := s.ListTasks("alice", ""); err != nil || len(tasks) != 0 {
		t.Fatalf("baseline failure left tasks = %+v, %v", tasks, err)
	}
	if found, err := s.ImportedAs("alice", []string{link.ExternalKey}); err != nil || len(found) != 0 {
		t.Fatalf("baseline failure left provenance = %+v, %v", found, err)
	}
}

func TestImportBaselineCreateAndCASAreAtomicAcrossCallers(t *testing.T) {
	s := newStore(t)
	const key = "github:primary@example.test/acme/kb#93"
	recordBaselineLink(t, s, "alice", key)
	candidates := []ImportBaseline{
		NewImportBaseline("one", "body one", "one"),
		NewImportBaseline("two", "body two", "two"),
	}
	created := make([]bool, 2)
	returned := make([]ImportBaseline, 2)
	var wg sync.WaitGroup
	for index := range candidates {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			var err error
			returned[index], created[index], err = s.CreateImportBaseline("alice", key, candidates[index])
			if err != nil {
				t.Errorf("create %d: %v", index, err)
			}
		}(index)
	}
	wg.Wait()
	if created[0] == created[1] {
		t.Fatalf("created flags = %v, want one winner", created)
	}
	winner, present, err := s.ImportBaseline("alice", key)
	if err != nil || !present || (winner != candidates[0] && winner != candidates[1]) {
		t.Fatalf("winner = %+v, %t, %v", winner, present, err)
	}
	for index := range returned {
		if returned[index] != winner {
			t.Fatalf("caller %d saw %+v, want winner %+v", index, returned[index], winner)
		}
	}

	next := []ImportBaseline{
		NewImportBaseline("next one", "body", "next-one"),
		NewImportBaseline("next two", "body", "next-two"),
	}
	swapped := make([]bool, 2)
	for index := range next {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			var err error
			swapped[index], err = s.CompareAndSwapImportBaseline("alice", key, winner, next[index])
			if err != nil {
				t.Errorf("swap %d: %v", index, err)
			}
		}(index)
	}
	wg.Wait()
	if swapped[0] == swapped[1] {
		t.Fatalf("swapped flags = %v, want one winner", swapped)
	}
}

func TestImportAtomicOperationsRejectMissingAndInvalidState(t *testing.T) {
	s := newStore(t)
	valid := NewImportBaseline("title", "body", "at")
	invalid := valid
	invalid.Title = "bad\nname"
	if _, _, err := s.CreateImportBaseline("alice", "missing", valid); err == nil {
		t.Fatal("missing provenance accepted a baseline")
	}
	if _, _, err := s.CreateImportBaseline("alice", "missing", invalid); err == nil {
		t.Fatal("invalid baseline accepted")
	}
	if _, err := s.CompareAndSwapImportBaseline("alice", "missing", invalid, valid); err == nil {
		t.Fatal("invalid expected baseline accepted")
	}
	if _, err := s.CompareAndSwapImportBaseline("alice", "missing", valid, invalid); err == nil {
		t.Fatal("invalid next baseline accepted")
	}
	if _, err := s.CompareAndSwapImportBaseline("alice", "missing", valid, valid); err == nil {
		t.Fatal("missing provenance accepted a swap")
	}

	link := ImportLink{Source: "primary", Kind: "github", ExternalKey: "qualified", Link: "github#1", URL: "https://example.test/owner/repo/issues/1", Title: "issue"}
	if _, err := s.AddTaskWithImportLink("alice", board.Task{Title: "issue", Status: board.Status("invalid")}, link, valid); err == nil {
		t.Fatal("invalid task status accepted")
	}
	if _, err := s.AddTaskWithImportLink("alice", board.Task{Title: "issue"}, link, ImportBaseline{}); err == nil {
		t.Fatal("missing import baseline accepted")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTaskWithImportLink("alice", board.Task{Title: "issue"}, link, valid); err == nil {
		t.Fatal("closed store accepted atomic import")
	}
	if _, _, err := s.CreateImportBaseline("alice", "qualified", valid); err == nil {
		t.Fatal("closed store created baseline")
	}
	if _, err := s.CompareAndSwapImportBaseline("alice", "qualified", valid, valid); err == nil {
		t.Fatal("closed store swapped baseline")
	}
	if err := s.SetImportBaseline("alice", "qualified", valid); err == nil {
		t.Fatal("closed store set baseline")
	}
	if _, _, err := s.ImportBaseline("alice", "qualified"); err == nil {
		t.Fatal("closed store read baseline")
	}
}
