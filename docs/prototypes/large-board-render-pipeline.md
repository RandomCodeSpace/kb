# Large-board render pipeline prototype

Status: disposable prototype for issue #250. This is evidence for the implementation plan, not production code.

## Question

Can the TUI keep startup and interaction latency effectively independent of a 120, 500, or 1000-task board by retaining the complete frame, rendering only visible cards, and measuring offscreen geometry progressively?

## Shape

The build-tagged `cmd/kb-render-prototype` command uses deterministic in-memory tasks and the production theme and card widget. It does not read `KB_DATA`, open the database, or persist changes.

The experiment implements these candidate boundaries:

- `View()` returns one retained complete `tea.View` without rendering.
- Projection records carry pre-normalized search text and status indexes.
- Only visible cards are styled and included in the immutable pointer hit map.
- Offscreen card heights are measured by one generation-checked command chain with an 8 ms work-slice target. Installing offscreen heights does not publish a frame.
- Accepted vertical navigation publishes at most 20 target frames per second during repeats. Repeats at the first and last task are discarded by the program input filter.
- Wheel events at a scroll boundary are discarded by the immutable view handler before `Update`.
- Hover changes publish only when the pointer crosses to a different target. Click selection, resize, filter, and overlay changes remain exact.

This prototype intentionally leaves production data loading, watcher integration, editing, drag/drop, and cache ownership untouched. Adding those would turn an architectural experiment into a second application, a traditional way to avoid learning anything.

## Run

Headless comparison:

```sh
GOTOOLCHAIN=go1.25.8 GOFLAGS=-buildvcs=false go run -tags prototype ./cmd/kb-render-prototype --bench
```

Physical terminal, large board:

```sh
GOTOOLCHAIN=go1.25.8 GOFLAGS=-buildvcs=false go run -tags prototype ./cmd/kb-render-prototype --tasks 1000
```

Controls:

- Arrow keys or `hjkl`: move selection.
- Mouse wheel: scroll the pointed column.
- Mouse motion: hover visible cards.
- `/`: enter a live filter; Enter accepts and Esc leaves filter entry.
- `?`: open or close the retained-backdrop help overlay.
- `q`: quit.

## Headless result

Measured on the development VM on 2026-08-26. Durations are process-local samples, not release benchmarks.

| Tasks | Startup p95 | All offscreen measurement | Largest slice | Accepted input plus frame p95 | Filter plus frame p95 | Retained-backdrop overlay p95 | Retained `View()` | Retained heap delta |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 120 | 8.92 ms | 4.36 ms | 4.36 ms | 7.07 ms | 6.60 ms | 0.37 ms | 5 ns | 0.8 MiB |
| 500 | 9.46 ms | 26.92 ms | 8.06 ms | 7.15 ms | 8.16 ms | 0.28 ms | 5 ns | 0.9 MiB |
| 1000 | 11.39 ms | 42.06 ms | 8.28 ms | 7.01 ms | 7.09 ms | 0.29 ms | 5 ns | 1.1 MiB |

For comparison, the accepted current-pipeline diagnostic measured a 1000-task `View()` at roughly 733-745 ms with about 323 MiB allocated and 1.98 million allocations per frame. The prototype removes the board-size slope from startup and accepted interaction. Total offscreen work still grows with task count, as physics remains regrettably installed, but the work is bounded per command and does not repaint the terminal.

## Physical-terminal gate

Run the 1000-task command in the actual terminal used for development, then report:

1. Hold Down until the last task and keep holding for at least two seconds. The cursor should stop without a delayed replay or frozen exit.
2. Repeat with Up at the first task.
3. Wheel rapidly in one column and keep wheeling at both scroll boundaries. Other input should remain immediate.
4. Move the pointer quickly across cards and empty space. Hover should follow without leaving the keyboard cursor behind.
5. Enter `/rendering`, backspace it, and clear it. Filtering should feel immediate and selection must remain valid.
6. Resize continuously across wide and narrow widths. The board should remain interactive and restore cleanly.
7. Toggle `?` repeatedly, move and wheel while it is open, close it, then quit. The terminal must restore its normal screen and pointer modes.
8. Compare 120 and 1000 tasks. The difference in interaction feel should be negligible.

The ticket remains open until this physical-terminal reaction is captured. Automated PTY output cannot establish whether held input feels correct. Machines are excellent at timing and famously indifferent to annoyance.

## Decision if the physical gate passes

Keep the selected architecture for the implementation plan:

1. Introduce one deep render-plan module owning normalized projection, layout records, prefix geometry, visible hits, and the retained complete view.
2. Make invalidation typed and Update-owned; keep `View()` O(1).
3. Render visible cards plus one viewport of overscan and progressively measure only unresolved offscreen geometry.
4. Enforce keyboard and pointer backpressure before expensive model work, with exact semantic actions and boundary discard.
5. Preserve current production behavior behind differential tests before replacing the current renderer.

If the physical gate fails, record the exact interaction and terminal before changing the architecture. The likely remaining boundary would be terminal output volume or input protocol behavior, not task projection.
