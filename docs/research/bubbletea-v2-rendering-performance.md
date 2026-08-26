# Bubble Tea v2.0.8 rendering and invalidation controls

Research for the Wayfinder ticket **Research Bubble Tea v2 rendering and
invalidation controls**. Verified on 2026-08-26 against Bubble Tea v2.0.8 at
commit [`fc707bb7`](https://github.com/charmbracelet/bubbletea/tree/fc707bb7ea0161405bb6c653ec93f6a9c6a72fe1),
the exact version selected by `kb`'s `go.mod`. All framework claims below cite
official upstream source or documentation. Statements labelled **Analysis** are
conclusions from those sources rather than an upstream API promise.

## Decision summary

Bubble Tea has two different costs that are easy to call "a render":

1. **Application view construction.** `Model.View()` runs synchronously after
   every accepted ordinary message, even when `Update` returns the same model
   and no command.
2. **Terminal rendering.** Bubble Tea retains only the latest submitted
   `tea.View`, flushes at a configurable maximum FPS, compares it with the last
   flushed view, builds a cell buffer when it changed, and lets the Cursed
   Renderer emit terminal differences.

The renderer optimizes terminal output. It does not suppress or memoize the
application's `Model.View()` work. Bubble Tea v2.0.8 exposes no dirty flag,
`NoRedraw` command, partial-view API, public renderer replacement, or
post-`Update` equality hook.

The supported seam for `kb` is therefore **application-owned memoization and
virtualization behind the normal `Model.View() tea.View` contract**. Cache pure
subview strings, measurements, and the immutable hit map that was derived from
them; compose them in the existing z-order into one complete `tea.View`; and
let Bubble Tea retain terminal state and restore the terminal. Lowering the
renderer FPS alone cannot fix expensive `View()` construction.

## 1. Exactly when `Model.View()` runs

The v2 model contract says that the view is rendered after every `Update`
([`tea.go`, model interface](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/tea.go#L52-L65)).
The event loop makes the exact order explicit
([`tea.go`, event loop](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/tea.go#L741-L889)):

```text
message received
  -> translate input
  -> WithFilter callback
       -> nil: stop; no Update and no View
  -> Bubble Tea internal handling
       -> concrete mouse event: last flushed View.OnMouse runs
  -> Model.Update(message)
  -> returned command is handed to the command runner
  -> Model.View()
  -> latest tea.View replaces the renderer's pending view
  -> FPS tick compares and flushes the latest pending view
```

The lifecycle adds an initial view after `Init()` is scheduled and, on a
graceful exit, one final view before terminal shutdown
([`tea.go`, `Program.Run`](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/tea.go#L1098-L1173)).

| Message path | `Model.Update` | `Model.View` |
|---|---:|---:|
| Initial program state | no | once |
| Ordinary accepted message | once | once |
| Accepted concrete mouse message | once, after `OnMouse` | once, after `OnMouse` |
| Message changed by `WithFilter` | once with replacement | once |
| Message dropped by `WithFilter` | no | no |
| `BatchMsg` / sequence carrier | no | no |
| Each ordinary result produced by a batch or sequence | once | once |
| `QuitMsg` on a graceful run | no | once as the final view |
| Context cancellation, kill, or event-loop error | no final update | no final view |

There is no model-identity comparison between `Update` and `View`. Returning an
unchanged value and `nil` command still reaches `p.render(model)`. `WithoutRenderer`
selects a no-op renderer, but `p.render` still evaluates `model.View()` before
calling it; it is not a view-construction benchmark switch
([`options.go`, `WithoutRenderer`](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/options.go#L90-L102),
[`tea.go`, `p.render`](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/tea.go#L885-L890)).

## 2. What the renderer retains and suppresses

`p.render(model)` first computes the complete `tea.View`. The Cursed Renderer's
`render` method then only assigns that value to a single pending slot
([`cursed_renderer.go`, `render`](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/cursed_renderer.go#L578-L584)).
The program's FPS ticker flushes that slot; the default is 60 FPS and the public
option is capped at 120 FPS
([`renderer.go`, FPS constants](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/renderer.go#L10-L15),
[`options.go`, `WithFPS`](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/options.go#L139-L146),
[`tea.go`, renderer ticker](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/tea.go#L1392-L1421)).

**Analysis:** multiple messages inside one FPS interval can cause multiple
`Model.View()` calls while only the latest resulting view reaches the terminal.
`WithFPS` controls terminal flush frequency, not application view frequency.

At flush time, Bubble Tea:

1. Creates a styled representation of the full `View.Content`.
2. Skips further work if the current and last views compare equal and their
   frame bounds match.
3. Otherwise clears the full application cell buffer, draws the full content
   into it, and hands that buffer to the retained terminal diff renderer.

The relevant implementation is
[`cursed_renderer.go`, flush setup and equality fast path](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/cursed_renderer.go#L256-L311)
and
[`cursed_renderer.go`, terminal render and output](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/cursed_renderer.go#L460-L575).
The upstream renderer dependency retains a current buffer and transforms
touched lines against it rather than blindly writing a whole terminal screen
([`ultraviolet`, `TerminalRenderer.Render`](https://github.com/charmbracelet/ultraviolet/blob/006e29f97886822b5d62d373b29f3143a7220ac3/terminal_renderer.go#L1242-L1369)).

This gives Bubble Tea useful byte-level and cursor-movement savings. It does not
provide retained application subviews: the application already constructed one
complete ANSI string, and a changed string is parsed and drawn into the full
frame buffer before the terminal diff is emitted.

### Equality has an `OnMouse` consequence

The renderer's equality function compares content and terminal-state fields,
but deliberately does not compare the `OnMouse` function
([`cursed_renderer.go`, `viewEquals`](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/cursed_renderer.go#L803-L842)).
If content and the compared fields are identical, the flush returns before
replacing `lastView`.

**Analysis:** a newly-created `OnMouse` closure is not installed merely because
the closure identity or captured hit map changed. Hit regions and content must
be treated as one immutable render snapshot. If hit geometry changes, the
visible content or another compared view field must also change; changing a
handler alone while returning identical content leaves the last flushed handler
active.

## 3. `View.OnMouse` and cell-motion input

`View.OnMouse` is documented as a handler for mouse behavior that depends on
content from the last render
([`tea.go`, `View.OnMouse`](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/tea.go#L82-L127)).
The source implements that wording literally: `onMouse` reads the callback from
`lastView`, not the latest pending view
([`cursed_renderer.go`, `onMouse`](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/cursed_renderer.go#L765-L777)).
This keeps coordinates aligned with what the terminal actually shows when
several model updates arrive between renderer flushes.

For each accepted concrete `MouseClickMsg`, `MouseReleaseMsg`, `MouseWheelMsg`,
or `MouseMotionMsg`, the event loop runs that callback before passing the same
raw mouse message to `Model.Update`. If the callback returns a command, Bubble
Tea starts it in a goroutine and sends its result as another program message
([`tea.go`, mouse dispatch](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/tea.go#L800-L808)).

Consequences:

- The raw mouse message still causes its own `Update` and `View`, even when the
  model ignores it.
- A non-nil callback command normally produces another message, then another
  `Update` and `View`.
- The callback result is asynchronous. Code must not assume it is processed
  before a later independent input message.
- Because `WithFilter` runs before internal mouse dispatch, returning `nil`
  suppresses both `OnMouse` and the model update. It cannot let `OnMouse` run
  and then suppress only the raw message's view.

`MouseModeCellMotion` enables clicks, releases, wheels, and movement while a
button is pressed. Buttonless pointer motion requires `MouseModeAllMotion`
([`tea.go`, mouse-mode definitions](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/tea.go#L283-L305)).
Therefore a cell-motion application is not paying for idle hover traffic; it can
still receive a dense drag stream and wheel stream.

## 4. Filters and commands

`WithFilter` is the only public message-level control that can prevent both
`Update` and `View`: it runs before Bubble Tea's internal handling and a `nil`
result drops the message
([`options.go`, `WithFilter`](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/options.go#L104-L137),
[`tea.go`, filter position](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/tea.go#L752-L761)).
It is appropriate for coalescing genuinely redundant input, but it is not a
state invalidation API. It has no access to a post-`Update` "did visible state
change?" result.

A `Cmd` is an I/O operation that returns one message
([`tea.go`, `Cmd`](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/tea.go#L383-L390)).
Regular commands run asynchronously and their returned messages re-enter the
same filtered event loop
([`tea.go`, command runner](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/tea.go#L698-L733)).
`Batch` runs commands concurrently with no result-order guarantee; `Sequence`
runs commands one at a time, in order
([`commands.go`, batch and sequence contracts](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/commands.go#L7-L30),
[`tea.go`, executors](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/tea.go#L892-L958)).

**Analysis:** cache invalidation that is pure and local should happen
synchronously with the state transition in `Update`, not through a command.
Moving it through `Cmd` adds a message, another view, and asynchronous ordering.
If an expensive computation must be asynchronous, tag its request/result with a
generation and discard stale results in `Update`; use `Sequence` only where
result order is a real contract.

## 5. Supported caching and retention seam

Bubble Tea v2's supported public composition boundary is a complete `tea.View`
whose `Content` is a string plus declarative terminal state such as `AltScreen`,
`MouseMode`, and `OnMouse`
([`tea.go`, `View`](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/tea.go#L67-L190),
[`UPGRADE_GUIDE_V2.md`, declarative views](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/UPGRADE_GUIDE_V2.md#the-big-idea-declarative-views)).
The renderer interface and renderer field are internal, so replacing or
partially driving the renderer is not a supported application API
([`renderer.go`, internal interface](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/renderer.go#L17-L57)).

The safe design is:

1. Keep a render snapshot or component caches owned by one model instance.
2. Key each cached subview by every input that can alter its bytes or geometry:
   data revision, filter/project, width, theme/profile, selection, pointer
   feedback, scroll position, and overlay/dim state as applicable.
3. Cache rendered card strings and measured heights separately from their board
   placement. Virtualize before expensive styling: compose only cards that can
   intersect the visible column viewport.
4. Build the final content in the existing fixed z-order. A cache hit may reuse
   component bytes; it must not reorder board, backdrop, overlays, footer, or
   pointer regions.
5. Generate `Content` and the immutable `OnMouse` hit map from the same snapshot
   and cache/invalidate them together.
6. Return a complete `tea.View` every time, including declarative terminal
   fields. Let Bubble Tea own terminal writes and cleanup.

Direct writes are not a retained-subview mechanism. Bubble Tea's `Println` and
`Printf` output is unmanaged and disabled in alternate-screen mode
([`renderer.go`, unmanaged output](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/renderer.go#L63-L92)).
Interactive child programs should use `ExecProcess`, which pauses the program,
releases the terminal, runs the child, restores the terminal, and resumes
([`exec.go`, `ExecProcess`](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/exec.go#L15-L52),
[`exec.go`, release/restore implementation](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/exec.go#L101-L128)).
Neither path should be repurposed to bypass the normal renderer for cached UI.

## 6. Implications for `kb`

The current `kb` integration:

- constructs the root program with a `WithFilter` input coalescer
  ([`internal/tui/run.go`](https://github.com/RandomCodeSpace/kb/blob/7fb3c15987020bc4e940cb1bbf01c543f727697e/internal/tui/run.go#L108-L117));
- drops accepted motion events inside a 16 ms window and re-injects coalesced
  wheel notches as program messages
  ([`internal/tui/input_coalesce.go`](https://github.com/RandomCodeSpace/kb/blob/7fb3c15987020bc4e940cb1bbf01c543f727697e/internal/tui/input_coalesce.go#L61-L165));
- rebuilds board content and hit regions in every root `View`, then returns a
  cell-motion, alternate-screen `tea.View`
  ([`internal/tui/model.go`](https://github.com/RandomCodeSpace/kb/blob/7fb3c15987020bc4e940cb1bbf01c543f727697e/internal/tui/model.go#L1021-L1092)); and
- emits a one-second polling message even when the board did not change
  ([`internal/tui/model.go`](https://github.com/RandomCodeSpace/kb/blob/7fb3c15987020bc4e940cb1bbf01c543f727697e/internal/tui/model.go#L783-L789),
  [`internal/tui/model.go`](https://github.com/RandomCodeSpace/kb/blob/7fb3c15987020bc4e940cb1bbf01c543f727697e/internal/tui/model.go#L826-L854),
  [`internal/tui/model.go`](https://github.com/RandomCodeSpace/kb/blob/7fb3c15987020bc4e940cb1bbf01c543f727697e/internal/tui/model.go#L1194-L1202)).

The program does not pass `tea.WithFPS`, so Bubble Tea's renderer uses its
60-FPS default. `kb`'s 20-FPS theme token controls its animation message cadence,
not Bubble Tea's terminal flush ticker.

**Analysis:** for an accepted mouse event whose `OnMouse` callback returns a
derived pointer message, `kb` commonly constructs the full board once for the
otherwise-ignored raw mouse message and again for the derived message. Filtering
reduces event count, and filtering the board reduces work inside each view, but
neither changes the framework rule that every accepted ordinary message invokes
`View()`.

This source review supports the following implementation direction, subject to
profiling in the separate performance ticket:

- make board filtering/projection and card measurement/rendering explicitly
  cacheable;
- virtualize offscreen cards before rendering them;
- make polling and pointer no-op paths return cheap cached snapshots;
- preserve `Content` plus hit regions as one render snapshot; and
- keep the normal Bubble Tea program lifecycle and renderer.

It does **not** support treating `WithFPS`, renderer diffing, or returning an
unchanged model as a solution to expensive application view construction.

## Source index

- Bubble Tea v2.0.8 tag and release:
  <https://github.com/charmbracelet/bubbletea/releases/tag/v2.0.8>
- Bubble Tea event loop and lifecycle:
  <https://github.com/charmbracelet/bubbletea/blob/v2.0.8/tea.go>
- Bubble Tea Cursed Renderer:
  <https://github.com/charmbracelet/bubbletea/blob/v2.0.8/cursed_renderer.go>
- Bubble Tea program options:
  <https://github.com/charmbracelet/bubbletea/blob/v2.0.8/options.go>
- Bubble Tea command combinators:
  <https://github.com/charmbracelet/bubbletea/blob/v2.0.8/commands.go>
- Bubble Tea v2 upgrade guide:
  <https://github.com/charmbracelet/bubbletea/blob/v2.0.8/UPGRADE_GUIDE_V2.md>
- Bubble Tea interactive execution and terminal restoration:
  <https://github.com/charmbracelet/bubbletea/blob/v2.0.8/exec.go>
- Ultraviolet terminal diff selected by current `kb` dependencies:
  <https://github.com/charmbracelet/ultraviolet/blob/006e29f97886822b5d62d373b29f3143a7220ac3/terminal_renderer.go>
