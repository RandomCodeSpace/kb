// Package carddetail renders the full-card overlay and its direct-store
// comment and blocker-link actions.
package carddetail

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/forge"
	"github.com/RandomCodeSpace/kb/internal/store"
	"github.com/RandomCodeSpace/kb/internal/tui/formview"
	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
	"github.com/RandomCodeSpace/kb/internal/tui/widget"
)

const (
	defaultWidth  = 80
	defaultHeight = 24
	// minPaneWidth is the floor the pane keeps on a frame too narrow for the
	// spec section 4 geometry, carried over from v1.0.1.
	minPaneWidth = 12
)

// Reader is the store projection needed to enrich a board task for display.
type Reader interface {
	Comments(user, taskRef string) ([]store.Comment, error)
	TaskLinks(user, taskID string) (store.TaskLinks, error)
	Tombstone(user, taskID string) (store.Tombstone, bool, error)
}

type detailLoadedMsg struct {
	taskID       string
	generation   uint64
	comments     []store.Comment
	links        store.TaskLinks
	tombstone    *store.Tombstone
	commentsErr  error
	linksErr     error
	tombstoneErr error
}

type markdownRenderer func(source string, width int) string

type mouseScrollMsg struct{ delta int }
type mouseDismissMsg struct{}

type pointerControlMsg struct {
	pointerSession  uint64
	actionSession   uint64
	driftSession    uint64
	driftGeneration uint64
	message         tea.Msg
}

// Model owns the overlay's task snapshot, enriched detail, and the bubbles
// viewport that carries its scroll state.
type Model struct {
	reader         Reader
	writer         Writer
	user           string
	task           board.Task
	comments       []store.Comment
	links          store.TaskLinks
	tombstone      *store.Tombstone
	open           bool
	loading        bool
	reloadPending  bool
	commentsErr    error
	linksErr       error
	tombstoneErr   error
	body           viewport.Model
	generation     uint64
	width          int
	height         int
	styles         *theme.Styles
	renderMarkdown markdownRenderer
	bodyLines      []string
	bodyWidth      int
	pointerSession uint64
	pointerState   pointer.State

	action        actionMode
	actionSession uint64
	commentInput  textarea.Model
	linkInput     textinput.Model
	mark          formview.Mark
	currentBlocks bool
	selection     int
	confirm       bool
	saving        bool
	changed       bool
	statusMessage string
	statusIsError bool

	driftBackend    DriftBackend
	driftContext    context.Context
	driftMode       driftMode
	driftSession    uint64
	driftGeneration uint64
	driftBusy       string
	driftCancel     context.CancelFunc
	driftChoices    []store.ImportLink
	driftSelection  int
	driftResult     forge.Drift
}

// New creates a closed detail pane. A nil reader still shows board-resident
// task fields; enrichment is simply unavailable to lightweight model tests.
//
// The styles are the design system of spec section 6: the pane never builds a
// lipgloss style, it composes the cached ones, and it hands the palette-derived
// markdown config to glamour through the injectable renderer.
func New(reader Reader, user string, styles *theme.Styles) Model {
	writer, _ := reader.(Writer)
	return Model{
		reader: reader, writer: writer, user: user, width: defaultWidth, height: defaultHeight,
		styles: styles, renderMarkdown: markdownWith(styles), body: newBodyViewport(),
	}
}

// newBodyViewport is the bubbles viewport that owns the body scroll offset.
// Map #136 component sourcing: the hand-rolled offset and clamp are gone, the
// charm component keeps the offset instead. It is driven programmatically
// rather than through viewport.Update, because the frozen v1.0.1 keymap and
// wheel deltas are the pane's, not the component's defaults: soft wrap and
// horizontal scrolling stay off so the panel-width rows the widget renders are
// never re-cut, and the wheel is routed through the pointer map that owns the
// pane's hit regions.
func newBodyViewport() viewport.Model {
	built := viewport.New()
	built.SoftWrap = false
	built.FillHeight = false
	built.MouseWheelEnabled = false
	built.SetHorizontalStep(0)
	return built
}

// SetStyles adopts a rebuilt design system. Spec section 6.3: the root resolves
// the palette again when tea.BackgroundColorMsg answers, and every pane it owns
// has to follow it or the frame renders two palettes at once.
func (m *Model) SetStyles(styles *theme.Styles) {
	if styles == nil {
		return
	}
	m.styles = styles
	m.renderMarkdown = markdownWith(styles)
}

// IsOpen reports whether the overlay currently owns input and rendering.
func (m Model) IsOpen() bool { return m.open }

// TaskID returns the displayed task's durable ID, or empty while closed.
func (m Model) TaskID() string {
	if !m.open {
		return ""
	}
	return m.task.ID
}

// Open resets the pane to task and returns the asynchronous enrichment load.
func (m *Model) Open(task board.Task) tea.Cmd {
	m.cancelDrift()
	m.pointerSession++
	m.pointerState = pointer.State{}
	m.actionSession++
	m.task = task
	m.comments = nil
	m.links = store.TaskLinks{}
	m.tombstone = nil
	m.open = true
	m.loading = false
	m.reloadPending = false
	m.commentsErr = nil
	m.linksErr = nil
	m.tombstoneErr = nil
	m.body.SetYOffset(0)
	m.action = actionNone
	m.selection = 0
	m.confirm = false
	m.saving = false
	m.changed = false
	m.statusMessage = ""
	m.statusIsError = false
	if m.reader == nil {
		m.rebuildBody()
		return nil
	}
	return m.startLoad()
}

// Refresh replaces the board-resident snapshot and serializes enrichment IO.
// Repeated refreshes during a load collapse into one successor for the latest
// task snapshot.
func (m *Model) Refresh(task board.Task) tea.Cmd {
	if !m.open || task.ID != m.task.ID {
		return nil
	}
	m.task = task
	if m.reader == nil {
		m.rebuildBody()
		return nil
	}
	if m.loading {
		m.reloadPending = true
		m.rebuildBody()
		return nil
	}
	return m.startLoad()
}

// Close dismisses the pane and invalidates any in-flight result by clearing
// the current task identity.
func (m *Model) Close() {
	m.cancelDrift()
	m.pointerSession++
	m.pointerState = pointer.State{}
	m.generation++
	m.actionSession++
	m.open = false
	m.loading = false
	m.reloadPending = false
	m.task = board.Task{}
	m.bodyLines = nil
	m.bodyWidth = 0
	m.syncScroll()
	m.action = actionNone
	m.selection = 0
	m.confirm = false
	m.saving = false
	m.changed = false
	m.statusMessage = ""
	m.statusIsError = false
}

// Resize updates the viewport used to bound persistent scroll state.
func (m *Model) Resize(width, height int) {
	m.width = max(width, 1)
	m.height = max(height, 1)
	paneWidth, _, _ := m.paneSize(m.width, m.height)
	if m.open && (m.bodyLines == nil || m.bodyWidth != paneWidth) {
		m.rebuildBody()
		return
	}
	m.syncScroll()
}

// Update handles enrichment results and overlay scrolling.
func (m *Model) Update(message tea.Msg) tea.Cmd {
	if !m.open {
		return nil
	}
	if pointer.IsMessage(message) {
		next, command, _ := m.pointerState.Update(message)
		m.pointerState = next
		m.rebuildBody()
		return command
	}
	switch msg := message.(type) {
	case actionChoicePointerMsg:
		m.updateActionChoice(msg.index)
		return nil
	case driftChoicePointerMsg, driftChoicesLoadedMsg, driftCheckedMsg, driftAcceptedMsg:
		return m.updateDrift(msg)
	case mutationCompletedMsg:
		return m.finishMutation(msg)
	case detailLoadedMsg:
		if msg.taskID != m.task.ID || msg.generation != m.generation {
			return nil
		}
		if m.reloadPending {
			m.reloadPending = false
			return m.startLoad()
		}
		m.loading = false
		m.comments = msg.comments
		m.links = msg.links
		m.tombstone = msg.tombstone
		m.commentsErr = msg.commentsErr
		m.linksErr = msg.linksErr
		m.tombstoneErr = msg.tombstoneErr
		m.reconcileDeleteActionAfterRefresh()
		m.rebuildBody()
	case mouseScrollMsg:
		m.scrollBy(msg.delta)
	case mouseDismissMsg:
		m.Close()
		return nil
	case tea.KeyPressMsg:
		if m.driftMode != driftNone {
			return m.updateDrift(msg)
		}
		if m.action != actionNone {
			return m.updateActionKey(msg)
		}
		if m.statusMessage != "" {
			m.statusMessage = ""
			m.statusIsError = false
			m.rebuildBody()
		}
		switch msg.String() {
		case "v":
			return m.beginDrift()
		case "c":
			return m.beginAction(actionAddComment)
		case "d":
			return m.beginAction(actionDeleteComment)
		case "b":
			return m.beginAction(actionAddLink)
		case "u":
			return m.beginAction(actionDeleteLink)
		}
		switch msg.String() {
		case "up", "k", "pgup":
			m.scrollBy(-scrollAmount(msg.String()))
		case "down", "j", "pgdown":
			m.scrollBy(scrollAmount(msg.String()))
		case "home", "g":
			m.body.GotoTop()
		}
	}
	m.syncScroll()
	return nil
}

// ResolvePointerMessage unwraps a message only while the exact rendered detail
// session and nested action state are still current. A release queued from an
// older pane cannot mutate a reopened task or a later confirmation.
func (m *Model) ResolvePointerMessage(message tea.Msg) (tea.Msg, bool) {
	msg, ok := message.(pointerControlMsg)
	if !ok {
		return message, false
	}
	if !m.open || msg.pointerSession != m.pointerSession ||
		msg.actionSession != m.actionSession || msg.driftSession != m.driftSession ||
		msg.driftGeneration != m.driftGeneration {
		return nil, true
	}
	return msg.message, true
}

// MouseHandler routes wheel scrolling within the pane and left-click dismissal
// outside it. The root disables this handler while nested detail input is active.
func (m Model) MouseHandler(width, height int) func(tea.MouseMsg) tea.Cmd {
	if !m.open {
		return nil
	}
	paneWidth, paneHeight, _ := m.paneSize(width, height)
	x0 := max((max(width, 1)-paneWidth)/2, 0)
	y0 := max((max(height, 1)-paneHeight)/2, 0)
	return func(message tea.MouseMsg) tea.Cmd {
		mouse := message.Mouse()
		inside := mouse.X >= x0 && mouse.X < x0+paneWidth && mouse.Y >= y0 && mouse.Y < y0+paneHeight
		if _, wheel := message.(tea.MouseWheelMsg); wheel {
			if !inside {
				return nil
			}
			delta := 0
			switch mouse.Button {
			case tea.MouseWheelUp:
				delta = -3
			case tea.MouseWheelDown:
				delta = 3
			default:
				return nil
			}
			return func() tea.Msg { return mouseScrollMsg{delta: delta} }
		}
		if _, click := message.(tea.MouseClickMsg); click && mouse.Button == tea.MouseLeft && !inside {
			return func() tea.Msg { return mouseDismissMsg{} }
		}
		return nil
	}
}

func (m *Model) reconcileDeleteActionAfterRefresh() {
	count, noun := 0, ""
	switch m.action {
	case actionDeleteComment:
		count, noun = len(m.comments), "comments"
		if m.commentsErr != nil {
			m.cancelDeleteActionAfterRefresh("comments unavailable; deletion cancelled", true)
			return
		}
	case actionDeleteLink:
		count, noun = len(m.linkChoices()), "blocker links"
		if m.linksErr != nil {
			m.cancelDeleteActionAfterRefresh("blocker links unavailable; deletion cancelled", true)
			return
		}
	default:
		return
	}
	if count == 0 {
		m.cancelDeleteActionAfterRefresh(noun+" changed; none remain to remove", false)
		return
	}
	m.selection = min(max(m.selection, 0), count-1)
	if m.confirm {
		m.confirm = false
		m.setStatus(noun+" changed; review the selection and confirm again", false)
	}
}

func (m *Model) cancelDeleteActionAfterRefresh(status string, isError bool) {
	m.action = actionNone
	m.selection = 0
	m.confirm = false
	m.setStatus(status, isError)
}

func scrollAmount(key string) int {
	if key == "pgup" || key == "pgdown" {
		return 8
	}
	return 1
}

func (m Model) load(taskID string, generation uint64) tea.Cmd {
	return func() tea.Msg {
		comments, commentsErr := m.reader.Comments(m.user, taskID)
		links, linksErr := m.reader.TaskLinks(m.user, taskID)
		tombstone, found, tombstoneErr := m.reader.Tombstone(m.user, taskID)
		var killed *store.Tombstone
		if found {
			killed = &tombstone
		}
		return detailLoadedMsg{
			taskID: taskID, generation: generation, comments: comments, links: links, tombstone: killed,
			commentsErr: commentsErr, linksErr: linksErr, tombstoneErr: tombstoneErr,
		}
	}
}

func (m *Model) startLoad() tea.Cmd {
	m.generation++
	m.loading = true
	m.commentsErr = nil
	m.linksErr = nil
	m.tombstoneErr = nil
	m.rebuildBody()
	return m.load(m.task.ID, m.generation)
}

// View renders the centered overlay panel sized for the current terminal. The
// panel carries no shadow here: a shadow needs something to fall on, and this
// path has no board behind it.
func (m *Model) View(width, height int) string {
	if !m.open {
		return ""
	}
	width = max(width, 1)
	height = max(height, 1)
	layout := m.layout(width, height)
	panel := fitTerminal(widget.Overlay(m.styles, layout.opts), width, height)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}

// Overlay composes the pane over the board without making carddetail own the
// board renderer. Input routing remains the root model's responsibility.
func (m *Model) Overlay(background string, width, height int) string {
	return m.PointerSurface(background, width, height).Content
}

// PointerSurface renders the detail overlay and returns the immutable pointer
// map produced from this exact layout pass. The root selects it as the active
// top-most surface, so carddetail never exposes board hit regions while open.
func (m *Model) PointerSurface(background string, width, height int) pointer.Surface {
	if !m.open {
		return pointer.Surface{Content: background}
	}
	width = max(width, 1)
	height = max(height, 1)
	background = fitTerminal(background, width, height)
	layout := m.layout(width, height)
	paneWidth, paneHeight := layout.opts.Width, layout.opts.Height
	x := max((width-paneWidth)/2, 0)
	y := max((height-paneHeight)/2, 0)
	layers := []*lipgloss.Layer{lipgloss.NewLayer(background)}
	if layout.elevated {
		layers = append(layers, widget.OverlayLayers(m.styles, layout.opts, x, y)...)
	} else {
		layers = append(layers, lipgloss.NewLayer(widget.Overlay(m.styles, layout.opts)).X(x).Y(y).Z(1))
	}
	content := fitTerminal(lipgloss.NewCompositor(layers...).Render(), width, height)

	bounds := pointer.Rect{X0: 0, Y0: 0, X1: width, Y1: height}
	pane := pointer.Rect{X0: x, Y0: y, X1: x + paneWidth, Y1: y + paneHeight}
	var hitMap pointer.Map
	wrap := func(message tea.Msg) tea.Msg {
		return pointerControlMsg{
			pointerSession: m.pointerSession, actionSession: m.actionSession,
			driftSession: m.driftSession, driftGeneration: m.driftGeneration,
			message: message,
		}
	}
	hitMap.AddWheel(pane, func(delta int) tea.Msg { return wrap(mouseScrollMsg{delta: delta * 3}) })
	if m.action == actionNone && m.driftMode == driftNone {
		hitMap.AddBackdrop(bounds, pane, func(pointer.Point) tea.Msg { return wrap(mouseDismissMsg{}) })
	}

	inset := m.styles.Metrics.OverlayInsetX
	displayWidth := layout.contentWidth
	footerY := y + paneHeight - 1
	// The action row is pinned directly above the footer band, so the buttons
	// land on the row the scroll window stops short of.
	actionY := y + 1 + layout.bodyRows
	bodyBottom := actionY
	if len(layout.controls) == 0 {
		bodyBottom = footerY
	}
	xCursor := x + inset
	for _, control := range layout.controls {
		buttonWidth := detailButtonWidth(m.styles, control)
		if xCursor+buttonWidth > x+paneWidth-inset || actionY < y+1 || actionY >= footerY {
			break
		}
		rect := pointer.Rect{X0: xCursor, Y0: actionY, X1: xCursor + buttonWidth, Y1: actionY + 1}
		message := wrap(control.message)
		hitMap.AddControl(detailFooterControlID(control), rect, func(pointer.Point) tea.Msg { return message })
		xCursor += buttonWidth + m.styles.Metrics.ButtonGap
	}
	if m.driftMode == driftSelect && m.driftBusy == "" {
		viewport := pointer.Viewport{
			Rect:   pointer.Rect{X0: x + inset, Y0: y + 1, X1: x + paneWidth - inset, Y1: bodyBottom},
			Scroll: m.scrollOffset(),
		}
		for index := range m.driftChoices {
			if rect, ok := viewport.Row(4+index, 0, displayWidth); ok {
				message := wrap(driftChoicePointerMsg{index: index})
				hitMap.AddControl(detailDriftControlID(index), rect, func(pointer.Point) tea.Msg { return message })
			}
		}
	}
	if (m.action == actionDeleteComment || m.action == actionDeleteLink) && !m.saving {
		count := len(m.comments)
		if m.action == actionDeleteLink {
			count = len(m.linkChoices())
		}
		start, end := selectionWindow(count, m.selection, max(m.height-10, 3))
		logicalRow := 2
		if start > 0 {
			logicalRow++
		}
		viewport := pointer.Viewport{
			Rect:   pointer.Rect{X0: x + inset, Y0: y + 1, X1: x + paneWidth - inset, Y1: bodyBottom},
			Scroll: m.scrollOffset(),
		}
		for index := start; index < end; index++ {
			if rect, ok := viewport.Row(logicalRow+index-start, 0, displayWidth); ok {
				message := wrap(actionChoicePointerMsg{index: index})
				hitMap.AddControl(detailActionChoiceControlID(m.action, index), rect, func(pointer.Point) tea.Msg { return message })
			}
		}
	}
	return pointer.Surface{Content: content, Pointer: hitMap.Handler()}
}

// paneLayout is one resolved render pass: the panel the widget draws, whether
// it is elevated, and the two widths the footer and its controls share so a
// clickable label always lands where it was rendered.
type paneLayout struct {
	opts         widget.OverlayOpts
	elevated     bool
	contentWidth int
	footerWidth  int
	controls     []detailPointerControl
	bodyRows     int
}

func (m *Model) layout(width, height int) paneLayout {
	width = max(width, 1)
	height = max(height, 1)
	m.ensureBody(width, height)
	paneWidth, paneHeight, elevated := m.paneSize(width, height)
	contentWidth := m.contentWidth(paneWidth)

	controls := m.pointerFooterControls(m.actionRowWidth(paneWidth))
	bodyRows := m.bodyRowCount(paneWidth, paneHeight)
	maxScroll := max(0, len(m.bodyLines)-bodyRows)
	// The viewport owns the offset and has already clamped it against this
	// geometry; the widget still owns the rows, because a panel body row carries
	// the OverlaySurf token edge to edge and viewport.View would pad it plain.
	start := min(m.scrollOffset(), maxScroll)
	end := min(start+bodyRows, len(m.bodyLines))
	body := append([]string(nil), m.bodyLines[start:end]...)
	if len(controls) > 0 {
		for len(body) < bodyRows {
			body = append(body, "")
		}
		body = append(body, widget.OverlayRow(m.styles, m.actionButtonRow(controls), paneWidth))
	}
	hint := ""
	// A band insets its content from the left and right-aligns its tail at its
	// own edge, so the footer hints have one inset less than a body row.
	footerWidth := max(paneWidth-m.styles.Metrics.OverlayInsetX, 1)
	if maxScroll > 0 {
		hint = widget.ScrollHint(m.styles, start+1, maxScroll+1, theme.OverlayBand)
		footerWidth = max(footerWidth-ansi.StringWidth(hint)-1, 1)
	}
	return paneLayout{
		opts: widget.OverlayOpts{
			Title:  m.headerTitle(),
			Seq:    m.headerSeq(),
			Body:   body,
			Footer: m.actionFooter(footerWidth),
			Hint:   hint,
			Width:  paneWidth,
			Height: paneHeight,
		},
		elevated:     elevated,
		contentWidth: contentWidth,
		footerWidth:  footerWidth,
		controls:     controls,
		bodyRows:     bodyRows,
	}
}

// actionRowWidth is the width the pinned action row has between the overlay
// insets, which is the budget its responsive button ladder trims against.
func (m Model) actionRowWidth(paneWidth int) int {
	return max(paneWidth-2*m.styles.Metrics.OverlayInsetX, 1)
}

// bodyRowCount is the scrollable body height. The pinned action row spends one
// of the panel's body rows whenever the pane's state offers any action, so the
// scroll window and the pointer viewports resolve it from one place.
func (m Model) bodyRowCount(paneWidth, paneHeight int) int {
	rows := max(paneHeight-2, 0)
	if len(m.pointerFooterControls(m.actionRowWidth(paneWidth))) > 0 {
		rows = max(rows-1, 0)
	}
	return rows
}

// headerTitle is the header band's bold title: the emoji and the card title.
func (m Model) headerTitle() string {
	title := strings.TrimSpace(safeText(m.task.Title, false))
	if m.task.Emoji != "" {
		title = safeText(m.task.Emoji, false) + " " + title
	}
	return title
}

// headerSeq is the right-aligned reference of the header band.
func (m Model) headerSeq() string {
	if m.task.Seq <= 0 {
		return ""
	}
	return fmt.Sprintf("#%d", m.task.Seq)
}

func fitDetailLine(line string, width int) string {
	width = max(width, 0)
	if ansi.StringWidth(line) <= width {
		return line
	}
	if width <= 1 {
		return ansi.Cut("…", 0, width)
	}
	return ansi.Cut(line, 0, width-1) + "…"
}

// paneSize is the overlay geometry of spec section 4: the panel spans a share
// of the frame on both axes and is centered. A frame too small for the section
// 4 minimums does not get an elevated panel: the overlay keeps the v1.0.1
// full-frame pane height, whose backdrop margin the frozen dismissal behavior
// depends on, and casts no shadow.
func (m Model) paneSize(width, height int) (paneWidth, paneHeight int, elevated bool) {
	width = max(width, 1)
	height = max(height, 1)
	metrics := m.styles.Metrics
	paneWidth, paneHeight = metrics.OverlayPane(width, height)
	paneWidth = max(paneWidth, min(width, minPaneWidth))
	if metrics.OverlayElevated(paneWidth, paneHeight) {
		return paneWidth, paneHeight, true
	}
	return paneWidth, min(max(min(height-2, height), 5), height), false
}

// contentWidth is the width a body row has between the overlay insets, held to
// the readable measure of spec section 4 so a wide panel does not become a
// single very long line of prose.
func (m Model) contentWidth(paneWidth int) int {
	return m.styles.Metrics.OverlayContent(paneWidth)
}

// syncScroll hands the current body and geometry to the viewport, which is what
// clamps the offset: SetContentLines pulls an over-scrolled pane back to the
// bottom, so the pane never has to do that arithmetic itself.
func (m *Model) syncScroll() {
	m.body.SoftWrap = false
	m.body.FillHeight = false
	m.body.MouseWheelEnabled = false
	m.body.SetHorizontalStep(0)
	if !m.open {
		m.body.SetContentLines(nil)
		m.body.SetYOffset(0)
		return
	}
	paneWidth, paneHeight, _ := m.paneSize(m.width, m.height)
	m.body.SetWidth(paneWidth)
	m.body.SetHeight(m.bodyRowCount(paneWidth, paneHeight))
	m.body.SetContentLines(m.bodyLines)
	m.body.SetYOffset(m.body.YOffset())
}

// scrollOffset is the first body line the pane shows. The pointer viewport and
// the body window both read it here rather than from a field.
func (m *Model) scrollOffset() int {
	return m.body.YOffset()
}

// scrollBy moves the offset by whole lines. Frozen v1.0.1 deltas: one line for
// the arrow and vim keys, eight for a page key, three per wheel notch.
func (m *Model) scrollBy(delta int) {
	if delta < 0 {
		m.body.ScrollUp(-delta)
		return
	}
	m.body.ScrollDown(delta)
}

func (m Model) maxScroll() int {
	paneWidth, paneHeight, _ := m.paneSize(m.width, m.height)
	return max(0, len(m.bodyLines)-m.bodyRowCount(paneWidth, paneHeight))
}

func (m *Model) ensureBody(width, height int) {
	m.width = max(width, 1)
	m.height = max(height, 1)
	paneWidth, _, _ := m.paneSize(m.width, m.height)
	if m.bodyLines == nil || m.bodyWidth != paneWidth {
		m.rebuildBody()
		return
	}
	m.syncScroll()
}

func (m *Model) rebuildBody() {
	if !m.open {
		m.bodyLines = nil
		m.bodyWidth = 0
		m.body.SetYOffset(0)
		return
	}
	paneWidth, _, _ := m.paneSize(m.width, m.height)
	body := m.renderBody(paneWidth)
	m.bodyLines = strings.Split(strings.TrimRight(body, "\n"), "\n")
	m.bodyWidth = paneWidth
	m.syncScroll()
}

func fitTerminal(rendered string, width, height int) string {
	lines := strings.Split(rendered, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "")
	}
	return strings.Join(lines, "\n")
}

// renderBody renders the scrollable body as panel-width rows. Spec section 4:
// the section breaks are OverlayBand rows, the key/value lines are field rows,
// and every other row carries the panel surface to both edges.
func (m Model) renderBody(width int) string {
	if m.driftMode != driftNone {
		return strings.Join(m.paneRows(m.driftBody(m.contentWidth(width)), width), "\n")
	}
	if m.action != actionNone {
		return strings.Join(m.paneRows(m.actionBody(m.contentWidth(width)), width), "\n")
	}
	return strings.Join(m.detailRows(width), "\n")
}

// paneRows turns an action or drift pane into panel rows: its first line is its
// heading and becomes the section break, the rest are body rows.
func (m Model) paneRows(body string, width int) []string {
	heading, rest, _ := strings.Cut(body, "\n")
	rows := []string{widget.Section(m.styles, heading, "", width)}
	for _, line := range strings.Split(rest, "\n") {
		rows = append(rows, m.row(line, width))
	}
	return rows
}

// row renders one body row of plain text on the panel surface.
func (m Model) row(text string, width int) string {
	return widget.OverlayRow(m.styles, m.styles.Overlay.Surf.Render(text), width)
}

// blank is one empty body row: a section separator that is not a break.
func (m Model) blank(width int) string { return m.row("", width) }

func (m Model) detailRows(width int) []string {
	content := m.contentWidth(width)
	rows := []string{widget.Section(m.styles, "DETAIL", "", width)}
	rows = append(rows, m.fieldRows(width)...)
	if strings.TrimSpace(m.task.Desc) != "" {
		rows = append(rows, m.blank(width))
		rows = append(rows, m.markdownRows(m.task.Desc, content, width)...)
	}
	if len(m.task.Checks) > 0 {
		rows = append(rows, m.checklistRows(width)...)
	}
	rows = append(rows, m.contextRows(width)...)
	rows = append(rows, m.commentRows(content, width)...)
	if m.statusMessage != "" {
		prefix := "status: "
		if m.statusIsError {
			prefix = "error: "
		}
		rows = append(rows, m.blank(width), m.row(prefix+m.statusMessage, width))
	}
	return rows
}

// fieldRows are the card's own attributes as the key/value rows of spec
// section 4: label in FgMuted, fixed gutter, value in FgBase.
func (m Model) fieldRows(width int) []string {
	status := string(m.task.Status)
	if m.task.Blocked {
		status += "  blocked"
	}
	rows := []string{
		widget.Field(m.styles, "status", status, width),
		widget.Field(m.styles, "priority", strconv.Itoa(m.task.Prio), width),
	}
	if m.task.Due != "" {
		rows = append(rows, widget.Field(m.styles, "due", safeText(m.task.Due, false), width))
	}
	if m.task.Effort != "" {
		rows = append(rows, widget.Field(m.styles, "effort", safeText(m.task.Effort, false), width))
	}
	if tags := regularTags(m.task.Tags); len(tags) > 0 {
		rows = append(rows, widget.Field(m.styles, "labels", strings.Join(tags, "  "), width))
	}
	if links := importLinks(m.task.Tags); len(links) > 0 {
		rows = append(rows, widget.Field(m.styles, "links", strings.Join(links, "  "), width))
	}
	if m.tombstone != nil {
		for _, line := range strings.Split(killedContext(*m.tombstone), "\n") {
			rows = append(rows, m.row(line, width))
		}
	}
	if m.tombstoneErr != nil {
		rows = append(rows, m.row("killed context error: "+safeText(m.tombstoneErr.Error(), false), width))
	}
	return rows
}

// markdownRows wraps glamour's output into body rows. The renderer is fed the
// palette-derived config of spec section 5.2 through the injectable field.
func (m Model) markdownRows(source string, content, width int) []string {
	rendered := strings.Split(m.markdown(source, content), "\n")
	rows := make([]string, 0, len(rendered))
	for _, line := range rendered {
		rows = append(rows, widget.OverlayRow(m.styles, line, width))
	}
	return rows
}

func (m Model) checklistRows(width int) []string {
	done := 0
	for _, check := range m.task.Checks {
		if check.Done {
			done++
		}
	}
	rows := []string{widget.Section(m.styles, "CHECKLIST",
		fmt.Sprintf("%d/%d", done, len(m.task.Checks)), width)}
	for _, check := range m.task.Checks {
		state := widget.CheckOpen
		if check.Done {
			state = widget.CheckDone
		}
		mark := widget.Check(m.styles, safeText(check.Text, false), state, theme.OverlaySurf, false)
		rows = append(rows, widget.OverlayRow(m.styles, m.styles.Overlay.Surf.Render("  ")+mark, width))
	}
	return rows
}

// contextRows are the blocker links and the completion gate, the two rows that
// answer whether this card can move.
func (m Model) contextRows(width int) []string {
	var rows []string
	if len(m.links.Blocks) > 0 {
		rows = append(rows, widget.Field(m.styles, "blocks", taskChips(m.links.Blocks), width))
	}
	if len(m.links.BlockedBy) > 0 {
		rows = append(rows, widget.Field(m.styles, "blocked by", taskChips(m.links.BlockedBy), width))
	}
	gate, state := renderCompletionGate(m.task, m.links, m.loading, m.linksErr)
	rows = append(rows, widget.OverlayRow(m.styles, m.styles.On(state, theme.OverlaySurf).Render(gate), width))
	if m.linksErr != nil {
		rows = append(rows, m.row("blocker links error: "+safeText(m.linksErr.Error(), false), width))
	}
	return rows
}

func (m Model) commentRows(content, width int) []string {
	if m.loading {
		return []string{
			widget.Section(m.styles, "COMMENTS", "", width),
			m.row("loading comments and context...", width),
		}
	}
	if m.commentsErr != nil {
		return []string{
			widget.Section(m.styles, "COMMENTS", "", width),
			m.row("comments error: "+safeText(m.commentsErr.Error(), false), width),
		}
	}
	count := "none"
	if len(m.comments) > 0 {
		count = strconv.Itoa(len(m.comments))
	}
	rows := []string{widget.Section(m.styles, "COMMENTS", count, width)}
	for _, comment := range m.comments {
		date := comment.CreatedAt.UTC().Format("2 Jan 2006")
		rows = append(rows, m.row(fmt.Sprintf("c%d  %s  %s",
			comment.ID, safeText(comment.Author, false), date), width))
		rows = append(rows, m.markdownRows(comment.Body, content, width)...)
		rows = append(rows, m.blank(width))
	}
	return rows
}

func (m Model) markdown(source string, width int) string {
	if m.renderMarkdown == nil {
		return markdownWith(m.styles)(source, width)
	}
	return m.renderMarkdown(source, width)
}

func regularTags(tags []string) []string {
	var out []string
	for _, tag := range tags {
		if !strings.HasPrefix(tag, "link::") {
			out = append(out, "["+safeText(tag, false)+"]")
		}
	}
	return out
}

func importLinks(tags []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, tag := range tags {
		if !strings.HasPrefix(tag, "link::") {
			continue
		}
		link := strings.TrimSpace(strings.TrimPrefix(tag, "link::"))
		if link != "" && !seen[link] {
			seen[link] = true
			out = append(out, "["+safeText(link, false)+"]")
		}
	}
	return out
}

func killedContext(tombstone store.Tombstone) string {
	date := tombstone.KilledAt
	if stamp, err := time.Parse(time.RFC3339Nano, tombstone.KilledAt); err == nil {
		date = stamp.UTC().Format("2 Jan 2006")
	}
	return fmt.Sprintf("killed %s\n%s", safeText(date, false), safeText(tombstone.Reason, false))
}

// markdownWith binds glamour to the palette-derived config of spec section
// 5.2, replacing the hardcoded DarkStyleConfig clone this file used to carry.
func markdownWith(styles *theme.Styles) markdownRenderer {
	return func(source string, width int) string {
		renderer, err := glamour.NewTermRenderer(
			glamour.WithStyles(styles.Markdown),
			glamour.WithWordWrap(max(width, 1)),
		)
		if err != nil {
			return safeText(source, true)
		}
		rendered, err := renderer.Render(parityMarkdown(safeText(source, true)))
		if err != nil {
			return safeText(source, true)
		}
		return strings.Trim(rendered, "\r\n")
	}
}

// renderCompletionGate reports whether the card can finish, and the status
// slot that says so at a glance.
func renderCompletionGate(task board.Task, links store.TaskLinks, loading bool, linksErr error) (string, theme.Slot) {
	var reasons []string
	if warning := store.CompletionWarning(task); warning != "" {
		reasons = append(reasons, warning)
	}
	var open []board.Task
	for _, blocker := range links.BlockedBy {
		if blocker.Status != board.StatusDone && blocker.Status != board.StatusCancelled {
			open = append(open, blocker)
		}
	}
	if len(open) > 0 {
		noun := "open linked blocker"
		if len(open) != 1 {
			noun += "s"
		}
		reasons = append(reasons, fmt.Sprintf("%d %s %s", len(open), noun, taskChips(open)))
	}
	unknown := ""
	if loading {
		unknown = "linked blockers loading"
	} else if linksErr != nil {
		unknown = "linked blockers unavailable"
	}
	if len(reasons) > 0 {
		if unknown != "" {
			reasons = append(reasons, unknown)
		}
		return "completion gate  blocked: " + strings.Join(reasons, "; "), theme.StatusDanger
	}
	if unknown != "" {
		return "completion gate  unknown: " + unknown, theme.FgSubtle
	}
	return "completion gate  clear", theme.StatusOK
}

func taskChips(tasks []board.Task) string {
	chips := make([]string, 0, len(tasks))
	for _, task := range tasks {
		ref := task.ID
		if task.Seq > 0 {
			ref = fmt.Sprintf("#%d", task.Seq)
		}
		chips = append(chips, fmt.Sprintf("[%s %s]", ref, task.Status))
	}
	return strings.Join(chips, "  ")
}

func safeText(text string, keepNewlines bool) string {
	text = ansi.Strip(text)
	var out strings.Builder
	for _, r := range text {
		if r == '\t' {
			out.WriteString("    ")
			continue
		}
		if r == '\v' || r == '\f' || r == '\r' {
			out.WriteByte(' ')
			continue
		}
		if keepNewlines && r == '\n' {
			out.WriteRune(r)
			continue
		}
		if unicode.IsControl(r) {
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}
