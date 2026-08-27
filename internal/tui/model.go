// Package tui implements kb's full-screen terminal interface.
package tui

import (
	"context"
	"errors"
	"time"
	"unsafe"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/RandomCodeSpace/kb/internal/ai"
	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/forge"
	"github.com/RandomCodeSpace/kb/internal/store"
	"github.com/RandomCodeSpace/kb/internal/tui/action"
	"github.com/RandomCodeSpace/kb/internal/tui/adrsplit"
	"github.com/RandomCodeSpace/kb/internal/tui/carddetail"
	"github.com/RandomCodeSpace/kb/internal/tui/cardeditor"
	"github.com/RandomCodeSpace/kb/internal/tui/cmdpalette"
	"github.com/RandomCodeSpace/kb/internal/tui/issueimport"
	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
	"github.com/RandomCodeSpace/kb/internal/tui/widget"
)

const (
	defaultWidth  = 80
	defaultHeight = 24
	ctrlCKey      = "ctrl+c"
)

var boardStatuses = [...]board.Status{
	board.StatusTodo,
	board.StatusDoing,
	board.StatusDone,
	board.StatusCancelled,
}

type boardReader interface {
	Board(user string) (board.Board, error)
}

type dataVersionReader interface {
	DataVersion(context.Context) (int64, error)
}

type boardLoadedMsg struct {
	board      board.Board
	err        error
	generation uint64
}

type dataVersionMsg struct {
	version int64
	err     error
}

type pollTickMsg struct{}

// Model is the single Bubble Tea root. It owns board state, focus, terminal
// dimensions, and all message routing; commands perform IO and only return
// messages, so Update remains deterministic.
type Model struct {
	store                       boardReader
	moveStore                   taskMoveStore
	actionStore                 taskActionStore
	watcher                     dataVersionReader
	user                        string
	dataDir                     string
	board                       board.Board
	styles                      *theme.Styles
	boardView                   boardViewState
	filter                      boardFilterState
	projects                    projectSwitcher
	activeProject               string
	detail                      carddetail.Model
	editor                      cardeditor.Model
	adr                         adrsplit.Model
	issueImport                 issueimport.Model
	palette                     cmdpalette.Model
	selectAfterLoad             string
	width                       int
	height                      int
	loading                     bool
	haveBoardSnapshot           bool
	reloadPending               bool
	loadErr                     error
	pollErr                     error
	dataVersion                 int64
	haveVersion                 bool
	pollStarted                 bool
	loadGeneration              uint64
	activeLoadGen               uint64
	watcherPollFastPaths        uint64
	watcherVersionFastPaths     uint64
	watcherStaleLoadFastPaths   uint64
	watcherLifecycleFallbacks   uint64
	renderImpactClassifications uint64
	stopped                     bool
	helpOpen                    bool
	isDark                      bool
	profile                     colorprofile.Profile
	readContext                 context.Context
	now                         func() time.Time
	renderedAt                  time.Time
	temporalSchedule            temporalScheduler
	temporalGeneration          uint64
	temporalVisualGeneration    uint64
	temporalDeadline            time.Time
	temporalScheduleInput       temporalScheduleInput
	haveTemporalScheduleInput   bool
	savePreferences             func(tuiPreferences) error
	preferenceErr               error
	prefSaving                  bool
	prefPending                 *tuiPreferences
	settings                    *settingsModel
	settingsNew                 func() *settingsModel
	move                        cardMoveState
	action                      taskActionState
	taskActionSession           uint64
	pointerState                pointer.State
	clicks                      pointer.Clicks
	spin                        spinner.Model
	scroll                      boardScrollState
	celebrate                   shipCelebration
	celebrateGen                uint64
	grace                       graceState
	actionStatus                string
	actionStatusError           bool
	actionNotice                bool
	noticeSeq                   uint64
	shipped                     shippedRecord
	inputAdmission              *inputAdmissionStats
	pointerMailbox              *pointerMailbox
	pointerOwner                renderHandlerTopology
	pointerOwnerEpoch           uint64
	pointerOwnerSeq             uint64
	pointerAdmission            pointerAdmissionState

	// The brand mark of spec section 10.6. The stretch and the reveal seed are
	// rolled once in newModel and are plain fields, so a test pins them by
	// assignment: no sync.Once, no package-level cache, no seam exemption.
	version      string
	brandStretch int
	brandSeed    int64
	brandFrame   int
	brandGen     uint32

	// renderPlan is the sole complete-frame publication point. It is copied
	// with Model, so content and the pointer closure derived from its hit map
	// advance atomically through Update.
	retainedRenderView
}

// SetVersion records the build version the launch screen's meta row prints.
// The TUI cannot reach package main's build info, so the string arrives on
// tui.Run and is stored here (spec section 10.6.5).
func (m *Model) SetVersion(version string) {
	m.version = version
	// The version changes terminal content and can change row geometry. Until
	// render records report their own dependencies, a root setter must be just
	// as conservative as Update.
	m.rebuildRenderPlan(renderImpactAll)
}

// applyStyles adopts a rebuilt design system and hands it to every sub-model
// the root owns.
func (m *Model) applyStyles(styles *theme.Styles) {
	m.styles = styles
	// Spec section 10.2.1: the plain tier takes its cadence from the one clock,
	// which is a property of the rebuilt theme rather than of the component.
	m.spin.Spinner = styles.Spinner
	m.detail.SetStyles(styles)
	m.editor.SetStyles(styles)
	m.adr.SetStyles(styles)
	m.issueImport.SetStyles(styles)
	m.palette.SetStyles(styles)
	if m.settings != nil {
		m.settings.SetStyles(styles)
	}
	m.settleBrand(styles)
}

// SetActiveProject hands the board the project local commands default to
// (KB_PROJECT, else the project stored by kb project use). It is the switcher's
// opening scope and the editor's default for a new card; stored preferences
// override the scope, never the default.
func (m *Model) SetActiveProject(name string) {
	m.activeProject = name
	m.projects.restore(projectSwitcher{}, name)
	m.editor.SetProjectDefault(m.projectDefault())
	m.rebuildRenderPlan(renderImpactAll)
}

func (m *Model) configureAI(runner *ai.Runner, ctx context.Context) {
	if runner == nil {
		return
	}
	m.editor.SetAIRunner(runner, ctx)
	adrStore, _ := m.store.(adrsplit.Store)
	m.adr = adrsplit.New(adrStore, runner, m.user, ctx)
	// Both overlays create cards, so both need the project every card must
	// carry; they resolve it from the data directory at write time, which is
	// what makes a kb project use mid-session take effect on the next batch.
	m.adr.SetDataDir(m.dataDir)
	m.adr.SetStyles(m.styles)
	if direct, ok := m.store.(*store.Store); ok {
		backend := forge.New(direct, runner, nil)
		m.issueImport = issueimport.New(direct, backend, m.user, ctx)
		m.issueImport.SetDataDir(m.dataDir)
		m.issueImport.SetStyles(m.styles)
		m.detail.SetDriftBackend(backend, ctx)
	}
}

// NewModel creates the root model for one local board owner.
func NewModel(store boardReader, watcher dataVersionReader, user string) Model {
	return newModel(store, watcher, user, context.Background())
}

func newModel(
	store boardReader,
	watcher dataVersionReader,
	user string,
	ctx context.Context,
) Model {
	detailReader, _ := store.(carddetail.Reader)
	editorStore, _ := store.(cardeditor.Store)
	moveStore, _ := store.(taskMoveStore)
	actionStore, _ := store.(taskActionStore)
	now := time.Now
	// Spec sections 6.3 and 10.7.5: the palette is resolved once for a dark
	// truecolor terminal and rebuilt only when tea.BackgroundColorMsg or
	// tea.ColorProfileMsg says otherwise. Both defaults are the reference
	// target, which is the correct assumption until the terminal answers.
	styles := theme.NewFor(true, colorprofile.TrueColor)
	brandStretch, brandSeed := widget.RollBrand(styles.Metrics)
	model := Model{
		store:          store,
		styles:         styles,
		isDark:         true,
		profile:        colorprofile.TrueColor,
		moveStore:      moveStore,
		actionStore:    actionStore,
		watcher:        watcher,
		user:           user,
		board:          board.Board{Title: "Board"},
		filter:         newBoardFilterState(),
		projects:       projectSwitcher{all: true},
		detail:         carddetail.New(detailReader, user, styles),
		editor:         cardeditor.New(editorStore, user),
		palette:        cmdpalette.New(),
		width:          defaultWidth,
		height:         defaultHeight,
		loading:        watcher == nil,
		readContext:    ctx,
		now:            now,
		renderedAt:     now(),
		action:         newTaskActionState(),
		spin:           spinner.New(spinner.WithSpinner(styles.Spinner)),
		inputAdmission: &inputAdmissionStats{},
		pointerMailbox: newPointerMailbox(),

		brandStretch: brandStretch,
		brandSeed:    brandSeed,
		brandGen:     1,
	}
	if watcher == nil {
		// Init is a value receiver, so the no-watcher startup generation must be
		// established before it creates the first load command.
		model.loadGeneration = 1
		model.activeLoadGen = 1
	}
	model.rebuildRenderPlan(renderImpactAll)
	return model
}

// Init loads the first board snapshot and starts the external-write watcher.
func (m Model) Init() tea.Cmd {
	if m.stopped {
		return nil
	}
	start := m.loadBoard(m.activeLoadGen)
	if m.watcher != nil {
		// Establish the connection-local baseline before loading. If these ran
		// concurrently, a commit between the stale load and a later baseline
		// could be absorbed into that baseline and never trigger a refresh.
		start = m.readDataVersion()
	}
	// The first snapshot is a plain-tier wait (spec section 10.8.7), so the tick
	// chain opens with the load rather than waiting for a message to notice the
	// board is already busy.
	return batchCommands(start, m.brandReveal(), m.trackBoardBusy(false))
}

// Update is the input-feel gate of spec section 10.3 wrapped around the root's
// own routing. Three machines sit here and nowhere else, because all three are
// about which messages reach a surface rather than what a surface does with
// them: the destructive-prompt grace, the double-click window, and the footer
// notice TTL. Each expires because a message arrived, never because a render
// read a clock.
type modelUpdateCommands struct {
	followUp tea.Cmd
	geometry tea.Cmd
	temporal tea.Cmd
}

func (commands modelUpdateCommands) command() tea.Cmd {
	return batchCommands(commands.followUp, commands.geometry, commands.temporal)
}

func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	next, commands := m.updateWithCommands(message)
	return next, commands.command()
}

// updateWithCommands keeps application follow-ups separate from the offscreen
// geometry chain. Production batches both in their original order; the
// performance harness uses the explicit seam to settle geometry without
// guessing which opaque tea.Cmd happens to own the worker token.
func (m Model) updateWithCommands(message tea.Msg) (Model, modelUpdateCommands) {
	if tick, ok := message.(temporalTickMsg); ok && !m.matchesTemporalTick(tick) {
		m.mutateRenderPlanStats(func(stats *RenderPlanStats) { stats.TemporalStaleTicks++ })
		return m, modelUpdateCommands{}
	}
	if flush, ok := message.(pointerFlushMsg); ok {
		return m.flushPointerIntent(flush)
	}
	if raw, ok := message.(tea.MouseMsg); ok {
		return m.updateRawPointer(raw)
	}
	if intent, ok := message.(boardNavigationIntentMsg); ok && !m.matchesBoardNavigationIntent(intent) {
		m.inputAdmission.discard()
		return m, modelUpdateCommands{}
	}
	// This identity belongs to admission, not publication. Keeping the two
	// counters separate lets the performance harness observe coalesced, delayed,
	// and stale publications once those later phases become asynchronous.
	m.acceptedMessageID++
	if commands, handled := m.fastWatcherLifecycle(message); handled {
		return m, commands
	}
	checkSource := m.current == nil || !m.current.projection.matchesSourceIdentity(m.board)
	if batch, ok := message.(geometryBatchMsg); ok {
		next := m.installGeometryBatch(batch)
		next.acceptedMessageID = m.acceptedMessageID
		commands := modelUpdateCommands{geometry: next.startGeometryWorker()}
		next.publishKeyboardAdmissionSnapshot()
		return next, commands
	}
	beforeBoard := m.board
	next, command := m.update(message)
	next.acceptedMessageID = m.acceptedMessageID
	next.renderImpactClassifications++
	impact := renderImpactAfterUpdate(message, m, next)
	if impact != 0 {
		next.rebuildRenderPlanAfterUpdate(impact, checkSource || !sameBoardSourceIdentity(beforeBoard, next.board))
	} else {
		// Admission state belongs to Update even when the accepted message has no
		// render effect. Keep the immutable input snapshot current and continue an
		// already-active geometry chain without manufacturing a frame.
		next.adoptPreparedProjectionWithoutFrame()
		next.publishKeyboardAdmissionSnapshot()
	}
	if next.stopped {
		next.resetPointerPipeline()
	}
	return next, modelUpdateCommands{
		followUp: command,
		geometry: next.startGeometryWorker(),
		temporal: next.reconcileTemporalSchedule(),
	}
}

// fastWatcherLifecycle handles settled watcher messages before the generic
// root router and render-impact classifier. Only strictly stale board-load
// results enter this seam; any result that can install source state stays generic.
func (m *Model) fastWatcherLifecycle(message tea.Msg) (modelUpdateCommands, bool) {
	settledPlainBoard := m.settledWatcherPlainBoard(false)
	switch msg := message.(type) {
	case pollTickMsg:
		if !settledPlainBoard || m.watcher == nil {
			m.watcherLifecycleFallbacks++
			return modelUpdateCommands{}, false
		}
		m.watcherPollFastPaths++
		return modelUpdateCommands{
			followUp: m.readDataVersion(),
			geometry: m.startGeometryWorker(),
			temporal: m.reconcileTemporalSchedule(),
		}, true
	case dataVersionMsg:
		if !settledPlainBoard || msg.err != nil || m.watcher == nil || !m.pollStarted ||
			!m.haveVersion || !m.haveBoardSnapshot || m.loadErr != nil || m.pollErr != nil ||
			m.move.saving || m.action.busy || m.move.lifted != nil {
			m.watcherLifecycleFallbacks++
			return modelUpdateCommands{}, false
		}
		m.watcherVersionFastPaths++
		followUp := m.observeDataVersion(msg)
		return modelUpdateCommands{
			followUp: followUp,
			geometry: m.startGeometryWorker(),
			temporal: m.reconcileTemporalSchedule(),
		}, true
	case boardLoadedMsg:
		// Only a generation proven older than the newest requested load belongs
		// to this lifecycle seam. Current results still take the generic path and
		// remain the sole place that can install board data.
		if msg.generation == 0 || msg.generation >= m.loadGeneration {
			return modelUpdateCommands{}, false
		}
		settledStaleLoad := m.settledWatcherPlainBoard(true)
		if !settledStaleLoad || !m.loading || m.activeLoadGen == 0 || msg.generation != m.activeLoadGen {
			m.watcherLifecycleFallbacks++
			return modelUpdateCommands{}, false
		}
		m.watcherStaleLoadFastPaths++
		followUp := m.finishBoardLoad(msg)
		m.adoptPreparedProjectionWithoutFrame()
		m.publishKeyboardAdmissionSnapshot()
		return modelUpdateCommands{
			followUp: followUp,
			geometry: m.startGeometryWorker(),
			temporal: m.reconcileTemporalSchedule(),
		}, true
	default:
		return modelUpdateCommands{}, false
	}
}

// settledWatcherPlainBoard is pointer-local because this predicate sits on a
// repeated lifecycle hot path. Calling the root's value-receiver overlay and
// launch helpers here would copy the entire Model for every watcher message.
func (m *Model) settledWatcherPlainBoard(allowLaunch bool) bool {
	if m.stopped || m.current == nil || m.preparedProjection != nil || m.helpOpen ||
		m.detail.IsOpen() || m.settings != nil || m.adr.IsOpen() || m.editor.IsOpen() ||
		m.action.open() || m.issueImport.IsOpen() || m.palette.IsOpen() {
		return false
	}
	return allowLaunch || !m.loading || m.haveBoardSnapshot
}

func (m Model) updateRawPointer(raw tea.MouseMsg) (Model, modelUpdateCommands) {
	if m.stopped {
		m.resetPointerPipeline()
		return m, modelUpdateCommands{}
	}
	command, route, result := m.pointerMailbox.take(raw)
	if result != pointerMailboxMatched || !route.sameOwner(m.pointerRoute()) {
		if m.mouseLiftedForWheel(raw) {
			return m.cancelMouseLiftForDiscardedWheel(m.pointerRoute())
		}
		return m.failClosedPointer()
	}
	if command == nil {
		if m.mouseLiftedForWheel(raw) {
			return m.cancelMouseLiftForDiscardedWheel(route)
		}
		if next, commands, ok := m.reuseBoundaryWheel(raw, route); ok {
			return next, commands
		}
		if _, wheel := raw.(tea.MouseWheelMsg); wheel {
			m.pointerAdmission.cancelPending()
		}
		m.discardPointer()
		return m, modelUpdateCommands{}
	}
	// Execute after the mailbox mutex is released. Pointer lifecycle and its
	// exact activation remain ordered inside this raw Update.
	resolved := command()
	if resolved == nil {
		return m, modelUpdateCommands{}
	}
	return m.admitResolvedPointer(resolved, route, raw)
}

func (m Model) applyResolvedPointer(message tea.Msg, publish bool) (Model, modelUpdateCommands) {
	before := m
	beforeBoard := m.board
	m.pointerAdmission.accepted++
	m.acceptedMessageID++
	var command tea.Cmd
	if pointer.IsMessage(message) {
		m, command = m.route(message)
		if command != nil {
			activation := command()
			if activation != nil {
				m.acceptedMessageID++
				m, command = m.update(activation)
			} else {
				command = nil
			}
		}
	} else {
		m, command = m.update(message)
	}
	checkSource := m.current == nil || !m.current.projection.matchesSourceIdentity(beforeBoard) ||
		!sameBoardSourceIdentity(beforeBoard, m.board)
	if publish {
		m.renderImpactClassifications++
		m.rebuildRenderPlanAfterUpdate(renderImpactAfterUpdate(message, before, m), checkSource)
	}
	if m.stopped {
		m.resetPointerPipeline()
	}
	return m, modelUpdateCommands{
		followUp: command,
		geometry: m.startGeometryWorker(),
		temporal: m.reconcileTemporalSchedule(),
	}
}

func (m *Model) resetPointerPipeline() {
	m.pointerMailbox.reset()
	m.pointerAdmission.resetAll()
	m.pointerState = m.pointerState.ClearCapture().ClearHoverObservation()
	m.clicks = m.clicks.Reset()
	if m.move.lifted != nil && m.move.lifted.fromMouse {
		m.cancelCardMove("pointer input lost")
	}
}

func (m Model) failClosedPointer() (Model, modelUpdateCommands) {
	_, rootHoverObserved := m.pointerState.HoverPoint()
	hadHoverObservation := m.pointerAdmission.hoverObserved || rootHoverObserved
	hadHoverVisual := m.pointerAdmission.hoverControl != "" || m.pointerState.Hovered() != ""
	hadActiveState := m.pointerAdmission.captureKey != "" || m.pointerState.Active() ||
		(m.move.lifted != nil && m.move.lifted.fromMouse) || hadHoverVisual
	m.discardPointer()
	if hadHoverObservation {
		if reset := pointer.ResetHover(); reset != nil {
			if message := reset(); message != nil {
				m, _ = m.route(message)
			}
		}
	}
	if cancel := pointer.Cancel(); cancel != nil {
		if message := cancel(); message != nil {
			m, _ = m.route(message)
		}
	}
	beforeBoard := m.board
	m.resetPointerPipeline()
	if !hadActiveState {
		return m, modelUpdateCommands{}
	}
	sourceChanged := !sameBoardSourceIdentity(beforeBoard, m.board)
	impact := renderImpactPointer | renderImpactAppearance
	if sourceChanged {
		impact |= renderImpactTasks | renderImpactProjection | renderImpactLayout
	}
	m.rebuildRenderPlanAfterUpdate(impact, sourceChanged)
	return m, modelUpdateCommands{geometry: m.startGeometryWorker(), temporal: m.reconcileTemporalSchedule()}
}

func (m Model) pointerRoute() pointerRouteIdentity {
	route := pointerRouteIdentity{owner: m.pointerOwner, ownerSession: m.pointerOwnerSeq}
	if m.current != nil {
		route.geometry = m.current.geometry.generation
		route.snapshot = m.current.stats.InstalledSnapshotID
	}
	return route
}

func (m Model) update(message tea.Msg) (Model, tea.Cmd) {
	if m.stopped {
		return m, nil
	}
	if m.grace.expire(message) {
		return m, nil
	}
	if next, handled := m.clicks.Expire(message); handled {
		m.clicks = next
		return m, nil
	}
	if m.expireNotice(message) {
		return m, nil
	}
	if m.settleScroll(message) {
		return m, nil
	}
	if command, handled := m.stepCelebration(message); handled {
		return m, command
	}
	// The board's own plain tier, matched by spinner id so an overlay's tick
	// still reaches the overlay that owns it.
	if msg, ok := message.(spinner.TickMsg); ok && msg.ID == m.spin.ID() {
		return m, m.boardSpinTick(msg)
	}
	if m.grace.swallows(message) {
		return m, m.grace.absorb(m.themeStyles().Timing)
	}
	grace := m.graceIdentity()
	notice := m.noticeKey()
	busy := m.boardBusy()
	next, command := m.route(message)
	handoff := next.syncEngines()
	return next, batchCommands(command, handoff, next.trackGrace(grace), next.trackNotice(notice),
		next.trackBoardBusy(busy))
}

// route handles global messages before any future pane-specific routing.
func (m Model) route(message tea.Msg) (Model, tea.Cmd) {
	if pointer.IsMessage(message) {
		switch {
		case m.palette.IsOpen():
			// Spec section 10.5.3: overlays receive mouse before the board, in
			// the section 4 z-order, and the palette is the topmost surface.
			return m, m.palette.Update(message)
		case m.issueImport.IsOpen():
			return m, m.issueImport.Update(message)
		case m.action.open():
			next, command, _ := m.pointerState.Update(message)
			m.pointerState = next
			return m, command
		case m.editor.IsOpen():
			return m, m.editor.Update(message)
		case m.adr.IsOpen():
			return m, m.adr.Update(message)
		case m.settings != nil:
			return m, m.settings.Update(message)
		case m.detail.IsOpen():
			return m, m.updateDetail(message)
		default:
			next, command, _ := m.pointerState.Update(message)
			m.pointerState = next
			return m, command
		}
	}
	if isBoardPointerMessage(message) && (m.helpOpen || m.action.open() || m.action.busy ||
		m.issueImport.IsOpen() || m.palette.IsOpen() || m.settings != nil || m.editor.IsOpen() ||
		m.adr.IsOpen() || m.detail.IsOpen()) {
		return m, nil
	}
	if m.palette.IsOpen() && cmdpalette.IsMessage(message) {
		command := m.palette.Update(message)
		if choice, ok := m.palette.ConsumeChoice(); ok {
			return m.runPaletteAction(choice, command)
		}
		return m, command
	}
	if m.palette.IsOpen() {
		switch msg := message.(type) {
		case tea.KeyPressMsg:
			if msg.String() == ctrlCKey {
				break
			}
			command := m.palette.Update(msg)
			if choice, ok := m.palette.ConsumeChoice(); ok {
				return m.runPaletteAction(choice, command)
			}
			return m, command
		case boardCardClickedMsg, boardColumnClickedMsg,
			filterTextClickedMsg, filterLabelClickedMsg, filterClearClickedMsg, filterProjectClickedMsg,
			boardPointerDownMsg, boardPointerMoveMsg, boardPointerUpMsg, boardColumnScrolledMsg,
			boardFooterClickedMsg:
			return m, nil
		}
	}
	if m.helpOpen {
		switch msg := message.(type) {
		case helpClosedMsg:
			m.helpOpen = false
			return m, nil
		case tea.KeyPressMsg:
			switch msg.String() {
			case "esc", "?":
				m.helpOpen = false
				return m, nil
			case "q", ctrlCKey:
				m.stopped = true
				m.reloadPending = false
				return m, tea.Quit
			default:
				return m, nil
			}
		case boardCardClickedMsg, boardColumnClickedMsg,
			filterTextClickedMsg, filterLabelClickedMsg, filterClearClickedMsg, filterProjectClickedMsg,
			boardPointerDownMsg, boardPointerMoveMsg, boardPointerUpMsg, boardColumnScrolledMsg,
			boardFooterClickedMsg:
			return m, nil
		}
	}
	if isTaskActionMessage(message) {
		return m, m.updateTaskAction(message)
	}
	if m.action.open() || m.action.busy {
		switch message.(type) {
		case tea.KeyPressMsg, spinner.TickMsg:
			return m, m.updateTaskAction(message)
		case boardCardClickedMsg, boardColumnClickedMsg,
			filterTextClickedMsg, filterLabelClickedMsg, filterClearClickedMsg, filterProjectClickedMsg,
			boardPointerDownMsg, boardPointerMoveMsg, boardPointerUpMsg, boardColumnScrolledMsg:
			return m, nil
		}
	}
	if carddetail.IsMutationMessage(message) {
		return m, m.updateDetail(message)
	}
	if m.issueImport.IsOpen() && issueimport.IsMessage(message) {
		command := m.issueImport.Update(message)
		if m.issueImport.ConsumeChanged() {
			return m, batchCommands(command, m.requireFreshBoard())
		}
		return m, command
	}
	if m.issueImport.IsOpen() {
		switch msg := message.(type) {
		case tea.KeyPressMsg:
			if msg.String() == ctrlCKey {
				break
			}
			command := m.issueImport.Update(msg)
			if m.issueImport.ConsumeChanged() {
				return m, batchCommands(command, m.requireFreshBoard())
			}
			return m, command
		case boardCardClickedMsg, boardColumnClickedMsg,
			filterTextClickedMsg, filterLabelClickedMsg, filterClearClickedMsg, filterProjectClickedMsg,
			boardPointerDownMsg, boardPointerMoveMsg, boardPointerUpMsg, boardColumnScrolledMsg:
			return m, nil
		}
	}
	if m.actionNotice && isBoardUserInput(message) {
		m.actionNotice = false
	}
	if m.move.lifted == nil && m.move.notice && isBoardUserInput(message) {
		m.move.notice = false
	}
	if m.settings != nil && isSettingsMessage(message) {
		command := m.settings.Update(message)
		if m.settings.closed {
			m.settings = nil
		}
		return m, command
	}
	if m.editor.IsOpen() && cardeditor.IsMessage(message) {
		command := m.editor.Update(message)
		if taskID, saved := m.editor.ConsumeSaved(); saved {
			m.selectAfterLoad = taskID
			return m, batchCommands(command, m.requireFreshBoard())
		}
		return m, command
	}
	if m.editor.IsOpen() {
		switch msg := message.(type) {
		case tea.KeyPressMsg:
			if msg.String() == ctrlCKey {
				// The explicit terminal interrupt remains global.
				break
			}
			return m, m.editor.Update(msg)
		case boardCardClickedMsg, boardColumnClickedMsg,
			filterTextClickedMsg, filterLabelClickedMsg, filterClearClickedMsg, filterProjectClickedMsg,
			boardPointerDownMsg, boardPointerMoveMsg, boardPointerUpMsg, boardColumnScrolledMsg:
			return m, nil
		}
	}
	if m.adr.IsOpen() && adrsplit.IsMessage(message) {
		command := m.adr.Update(message)
		if m.adr.ConsumeChanged() {
			return m, batchCommands(command, m.requireFreshBoard())
		}
		return m, command
	}
	if m.adr.IsOpen() {
		switch msg := message.(type) {
		case tea.KeyPressMsg:
			if msg.String() == ctrlCKey {
				break
			}
			return m, m.adr.Update(msg)
		case boardCardClickedMsg, boardColumnClickedMsg,
			filterTextClickedMsg, filterLabelClickedMsg, filterClearClickedMsg, filterProjectClickedMsg,
			boardPointerDownMsg, boardPointerMoveMsg, boardPointerUpMsg, boardColumnScrolledMsg:
			return m, nil
		}
	}
	var detailCmd tea.Cmd
	if m.detail.IsOpen() {
		if resolved, pointerMessage := m.detail.ResolvePointerMessage(message); pointerMessage {
			if resolved == nil {
				return m, nil
			}
			message = resolved
		}
		switch msg := message.(type) {
		case tea.KeyPressMsg:
			if m.detail.OwnsInput() && msg.String() != ctrlCKey {
				return m, m.updateDetail(message)
			}
			switch msg.String() {
			case "esc":
				m.detail.Close()
				return m, nil
			case "e":
				if m.editor.Enabled() {
					if task, ok := m.taskByID(m.detail.TaskID()); ok {
						m.editor.SetProjectDefault(m.projectDefault())
						return m, m.editor.OpenEdit(task)
					}
				}
				return m, nil
			case "t", "x", "r", "D", "delete", "backspace":
				if handled, command := m.handleSelectedTaskAction(msg.String()); handled {
					return m, command
				}
				return m, nil
			case "q", ctrlCKey:
				// Preserve root quit while idle detail is open.
			default:
				return m, m.updateDetail(message)
			}
		case carddetail.OpenTaskRefMsg:
			return m, m.openTaskRef(msg.Seq)
		case boardCardClickedMsg, boardColumnClickedMsg,
			filterTextClickedMsg, filterLabelClickedMsg, filterClearClickedMsg, filterProjectClickedMsg,
			boardPointerDownMsg, boardPointerMoveMsg, boardPointerUpMsg, boardColumnScrolledMsg:
			return m, nil
		default:
			detailCmd = m.updateDetail(message)
		}
	}
	switch msg := message.(type) {
	case boardNavigationIntentMsg:
		m.applyBoardNavigationIntent(msg)
		return m, nil
	case tea.KeyPressMsg:
		if msg.String() == ctrlCKey {
			if m.settings != nil {
				m.settings.Close()
			}
			m.editor.CancelAsync()
			m.adr.Close()
			m.issueImport.Close()
			m.palette.Close()
			m.stopped = true
			m.reloadPending = false
			return m, tea.Quit
		}
		if m.settings != nil {
			command := m.settings.Update(msg)
			if m.settings.closed {
				m.settings = nil
			}
			return m, command
		}
		if m.move.lifted != nil {
			key := msg.String()
			if m.move.saving && key != "q" {
				return m, nil
			}
			switch key {
			case "esc":
				m.cancelCardMove("")
				return m, nil
			case "q":
				// The root quit contract remains available while a write is hung.
			case "enter", "space":
				return m, m.startCardDrop()
			case "up", "down", "left", "right", "h", "j", "k", "l":
				if preview, handled := m.move.previewKey(key); handled {
					m.board = preview
					m.boardView.focusTask(m.filteredBoard(), m.move.lifted.taskID)
				}
				return m, nil
			case "s", "c", "1", "2", "3", "4", "tab", "shift+tab", "n", "e", "a", "i", "/", "f", "x", "p", "P":
				m.cancelCardMove("focus changed")
			default:
				return m, nil
			}
		}
		if handled, command := m.handleFilterKey(msg); handled {
			return m, command
		}
		if handled, command := m.handleSelectedTaskAction(msg.String()); handled {
			return m, command
		}
		switch msg.String() {
		case "q":
			m.stopped = true
			m.reloadPending = false
			return m, tea.Quit
		case "?":
			m.helpOpen = true
			return m, nil
		case action.PaletteKey:
			return m, m.openPalette()
		case "s":
			if m.settingsNew != nil {
				m.settings = m.settingsNew()
				m.settings.SetStyles(m.styles)
				return m, m.settings.Init()
			}
		case "a":
			if m.adr.Enabled() && !m.move.saving {
				return m, m.adr.Open()
			}
		case "i":
			if m.issueImport.Enabled() && !m.writeBusy() {
				return m, m.issueImport.Open()
			}
		case "enter":
			if task, ok := m.selectedTask(); ok {
				m.detail.Resize(m.width, m.height)
				return m, m.detail.Open(task)
			}
			return m, nil
		case "n":
			if m.editor.Enabled() {
				m.editor.SetProjectDefault(m.projectDefault())
				return m, m.editor.OpenAdd(boardStatuses[m.boardView.column])
			}
		case "e":
			if m.editor.Enabled() {
				if task, ok := m.selectedTask(); ok {
					m.editor.SetProjectDefault(m.projectDefault())
					return m, m.editor.OpenEdit(task)
				}
			}
		case "p":
			return m, m.cycleProject(1)
		case "P":
			return m, m.cycleProject(-1)
		case "space":
			if !m.loading {
				if task, ok := m.selectedTask(); ok {
					m.move.beginVisible(m.board, m.filteredBoard(), task, m.boardView.visibleStatuses(), false)
				}
			}
			return m, nil
		default:
			if m.handleBoardNavigationKey(msg.String()) == boardToggledCancelled {
				return m, m.queuePreferences()
			}
		}
	case boardCardClickedMsg:
		if m.move.saving {
			return m, nil
		}
		if m.move.lifted != nil && !m.move.saving {
			m.cancelCardMove("focus changed")
		}
		m.filter.blur()
		if m.focusBoardTask(msg.taskID) {
			if task, ok := m.selectedTask(); ok {
				m.detail.Resize(m.width, m.height)
				return m, m.detail.Open(task)
			}
		}
	case boardFooterClickedMsg:
		return m, m.handleBoardFooterClick(msg.key)
	case boardColumnClickedMsg:
		if m.move.saving {
			return m, nil
		}
		if m.move.lifted != nil && !m.move.saving {
			m.cancelCardMove("focus changed")
		}
		m.filter.blur()
		m.focusBoardColumn(msg.status)
	case boardPointerDownMsg:
		if m.loading || m.move.saving {
			return m, nil
		}
		if m.move.lifted != nil {
			m.cancelCardMove("focus changed")
		}
		m.filter.blur()
		if !m.focusBoardTask(msg.taskID) {
			return m, nil
		}
		if task, ok := m.selectedTask(); ok {
			m.move.beginVisible(m.board, m.filteredBoard(), task, m.boardView.visibleStatuses(), true)
		}
	case boardPointerMoveMsg:
		if preview, handled := m.move.previewMouse(msg.status, msg.beforeTaskID); handled {
			m.board = preview
			m.boardView.focusTask(m.filteredBoard(), m.move.lifted.taskID)
		}
	case boardPointerUpMsg:
		if m.move.lifted == nil || !m.move.lifted.fromMouse {
			return m, nil
		}
		if msg.resolved {
			if !msg.valid {
				m.cancelCardMove("pointer released outside a drop target")
				return m, nil
			}
			if preview, handled := m.move.previewMouse(msg.status, msg.beforeTaskID); handled {
				m.board = preview
				m.boardView.focusTask(m.filteredBoard(), m.move.lifted.taskID)
			}
		}
		if !m.move.lifted.dragged {
			taskID := m.move.lifted.taskID
			// Spec section 10.3.5: the classifier runs on the completed
			// gesture, so a lift that ended on its origin is excluded by the
			// dragged flag rather than by luck. kb's single click already opens
			// the card detail (frozen v1.0.1), which is the same binding the
			// double-click carries, so arming the window here keeps the two
			// routes on one code path.
			clicks, _, window := m.clicks.Click(pointer.ControlID(taskID), false,
				m.themeStyles().Timing.DoubleClickWindow)
			m.clicks = clicks
			m.cancelCardMove("")
			m.move.status = ""
			m.focusBoardTask(taskID)
			if task, ok := m.selectedTask(); ok {
				m.detail.Resize(m.width, m.height)
				return m, batchCommands(window, m.detail.Open(task))
			}
			return m, window
		}
		m.clicks = m.clicks.Reset()
		return m, m.startCardDrop()
	case boardPointerCancelMoveMsg:
		if m.move.lifted != nil && m.move.lifted.fromMouse {
			m.cancelCardMove("")
		}
	case boardColumnScrolledMsg:
		column := statusIndex(msg.status)
		if m.move.lifted != nil && m.move.lifted.fromMouse {
			dragged := m.move.lifted.dragged
			liftedTaskID := m.move.lifted.taskID
			if dragged {
				for index, anchor := range m.boardView.scrollAnchors {
					if anchor.TaskID == liftedTaskID {
						m.boardView.scrollAnchors[index] = boardTaskAnchor{}
					}
				}
				msg.anchor = boardTaskAnchor{}
			}
			m.cancelCardMove("")
		}
		m.boardView.scrolls[column] = max(msg.offset, 0)
		m.boardView.scrollAnchors[column] = msg.anchor
		m.boardView.manualScroll[column] = true
		return m, batchCommands(detailCmd, m.armScrollLinger(column))
	case cardMoveStoredMsg:
		return m, m.finishCardDrop(msg)
	case filterTextClickedMsg:
		if m.settings != nil {
			return m, nil
		}
		if m.move.saving {
			return m, nil
		}
		if m.move.lifted != nil {
			m.cancelCardMove("focus changed")
		}
		return m, m.filter.focusText()
	case filterLabelClickedMsg:
		if m.settings != nil {
			return m, nil
		}
		if m.move.saving {
			return m, nil
		}
		if m.move.lifted != nil {
			m.cancelCardMove("focus changed")
		}
		return m, m.mutateFilter(func(filter *boardFilterState) { filter.toggleTag(msg.tag) })
	case filterClearClickedMsg:
		if m.settings != nil {
			return m, nil
		}
		if m.move.saving {
			return m, nil
		}
		if m.move.lifted != nil {
			m.cancelCardMove("focus changed")
		}
		return m, m.mutateFilter(func(filter *boardFilterState) { filter.clear() })
	case filterProjectClickedMsg:
		if m.settings != nil {
			return m, nil
		}
		if m.move.saving {
			return m, nil
		}
		if m.move.lifted != nil {
			m.cancelCardMove("focus changed")
		}
		return m, m.cycleProject(1)
	case preferenceSavedMsg:
		return m, m.finishPreferences(msg)
	case tea.BackgroundColorMsg:
		// Spec section 6.2: New is called on program start, here, and on the
		// profile message below, nowhere else. Every style in the tree is
		// rebuilt exactly once per answer, and every pane the root owns adopts
		// the same instance.
		m.isDark = msg.IsDark()
		m.applyStyles(theme.NewFor(m.isDark, m.profile))
	case tea.ColorProfileMsg:
		// Spec section 10.7.5: the terminal floor is resolved once from the
		// detected profile and no view ever sees the profile itself. bubbletea
		// sends this at startup and again if the terminal upgrades to truecolor
		// through a capability report.
		if msg.Profile == m.profile {
			return m, detailCmd
		}
		m.profile = msg.Profile
		m.applyStyles(theme.NewFor(m.isDark, m.profile))
	case tea.WindowSizeMsg:
		if msg.Width > 0 {
			m.width = msg.Width
		}
		if msg.Height > 0 {
			m.height = msg.Height
		}
		m.detail.Resize(m.width, m.height)
	case brandStepMsg:
		return m, batchCommands(detailCmd, m.stepBrand(msg))
	case boardLoadedMsg:
		next := m.finishBoardLoad(msg)
		return m, batchCommands(detailCmd, next)
	case pollTickMsg:
		return m, m.readDataVersion()
	case temporalTickMsg:
		m.temporalDeadline = time.Time{}
		m.renderedAt = m.now()
		if m.renderedAt.Before(msg.deadline) {
			m.renderedAt = msg.deadline
		}
		m.normalizeShippedAt(m.renderedAt)
		m.temporalVisualGeneration++
	case dataVersionMsg:
		next := m.observeDataVersion(msg)
		return m, next
	}
	return m, detailCmd
}

func isBoardPointerMessage(message tea.Msg) bool {
	switch message.(type) {
	case boardCardClickedMsg, boardColumnClickedMsg,
		filterTextClickedMsg, filterLabelClickedMsg, filterClearClickedMsg, filterProjectClickedMsg,
		boardPointerDownMsg, boardPointerMoveMsg, boardPointerUpMsg, boardPointerCancelMoveMsg, boardColumnScrolledMsg,
		boardFooterClickedMsg:
		return true
	default:
		return false
	}
}

func (m *Model) updateDetail(message tea.Msg) tea.Cmd {
	command := m.detail.Update(message)
	if m.detail.ConsumeChanged() {
		return batchCommands(command, m.requireFreshBoard())
	}
	return command
}

func isBoardUserInput(message tea.Msg) bool {
	switch message.(type) {
	case tea.KeyPressMsg, boardNavigationIntentMsg, boardCardClickedMsg, boardColumnClickedMsg,
		boardPointerDownMsg, boardPointerMoveMsg, boardPointerUpMsg,
		boardColumnScrolledMsg, filterTextClickedMsg, filterLabelClickedMsg, filterClearClickedMsg,
		filterProjectClickedMsg:
		return true
	default:
		return false
	}
}

// observeDataVersion advances the watcher baseline and schedules exactly one
// successor poll. Baselines are opaque: only equality with the last successful
// value matters.
func (m *Model) observeDataVersion(msg dataVersionMsg) tea.Cmd {
	var load tea.Cmd
	if msg.err != nil {
		if errors.Is(msg.err, context.Canceled) && errors.Is(m.readContext.Err(), context.Canceled) {
			return nil
		}
		m.pollErr = msg.err
		if !m.haveVersion || m.loadErr != nil {
			load = m.startBoardLoad()
		}
		return m.continuePollingAfter(load)
	}

	m.pollErr = nil
	initial := !m.haveVersion
	changed := !initial && msg.version != m.dataVersion
	m.dataVersion = msg.version
	m.haveVersion = true

	switch {
	case initial || changed:
		load = m.requireFreshBoard()
	case m.loadErr != nil:
		load = m.startBoardLoad()
	}
	return m.continuePollingAfter(load)
}

func (m Model) continuePollingAfter(load tea.Cmd) tea.Cmd {
	if !m.pollStarted {
		if load == nil {
			return nil
		}
		// Keep the initial load lazy and distinguishable from the poll chain. A
		// one-command batch still runs after Update publishes its current frame.
		return func() tea.Msg { return tea.BatchMsg{load} }
	}
	return m.pollAfter(load)
}

// finishBoardLoad commits only successful snapshots. A pending freshness
// obligation starts one serialized successor without creating another poll.
func (m *Model) finishBoardLoad(msg boardLoadedMsg) tea.Cmd {
	generation := msg.generation
	if generation == 0 {
		// Package-local callers predating generation-tagged IO inject a settled
		// snapshot directly. Production load commands always carry a generation.
		generation = m.activeLoadGen
		if generation == 0 {
			generation = m.loadGeneration
		}
	}
	if m.activeLoadGen != 0 && generation != m.activeLoadGen {
		return nil
	}
	m.loading = false
	m.activeLoadGen = 0
	if generation != 0 && generation < m.loadGeneration {
		// A newer freshness obligation arrived while this serialized read was in
		// flight. Never flash the older board; immediately spend the single
		// successor slot on the newest requested generation.
		m.reloadPending = false
		return m.startBoardLoadGeneration(m.loadGeneration)
	}
	m.loadErr = msg.err
	var detailCmd tea.Cmd
	if msg.err == nil {
		m.haveBoardSnapshot = true
		previous := m.filteredBoard()
		m.board = msg.board
		if m.current != nil {
			prepared, delta := m.current.projection.reconcileSource(*m)
			m.preparedProjection = &prepared
			m.preparedDerivations = delta.DerivedTasks
			m.preparedComparisons = len(m.board.Tasks)
		}
		filtered := m.filteredBoard()
		m.boardView.adoptBoard(previous, filtered)
		if m.selectAfterLoad != "" {
			focused := m.boardView.focusTask(filtered, m.selectAfterLoad)
			if focused || !m.reloadPending {
				m.selectAfterLoad = ""
			}
		}
		detailCmd = batchCommands(m.reconcileDetail(), m.reconcileEditor())
	}
	if m.reloadPending {
		m.reloadPending = false
		return batchCommands(detailCmd, m.startBoardLoadGeneration(m.loadGeneration))
	}
	if m.watcher != nil && !m.pollStarted {
		m.pollStarted = true
		return batchCommands(detailCmd, m.schedulePoll())
	}
	return detailCmd
}

func (m *Model) reconcileEditor() tea.Cmd {
	if !m.editor.IsOpen() || m.editor.TaskID() == "" {
		return nil
	}
	task, found := m.taskByID(m.editor.TaskID())
	return m.editor.Refresh(task, found)
}

func (m *Model) reconcileDetail() tea.Cmd {
	if !m.detail.IsOpen() {
		return nil
	}
	taskID := m.detail.TaskID()
	for _, task := range m.board.Tasks {
		if task.ID == taskID {
			return m.detail.Refresh(task)
		}
	}
	m.detail.Close()
	return nil
}

// startBoardLoad starts a fallback or retry only when no load is active. The
// active load already satisfies that obligation, so it does not queue another.
func (m *Model) startBoardLoad() tea.Cmd {
	if m.writeBusy() {
		m.loadGeneration++
		m.reloadPending = true
		return nil
	}
	if m.loading {
		return nil
	}
	m.loadGeneration++
	return m.startBoardLoadGeneration(m.loadGeneration)
}

func (m *Model) startBoardLoadGeneration(generation uint64) tea.Cmd {
	if generation == 0 {
		m.loadGeneration++
		generation = m.loadGeneration
	}
	if m.writeBusy() {
		m.reloadPending = true
		return nil
	}
	if m.loading {
		m.reloadPending = true
		return nil
	}
	m.loading = true
	m.activeLoadGen = generation
	return m.loadBoard(generation)
}

// requireFreshBoard records a new baseline/change obligation while a load is
// active. Multiple obligations coalesce into one serialized successor.
func (m *Model) requireFreshBoard() tea.Cmd {
	m.loadGeneration++
	if m.writeBusy() {
		m.reloadPending = true
		return nil
	}
	if m.move.lifted != nil {
		m.cancelCardMove("board changed; refreshing")
	}
	if m.loading {
		m.reloadPending = true
		return nil
	}
	return m.startBoardLoadGeneration(m.loadGeneration)
}

func (m Model) writeBusy() bool { return m.move.saving || m.action.busy }

func (m *Model) cancelCardMove(reason string) {
	if m.move.lifted == nil {
		return
	}
	taskID := m.move.lifted.taskID
	m.board = m.move.cancel(reason)
	filtered := m.filteredBoard()
	if !m.boardView.focusTask(filtered, taskID) {
		m.boardView.normalizeSelection(filtered)
	}
}

func (m Model) pollAfter(load tea.Cmd) tea.Cmd {
	if load == nil {
		return m.schedulePoll()
	}
	return tea.Batch(load, m.schedulePoll())
}

func batchCommands(commands ...tea.Cmd) tea.Cmd {
	filtered := make([]tea.Cmd, 0, len(commands))
	for _, command := range commands {
		if command != nil {
			filtered = append(filtered, command)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	if len(filtered) == 1 {
		return filtered[0]
	}
	return tea.Batch(filtered...)
}

// overlayOpen reports whether anything is elevated over the board this frame.
func (m Model) overlayOpen() bool {
	return m.helpOpen || m.detail.IsOpen() || m.settings != nil ||
		m.adr.IsOpen() || m.editor.IsOpen() || m.action.open() || m.issueImport.IsOpen() ||
		m.palette.IsOpen()
}

// openPalette hands the palette this board's theme and the features the help
// pane reports, then shows it. The features are resolved here rather than at
// construction because the forge and AI backends are wired after newModel runs.
func (m *Model) openPalette() tea.Cmd {
	m.palette.SetStyles(m.styles)
	m.palette.SetFeatures(m.actionFeatures())
	return m.palette.Open()
}

// runPaletteAction replays the key press the chosen registry row stands for, so
// the board's own handler runs the action. The palette therefore carries no
// dispatch of its own and cannot drift from the keymap: a rebind in the registry
// moves the key the board matches and the key the palette replays together.
// The replay goes through route rather than Update, for two reasons. It is what
// the caller is already inside, so the model stays concrete; and the section
// 10.3 gates Update wraps around route are about raw input arriving from a
// terminal, which a choice the user already committed in a panel is not.
func (m Model) runPaletteAction(choice action.Action, command tea.Cmd) (Model, tea.Cmd) {
	press, ok := choice.KeyPress()
	if !ok {
		return m, command
	}
	next, replayed := m.route(press)
	return next, batchCommands(command, replayed)
}

// backdrop is the model the board renders through this frame. Spec section 4
// step 1: an overlay dims the board by re-rendering it with the dimmed *Styles,
// which is a second palette built once by theme.New, never a post-pass over a
// rendered string.
func (m Model) backdrop() Model {
	if !m.overlayOpen() {
		return m
	}
	if dimmed := m.themeStyles().Dimmed; dimmed != nil {
		m.styles = dimmed
	}
	return m
}

// renderColdView is the differential oracle retained until the production
// render plan has passed every parity gate.
func (m Model) renderColdView() tea.View { return m.renderColdSnapshot().view }

func (m Model) renderColdSnapshot() coldRenderSnapshot {
	// The oracle is the full-board renderer. Keep the accepted immutable
	// projection so both paths consume identical tasks, but explicitly remove
	// retained geometry before entering the shared frame composer.
	m.renderingGeometry = nil
	m.cardArtifacts = nil
	base := m.renderBoardBase(m.overlayOpen())
	return m.composeSnapshot(base)
}

func (m Model) renderRetainedSnapshot() coldRenderSnapshot {
	base := m.renderBoardBase(m.overlayOpen())
	return m.composeSnapshot(base)
}

type boardRenderBase struct {
	content string
	hits    []boardHit
	columns [len(boardStatuses)]columnRenderSemantics
	pointer pointer.State
}

func (b boardRenderBase) ownedBytesEstimate() uint64 {
	estimate := uint64(len(b.content)) + uint64(cap(b.hits))*uint64(unsafe.Sizeof(boardHit{}))
	for _, hit := range b.hits {
		estimate += uint64(len(hit.status) + len(hit.taskID) + len(hit.tag) + len(hit.key))
	}
	return estimate
}

func (m Model) renderBoardBase(dimmed bool) boardRenderBase {
	backdrop := m
	if dimmed {
		// Retain the normal board artifacts only. Dimmed bases already have their
		// own bounded retained frame and are rendered lazily on overlay open.
		backdrop.cardArtifacts = nil
		if styles := m.themeStyles().Dimmed; styles != nil {
			backdrop.styles = styles
		}
	} else if backdrop.cardArtifacts != nil {
		backdrop.cardArtifacts.beginPass()
	}
	base := boardRenderBase{pointer: m.pointerState}
	if m.launching() {
		// Spec section 10.6.7: the launch screen owns the frame until the first
		// board snapshot lands, and it carries no hit regions because it
		// carries no controls.
		base.content = backdrop.renderLaunch()
	} else {
		base.content, base.hits = backdrop.renderBoard()
		base.hits = append([]boardHit(nil), base.hits...)
	}
	if m.renderingGeometry != nil && m.renderingProjection != nil {
		base.columns = m.renderingGeometry.renderSemantics(m.boardView, m.renderingProjection)
	}
	return base
}

func (m Model) composeSnapshot(base boardRenderBase) coldRenderSnapshot {
	content := base.content
	hits := base.hits
	handler := renderHandlerNone
	var overlayMouse func(tea.MouseMsg) tea.Cmd
	var overlayTopology pointer.Topology
	var ownerEpoch uint64
	if m.helpOpen {
		surface := m.keyboardHelpSurface(content)
		content = surface.Content
		overlayMouse = surface.Pointer
		overlayTopology = surface.Topology
		handler = renderHandlerHelp
		hits = nil
	}
	if m.detail.IsOpen() {
		surface := m.detail.PointerSurface(content, m.width, m.height)
		content = surface.Content
		overlayMouse = surface.Pointer
		overlayTopology = surface.Topology
		ownerEpoch = m.detail.PointerSession()
		handler = renderHandlerDetail
		hits = nil
	}
	if m.settings != nil {
		surface := m.settings.Surface(content, m.width, m.height)
		content = surface.Content
		overlayMouse = surface.Pointer
		overlayTopology = surface.Topology
		handler = renderHandlerSettings
		hits = nil
	}
	if m.adr.IsOpen() {
		content = m.adr.Overlay(content, m.width, m.height)
		surface := m.adr.PointerSurface(m.width, m.height)
		overlayMouse = surface.Pointer
		overlayTopology = surface.Topology
		ownerEpoch = m.adr.PointerSession()
		handler = renderHandlerADR
		hits = nil
	}
	if m.editor.IsOpen() {
		content = m.editor.Overlay(content, m.width, m.height)
		surface := m.editor.PointerSurface(m.width, m.height)
		overlayMouse = surface.Pointer
		overlayTopology = surface.Topology
		ownerEpoch = m.editor.PointerSession()
		handler = renderHandlerEditor
		hits = nil
	}
	if m.action.open() {
		surface := m.taskActionSurface(content)
		content = surface.Content
		overlayMouse = surface.Pointer
		overlayTopology = surface.Topology
		ownerEpoch = m.taskActionSession
		handler = renderHandlerAction
		hits = nil
	}
	if m.issueImport.IsOpen() {
		content = m.issueImport.Overlay(content, m.width, m.height)
		surface := m.issueImport.PointerSurface(m.width, m.height)
		overlayMouse = surface.Pointer
		overlayTopology = surface.Topology
		ownerEpoch = m.issueImport.PointerSession()
		handler = renderHandlerIssueImport
		hits = nil
	}
	if m.palette.IsOpen() {
		content = m.palette.Overlay(content, m.width, m.height)
		surface := m.palette.PointerSurface(m.width, m.height)
		overlayMouse = surface.Pointer
		overlayTopology = surface.Topology
		ownerEpoch = m.palette.PointerSession()
		handler = renderHandlerPalette
		hits = nil
	}
	view := tea.NewView(content)
	view.AltScreen = true
	// All-motion is safe only after semantic admission has resolved same-target
	// observations to the retained no-op path.
	view.MouseMode = tea.MouseModeAllMotion
	view.KeyboardEnhancements.ReportEventTypes = true
	if overlayMouse != nil {
		view.OnMouse = overlayMouse
		return coldRenderSnapshot{view: view, semantics: renderSnapshotSemantics{
			handler: handler, ownerEpoch: ownerEpoch, topology: overlayTopology,
		}}
	}
	if !m.helpOpen && m.settings == nil && !m.editor.IsOpen() && !m.adr.IsOpen() && !m.action.open() &&
		!m.issueImport.IsOpen() && !m.palette.IsOpen() && !m.detail.OwnsInput() {
		pointerActive := m.move.lifted != nil && m.move.lifted.fromMouse
		immutableHits := append([]boardHit(nil), hits...)
		surface := boardPointerSurface(immutableHits, pointerActive, m.pointerState)
		view.OnMouse = surface.Pointer
		overlayTopology = surface.Topology
		hits = immutableHits
		handler = renderHandlerBoard
	}
	semantics := renderSnapshotSemantics{handler: handler, topology: overlayTopology, hits: hits, columns: base.columns}
	return coldRenderSnapshot{view: view, hitRegions: hits, semantics: semantics}
}

func (m *Model) handleBoardFooterClick(key string) tea.Cmd {
	if m.move.lifted != nil && key != "q" {
		if m.move.saving {
			return nil
		}
		m.cancelCardMove("focus changed")
	}
	switch key {
	case "q":
		m.stopped = true
		m.reloadPending = false
		return tea.Quit
	case "?":
		m.helpOpen = true
	case "s":
		if m.settingsNew != nil {
			m.settings = m.settingsNew()
			m.settings.SetStyles(m.styles)
			return m.settings.Init()
		}
	case "a":
		if m.adr.Enabled() && !m.move.saving {
			return m.adr.Open()
		}
	case "i":
		if m.issueImport.Enabled() && !m.writeBusy() {
			return m.issueImport.Open()
		}
	case "n":
		if m.editor.Enabled() {
			m.editor.SetProjectDefault(m.projectDefault())
			return m.editor.OpenAdd(boardStatuses[m.boardView.column])
		}
	case "e":
		if m.editor.Enabled() {
			if task, ok := m.selectedTask(); ok {
				m.editor.SetProjectDefault(m.projectDefault())
				return m.editor.OpenEdit(task)
			}
		}
	case "c":
		if m.boardView.handleKey("c", m.filteredBoard()) == boardToggledCancelled {
			return m.queuePreferences()
		}
	}
	return nil
}

// openTaskRef opens the card a rendered kb://task reference addresses (issue
// #212). It is the path a click on a board card already takes: the board cursor
// follows the reference so the pane it opens is the pane esc returns from, and
// a card the current filter hides still opens - the reference is an explicit
// request for that card, not a board navigation.
//
// A reference to a card this board does not hold is a notice in the pane rather
// than a dismissal or a crash: the sequence number is card text, and card text
// is never trusted to name something that exists.
func (m *Model) openTaskRef(seq int) tea.Cmd {
	task, found := m.taskBySeq(seq)
	if !found {
		m.detail.NoticeUnknownTaskRef(seq)
		return nil
	}
	if task.ID == m.detail.TaskID() {
		return nil
	}
	m.boardView.focusTask(m.filteredBoard(), task.ID)
	m.detail.Resize(m.width, m.height)
	return m.detail.Open(task)
}

func (m Model) taskBySeq(seq int) (board.Task, bool) {
	if seq <= 0 {
		return board.Task{}, false
	}
	for _, task := range m.board.Tasks {
		if task.Seq == seq {
			return task, true
		}
	}
	return board.Task{}, false
}

func (m Model) taskByID(id string) (board.Task, bool) {
	for _, task := range m.board.Tasks {
		if task.ID == id {
			return task, true
		}
	}
	return board.Task{}, false
}

func (m Model) loadBoard(generation uint64) tea.Cmd {
	return func() tea.Msg {
		loaded, err := m.store.Board(m.user)
		return boardLoadedMsg{board: loaded, err: err, generation: generation}
	}
}

func (m Model) readDataVersion() tea.Cmd {
	return func() tea.Msg {
		version, err := m.watcher.DataVersion(m.readContext)
		return dataVersionMsg{version: version, err: err}
	}
}

func (m Model) schedulePoll() tea.Cmd {
	return theme.Tick(m.themeStyles().Timing.PollInterval, pollTickMsg{})
}
