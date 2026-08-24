package tui

import (
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/action"
	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
	"github.com/RandomCodeSpace/kb/internal/tui/widget"
)

type helpClosedMsg struct{}

// helpCloseLabel is the footer control that dismisses the pane with a click.
// The dismissals themselves are frozen v1.0.1 behavior: this label, "?", "esc",
// and a click on the backdrop.
const helpCloseLabel = "[Close]"

// helpCloseID keys the footer control across renders so the pressed feedback
// survives a redraw.
const helpCloseID = pointer.ControlID("help:close")

// helpKeys is the bubbles key registry behind the ? overlay. Map #136
// component sourcing: the keys are key.Bindings and the pane is rendered by
// bubbles/help, replacing the hand-laid two-column string table this file used
// to carry. The bindings are the frozen v1.0.1 keymap; a binding for a feature
// the board was built without is disabled rather than omitted, which is how
// bubbles/help expects a self-managing keymap to be written.
type helpKeys struct {
	navigate []key.Binding
	actions  []key.Binding
	dismiss  []key.Binding
}

// ShortHelp is the footer ladder: the dismissals, which are the only keys the
// pane guarantees are reachable however short the frame is.
func (k helpKeys) ShortHelp() []key.Binding { return k.dismiss }

// FullHelp is the pane body, one column per group.
func (k helpKeys) FullHelp() [][]key.Binding { return [][]key.Binding{k.navigate, k.actions} }

// bindings turns one group of the action registry into the bubbles bindings the
// help pane renders. The key, its spelling and its description all come from
// the table; nothing in this file spells one of its own, which is what keeps the
// help pane and the ctrl+k palette from ever describing the same key
// differently.
//
// A row for a feature this board was built without is disabled rather than
// dropped, which is how bubbles/help expects a self-managing keymap to be
// written.
func bindings(group action.Group, features action.Features) []key.Binding {
	entries := action.InGroup(group)
	out := make([]key.Binding, 0, len(entries))
	for _, entry := range entries {
		binding := key.NewBinding(key.WithKeys(entry.Key), key.WithHelp(entry.Hint, entry.Name))
		binding.SetEnabled(entry.Enabled(features))
		out = append(out, binding)
	}
	return out
}

// actionFeatures is what this board was built with, in the form the registry
// gates on.
func (m Model) actionFeatures() action.Features {
	return action.Features{
		Editor:   m.editor.Enabled(),
		Settings: m.settingsNew != nil,
		ADR:      m.adr.Enabled(),
		Issues:   m.issueImport.Enabled(),
	}
}

// helpKeyMap resolves the registry against the features this board was built
// with. Spec section 5.2: the keybinding registry feeds the help pane.
func (m Model) helpKeyMap() helpKeys {
	features := m.actionFeatures()
	return helpKeys{
		navigate: bindings(action.Navigate, features),
		actions:  bindings(action.Act, features),
		dismiss:  bindings(action.Dismiss, features),
	}
}

func (m Model) keyboardHelpOverlay(background string) string {
	return m.keyboardHelpSurface(background).Content
}

// keyboardHelpSurface renders the ? overlay as the elevated panel of spec
// section 4: a bubbles/help body on the panel surface, a header band, and a
// footer band carrying the clickable close control and the dismissal keys.
func (m Model) keyboardHelpSurface(background string) pointer.Surface {
	width := max(m.width, 1)
	height := max(m.height, 1)
	if width < 4 || height < 3 {
		return pointer.Surface{Content: background}
	}
	styles := m.themeStyles()
	inset := styles.Metrics.OverlayInsetX
	paneWidth := max(min(width-4, styles.Metrics.Overlay.Help), 1)
	keys := m.helpKeyMap()

	pane := help.New()
	pane.Styles = styles.Help
	pane.ShowAll = true
	pane.SetWidth(max(paneWidth-2*inset, 1))
	body := helpBodyRows(styles, pane.FullHelpView(keys.FullHelp()), paneWidth)

	opts := widget.OverlayOpts{
		Title:  "Keyboard help",
		Body:   body,
		Footer: m.helpFooter(styles, keys, max(paneWidth-inset, 1)),
		Width:  paneWidth,
		Height: min(len(body)+2, height),
	}
	x := max((width-paneWidth)/2, 0)
	y := max((height-opts.Height)/2, 0)
	layers := append(
		[]*lipgloss.Layer{lipgloss.NewLayer(fitActionFrame(background, width, height))},
		widget.OverlayLayers(styles, opts, x, y)...,
	)
	content := fitActionFrame(lipgloss.NewCompositor(layers...).Render(), width, height)

	var hits pointer.Map
	panel := pointer.Rect{X0: x, Y0: y, X1: x + paneWidth, Y1: y + opts.Height}
	closeAction := func(pointer.Point) tea.Msg { return helpClosedMsg{} }
	hits.AddBackdrop(pointer.Rect{X1: width, Y1: height}, panel, closeAction)
	footerY := y + opts.Height - 1
	hits.AddControl(
		helpCloseID,
		pointer.Rect{
			X0: x + inset, Y0: footerY,
			X1: min(x+paneWidth, x+inset+ansi.StringWidth(helpCloseLabel)), Y1: footerY + 1,
		},
		closeAction,
	)
	return pointer.Surface{Content: content, Pointer: hits.Handler()}
}

// helpBodyRows lays the component's own output onto the panel surface. bubbles
// writes a plain space between its key and description columns and pads unequal
// columns with plain spaces, so each line is re-armed on the surface slot
// before the widget insets it; nothing the component rendered is reformatted.
func helpBodyRows(styles *theme.Styles, rendered string, width int) []string {
	lines := strings.Split(rendered, "\n")
	rows := make([]string, 0, len(lines))
	for _, line := range lines {
		rows = append(rows, widget.OverlayRow(styles, styles.SurfaceRun(theme.OverlaySurf, line), width))
	}
	return rows
}

// helpFooter is the close control followed by the dismissal ladder. Spec
// section 5.2 scopes bubbles/help to the overlay body, so the band composes the
// same registry's short help itself rather than pulling the component's own
// surface token into a band row.
//
// Spec section 10.4.6 names this pane's own hand-rolled ladder as the shape the
// packer generalizes: the control is the pinned head, because clicking it is a
// frozen v1.0.1 dismissal and it has to stay reachable on a frame too narrow to
// spell the keys out, and the dismissal keys are the droppable middle. The
// packer now owns the arithmetic, so a narrow band drops whole rungs with their
// separators rather than cutting one mid-word.
func (m Model) helpFooter(styles *theme.Styles, keys helpKeys, width int) string {
	hints := make([]string, 0, len(keys.ShortHelp()))
	for _, dismissal := range keys.ShortHelp() {
		hints = append(hints, dismissal.Help().Key+" "+dismissal.Help().Desc)
	}
	line, _ := widget.Hints(styles, widget.Ladder{
		Head:   []string{helpCloseLabel},
		Middle: hints,
	}, width)
	// The control is styled after packing: a rendered run carries its own SGR
	// sequences and is not a safe key for the packer's width arithmetic.
	return strings.Replace(line, helpCloseLabel,
		m.pointerState.Render(styles, helpCloseID, helpCloseLabel), 1)
}
