# kb TUI design-token and widget spec

Binding resolution for ticket [#139](https://github.com/RandomCodeSpace/kb/issues/139),
part of map [#136](https://github.com/RandomCodeSpace/kb/issues/136).

Inputs: visual language chosen in [#137](https://github.com/RandomCodeSpace/kb/issues/137)
(Strata base + web-faithful column header bands), research findings in
[#138](https://github.com/RandomCodeSpace/kb/issues/138)
(`docs/research/lipgloss-design-system.md`), prototype sources on branches
`prototype/wildcard` (base) and `prototype/web-faithful` (header bands).

Reference target: **truecolor, dark background**. 256-color is an honest
degradation, audited below. Light-background adaptation stays fog (map #136) but
the `LightDark` seam exists from day one.

Restyle build slices graduate from this document. Every number in it is
normative; a slice that needs a value not written here has found a spec gap and
must come back, not invent one.

---

## 1. Semantic palette

One palette, ~30 named slots, roles not hues. Nothing outside the theme package
names a hex.

The `x256` column is the xterm-256 index each hex quantizes to under nearest-RGB
matching, computed with the prototype's `-audit256` algorithm
(`prototype/wildcard:prototypes/wildcard/main.go`, `xterm256`). It is recorded so
256-color collisions are a decided fact, not a surprise.

### 1.1 Depth tiers (backgrounds)

Depth is carried entirely by background shade. There are no box-drawing borders
on cards or columns.

| Slot | Hex | x256 | Role |
|---|---|---|---|
| `Shadow` | `#05070a` | 232 | Overlay drop shadow; darkest thing on screen |
| `Canvas` | `#0b0e14` | 233 | Page ground, behind everything |
| `Surface` | `#171d27` | 234 | Column panel body, footer bar, filter field |
| `Zebra` | `#1e2632` | 235 | Alternating card tier, compact density only |
| `Card` | `#252f3d` | 236 | Card surface |
| `Raised` | `#35404f` | 238 | Selected / hovered card surface; **and** the unfocused column header band (`BandRest` names this same tier) |
| `OverlaySurf` | `#3c495c` | 239 | Overlay panel body |
| `OverlayBand` | `#4a5970` | 59 | Overlay header/footer/section bands |

**The band tier, and why it is `Raised`.** The wildcard prototype draws the
unfocused band on `Surface` — the same shade as the panel body under it — so it
does not read as a band at all. Web-faithful's `bgBand` is `#1a1f2b`, and that
hex quantizes onto **235**, colliding with `Zebra`; since the band is present at
every density and `Zebra` appears at compact density, those two genuinely share a
frame. The 256 greyscale ramp has exactly eight usable dark steps (232-239) and
this palette already spends all eight, so there is no ninth grey to allocate.

The band therefore uses the **`Raised`** tier: the unfocused band sits one step
*above* the card surface, which is also how web-faithful reads it — the band is
the most prominent thing in an unfocused column. `BandRest` and `Raised` are the
same hex, so this is a same-hex alias and not a collision. It is safe because
selection exists only in the focused column (`selected = focused && i == cursor`),
and the focused column's band is a solid hue fill — so a `Raised` card and a
`BandRest` band can never appear in the same column. That guarantee is structural,
not incidental.

Resulting tier order, dark to light:
`Shadow < Canvas < Surface < Zebra < Card < Raised/BandRest < OverlaySurf`,
with `OverlayBand` escaping the greyscale ramp into the blue cube at 59.

### 1.2 Foreground scale

| Slot | Hex | x256 | Role |
|---|---|---|---|
| `FgBase` | `#e3e9f2` | 255 | Primary text: card titles, field values, overlay body |
| `FgSubtle` | `#9aa5b6` | 248 | Secondary text: footer, band labels, effort, section labels |
| `FgMuted` | `#6b7686` | 243 | Tertiary text: description snippet, `#seq`, age, counts, hints |
| `FgOnAccent` | `#0b0e14` | 233 | Text drawn on a saturated fill (pill interiors, focused band) |

`FgOnAccent` is a same-hex alias of `Canvas`; that is deliberate and is not a
collision.

### 1.3 Brand and column hues

| Slot | Hex | x256 | Role |
|---|---|---|---|
| `Brand` | `#4f8ef7` | 69 | `kb` wordmark pill, overlay header band, focus accent |
| `HueTodo` | `#7aa2f7` | 111 | TO DO column identity |
| `HueDoing` | `#f2a33c` | 215 | DOING column identity |
| `HueDone` | `#3fbf7f` | 72 | DONE column identity |
| `HueCancelled` | `#7b8494` | 102 | CANCELLED column / hidden-count chip |

### 1.4 Priority scale

| Slot | Hex | x256 | Role |
|---|---|---|---|
| `Prio1` | `#ff5a48` | 203 | P1 |
| `Prio2` | `#ffb020` | 214 | P2 |
| `Prio3` | `#4f8ef7` | 69 | P3, and the fallback for any unknown priority |
| `Prio4` | `#b8bdc7` | 7 | P4 |

Mapping is exact-match on 1/2/4, everything else falls to `Prio3`
(prototype `prioSlot`).

### 1.5 Status colors

| Slot | Hex | x256 | Role |
|---|---|---|---|
| `StatusOK` | `#3fbf7f` | 72 | shipped counter, ready dot, `clear` completion gate |
| `StatusWarn` | `#ffb020` | 214 | blocked chip, warnings |
| `StatusDanger` | `#ff5a48` | 203 | overdue chip, errors, purge arm state |
| `StatusInfo` | `#4f8ef7` | 69 | due chip (not overdue), informational |

### 1.6 Label pill wheel

Five-color wheel, selected by `sum(runes(tag)) % 5` — same hash as today's
`labelColor` in `board_view.go:1021`, so existing label colors stay stable
relative to each other.

| Slot | Hex | x256 |
|---|---|---|
| `Label1` | `#ff7b54` | 209 |
| `Label2` | `#4f8ef7` | 69 |
| `Label3` | `#3f9d58` | 71 |
| `Label4` | `#b98af7` | 141 |
| `Label5` | `#ffb020` | 214 |

The dark half of a scoped `key::value` pill uses **`Surface`** (`#171d27`), not a
dedicated slot. The wildcard prototype had a `labelKey` slot at `#1b2330` whose
only distinction from `Surface` was invisible, and it produced the palette's one
genuine 256 collision (with `Zebra`, both landing on 235). Folding it into
`Surface` removes the collision and one slot. See §8, contestable call 4.

### 1.7 256-color audit

Verified output of the audit over the palette above (same algorithm as
`-audit256`):

```
Shadow          #05070a -> 232     Brand        #4f8ef7 ->  69
Canvas          #0b0e14 -> 233     HueTodo      #7aa2f7 -> 111
Surface         #171d27 -> 234     HueDoing     #f2a33c -> 215
Zebra           #1e2632 -> 235     HueDone      #3fbf7f ->  72
Card            #252f3d -> 236     HueCancelled #7b8494 -> 102
Raised/BandRest #35404f -> 238     Prio1        #ff5a48 -> 203
OverlaySurf     #3c495c -> 239     Prio2        #ffb020 -> 214
OverlayBand     #4a5970 ->  59     Prio3        #4f8ef7 ->  69
FgBase          #e3e9f2 -> 255     Prio4        #b8bdc7 ->   7
FgSubtle        #9aa5b6 -> 248     StatusOK     #3fbf7f ->  72
FgMuted         #6b7686 -> 243     StatusWarn   #ffb020 -> 214
FgOnAccent      #0b0e14 -> 233     StatusDanger #ff5a48 -> 203
                                   StatusInfo   #4f8ef7 ->  69
                                   Label1..5    209 / 69 / 71 / 141 / 214
```

**Same-hex aliases** (intended, not collisions):
`Canvas`/`FgOnAccent` (233); `Raised`/`BandRest` (238);
`Brand`/`Prio3`/`StatusInfo`/`Label2` (69); `Prio2`/`StatusWarn`/`Label5` (214);
`HueDone`/`StatusOK` (72); `Prio1`/`StatusDanger` (203).

**Real collisions** (different hex, same index): **none.**

Two candidate hexes were rejected on this basis and are recorded so they are not
re-proposed:

| Rejected hex | Source | x256 | Collides with |
|---|---|---|---|
| `#1a1f2b` | web-faithful `bgBand` | 235 | `Zebra` — see §1.1 |
| `#1b2330` | wildcard `labelKey` | 235 | `Zebra` — see §1.6 |

Both are eliminated: the band moves to the `Raised` tier (§1.1) and the scoped
pill's dark half moves to `Surface` (§1.6). The prototype's `-audit256` reported
`labelKey`/`Zebra` as the one surviving real collision; this palette has zero.

**Guard.** The theme package ships a test that quantizes every slot and asserts
the real-collision set is **empty**, with the alias set exactly as listed above.
A new slot that lands on an occupied index with a different hex fails the build
until it is re-hued, or until the alias is justified in writing here. This is the
mechanism that keeps 256 honest; the audit is not a one-time exercise. It is
cheap — a pure function over ~29 colors, no terminal involved.

### 1.8 Dimmed variant

The overlay backdrop uses a second, fully-built palette: every slot blended 66%
toward `Canvas`.

```
Dim(c) = c*(1-0.66) + Canvas*0.66   // per channel, 8-bit, round-half-up
```

Built once alongside the base palette. Never computed per frame.

---

## 2. Depth and structure rules

### 2.1 Shade-tier assignment

| Element | Background |
|---|---|
| Page ground, top bar, toolbar row, page padding row | `Canvas` |
| Column panel body, footer bar, filter input field | `Surface` |
| Column header band, unfocused | `BandRest` (= `Raised`) |
| Column header band, focused | the column's hue (`HueTodo`/`HueDoing`/`HueDone`/`HueCancelled`), solid |
| Card, resting | `Card` |
| Card, alternate row (compact density only) | `Zebra` |
| Card, selected | `Raised` |
| Overlay panel body | `OverlaySurf` |
| Overlay header band | `Brand` |
| Overlay footer band, overlay section break | `OverlayBand` |
| Overlay drop shadow | `Shadow` |

Adjacent tiers are spaced so their 256 neighbours stay distinct
(233/234/235/236/238/239). That spacing is the load-bearing decision of the
language; do not insert a tier between two of these without re-running the audit.

### 2.2 Column header band

One row, full column width. Merged from both prototypes: web-faithful's band
content (status dot, name, count) on Strata's borderless shade-tier depth.

**Unfocused** — background `BandRest` (the `Raised` tier, §1.1), foreground the
column hue, bold:

```
▌● 1 TO DO                                4
^ ^ ^ ^                                   ^
| | | column label (bold, column hue, ellipsized to width-5)
| | index digit + space
| status dot U+25CF, column hue
half-block rail U+258C, column hue
```

**Focused** — background the column hue, solid; foreground `FgOnAccent`, bold.
The rail is replaced by a focus caret so the band reads as filled edge to edge:

```
▸ 2 DOING                                 3
```

No rule, no border, no separator line under the band. The tier step from the band
to the panel body is the separation. (This is the point of the merge: web-faithful
paid 4 rows per column for top border, band, band rule, bottom border; here the
band costs 1 row and nothing else.)

Count is right-aligned with one trailing space.

### 2.3 Column meta line

One row under the band, background `Surface`, foreground `FgMuted`, inset 2
columns:

```
  4 cards · 1 blocked
```

Blocked segment appears only when the count is non-zero. Dropped at compact
density (§2.6).

### 2.4 Focus and selection

| Signal | Treatment |
|---|---|
| Focused column | Header band fills solid with the column hue |
| Selected card | Background steps `Card` → `Raised`; left rail glyph thickens `▌` (U+258C) → `█` (U+2588); title renders bold |
| Rail hue | The card's priority hue, **always** — including when selected |
| Pressed / active pointer target | Reverse video (SGR 7), unchanged from today's `pointer.State.Render`, but promoted into the theme as `Styles.Pressed` |

The rail keeps the priority hue on selection. The wildcard prototype re-hued it
to `Brand`, which erases the P1 signal on the exact card the user is looking at.
`Raised` + full block + bold title is already three independent selection cues.
See §8, contestable call 3.

### 2.5 Gutters and padding scale

| Token | Value | Applies to |
|---|---|---|
| `PageMarginX` | `1` when frame width ≥ 100, else `0` | Left/right page margin |
| `PagePadTop` | `1` row at normal density, `0` at compact | Canvas row between the toolbar and the columns |
| `ColumnGutter` | `1` | Columns between panels |
| `ColumnPadX` | `1` at normal, `0` at compact | Inset of the card stack inside its panel |
| `ColumnMetaInset` | `2` | Meta line and `+N more` inset from the panel edge |
| `CardGap` | `1` row at normal, `0` at compact | Between stacked cards |
| `CardRail` | `1` column | Reserved on the card's left edge, always |
| `CardPadLeft` | `1` at normal, `0` at compact | Between rail and content |
| `CardPadRight` | `1` | Always |
| `MaxColumnWidth` | `52` | Terminal analogue of a web max-width container |
| `OverlayInsetX` | `2` | Overlay content inset from the panel edge |

**Chrome row budget.** Normal density spends 4 rows: top bar, toolbar, page
padding, footer. Compact spends 3 (no page padding). Everything else is board.

**Column widths.** Available width = `frameW - 2*PageMarginX - (n-1)*ColumnGutter`,
split evenly with the remainder distributed to the leftmost columns. If the
resulting width exceeds `MaxColumnWidth`, every column is clamped to 52 and the
whole group is centered; the leftover becomes page margin. Extra terminal width
must never become stretched cards.

**Card geometry.** For a card of width `W`:

- normal: `rail(1) + padLeft(1) + inner + padRight(1) = W`, so `inner = W - 3`
- compact: `rail(1) + inner + padRight(1) = W`, so `inner = W - 2`

A card whose `inner < 6` renders the surface and rail only, no content.

### 2.6 Compact density

**Threshold.** Compaction fires when

```
frameHeight < 30   OR   columnInnerWidth < 22
```

The height axis is the prototype's, unchanged. The width axis is added: at 3
columns on an 80-cell frame the panels are ~26 wide and cards are ~23 inner,
which just fits; below that, pill end caps and the chip row stop fitting and
normal density produces a wall of dropped chips instead of a denser board. See
§8, contestable call 5.

**Second description line.** At `frameHeight >= 45` the description snippet gets
a second line (card grows from 4 to 5 content rows). Below that it is one
ellipsized line.

**Drop order.** Compaction is not gradual — crossing the threshold applies all of
it at once. The order below is normative for any future partial-compaction work
and matches the map's decision that the description goes first.

1. **Description snippet** — dropped entirely.
2. **Page padding row** — `PagePadTop` 1 → 0.
3. **Column meta line** — the `N cards · N blocked` row.
4. **Inter-card gutter** — `CardGap` 1 → 0, replaced by `Zebra` striping on
   alternate cards so the stack still separates.
5. **Card label row** — labels merge onto the meta chip row instead of owning
   their own line.
6. **Inner paddings** — `CardPadLeft` 1 → 0 and `ColumnPadX` 1 → 0.
7. **Pill end caps** — chips degrade to flat colored bold text (`flatChip`),
   scoped labels keep only their value half. This is the last thing to go
   because it is the only step that changes the vocabulary rather than the
   spacing.

---

## 3. Card anatomy

### 3.1 Rows by density

**Normal** (`30 <= h < 45`) — 4 content rows + 1 gutter = 5 rows per card:

```
▌🐛 Drag ghost sticks on resize      #142     row 0  title
▌Pointer capture leaks when the col…          row 1  description (FgMuted)
▌P1 3d old ▐blocked▌ ▐in 2d▌ ◇M               row 2  meta chips
▌▐type:▌feature▌ ▐#area:tui▌                  row 3  label pills
                                              gutter (Canvas-free: Surface)
```

**Tall** (`h >= 45`) — 5 content rows + 1 gutter = 6 rows per card. Row 1 becomes
rows 1-2, description wrapped to two lines. Chip rows stay pinned at
`1 + descLines` and `2 + descLines`, so a one-line description does not pull the
chips up; the card's row grid is fixed by density, not by content.

**Compact** (`h < 30` or `innerW < 22`) — 2 content rows, no gutter:

```
▌🐛 Drag ghost stic…          #142     row 0  title
▌P1 3d old ⛔ !2d ◇M bug               row 1  meta + labels, flat chips
```

### 3.2 Row 0 — title

`emoji + " " + title`, foreground `FgBase`, bold only when selected. Right-aligned
`#seq` in `FgMuted`. The title field is `inner - width(seq) - 1` wide and is
ellipsized to fit; `#seq` is never truncated.

### 3.3 Row 1 — description snippet

Foreground `FgMuted`. Greedy word wrap to `inner` columns across `descLines`
lines. Rules:

- A word longer than `inner` is hard-truncated with an ellipsis rather than
  overflowing.
- The **last** allotted line carries the ellipsis when text remains, so the
  snippet can never wrap past the card.
- A description shorter than the allotment leaves its remaining rows blank; the
  chip rows do not move up.
- Line count: `descLines = 0` compact, `1` normal, `2` when `frameHeight >= 45`.

Truncation primitive: `ansi.Truncate(s, w-1, "") + "…"` — width-aware, not
byte- or rune-aware.

### 3.4 Meta chip row

Ordered left to right. This order is also the survival order: chips that do not
fit are skipped individually and shorter chips behind them are still attempted
(never a blanket right-trim). Separator is one space.

| Position | Content | Normal | Compact |
|---|---|---|---|
| 1 | Priority | `P1` bold, priority hue, no pill | same |
| 2 | Age | `3d old` / `6h here` / `new` / `shipped`, `FgMuted` | same |
| 3 | Blocked | pill `blocked` on `StatusWarn` | `⛔` in `StatusWarn` |
| 4 | Due | pill `today` / `in 2d` / `overdue · 2d` on `StatusInfo`, or `StatusDanger` when overdue | `!today` / `!in 2d` / `!2d`, same hue |
| 5 | Effort | `◇M`, `FgSubtle` | same |

Priority survives longest because it is never a pill and is only two cells.

### 3.5 Label pill row

One pill per label, wheel-hued by `sum(runes) % 5`.

- **Scoped** `key::value` → two-tone pill: `key:` on `Surface` in `FgSubtle`,
  `value` on the wheel hue in `FgOnAccent`.
- **Plain** `tag` → single-tone pill `#tag` on the wheel hue in `FgOnAccent`.
- **Compact** → flat bold text: scoped keeps only `value`, plain becomes `#tag`,
  both in the wheel hue on the card background.

### 3.6 Pill rendering

The pill is the language's chip primitive: half-block end caps carrying the fill
color as *foreground* over the surface behind, so the chip reads as rounded at
half-cell resolution.

```
runs = [ "▐" fg=Fill  bg=On ]   U+2590 right half block
       [ text fg=FgOnAccent bg=Fill ]
       [ "▌" fg=Fill  bg=On ]   U+258C left half block
```

Cost: `width(text) + 2` columns. The scoped variant substitutes `Surface`/
`FgSubtle` for the first cap and the key run.

**Known risk, carried consciously (map #136):** the entire accent vocabulary is
U+2588 / U+258C / U+2590. On fonts without block glyphs this degrades worse than
a border would and has no ASCII analogue. Shade tiers survive that failure; pills
and rails do not. Accepted at the #137 resolution.

### 3.7 Overflow cue

When the stack cannot fit every card, the panel's last row carries
`+N more` in `FgMuted` on `Surface`, inset `ColumnMetaInset`.

---

## 4. Overlay elevation

Elevation is a shade step plus a shadow, never a frame.

**Recipe, in order:**

1. **Dim the board.** Re-render the entire board through the dimmed `*Styles`
   (§1.8). Not a post-pass on a rendered string — the board render is called with
   `styles.Dimmed`. This is the structural consequence of kb composing strings
   rather than owning a cell grid the way the prototype did: the dim variant must
   exist as a second built `*Styles`, resolved once per theme.
2. **Cast the shadow.** Two `Shadow`-filled bands offset one cell down and right
   of the panel: a `pw × 1` band along the bottom starting at `(px+1, py+ph)`,
   and a `1 × ph` band along the right starting at `(px+pw, py+1)`.
3. **Fill the panel** at `OverlaySurf`.
4. **Header band**, row 0 of the panel: solid `Brand`, `FgOnAccent`, bold title
   inset `OverlayInsetX`, right-aligned `#seq`.
5. **Section breaks** are `OverlayBand` rows carrying a bold `FgSubtle` label
   (`DETAIL`, `COMMENTS`, `CHECKLIST`), not rules.
6. **Footer band**, last row of the panel: `OverlayBand`, `FgSubtle`, action
   hints inset `OverlayInsetX`.

**Geometry.** `pw = min(72, frameW - 8)`, `ph = min(13, frameH - 6)` for the card
detail pane; centered. Below `pw < 24` or `ph < 8` the overlay does not render as
a panel and the view falls back to full-frame. Per-overlay width caps that
already exist stay as tokens: card detail 92, editor 96, ADR split 100, issue
import 88, task action 72, help 56.

**Field rows** inside an overlay: label at inset `OverlayInsetX` in `FgMuted`,
fixed 12-column label gutter, value at `OverlayInsetX + 12` in `FgBase`.

**Z-order** is unchanged from `model.go`: board → help → card detail → settings →
ADR split → card editor → task action → issue import.

---

## 5. Widget inventory

Sourcing rule, from map #136: charm first, kb widgets last resort.

**Verified constraint** (§7 of this doc): `charm.land/huh/v2` v2.0.3 compiles and
runs against kb's exact stack. It is therefore *available*, and it is assigned
the roles below. It is **not** assigned any role that requires pointer hit
regions, because huh exposes no hit-testing surface and kb's mouse-first-class
interaction model is locked by v1.0.1 (map #136, out of scope).

kb widgets live in `internal/tui/widget`. Their reference shape is crush's
`internal/ui/common` (issue #136: `ButtonOpts{Text, Selected, Hovered,
UnderlineIndex, Padding}` + cached `Styles` + `StyleRanges` for the hotkey
underline + `ButtonGroup`).

### 5.1 Board and structural elements

| Element | Source | API sketch |
|---|---|---|
| Card surface | kb `widget` | `Card(o CardOpts) []string` — `CardOpts{Title, Emoji, Seq, Desc, Meta []Chip, Labels []Chip, Priority int, Selected, Alt bool, Width int, Density Density}` |
| Column panel | kb `widget` | `Panel(o PanelOpts) []string` — `PanelOpts{Header BandOpts, Meta string, Body []string, Width, Height int, Density Density}` |
| Column header band | kb `widget` | `Band(o BandOpts) string` — `BandOpts{Index int, Label string, Count int, Hue theme.Slot, Focused bool, Width int}` |
| Card rail | kb `widget` | `Rail(hue theme.Slot, selected bool) string` — one cell, `▌`/`█` |
| Chip / pill | kb `widget` | `Chip(o ChipOpts) string` — `ChipOpts{Text, Key string, Fill, On theme.Slot, Flat bool}`; `Key` non-empty selects the scoped two-tone form, `Flat` the compact degradation |
| Label pill | kb `widget` | `Label(tag string, on theme.Slot, flat bool) string` — wraps `Chip`, owns the `%5` wheel |
| Priority marker | kb `widget` | `Priority(p int, on theme.Slot) string` |
| Button / action | kb `widget` | `Button(o ButtonOpts) string` + `ButtonGroup(gap int, bs ...string) string` — `ButtonOpts{Text string, Selected, Hovered, Armed bool, UnderlineIndex int, Padding [2]int}`; `Armed` is kb's addition for the purge/remove two-step |
| Status line / footer | kb `widget` | `StatusBar(o StatusOpts) string` — `StatusOpts{Dot theme.Slot, State string, Hints []string, Width int}`; responsive hint ladder is the caller's |
| Filter bar | kb `widget` | `FilterBar(o FilterOpts) string` — `FilterOpts{Field string, Chips []string, Count string, Width int}` |
| Top bar | kb `widget` | `TopBar(o TopBarOpts) string` — brand pill, title, user, right-aligned counters |
| Overlay panel | kb `widget` | `Overlay(o OverlayOpts) string` — `OverlayOpts{Title, Seq string, Sections []Section, Footer string, W, H int}`; owns the shadow, bands and section breaks of §4 |
| Overlay shadow | kb `widget` | folded into `Overlay`; not separately callable |
| Checkbox row | kb `widget` | `Check(label string, state CheckState, focused bool) string` — `☐ ☑ ☒` |
| Key/value field row | kb `widget` | `Field(label, value string, w int) string` — the 12-column label gutter of §4 |
| Scroll indicator | kb `widget` | `ScrollHint(cur, total int) string` — `12/40` in `FgMuted`; kb's overlays scroll by hand-managed offset and `bubbles/viewport` does not expose that offset in a form the pointer regions can consume |

### 5.2 Charm-sourced components

| Element | Source | Notes |
|---|---|---|
| Single-line text input | `bubbles/v2 textinput` | Already in use in 12 files. Feed it `theme.Styles.Input` (a `textinput.Styles` embedded in kb's Styles, crush's pattern). |
| Multi-line text input | `bubbles/v2 textarea` | Already in use in 7 files. Same: embed `textarea.Styles`. |
| Cursor | `bubbles/v2 cursor` | Adopt to replace the three hand-rolled `cursorViewport` helpers. |
| Markdown | `glamour/v2` | Already in use. The `styles.DarkStyleConfig` clone at `carddetail/model.go:743` becomes `theme.Styles.Markdown ansi.StyleConfig`, derived from the palette, injected through the existing `markdownRenderer` func field. |
| Keybinding registry | `bubbles/v2 key` | Adopt. Replaces ad-hoc hint strings; feeds both the footer ladder and the help pane. |
| Help pane | `bubbles/v2 help` | Adopt for the help overlay body. Embed `help.Styles` in kb's Styles. The overlay chrome around it stays kb's `Overlay` widget. |
| Spinner | `bubbles/v2 spinner` | Adopt for every `…ing…` busy state (drafting, saving, fetching, importing) that is currently static text. |
| Progress | `bubbles/v2 progress` | Adopt for `issueimport`'s `writing i/N`. |
| Confirm dialog | `huh/v2 Confirm` | Assigned: the ship / kill confirm prompt's yes-no core. |
| Choice row | `huh/v2 Select` | Assigned: the three-way `Cancel / Tick everything / Ship anyway` and `Priority` / `Effort` / `Source` / `Max stories` choice fields. |
| Note / disclaimer block | `huh/v2 Note` | Assigned: the AI disclaimer blocks in `adrsplit` and `carddetail/drift`. |

### 5.3 Explicitly not sourced from charm, with reasons

| Element | Charm candidate | Why not |
|---|---|---|
| Card list / column stack | `bubbles/v2 list` | Owns its own filtering, pagination, status bar and keymap; kb's columns are a miller-column board with drag-and-drop lift and per-column scroll windows. Adopting `list` means fighting it. |
| Comment / blocker-link pickers | `bubbles/v2 list` | Same, plus they need pointer hit regions per row. |
| Board grid | `bubbles/v2 table` | Cards are multi-row surfaces, not cells. |
| Overlay scrolling | `bubbles/v2 viewport` | kb's overlays scroll with focus-follow and wheel regions keyed to `pointer.Surface`; `viewport` does not surface the offset/region pairing those need. Revisit if a slice proves otherwise. |
| Editor form as a whole | `huh/v2 Form` | The card editor carries a label-suggestion dropdown, a similar-items block with per-row dismiss, a stale-refresh banner and mouse hit regions on every control. huh owns layout and focus and exposes no hit regions. Its *fields* are adopted (§5.2); its `Form` container is not. See §8, contestable call 1. |
| Settings pane | `huh/v2 Form` | Same, plus dynamic forge row groups with locked fields. |
| ADR split review stage | `huh/v2 Form` | Per-story row groups generated at runtime with independent include/priority/effort state. |
| Pressed feedback | — | Stays SGR 7 reverse video, but as `Styles.Pressed`, not a raw escape in `pointer.go:54`. |

---

## 6. Theme package contract

### 6.1 Location and shape

```
internal/tui/theme/          palette.go, styles.go, audit_test.go
internal/tui/widget/         card.go, panel.go, band.go, chip.go, button.go, ...
```

`theme` must not import `widget`; `widget` imports `theme`. Neither imports
`internal/tui`.

```go
package theme

type Slot uint8              // the ~30 names of §1
type Palette [numSlots]color.Color

// New resolves the palette for a terminal background and builds every style
// exactly once. Both the base and the dimmed variant are built here.
func New(isDark bool) *Styles

type Styles struct {
    Pal    Palette
    Dimmed *Styles      // nil on the dimmed instance itself; §4 step 1

    Board   BoardStyles    // Canvas, TopBar, Toolbar, Footer, PagePad
    Column  ColumnStyles   // Panel, BandRest, BandFocus, BandLabel, Meta, More
    Card    CardStyles     // Rest, Zebra, Raised, Title, TitleSel, Desc, Seq
    Rail    [5]lipgloss.Style   // by priority, index 0 unused
    Chip    ChipStyles     // CapLeft, CapRight, Body, ScopedKey, Flat
    Label   [5]ChipStyles  // the wheel
    Status  StatusStyles   // OK, Warn, Danger, Info, Dot
    Overlay OverlayStyles  // Surf, HeaderBand, SectionBand, FooterBand, Shadow, FieldLabel, FieldValue
    Button  ButtonStyles   // Rest, Focused, Hovered, Armed, Pressed
    Pressed lipgloss.Style

    Input    textinput.Styles   // handed to bubbles
    Area     textarea.Styles
    Help     help.Styles
    Spinner  spinner.Spinner
    Markdown ansi.StyleConfig   // handed to glamour
    Huh      *huh.Styles        // handed to huh fields, built by the same factory

    Metrics Metrics   // §2.5, plus CompactBelow=30, CompactInnerW=22, DescTwoLines=45
    Glyph   Glyphs    // Rail, RailFull, CapL, CapR, Dot, Check, Diamond, Focus, More
}
```

Glyphs and layout metrics are tokens and live here, beside the colors
(crush's icon constants, gh-dash's `HeaderHeight` vars — research §3).

### 6.2 Cached style factory

Non-negotiable, from the research (#138) and crush's own comment calling the
style rebuild expensive:

- Every `lipgloss.Style` in `Styles` is constructed inside `New`, once.
- `New` is called on program start and on `tea.BackgroundColorMsg`. Nowhere else.
- The result is stored on the root `Model` and threaded down. No package-level
  mutable style state.
- **No `lipgloss.NewStyle()` in any `View()` / `render*` path.** This is a
  reviewable rule, and the migration slice adds a lint or grep-based test that
  fails on `NewStyle(` under `internal/tui/**` outside `theme/`.
- **No `Style.Inherit`.** The factory sets every property explicitly; `Inherit`
  silently skips padding and margins (research §5, hazard 1) and is a foot-gun in
  a token system.

Sizing helpers (`.Width()`, `.Height()`) applied per frame to an already-built
style are fine — those are layout, not tokens.

### 6.3 LightDark hook

```go
ld := lipgloss.LightDark(isDark)
Canvas: ld(lightHex, darkHex)
```

The seam exists in `New` from the first commit; the light column is populated but
**not designed** — light-background adaptation remains fog per map #136. The dark
column is the reference and the only one that is reviewed, audited, or goldened.

`isDark` arrives from `tea.BackgroundColorMsg.IsDark()`. Default to `true` until
the message lands, then rebuild once. Do **not** import
`lipgloss/v2/compat` — 2s import-time terminal query, global mutable state,
RGBA-collapse of indexed colors (research §5, hazard 2).

`huh` themes take the same seam: its `ThemeFunc func(isDark bool) *huh.Styles`
signature matches `theme.New` exactly, so kb registers
`huh.ThemeFunc(func(d bool) *huh.Styles { return theme.New(d).Huh })`.

### 6.4 Color profile pinning for goldens

The renderer downsamples per cell against the detected profile, so golden cell
grids depend on the profile (research §5, hazard 3).

- Every teatest-driven golden pins `tea.WithColorProfile(...)` explicitly. Three
  tests do this today (`model_test.go:2355`, `:2417`, `move_model_test.go:499`)
  and they pin `colorprofile.ASCII`.
- Restyle slices must pin **`colorprofile.TrueColor`** for board and overlay
  goldens — an ASCII-pinned golden of a design whose entire depth model is
  background color asserts nothing about the design.
- `golden.RequireEqual` tests that strip ANSI keep a **structure** golden
  (layout, truncation, drop order) and stay ASCII-pinned. They are cheap and they
  catch geometry regressions.
- Add a **second** color-pinned golden per view for the palette. New color-pinned
  goldens are needed where none exist today: the board with cards, the help
  overlay, the task-action modal, the issue-import overlay.
- Production pins nothing (`run.go:24`) — unchanged.
- Every existing golden is regenerated by the slice that restyles its view.

---

## 7. Verified fact: huh v2 compatibility

**huh IS bubbletea-v2 / charm.land compatible.** Evidence, gathered 2026-08-19:

- `charmbracelet/huh` `main` and tag `v2.0.3` both declare
  `module charm.land/huh/v2`.
- `v2.0.0` … `v2.0.3` are published, non-prerelease releases (v2.0.3 on
  2026-03-10).
- `v2.0.3`'s `go.mod` requires `charm.land/bubbletea/v2 v2.0.2`,
  `charm.land/lipgloss/v2 v2.0.1`, `charm.land/bubbles/v2 v2.0.0`; `main`'s
  requires `bubbletea/v2 v2.0.8`, `lipgloss/v2 v2.0.5`, `bubbles/v2 v2.1.1` —
  the same major line kb is on.
- **Compile proof:** a scratch module pinned to kb's exact versions
  (`bubbletea/v2 v2.0.8`, `lipgloss/v2 v2.0.6`, `bubbles/v2 v2.1.1`,
  `huh/v2 v2.0.3`) builds and runs a program constructing
  `Input`/`Text`/`Select`/`MultiSelect`/`Confirm`/`Note` inside a `huh.Form`
  embedded as a sub-model of a `tea.Model`.

**One integration detail, verified:** `*huh.Form` is *not* itself a
`tea.Model`. huh keeps a v1-shaped model interface —
`Init() tea.Cmd`, `Update(tea.Msg) (huh.Model, tea.Cmd)`, `View() string`.
Bubbletea v2.0.8 wants `View() tea.View`. Embedding costs a three-line adapter
(type-assert the returned `huh.Model` back to `*huh.Form`, wrap `View() string`
in `tea.NewView`), which is the normal nested-sub-model pattern regardless.
It is a non-issue, but it does mean huh cannot be a top-level program model.

**Consequence for this spec:** the "if huh is not v2-compatible, reassign its
roles to hand-crafted widgets" branch does **not** fire. huh keeps the roles
assigned in §5.2. The reason the editor/settings/ADR forms are still hand-crafted
is pointer hit regions, not compatibility — see §5.3 and §8, contestable call 1.

---

## 8. Contestable calls

Five decisions in this spec are genuine judgement, not derivation from #136 /
#137. They should be confirmed before the spec is locked.

**1. huh scope: fields yes, forms no.**
Chosen: adopt huh's `Confirm`, `Select`, `Note` for the ship/kill dialogs and the
priority/effort/source choice fields; keep the card editor, settings pane, ADR
split and issue import hand-crafted (restyled, not rewritten).
Alternative: adopt `huh.Form` wholesale for those four overlays and rebuild
kb's mouse hit-testing on top of it, or accept losing mouse targets inside forms.
Reason for the choice: huh owns layout and focus and exposes no hit regions;
mouse-first-class is locked by v1.0.1.

**2. The unfocused header band sits on the `Raised` tier.**
Chosen: `BandRest` = `Raised` (`#35404f`), one step *above* the card surface, so
the band is the most prominent element in an unfocused column and no new grey is
needed. Safe because selection only exists in the focused column, whose band is a
solid hue fill.
Alternative: keep Strata's band on `Surface`, where it is the same shade as the
panel body and does not read as a band, delivering only half of the #137
resolution. Web-faithful's own `#1a1f2b` is *not* an option — it quantizes onto
235 and collides with `Zebra` (verified, §1.7), and the 256 greyscale ramp has no
free step left. What is contestable here is the *direction*: a band lighter than
the cards it sits above is the web idiom, but it inverts Strata's "higher tier =
closer to the viewer" reading, where the topmost surface should be the overlay.

**3. Selection rail keeps the priority hue.**
Chosen: the selected card's rail thickens `▌`→`█` but stays priority-hued.
Alternative: the prototype's behaviour — re-hue to `Brand` on selection.
Reason for the choice: the prototype erases the P1 signal on the focused card;
`Raised` + full block + bold title are already three selection cues.

**4. Folding `labelKey` into `Surface`.**
Chosen: the dark half of a scoped label pill uses `Surface`, dropping the
`labelKey` slot and eliminating the palette's only real 256 collision.
Alternative: keep `#1b2330` as its own slot and rely on the prototype's argument
that `labelKey` and `Zebra` never share a frame (scoped pills are normal-density
only, `Zebra` is compact-only) — true today, but a latent trap for any future
change that renders a scoped pill at compact density.

**5. A width axis on the compaction threshold.**
Chosen: compact when `height < 30` **or** `columnInnerWidth < 22`.
Alternative: the prototype's height-only `height < 30`.
Reason for the choice: a wide-but-short frame and a tall-but-narrow frame fail
differently; height-only means a 60×50 terminal renders normal density and drops
most chips silently. The `22` figure is reasoned from the 80×24 capture, not
measured across a range — it is the number most worth arguing with.

---

## 9. Migration notes

### 9.1 Named anti-patterns to remove

All in `internal/tui/board_view.go` unless noted. These are the migration target
the research (#138) named explicitly.

| Site | What |
|---|---|
| `board_view.go:995-1000` | `var priorityColors map[int]color.Color` with literal hexes → `theme.Styles.Rail` / `Priority` |
| `board_view.go:1013-1019` | `var labelColors [5]color.Color` → `theme.Styles.Label` wheel |
| `board_view.go:1021` | `labelColor(tag)` hash → moves into `widget.Label`, hash unchanged |
| `board_view.go:1006` | `lipgloss.NewStyle().Foreground(...).Bold(true)` per priority chip → `widget.Priority` |
| `board_view.go:1009-1010` | `chip(label, fill)` building `NewStyle().Background(fill).Foreground(lipgloss.Color("#20242c"))` per call → `widget.Chip` |
| `board_view.go:1039-1040` | the scoped-label two-`NewStyle()` chain with `#20242c` / `#ffffff` literals → `widget.Chip` scoped form |
| `board_view.go:937,941,943` | inline blocked / due / overdue hexes → `Styles.Status` |
| `help.go:65-69`, `carddetail/model.go:541-546`, `cardeditor/view.go:232-237`, `adrsplit/view.go:81-86`, `issueimport/view.go:81` | five copies of `NewStyle().Border(RoundedBorder()).Padding(0,1).Width(...)` → one `widget.Overlay`; the borders themselves are deleted, replaced by §4's shade tiers and shadow |
| `ship_actions.go:925-932` | a dialog frame hand-drawn from `┌─┐│└┘` runes → `widget.Overlay` |
| `pointer/pointer.go:54` | raw `"\x1b[7m" + content + "\x1b[27m"` → `theme.Styles.Pressed` |
| `carddetail/model.go:743-759` | `styles.DarkStyleConfig` clone hardcoded to dark → `theme.Styles.Markdown` |
| scattered | `wideBoardWidth=100`, `maxPaneWidth=92`, `maxEditorWidth=96`, `maxPaneWidth=100`, literals `88`, `72`, `56` → `theme.Metrics` |

### 9.2 The compile-time seam

**Views take a `*Styles`. Views never construct styles.**

- Every render entry point gains a `*theme.Styles` parameter or reads one from a
  field the constructor set. The candidate signatures are
  `Model.View`, `renderBoard`, `renderFilterBar`, `renderBoardColumn`,
  `renderTaskLines`, `keyboardHelpSurface`, `settingsModel.View/Surface`,
  `taskActionSurface`, `carddetail.Model.View/frame/renderBody`,
  `cardeditor.Model.View/frame/bodyLines`, `adrsplit.Model.View/frame/bodyLines`,
  `issueimport.Model.View`.
- Prefer a `New(..., *theme.Styles)` constructor argument over a per-call
  parameter for the sub-package models — all five are constructed from one place
  (`internal/tui/model.go:103-151`), so threading is a single-site change.
- `formview.Input` / `formview.Area` already demonstrate the injection shape
  (`clean`, `cursor` func params); `*theme.Styles` joins them.
- The four package-level free functions holding all color today
  (`priorityChip`, `chip`, `labelColor`, `labelChip`) become `widget` functions
  taking `*theme.Styles`; their callers `cardMetaEntries` and `wrapMeta` follow.
- `carddetail.Model.renderMarkdown` (`model.go:79`) is the existing injectable
  renderer precedent and is how `Styles.Markdown` reaches glamour.

The seam is enforced, not merely documented: after the migration slice, a test
asserts no `lipgloss.NewStyle(` occurrence under `internal/tui/` outside
`internal/tui/theme/`. A view that cannot render without constructing a style has
found a missing token, and the fix is a new token, not an exemption.

### 9.3 Suggested slice boundaries

Not binding — the map lists slicing as still-fogged — but this spec cuts along:

1. `theme` package + `widget` primitives (`Chip`, `Rail`, `Button`, `Band`) +
   the 256 audit test + the no-`NewStyle` guard. No view changes.
2. Board: columns, bands, cards, chips, density, filter bar, footer.
3. Card detail overlay + the dim-and-composite recipe + `widget.Overlay`.
4. Editor and forms: card editor, settings, huh field adoption.
5. Remaining overlays: help, task action, ADR split, issue import.
6. Bubbles adoption: `key`, `help`, `spinner`, `progress`, `cursor`.

Each slice regenerates the goldens for the views it touches.
