package board

import "testing"

func TestParseIgnoresContentBeforeFirstTaskInAColumn(t *testing.T) {
	b := Parse("# Board\n\n## To Do\n\norphan prose\n  - [ ] orphan check\n- [ ] actual task\n")
	if len(b.Tasks) != 1 || b.Tasks[0].Title != "actual task" {
		t.Fatalf("Parse created tasks from orphan content: %+v", b.Tasks)
	}
}
