package carddetail

import "testing"

func TestRebuildBodyClearsStateWhenClosed(t *testing.T) {
	m := New(stubReader{found: true}, "u", testStyles())
	m.bodyLines = []string{"stale"}
	m.bodyWidth = 40
	m.scroll = 3

	m.rebuildBody()
	if m.bodyLines != nil || m.bodyWidth != 0 || m.scroll != 0 {
		t.Fatalf("closed rebuild kept state: lines %v width %d scroll %d",
			m.bodyLines, m.bodyWidth, m.scroll)
	}
}
