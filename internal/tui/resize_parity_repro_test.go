package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestResizeDeferredGeometryConvergesRetainedFrameToColdRenderer(t *testing.T) {
	model := performanceModel(37, "", performanceWidth, performanceHeight)
	updated, command := model.Update(tea.WindowSizeMsg{Width: performanceWidth, Height: 31})
	model = updated.(Model)
	if command == nil || model.RenderPlanStats().EstimatedLayoutRecords == 0 {
		t.Fatal("minimized resize did not defer estimated offscreen geometry")
	}
	settlePerformanceCommands(t, &model, []tea.Cmd{command})
	if estimated := model.RenderPlanStats().EstimatedLayoutRecords; estimated != 0 {
		t.Fatalf("resize settled with %d estimated layout records", estimated)
	}

	got := model.View()
	want := model.renderColdView()
	if got.Content != want.Content || got.AltScreen != want.AltScreen || got.MouseMode != want.MouseMode {
		line, retainedLine, coldLine := firstDifferentLine(ansi.Strip(got.Content), ansi.Strip(want.Content))
		t.Fatalf(
			"settled resize/37 retained frame differs from cold renderer: content_bytes=%d cold_bytes=%d first_diff_byte=%d first_plain_diff_line=%d alt=%t/%t mouse=%v/%v\nretained: %q\ncold:     %q",
			len(got.Content), len(want.Content), firstDifferentByte(got.Content, want.Content),
			line, got.AltScreen, want.AltScreen, got.MouseMode, want.MouseMode, retainedLine, coldLine,
		)
	}
}

func firstDifferentByte(left, right string) int {
	limit := min(len(left), len(right))
	for index := range limit {
		if left[index] != right[index] {
			return index
		}
	}
	if len(left) != len(right) {
		return limit
	}
	return -1
}

func firstDifferentLine(left, right string) (int, string, string) {
	leftLines := strings.Split(left, "\n")
	rightLines := strings.Split(right, "\n")
	limit := min(len(leftLines), len(rightLines))
	for index := range limit {
		if leftLines[index] != rightLines[index] {
			return index, leftLines[index], rightLines[index]
		}
	}
	if len(leftLines) != len(rightLines) {
		return limit, lineAt(leftLines, limit), lineAt(rightLines, limit)
	}
	return -1, "", ""
}

func lineAt(lines []string, index int) string {
	if index >= len(lines) {
		return "<missing>"
	}
	return lines[index]
}
