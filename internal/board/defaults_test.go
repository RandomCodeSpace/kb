package board

import (
	"reflect"
	"testing"
)

func TestNewTaskDefaults(t *testing.T) {
	for _, tc := range []struct {
		name string
		task Task
		want []string
	}{
		{name: "bare", task: Task{Title: "x"}, want: []string{"status todo", "priority low", "effort S"}},
		{name: "effort only", task: Task{Title: "x", Status: StatusDoing, Prio: PrioHigh}, want: []string{"effort S"}},
		{name: "explicit", task: Task{Title: "x", Status: StatusTodo, Prio: PrioLow, Effort: "L"}, want: nil},
	} {
		if got := NewTaskDefaults(tc.task); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: NewTaskDefaults = %v, want %v", tc.name, got, tc.want)
		}
	}
}
