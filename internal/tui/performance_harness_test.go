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
	ConvergenceLatency             performanceDistribution       `json:"convergence_latency"`
	AllocationsPerOp               float64                       `json:"allocations_per_op"`
	AllocatedBytesPerOp            float64                       `json:"allocated_bytes_per_op"`
	PublicationAllocatedBytesMax   uint64                        `json:"publication_allocated_bytes_max"`
	RetainedPlanOwnedBytesEstimate uint64                        `json:"retained_plan_owned_bytes_estimate"`
	AcceptedMessages               uint64                        `json:"accepted_messages"`
	PublishedFrames                uint64                        `json:"published_frames"`
	DiscardedEvents                uint64                        `json:"discarded_events"`
	StaleBoardLoads                uint64                        `json:"stale_board_loads"`
	StaleWorkerResults             uint64                        `json:"stale_worker_results"`
	NormalBaseBuilds               uint64                        `json:"normal_base_builds"`
	DimmedBaseBuilds               uint64                        `json:"dimmed_base_builds"`
	OverlayCompositions            uint64                        `json:"overlay_compositions"`
	ProjectionBuilds               uint64                        `json:"projection_builds"`
	ProjectionTaskVisits           uint64                        `json:"projection_task_visits"`
	SourceTaskComparisons          uint64                        `json:"source_task_comparisons"`
	ShippedIDVisits                uint64                        `json:"shipped_id_visits"`
	RenderedCardRecords            uint64                        `json:"rendered_card_records"`
	NavigationArtifactHits         uint64                        `json:"navigation_artifact_hits"`
	NavigationArtifactMisses       uint64                        `json:"navigation_artifact_misses"`
	NavigationArtifactPublications uint64                        `json:"navigation_artifact_publications"`
	NavigationArtifactFallbacks    uint64                        `json:"navigation_artifact_fallbacks"`
	WatcherPollFastPaths           uint64                        `json:"watcher_poll_fast_paths"`
	WatcherVersionFastPaths        uint64                        `json:"watcher_version_fast_paths"`
	WatcherStaleLoadFastPaths      uint64                        `json:"watcher_stale_load_fast_paths"`
	WatcherLifecycleFallbacks      uint64                        `json:"watcher_lifecycle_fallbacks"`
	RenderImpactClassifications    uint64                        `json:"render_impact_classifications"`
	SynchronousLayoutRecords       uint64                        `json:"synchronous_layout_records"`
	TemporalScheduledTicks         uint64                        `json:"temporal_scheduled_ticks"`
	TemporalStaleTicks             uint64                        `json:"temporal_stale_ticks"`
	TemporalTaskVisits             uint64                        `json:"temporal_task_visits"`
	TemporalRecordsRefreshed       uint64                        `json:"temporal_records_refreshed"`
	TemporalIndexNodesVisited      uint64                        `json:"temporal_index_nodes_visited"`
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
	StaleBoardLoad        bool   `json:"stale_board_load"`
	followUp              tea.Cmd
	pollFollowUp          tea.Cmd
	geometry              tea.Cmd
	temporal              tea.Cmd
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
	deferGeometry      bool
	// reconciliationOnly marks direct boardLoadedMsg coverage. It exercises
	// the incremental projection seam, not the watcher admission path.
	reconciliationOnly bool
	warmups            int
	samples            int
	newModel           func() Model
	prepare            func(*Model, int)
	admit              func(*Model, int) []performancePublicationStamp
	requireFollowUp    bool
	requireTemporal    bool
	settleFollowUp     func(*testing.T, *Model, tea.Cmd)
	validateSample     func(*testing.T, *Model, []performancePublicationStamp)
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
		{name: "refresh_25_task_burst", wantEvents: 27, wantAccepted: 27},
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
			if test.name == "refresh_25_task_burst" {
				accepted++
				wantMessage++
				if !stamp.Accepted || stamp.AcceptedMessageID != wantMessage {
					t.Fatalf("%s event %d admission identity = %+v, want accepted message %d",
						test.name, index, stamp, wantMessage)
				}
				if index < len(stamps)-1 {
					if stamp.Published || stamp.InstalledSnapshotID != wantSnapshot || stamp.InstalledForMessageID != before.InstalledForMessageID {
						t.Fatalf("%s event %d published before newest generation: %+v", test.name, index, stamp)
					}
					if index == len(stamps)-2 && (!stamp.StaleBoardLoad || stamp.Discarded) {
						t.Fatalf("%s stale first load observation = %+v, want generation-stale without admission discard", test.name, stamp)
					}
					continue
				}
				wantSnapshot++
				if !stamp.Published || stamp.InstalledSnapshotID != wantSnapshot || stamp.InstalledForMessageID != wantMessage {
					t.Fatalf("%s newest generation publication = %+v, want snapshot %d for message %d",
						test.name, stamp, wantSnapshot, wantMessage)
				}
				continue
			}
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

func TestPerformanceRefreshScenariosEnforcePublicationContracts(t *testing.T) {
	for _, count := range []int{120, 500, 1000} {
		t.Run(integerLabel(count), func(t *testing.T) {
			for _, name := range []string{
				"refresh_nonmatching_insert", "refresh_nonmatching_total_metadata",
				"refresh_nonmatching_label_metadata", "refresh_admission_new_issue", "refresh_25_task_burst",
			} {
				t.Run(name, func(t *testing.T) {
					var scenario performanceScenario
					found := false
					for _, candidate := range performanceScenarios(count) {
						if candidate.name == name {
							scenario, found = candidate, true
							break
						}
					}
					if !found {
						t.Fatalf("scenario %q not found", name)
					}
					if name != "refresh_admission_new_issue" && name != "refresh_25_task_burst" && !scenario.reconciliationOnly {
						t.Fatalf("direct boardLoaded scenario %q is not marked reconciliation-only", name)
					}
					scenario.warmups, scenario.samples = 1, 1
					result := runPerformanceScenario(t, count, scenario)
					switch name {
					case "refresh_nonmatching_insert":
						if result.AcceptedMessages != 1 || result.PublishedFrames != 0 || result.ProjectionBuilds != 0 ||
							result.SourceTaskComparisons != uint64(count+1) {
							t.Fatalf("source-only refresh = accepted:%d published:%d projection-builds:%d source-comparisons:%d, want 1/0/0/%d",
								result.AcceptedMessages, result.PublishedFrames, result.ProjectionBuilds,
								result.SourceTaskComparisons, count+1)
						}
					case "refresh_nonmatching_total_metadata", "refresh_nonmatching_label_metadata":
						if result.AcceptedMessages != 1 || result.PublishedFrames != 1 {
							t.Fatalf("metadata refresh = accepted:%d published:%d, want 1/1",
								result.AcceptedMessages, result.PublishedFrames)
						}
					case "refresh_admission_new_issue":
						if result.AcceptedMessages != 5 || result.PublishedFrames != 1 {
							t.Fatalf("end-to-end refresh = accepted:%d published:%d, want 5/1",
								result.AcceptedMessages, result.PublishedFrames)
						}
						if violation := navigationArtifactAcceptanceViolation(result); violation != "" {
							t.Fatalf("end-to-end refresh artifact acceptance: %s", violation)
						}
						if result.WatcherPollFastPaths != 2 || result.WatcherVersionFastPaths != 2 ||
							result.WatcherLifecycleFallbacks != 0 || result.RenderImpactClassifications != 1 {
							t.Fatalf("end-to-end lifecycle = polls:%d versions:%d fallbacks:%d classifications:%d, want 2/2/0/1",
								result.WatcherPollFastPaths, result.WatcherVersionFastPaths,
								result.WatcherLifecycleFallbacks, result.RenderImpactClassifications)
						}
					case "refresh_25_task_burst":
						if result.AcceptedMessages != 27 || result.PublishedFrames != 1 || result.DiscardedEvents != 0 || result.StaleBoardLoads != 1 {
							t.Fatalf("generation burst = accepted:%d published:%d discarded:%d stale-loads:%d, want 27/1/0/1",
								result.AcceptedMessages, result.PublishedFrames, result.DiscardedEvents, result.StaleBoardLoads)
						}
						if violation := navigationArtifactAcceptanceViolation(result); violation != "" {
							t.Fatalf("generation burst artifact acceptance: %s", violation)
						}
						if result.WatcherPollFastPaths != 0 || result.WatcherVersionFastPaths != 25 ||
							result.WatcherStaleLoadFastPaths != 1 || result.WatcherLifecycleFallbacks != 0 ||
							result.RenderImpactClassifications != 1 {
							t.Fatalf("generation burst lifecycle = polls:%d versions:%d stale-loads:%d fallbacks:%d classifications:%d, want 0/25/1/0/1",
								result.WatcherPollFastPaths, result.WatcherVersionFastPaths,
								result.WatcherStaleLoadFastPaths, result.WatcherLifecycleFallbacks,
								result.RenderImpactClassifications)
						}
					}
				})
			}
		})
	}
}

func TestPerformanceBurstFirstVisibleExcludesDeferredGeometry(t *testing.T) {
	var scenario performanceScenario
	found := false
	for _, candidate := range performanceScenarios(120) {
		if candidate.name == "refresh_25_task_burst" {
			scenario, found = candidate, true
			break
		}
	}
	if !found {
		t.Fatal("refresh_25_task_burst scenario not found")
	}
	scenario.warmups, scenario.samples = 1, 1
	admit := scenario.admit
	scenario.admit = func(model *Model, sample int) []performancePublicationStamp {
		stamps := admit(model, sample)
		wrapped := false
		for index := range stamps {
			if stamps[index].geometry == nil {
				continue
			}
			command := stamps[index].geometry
			stamps[index].geometry = func() tea.Msg {
				time.Sleep(75 * time.Millisecond)
				return command()
			}
			wrapped = true
		}
		if !wrapped {
			panic("refresh burst did not expose deferred geometry")
		}
		return stamps
	}
	result := runPerformanceScenario(t, 120, scenario)
	if result.Latency.P50NS >= result.ConvergenceLatency.P50NS {
		t.Fatalf("first-visible latency = %s, convergence = %s; deferred worker polluted first-visible",
			time.Duration(result.Latency.P50NS), time.Duration(result.ConvergenceLatency.P50NS))
	}
	if result.ConvergenceLatency.P50NS < (75 * time.Millisecond).Nanoseconds() {
		t.Fatalf("convergence latency = %s, did not record deferred worker", time.Duration(result.ConvergenceLatency.P50NS))
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

func TestPerformanceLatencyExcludesSampleValidation(t *testing.T) {
	validated := false
	scenario := performanceScenario{
		name:     "validation_outside_latency",
		warmups:  1,
		samples:  1,
		newModel: func() Model { return performanceModel(17, "", performanceWidth, performanceHeight) },
		admit: func(model *Model, _ int) []performancePublicationStamp {
			return discardedPerformanceStamp(model)
		},
		validateSample: func(_ *testing.T, _ *Model, _ []performancePublicationStamp) {
			time.Sleep(100 * time.Millisecond)
			validated = true
		},
	}
	result := runPerformanceScenario(t, 17, scenario)
	if !validated {
		t.Fatal("sample validation did not run")
	}
	if latency := time.Duration(result.Latency.P50NS); latency >= 50*time.Millisecond {
		t.Fatalf("measured latency %s included the 100ms validation delay", latency)
	}
}

func TestNavigationArtifactAcceptanceRejectsMissingOrUnboundedReuse(t *testing.T) {
	held := performanceScenarioResult{
		Name:                           "held_navigation",
		Tasks:                          1000,
		PublishedFrames:                202,
		NavigationArtifactHits:         5050,
		NavigationArtifactMisses:       909,
		NavigationArtifactPublications: 202,
		RenderedCardRecords:            909,
	}
	wheel := performanceScenarioResult{
		Name:                           "useful_wheel",
		Tasks:                          1000,
		PublishedFrames:                101,
		NavigationArtifactHits:         1414,
		NavigationArtifactMisses:       101,
		NavigationArtifactPublications: 101,
		RenderedCardRecords:            101,
	}
	refresh := performanceScenarioResult{
		Name:                           "refresh_admission_new_issue",
		Tasks:                          1000,
		PublishedFrames:                1,
		NavigationArtifactHits:         20,
		NavigationArtifactMisses:       1,
		NavigationArtifactPublications: 1,
		RenderedCardRecords:            1,
	}
	if violation := navigationArtifactAcceptanceViolation(held); violation != "" {
		t.Fatalf("honest held artifacts rejected: %s", violation)
	}
	if violation := navigationArtifactAcceptanceViolation(wheel); violation != "" {
		t.Fatalf("honest wheel artifacts rejected: %s", violation)
	}
	if violation := navigationArtifactAcceptanceViolation(refresh); violation != "" {
		t.Fatalf("honest refresh artifacts rejected: %s", violation)
	}

	tests := []struct {
		name   string
		result performanceScenarioResult
	}{
		{name: "held zero hits", result: func() performanceScenarioResult {
			result := held
			result.NavigationArtifactHits = 0
			return result
		}()},
		{name: "held excessive misses", result: func() performanceScenarioResult {
			result := held
			result.NavigationArtifactMisses = 5*result.PublishedFrames + 1
			result.RenderedCardRecords = result.NavigationArtifactMisses
			return result
		}()},
		{name: "held fallback", result: func() performanceScenarioResult {
			result := held
			result.NavigationArtifactFallbacks = 1
			return result
		}()},
		{name: "wheel zero hits", result: func() performanceScenarioResult {
			result := wheel
			result.NavigationArtifactHits = 0
			return result
		}()},
		{name: "wheel excessive misses", result: func() performanceScenarioResult {
			result := wheel
			result.NavigationArtifactMisses++
			result.RenderedCardRecords = result.NavigationArtifactMisses
			return result
		}()},
		{name: "wheel fallback", result: func() performanceScenarioResult {
			result := wheel
			result.NavigationArtifactFallbacks = 1
			return result
		}()},
		{name: "refresh zero hits", result: func() performanceScenarioResult {
			result := refresh
			result.NavigationArtifactHits = 0
			return result
		}()},
		{name: "refresh excessive misses", result: func() performanceScenarioResult {
			result := refresh
			result.NavigationArtifactMisses = 65
			result.RenderedCardRecords = 65
			return result
		}()},
		{name: "refresh fallback", result: func() performanceScenarioResult {
			result := refresh
			result.NavigationArtifactFallbacks = 1
			return result
		}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if violation := navigationArtifactAcceptanceViolation(test.result); violation == "" {
				t.Fatal("invalid artifact counters passed acceptance")
			}
		})
	}
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

func TestPerformanceGeometrySettlementDoesNotExecuteTemporalTimer(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	model := NewModel(stubBoardReader{}, nil, "perf")
	model.now = func() time.Time { return now }
	model.renderedAt = now
	model.board = board.Board{Title: "Clock", Tasks: []board.Task{{
		ID: "one", Title: "One", Status: board.StatusTodo, CreatedAt: now.Add(-23 * time.Hour),
	}}}
	executed := false
	model.temporalSchedule = func(_ time.Duration, message temporalTickMsg) tea.Cmd {
		return func() tea.Msg {
			executed = true
			return message
		}
	}
	model.rebuildRenderPlan(renderImpactAll)
	model, commands := model.updateWithCommands(tea.WindowSizeMsg{Width: 100, Height: 30})
	if commands.temporal == nil {
		t.Fatal("fixture did not expose a temporal timer separately from geometry")
	}
	settlePerformanceGeometryCommand(t, &model, commands.geometry)
	if executed {
		t.Fatal("geometry settlement executed the temporal timer")
	}
}

func TestPerformanceCachedOverlayAndTemporalScenariosExactWork(t *testing.T) {
	for _, name := range []string{
		"overlay_update_cached_dim", "overlay_close_cached_normal", "temporal_boundary", "stale_temporal_tick",
	} {
		t.Run(name, func(t *testing.T) {
			var scenario performanceScenario
			found := false
			for _, candidate := range performanceScenarios(120) {
				if candidate.name == name {
					scenario, found = candidate, true
					break
				}
			}
			if !found {
				t.Fatalf("scenario %q not found", name)
			}
			scenario.warmups, scenario.samples = 1, 2
			result := runPerformanceScenario(t, 120, scenario)
			switch name {
			case "overlay_update_cached_dim", "overlay_close_cached_normal":
				if result.NormalBaseBuilds != 0 || result.DimmedBaseBuilds != 0 ||
					result.ProjectionBuilds != 0 || result.ProjectionTaskVisits != 0 ||
					result.SourceTaskComparisons != 0 || result.ShippedIDVisits != 0 ||
					result.RenderedCardRecords != 0 || result.SynchronousLayoutRecords != 0 ||
					result.OverlayCompositions != uint64(scenario.samples) ||
					result.PublishedFrames != uint64(scenario.samples) || result.TemporalTaskVisits != 0 ||
					result.TemporalScheduledTicks != 0 {
					t.Fatalf("cached overlay exact work = %+v", result)
				}
			case "temporal_boundary":
				if result.ProjectionBuilds != 0 || result.ProjectionTaskVisits != 0 ||
					result.SourceTaskComparisons != 0 || result.ShippedIDVisits != 0 ||
					result.SynchronousLayoutRecords != 0 ||
					result.NormalBaseBuilds != uint64(scenario.samples) ||
					result.OverlayCompositions != uint64(scenario.samples) ||
					result.PublishedFrames != uint64(scenario.samples) ||
					result.TemporalScheduledTicks != uint64(scenario.samples) {
					t.Fatalf("temporal boundary exact work = %+v", result)
				}
			case "stale_temporal_tick":
				if result.AcceptedMessages != 0 || result.PublishedFrames != 0 ||
					result.NormalBaseBuilds != 0 || result.DimmedBaseBuilds != 0 ||
					result.OverlayCompositions != 0 || result.ProjectionBuilds != 0 ||
					result.ProjectionTaskVisits != 0 || result.SourceTaskComparisons != 0 ||
					result.ShippedIDVisits != 0 || result.RenderedCardRecords != 0 ||
					result.SynchronousLayoutRecords != 0 || result.TemporalScheduledTicks != 0 ||
					result.TemporalStaleTicks != uint64(scenario.samples) || result.TemporalTaskVisits != 0 {
					t.Fatalf("stale temporal exact work = %+v", result)
				}
			}
		})
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
	scopedBoard := cloneBoard(baseBoard)
	for index := range scopedBoard.Tasks {
		scopedBoard.Tasks[index].Tags = append(scopedBoard.Tasks[index].Tags, "project::alpha")
	}
	outOfScope := appendPerformanceTasks(scopedBoard, 1, false)
	outOfScope.Tasks[len(outOfScope.Tasks)-1].Tags = []string{"performance", "dev", "project::beta"}
	metadataTotal := appendPerformanceTasks(baseBoard, 1, false)
	metadataLabel := cloneBoard(baseBoard)
	for index := range metadataLabel.Tasks {
		if !strings.Contains(metadataLabel.Tasks[index].Title, "keep17") {
			metadataLabel.Tasks[index].Tags = append(metadataLabel.Tasks[index].Tags, "new-filter-label")
			break
		}
	}
	resetBoard := func(filter string) func(*Model, int) {
		return func(model *Model, _ int) {
			model.board = baseBoard
			model.filter.input.SetValue(filter)
			model.boardView.normalizeSelection(model.filteredBoard())
			model.rebuildRenderPlan(renderImpactAll)
		}
	}
	resetScopedBoard := func(filter string) func(*Model, int) {
		return func(model *Model, _ int) {
			model.board = scopedBoard
			model.projects = projectSwitcher{name: "alpha"}
			model.filter.input.SetValue(filter)
			model.boardView.normalizeSelection(model.filteredBoard())
			model.rebuildRenderPlan(renderImpactAll)
		}
	}
	var clampedAdmission *keyboardAdmission
	var clampedClock stepClock
	var heldAdmission *keyboardAdmission
	var heldClock stepClock
	var burstReader *sequenceBoardReader
	var admissionReader *sequenceBoardReader
	var admissionWatcher *countingVersionReader
	var admissionTimerTicks int
	burstStale := appendPerformanceTasks(baseBoard, 1, true)
	burstNewest := appendPerformanceTasks(baseBoard, 25, true)
	admissionBoard := appendPerformanceTasks(baseBoard, 1, true)

	return []performanceScenario{
		{
			name: "useful_navigation",
			newModel: func() Model {
				model := base("")
				model.boardView.setCursorAtFrom(model.currentProjection(), 0,
					min(1, taskCount(model.board, board.StatusTodo)-1))
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
				model.boardView.setCursorAtFrom(model.currentProjection(), 0,
					taskCount(model.board, board.StatusTodo)-1)
				model.rebuildRenderPlan(renderImpactAll)
				clampedClock.at = time.Unix(0, 0)
				clampedAdmission = newKeyboardAdmission(clampedClock.now, model.inputAdmission, model.themeStyles().Timing)
				return model
			},
			admit: func(model *Model, _ int) []performancePublicationStamp {
				return admitPerformanceFilteredMessage(model, clampedAdmission, tea.KeyPressMsg{Code: tea.KeyDown})
			},
			validateSample: func(t *testing.T, _ *Model, stamps []performancePublicationStamp) {
				t.Helper()
				assertPerformanceAdmissionCounts(t, "clamped_navigation", stamps, 0, len(stamps))
				if clampedAdmission.active || clampedAdmission.desiredTaskID != "" || clampedAdmission.desiredOrdinal != 0 {
					t.Fatalf("clamped_navigation retained admission state: active=%t target=%q ordinal=%d",
						clampedAdmission.active, clampedAdmission.desiredTaskID, clampedAdmission.desiredOrdinal)
				}
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
				model.boardView.setCursorAtFrom(model.currentProjection(), 0,
					min(1, taskCount(model.board, board.StatusTodo)-1))
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
			validateSample: func(t *testing.T, _ *Model, stamps []performancePublicationStamp) {
				t.Helper()
				assertPerformanceAdmissionCounts(t, "held_navigation", stamps, 2, 6)
			},
		},
		{
			name: "useful_wheel",
			newModel: func() Model {
				return performanceModel(count, "", 80, 24)
			},
			requireFollowUp: true,
			settleFollowUp:  settlePerformancePointerScrollLinger,
			prepare: func(model *Model, _ int) {
				status := board.StatusTodo
				column := statusIndex(status)
				maximum := model.current.semantics.columns[column].maxScroll
				reset := boardColumnScrolledMsg{
					status: status,
					from:   model.boardView.scrolls[column],
					offset: 0,
					anchor: model.boardScrollAnchor(status, 0),
					max:    maximum,
				}
				*model, _ = model.updateWithCommands(reset)
				model.pointerAdmission.resetCadence()
			},
			admit: func(model *Model, _ int) []performancePublicationStamp {
				return admitPerformancePointer(model, tea.MouseWheelMsg{X: 10, Y: 6, Button: tea.MouseWheelDown})
			},
			validateSample: func(t *testing.T, model *Model, stamps []performancePublicationStamp) {
				t.Helper()
				assertPerformanceAdmissionCounts(t, "useful_wheel", stamps, 1, 0)
				if model.boardView.scrolls[statusIndex(board.StatusTodo)] <= 0 {
					t.Fatal("useful_wheel did not change the published scroll target")
				}
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
			name: "overlay_update_cached_dim",
			newModel: func() Model {
				model := base("")
				model, _ = model.updateWithCommands(tea.KeyPressMsg{Code: '?', Text: "?"})
				return model
			},
			admit: func(model *Model, _ int) []performancePublicationStamp {
				return admitPerformanceMessage(model, tea.KeyPressMsg{Code: 'x', Text: "x"})
			},
		},
		{
			name: "overlay_close_cached_normal",
			newModel: func() Model {
				model := base("")
				model, _ = model.updateWithCommands(tea.KeyPressMsg{Code: '?', Text: "?"})
				return model
			},
			prepare: func(model *Model, _ int) {
				if !model.helpOpen {
					*model, _ = model.updateWithCommands(tea.KeyPressMsg{Code: '?', Text: "?"})
				}
			},
			admit: func(model *Model, _ int) []performancePublicationStamp {
				return admitPerformanceMessage(model, tea.KeyPressMsg{Code: '?', Text: "?"})
			},
		},
		{
			name: "temporal_boundary",
			newModel: func() Model {
				model := base("")
				model.temporalSchedule = func(_ time.Duration, message temporalTickMsg) tea.Cmd {
					return func() tea.Msg { return message }
				}
				_ = model.reconcileTemporalSchedule()
				return model
			},
			prepare: func(model *Model, _ int) {
				model.now = func() time.Time { return model.temporalDeadline }
			},
			admit: func(model *Model, _ int) []performancePublicationStamp {
				return admitPerformanceMessage(model, temporalTickMsg{
					generation: model.temporalGeneration, deadline: model.temporalDeadline,
				})
			},
			requireTemporal: true,
		},
		{
			name:               "stale_temporal_tick",
			expectedZeroFrames: true,
			newModel: func() Model {
				model := base("")
				_ = model.reconcileTemporalSchedule()
				return model
			},
			admit: func(model *Model, _ int) []performancePublicationStamp {
				return admitPerformanceMessage(model, temporalTickMsg{
					generation: model.temporalGeneration - 1, deadline: model.temporalDeadline,
				})
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
				model.pollStarted = true
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
			name:               "refresh_matching_insert",
			reconciliationOnly: true,
			newModel:           func() Model { return base("keep17") },
			prepare:            resetBoard("keep17"),
			admit: func(model *Model, _ int) []performancePublicationStamp {
				return admitPerformanceMessage(model, boardLoadedMsg{board: matching})
			},
		},
		{
			name:               "refresh_nonmatching_insert",
			expectedZeroFrames: true,
			reconciliationOnly: true,
			newModel: func() Model {
				model := base("keep17")
				model.board = scopedBoard
				model.projects = projectSwitcher{name: "alpha"}
				model.rebuildRenderPlan(renderImpactAll)
				return model
			},
			prepare: resetScopedBoard("keep17"),
			admit: func(model *Model, _ int) []performancePublicationStamp {
				return admitPerformanceMessage(model, boardLoadedMsg{board: outOfScope})
			},
			validateSample: func(t *testing.T, model *Model, stamps []performancePublicationStamp) {
				t.Helper()
				if len(stamps) != 1 || !stamps[0].Accepted || stamps[0].Published {
					t.Fatalf("out-of-project refresh admission = %+v, want accepted without publication", stamps)
				}
				if !model.current.projection.matchesSourceIdentity(outOfScope) ||
					len(model.current.projection.tasks) != count+1 || model.projects.name != "alpha" {
					t.Fatal("out-of-project refresh did not reconcile the scoped source")
				}
			},
		},
		{
			name:               "refresh_nonmatching_total_metadata",
			reconciliationOnly: true,
			newModel:           func() Model { return base("keep17") },
			prepare:            resetBoard("keep17"),
			admit: func(model *Model, _ int) []performancePublicationStamp {
				return admitPerformanceMessage(model, boardLoadedMsg{board: metadataTotal})
			},
			validateSample: func(t *testing.T, model *Model, stamps []performancePublicationStamp) {
				t.Helper()
				if len(stamps) != 1 || !stamps[0].Accepted || !stamps[0].Published {
					t.Fatalf("total metadata refresh admission = %+v, want one publication", stamps)
				}
				if !strings.Contains(model.View().Content, "17 of "+integerLabel(count+1)+" cards") {
					t.Fatal("total metadata refresh did not publish the new source total")
				}
			},
		},
		{
			name:               "refresh_nonmatching_label_metadata",
			reconciliationOnly: true,
			newModel:           func() Model { return base("keep17") },
			prepare:            resetBoard("keep17"),
			admit: func(model *Model, _ int) []performancePublicationStamp {
				return admitPerformanceMessage(model, boardLoadedMsg{board: metadataLabel})
			},
			validateSample: func(t *testing.T, model *Model, stamps []performancePublicationStamp) {
				t.Helper()
				if len(stamps) != 1 || !stamps[0].Accepted || !stamps[0].Published {
					t.Fatalf("label metadata refresh admission = %+v, want one publication", stamps)
				}
				if !strings.Contains(model.View().Content, "new-filter-label") {
					t.Fatal("label metadata refresh did not publish the new toolbar label")
				}
			},
		},
		{
			name: "refresh_admission_new_issue",
			newModel: func() Model {
				reader := &sequenceBoardReader{results: []boardResult{{board: admissionBoard}}}
				watcher := &countingVersionReader{version: 2}
				admissionReader = reader
				admissionWatcher = watcher
				model := base("keep17")
				model.store = reader
				model.watcher = watcher
				model.haveVersion = true
				model.dataVersion = 1
				model.pollStarted = true
				model.loadGeneration = 1
				model.applyStyles(theme.NewWith(model.isDark, theme.TimingCollapsed))
				model.rebuildRenderPlan(renderImpactAll)
				return model
			},
			prepare: func(model *Model, _ int) {
				resetBoard("keep17")(model, 0)
				admissionReader.results = []boardResult{{board: admissionBoard}}
				admissionReader.calls = 0
				admissionWatcher.calls = 0
				admissionTimerTicks = 0
				model.store = admissionReader
				model.watcher = admissionWatcher
				model.haveVersion = true
				model.dataVersion = 1
				model.pollStarted = true
				model.loading = false
				model.reloadPending = false
				model.loadGeneration = 1
				model.activeLoadGen = 0
				model.applyStyles(theme.NewWith(model.isDark, theme.TimingCollapsed))
				model.rebuildRenderPlan(renderImpactAll)
			},
			admit: func(model *Model, _ int) []performancePublicationStamp {
				admissionTimerTicks++
				timer := model.schedulePoll()
				timerStamps := admitPerformanceMessage(model, timer())
				versionRead := timerStamps[0].followUp
				timerStamps[0].followUp = nil
				version := performanceDataVersionResult(versionRead)
				versionStamps := admitPerformanceMessage(model, version)
				load, poll := performanceLoadAndPollCommands(versionStamps[0].followUp)
				versionStamps[0].followUp = nil
				versionStamps[0].pollFollowUp = poll
				loaded := performanceLoadResult(load)
				loadedStamps := admitPerformanceMessage(model, loaded)
				nextPoll := versionStamps[0].pollFollowUp
				admissionTimerTicks++
				nextPollStamps := admitPerformanceMessage(model, nextPoll())
				nextRead := nextPollStamps[0].followUp
				nextPollStamps[0].followUp = nil
				nextVersionStamps := admitPerformanceMessage(model, performanceDataVersionResult(nextRead))
				nextPollStamps[0].pollFollowUp = nil
				nextVersionStamps[0].pollFollowUp = nextVersionStamps[0].followUp
				nextVersionStamps[0].followUp = nil
				stamps := append(timerStamps, versionStamps...)
				stamps = append(stamps, loadedStamps...)
				stamps = append(stamps, nextPollStamps...)
				return append(stamps, nextVersionStamps...)
			},
			validateSample: func(t *testing.T, model *Model, stamps []performancePublicationStamp) {
				t.Helper()
				if len(stamps) != 5 || admissionTimerTicks != 2 || admissionWatcher.calls != 2 ||
					!stamps[0].Accepted || stamps[0].Published ||
					!stamps[1].Accepted || stamps[1].Published || stamps[1].pollFollowUp == nil ||
					!stamps[2].Accepted || !stamps[2].Published ||
					!stamps[3].Accepted || stamps[3].Published || stamps[3].pollFollowUp != nil ||
					!stamps[4].Accepted || stamps[4].Published || stamps[4].pollFollowUp == nil || admissionReader.calls != 1 ||
					model.loadGeneration != 2 || model.activeLoadGen != 0 || model.loading || model.reloadPending ||
					!model.current.projection.matchesSourceIdentity(admissionBoard) {
					t.Fatalf("end-to-end refresh = stamps:%+v timers:%d version-reads:%d board-reads:%d source:%t",
						stamps, admissionTimerTicks, admissionWatcher.calls, admissionReader.calls,
						model.current.projection.matchesSourceIdentity(admissionBoard))
				}
				assertPerformanceSinglePollSuccessor(t, stamps[4].pollFollowUp)
			},
		},
		{
			name:          "refresh_25_task_burst",
			deferGeometry: true,
			newModel: func() Model {
				reader := &sequenceBoardReader{results: []boardResult{{board: burstStale}, {board: burstNewest}}}
				burstReader = reader
				model := base("keep17")
				model.store = reader
				model.watcher = stubVersionReader{}
				model.haveVersion = true
				model.dataVersion = 1
				model.pollStarted = true
				model.loadGeneration = 1
				model.activeLoadGen = 0
				model.applyStyles(theme.NewWith(model.isDark, theme.TimingCollapsed))
				model.rebuildRenderPlan(renderImpactAll)
				return model
			},
			prepare: func(model *Model, _ int) {
				resetBoard("keep17")(model, 0)
				burstReader.results = []boardResult{{board: burstStale}, {board: burstNewest}}
				burstReader.calls = 0
				model.store = burstReader
				model.watcher = stubVersionReader{}
				model.haveVersion = true
				model.dataVersion = 1
				model.pollStarted = true
				model.loading = false
				model.reloadPending = false
				model.loadGeneration = 1
				model.activeLoadGen = 0
				model.applyStyles(theme.NewWith(model.isDark, theme.TimingCollapsed))
				model.rebuildRenderPlan(renderImpactAll)
			},
			admit: func(model *Model, _ int) []performancePublicationStamp {
				stamps := admitPerformanceMessage(model, dataVersionMsg{version: 2})
				firstLoad := stamps[0].followUp
				stamps[0].followUp = nil
				for version := int64(3); version <= 26; version++ {
					versionStamps := admitPerformanceMessage(model, dataVersionMsg{version: version})
					performancePollResult(versionStamps[0].followUp)
					versionStamps[0].followUp = nil
					stamps = append(stamps, versionStamps...)
				}
				stale := performanceLoadAndPollResult(firstLoad)
				staleStamps := admitPerformanceMessage(model, stale)
				nextLoad := staleStamps[0].followUp
				staleStamps[0].followUp = nil
				stamps = append(stamps, staleStamps...)
				newest := performanceLoadResult(nextLoad)
				return append(stamps, admitPerformanceMessage(model, newest)...)
			},
			validateSample: func(t *testing.T, model *Model, stamps []performancePublicationStamp) {
				t.Helper()
				if len(stamps) != 27 {
					t.Fatalf("refresh burst emitted %d events, want 27 version/load admissions", len(stamps))
				}
				published, discarded, staleLoads := 0, 0, 0
				for _, stamp := range stamps {
					if stamp.Published {
						published++
					}
					if stamp.Discarded {
						discarded++
					}
					if stamp.StaleBoardLoad {
						staleLoads++
					}
				}
				if published != 1 || discarded != 0 || staleLoads != 1 || !model.current.projection.matchesSourceIdentity(burstNewest) {
					t.Fatalf("refresh burst settlement = published:%d discarded:%d stale-loads:%d source:%t",
						published, discarded, staleLoads, model.current.projection.matchesSourceIdentity(burstNewest))
				}
			},
		},
	}
}

func performanceDataVersionResult(command tea.Cmd) dataVersionMsg {
	if command == nil {
		panic("refresh admission did not retain a data-version read")
	}
	message, ok := command().(dataVersionMsg)
	if !ok {
		panic(fmt.Sprintf("refresh admission read returned %T, want dataVersionMsg", message))
	}
	return message
}

func performanceLoadAndPollCommands(command tea.Cmd) (tea.Cmd, tea.Cmd) {
	if command == nil {
		panic("refresh admission did not retain a load and poll command")
	}
	message := command()
	batch, ok := message.(tea.BatchMsg)
	if !ok || len(batch) != 2 || batch[0] == nil || batch[1] == nil {
		panic(fmt.Sprintf("refresh admission returned %T batch with wrong cardinality", message))
	}
	return batch[0], batch[1]
}

// performanceLoadAndPollResult executes the production load plus poll
// successor returned by a data-version admission. Timing is collapsed, so the
// poll command is observed without waiting for a real timer.
func performanceLoadAndPollResult(command tea.Cmd) boardLoadedMsg {
	load, poll := performanceLoadAndPollCommands(command)
	performancePollResult(poll)
	return performanceLoadResult(load)
}

func performancePollResult(command tea.Cmd) {
	if command == nil {
		panic("refresh admission dropped a poll successor")
	}
	if poll, ok := command().(pollTickMsg); !ok {
		panic(fmt.Sprintf("refresh admission successor returned %T, want pollTickMsg", poll))
	}
}

func assertPerformanceSinglePollSuccessor(t *testing.T, command tea.Cmd) {
	t.Helper()
	if command == nil {
		t.Fatal("refresh admission did not return its next poll successor")
	}
	if message := command(); message == nil {
		t.Fatal("refresh admission poll successor returned nil")
	} else if _, ok := message.(pollTickMsg); !ok {
		t.Fatalf("refresh admission poll successor returned %T, want exactly one pollTickMsg", message)
	}
}

// performanceLoadResult executes a direct production board-load command.
func performanceLoadResult(command tea.Cmd) boardLoadedMsg {
	if command == nil {
		panic("refresh burst did not retain a load command")
	}
	message := command()
	if loaded, ok := message.(boardLoadedMsg); ok {
		return loaded
	}
	batch, ok := message.(tea.BatchMsg)
	if !ok || len(batch) == 0 || batch[0] == nil {
		panic(fmt.Sprintf("refresh burst load command returned %T", message))
	}
	return performanceLoadResult(batch[0])
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
	convergenceLatencies := make([]time.Duration, samples)
	var allocations, allocatedBytes, maximumPublicationBytes, maximumRetainedPlanBytes uint64
	var accepted, frames, discarded, stale, staleBoardLoads uint64
	var normalBases, dimmedBases, compositions, projectionBuilds, projectionVisits uint64
	var sourceComparisons, shippedIDVisits uint64
	var renderedCards, artifactHits, artifactMisses, artifactPublications, artifactFallbacks uint64
	var watcherPollFastPaths, watcherVersionFastPaths, watcherStaleLoadFastPaths uint64
	var watcherLifecycleFallbacks, renderImpactClassifications uint64
	var synchronousLayout, temporalScheduled, temporalStale, temporalVisits uint64
	var temporalRecords, temporalNodes uint64
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
		if scenario.deferGeometry {
			for _, stamp := range stamps {
				if stamp.followUp != nil || stamp.temporal != nil {
					t.Fatalf("%s scheduled a non-geometry command before first-visible timing ended", scenario.name)
				}
			}
			latencies[sample] = time.Since(started)
			convergenceStarted := time.Now()
			settlePerformanceStampCommands(t, &model, scenario, stamps)
			convergenceLatencies[sample] = time.Since(convergenceStarted)
		} else {
			settlePerformanceStampCommands(t, &model, scenario, stamps)
			latencies[sample] = time.Since(started)
		}
		if scenario.validateSample != nil {
			scenario.validateSample(t, &model, stamps)
		}
		afterStats := model.RenderPlanStats()
		runtime.ReadMemStats(&afterMemory)
		allocations += afterMemory.Mallocs - beforeMemory.Mallocs
		publicationBytes := afterMemory.TotalAlloc - beforeMemory.TotalAlloc
		allocatedBytes += publicationBytes
		maximumPublicationBytes = max(maximumPublicationBytes, publicationBytes)
		maximumRetainedPlanBytes = max(maximumRetainedPlanBytes, afterStats.RetainedPlanOwnedBytesEstimate)
		frames += afterStats.PublishedFrames - beforeStats.PublishedFrames
		stale += afterStats.StaleWorkerResults - beforeStats.StaleWorkerResults
		normalBases += afterStats.NormalBaseBuilds - beforeStats.NormalBaseBuilds
		dimmedBases += afterStats.DimmedBaseBuilds - beforeStats.DimmedBaseBuilds
		compositions += afterStats.OverlayCompositions - beforeStats.OverlayCompositions
		projectionBuilds += afterStats.ProjectionBuilds - beforeStats.ProjectionBuilds
		projectionVisits += afterStats.ProjectionTaskVisits - beforeStats.ProjectionTaskVisits
		sourceComparisons += afterStats.SourceTaskComparisons - beforeStats.SourceTaskComparisons
		shippedIDVisits += afterStats.ShippedIDVisits - beforeStats.ShippedIDVisits
		renderedCards += afterStats.RenderedCardRecords - beforeStats.RenderedCardRecords
		artifactHits += afterStats.NavigationArtifactHits - beforeStats.NavigationArtifactHits
		artifactMisses += afterStats.NavigationArtifactMisses - beforeStats.NavigationArtifactMisses
		artifactPublications += afterStats.NavigationArtifactPublications - beforeStats.NavigationArtifactPublications
		artifactFallbacks += afterStats.NavigationArtifactFallbacks - beforeStats.NavigationArtifactFallbacks
		watcherPollFastPaths += afterStats.WatcherPollFastPaths - beforeStats.WatcherPollFastPaths
		watcherVersionFastPaths += afterStats.WatcherVersionFastPaths - beforeStats.WatcherVersionFastPaths
		watcherStaleLoadFastPaths += afterStats.WatcherStaleLoadFastPaths - beforeStats.WatcherStaleLoadFastPaths
		watcherLifecycleFallbacks += afterStats.WatcherLifecycleFallbacks - beforeStats.WatcherLifecycleFallbacks
		renderImpactClassifications += afterStats.RenderImpactClassifications - beforeStats.RenderImpactClassifications
		synchronousLayout += afterStats.SynchronousLayoutRecords - beforeStats.SynchronousLayoutRecords
		temporalScheduled += afterStats.TemporalScheduledTicks - beforeStats.TemporalScheduledTicks
		temporalStale += afterStats.TemporalStaleTicks - beforeStats.TemporalStaleTicks
		temporalVisits += afterStats.TemporalTaskVisits - beforeStats.TemporalTaskVisits
		temporalRecords += afterStats.TemporalRecordsRefreshed - beforeStats.TemporalRecordsRefreshed
		temporalNodes += afterStats.TemporalIndexNodesVisited - beforeStats.TemporalIndexNodesVisited
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
			if stamps[event].StaleBoardLoad {
				staleBoardLoads++
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
	var convergence performanceDistribution
	if scenario.deferGeometry {
		convergence = performanceDurations(convergenceLatencies)
	}
	return performanceScenarioResult{
		Name:                           scenario.name,
		Tasks:                          count,
		Warmups:                        warmups,
		Samples:                        samples,
		Latency:                        performanceDurations(latencies),
		ConvergenceLatency:             convergence,
		AllocationsPerOp:               float64(allocations) / float64(samples),
		AllocatedBytesPerOp:            float64(allocatedBytes) / float64(samples),
		PublicationAllocatedBytesMax:   maximumPublicationBytes,
		RetainedPlanOwnedBytesEstimate: maximumRetainedPlanBytes,
		AcceptedMessages:               accepted,
		PublishedFrames:                frames,
		DiscardedEvents:                discarded,
		StaleBoardLoads:                staleBoardLoads,
		StaleWorkerResults:             stale,
		NormalBaseBuilds:               normalBases,
		DimmedBaseBuilds:               dimmedBases,
		OverlayCompositions:            compositions,
		ProjectionBuilds:               projectionBuilds,
		ProjectionTaskVisits:           projectionVisits,
		SourceTaskComparisons:          sourceComparisons,
		ShippedIDVisits:                shippedIDVisits,
		RenderedCardRecords:            renderedCards,
		NavigationArtifactHits:         artifactHits,
		NavigationArtifactMisses:       artifactMisses,
		NavigationArtifactPublications: artifactPublications,
		NavigationArtifactFallbacks:    artifactFallbacks,
		WatcherPollFastPaths:           watcherPollFastPaths,
		WatcherVersionFastPaths:        watcherVersionFastPaths,
		WatcherStaleLoadFastPaths:      watcherStaleLoadFastPaths,
		WatcherLifecycleFallbacks:      watcherLifecycleFallbacks,
		RenderImpactClassifications:    renderImpactClassifications,
		SynchronousLayoutRecords:       synchronousLayout,
		TemporalScheduledTicks:         temporalScheduled,
		TemporalStaleTicks:             temporalStale,
		TemporalTaskVisits:             temporalVisits,
		TemporalRecordsRefreshed:       temporalRecords,
		TemporalIndexNodesVisited:      temporalNodes,
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
	staleBoardLoad := false
	if loaded, ok := message.(boardLoadedMsg); ok {
		staleBoardLoad = loaded.generation != 0 && loaded.generation < model.loadGeneration &&
			after.InstalledSnapshotID == before.InstalledSnapshotID &&
			after.InstalledForMessageID == before.InstalledForMessageID
	}
	return []performancePublicationStamp{{
		Accepted:              after.AcceptedMessageID != before.AcceptedMessageID,
		AcceptedMessageID:     after.AcceptedMessageID,
		Published:             after.InstalledSnapshotID != before.InstalledSnapshotID,
		InstalledSnapshotID:   after.InstalledSnapshotID,
		InstalledForMessageID: after.InstalledForMessageID,
		followUp:              commands.followUp,
		geometry:              commands.geometry,
		temporal:              commands.temporal,
		StaleBoardLoad:        staleBoardLoad,
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
		followUp, geometry, temporal := stamps[index].followUp, stamps[index].geometry, stamps[index].temporal
		stamps[index].followUp = nil
		stamps[index].geometry = nil
		stamps[index].temporal = nil
		if followUp != nil {
			if scenario.settleFollowUp == nil {
				t.Fatalf("%s returned an unexpected non-worker command", scenario.name)
			}
			scenario.settleFollowUp(t, model, followUp)
		} else if scenario.requireFollowUp {
			t.Fatalf("%s did not return its required non-worker command", scenario.name)
		}
		if scenario.requireTemporal && temporal == nil {
			t.Fatalf("%s did not return its required temporal command", scenario.name)
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

func assertPerformanceAdmissionCounts(
	t *testing.T,
	name string,
	stamps []performancePublicationStamp,
	wantAccepted, wantDiscarded int,
) {
	t.Helper()
	accepted, published, discarded := 0, 0, 0
	for _, stamp := range stamps {
		if stamp.Accepted {
			accepted++
		}
		if stamp.Published {
			published++
		}
		if stamp.Discarded {
			discarded++
		}
	}
	if accepted != wantAccepted || published != wantAccepted || discarded != wantDiscarded {
		t.Fatalf("%s admission accepted=%d published=%d discarded=%d, want %d/%d/%d",
			name, accepted, published, discarded, wantAccepted, wantAccepted, wantDiscarded)
	}
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
		if result.Name == "held_navigation" &&
			(result.AcceptedMessages != 202 || result.PublishedFrames != 202 || result.DiscardedEvents != 606) {
			t.Errorf("held_navigation/%d accepted=%d published=%d discarded=%d, want 202/202/606",
				result.Tasks, result.AcceptedMessages, result.PublishedFrames, result.DiscardedEvents)
		}
		if violation := navigationArtifactAcceptanceViolation(result); violation != "" {
			t.Errorf("%s/%d navigation artifact acceptance: %s", result.Name, result.Tasks, violation)
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

func navigationArtifactAcceptanceViolation(result performanceScenarioResult) string {
	isRefresh := result.Name == "refresh_admission_new_issue" || result.Name == "refresh_25_task_burst"
	if result.Name != "held_navigation" && result.Name != "useful_wheel" && !isRefresh {
		return ""
	}
	if result.NavigationArtifactHits == 0 {
		return "recorded zero reuse hits"
	}
	if !isRefresh && result.NavigationArtifactPublications != result.PublishedFrames {
		return fmt.Sprintf("artifact publications=%d, published frames=%d",
			result.NavigationArtifactPublications, result.PublishedFrames)
	}
	if result.RenderedCardRecords != result.NavigationArtifactMisses {
		return fmt.Sprintf("rendered cards=%d, misses=%d",
			result.RenderedCardRecords, result.NavigationArtifactMisses)
	}
	if result.NavigationArtifactFallbacks != 0 {
		return fmt.Sprintf("fallbacks=%d, want zero", result.NavigationArtifactFallbacks)
	}
	if !isRefresh && result.NavigationArtifactHits <= result.NavigationArtifactMisses {
		return fmt.Sprintf("hits=%d do not exceed misses=%d",
			result.NavigationArtifactHits, result.NavigationArtifactMisses)
	}
	if result.Name == "held_navigation" {
		minimum := 2 * result.PublishedFrames
		maximum := 5 * result.PublishedFrames
		if result.NavigationArtifactMisses < minimum || result.NavigationArtifactMisses > maximum {
			return fmt.Sprintf("misses=%d outside held bound %d..%d",
				result.NavigationArtifactMisses, minimum, maximum)
		}
		return ""
	}
	if isRefresh {
		if result.NavigationArtifactPublications == 0 {
			return "recorded zero artifact publications"
		}
		maximum := 64 * result.NavigationArtifactPublications
		if result.NavigationArtifactMisses > maximum {
			return fmt.Sprintf("misses=%d exceed refresh bound %d",
				result.NavigationArtifactMisses, maximum)
		}
		return ""
	}
	if result.NavigationArtifactMisses != result.PublishedFrames {
		return fmt.Sprintf("misses=%d, want one entering-card miss per %d wheel frames",
			result.NavigationArtifactMisses, result.PublishedFrames)
	}
	return ""
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
			model.boardView.setCursorAtFrom(model.currentProjection(), 0,
				taskCount(model.board, board.StatusTodo)-1)
			model.rebuildRenderPlan(renderImpactAll)
			admission := newKeyboardAdmission(time.Now, model.inputAdmission, model.themeStyles().Timing)
			b.ReportAllocs()
			for b.Loop() {
				_ = admitPerformanceFilteredMessage(&model, admission, tea.KeyPressMsg{Code: tea.KeyDown})
			}
		})
	}
}
