package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

const (
	performanceWarmups = 10
	performanceSamples = 101
	startupSamples     = 31
)

var retainedPerformanceView tea.View

type performanceReport struct {
	SchemaVersion int                          `json:"schema_version"`
	GeneratedAt   time.Time                    `json:"generated_at"`
	Environment   performanceEnvironment       `json:"environment"`
	Scenarios     []performanceScenarioResult  `json:"scenarios"`
	Startup       []performanceStartupResult   `json:"startup"`
	ExternalGates performanceExternalGateState `json:"external_gates"`
}

type performanceEnvironment struct {
	Commit       string `json:"commit"`
	GOOS         string `json:"goos"`
	GOARCH       string `json:"goarch"`
	GoVersion    string `json:"go_version"`
	GoModVersion string `json:"go_mod_version"`
	GOMAXPROCS   int    `json:"gomaxprocs"`
	Kernel       string `json:"kernel,omitempty"`
	CPUModel     string `json:"cpu_model,omitempty"`
	TERM         string `json:"term,omitempty"`
	COLORTERM    string `json:"colorterm,omitempty"`
	Multiplexer  string `json:"multiplexer,omitempty"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
}

type performanceDistribution struct {
	P50NS int64 `json:"p50_ns"`
	P95NS int64 `json:"p95_ns"`
	P99NS int64 `json:"p99_ns"`
	MaxNS int64 `json:"max_ns"`
}

type performanceScenarioResult struct {
	Name                           string                        `json:"name"`
	Tasks                          int                           `json:"tasks"`
	Warmups                        int                           `json:"warmups"`
	Samples                        int                           `json:"samples"`
	Latency                        performanceDistribution       `json:"latency"`
	AllocationsPerOp               float64                       `json:"allocations_per_op"`
	AllocatedBytesPerOp            float64                       `json:"allocated_bytes_per_op"`
	PublicationAllocatedBytesMax   uint64                        `json:"publication_allocated_bytes_max"`
	RetainedPlanOwnedBytesEstimate uint64                        `json:"retained_plan_owned_bytes_estimate"`
	AcceptedMessages               uint64                        `json:"accepted_messages"`
	PublishedFrames                uint64                        `json:"published_frames"`
	DiscardedEvents                uint64                        `json:"discarded_events"`
	StaleWorkerResults             uint64                        `json:"stale_worker_results"`
	PublicationTrace               []performancePublicationStamp `json:"publication_trace"`
	ExpectedZeroFrames             bool                          `json:"expected_zero_frames"`
}

// performancePublicationStamp records the identity assigned at Update
// admission separately from the retained snapshot installed for that input.
// A held or burst sample emits one stamp per event, so later asynchronous and
// coalescing phases can prove exactly which accepted event was published.
type performancePublicationStamp struct {
	Sample                int    `json:"sample"`
	Event                 int    `json:"event"`
	Accepted              bool   `json:"accepted"`
	AcceptedMessageID     uint64 `json:"accepted_message_id"`
	Published             bool   `json:"published"`
	InstalledSnapshotID   uint64 `json:"installed_snapshot_id"`
	InstalledForMessageID uint64 `json:"installed_for_message_id"`
	Discarded             bool   `json:"discarded"`
	followUp              tea.Cmd
	geometry              tea.Cmd
}

type performanceStartupResult struct {
	Tasks    int                     `json:"tasks"`
	Samples  int                     `json:"samples"`
	Attempts int                     `json:"attempts"`
	Latency  performanceDistribution `json:"latency"`
}

type performanceExternalGateState struct {
	PTYTTIMeasured           bool   `json:"pty_tti_measured"`
	IdleCPURSSMeasured       bool   `json:"idle_cpu_rss_measured"`
	PhysicalTerminalRequired bool   `json:"physical_terminal_required"`
	Note                     string `json:"note"`
}

type performanceScenario struct {
	name               string
	expectedZeroFrames bool
	warmups            int
	samples            int
	newModel           func() Model
	prepare            func(*Model, int)
	admit              func(*Model, int) []performancePublicationStamp
	requireFollowUp    bool
	settleFollowUp     func(*testing.T, *Model, tea.Cmd)
}

// TestLargeBoardPerformanceHarness is the machine-readable process-local gate.
// It is deliberately opt-in: the cold renderer is still expensive, and making
// every unit-test run spend minutes proving that fact would be performance art
// in the least useful sense.
func TestLargeBoardPerformanceHarness(t *testing.T) {
	if os.Getenv("KB_PERF_RUN") != "1" && os.Getenv("KB_PERF_ACCEPT") != "1" {
		t.Skip("set KB_PERF_RUN=1 or KB_PERF_ACCEPT=1")
	}
	previousProcs := runtime.GOMAXPROCS(8)
	t.Cleanup(func() { runtime.GOMAXPROCS(previousProcs) })

	report := performanceReport{
		SchemaVersion: 3,
		GeneratedAt:   time.Now().UTC(),
		Environment:   currentPerformanceEnvironment(),
		ExternalGates: performanceExternalGateState{
			PhysicalTerminalRequired: true,
			Note:                     "PTY TTI, settled pidstat CPU/RSS, held-input feel, pointer feel, resize, overlays, and terminal restoration are recorded by the later external/physical-terminal gate.",
		},
	}
	for _, count := range selectedPerformanceCorpora(t) {
		assertPerformanceOracle(t, count)
		for _, scenario := range selectedPerformanceScenarios(t, count) {
			report.Scenarios = append(report.Scenarios, runPerformanceScenario(t, count, scenario))
		}
		if os.Getenv("KB_PERF_SKIP_STARTUP") != "1" {
			report.Startup = append(report.Startup, measureFreshProcessStartup(t, count))
		}
	}

	path := writePerformanceReport(t, report)
	t.Logf("performance report: %s", path)
	if os.Getenv("KB_PERF_ACCEPT") == "1" {
		validatePerformanceReport(t, report)
	}
}

func TestPerformanceHarnessRecordsHeldAndBurstPublicationIdentity(t *testing.T) {
	scenarios := performanceScenarios(120)
	for _, test := range []struct {
		name         string
		wantEvents   int
		wantAccepted int
	}{
		{name: "held_navigation", wantEvents: 8, wantAccepted: 2},
		{name: "refresh_25_task_burst", wantEvents: 25, wantAccepted: 25},
	} {
		var scenario *performanceScenario
		for index := range scenarios {
			if scenarios[index].name == test.name {
				scenario = &scenarios[index]
				break
			}
		}
		if scenario == nil {
			t.Fatalf("scenario %q not found", test.name)
		}
		model := scenario.newModel()
		if scenario.prepare != nil {
			scenario.prepare(&model, 0)
		}
		before := model.RenderPlanStats()
		stamps := scenario.admit(&model, 0)
		settlePerformanceStampCommands(t, &model, *scenario, stamps)
		if len(stamps) != test.wantEvents {
			t.Fatalf("%s emitted %d identity stamps, want %d", test.name, len(stamps), test.wantEvents)
		}
		wantMessage := before.AcceptedMessageID
		wantSnapshot := before.InstalledSnapshotID
		accepted := 0
		for index, stamp := range stamps {
			if stamp.Accepted {
				accepted++
				wantMessage++
				wantSnapshot++
				if !stamp.Published || stamp.AcceptedMessageID != wantMessage ||
					stamp.InstalledSnapshotID != wantSnapshot || stamp.InstalledForMessageID != wantMessage {
					t.Fatalf("%s accepted event %d publication = %+v, want snapshot %d for message %d",
						test.name, index, stamp, wantSnapshot, wantMessage)
				}
				continue
			}
			if !stamp.Discarded || stamp.Published || stamp.AcceptedMessageID != wantMessage ||
				stamp.InstalledSnapshotID != wantSnapshot {
				t.Fatalf("%s discarded event %d changed publication identity: %+v", test.name, index, stamp)
			}
		}
		if accepted != test.wantAccepted {
			t.Fatalf("%s accepted %d events, want %d", test.name, accepted, test.wantAccepted)
		}
	}
}

func TestPerformanceMemoryGateUsesRetainedPlanEstimate(t *testing.T) {
	goVersion := moduleGoVersion()
	report := performanceReport{
		Environment: performanceEnvironment{
			GoVersion:    "go" + goVersion,
			GoModVersion: goVersion,
		},
		Scenarios: []performanceScenarioResult{{
			Name:                           "separate-memory-contracts",
			Tasks:                          1000,
			PublicationAllocatedBytesMax:   1 << 30,
			RetainedPlanOwnedBytesEstimate: 63 << 20,
		}},
		Startup: []performanceStartupResult{{
			Tasks: 120,
		}},
	}
	validatePerformanceReport(t, report)
}

func TestPerformanceAdmissionPreservesNonWorkerCommands(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*Model)
		message tea.Msg
		want    func(tea.Msg) bool
	}{
		{
			name:    "exact action",
			message: tea.KeyPressMsg{Code: 'q', Text: "q"},
			want: func(message tea.Msg) bool {
				_, ok := message.(tea.QuitMsg)
				return ok
			},
		},
		{
			name: "poll",
			prepare: func(model *Model) {
				model.applyStyles(theme.NewWith(model.isDark, theme.TimingCollapsed))
				model.watcher = stubVersionReader{version: 1}
				model.haveVersion = true
				model.dataVersion = 1
				model.pollStarted = true
				model.rebuildRenderPlan(renderImpactAll)
			},
			message: dataVersionMsg{version: 1},
			want: func(message tea.Msg) bool {
				_, ok := message.(pollTickMsg)
				return ok
			},
		},
		{
			name: "reload",
			prepare: func(model *Model) {
				model.loading = true
				model.reloadPending = true
			},
			message: boardLoadedMsg{board: performanceBoard(17)},
			want: func(message tea.Msg) bool {
				_, ok := message.(boardLoadedMsg)
				return ok
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := performanceModel(17, "", performanceWidth, performanceHeight)
			if test.prepare != nil {
				test.prepare(&model)
			}
			stamps := admitPerformanceMessage(&model, test.message)
			if len(stamps) != 1 || stamps[0].followUp == nil {
				t.Fatalf("non-worker command was dropped: stamps=%+v", stamps)
			}
			if message := stamps[0].followUp(); !test.want(message) {
				t.Fatalf("preserved command returned %T", message)
			}
		})
	}
}

func TestPerformanceScenarioAccountsForGeometrySettlement(t *testing.T) {
	scenario := performanceScenario{
		name:     "geometry_settlement_accounting",
		warmups:  1,
		samples:  3,
		newModel: func() Model { return performanceModel(37, "", performanceWidth, performanceHeight) },
		admit: func(model *Model, _ int) []performancePublicationStamp {
			stamps := admitPerformanceMessage(model, tea.WindowSizeMsg{Width: 100, Height: 30})
			// Supersede the in-flight slice before it returns. Settlement must count
			// both the stale result and the replacement chain it starts.
			stamps = append(stamps, admitPerformanceMessage(model,
				tea.WindowSizeMsg{Width: performanceWidth, Height: performanceHeight})...)
			return append(stamps, admitPerformanceMessage(model,
				tea.WindowSizeMsg{Width: performanceWidth, Height: 31})...)
		},
	}
	result := runPerformanceScenario(t, 37, scenario)
	if result.StaleWorkerResults != uint64(scenario.samples) {
		t.Fatalf("stale worker results = %d, want %d sample settlements",
			result.StaleWorkerResults, scenario.samples)
	}
	if result.PublishedFrames <= result.AcceptedMessages {
		t.Fatalf("published frames = %d, accepted inputs = %d; settlement frames were omitted",
			result.PublishedFrames, result.AcceptedMessages)
	}
}

func TestPerformanceFilterScenariosDiscardOnlyTheirBlinkFollowUp(t *testing.T) {
	for _, name := range []string{"filter_to_17", "filter_to_empty"} {
		t.Run(name, func(t *testing.T) {
			var scenario *performanceScenario
			for _, candidate := range performanceScenarios(120) {
				if candidate.name == name {
					selected := candidate
					scenario = &selected
					break
				}
			}
			if scenario == nil {
				t.Fatalf("scenario %q not found", name)
			}
			scenario.warmups = 1
			scenario.samples = 2
			result := runPerformanceScenario(t, 120, *scenario)
			if result.AcceptedMessages != uint64(scenario.samples) {
				t.Fatalf("accepted messages = %d, want %d", result.AcceptedMessages, scenario.samples)
			}
		})
	}
}

func TestPerformanceFilterBlinkSettlementDoesNotExecuteTimer(t *testing.T) {
	model := performanceModel(17, "", performanceWidth, performanceHeight)
	_ = model.filter.focusText()
	model.filter.input.SetValue("keep17")
	executed := false
	settlePerformanceFilterBlink(t, &model, func() tea.Msg {
		executed = true
		return nil
	})
	if executed {
		t.Fatal("filter blink timer was synchronously awaited")
	}
}

func TestPointerResolverCostIsBoundedByVisibleHitSnapshot(t *testing.T) {
	const visibleHitBudget = 512
	var baseline uint64
	for _, count := range []int{120, 500, 1000} {
		t.Run(fmt.Sprintf("tasks_%04d", count), func(t *testing.T) {
			model := performanceModel(count, "", performanceWidth, performanceHeight)
			stats := model.RenderPlanStats()
			if stats.HitRegions == 0 || stats.HitRegions > visibleHitBudget {
				t.Fatalf("resolver hit snapshot = %d regions, budget 1..%d", stats.HitRegions, visibleHitBudget)
			}
			if got := uint64(len(model.current.semantics.hits)); got != stats.HitRegions {
				t.Fatalf("resolver instrumentation = %d regions, immutable visible hits = %d", stats.HitRegions, got)
			}
			if count == 120 {
				baseline = stats.HitRegions
			} else if stats.HitRegions > baseline+16 {
				t.Fatalf("resolver regions grew with offscreen tasks: baseline=%d current=%d", baseline, stats.HitRegions)
			}

			handler := model.View().OnMouse
			if handler == nil {
				t.Fatal("last-flushed board installed no resolver")
			}
			raw := tea.MouseMotionMsg{X: 0, Y: 0, Button: tea.MouseNone}
			if command := handler(raw); command != nil {
				t.Fatal("resolver escaped the synchronous mailbox")
			}
			before := model.RenderPlanStats()
			next, commands := model.updateWithCommands(raw)
			if commands.followUp != nil || next.RenderPlanStats().PublishedFrames-before.PublishedFrames > 1 {
				t.Fatal("one raw pointer observation produced more than one semantic publication")
			}
		})
	}
}

func performanceScenarios(count int) []performanceScenario {
	base := func(filter string) Model {
		return performanceModel(count, filter, performanceWidth, performanceHeight)
	}

	var hoverX, hoverY int
	hoverModel := func() Model {
		model := base("")
		_, hits := model.renderBoard()
		for _, hit := range hits {
			if hit.kind == boardHitDefault && hit.taskID != "" {
				hoverX, hoverY = hit.x0+1, hit.y0
				break
			}
		}
		return model
	}

	baseBoard := performanceBoard(count)
	matching := appendPerformanceTasks(baseBoard, 1, true)
	nonmatching := appendPerformanceTasks(baseBoard, 1, false)
	burst := make([]board.Board, 25)
	for index := range burst {
		burst[index] = appendPerformanceTasks(baseBoard, index+1, true)
	}
	resetBoard := func(filter string) func(*Model, int) {
		return func(model *Model, _ int) {
			model.board = baseBoard
			model.filter.input.SetValue(filter)
			model.boardView.normalizeSelection(model.filteredBoard())
			model.rebuildRenderPlan(renderImpactAll)
		}
	}
	var clampedAdmission *keyboardAdmission
	var clampedClock stepClock
	var heldAdmission *keyboardAdmission
	var heldClock stepClock

	return []performanceScenario{
		{
			name: "useful_navigation",
			newModel: func() Model {
				model := base("")
				model.boardView.rows[0] = min(1, taskCount(model.board, board.StatusTodo)-1)
				model.rebuildRenderPlan(renderImpactAll)
				return model
			},
			admit: func(model *Model, sample int) []performancePublicationStamp {
				code := tea.KeyDown
				if sample%2 == 1 {
					code = tea.KeyUp
				}
				return admitPerformanceMessage(model, tea.KeyPressMsg{Code: code})
			},
		},
		{
			name:               "clamped_navigation",
			expectedZeroFrames: true,
			newModel: func() Model {
				model := base("")
				model.boardView.rows[0] = taskCount(model.board, board.StatusTodo) - 1
				model.rebuildRenderPlan(renderImpactAll)
				clampedClock.at = time.Unix(0, 0)
				clampedAdmission = newKeyboardAdmission(clampedClock.now, model.inputAdmission, model.themeStyles().Timing)
				return model
			},
			admit: func(model *Model, _ int) []performancePublicationStamp {
				return admitPerformanceFilteredMessage(model, clampedAdmission, tea.KeyPressMsg{Code: tea.KeyDown})
			},
		},
		{
			name: "held_navigation",
			newModel: func() Model {
				model := base("")
				heldClock.at = time.Unix(0, 0)
				heldAdmission = newKeyboardAdmission(heldClock.now, model.inputAdmission, model.themeStyles().Timing)
				return model
			},
			prepare: func(model *Model, sample int) {
				heldAdmission.clear()
				heldClock.at = time.Unix(int64(sample+1), 0)
				model.boardView.rows[0] = min(1, taskCount(model.board, board.StatusTodo)-1)
				model.rebuildRenderPlan(renderImpactAll)
			},
			admit: func(model *Model, _ int) []performancePublicationStamp {
				stamps := make([]performancePublicationStamp, 0, 8)
				stamps = append(stamps, admitPerformanceFilteredMessage(model, heldAdmission,
					tea.KeyPressMsg{Code: tea.KeyDown})...)
				for range 7 {
					heldClock.advance(10 * time.Millisecond)
					stamps = append(stamps, admitPerformanceFilteredMessage(model, heldAdmission,
						tea.KeyPressMsg{Code: tea.KeyDown, IsRepeat: true})...)
				}
				return stamps
			},
		},
		{
			name:            "useful_wheel",
			newModel:        func() Model { return base("") },
			requireFollowUp: true,
			settleFollowUp:  settlePerformancePointerScrollLinger,
			prepare: func(model *Model, _ int) {
				model.pointerAdmission.resetCadence()
				model.boardView.scrolls[0] = 0
				model.rebuildRenderPlan(renderImpactAll)
			},
			admit: func(model *Model, _ int) []performancePublicationStamp {
				return admitPerformancePointer(model, tea.MouseWheelMsg{X: 10, Y: 6, Button: tea.MouseWheelDown})
			},
		},
		{
			name:               "saturated_wheel",
			expectedZeroFrames: true,
			newModel:           func() Model { return base("") },
			prepare: func(model *Model, _ int) {
				model.boardView.scrolls[0] = 0
				model.rebuildRenderPlan(renderImpactAll)
			},
			admit: func(model *Model, _ int) []performancePublicationStamp {
				return admitPerformancePointer(model, tea.MouseWheelMsg{X: 10, Y: 6, Button: tea.MouseWheelUp})
			},
		},
		{
			name:     "hover_change",
			newModel: hoverModel,
			prepare: func(model *Model, _ int) {
				model.pointerAdmission.resetCadence()
				model.pointerState = pointerZeroState()
				model.rebuildRenderPlan(renderImpactAll)
			},
			admit: func(model *Model, _ int) []performancePublicationStamp {
				return admitPerformancePointer(model, tea.MouseMotionMsg{X: hoverX, Y: hoverY, Button: tea.MouseNone})
			},
		},
		{
			name:               "same_target_hover",
			expectedZeroFrames: true,
			newModel:           hoverModel,
			prepare: func(model *Model, _ int) {
				_ = admitPerformancePointer(model, tea.MouseMotionMsg{X: hoverX, Y: hoverY, Button: tea.MouseNone})
			},
			admit: func(model *Model, _ int) []performancePublicationStamp {
				return admitPerformancePointer(model, tea.MouseMotionMsg{X: hoverX, Y: hoverY, Button: tea.MouseNone})
			},
		},
		{
			name:     "filter_to_17",
			newModel: func() Model { return base("") },
			prepare: func(model *Model, _ int) {
				model.filter.input.SetValue("")
				_ = model.filter.focusText()
				model.rebuildRenderPlan(renderImpactAll)
			},
			admit: func(model *Model, _ int) []performancePublicationStamp {
				return admitPerformanceMessage(model, tea.KeyPressMsg{Code: 'k', Text: "keep17"})
			},
			requireFollowUp: true,
			settleFollowUp:  settlePerformanceFilterBlink,
		},
		{
			name:     "filter_to_empty",
			newModel: func() Model { return base("") },
			prepare: func(model *Model, _ int) {
				model.filter.input.SetValue("")
				_ = model.filter.focusText()
				model.rebuildRenderPlan(renderImpactAll)
			},
			admit: func(model *Model, _ int) []performancePublicationStamp {
				return admitPerformanceMessage(model, tea.KeyPressMsg{Code: 'z', Text: "matches-nothing"})
			},
			requireFollowUp: true,
			settleFollowUp:  settlePerformanceFilterBlink,
		},
		{
			name:     "resize",
			newModel: func() Model { return base("") },
			admit: func(model *Model, sample int) []performancePublicationStamp {
				width, height := 100, 30
				if sample%2 == 1 {
					width, height = performanceWidth, performanceHeight
				}
				return admitPerformanceMessage(model, tea.WindowSizeMsg{Width: width, Height: height})
			},
		},
		{
			name:     "overlay_open",
			newModel: func() Model { return base("") },
			prepare: func(model *Model, _ int) {
				model.helpOpen = false
				model.rebuildRenderPlan(renderImpactAll)
			},
			admit: func(model *Model, _ int) []performancePublicationStamp {
				return admitPerformanceMessage(model, tea.KeyPressMsg{Code: '?'})
			},
		},
		{
			name:               "unchanged_poll",
			expectedZeroFrames: true,
			newModel: func() Model {
				model := base("")
				model.applyStyles(theme.NewWith(model.isDark, theme.TimingCollapsed))
				model.watcher = stubVersionReader{version: 1}
				model.haveVersion = true
				model.dataVersion = 1
				model.rebuildRenderPlan(renderImpactAll)
				return model
			},
			admit: func(model *Model, _ int) []performancePublicationStamp {
				return admitPerformanceMessage(model, dataVersionMsg{version: 1})
			},
			requireFollowUp: true,
			settleFollowUp: func(t *testing.T, _ *Model, command tea.Cmd) {
				t.Helper()
				if _, ok := command().(pollTickMsg); !ok {
					t.Fatal("unchanged poll did not preserve its successor timer")
				}
			},
		},
		{
			name:     "refresh_matching_insert",
			newModel: func() Model { return base("keep17") },
			prepare:  resetBoard("keep17"),
			admit: func(model *Model, _ int) []performancePublicationStamp {
				return admitPerformanceMessage(model, boardLoadedMsg{board: matching})
			},
		},
		{
			name:               "refresh_nonmatching_insert",
			expectedZeroFrames: true,
			newModel:           func() Model { return base("keep17") },
			prepare:            resetBoard("keep17"),
			admit: func(model *Model, _ int) []performancePublicationStamp {
				return admitPerformanceMessage(model, boardLoadedMsg{board: nonmatching})
			},
		},
		{
			name:     "refresh_25_task_burst",
			newModel: func() Model { return base("keep17") },
			prepare:  resetBoard("keep17"),
			admit: func(model *Model, _ int) []performancePublicationStamp {
				stamps := make([]performancePublicationStamp, 0, len(burst))
				for _, snapshot := range burst {
					stamps = append(stamps, admitPerformanceMessage(model, boardLoadedMsg{board: snapshot})...)
				}
				return stamps
			},
		},
	}
}

func runPerformanceScenario(t *testing.T, count int, scenario performanceScenario) performanceScenarioResult {
	t.Helper()
	warmups, samples := scenario.warmups, scenario.samples
	if warmups <= 0 {
		warmups = performanceWarmups
	}
	if samples <= 0 {
		samples = performanceSamples
	}
	model := scenario.newModel()
	for sample := range warmups {
		if scenario.prepare != nil {
			scenario.prepare(&model, sample)
		}
		stamps := scenario.admit(&model, sample)
		settlePerformanceStampCommands(t, &model, scenario, stamps)
	}

	runtime.GC()
	latencies := make([]time.Duration, samples)
	var allocations, allocatedBytes, maximumPublicationBytes, maximumRetainedPlanBytes uint64
	var accepted, frames, discarded, stale uint64
	var trace []performancePublicationStamp
	for sample := range samples {
		if scenario.prepare != nil {
			scenario.prepare(&model, sample)
		}
		var beforeMemory, afterMemory runtime.MemStats
		runtime.ReadMemStats(&beforeMemory)
		beforeStats := model.RenderPlanStats()
		started := time.Now()
		stamps := scenario.admit(&model, sample)
		settlePerformanceStampCommands(t, &model, scenario, stamps)
		latencies[sample] = time.Since(started)
		afterStats := model.RenderPlanStats()
		runtime.ReadMemStats(&afterMemory)
		allocations += afterMemory.Mallocs - beforeMemory.Mallocs
		publicationBytes := afterMemory.TotalAlloc - beforeMemory.TotalAlloc
		allocatedBytes += publicationBytes
		maximumPublicationBytes = max(maximumPublicationBytes, publicationBytes)
		maximumRetainedPlanBytes = max(maximumRetainedPlanBytes, afterStats.RetainedPlanOwnedBytesEstimate)
		frames += afterStats.PublishedFrames - beforeStats.PublishedFrames
		stale += afterStats.StaleWorkerResults - beforeStats.StaleWorkerResults
		var observedDiscard uint64
		for event := range stamps {
			stamps[event].Sample = sample
			stamps[event].Event = event
			if stamps[event].Accepted {
				accepted++
			}
			if stamps[event].Discarded {
				observedDiscard++
			}
		}
		trace = append(trace, stamps...)
		instrumentedDiscard := afterStats.DiscardedEvents - beforeStats.DiscardedEvents
		discarded += max(instrumentedDiscard, observedDiscard)
	}

	if got, want := model.View(), model.renderColdView(); got.Content != want.Content ||
		got.AltScreen != want.AltScreen || got.MouseMode != want.MouseMode {
		t.Errorf("%s/%d retained frame differs from cold renderer", scenario.name, count)
	}
	return performanceScenarioResult{
		Name:                           scenario.name,
		Tasks:                          count,
		Warmups:                        warmups,
		Samples:                        samples,
		Latency:                        performanceDurations(latencies),
		AllocationsPerOp:               float64(allocations) / float64(samples),
		AllocatedBytesPerOp:            float64(allocatedBytes) / float64(samples),
		PublicationAllocatedBytesMax:   maximumPublicationBytes,
		RetainedPlanOwnedBytesEstimate: maximumRetainedPlanBytes,
		AcceptedMessages:               accepted,
		PublishedFrames:                frames,
		DiscardedEvents:                discarded,
		StaleWorkerResults:             stale,
		PublicationTrace:               trace,
		ExpectedZeroFrames:             scenario.expectedZeroFrames,
	}
}

func admitPerformanceMessage(model *Model, message tea.Msg) []performancePublicationStamp {
	before := model.RenderPlanStats()
	updated, commands := model.updateWithCommands(message)
	*model = updated
	retainedPerformanceView = model.View()
	after := model.RenderPlanStats()
	return []performancePublicationStamp{{
		Accepted:              after.AcceptedMessageID != before.AcceptedMessageID,
		AcceptedMessageID:     after.AcceptedMessageID,
		Published:             after.InstalledSnapshotID != before.InstalledSnapshotID,
		InstalledSnapshotID:   after.InstalledSnapshotID,
		InstalledForMessageID: after.InstalledForMessageID,
		followUp:              commands.followUp,
		geometry:              commands.geometry,
	}}
}

func settlePerformanceStampCommands(
	t *testing.T,
	model *Model,
	scenario performanceScenario,
	stamps []performancePublicationStamp,
) {
	t.Helper()
	for index := range stamps {
		followUp, geometry := stamps[index].followUp, stamps[index].geometry
		stamps[index].followUp = nil
		stamps[index].geometry = nil
		if followUp != nil {
			if scenario.settleFollowUp == nil {
				t.Fatalf("%s returned an unexpected non-worker command", scenario.name)
			}
			scenario.settleFollowUp(t, model, followUp)
		} else if scenario.requireFollowUp {
			t.Fatalf("%s did not return its required non-worker command", scenario.name)
		}
		settlePerformanceGeometryCommand(t, model, geometry)
	}
	retainedPerformanceView = model.View()
}

func settlePerformanceFilterBlink(t *testing.T, model *Model, command tea.Cmd) {
	t.Helper()
	if command == nil || model.filter.focus != filterText || !model.filter.input.Focused() ||
		!model.filter.input.VirtualCursor() || model.savePreferences != nil {
		t.Fatalf("filter follow-up is not an isolated virtual-cursor blink: focus=%d focused=%t virtual=%t preferences=%t",
			model.filter.focus, model.filter.input.Focused(), model.filter.input.VirtualCursor(), model.savePreferences != nil)
	}
	// The cursor blink is a non-semantic 500ms timer. Production retains it;
	// the interaction harness explicitly ends this scenario at the published
	// filter frame instead of serially awaiting a cosmetic deadline.
}

func settlePerformancePointerScrollLinger(t *testing.T, model *Model, command tea.Cmd) {
	t.Helper()
	if command == nil {
		t.Fatal("useful wheel did not retain its scroll-linger deadline")
	}
	lingering := false
	for column := range boardStatuses {
		lingering = lingering || model.scroll.lingering(column)
	}
	if !lingering {
		t.Fatal("wheel follow-up is not an active scroll-linger deadline")
	}
}

func settlePerformanceGeometryCommand(t *testing.T, model *Model, command tea.Cmd) {
	t.Helper()
	for steps := 0; command != nil; steps++ {
		if steps > 4096 {
			t.Fatal("performance geometry command did not settle")
		}
		message := command()
		if _, ok := message.(geometryBatchMsg); !ok {
			t.Fatalf("geometry command returned non-worker message %T", message)
		}
		updated, commands := model.updateWithCommands(message)
		*model = updated
		if commands.followUp != nil {
			t.Fatal("geometry settlement produced a non-worker command")
		}
		command = commands.geometry
	}
}

func settlePerformanceCommands(t *testing.T, model *Model, commands []tea.Cmd) {
	t.Helper()
	queue := append([]tea.Cmd(nil), commands...)
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 4096 {
			t.Fatal("deferred performance commands did not settle")
		}
		command := queue[0]
		queue = queue[1:]
		if command == nil {
			continue
		}
		message := command()
		if batch, ok := message.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		updated, next := model.Update(message)
		*model = updated.(Model)
		if next != nil {
			queue = append(queue, next)
		}
	}
}

func admitPerformanceFilteredMessage(
	model *Model,
	admission *keyboardAdmission,
	message tea.Msg,
) []performancePublicationStamp {
	filtered := admission.Filter(*model, message)
	if filtered == nil {
		return discardedPerformanceStamp(model)
	}
	return admitPerformanceMessage(model, filtered)
}

func admitPerformancePointer(model *Model, message tea.MouseMsg) []performancePublicationStamp {
	handler := model.View().OnMouse
	if handler == nil {
		return discardedPerformanceStamp(model)
	}
	command := handler(message)
	if command != nil {
		panic("pointer resolver escaped the synchronous mailbox")
	}
	return admitPerformanceMessage(model, message)
}

func discardedPerformanceStamp(model *Model) []performancePublicationStamp {
	stats := model.RenderPlanStats()
	return []performancePublicationStamp{{
		AcceptedMessageID:     stats.AcceptedMessageID,
		InstalledSnapshotID:   stats.InstalledSnapshotID,
		InstalledForMessageID: stats.InstalledForMessageID,
		Discarded:             true,
	}}
}

func appendPerformanceTasks(current board.Board, count int, matching bool) board.Board {
	next := current
	next.Tasks = append([]board.Task(nil), current.Tasks...)
	for i := range count {
		marker := "ordinary"
		if matching {
			marker = "keep17"
		}
		next.Tasks = append(next.Tasks, board.Task{
			ID:       fmt.Sprintf("refresh-%t-%02d", matching, i),
			Seq:      len(next.Tasks) + 1,
			Title:    marker + " live refresh task",
			Desc:     "arrived while the TUI was open",
			Status:   board.StatusTodo,
			Prio:     2,
			Position: taskCount(next, board.StatusTodo),
		})
	}
	return next
}

// pointerZeroState avoids teaching the performance fixture the internals of
// pointer.State while still giving every hover-change sample a clean target.
func pointerZeroState() (zero pointer.State) { return zero }

func assertPerformanceOracle(t *testing.T, count int) {
	t.Helper()
	for _, size := range [][2]int{{performanceWidth, performanceHeight}, {100, 30}, {80, 24}} {
		for _, filter := range []string{"", "keep17", "matches-nothing"} {
			model := performanceModel(count, filter, size[0], size[1])
			got, want := model.View(), model.renderColdView()
			if got.Content != want.Content || got.AltScreen != want.AltScreen || got.MouseMode != want.MouseMode {
				t.Errorf("oracle mismatch: tasks=%d size=%dx%d filter=%q", count, size[0], size[1], filter)
			}
		}
	}
}

func TestPerformanceColdOracleDefaultSizeFilterParity(t *testing.T) {
	for _, filter := range []string{"keep17", "matches-nothing"} {
		t.Run(filter, func(t *testing.T) {
			assertPerformanceColdOracleParity(t, performanceModel(120, filter, 80, 24))
		})
	}
}

func TestPerformanceFilterUpdatePublishesAtDefaultSize(t *testing.T) {
	fixture := performanceBoard(0)
	model := NewModel(stubBoardReader{board: fixture}, nil, "perf")
	updated, _ := model.Update(boardLoadedMsg{board: fixture})
	model = updated.(Model)
	model, _ = applyPerformanceFilter(model, "k")
	updated, _ = model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	assertPerformanceColdOracleParity(t, updated.(Model))
}

func TestPerformanceDirectMutationDoesNotTurnSameSizeResizeIntoPublication(t *testing.T) {
	fixture := performanceBoard(0)
	model := NewModel(stubBoardReader{board: fixture}, nil, "perf")
	updated, _ := model.Update(boardLoadedMsg{board: fixture})
	model = updated.(Model)
	beforeView := model.View()
	beforeStats := model.RenderPlanStats()

	// Direct sub-model mutation is not a supported publication path. This is a
	// guard for test fixtures: a no-op resize must not conceal their bypass by
	// manufacturing a frame.
	model.filter.input.SetValue("k")
	if cold := model.renderColdView(); cold.Content == beforeView.Content {
		t.Fatal("direct mutation did not make the cold model observably different")
	}
	updated, _ = model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = updated.(Model)
	afterStats := model.RenderPlanStats()
	if afterStats.PublishedFrames != beforeStats.PublishedFrames {
		t.Fatalf("same-size resize published %d frames, want 0", afterStats.PublishedFrames-beforeStats.PublishedFrames)
	}
	if after := model.View(); after.Content != beforeView.Content {
		t.Fatal("same-size resize published an out-of-band direct mutation")
	}
}

func assertPerformanceColdOracleParity(t *testing.T, model Model) {
	t.Helper()
	got, want := model.View(), model.renderColdView()
	if got.Content == want.Content && got.AltScreen == want.AltScreen && got.MouseMode == want.MouseMode {
		return
	}

	gotLines := strings.Split(plain(got.Content), "\n")
	wantLines := strings.Split(plain(want.Content), "\n")
	limit := min(len(gotLines), len(wantLines))
	for row := range limit {
		if gotLines[row] != wantLines[row] {
			t.Fatalf("cold-oracle mismatch at row %d: retained=%q cold=%q metadata retained=(alt=%t mouse=%d) cold=(alt=%t mouse=%d)",
				row, gotLines[row], wantLines[row], got.AltScreen, got.MouseMode, want.AltScreen, want.MouseMode)
		}
	}
	t.Fatalf("cold-oracle mismatch after %d shared rows: retained rows=%d cold rows=%d metadata retained=(alt=%t mouse=%d) cold=(alt=%t mouse=%d)",
		limit, len(gotLines), len(wantLines), got.AltScreen, got.MouseMode, want.AltScreen, want.MouseMode)
}

func performanceDurations(values []time.Duration) performanceDistribution {
	ordered := append([]time.Duration(nil), values...)
	slices.Sort(ordered)
	at := func(percent int) int64 {
		index := (len(ordered)*percent + 99) / 100
		return ordered[min(max(index-1, 0), len(ordered)-1)].Nanoseconds()
	}
	return performanceDistribution{
		P50NS: at(50), P95NS: at(95), P99NS: at(99), MaxNS: ordered[len(ordered)-1].Nanoseconds(),
	}
}

func selectedPerformanceCorpora(t *testing.T) []int {
	t.Helper()
	value := strings.TrimSpace(os.Getenv("KB_PERF_CORPORA"))
	if value == "" {
		return performanceCorpora[:]
	}
	var corpora []int
	for _, field := range strings.Split(value, ",") {
		count, err := strconv.Atoi(strings.TrimSpace(field))
		if err != nil || count < 1 {
			t.Fatalf("KB_PERF_CORPORA contains invalid task count %q", field)
		}
		corpora = append(corpora, count)
	}
	return corpora
}

func selectedPerformanceScenarios(t *testing.T, count int) []performanceScenario {
	t.Helper()
	wantedText := strings.TrimSpace(os.Getenv("KB_PERF_SCENARIOS"))
	if wantedText == "" {
		return performanceScenarios(count)
	}
	wanted := make(map[string]struct{})
	for _, name := range strings.Split(wantedText, ",") {
		if name = strings.TrimSpace(name); name != "" {
			wanted[name] = struct{}{}
		}
	}
	var selected []performanceScenario
	for _, scenario := range performanceScenarios(count) {
		if _, ok := wanted[scenario.name]; ok {
			selected = append(selected, scenario)
			delete(wanted, scenario.name)
		}
	}
	if len(wanted) != 0 {
		unknown := make([]string, 0, len(wanted))
		for name := range wanted {
			unknown = append(unknown, name)
		}
		slices.Sort(unknown)
		t.Fatalf("KB_PERF_SCENARIOS contains unknown scenarios: %s", strings.Join(unknown, ","))
	}
	if len(selected) == 0 {
		t.Fatal("KB_PERF_SCENARIOS selected no scenarios")
	}
	return selected
}

func currentPerformanceEnvironment() performanceEnvironment {
	environment := performanceEnvironment{
		Commit:       commandOutput("git", "rev-parse", "HEAD"),
		GOOS:         runtime.GOOS,
		GOARCH:       runtime.GOARCH,
		GoVersion:    runtime.Version(),
		GoModVersion: moduleGoVersion(),
		GOMAXPROCS:   runtime.GOMAXPROCS(0),
		Kernel:       commandOutput("uname", "-srvm"),
		CPUModel:     cpuModel(),
		TERM:         os.Getenv("TERM"),
		COLORTERM:    os.Getenv("COLORTERM"),
		Width:        performanceWidth,
		Height:       performanceHeight,
	}
	switch {
	case os.Getenv("TMUX") != "":
		environment.Multiplexer = "tmux"
	case os.Getenv("STY") != "":
		environment.Multiplexer = "screen"
	}
	return environment
}

func commandOutput(name string, args ...string) string {
	output, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func cpuModel() string {
	content, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(content), "\n") {
		name, value, found := strings.Cut(line, ":")
		if found && strings.TrimSpace(name) == "model name" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func moduleGoVersion() string {
	content, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(content), "\n") {
		if version, found := strings.CutPrefix(strings.TrimSpace(line), "go "); found {
			return version
		}
	}
	return ""
}

func writePerformanceReport(t *testing.T, report performanceReport) string {
	t.Helper()
	path := os.Getenv("KB_PERF_REPORT")
	if path == "" {
		path = filepath.Join(t.TempDir(), "tui-performance.json")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	content = append(content, '\n')
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func validatePerformanceReport(t *testing.T, report performanceReport) {
	t.Helper()
	if report.Environment.GoModVersion == "" ||
		strings.TrimPrefix(report.Environment.GoVersion, "go") != report.Environment.GoModVersion {
		t.Errorf("performance run used %s, go.mod requires go%s",
			report.Environment.GoVersion, report.Environment.GoModVersion)
	}
	for _, result := range report.Scenarios {
		if result.Tasks < 120 {
			continue
		}
		if result.Latency.P95NS >= (50*time.Millisecond).Nanoseconds() ||
			result.Latency.P99NS >= (100*time.Millisecond).Nanoseconds() {
			t.Errorf("%s/%d latency p95=%s p99=%s exceeds budget", result.Name, result.Tasks,
				time.Duration(result.Latency.P95NS), time.Duration(result.Latency.P99NS))
		}
		if result.ExpectedZeroFrames && result.PublishedFrames != 0 {
			t.Errorf("%s/%d published %d frames, want zero", result.Name, result.Tasks, result.PublishedFrames)
		}
		// The 64 MiB budget applies only to installed plan-owned data. Transient
		// TotalAlloc remains diagnostic, and whole-process RSS belongs to the
		// external gate; conflating the three produces very confident nonsense.
		if result.Tasks == 1000 && result.RetainedPlanOwnedBytesEstimate > 64<<20 {
			t.Errorf("%s retained plan estimate is %d bytes, want no more than 64 MiB",
				result.Name, result.RetainedPlanOwnedBytesEstimate)
		}
	}
	if len(report.Startup) == 0 {
		t.Error("fresh-process startup was not measured")
	}
	for _, result := range report.Startup {
		if result.Tasks >= 120 && result.Latency.P95NS > (700*time.Millisecond).Nanoseconds() {
			t.Errorf("startup/%d p95=%s exceeds 700ms", result.Tasks, time.Duration(result.Latency.P95NS))
		}
	}
}

// TestLargeBoardStartupChild is invoked by the harness in a fresh test process.
// The sentinel is parsed by the parent; ordinary test runs skip it.
func TestLargeBoardStartupChild(t *testing.T) {
	databasePath := os.Getenv("KB_PERF_CHILD_DB")
	if databasePath == "" {
		t.Skip("performance startup child")
	}
	secret := []byte(os.Getenv("KB_PERF_CHILD_SECRET"))
	started := time.Now()
	database, err := store.Open(databasePath, secret)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	model := NewModel(database, nil, "perf")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: performanceWidth, Height: performanceHeight})
	model = updated.(Model)
	fixture, err := database.Board("perf")
	if err != nil {
		t.Fatal(err)
	}
	updated, _ = model.Update(boardLoadedMsg{board: fixture})
	model = updated.(Model)
	if model.View().Content == "" {
		t.Fatal("startup installed an empty snapshot")
	}
	fmt.Printf("KB_PERF_STARTUP_NS=%d\n", time.Since(started).Nanoseconds())
}

func measureFreshProcessStartup(t *testing.T, count int) performanceStartupResult {
	t.Helper()
	databasePath, secret := seedPerformanceStore(t, count)
	values := make([]time.Duration, startupSamples)
	attempts := 0
	for sample := range startupSamples {
		var lastError error
		for attempt := 0; attempt < 2; attempt++ {
			attempts++
			command := exec.Command(os.Args[0], "-test.run=^TestLargeBoardStartupChild$", "-test.count=1")
			command.Env = append(os.Environ(),
				"KB_PERF_CHILD_DB="+databasePath,
				"KB_PERF_CHILD_SECRET="+string(secret),
				"GOMAXPROCS=8",
			)
			output, err := command.CombinedOutput()
			if err != nil {
				lastError = fmt.Errorf("startup child: %w: %s", err, strings.TrimSpace(string(output)))
				continue
			}
			marker := "KB_PERF_STARTUP_NS="
			index := strings.Index(string(output), marker)
			if index < 0 {
				lastError = fmt.Errorf("startup child emitted no timing sentinel: %s", strings.TrimSpace(string(output)))
				continue
			}
			line := strings.SplitN(string(output[index+len(marker):]), "\n", 2)[0]
			nanoseconds, err := strconv.ParseInt(strings.TrimSpace(line), 10, 64)
			if err != nil {
				lastError = fmt.Errorf("parse startup timing %q: %w", line, err)
				continue
			}
			values[sample] = time.Duration(nanoseconds)
			lastError = nil
			break
		}
		if lastError != nil {
			t.Fatal(lastError)
		}
	}
	return performanceStartupResult{Tasks: count, Samples: startupSamples, Attempts: attempts, Latency: performanceDurations(values)}
}

func BenchmarkLargeBoardRetainedView(b *testing.B) {
	for _, count := range performanceCorpora {
		b.Run(fmt.Sprintf("tasks_%04d", count), func(b *testing.B) {
			model := performanceModel(count, "", performanceWidth, performanceHeight)
			b.ReportAllocs()
			for b.Loop() {
				retainedPerformanceView = model.View()
			}
		})
	}
}

func BenchmarkLargeBoardColdView(b *testing.B) {
	for _, count := range performanceCorpora {
		b.Run(fmt.Sprintf("tasks_%04d", count), func(b *testing.B) {
			model := performanceModel(count, "", performanceWidth, performanceHeight)
			b.ReportAllocs()
			for b.Loop() {
				retainedPerformanceView = model.renderColdView()
			}
		})
	}
}

func BenchmarkLargeBoardInputToFrame(b *testing.B) {
	for _, count := range performanceCorpora[1:] {
		b.Run(fmt.Sprintf("tasks_%04d", count), func(b *testing.B) {
			model := performanceModel(count, "", performanceWidth, performanceHeight)
			model.boardView.rows[0] = taskCount(model.board, board.StatusTodo) - 1
			model.rebuildRenderPlan(renderImpactAll)
			admission := newKeyboardAdmission(time.Now, model.inputAdmission, model.themeStyles().Timing)
			b.ReportAllocs()
			for b.Loop() {
				_ = admitPerformanceFilteredMessage(&model, admission, tea.KeyPressMsg{Code: tea.KeyDown})
			}
		})
	}
}
