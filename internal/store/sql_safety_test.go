package store

import (
	"reflect"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
)

func TestTasksByLinkBindsInjectionShapedScopeAndLink(t *testing.T) {
	s := newStore(t)
	scope := "alice' OR 1=1 --"
	link := "link::gitlab#1')OR(1=1)--"
	own, err := s.AddTask(scope, board.Task{Title: "own", Tags: []string{link}})
	if err != nil {
		t.Fatalf("AddTask(injection-shaped scope): %v", err)
	}
	if _, err := s.AddTask(scope, board.Task{Title: "wrong link", Tags: []string{"link::gitlab#other"}}); err != nil {
		t.Fatalf("AddTask(wrong link): %v", err)
	}
	if _, err := s.AddTask("victim", board.Task{Title: "foreign", Tags: []string{link}}); err != nil {
		t.Fatalf("AddTask(foreign scope): %v", err)
	}

	got, err := s.TasksByLink(scope, link)
	if err != nil {
		t.Fatalf("TasksByLink: %v", err)
	}
	want := []SimilarHit{{ID: own.ID, Title: own.Title, Status: string(own.Status), Via: "card", Link: link}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TasksByLink = %+v, want only scoped exact match %+v", got, want)
	}
}

func TestImportedAsBindsInjectionShapedScopeAndExternalKeys(t *testing.T) {
	s := newStore(t)
	scope := "alice' OR 1=1 --"
	keys := []string{"shared-key", "key') OR 1=1 --", "foreign-only", "missing"}
	own := []ImportLink{
		{ExternalKey: keys[0], Link: "link::own-shared", Title: "own shared"},
		{ExternalKey: keys[1], Link: "link::own-shaped", Title: "own shaped"},
	}
	foreign := []ImportLink{
		{ExternalKey: keys[0], Link: "link::foreign-shared", Title: "foreign shared"},
		{ExternalKey: keys[1], Link: "link::foreign-shaped", Title: "foreign shaped"},
		{ExternalKey: keys[2], Link: "link::foreign-only", Title: "foreign only"},
	}
	if err := s.RecordImportLinks(scope, own); err != nil {
		t.Fatalf("RecordImportLinks(injection-shaped scope): %v", err)
	}
	if err := s.RecordImportLinks("victim", foreign); err != nil {
		t.Fatalf("RecordImportLinks(foreign scope): %v", err)
	}

	got, err := s.ImportedAs(scope, keys)
	if err != nil {
		t.Fatalf("ImportedAs: %v", err)
	}
	if _, ok := got[keys[2]]; ok {
		t.Fatalf("ImportedAs returned foreign-only key %q: %+v", keys[2], got)
	}
	want := map[string]ImportLink{keys[0]: own[0], keys[1]: own[1]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ImportedAs = %+v, want only scoped requested keys %+v", got, want)
	}
}

func TestReplaceBoardBindsInjectionShapedScopeWhenLoadingExistingTasks(t *testing.T) {
	s := newStore(t)
	scope := "alice' OR 1=1 --"
	scopedSeed := board.Board{Title: "Scoped", Tasks: []board.Task{
		{Title: "dummy", Status: board.StatusTodo, Prio: 3},
		{Title: "same title", Status: board.StatusTodo, Prio: 3},
	}}
	foreignSeed := board.Board{Title: "Foreign", Tasks: []board.Task{{Title: "same title", Status: board.StatusTodo, Prio: 3}}}
	if err := s.ReplaceBoard(scope, scopedSeed); err != nil {
		t.Fatalf("ReplaceBoard(injection-shaped scope): %v", err)
	}
	if err := s.ReplaceBoard("victim", foreignSeed); err != nil {
		t.Fatalf("ReplaceBoard(foreign scope): %v", err)
	}
	before, err := s.Board(scope)
	if err != nil {
		t.Fatalf("Board(injection-shaped scope): %v", err)
	}
	foreignBefore, err := s.Board("victim")
	if err != nil {
		t.Fatalf("Board(foreign scope): %v", err)
	}
	if len(before.Tasks) != 2 || before.Tasks[0].Title != "dummy" || before.Tasks[0].Position != 0 || before.Tasks[1].Title != "same title" || before.Tasks[1].Position != 1 {
		t.Fatalf("scoped ordering precondition failed: %+v", before.Tasks)
	}
	if len(foreignBefore.Tasks) != 1 || foreignBefore.Tasks[0].Title != "same title" || foreignBefore.Tasks[0].Position != 0 {
		t.Fatalf("foreign ordering precondition failed: %+v", foreignBefore.Tasks)
	}

	updated := board.Board{Title: "Scoped", Tasks: []board.Task{
		{Title: "dummy", Status: board.StatusTodo, Prio: 3},
		{Title: "same title", Desc: "updated", Status: board.StatusTodo, Prio: 3},
	}}
	if err := s.ReplaceBoard(scope, updated); err != nil {
		t.Fatalf("ReplaceBoard update: %v", err)
	}
	after, err := s.Board(scope)
	if err != nil {
		t.Fatalf("Board after update: %v", err)
	}
	foreignAfter, err := s.Board("victim")
	if err != nil {
		t.Fatalf("Board(foreign scope) after update: %v", err)
	}
	if len(before.Tasks) != 2 || len(after.Tasks) != 2 || after.Tasks[1].ID != before.Tasks[1].ID {
		t.Fatalf("scoped identity changed: before=%+v after=%+v", before.Tasks, after.Tasks)
	}
	if !reflect.DeepEqual(foreignAfter, foreignBefore) {
		t.Fatalf("foreign board changed: before=%+v after=%+v", foreignBefore, foreignAfter)
	}
}
