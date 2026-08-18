package carddetail

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

// Writer is the direct SQLite projection used by detail actions. Reader stays
// separate so read-only and lightweight callers do not need mutation stubs.
type Writer interface {
	AddComment(user, taskRef, author, body string) (store.Comment, error)
	DeleteComment(user string, id int) (store.Comment, error)
	Link(user, blockerRef, blockedRef string) (blocker, blocked board.Task, err error)
	Unlink(user, aRef, bRef string) error
}

type actionMode uint8

const (
	actionNone actionMode = iota
	actionAddComment
	actionDeleteComment
	actionAddLink
	actionDeleteLink
)

type mutationKind uint8

const (
	mutationAddComment mutationKind = iota
	mutationDeleteComment
	mutationAddLink
	mutationDeleteLink
)

type mutationCompletedMsg struct {
	taskID     string
	session    uint64
	kind       mutationKind
	comment    store.Comment
	blocker    board.Task
	blocked    board.Task
	other      board.Task
	currentSeq int
	err        error
}

type linkChoice struct {
	task   board.Task
	blocks bool
}

func newCommentInput() textarea.Model {
	input := textarea.New()
	input.Prompt = ""
	input.Placeholder = "Write a comment"
	input.ShowLineNumbers = false
	input.SetWidth(64)
	input.SetHeight(6)
	return input
}

func newLinkInput() textinput.Model {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "task number, UUID, or unique prefix"
	input.SetWidth(48)
	input.CharLimit = 256
	return input
}

// OwnsInput is the root-routing seam for text entry, selection, confirmation,
// and in-flight writes. Destructive or focus-changing root shortcuts must not
// run while it returns true.
func (m Model) OwnsInput() bool {
	return m.action != actionNone || m.saving || m.driftMode != driftNone
}

// ConsumeChanged reports one acknowledged mutation exactly once.
func (m *Model) ConsumeChanged() bool {
	changed := m.changed
	m.changed = false
	return changed
}

// IsMutationMessage identifies action results without exposing their concrete
// message types to the root package.
func IsMutationMessage(message tea.Msg) bool {
	_, ok := message.(mutationCompletedMsg)
	return ok
}

func (m *Model) beginAction(next actionMode) tea.Cmd {
	if m.writer == nil || m.saving {
		return nil
	}
	switch next {
	case actionDeleteComment:
		if m.loading {
			m.setStatus("comments are still loading", false)
			m.rebuildBody()
			return nil
		}
		if m.commentsErr != nil {
			m.setStatus("comments unavailable; retry after the next refresh", true)
			m.rebuildBody()
			return nil
		}
		if len(m.comments) == 0 {
			m.setStatus("no comments to delete", false)
			m.rebuildBody()
			return nil
		}
	case actionDeleteLink:
		if m.loading {
			m.setStatus("blocker links are still loading", false)
			m.rebuildBody()
			return nil
		}
		if m.linksErr != nil {
			m.setStatus("blocker links unavailable; retry after the next refresh", true)
			m.rebuildBody()
			return nil
		}
		if len(m.linkChoices()) == 0 {
			m.setStatus("no blocker links to remove", false)
			m.rebuildBody()
			return nil
		}
	}
	m.actionSession++
	m.action = next
	m.selection = 0
	m.confirm = false
	m.statusMessage = ""
	m.statusIsError = false
	m.commentInput = newCommentInput()
	m.linkInput = newLinkInput()
	m.currentBlocks = true
	if next == actionAddComment {
		m.commentInput.Focus()
	}
	if next == actionAddLink {
		m.linkInput.Focus()
	}
	m.scroll = 0
	m.rebuildBody()
	m.focusActionSelection()
	return nil
}

func (m *Model) cancelAction() {
	if m.saving {
		m.setStatus("write in progress", false)
		m.rebuildBody()
		return
	}
	if m.confirm {
		m.confirm = false
		m.setStatus("deletion cancelled", false)
		m.rebuildBody()
		return
	}
	m.actionSession++
	m.action = actionNone
	m.selection = 0
	m.statusMessage = ""
	m.statusIsError = false
	m.scroll = 0
	m.rebuildBody()
}

func (m *Model) updateActionKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	if key == "esc" {
		m.cancelAction()
		return nil
	}
	if m.saving {
		return nil
	}
	switch m.action {
	case actionAddComment:
		if key == "ctrl+s" {
			return m.startAddComment()
		}
		var command tea.Cmd
		m.commentInput, command = m.commentInput.Update(msg)
		if value := safeText(m.commentInput.Value(), true); value != m.commentInput.Value() {
			m.commentInput.SetValue(value)
		}
		m.rebuildBody()
		return command
	case actionAddLink:
		if key == "tab" || key == "shift+tab" {
			m.currentBlocks = !m.currentBlocks
			m.rebuildBody()
			return nil
		}
		if key == "enter" {
			return m.startAddLink()
		}
		var command tea.Cmd
		m.linkInput, command = m.linkInput.Update(msg)
		if value := safeText(m.linkInput.Value(), false); value != m.linkInput.Value() {
			m.linkInput.SetValue(value)
		}
		m.rebuildBody()
		return command
	case actionDeleteComment, actionDeleteLink:
		return m.updateDeleteKey(key)
	}
	return nil
}

func (m *Model) updateDeleteKey(key string) tea.Cmd {
	count := len(m.comments)
	if m.action == actionDeleteLink {
		count = len(m.linkChoices())
	}
	if count == 0 {
		m.cancelAction()
		return nil
	}
	if m.confirm {
		if key == "enter" {
			if m.action == actionDeleteComment {
				return m.startDeleteComment()
			}
			return m.startDeleteLink()
		}
		return nil
	}
	switch key {
	case "up", "k":
		m.selection = (m.selection - 1 + count) % count
	case "down", "j":
		m.selection = (m.selection + 1) % count
	case "enter":
		m.confirm = true
		m.setStatus("press Enter again to delete; Esc cancels", true)
	}
	m.rebuildBody()
	m.focusActionSelection()
	return nil
}

func (m *Model) focusActionSelection() {
	if m.action != actionDeleteComment && m.action != actionDeleteLink {
		return
	}
	focusLine := 0
	for i, line := range m.bodyLines {
		if strings.HasPrefix(line, "> ") {
			focusLine = i
			break
		}
	}
	_, innerHeight, _ := paneGeometry(m.width, m.height)
	if focusLine < m.scroll {
		m.scroll = focusLine
	}
	if focusLine >= m.scroll+innerHeight {
		m.scroll = focusLine - innerHeight + 1
	}
	m.clampScroll()
}

func (m *Model) startAddComment() tea.Cmd {
	body := strings.TrimSpace(safeText(m.commentInput.Value(), true))
	if body == "" {
		m.setStatus("comment must not be empty", true)
		m.rebuildBody()
		return nil
	}
	m.saving = true
	m.setStatus("adding comment...", false)
	m.rebuildBody()
	taskID, session, user, writer := m.task.ID, m.actionSession, m.user, m.writer
	return func() tea.Msg {
		comment, err := writer.AddComment(user, taskID, user, body)
		return mutationCompletedMsg{
			taskID: taskID, session: session, kind: mutationAddComment, comment: comment, err: err,
		}
	}
}

func (m *Model) startDeleteComment() tea.Cmd {
	if len(m.comments) == 0 {
		return nil
	}
	comment := m.comments[min(max(m.selection, 0), len(m.comments)-1)]
	m.saving = true
	m.setStatus(fmt.Sprintf("deleting comment c%d...", comment.ID), false)
	m.rebuildBody()
	taskID, session, user, writer := m.task.ID, m.actionSession, m.user, m.writer
	return func() tea.Msg {
		deleted, err := writer.DeleteComment(user, comment.ID)
		return mutationCompletedMsg{
			taskID: taskID, session: session, kind: mutationDeleteComment, comment: deleted, err: err,
		}
	}
}

func (m *Model) startAddLink() tea.Cmd {
	target := strings.TrimSpace(safeText(m.linkInput.Value(), false))
	if target == "" {
		m.setStatus("target task is required", true)
		m.rebuildBody()
		return nil
	}
	m.saving = true
	m.setStatus("adding blocker link...", false)
	m.rebuildBody()
	taskID, session, user, writer := m.task.ID, m.actionSession, m.user, m.writer
	currentBlocks := m.currentBlocks
	return func() tea.Msg {
		blockerRef, blockedRef := taskID, target
		if !currentBlocks {
			blockerRef, blockedRef = target, taskID
		}
		blocker, blocked, err := writer.Link(user, blockerRef, blockedRef)
		return mutationCompletedMsg{
			taskID: taskID, session: session, kind: mutationAddLink,
			blocker: blocker, blocked: blocked, err: err,
		}
	}
}

func (m *Model) startDeleteLink() tea.Cmd {
	choices := m.linkChoices()
	if len(choices) == 0 {
		return nil
	}
	choice := choices[min(max(m.selection, 0), len(choices)-1)]
	m.saving = true
	m.setStatus("removing blocker link...", false)
	m.rebuildBody()
	taskID, session, user, writer := m.task.ID, m.actionSession, m.user, m.writer
	currentSeq := m.task.Seq
	return func() tea.Msg {
		err := writer.Unlink(user, taskID, choice.task.ID)
		return mutationCompletedMsg{
			taskID: taskID, session: session, kind: mutationDeleteLink,
			other: choice.task, currentSeq: currentSeq, err: err,
		}
	}
}

func (m *Model) finishMutation(msg mutationCompletedMsg) tea.Cmd {
	if msg.taskID != m.task.ID || msg.session != m.actionSession {
		return nil
	}
	m.saving = false
	if msg.err != nil {
		m.setStatus("write refused: "+safeText(msg.err.Error(), false), true)
		m.rebuildBody()
		return nil
	}
	m.changed = true
	m.action = actionNone
	m.confirm = false
	m.selection = 0
	switch msg.kind {
	case mutationAddComment:
		m.setStatus(fmt.Sprintf("comment c%d added", msg.comment.ID), false)
	case mutationDeleteComment:
		m.setStatus(fmt.Sprintf("comment c%d deleted", msg.comment.ID), false)
	case mutationAddLink:
		m.setStatus(taskActionRef(msg.blocker)+" now blocks "+taskActionRef(msg.blocked), false)
	case mutationDeleteLink:
		current := board.Task{ID: msg.taskID, Seq: msg.currentSeq}
		m.setStatus("link between "+taskActionRef(current)+" and "+taskActionRef(msg.other)+" removed", false)
	}
	m.scroll = 0
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

func taskActionRef(task board.Task) string {
	if task.Seq > 0 {
		return fmt.Sprintf("#%d", task.Seq)
	}
	return safeText(task.ID, false)
}

func (m *Model) setStatus(message string, isError bool) {
	m.statusMessage = safeText(message, false)
	m.statusIsError = isError
}

func (m Model) linkChoices() []linkChoice {
	choices := make([]linkChoice, 0, len(m.links.Blocks)+len(m.links.BlockedBy))
	for _, task := range m.links.Blocks {
		choices = append(choices, linkChoice{task: task, blocks: true})
	}
	for _, task := range m.links.BlockedBy {
		choices = append(choices, linkChoice{task: task})
	}
	return choices
}

func (m Model) actionBody(width int) string {
	ref := m.task.ID
	if m.task.Seq > 0 {
		ref = fmt.Sprintf("#%d", m.task.Seq)
	}
	var lines []string
	switch m.action {
	case actionAddComment:
		lines = []string{"ADD COMMENT / " + ref, "", "Comment:"}
		lines = append(lines, textareaLines(m.commentInput, width, 8)...)
	case actionDeleteComment:
		lines = []string{"DELETE COMMENT / " + ref, ""}
		start, end := selectionWindow(len(m.comments), m.selection, max(m.height-10, 3))
		if start > 0 {
			lines = append(lines, fmt.Sprintf("  ... %d earlier", start))
		}
		for i := start; i < end; i++ {
			comment := m.comments[i]
			marker := "  "
			if i == m.selection {
				marker = "> "
			}
			preview := strings.ReplaceAll(safeText(comment.Body, true), "\n", " ")
			lines = append(lines, fmt.Sprintf("%sc%d  %s  %s", marker, comment.ID, safeText(comment.Author, false), preview))
		}
		if end < len(m.comments) {
			lines = append(lines, fmt.Sprintf("  ... %d later", len(m.comments)-end))
		}
	case actionAddLink:
		direction := "this card blocks target"
		if !m.currentBlocks {
			direction = "target blocks this card"
		}
		lines = []string{
			"ADD BLOCKER LINK / " + ref,
			"",
			"Direction: " + direction,
			"Target: " + textInputLine(m.linkInput, width-len("Target: ")),
		}
	case actionDeleteLink:
		lines = []string{"REMOVE BLOCKER LINK / " + ref, ""}
		choices := m.linkChoices()
		start, end := selectionWindow(len(choices), m.selection, max(m.height-10, 3))
		if start > 0 {
			lines = append(lines, fmt.Sprintf("  ... %d earlier", start))
		}
		for i := start; i < end; i++ {
			choice := choices[i]
			marker := "  "
			if i == m.selection {
				marker = "> "
			}
			direction := "blocks"
			if !choice.blocks {
				direction = "blocked by"
			}
			lines = append(lines, marker+direction+" "+taskChips([]board.Task{choice.task}))
		}
		if end < len(choices) {
			lines = append(lines, fmt.Sprintf("  ... %d later", len(choices)-end))
		}
	}
	if m.statusMessage != "" {
		prefix := "status: "
		if m.statusIsError {
			prefix = "error: "
		}
		lines = append(lines, "", prefix+m.statusMessage)
	}
	for i := range lines {
		lines[i] = fitDetailLine(safeText(lines[i], true), max(width-2, 1))
	}
	return strings.Join(lines, "\n")
}

func selectionWindow(count, selection, limit int) (int, int) {
	if count <= 0 {
		return 0, 0
	}
	limit = min(max(limit, 1), count)
	selection = min(max(selection, 0), count-1)
	start := max(selection-limit/2, 0)
	start = min(start, count-limit)
	return start, start + limit
}

func (m Model) actionFooter(width int) string {
	if m.driftMode != driftNone {
		return m.driftFooter()
	}
	if m.saving {
		return "write in progress | esc stays here"
	}
	if m.action == actionNone && m.statusMessage != "" {
		prefix := "status: "
		if m.statusIsError {
			prefix = "error: "
		}
		return prefix + m.statusMessage
	}
	if m.confirm {
		return "enter confirm delete | esc cancel"
	}
	switch m.action {
	case actionAddComment:
		return "ctrl+s add comment | esc back"
	case actionDeleteComment:
		return "up/down choose | enter delete | esc back"
	case actionAddLink:
		return "tab direction | enter add | esc back"
	case actionDeleteLink:
		return "up/down choose | enter remove | esc back"
	default:
		switch {
		case width >= 40:
			return "e edit v drift c add d/u rm b esc close ↑/↓"
		case width >= 26:
			return "e c add d/u rm b esc close"
		default:
			return "e c d u b esc"
		}
	}
}

func textInputLine(input textinput.Model, width int) string {
	value := safeText(input.Value(), false)
	if value == "" {
		value = safeText(input.Placeholder, false)
	}
	position := min(max(input.Position(), 0), len([]rune(input.Value())))
	visible := insertCursor(value, position)
	return ansi.Truncate(visible, max(width, 0), "")
}

func textareaLines(input textarea.Model, width, rows int) []string {
	value := input.Value()
	placeholder := false
	if value == "" {
		value = input.Placeholder
		placeholder = true
	}
	logical := strings.Split(safeText(value, true), "\n")
	line := min(max(input.Line(), 0), len(logical)-1)
	start := max(line-rows+1, 0)
	end := min(start+rows, len(logical))
	out := make([]string, 0, rows)
	for i := start; i < end; i++ {
		content := logical[i]
		if i == line && !placeholder {
			content = insertCursor(content, min(input.Column(), len([]rune(content))))
		}
		out = append(out, "  "+ansi.Truncate(content, max(width-2, 0), ""))
	}
	for len(out) < rows {
		out = append(out, "  ")
	}
	return out
}

func insertCursor(value string, position int) string {
	runes := []rune(value)
	position = min(max(position, 0), len(runes))
	return string(runes[:position]) + "|" + string(runes[position:])
}
