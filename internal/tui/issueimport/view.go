package issueimport

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func (m Model) Overlay(background string, width, height int) string {
	if !m.open {
		return background
	}
	return lipgloss.Place(max(width, 1), max(height, 1), lipgloss.Center, lipgloss.Center, m.View(width, height))
}

func (m Model) View(width, height int) string {
	if !m.open {
		return ""
	}
	paneWidth := min(max(width-4, 24), 88)
	inner := paneWidth - 4
	lines := []string{"Forge issue import"}
	if m.stage == stageInput {
		lines = append(lines,
			focusMark(m.focus == 0)+"source  "+m.sourceName(),
			focusMark(m.focus == 1)+"ref     "+m.ref.View(),
			focusMark(m.focus == 2)+fmt.Sprintf("max     %d", m.max),
		)
		if m.operation == "preview" {
			lines = append(lines, "", "fetching configured forge data and drafting...")
		}
		lines = append(lines, "", "Tab fields  Left/Right change  Enter preview  Esc close")
	} else {
		if m.preview.Truncated {
			lines = append(lines, fmt.Sprintf("fetched %d of about %d; results truncated", m.preview.Fetched, m.preview.TotalHint))
		} else {
			lines = append(lines, fmt.Sprintf("fetched %d", m.preview.Fetched))
		}
		if m.preview.Note != "" {
			lines = append(lines, "note  "+m.preview.Note)
		}
		lines = append(lines, "")
		start, end := rowWindow(len(m.rows), m.selection, max(1, min(height-10, 12)))
		for index := start; index < end; index++ {
			item := m.rows[index]
			cursor := "  "
			if index == m.selection {
				cursor = "> "
			}
			check := "[ ]"
			if item.include {
				check = "[x]"
			}
			state := ""
			switch {
			case item.created:
				state = " [created]"
			case item.draft.Duplicate != nil:
				state = fmt.Sprintf(" [duplicate via %s: %s]", item.draft.Duplicate.Via, item.draft.Duplicate.Title)
			}
			lines = append(lines, cursor+check+" "+item.draft.Title+state)
			if item.err != "" {
				lines = append(lines, "      "+item.err)
			}
		}
		if progress := m.progress(); progress != "" {
			lines = append(lines, "", progress)
		}
		lines = append(lines, "", "Up/Down select  Space toggle  Enter import/retry  Esc back")
	}
	if m.status != "" {
		prefix := "status  "
		if m.statusError {
			prefix = "error   "
		}
		lines = append(lines, prefix+m.status)
	}
	for index := range lines {
		lines[index] = fit(lines[index], inner)
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).Width(paneWidth - 2).Render(strings.Join(lines, "\n"))
}

func focusMark(active bool) string {
	if active {
		return "> "
	}
	return "  "
}

func rowWindow(count, selection, limit int) (int, int) {
	if count <= limit {
		return 0, count
	}
	start := max(0, selection-limit/2)
	start = min(start, count-limit)
	return start, start + limit
}

func fit(value string, width int) string {
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' || r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, value)
	if ansi.StringWidth(value) <= width {
		return value
	}
	if width <= 1 {
		return ansi.Truncate(value, width, "")
	}
	return ansi.Truncate(value, width-1, "") + "…"
}
