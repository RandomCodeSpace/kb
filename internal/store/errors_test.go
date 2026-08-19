package store

import "testing"

func TestConflictErrorMessages(t *testing.T) {
	fields := &TaskFieldsConflictError{Fields: []string{"title", "desc"}}
	if got := fields.Error(); got != "store: task changed in title, desc" {
		t.Fatalf("TaskFieldsConflictError.Error() = %q", got)
	}

	revision := &RevisionConflictError{CurrentRevision: 42}
	if got := revision.Error(); got != "store: board revision conflict (current 42)" {
		t.Fatalf("RevisionConflictError.Error() = %q", got)
	}
}
