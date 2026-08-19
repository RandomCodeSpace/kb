# Research: lipgloss v2 design-system patterns

Ticket: #138 · Map: #136 (TUI visual redesign - web-like design language)
Date: 2026-08-19

Sources: `charm.land/lipgloss/v2@v2.0.6` and `charm.land/bubbletea/v2@v2.0.8` module
cache source (the exact versions vendored in go.mod), `github.com/charmbracelet/ultraviolet`
(bubbletea v2's renderer), and the `main` branches of charmbracelet/crush,
dlvhdr/gh-dash, and charmbracelet/glow on GitHub. All code references verified
against those sources this session.

---

## 1. Style composition and border APIs (v2.0.6)

### Border geometry: exact costs

Every stock border style costs exactly **1 row per enabled horizontal edge and
1 column per enabled vertical edge**. `borders.go` defines each edge as a
single-cell rune; `Border.Get{Top,Right,Bottom,Left}Size()` returns the max
display width of the edge runes, which is 1 for all built-ins:

| Border | Edges | Cost (full box) | Notes |
|---|---|---|---|
| `NormalBorder()` | `─ │ ┌ ┐ └ ┘` | 2 rows + 2 cols | 90-degree corners |
| `RoundedBorder()` | `─ │ ╭ ╮ ╰ ╯` | 2 rows + 2 cols | web-card look |
| `ThickBorder()` | `━ ┃ ┏ ┓ ┗ ┛` | 2 rows + 2 cols | heavier stroke, same cost |
| `DoubleBorder()` | `═ ║ ╔ ╗ ╚ ╝` | 2 rows + 2 cols | |
| `BlockBorder()` | `█` everywhere | 2 rows + 2 cols | solid frame |
| `OuterHalfBlockBorder()` / `InnerHalfBlockBorder()` | `▀ ▄ ▌ ▐` + quadrant corners | 2 rows + 2 cols | visually half-cell, still occupies full cells |
| `HiddenBorder()` | spaces | 2 rows + 2 cols | keeps layout, takes background color |
| `ASCIIBorder()` / `MarkdownBorder()` | `- \| +` | 2 rows + 2 cols | degradation-safe |
| `Border{}` (custom) | any runes | max rune width per edge | corners clamped to first rune |

Per-side toggles (`BorderTop(false)` etc.) drop that side's row/column
entirely; corners are auto-dropped when an adjacent side is off. So a
"web card" = rounded border + `Padding(0, 1)` costs:

```
width  overhead = 2 (border) + 2 (padding)   = 4 columns
height overhead = 2 (border) + 0             = 2 rows
```

With `Padding(1, 2)` (web-like breathing room): 6 columns + 4 rows. On an
80x24 terminal a 3-column miller board with padded cards burns ~12 of 80
columns on chrome per row of cards - fine at normal sizes, which is why the
map's adaptive-compaction decision matters below a height threshold.

Budgeting helpers (in `get.go`): `GetHorizontalFrameSize()` /
`GetVerticalFrameSize()` / `GetFrameSize()` = margins + padding + border, and
`GetHorizontalBorderSize()` etc. Use these in layout math instead of
hardcoding 2s; they respect per-side toggles.

New in v2: `BorderForegroundBlend(...color.Color)` +
`BorderForegroundBlendOffset(int)` paint a gradient around the border
perimeter (per-cell SGR on every border cell - pretty, but the most expensive
border option; see section 4). Per-side border fore/background colors carry
over from v1.

### Composition and inheritance

- `Style` is a **plain value struct** (bitfield `props` + typed fields - no
  maps, no renderer pointer). Copy = assignment. `Foo.Bold(true)` returns a
  modified copy; sharing a base style is safe.
- `s.Inherit(other)`: copies only props set on `other` and **not** set on `s`.
  **Margins and padding are never inherited** (explicit cases in
  `style.go:238`). Background color additionally flows into margin background
  if unset. Practical consequence for a token package: spacing must live in
  component style factories, not in a base style you `Inherit` from.
- v1's `Renderer` is gone entirely. `Style` carries no output/profile state;
  `Render()` always emits full-fidelity ANSI. Downsampling happens at the
  output layer (bubbletea does it; standalone code uses `lipgloss.Println` /
  `colorprofile.NewWriter`).

### v2 API differences worth flagging (vs v1)

- `lipgloss.Color(string)` is now a **function returning `image/color.Color`**,
  not a string type. Everything takes stdlib `color.Color`.
- 16 named ANSI constants: `lipgloss.Red`, `lipgloss.BrightBlack`, ...
  (`ansi.BasicColor`). `ANSIColor` = alias for `ansi.IndexedColor` (256-color).
- Color manipulation now built in: `Alpha`, `Darken`, `Lighten`,
  `Complementary`, `Blend1D`/`Blend2D` (gradients). Useful for deriving hover/
  dim token variants from a small base palette instead of hand-picking 40 hexes.
- New style methods: `UnderlineStyle` (curly/double/dotted), `UnderlineColor`,
  `PaddingChar`, `MarginChar`, `Hyperlink`.
- `lipgloss.Layer` / `lipgloss.Canvas` / `Compositor` (v2-only): real
  z-ordered compositing with per-layer x/y/z and hit-testing (`LayerHit`) -
  directly relevant to the map's "overlays visually separate from the board"
  goal and mouse support. bubbletea v2's `tea.View` accepts layers.
- The repo ships `UPGRADE_GUIDE_V2.md` inside the module
  (`$GOMODCACHE/charm.land/lipgloss/v2@v2.0.6/UPGRADE_GUIDE_V2.md`) - the
  authoritative migration table.

---

## 2. Adaptive color in v2

### What replaced AdaptiveColor / CompleteColor

v1's `AdaptiveColor{Light, Dark}`, `CompleteColor`, `CompleteAdaptiveColor`
are **removed from the root package**. Two replacements:

1. **Explicit helpers (recommended by the upgrade guide):**
   - `lipgloss.LightDark(isDark bool) LightDarkFunc` - returns
     `func(light, dark color.Color) color.Color`. You resolve colors at
     style-construction time, with `isDark` coming from the terminal query.
   - `lipgloss.Complete(p colorprofile.Profile) CompleteFunc` - returns
     `func(ansi, ansi256, truecolor color.Color) color.Color` for explicit
     per-profile picks.

2. **`charm.land/lipgloss/v2/compat` package** - drop-in
   `compat.AdaptiveColor{Light, Dark color.Color}` etc. These resolve **lazily
   in `RGBA()`** against package-level globals:

   ```go
   var (
       HasDarkBackground = lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
       Profile           = colorprofile.Detect(os.Stdout, os.Environ())
   )
   ```

   ⚠️ That initializer runs a **blocking OSC 11 terminal query with a 2-second
   timeout at package import time** (`terminal.go`: `queryBackgroundColor`,
   `defaultQueryTimeout = 2s`). Inside a bubbletea app this is redundant
   (bubbletea does its own query) and can misbehave when stdin isn't the tty.
   ⚠️ `compat` colors also collapse to raw RGBA values, so an indexed ANSI
   color loses its index identity and gets re-quantized by the downsampler.
   **Verdict: don't use `compat` in kb.** It exists for migrating standalone
   CLIs. gh-dash uses it (they support light/dark config themes); crush and
   glow don't.

### Background detection in bubbletea v2

Exact protocol (verified in `bubbletea/v2@v2.0.8/color.go`):

```go
func (m Model) Init() tea.Cmd { return tea.RequestBackgroundColor }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.BackgroundColorMsg:      // struct{ color.Color }
        m.styles = newStyles(msg.IsDark())
    }
}
```

- `tea.BackgroundColorMsg.IsDark()` uses HSL luminance < 0.5 (via
  ultraviolet). Also available: `RequestForegroundColor`, `RequestCursorColor`.
- The message is delivered again if the terminal background changes at
  runtime (terminal theme switch), so the styles rebuild path doubles as live
  theme adaptation.
- `tea.View.BackgroundColor` (field on the returned View) sets the terminal's
  background for the whole program - relevant if the design paints its own
  canvas color rather than inheriting the terminal's.
- Map decision already fogs light-background adaptation; the mechanism above
  is what un-fogs it later with zero architectural change: the theme package
  just needs a `New(isDark bool) Styles` constructor from day one, even if
  only the dark palette is real initially. Glow does exactly this (defaults
  dark until the msg arrives).

### Color-profile degradation (truecolor -> 256 -> 16)

- `github.com/charmbracelet/colorprofile` (already a direct dep of kb) defines
  `TrueColor > ANSI256 > ANSI > Ascii > NoTTY`, detected from env
  (`COLORTERM`, `TERM`, `NO_COLOR`, `CLICOLOR_FORCE`).
- bubbletea v2 detects the profile at startup, sends **`tea.ColorProfileMsg`**
  to Update, and passes the profile into the renderer
  (`cursedRenderer.setColorProfile` -> ultraviolet
  `TerminalScreen.SetColorProfile`). **Downsampling is automatic and
  per-cell at render time** - styles always carry truecolor; the renderer
  quantizes to 256/16 as needed. `tea.WithColorProfile(p)` forces one (tests:
  kb's goldens should pin this).
- Consequence for tokens: "honest 256-color degradation" (map decision) comes
  free if token colors are chosen so their nearest 256-palette neighbors stay
  distinct. Verify by rendering with `tea.WithColorProfile(colorprofile.ANSI256)`.
  Explicit per-profile overrides via `lipgloss.Complete(p)` (driven by
  `ColorProfileMsg`) are only needed where auto-quantization picks a bad
  neighbor - subtle grays on dark backgrounds are the classic casualty.

---

## 3. Theme/token structure in production apps

### crush (charmbracelet/crush - the flagship v2 app)

`internal/ui/styles/` package, four files:

- `styles.go` (~25 KB): one big `Styles` struct - the entire design system as
  data. Component-grouped nested structs of `lipgloss.Style` fields
  (`s.Header.*`, `s.Editor.*`, `s.Button.{Focused,Blurred,Hovered,Negative}`,
  `s.Messages.*`), plus bubbles sub-styles embedded wholesale
  (`textinput.Styles`, `help.Styles`, `filepicker.Styles`), glamour
  `ansi.StyleConfig` for markdown, an `ANSI [16]color.Color` remap palette,
  and **icon/glyph constants** (`CheckIcon`, `BorderThin "│"`,
  `BorderThick "▌"`, scrollbar runes) in the same package - glyphs are treated
  as design tokens too.
- `quickstyle.go`: **the token layer.** `quickStyleOpts` is a struct of
  ~50 semantic `color.Color` slots: brand (`primary`, `secondary`, `accent`,
  `keyword`), fg tiers (`fgBase`, `fgSubtle`, `fgMoreSubtle`, `fgMostSubtle`),
  bg tiers (`bgBase`, `bgLeastVisible`, `bgLessVisible`, `bgMostVisible`),
  contrast pairings (`onPrimary`), `separator`, statuses (`error`, `warning`,
  `warningSubtle`, `success`, `info`, `busy`, `attention`, `destructive` with
  MoreSubtle/MostSubtle variants), and the 16 ANSI remaps.
  `quickStyle(opts) Styles` is the **style factory**: builds every component
  style from the semantic palette; themes then apply targeted overrides on
  the returned struct.
- `themes.go`: named theme constructors (`CharmtonePantera()` - the default -
  feeding `charmtone` named colors into `quickStyle`), plus
  `ThemeKeyForProvider`/`ThemeForProvider` switching. The comment there is
  explicit production guidance: callers use the key to *"cheaply detect when
  switching providers would not actually change the active theme and skip the
  **expensive style rebuild**"* - i.e. build `Styles` once, cache by theme
  key, never rebuild per frame.
- `grad.go`: gradient helpers.

No adaptive color at all - crush is dark-only by design (kb's
"truecolor-dark reference" decision mirrors this).

### gh-dash (dlvhdr/gh-dash)

- `internal/tui/theme/theme.go`: `Theme` struct = flat semantic color map of
  `compat.AdaptiveColor` fields (`PrimaryBorder`, `FaintBorder`,
  `SecondaryBorder`, `PrimaryText`, `SecondaryText`, `FaintText`,
  `InvertedText`, `SuccessText`, `WarningText`, `ErrorText`,
  `SelectedBackground`, icon colors) + icon glyphs. `DefaultTheme` uses
  **ANSI indexed colors** (`lipgloss.ANSIColor(8)` etc.) so it inherits the
  user's terminal palette; `ParseTheme(cfg)` overlays user-config hex values.
- `internal/tui/common/styles.go`: `CommonStyles` struct + `BuildStyles(theme)
  Theme -> Styles` factory; also **layout metric constants as package vars**
  (`HeaderHeight = 2`, `SearchHeight = 3`, `FooterHeight = 1`, ...) - spacing
  tokens live beside style tokens.
- Per-view style structs (`internal/tui/context/styles.go`) built from the
  same Theme. Styles built once at startup/config-reload, threaded through a
  shared `ProgramContext`.

### glow (charmbracelet/glow v3 - on the identical stack: bubbletea v2.0.8, lipgloss v2.0.6)

- Single `ui/styles.go`: unexported `Styles` struct holding both
  `lipgloss.Style` fields and pre-rendered string funcs; **`newStyles(isDark
  bool) Styles`** rebuilds everything from `lipgloss.LightDark(isDark)` with a
  private `s.adaptive(light, dark)` helper. Defaults to dark until
  `tea.BackgroundColorMsg` arrives, then rebuilds once. The struct is stored
  on the model - no globals.

### Synthesis for kb's token decision (#139)

Common shape across all three, in increasing sophistication
(glow -> gh-dash -> crush):

1. **Semantic color map first** (roles, not hues): fg tiers + bg tiers +
   statuses + brand + separator. Crush's tiered naming
   (`fgSubtle/MoreSubtle/MostSubtle`, `bgLeast/Less/MostVisible`) is the most
   battle-tested vocabulary.
2. **One style factory** turning the palette into a component-grouped
   `Styles` struct (every rendered element gets a named style; glyphs and
   layout metrics are tokens too).
3. **Build once, store on the model / context, rebuild only on theme or
   background change.** Never construct styles in View paths.
4. Theme switching = swapping palette structs through the same factory,
   keyed for cheap change detection.

kb's current code constructs `lipgloss.NewStyle()...Render(...)` inline in
view code (e.g. `internal/tui/board_view.go:1006-1040`) with hardcoded hexes -
exactly the pattern all three apps avoid. The token work is a migration from
inline styles to pattern 1-3.

---

## 4. Render cost: heavy styling vs the cell-diff renderer

Verified pipeline in bubbletea v2.0.8 (`cursed_renderer.go` `flush()`) +
ultraviolet `terminal_renderer.go`:

1. `View()` returns a `tea.View` whose `Content` is one ANSI string.
2. **Short-circuit:** if `Content` (and other View fields) is `==` to the
   previous frame's and bounds are unchanged, flush returns immediately -
   an unchanged frame costs one string comparison.
3. Otherwise the whole string is re-parsed (`uv.NewStyledString`), drawn into
   a cell buffer (`content.Draw(s.cellbuf, ...)`), and diffed line-by-line
   against the last presented frame: per-line dirty flags, first/last changed
   cell per line, SGR style diffing (`newStyle.Diff(&oldStyle)`), cursor
   movement minimization. **Bytes written to the terminal scale with what
   changed, not with how styled the frame is.**
4. Color downsampling happens per-cell at this stage against the detected
   profile.

Implications for a heavily styled board:

- Per-cell styling (backgrounds on every card cell, border colors, gradients)
  does **not** stress terminal output on mostly-static frames - the diff
  eats it. What it does cost, on every changed frame: lipgloss `Render()`
  string assembly + full ANSI re-parse + cell-buffer draw, all O(frame area).
  At board scale (10^3-10^4 cells) this is microseconds-to-low-milliseconds
  in Go; not a frame-budget risk at TUI event rates.
- The genuinely expensive habits: rebuilding *style structs* per frame
  (crush's code comments call the style rebuild "expensive" and cache it),
  and animated per-cell effects (gradients that shift every frame defeat both
  the string short-circuit and the diff). Static `BorderForegroundBlend` is
  fine; animating its offset redraws the full border every frame.
- No official "style caching" doc exists beyond the upgrade guides; the
  authoritative guidance is the pattern in Charm's own apps: **styles as
  fields of a long-lived struct built once per theme/background change**
  (crush, glow), never package-level mutable state rebuilt in View, never
  inline `NewStyle()` chains in hot render loops. Inline chains are
  correctness-safe (value semantics) but waste allocations and scatter tokens.
- `tea.WithANSICompressor` from v1 is gone - the renderer optimizes output
  itself (upgrade guide).

---

## 5. Trip hazards for the token decision (#139)

1. **`Inherit` skips padding and margins.** A "base card style" cannot carry
   spacing via inheritance; spacing tokens must be applied by the factory per
   component, or use crush's approach (no Inherit at all - the factory sets
   everything explicitly). Prefer the latter; `Inherit`'s selective semantics
   are a foot-gun in a token system.
2. **Skip `compat`.** Import-time 2s terminal query, global mutable state,
   RGBA-collapse of indexed colors. Use `LightDark` + `BackgroundColorMsg`
   plumbing even while light mode stays fogged: `theme.New(isDark bool)` from
   day one costs nothing.
3. **Goldens vs profile.** kb goldens compare normalized final cell grids
   (per #122). The renderer's per-cell downsampling means golden output
   depends on the color profile - pin `tea.WithColorProfile` in teatest
   setups or grids will differ between CI (likely NoTTY/Ascii) and local
   truecolor terminals. Also decide whether goldens normalize SGR at all -
   restyle tickets regenerate every golden regardless.
4. **256-color honesty is a palette-selection problem, not a code problem.**
   Auto-quantization handles the mechanics; the token palette should be
   audited once under `colorprofile.ANSI256` for collisions (especially
   subtle-gray tiers - crush's 4 fg tiers survive because charmtone was
   picked with quantization in mind). Reserve `Complete()` for proven bad
   cases only.
5. **Half-block borders look web-like but degrade badly** - `▌▐▀▄` render as
   garbage on fonts without block glyphs and have no ASCII analog. Rounded +
   normal borders degrade to ASCII cleanly. If a "shadow" or "elevated card"
   effect is wanted, half-blocks + bg color is the standard trick (crush uses
   `▌` as `BorderThick` accent) - keep it an accent, not structure.
6. **Overlay separation has first-class support now**: `lipgloss.Layer` with
   z-index + `Canvas`/`Compositor`, and layer hit-testing for mouse. Worth a
   deliberate choice in #139: keep the current string-composition overlays or
   move to layers. Layers also interact with the View's `OnMouse` handler in
   bubbletea v2. Don't decide by accident.
7. **Spacing/glyph tokens belong in the theme package** alongside colors
   (gh-dash's `HeaderHeight` vars, crush's icon constants). The compact-density
   threshold decision (fogged in the map) will need exactly these as named
   tokens to flip on height.
8. **Terminal-inherited vs owned palette.** gh-dash defaults to ANSI indexed
   colors (respects user terminal palette, guaranteed-legible); crush/glow own
   their hexes (brand-exact, terminal-independent). kb's "truecolor-dark
   reference" decision implies the crush model; note the trade-off is already
   made and the ANSI-remap trick (crush's `ANSI [16]color.Color`) exists if
   embedded shell/raw output ever needs to look on-brand.
