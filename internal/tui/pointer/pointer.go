// Package pointer maps rendered terminal cells to pointer interactions.
package pointer

import tea "charm.land/bubbletea/v2"

// Point is a zero-based terminal cell.
type Point struct {
	X int
	Y int
}

// Surface couples rendered content with the pointer map from that render pass.
type Surface struct {
	Content string
	Pointer func(tea.MouseMsg) tea.Cmd
}

// Rect is a half-open rectangle in zero-based terminal cells.
type Rect struct {
	X0 int
	Y0 int
	X1 int
	Y1 int
}

// Viewport projects logical content rows into a visible terminal rectangle.
type Viewport struct {
	Rect   Rect
	Scroll int
}

// Action builds the model message produced by one pointer activation.
type Action func(Point) tea.Msg

type region struct {
	rect   Rect
	action Action
}

type wheelRegion struct {
	rect   Rect
	action func(int) tea.Msg
}

// Map resolves rendered terminal regions to model messages.
type Map struct {
	regions []region
	wheels  []wheelRegion
}

// Add registers an action region. Later regions take precedence when they overlap.
func (m *Map) Add(rect Rect, action Action) {
	if rect.empty() || action == nil {
		return
	}
	m.regions = append(m.regions, region{rect: rect, action: action})
}

// AddBackdrop registers the portion of bounds outside pane as one action.
func (m *Map) AddBackdrop(bounds, pane Rect, action Action) {
	if bounds.empty() || action == nil {
		return
	}
	pane = pane.intersect(bounds)
	if pane.empty() {
		m.Add(bounds, action)
		return
	}
	m.Add(Rect{X0: bounds.X0, Y0: bounds.Y0, X1: bounds.X1, Y1: pane.Y0}, action)
	m.Add(Rect{X0: bounds.X0, Y0: pane.Y1, X1: bounds.X1, Y1: bounds.Y1}, action)
	m.Add(Rect{X0: bounds.X0, Y0: pane.Y0, X1: pane.X0, Y1: pane.Y1}, action)
	m.Add(Rect{X0: pane.X1, Y0: pane.Y0, X1: bounds.X1, Y1: pane.Y1}, action)
}

// AddWheel registers a wheel zone. The action receives -1 for up and +1 for down.
func (m *Map) AddWheel(rect Rect, action func(delta int) tea.Msg) {
	if rect.empty() || action == nil {
		return
	}
	m.wheels = append(m.wheels, wheelRegion{rect: rect, action: action})
}

// Handler returns the immutable render snapshot's mouse callback.
func (m Map) Handler() func(tea.MouseMsg) tea.Cmd {
	regions := append([]region(nil), m.regions...)
	wheels := append([]wheelRegion(nil), m.wheels...)
	pressed := -1
	dragged := false
	hit := func(point Point) int {
		for index := len(regions) - 1; index >= 0; index-- {
			candidate := regions[index]
			if candidate.action != nil && candidate.rect.contains(point) {
				return index
			}
		}
		return -1
	}
	return func(message tea.MouseMsg) tea.Cmd {
		mouse := message.Mouse()
		point := Point{X: mouse.X, Y: mouse.Y}
		if _, wheel := message.(tea.MouseWheelMsg); wheel {
			pressed = -1
			dragged = false
			delta := 0
			switch mouse.Button {
			case tea.MouseWheelUp:
				delta = -1
			case tea.MouseWheelDown:
				delta = 1
			default:
				return nil
			}
			for index := len(wheels) - 1; index >= 0; index-- {
				candidate := wheels[index]
				if candidate.action == nil || !candidate.rect.contains(point) {
					continue
				}
				result := candidate.action(delta)
				if result == nil {
					return nil
				}
				return func() tea.Msg { return result }
			}
			return nil
		}
		switch message.(type) {
		case tea.MouseClickMsg:
			dragged = false
			pressed = -1
			if mouse.Button == tea.MouseLeft {
				pressed = hit(point)
			}
			return nil
		case tea.MouseMotionMsg:
			if pressed >= 0 && mouse.Button == tea.MouseLeft {
				dragged = true
			}
			return nil
		case tea.MouseReleaseMsg:
			if mouse.Button != tea.MouseLeft && mouse.Button != tea.MouseNone {
				pressed = -1
				dragged = false
				return nil
			}
		default:
			return nil
		}
		pressedIndex := pressed
		wasDragged := dragged
		pressed = -1
		dragged = false
		if pressedIndex < 0 || wasDragged || hit(point) != pressedIndex {
			return nil
		}
		result := regions[pressedIndex].action(point)
		if result == nil {
			return nil
		}
		return func() tea.Msg { return result }
	}
}

// Row projects one logical content-row span into the viewport and clips it.
func (v Viewport) Row(logicalRow, x0, x1 int) (Rect, bool) {
	row := Rect{
		X0: v.Rect.X0 + x0,
		Y0: v.Rect.Y0 + logicalRow - v.Scroll,
		X1: v.Rect.X0 + x1,
		Y1: v.Rect.Y0 + logicalRow - v.Scroll + 1,
	}.intersect(v.Rect)
	if row.empty() {
		return Rect{}, false
	}
	return row, true
}

func (r Rect) empty() bool {
	return r.X0 >= r.X1 || r.Y0 >= r.Y1
}

func (r Rect) contains(point Point) bool {
	return point.X >= r.X0 && point.X < r.X1 && point.Y >= r.Y0 && point.Y < r.Y1
}

func (r Rect) intersect(other Rect) Rect {
	return Rect{
		X0: max(r.X0, other.X0),
		Y0: max(r.Y0, other.Y0),
		X1: min(r.X1, other.X1),
		Y1: min(r.Y1, other.Y1),
	}
}
