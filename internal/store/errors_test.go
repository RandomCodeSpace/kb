package store

import "testing"

func TestTaskFieldsConflictErrorMessage(t *testing.T) {
	fields := &TaskFieldsConflictError{Fields: []string{"title", "desc"}}
	if got := fields.Error(); got != "store: task changed in title, desc" {
		t.Fatalf("TaskFieldsConflictError.Error() = %q", got)
	}
}
