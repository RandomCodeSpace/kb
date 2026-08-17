# Bubble Tea stack research (#82)

Research for wayfinder ticket #82: pinning the charmbracelet TUI stack, multi-pane
architecture, mouse support, teatest, and SQLite `PRAGMA data_version` for
cross-process change detection.

This file creates the `docs/research/` directory; the repo had no research docs
before this. All findings verified against primary sources on 2026-08-17: the Go
module proxy, charmbracelet repositories and release notes, SQLite documentation
and source, and the modernc.org/sqlite (cznic/sqlite) repository. Claims cite the
source that owns them; anything inferred rather than quoted is marked as analysis.

---

## 1. Versions, module paths, licenses

Resolved via `proxy.golang.org/<module>/@latest` and each tag's `go.mod`
(2026-08-17). Licenses via the GitHub API (`spdx_id`) and the GitLab projects API.

| Library | Canonical module path (v2 era) | Latest stable | Released | License |
|---|---|---|---|---|
| Bubble Tea | `charm.land/bubbletea/v2` | v2.0.8 | 2026-07-03 | MIT |
| Bubbles | `charm.land/bubbles/v2` | v2.1.1 | 2026-07-04 | MIT |
| Lip Gloss | `charm.land/lipgloss/v2` | v2.0.6 | 2026-08-11 | MIT |
| Glamour | `charm.land/glamour/v2` | v2.0.1 | 2026-06-12 | MIT |
| teatest | `github.com/charmbracelet/x/exp/teatest/v2` | v2.0.0-20260816001655-68d539dca504 (pseudo-version, untagged) | 2026-08-16 commit | MIT (charmbracelet/x) |
| modernc.org/sqlite | `modernc.org/sqlite` | v1.56.0 (embeds SQLite 3.53.3) | 2026-08-03 | BSD-3-Clause |

Sources:
- Proxy `@latest` for each module, e.g. <https://proxy.golang.org/charm.land/bubbletea/v2/@latest> → v2.0.8; <https://proxy.golang.org/modernc.org/sqlite/@latest> → v1.56.0.
- Canonical paths from each tag's `go.mod` `module` line: <https://raw.githubusercontent.com/charmbracelet/bubbletea/v2.0.8/go.mod> (`module charm.land/bubbletea/v2`), same pattern for bubbles v2.1.1, lipgloss v2.0.6, glamour v2.0.1. teatest v2 still declares `module github.com/charmbracelet/x/exp/teatest/v2` (<https://github.com/charmbracelet/x/tree/main/exp/teatest/v2>).
- Licenses: GitHub API for charmbracelet/{bubbletea,bubbles,lipgloss,glamour,x} all report `MIT`; GitLab API for cznic/sqlite reports BSD-3-Clause (<https://gitlab.com/cznic/sqlite>).
- SQLite 3.53.3 embedded: `doc.go` platform table at <https://gitlab.com/cznic/sqlite/-/blob/v1.56.0/doc.go>.

### The v2 situation

- The whole Charm stack went v2-stable on 2026-02-24 (bubbletea v2.0.0, bubbles
  v2.0.0, lipgloss v2.0.0 all tagged that day; glamour v2.0.0 on 2026-03-09) —
  proxy `.info` metadata for each `v2.0.0` tag.
- v2 moved the canonical import paths to the `charm.land` vanity domain. The v2.0.0
  release notes state: `import tea "charm.land/bubbletea/v2"` replaces
  `github.com/charmbracelet/bubbletea` (<https://github.com/charmbracelet/bubbletea/releases/tag/v2.0.0>).
  Because each v2 `go.mod` declares the `charm.land` path, that path is the only
  one `go get` accepts for v2 (analysis: Go's module path verification).
- v1 lines are maintenance-only: bubbletea v1.3.10 (2025-09-17), lipgloss v1.1.0
  (2025-03-12); bubbles and glamour tagged a final v1.0.0 (2026-02-09 and
  2025-11-24) before v2.
- **Pin for a new project today: the v2 line.** v2.0.0 shipped an upgrade guide
  (`UPGRADE_GUIDE_V2.md` in the repo) and six months of patch releases have
  followed (v2.0.8 current).
- teatest has never been tagged (`/@v/list` is empty for both major lines); it
  lives in the experimental `exp/` tree of charmbracelet/x and is pinned by
  pseudo-version. Use the `/v2` variant with bubbletea v2.
- Toolchain floor: bubbletea/bubbles v2 declare `go 1.25.0`; glamour v2.0.1
  declares `go 1.25.8` (its `go.mod`), so importing glamour v2 raises kb's
  `go` directive from the current 1.25.0.

## 2. Multi-pane architecture

### What the framework itself demonstrates

Bubble Tea is Elm architecture: one root `Model` with `Init/Update/View`, and the
official pattern for multiple panes is **composed sub-models plus an explicit
focus enum in the root model**. The bundled `examples/composable-views` does
exactly this: a `mainModel` holds `timer.Model` and `spinner.Model` fields plus a
`state sessionState` ("used to track which model is focused"); `Update` handles
global keys (quit, tab-to-switch-focus) itself, then relays the message only to
the focused sub-model (`m.spinner, cmd = m.spinner.Update(msg)`), batching
returned commands (<https://github.com/charmbracelet/bubbletea/blob/main/examples/composable-views/main.go>).

### gh-dash (dlvhdr/gh-dash, ~12.3k stars, already on charm.land v2)

<https://github.com/dlvhdr/gh-dash/blob/main/internal/tui/ui.go>

- Root `Model` in `internal/tui` composes many sub-models as fields: `sidebar
  sidebar.Model`, `prView prview.Model`, `footer footer.Model`, `tabs
  tabs.Model`, plus slices of a `section.Section` interface for the pane list
  (`prs`, `issues`, `notifications []section.Section`) and `currSectionId int`
  for focus.
- Layout: one package per component under `internal/tui/components/` (sidebar,
  footer, tabs, prssection, issuessection, section, ...), shared helpers in
  `internal/tui/{common,context,keys,theme,markdown}`.
- Cross-cutting state travels via a shared `*context.ProgramContext` pointer
  handed to every sub-model constructor — dimensions, theme, config, and a
  `StartTask func(task) tea.Cmd` hook so sub-models schedule async work without
  owning it.
- Mouse hit-testing uses `github.com/lrstanley/bubblezone/v2` (zone-marking
  rendered regions so click coordinates map back to components).

### crush (charmbracelet/crush — Charm's own production agent TUI)

Charm documents its architecture in-repo at
<https://github.com/charmbracelet/crush/blob/main/internal/ui/AGENTS.md>. Notable:
they deliberately do **not** run nested Elm models everywhere:

- "The `UI` model is the **sole Bubble Tea model**." Sub-components (`Chat`,
  `List`, `Attachments`, ...) are stateful structs with imperative methods the
  root calls directly; several "have no `Update` method at all".
- Root model owns: message routing ("giant `switch msg.(type)` in `Update`"),
  focus (`focus uiFocusState` — `uiFocusNone | uiFocusEditor | uiFocusMain`),
  UI state machine (`state uiState`), and rectangle-based layout
  (`layout uiLayout` with `header/main/editor/sidebar/pills/status` rects).
- Rules they enforce: "Never do IO or expensive work in `Update`; always use a
  `tea.Cmd`", "Never change the model state inside of a command. Use messages
  and update the state in the main `Update` loop", "do not nest models".
- Components expose `Render(width int) string` / `Draw(scr, area)` plus
  imperative mutators returning `tea.Cmd` when side effects are needed.

### superfile (yorukot/superfile)

<https://github.com/yorukot/superfile/tree/main/src/internal> — a single large
`model.go` root model with handler files split by concern
(`handle_panel_movement.go`, `handle_panel_navigation.go`, `handle_modal.go`,
`key_function.go`, `model_render.go`) rather than per-pane packages; panel focus
handled in the root (`model_panel_focus_test.go` covers it).

### Synthesis (analysis)

Three production codebases, one spectrum: gh-dash = many true sub-models glued by
a shared context pointer; crush = one root model commanding dumb-ish stateful
components; superfile = one flat model split across files. Common to all:

1. Exactly one place owns focus (an enum/int in the root model).
2. The root `Update` routes messages: global keys first, then forward to the
   focused pane only; broadcast layout/size changes to all panes.
3. Async work always flows through `tea.Cmd` → message → root `Update`.
4. Panes get their dimensions told to them (context pointer or explicit
   `SetSize`), they never measure the terminal themselves.

## 3. Mouse support

APIs verified against the bubbletea **v2.0.8** source.

### Enabling — declarative in v2

v1's program options `WithMouseCellMotion()` / `WithMouseAllMotion()`
(<https://github.com/charmbracelet/bubbletea/blob/v1.3.10/options.go>) and the
`tea.EnableMouseCellMotion` command family are **gone in v2**. The v2.0.0 release
notes: "In v1, you'd toggle terminal features on and off with commands like
`tea.EnterAltScreen`, `tea.EnableMouseCellMotion` ... In v2, all of that is gone
and replaced by fields on the `View` struct"
(<https://github.com/charmbracelet/bubbletea/releases/tag/v2.0.0>).

In v2, `Model.View()` returns a `tea.View` struct; set its `MouseMode` field
(<https://github.com/charmbracelet/bubbletea/blob/v2.0.8/tea.go>):

- `MouseModeNone` — disabled (default).
- `MouseModeCellMotion` — "enables mouse click, release, and wheel events. Mouse
  movement events are also captured if a mouse button is pressed" (i.e. drag).
- `MouseModeAllMotion` — all of the above plus motion with no button held.
- Doc comment: cell motion "will try to enable the mouse in extended mode (SGR),
  if that is not supported by the terminal it will fall back to normal mode
  (X10)".

### Event API (v2)

From <https://github.com/charmbracelet/bubbletea/blob/v2.0.8/mouse.go>:

- Concrete messages: `tea.MouseClickMsg`, `tea.MouseReleaseMsg`,
  `tea.MouseWheelMsg`, `tea.MouseMotionMsg` — each is a rename of `tea.Mouse`,
  which carries `X, Y int` (zero-based, (0,0) top-left), `Button MouseButton`,
  `Mod KeyMod`.
- `tea.MouseMsg` is an interface (`Mouse() Mouse`) implemented by all four —
  match it to catch everything, or a concrete type to catch one kind.
- Buttons: `MouseLeft/Middle/Right`, `MouseWheelUp/Down/Left/Right`,
  `MouseBackward/Forward`, `MouseButton10/11` ("based on X11 mouse button
  codes"; "Other buttons are not supported").
- Drag handling = `MouseModeCellMotion` + tracking state across
  `MouseClickMsg` → `MouseMotionMsg`(s) → `MouseReleaseMsg` yourself; crush's
  `internal/ui/model/chat.go` does exactly this ("mouse tracking (drag,
  double/triple click)") per its AGENTS.md.
- v2 also adds `View.OnMouse func(MouseMsg) tea.Cmd` — an optional hook to
  "intercept mouse messages that depends on view content from last render",
  i.e. hit-testing against what was actually drawn without breaking
  unidirectional flow (`tea.go` doc comment with worked example).

### Caveats / limitations

- Coordinate-to-widget mapping is entirely yours; production apps reach for
  `bubblezone` (gh-dash imports `lrstanley/bubblezone/v2`) or `View.OnMouse`.
- Terminal support varies: the source documents the SGR-to-X10 fallback (above).
  X10 mode's coordinate encoding caps positions at 223 columns/rows (analysis:
  property of the X10 protocol, hence the SGR preference).
- `MouseModeAllMotion` streams motion events continuously — high message volume;
  crush caches rendered items to keep redraws cheap (AGENTS.md).
- unverified beyond common terminal behavior: while the app captures the mouse,
  native terminal text selection needs a modifier (usually shift). Not stated in
  bubbletea docs; plan UX accordingly.

## 4. teatest

Package: `github.com/charmbracelet/x/exp/teatest/v2` (untagged, experimental —
see §1). API verified from source at commit `68d539d`
(<https://github.com/charmbracelet/x/blob/main/exp/teatest/v2/teatest.go>);
usage patterns from Charm's announcement post (<https://charm.land/blog/teatest/>).

Exported surface: `NewTestModel(tb, model, ...TestOption)`,
`WithInitialTermSize(x, y)`, `WithProgramOptions(...tea.ProgramOption)`,
`TestModel.Send(tea.Msg)`, `.Type(string)`, `.Output()`, `.Quit()`,
`.FinalModel(tb, ...FinalOpt)`, `.FinalOutput(tb, ...FinalOpt)`,
`.WaitFinished(tb, ...FinalOpt)`, `.GetProgram()`,
`WaitFor(tb, reader, condition, ...WaitForOption)` with `WithCheckInterval(d)`
and `WithDuration(d)`, `WithFinalTimeout(d)` / `WithTimeoutFn(fn)`, and
`RequireEqualOutput(tb, out)`.

Patterns from the blog post:

- **Full-run golden test**: run the model, `out := tm.FinalOutput(t)`, then
  `teatest.RequireEqualOutput(t, out)`; regenerate goldens with
  `go test ./... -update`. Golden files come from `charmbracelet/x/exp/golden`.
- **Intermediate assertion**: `teatest.WaitFor(t, tm.Output(), func(bts []byte)
  bool { return bytes.Contains(bts, ...) }, teatest.WithCheckInterval(
  time.Millisecond*100), teatest.WithDuration(time.Second*3))` — poll output
  until a condition or deadline.
- **Interaction**: `tm.Send(...)` key/mouse messages (any `tea.Msg`, so v2
  mouse messages work), `tm.Type("q")` for text; finish with
  `tm.WaitFinished(t, teatest.WithFinalTimeout(time.Second))`.
- **Deadlines**: every `Final*`/`WaitFor` accepts a timeout option; default
  failure calls `tb.Fatal`, overridable via `WithTimeoutFn`.
- **CI stability**: force a fixed color profile so golden bytes match across
  environments (v2: `tea.WithColorProfile(...)` program option — v2.0.8
  `options.go`; the blog's v1 recipe was `lipgloss.SetColorProfile(
  termenv.Ascii)`), and add `*.golden -text` to `.gitattributes` so git leaves
  line endings alone.

### Coverage reality check

No primary source shows a production TUI holding 95%+ statement coverage, and
none of the three studied apps advertises a coverage bar:

- Charm's own bubbles tests against golden files (`x/exp/golden` is a test
  dependency in bubbles v2.1.1 `go.mod`).
- crush has extensive focused unit tests in `internal/ui/model/` (layout,
  permission, mouse: `attachments_mouse_test.go`, `layout_test.go`, ...).
- superfile unit-tests root-model behavior per concern
  (`model_panel_focus_test.go`, `model_navigation_test.go`, plus a
  `test_utils_teaprog.go` harness).
- gh-dash has comparatively thin TUI tests (`ui_test.go` with JSON fixtures).

Analysis: high coverage of a TUI package is achieved by keeping `Update` pure
(no IO — the crush rule) so logic is table-testable, and using teatest goldens
for the rendering integration layer. A 95% statement target is a kb policy
choice, not an ecosystem norm; renderer-heavy files are where it gets expensive.

## 5. SQLite `PRAGMA data_version`

### Documented semantics

<https://www.sqlite.org/pragma.html#pragma_data_version>:

- "The integer values returned by two invocations of `PRAGMA data_version` from
  the same connection will be different if changes were committed to the
  database by any other connection in the interim."
- "The `PRAGMA data_version` value is unchanged for commits made on the same
  database connection."
- Works uniformly for "database connections in separate processes and shared
  cache database connections" — cross-process detection is the designed use.
- "It is only meaningful to compare the `PRAGMA data_version` values returned by
  the same database connection at two different points in time." The number is
  per-connection state, not a persistent database property.
- Intended pattern, verbatim: "Interactive programs that hold database content
  in memory or that display database content on-screen can use the PRAGMA
  data_version command to determine if they need to flush and reload their
  memory or update the screen display."

If you also need to see your *own* connection's commits, that is what the
`SQLITE_FCNTL_DATA_VERSION` file control is for — "the only mechanism to detect
changes that happen either internally or externally"
(<https://www.sqlite.org/c3ref/c_fcntl_begin_atomic_write.html>).

### How it works (SQLite source, github.com/sqlite/sqlite master)

- `Pager.iDataVersion` — "Changes whenever database content changes" — is
  incremented in two places in `src/pager.c`: commit phase two (own commits)
  and `pager_reset()`, the path that discards the page cache when the pager
  detects the file changed underneath it (external commits).
- `PRAGMA data_version` reads it through `sqlite3BtreeGetMeta` in
  `src/btree.c`: `*pMeta = sqlite3PagerDataVersion(pBt->pPager) +
  p->iBDataVersion`, and on the connection's own commit the btree does
  `p->iBDataVersion--;  /* Compensate for pPager->iDataVersion++; */` — which is
  exactly why same-connection commits don't move the pragma value.
- `sqlite3BtreeGetMeta` "may only be called if the b-tree connection already has
  a read or write transaction open", so each `PRAGMA data_version` opens (and
  closes) a read transaction.

### WAL behavior (analysis grounded in the above)

The pragma page says nothing WAL-specific; the mechanism implies:

- Under WAL, an external commit is noticed when the polling connection next
  begins a read transaction and the pager sees a newer WAL snapshot →
  `pager_reset` → counter bump. The `PRAGMA` itself opens that read
  transaction, so polling *is* the detection step.
- Corollary: a connection holding a long-lived open read transaction sits on a
  frozen snapshot and its `data_version` will not advance until that
  transaction ends. Poll on a connection that is otherwise idle.
- Cost per poll: begin read txn + wal-index header check (shared memory) + end.
  No full file scan, no writes. Cheap enough for ~1 Hz UI polling, which is the
  use case the SQLite docs themselves name (quote above). No official
  benchmark exists; treat "cheap" as architecture-level, not measured here.

### modernc.org/sqlite specifics

- The driver is a mechanical CGo-free port of the C amalgamation (v1.56.0 embeds
  SQLite 3.53.3 — `doc.go`), so pragma semantics are the C engine's; nothing in
  cznic/sqlite overrides pragma handling.
- Registers driver name `sqlite`; pragmas configurable per-DSN via repeated
  `_pragma=` query parameters, executed verbatim at connection open
  (`sqlite.go` in v1.56.0, e.g. `?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)`).
- `database/sql` pooling caveat (analysis, forced by the "same connection" rule
  above): `db.QueryRow("PRAGMA data_version")` may hit a different pooled
  connection each call, making comparisons meaningless. Pin one dedicated
  `*sql.Conn` (`db.Conn(ctx)`) for the watcher and run every poll on it.
- Repo warning worth honoring: keep `modernc.org/libc` at exactly the version in
  cznic/sqlite's own `go.mod` (<https://gitlab.com/cznic/sqlite/-/issues/177>,
  restated in `doc.go`).

---

## Recommendations for kb

**Pin (v2 line, canonical charm.land paths):**

```
charm.land/bubbletea/v2  v2.0.8
charm.land/bubbles/v2    v2.1.1
charm.land/lipgloss/v2   v2.0.6
charm.land/glamour/v2    v2.0.1   // raises go directive to 1.25.8
github.com/charmbracelet/x/exp/teatest/v2 v2.0.0-20260816001655-68d539dca504  // test-only
modernc.org/sqlite       v1.56.0  // upgrade from v1.54.0; keep libc in lockstep
```

All MIT except modernc.org/sqlite (BSD-3-Clause); all OSI, no lock-in. teatest is
experimental and untagged — acceptable as a test-only dependency, pinned by
pseudo-version.

**Architecture:** single root model that owns focus (enum), layout, and message
routing, with one package per pane exposing either small `tea.Model`-ish
sub-models (gh-dash style) or imperative components with `Render/SetSize`
methods (crush style). For a kb-sized TUI: root model + sub-model per pane,
shared read-only context struct for theme/dimensions, global keys handled in
root before forwarding to the focused pane, all IO behind `tea.Cmd`. Adopt
crush's two rules verbatim: no IO in `Update`, no state mutation in commands.

**Mouse:** set `View.MouseMode = tea.MouseModeCellMotion` (clicks, wheel, drags;
skip AllMotion unless hover effects are worth the message flood); handle
`tea.MouseClickMsg`/`MouseWheelMsg`/`MouseMotionMsg`/`MouseReleaseMsg`; use
`View.OnMouse` or bubblezone/v2 for hit-testing instead of hand-rolled
coordinate math.

**Testing:** teatest/v2 goldens for whole-screen rendering with
`tea.WithColorProfile` fixed for CI and `*.golden -text` in `.gitattributes`;
keep `Update` pure so pane logic is table-testable without a terminal. Expect
the last few percent toward a 95% bar to live in renderer code — budget for it
or scope the bar to non-render packages.

**data_version polling:** correct primitive for detecting cross-process writes
(other kb processes, MCP server vs CLI). Pattern: open a dedicated watcher
connection (pinned `*sql.Conn`), read `PRAGMA data_version` on a ticker
(`tea.Tick`, ~1s), compare with the previous value from the same connection,
and on change emit a reload message. Do not hold transactions open on the
watcher connection; own-connection writes won't trigger it (by design), so the
writing side should emit its own refresh message after commit.
