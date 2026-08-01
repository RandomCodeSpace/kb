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
	keys := []string{"shared-key", "key') OR 1=1 --", "missing"}
	own := []ImportLink{
		{ExternalKey: keys[0], Link: "link::own-shared", Title: "own shared"},
		{ExternalKey: keys[1], Link: "link::own-shaped", Title: "own shaped"},
	}
	foreign := []ImportLink{
		{ExternalKey: keys[0], Link: "link::foreign-shared", Title: "foreign shared"},
		{ExternalKey: keys[1], Link: "link::foreign-shaped", Title: "foreign shaped"},
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
	want := map[string]ImportLink{keys[0]: own[0], keys[1]: own[1]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ImportedAs = %+v, want only scoped requested keys %+v", got, want)
	}
}

func TestReplaceBoardBindsInjectionShapedScopeWhenLoadingExistingTasks(t *testing.T) {
	s := newStore(t)
	scope := "alice' OR 1=1 --"
	seed := board.Board{Title: "Scoped", Tasks: []board.Task{{Title: "same title", Status: board.StatusTodo, Prio: 3}}}
	if err := s.ReplaceBoard(scope, seed); err != nil {
		t.Fatalf("ReplaceBoard(injection-shaped scope): %v", err)
	}
	if err := s.ReplaceBoard("victim", seed); err != nil {
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

	updated := board.Board{Title: "Scoped", Tasks: []board.Task{{Title: "same title", Desc: "updated", Status: board.StatusTodo, Prio: 3}}}
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
	if len(before.Tasks) != 1 || len(after.Tasks) != 1 || after.Tasks[0].ID != before.Tasks[0].ID {
		t.Fatalf("scoped identity changed: before=%+v after=%+v", before.Tasks, after.Tasks)
	}
	if !reflect.DeepEqual(foreignAfter, foreignBefore) {
		t.Fatalf("foreign board changed: before=%+v after=%+v", foreignBefore, foreignAfter)
	}
}
