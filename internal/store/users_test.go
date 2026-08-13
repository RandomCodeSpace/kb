package store

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
)

func TestUsers(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "kb.db"), []byte("test-secret"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	users, err := s.Users()
	if err != nil || len(users) != 0 {
		t.Fatalf("empty store users = %v, %v; want none", users, err)
	}

	if _, err := s.AddTask("alice", board.Task{Title: "one"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := s.AddTask("alice", board.Task{Title: "two"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := s.AddTask("bob", board.Task{Title: "three"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	// A board that only ever saved a title still counts as an owner.
	if err := s.ReplaceBoard("carol", board.Board{Title: "Carol's board"}); err != nil {
		t.Fatalf("replace: %v", err)
	}

	users, err = s.Users()
	if err != nil {
		t.Fatalf("users: %v", err)
	}
	want := []UserTasks{{User: "alice", Tasks: 2}, {User: "bob", Tasks: 1}, {User: "carol", Tasks: 0}}
	if !reflect.DeepEqual(users, want) {
		t.Fatalf("users = %v, want %v", users, want)
	}
}
