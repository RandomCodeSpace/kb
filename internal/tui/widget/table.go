package widget

import (
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// Table lays out aligned rows with lipgloss/v2 table. Map #136 component
// sourcing: the settings pane's label/value rows used to be concatenated
// strings whose values started wherever the label happened to end, and the
// charm component that exists for that is a table.
//
// It is the one function in this package that is not a hand-crafted element: it
// is the adapter that lets a kb view feed a charm component and get rows back
// in the shape the overlay widget consumes. The output is deliberately plain
// text - the cell styles carry the column gutter and no color (theme.TableStyles)
// - because the caller paints each row with the token its role names, and a
// pre-colored cell would fight the surface the row is composed onto.
//
// Every row must have the same number of cells. The result is one line per row,
// so a caller keying pointer hit regions to row indices keeps the mapping it
// had. Columns size to their content: the caller cuts its cells to the measure
// it can afford, and the table decides where the columns land. A forced table
// width would spread the slack across every column instead of leaving it on the
// last one, which is the opposite of what a label gutter wants.
func Table(styles *theme.Styles, rows [][]string) []string {
	if len(rows) == 0 {
		return nil
	}
	columns := len(rows[0])
	built := table.New().
		Border(lipgloss.Border{}).
		BorderTop(false).BorderBottom(false).BorderLeft(false).BorderRight(false).
		BorderColumn(false).BorderRow(false).BorderHeader(false).
		Wrap(false).
		StyleFunc(func(_, column int) lipgloss.Style {
			return styles.Table.Column(column, columns)
		}).
		Rows(rows...)
	return strings.Split(built.Render(), "\n")
}
