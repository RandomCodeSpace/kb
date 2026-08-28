package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

type temporalTickMsg struct {
	generation uint64
	deadline   time.Time
}

type temporalScheduler func(time.Duration, temporalTickMsg) tea.Cmd

type temporalScheduleInput struct {
	projectionGeneration uint64
	renderedAt           time.Time
	shippedDate          string
	shippedRevision      uint64
	shippedVisible       bool
	stopped              bool
}

func defaultTemporalScheduler(delay time.Duration, message temporalTickMsg) tea.Cmd {
	return theme.Tick(max(delay, 0), message)
}

func nextTaskAgeBoundary(task board.Task, renderedAt time.Time) (time.Time, bool) {
	var reference time.Time
	switch task.Status {
	case board.StatusTodo, board.StatusCancelled:
		reference = task.CreatedAt
	case board.StatusDoing:
		reference = task.MovedAt
	default:
		return time.Time{}, false
	}
	if reference.IsZero() {
		return time.Time{}, false
	}
	elapsed := renderedAt.Sub(reference)
	if elapsed < 0 {
		elapsed = 0
	}
	if task.Status == board.StatusDoing {
		if elapsed < 2*time.Hour {
			return reference.Add(2 * time.Hour), true
		}
		if elapsed < day {
			return reference.Add((elapsed/time.Hour + 1) * time.Hour), true
		}
	}
	return reference.Add((elapsed/day + 1) * day), true
}

func nextTaskTemporalBoundary(task board.Task, renderedAt time.Time) (time.Time, bool) {
	deadline, have := nextTaskAgeBoundary(task, renderedAt)
	if validDueDate(task.Due) {
		deadline, have = earlierTemporalDeadline(deadline, have, nextLocalMidnight(renderedAt), renderedAt)
	}
	return deadline, have
}

func nextLocalMidnight(at time.Time) time.Time {
	local := at.In(at.Location())
	return time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, local.Location())
}

func validDueDate(value string) bool {
	_, err := time.Parse(shippedDateLayout, value)
	return err == nil
}

func earlierTemporalDeadline(current time.Time, have bool, candidate time.Time, renderedAt time.Time) (time.Time, bool) {
	if !candidate.After(renderedAt) || have && !candidate.Before(current) {
		return current, have
	}
	return candidate, true
}

func (m Model) nextBoardTemporalBoundary() (time.Time, bool) {
	deadline, have, _ := m.nextBoardTemporalBoundaryWithVisits()
	return deadline, have
}

func (m Model) nextBoardTemporalBoundaryWithVisits() (time.Time, bool, int) {
	if m.current != nil && m.current.geometry.initialized &&
		m.current.geometry.renderedAt.Equal(m.renderedAt) {
		var deadline time.Time
		var have bool
		for index := range m.current.geometry.columns {
			candidate := cardNodeMinTemporal(m.current.geometry.columns[index].index.root)
			if candidate.IsZero() {
				continue
			}
			deadline, have = earlierTemporalDeadline(deadline, have, candidate, m.renderedAt)
		}
		if m.shippedCount() > 0 {
			deadline, have = earlierTemporalDeadline(
				deadline, have, nextLocalMidnight(m.renderedAt), m.renderedAt,
			)
		}
		return deadline, have, 0
	}
	var tasks []board.Task
	if m.current != nil && m.current.projection.initialized {
		tasks = m.current.projection.board.Tasks
	} else {
		tasks = m.filteredBoard().Tasks
	}
	var deadline time.Time
	var have bool
	midnight := nextLocalMidnight(m.renderedAt)
	dueValidity := make(map[string]bool)
	for _, task := range tasks {
		if age, ok := nextTaskAgeBoundary(task, m.renderedAt); ok {
			deadline, have = earlierTemporalDeadline(deadline, have, age, m.renderedAt)
		}
		validDue, known := dueValidity[task.Due]
		if task.Due != "" && !known {
			validDue = validDueDate(task.Due)
			dueValidity[task.Due] = validDue
		}
		if validDue {
			deadline, have = earlierTemporalDeadline(deadline, have, midnight, m.renderedAt)
		}
	}
	if m.shippedCount() > 0 {
		deadline, have = earlierTemporalDeadline(deadline, have, midnight, m.renderedAt)
	}
	return deadline, have, len(tasks)
}

func (m *Model) reconcileTemporalSchedule() tea.Cmd {
	input := temporalScheduleInput{
		projectionGeneration: m.currentProjectionGeneration(),
		renderedAt:           m.renderedAt,
		shippedDate:          m.shipped.Date,
		shippedRevision:      m.shipped.revision,
		shippedVisible:       m.shippedCount() > 0,
		stopped:              m.stopped,
	}
	if m.haveTemporalScheduleInput && input == m.temporalScheduleInput {
		return nil
	}
	m.temporalScheduleInput = input
	m.haveTemporalScheduleInput = true
	deadline, have, visits := m.nextBoardTemporalBoundaryWithVisits()
	m.mutateRenderPlanStats(func(stats *RenderPlanStats) { stats.TemporalTaskVisits += uint64(visits) })
	if m.stopped {
		have = false
	}
	if !have {
		if !m.temporalDeadline.IsZero() {
			m.temporalGeneration++
			m.temporalDeadline = time.Time{}
		}
		return nil
	}
	if deadline.Equal(m.temporalDeadline) {
		return nil
	}
	m.temporalGeneration++
	m.temporalDeadline = deadline
	scheduler := m.temporalSchedule
	if scheduler == nil {
		scheduler = defaultTemporalScheduler
	}
	message := temporalTickMsg{generation: m.temporalGeneration, deadline: deadline}
	m.mutateRenderPlanStats(func(stats *RenderPlanStats) { stats.TemporalScheduledTicks++ })
	return scheduler(deadline.Sub(m.now()), message)
}

func (m Model) matchesTemporalTick(message temporalTickMsg) bool {
	return message.generation != 0 && message.generation == m.temporalGeneration &&
		!m.temporalDeadline.IsZero() && message.deadline.Equal(m.temporalDeadline)
}

func (m *Model) mutateRenderPlanStats(mutate func(*RenderPlanStats)) {
	if m.current == nil {
		return
	}
	next := *m.current
	mutate(&next.stats)
	m.current = &next
}
