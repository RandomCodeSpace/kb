# TUI large-board performance baseline

Date: 2026-08-26  
Commit: `7fb3c15987020bc4e940cb1bbf01c543f727697e`  
Host: Linux/amd64, 8 vCPU, AMD EPYC 9354P, Go 1.25.8

## Result

The lag is reproducible. The dominant cost is application-side frame
construction, not SQLite, terminal diffing, or a sustained memory leak.

At the end of a column, a repeated Down key leaves selection unchanged but
still rebuilds the complete frame. Its measured input-to-frame p95 was 113 ms
at 120 tasks, 398 ms at 500 tasks, and 799 ms at 1,000 tasks. Those values miss
the Wayfinder destination of p95 below 50 ms and p99 below 100 ms.

An actionable wheel event commonly costs two application frames: the accepted
raw mouse message and the derived application message. At 120 tasks that path
was about 197-205 ms; at 1,000 tasks it was 1.45-1.55 s.

## Workload

The committed harness in `internal/tui/performance_baseline_test.go` generates
deterministic boards at 17, 120, 500, and 1,000 tasks. Tasks are distributed
evenly across Todo, Doing, and Done and include wrapping titles, descriptions,
emoji, three labels, two checklist items, due dates, effort, priority, and
blocked states. The reference frame is 211 by 52 cells.

The current default development data directory is `~/.local/share/kb` because
`KB_DATA` is unset. It contained 9 tasks during this measurement. `kb tui`
launches that board without changing configuration.

For a disposable large board, use a path that does not already exist:

```sh
KB_PERF_FIXTURE_DIR=/tmp/kb-perf-120 KB_PERF_TASKS=120 \
  go test ./internal/tui -run '^TestWriteLargeBoardFixture$' -count=1 -v
KB_DATA=/tmp/kb-perf-120 kb tui
```

The generator refuses an existing fixture path so it cannot silently append to
real board data.

## Measurements

Ranges below are three runs unless stated otherwise. `allocs/op` includes the
complete immutable `tea.View` construction and its mouse hit map.

| Path | 17 tasks | 120 tasks | 500 tasks | 1,000 tasks |
|---|---:|---:|---:|---:|
| Full `Model.View` | 19.7-22.0 ms | 97.7-107.5 ms | 373.2-389.4 ms | 732.5-744.9 ms |
| Full-view allocations | 40,324-40,330 | 245,678-245,685 | 996,435-996,451 | 1,984,714-1,984,757 |
| Input plus view, useful key | - | 94.1-101.3 ms | 374.3-403.1 ms | 726.5-740.3 ms |
| Input plus view, clamped key | - | 98.5-106.2 ms | 359.0-371.8 ms | 715.3-754.4 ms |
| Pointer motion plus view | - | 93.2-101.2 ms | 358.8-373.9 ms | 757.2-794.5 ms |
| Actionable wheel sequence | - | 197.0-204.6 ms | 744.6-811.3 ms | 1,453.3-1,552.3 ms |
| Help overlay view | - | 97.4-104.1 ms | 332.9-364.0 ms | 684.8-705.9 ms |
| SQLite `Store.Board` | - | 1.2-1.9 ms | 4.2-4.8 ms | 9.2-11.5 ms |
| Store read plus first interactive model frame | - | 102.6-109.5 ms | 376.5-393.5 ms | 730.3-770.1 ms |

The first-interactive benchmark is a lower-bound proxy: it includes the SQLite
read, model construction, window sizing, and first usable view. It excludes Go
process startup, PTY setup, and the terminal write. Consequently, 1,000 tasks
is too close to the 1 s destination to claim startup compliance.

Filtering confirms the user's observation. Keeping exactly 17 matches costs
25.9-27.1 ms from 120 total tasks, 37.2-41.7 ms from 500, and 45.2-52.4 ms from
1,000. Matching work therefore still scales with total board size even when
render work scales mostly with the smaller result.

At 1,000 total tasks, the isolated filter projection took 7.5-8.8 ms and
allocated about 7.8 MB. For one 334-card column, card measurement took
36.7-40.1 ms and card rendering took 163.6-173.5 ms. The board path performs
these operations for every visible column and may measure a column twice after
reserving its scrollbar.

## Settled idle process

An eight-second PTY launch was measured with `/usr/bin/time` and no input. CPU
percent is the tool's one-core convention; the parenthetical value normalizes
it across this 8-vCPU VM.

| Board | Process CPU | Approx. VM capacity | Max RSS |
|---|---:|---:|---:|
| Default dev data, 9 tasks | 4% | 0.5% | 52.9 MiB |
| Synthetic 120 | 14% | 1.75% | 58.5 MiB |
| Synthetic 500 | 41% | 5.1% | 73.7 MiB |
| Synthetic 1,000 | 67% | 8.4% | 94.1 MiB |

The settled event loop polls once per second. An unchanged poll produces a
poll message and a data-version result; Bubble Tea asks for a view after each.
The benchmarked service demand for that pair was 193-212 ms at 120 tasks,
736-751 ms at 500, and 1.46-1.49 s at 1,000. This explains why the default and
120-task boards may not look like a CPU spike while the large board is still
doing substantial periodic work.

## CPU profile

A three-frame CPU profile of the 1,000-task view attributed 82.8% cumulative
time to `Model.View` and `renderBoard`, with 81.7% in
`renderBoardColumnAt`. Within that path:

- `renderTaskLines`: 53.9% cumulative;
- `measureCards`: 27.7% cumulative;
- Lip Gloss `Style.Render`: 43.8% cumulative;
- ANSI string-width calculation: 19.5% cumulative;
- `cardOptsFor`: 13.9% cumulative;
- background GC work: 14.2% cumulative.

The frame allocated about 323 MB and 1.98 million objects. The allocation rate
is sufficient to make GC part of the visible latency even though retained RSS
does not continuously climb.

## Hypothesis verdicts

1. Confirmed: full-frame construction dominates and scales with matched cards.
2. Confirmed: repeated keys can arrive faster than frames complete; clamped
   repeats cost the same as useful navigation.
3. Confirmed: pointer traffic pays the accepted-message view tax, and an
   actionable wheel event commonly pays for a second view.
4. Confirmed: SQLite is a small part of the first interactive frame; rendering
   is the startup bottleneck at these sizes.
5. Confirmed: filtering reduces card rendering but still projects the full
   source board and allocates proportionally to total tasks.

## Planning consequences

The next decisions need to cover all of the following. None can be declared
optional by fixing only keyboard repeat.

- Establish a rendering boundary that does not measure and render offscreen
  cards while preserving content-sized card heights and pointer hit geometry.
- Define frame invalidation so unchanged polls, clamped navigation, and
  equivalent state do not rebuild the board.
- Define key-repeat backpressure separately from the semantic no-op rule at
  list boundaries.
- Define pointer backpressure for motion and wheel traffic, including the raw
  versus derived two-frame path.
- Define immutable caches for filtering, card measurement, rendered content,
  and hit maps with explicit invalidation keys.
- Add a regression gate for the 120, 500, and 1,000-task corpora using the
  existing p95, p99, startup, and settled-idle destinations.

## Reproduction

The acceptance loop is deliberately red on this baseline:

```sh
KB_PERF_ACCEPT=1 GOTOOLCHAIN=go1.25.8 go test ./internal/tui \
  -run '^TestLargeBoardInputToFrameBudget$' -count=1 -v
```

The benchmark suite is:

```sh
GOTOOLCHAIN=go1.25.8 go test ./internal/tui -run '^$' \
  -bench '^BenchmarkLargeBoard' -benchmem -benchtime=5x -count=3
```

Generate a CPU profile for the largest frame with:

```sh
GOTOOLCHAIN=go1.25.8 go test ./internal/tui -run '^$' \
  -bench '^BenchmarkLargeBoardView/tasks_1000$' -benchtime=3x \
  -cpuprofile=/tmp/kb-view-1000.cpu
go tool pprof -top -cum /tmp/kb-view-1000.cpu
```
