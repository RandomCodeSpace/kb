package store

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
)

func BenchmarkStoreOpenIntegrity(b *testing.B) {
	for _, count := range []int{0, 1000, 10000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			path := filepath.Join(b.TempDir(), "kb.db")
			secret := []byte("integrity-benchmark-secret")
			s, err := Open(path, secret)
			if err != nil {
				b.Fatal(err)
			}
			tasks := make([]board.Task, count)
			for i := range tasks {
				tasks[i] = board.Task{Title: fmt.Sprintf("Task %d", i), Status: board.StatusTodo, Prio: board.PrioLow, Desc: strings.Repeat("local board text ", 16), Tags: []string{"project::bench"}}
			}
			if err := s.ReplaceBoard("default", board.Board{Tasks: tasks}); err != nil {
				b.Fatal(err)
			}
			if err := s.Close(); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for b.Loop() {
				s, err := Open(path, secret)
				if err != nil {
					b.Fatal(err)
				}
				if err := s.Close(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
