package carddetail

import "testing"

func TestRebuildBodyClearsStateWhenClosed(t *testing.T) {
	m := New(stubReader{found: true}, "u", testStyles())
	m.bodyLines = []string{"stale", "rows", "the", "closed", "pane", "must", "drop"}
	m.bodyWidth = 40
	m.body.SetHeight(2)
	m.body.SetContentLines(m.bodyLines)
	m.body.SetYOffset(3)
	if m.scrollOffset() != 3 {
		t.Fatalf("viewport did not take the offset: %d", m.scrollOffset())
	}

	m.rebuildBody()
	if m.bodyLines != nil || m.bodyWidth != 0 || m.scrollOffset() != 0 {
		t.Fatalf("closed rebuild kept state: lines %v width %d scroll %d",
			m.bodyLines, m.bodyWidth, m.scrollOffset())
	}
}
