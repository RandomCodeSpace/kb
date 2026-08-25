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

One palette, ~34 named slots, roles not hues. Nothing outside the theme package
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
| `StatusAlarm` | `#b31f14` | 124 | the armed fill of a two-step destructive confirm (§1.9) |

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
                                   StatusAlarm  #b31f14 -> 124
                                   Label1..5    209 / 69 / 71 / 141 / 214

TintPrimary     #a8b6ff -> 147     TintSuccess  #7fe0b0 -> 115
TintDanger      #ffa7a0 -> 217
```

The four slots added for the button variants of §1.9 are audited by the same
test as every other: they occupy 124, 147, 115 and 217, all previously free, so
the real-collision set is still empty and the alias set is unchanged.

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
cheap — a pure function over ~34 colors, no terminal involved.

### 1.8 Dimmed variant

The overlay backdrop uses a second, fully-built palette: every slot blended 66%
toward `Canvas`.

```
Dim(c) = c*(1-0.66) + Canvas*0.66   // per channel, 8-bit, round-half-up
```

Built once alongside the base palette. Never computed per frame.

### 1.9 Button variants

Added by [#157](https://github.com/RandomCodeSpace/kb/issues/157). The dogfood
finding: every button in the card-detail action row rendered the same `Raised`
surface, so a row of them said nothing about what any one of them did. A button
carries its meaning in its color, and the meaning is a *role*, never a hue the
caller picks.

Four variants, `theme.ButtonVariant`. Neutral is the zero value: a caller that
states no meaning gets the calmest surface rather than an accidental accent.

| Variant | Means |
|---|---|
| `ButtonNeutral` | dismissal, navigation, a side action |
| `ButtonPrimary` | the pane's main affirmative |
| `ButtonSuccess` | the state-advancing action |
| `ButtonDanger` | the destructive action |

Three new tint slots carry the readable-on-`Raised` form of a variant's hue, and
double as the fill its hovered state wears:

| Slot | Hex | x256 | Role |
|---|---|---|---|
| `TintPrimary` | `#a8b6ff` | 147 | blurred Primary label; hovered Primary fill |
| `TintSuccess` | `#7fe0b0` | 115 | blurred Success label; hovered Success fill |
| `TintDanger` | `#ffa7a0` | 217 | blurred Danger label; hovered Danger fill |

**State matrix.** The hue carries the meaning; the state carries the elevation.
A blurred button wears its variant as a tint on the resting surface, a hovered
one wears the tint as a fill, and a focused one wears the saturated hue.

| Variant | Blurred | Hovered | Focused |
|---|---|---|---|
| Neutral | `FgBase` on `Raised` | `FgBase` bold on `OverlayBand` | `FgOnAccent` bold on `FgSubtle` |
| Primary | `TintPrimary` on `Raised` | `FgOnAccent` on `TintPrimary` | `FgOnAccent` bold on `Brand` |
| Success | `TintSuccess` on `Raised` | `FgOnAccent` on `TintSuccess` | `FgOnAccent` bold on `StatusOK` |
| Danger | `TintDanger` on `Raised` | `FgOnAccent` on `TintDanger` | `FgOnAccent` bold on `StatusDanger` |

Neutral has no hue to spend, so its hovered state stays the surface step the
widget always used. That asymmetry is deliberate: hover is pointer feedback, and
on a variant with no hue the only honest cue left is elevation.

**Armed** is `FgBase` bold on `StatusAlarm`, for every variant. Arming is only
ever destructive, so it does not vary; it gets its own deeper fill because a
purge button arms *from* the Danger variant, and an armed button that rendered
the same fill as a focused Danger one would be the single state a user must not
misread. **Pressed** is unchanged: SGR 7 reverse video over whatever the state
resolved to, which preserves the pair's contrast ratio by construction.

**Contrast guard.** A button label is the smallest run in the TUI a user must
read before acting on it, so every pair above clears WCAG 2.x AA for normal text
— 4.5:1 — in truecolor *and* again after 256-color quantization. Measured:

| Variant | Blurred | Hovered | Focused | Armed |
|---|---|---|---|---|
| Neutral | 8.61 / 8.39 | 5.82 / 5.50 | 7.76 / 7.88 | 5.51 / 6.41 |
| Primary | 5.41 / 4.82 | 9.93 / 9.28 | 6.02 / 5.71 | 5.51 / 6.41 |
| Success | 6.60 / 5.74 | 12.13 / 11.03 | 8.25 / 7.11 | 5.51 / 6.41 |
| Danger | 5.65 / 5.57 | 10.38 / 10.71 | 6.26 / 6.29 | 5.51 / 6.41 |

(truecolor / x256; worst cell 4.82.) The theme package ships the audit as a
test, the same mechanism §1.7 uses for the palette: re-hue a variant below the
floor and the build fails. A second test asserts the four variants stay
separable at 256 colors — a variant that collapsed onto another's index would
reproduce the finding this section exists for, on the terminals least able to
afford it.

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
| Focused pill (traversal over a pill set) | End caps thicken `▐`/`▌` → `█`/`█` — the rail-thickening vocabulary applied to §3.6's caps; zero width change (added by #207) |

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
| `MinColumnWidth` | `16` | Narrowest panel a column may shrink to and still hold a title |
| `OverlayInsetX` | `2` | Overlay content inset from the panel edge |
| `TableGutter` | `1` | Columns between two cells of a `lipgloss/v2 table` row (§5.2) |

**Chrome row budget.** Normal density spends 4 rows: top bar, toolbar, page
padding, footer. Compact spends 3 (no page padding). Everything else is board.

**Column widths.** Available width = `frameW - 2*PageMarginX - (n-1)*ColumnGutter`,
split evenly with the remainder distributed to the leftmost columns. The split
is the whole rule: the columns own the entire frame and there is no upper clamp.
If the even split falls below `MinColumnWidth`, every column takes the floor
instead and the row is clipped at the frame edge — fewer readable columns beats
a screenful of slivers.

**Amended by [#151](https://github.com/RandomCodeSpace/kb/issues/151).** The
original rule clamped every column to a `MaxColumnWidth` of 52 and centered the
group, on the reasoning that extra terminal width must never become stretched
cards. On the reference hardware — a 14-inch laptop terminal, roughly 180-220
columns by 45-60 rows — that produced a 211-column board rendered as a centered
strip with ~50 dead columns of canvas either side of it. The web UI the TUI is
at parity with filled its window. Dead canvas is the worse failure, so the clamp
is gone and a floor takes its place.

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
▌P1 3d old ▐blocked▌ ▐in 2d▌ 🟨 M            row 2  meta chips
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
▌P1 3d old ⛔ !2d 🟨 M bug            row 1  meta + labels, flat chips
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
| 5 | Effort | `🟦 S` / `🟨 M` / `🟧 L`, `FgSubtle` | same |

Priority survives longest because it is never a pill and is only two cells.

The effort chip's own interior space is not the chip separator: it is the
§10.4.1 adjacency rule, and the chip is four cells in both densities — a
two-cell square, the column it owns, and the letter. The letter is not
redundant with the square: color alone must not carry the value, and the
letter is what still reads on a font that has no pictograph and draws tofu.
An effort value the S/M/L scale does not name falls back to `Diamond` and
three cells. `metaRowWidth` measures each chip rather than assuming a width,
so the survival order and the column-wide drop of `metaDepth` carry the cost
without a constant to maintain; the fourth cell is spent on the last label of
the compact flat row at the widths where that row was already full.

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

Cost: `width(text) + 2` columns. Both caps carry the fill color as foreground;
the scoped variant substitutes `FgSubtle` on `Surface` for the key run only, so
the pill reads hue-bracketed with the dark key half inside the bracket. The
first cap was `Surface` until #219, which is near-invisible in truecolor and a
grey bar once quantized.

**Inactive variant** (added by #207 for toggleable pill sets — today the filter
bar; re-hued by #208). A pill that is present but not selected withdraws its
*fill*, not its hue: `ChipRunsTint(fill, surface)` drops the fill to `Surface`
so the pill cannot read as selected, and moves the wheel hue onto the body text
as foreground in place of `FgOnAccent` — §1.9's rule that a variant which has
lost its state still carries its identity as a tint on the resting surface —
while the end caps keep the wheel hue they carry in the filled form (#219): the
withdrawal takes the fill, never the identity. The
scoped key run is `FgSubtle` on `Surface`, one step under the hue, preserving
the two-tone split; it sits at the secondary rather than the tertiary role
because it also carries the toggle marker, which is prose a user must read
before acting and so takes the §1.9 AA floor with it. All five wheel hues clear
AA 4.5:1 on `Surface` in truecolor and after 256-color quantization (worst 4.97
and 5.19), which is what permits the hue on prose. The caps carry it too, but
as glyph area they take no AA floor; what binds them is §1.7 separability,
audited on cap foregrounds against every ground a pill lands on (`Card`,
`Zebra`, `Raised`, `Canvas`, `OverlaySurf`).
Hue-on-fill against hue-on-surface is the toggle affordance in color; the
equal-width two-cell marker inside the caps (`MarkFilterOff` `+ ` /
`MarkFilterOn` `x `, §10.4.1) carries it where color cannot — at `FidelityFlat`
(§10.7.5), and equally in the compact form, which has no fill and no cap to
spend, the marker is the entire toggle signal.

**Focused pill** (keyboard traversal over a pill set): the end caps thicken
`▐`/`▌` → `█`/`█`, borrowing §2.4's rail-thickening vocabulary. Zero width
change — the old `>label<` bracket form cost two cells and reflowed the row on
every cursor move, a §10.4.4 violation the plain-text form carried. Toggle,
focus and hover are three orthogonal axes; none moves a cell.

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
6. **Action row**, the last body row of the panel, directly above the footer
   band: the state-appropriate actions as visible padded buttons, inset
   `OverlayInsetX`, laid out by `ButtonGroup` on `OverlaySurf`. It is pinned,
   not scrolled: it spends one of the panel's body rows, and the scroll window,
   the focus-follow of a choice list and the pointer viewports all resolve the
   body height from that one subtraction. A panel whose state offers no action
   -- a write in progress, a check in flight -- spends no row on it.
7. **Footer band**, last row of the panel: `OverlayBand`, `FgSubtle`, keyboard
   hints inset `OverlayInsetX`. Hints only: a band re-renders its own style
   around its content, so an embedded button's reset would drop the band
   background for the rest of the row. Buttons live in body rows.

**Geometry.** Every content overlay — card detail, card editor, settings, ADR
split, issue import — resolves the same panel size, centered. The rule has two
regimes, split at the `WideFrame` threshold of 100 the board already collapses
on, which is how a responsive modal behaves:

| Regime | Panel width | Panel height |
|---|---|---|
| `frameW < 100` | `frameW - NarrowSlackW` | `frameH - NarrowSlackH` |
| `frameW >= 100` | `round(frameW * WidthPct / 100)` | `round(frameH * HeightPct / 100)` |

| Token | Value | Applies to |
|---|---|---|
| `WidthPct` | `85` | Share of a wide frame's width the panel spans |
| `HeightPct` | `88` | Share of a wide frame's height the panel spans |
| `FrameSlackW` | `2` | Columns a proportional panel always leaves free |
| `FrameSlackH` | `2` | Rows a proportional panel always leaves free |
| `NarrowSlackW` | `4` | Columns a narrow-frame panel leaves free |
| `NarrowSlackH` | `2` | Rows a narrow-frame panel leaves free |
| `MinPaneW` | `24` | Narrowest panel the proportional rule will produce |
| `MinPaneH` | `8` | Shortest panel the proportional rule will produce |
| `ContentMax` | `96` | Readable measure cap for prose inside a panel |

In the proportional regime the share is raised to `MinPaneW` / `MinPaneH` and
then capped at `frame - FrameSlack` on each axis, so the panel never touches the
edge it casts its shadow onto. Below `pw < 24` or `ph < 8` the overlay does not
render as a panel and the view falls back to full-frame, which is what keeps the
frozen v1.0.1 dismissal behaviors reachable on a terminal too small to center
anything in.

**Content measure.** A body row inside a panel is `min(pw - 2*OverlayInsetX,
ContentMax)` wide. The panel grows with the frame; the prose column inside it
stops at the point where a line stops being scannable. Bands (header, footer,
section breaks) always span the full panel width.

The two content-sized dialogs keep fixed width caps, because their height is
their content and a proportional panel would frame a handful of rows in a
screenful of surface: task action 72, help 56.

**Amended by [#151](https://github.com/RandomCodeSpace/kb/issues/151).** The
original geometry was `pw = min(72, frameW - 8)`, `ph = min(13, frameH - 6)` for
the card detail pane, with per-overlay width caps of card detail 92, editor 96,
ADR split 100 and issue import 88. On a 211x52 laptop terminal that rendered the
detail pane at 72x13 — 34% of the frame width, 25% of its height — against a web
UI whose detail modal was a large centered panel. Those caps are replaced by the
proportional rule above; the narrow regime reproduces the v1.0.1 sizes on frames
below 100 columns, so nothing on a small terminal shrank to buy this.

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
| Button / action | kb `widget` | `Button(o ButtonOpts) string` + `ButtonGroup(gap int, bs ...string) string` — `ButtonOpts{Text string, Variant theme.ButtonVariant, Selected, Hovered, Armed bool, UnderlineIndex int, Padding [2]int}`; `Armed` is kb's addition for the purge/remove two-step, and `Variant` is the semantic role of §1.9, assigned per surface in §5.4. A rendered button is `Padding{1,1}` and a group's gap is 1, so an action reads as a filled surface wider than its label. `UnderlineIndex` marks the hotkey where the label spells it; where it does not, the caller appends the key in parentheses and underlines that. This is a display convention, never a keymap change |
| Status line / footer | kb `widget` | `StatusBar(o StatusOpts) string` — `StatusOpts{Dot theme.Slot, State string, Hints []string, Width int}`; responsive hint ladder is the caller's |
| Filter bar | kb `widget` | `FilterBar(o FilterOpts) string` — `FilterOpts{Field string, Chips []string, Count string, Width int}`. Status note (#207): the bar's *content* now matches this shape (pill chips via `widget.Chip`), but the bar itself remains inline in `board_view.go` because it builds pointer hit regions as it renders — this row is a target shape, not a shipped widget |
| Top bar | kb `widget` | `TopBar(o TopBarOpts) string` — brand pill, title, user, right-aligned counters |
| Overlay panel | kb `widget` | `Overlay(o OverlayOpts) string` — `OverlayOpts{Title, Seq string, Sections []Section, Footer string, W, H int}`; owns the shadow, bands and section breaks of §4 |
| Overlay shadow | kb `widget` | folded into `Overlay`; not separately callable |
| Checkbox row | kb `widget` | `Check(label string, state CheckState, focused bool) string` — `☐ ☑ ☒` |
| Key/value field row | kb `widget` | `Field(label, value string, w int) string` — the 12-column label gutter of §4 |
| Scroll indicator | kb `widget` | `ScrollHint(cur, total int) string` — `12/40` in `FgMuted`; charm ships no scroll-position label, and the offset it reads now comes from the adopted viewport (§5.2) |
| Table adapter | kb `widget` | `Table(styles, rows [][]string) []string` — the adapter that feeds `lipgloss/v2 table` (§5.2) and hands rows back one line each. Not an element: the only thing in `widget` that wraps a charm component instead of replacing one |

### 5.2 Charm-sourced components

| Element | Source | Notes |
|---|---|---|
| Single-line text input | `bubbles/v2 textinput` | Already in use in 12 files. Feed it `theme.Styles.Input` (a `textinput.Styles` embedded in kb's Styles, crush's pattern). |
| Multi-line text input | `bubbles/v2 textarea` | Already in use in 7 files. Same: embed `textarea.Styles`. |
| Cursor | `bubbles/v2 cursor` | Adopt to replace the three hand-rolled `cursorViewport` helpers. |
| Markdown | `glamour/v2` | Already in use. The `styles.DarkStyleConfig` clone at `carddetail/model.go:743` becomes `theme.Styles.Markdown ansi.StyleConfig`, derived from the palette, injected through the existing `markdownRenderer` func field. **Parity-grammar contract** (recorded by #211/#213, previously living only in `parityMarkdown`'s doc comment): (1) the grammar is per-line and starts at column zero — indentation is not syntax and is dropped as prose whitespace, matching the frozen web renderer; no construct spans two lines except a fence. (2) Neutralizing out-of-grammar syntax must be invisible, which bounds it to glamour's fixed eighteen-pair escape replacer (`ansi/baseelement.go`) — glamour does not run goldmark's unescaping, so any character outside that set must be neutralized through the character-reference channel (`~` → `&#126;`, `&` → `&amp;`, leading `:` → `&#58;`), never a backslash. A future renderer change that widens either side of this contract must re-run the leak audit (`TestRenderedMarkdownLeaksNoEscapes`). One sanctioned divergence from the frozen web grammar: `kb://task/<seq>` is recognized as an autolink and rendered as an openable reference (#212) — TUI-side rendering and navigation only, wire format unchanged; a parity audit must not "fix" it. |
| Keybinding registry | `bubbles/v2 key` | **Adopted** (#153) for the help pane: `helpKeys` in `help.go` is the registry, and an unavailable feature is a disabled binding rather than an omitted line, which is the self-managing keymap bubbles documents. The board's own footer ladder is still hint strings; it is not driven by the registry yet. |
| Help pane | `bubbles/v2 help` | **Adopted** (#153) for the help overlay body: `FullHelpView` renders the two key columns, `help.Styles` is `theme.Styles.Help`. The overlay chrome around it stays kb's `Overlay` widget, and the footer band composes the dismissal ladder from the same registry rather than pulling the component's surface token into a band row. |
| Overlay body scrolling | `bubbles/v2 viewport` | **Adopted** (#153) for the card detail body. The viewport owns the offset, the clamp and the content; the pane drives it programmatically (`ScrollUp` / `ScrollDown` / `GotoTop` / `SetYOffset`) because the frozen v1.0.1 deltas and focus-follow are kb's, not the component's defaults, and the wheel is routed through the pointer map. `viewport.View` is not used: a panel body row carries `OverlaySurf` edge to edge and the component pads plain. |
| Aligned key/value rows | `lipgloss/v2 table` | **Adopted** (#153) for the settings pane and its forge-integration rows, through `widget.Table`. Cell styles are layout-only (`theme.Styles.Table`, `TableGutter`): the table lays out plain text and the view paints each row with the token its role names. Columns size to content — a forced table width spreads slack across every column, which is the opposite of what a label gutter wants. |
| Spinner | `bubbles/v2 spinner` | Adopt for every `…ing…` busy state (drafting, saving, fetching, importing) that is currently static text. |
| Progress | `bubbles/v2 progress` | Adopt for `issueimport`'s `writing i/N`. |
| Confirm dialog | `huh/v2 Confirm` | Assigned: the ship / kill confirm prompt's yes-no core. |
| Choice row | `huh/v2 Select` | Assigned: the single-row `Priority` / `Effort` / `Source` / `Max stories` choice fields, in its inline form. **Amended by [#152](https://github.com/RandomCodeSpace/kb/issues/152):** the three-way `Cancel / Tick everything / Ship anyway` guard and the kill prompt's choices are no longer Select's. Select renders a vertical option list; those two are dialog choices and the dogfood verdict of map #136 requires a dialog choice to be a visible button, so they render as a `ButtonGroup` row that stacks one button per row when the panel is too narrow for the group. Confirm keeps the yes/no core, with its `FocusedButton` and `BlurredButton` built from the same `Styles.Button` tokens the widget uses. **Amended by [#157](https://github.com/RandomCodeSpace/kb/issues/157):** those two tokens are the Neutral variant's focused and blurred states, and that is a limitation, not a preference — huh exposes one button pair per Confirm, while the pair spans two meanings (`Cancel` and `Ship anyway`). Painting it with either meaning would lie about the other, so the confirm carries the calmest variant and the hued treatment lives in the `ButtonGroup` form of the same guard. |
| Note / disclaimer block | `huh/v2 Note` | Assigned: the AI disclaimer blocks in `adrsplit` and `carddetail/drift`. |

### 5.3 Explicitly not sourced from charm, with reasons

| Element | Charm candidate | Why not |
|---|---|---|
| Card list / column stack | `bubbles/v2 list` | Owns its own filtering, pagination, status bar and keymap; kb's columns are a miller-column board with drag-and-drop lift and per-column scroll windows. Adopting `list` means fighting it. |
| Comment / blocker-link pickers | `bubbles/v2 list` | Same, plus they need pointer hit regions per row. |
| Board grid | `bubbles/v2 table` | Cards are multi-row surfaces, not cells. |
| Editor form as a whole | `huh/v2 Form` | The card editor carries a label-suggestion dropdown, a similar-items block with per-row dismiss, a stale-refresh banner and mouse hit regions on every control. huh owns layout and focus and exposes no hit regions. Its *fields* are adopted (§5.2); its `Form` container is not. See §8, contestable call 1. |
| Settings pane | `huh/v2 Form` | Same, plus dynamic forge row groups with locked fields. |
| ADR split review stage | `huh/v2 Form` | Per-story row groups generated at runtime with independent include/priority/effort state. |
| Pressed feedback | — | Stays SGR 7 reverse video, but as `Styles.Pressed`, not a raw escape in `pointer.go:54`. |

**Amended by [#153](https://github.com/RandomCodeSpace/kb/issues/153).** The
sweep that carries the map's final component-sourcing policy — a charm component
is used wherever one exists — moved four rows out of this table and into §5.2:
overlay scrolling, the help pane, the keybinding registry and the settings
key/value rows.

The overlay-scrolling refusal was wrong on the facts. `bubbles/v2 viewport`
v2.1.1 exposes `YOffset()` and `SetYOffset(n)`, which is exactly the
offset/region pairing the pointer map needs; the pane reads the offset for its
`pointer.Viewport` rects and the component keeps the clamp. What the component
genuinely cannot do here is render: `viewport.View` pads its window with plain
`lipgloss` width/height padding, and a panel body row has to carry `OverlaySurf`
to both edges. So the split is offset to the component, rows to the widget.

`bubbles/v2 list` stayed unadopted, and the reasons above are unchanged — the
board's columns are a drag-and-drop miller board, and the pickers need a hit
region per row. The map's "do not force-fit" applies: there is no remaining
list-shaped rendering in the TUI that `list` would own rather than fight.

**Composing an adopted component onto a kb surface.** A charm component paints
its own runs and closes each with a reset, and it writes plain spaces between
them — `bubbles/help` does this between its key and description columns, and
`lipgloss` does it where it joins columns of unequal length. Those cells punch
holes in a panel. `theme.Styles.SurfaceRun(slot, content)` arms the surface
background and re-arms it after every reset in the content, the same shape
`PressedRun` already had for the pressed attribute. Any view laying a
component's output onto a shade tier goes through it.

### 5.4 Button variant assignment

Added by [#157](https://github.com/RandomCodeSpace/kb/issues/157). Every button
surface in the TUI, and the variant of §1.9 it carries. The variant is a
property of the *action*, carried structurally on the control or the row that
owns it — never recovered by matching a rendered label.

| Surface | Button | Variant |
|---|---|---|
| Card detail action row | `Edit` | Primary |
| | `Check`, `Restore` | Success |
| | `Kill`, `Del`, `Unlink`, `Purge` | Danger |
| | `Drift`, `Comment`, `Link`, `Close` | Neutral |
| Card detail, comment mode | `Save comment` | Primary |
| Card detail, link mode | `Add link` | Primary |
| | `Toggle direction`, `Cancel` | Neutral |
| Card detail, delete confirm | `Delete`, `Confirm delete` | Danger |
| | `Cancel` | Neutral |
| Card detail, drift mode | `Check selected`, `Update baseline` | Primary |
| | `Back` | Neutral |
| Card editor | `Save card` | Primary |
| | `Cancel`, `Draft`, `Cancel draft`, `Dismiss all similar items` | Neutral |
| Settings | `Save AI settings`, `Save` | Primary |
| | `Test connection`, `Test`, `+ Add integration` | Neutral |
| | `Remove`, `Confirm remove` | Danger (armed on the second step) |
| Ship guard | `Tick everything` | Success |
| | `Ship anyway` | Danger |
| | `Cancel` | Neutral |
| Kill prompt | `Kill without reason`, `Kill with reason` | Danger |
| | `Cancel` | Neutral |
| Purge prompt | the single arm button | Danger (armed on the second step) |
| ADR split | `Propose stories`, `Add selected (N)` | Primary |
| | `Cancel`, `Close`, `Back to source` | Neutral |
| Issue import | `Import` | Primary |
| | `Cancel`, `Back`, `Close` | Neutral |
| huh `Confirm` | both buttons | Neutral (§5.2: one pair, two meanings) |

**Recorded judgment calls.**

- `Ship anyway` is **Danger**, not a warning shade. It overrides a guard the
  board raised over open checklist items or a blocked flag; the variant set
  spends no fifth token family on a single button, and the guard's other
  affirmative (`Tick everything`) already carries Success, so the pair reads as
  "do the work" against "skip the work".
- `Unlink` is **Danger**. It removes a relation, which is a delete with a softer
  name; `Link`, which creates one, is Neutral because it opens a picker rather
  than committing anything.
- The AI actions (`Draft`, `Test connection`, `Test`) are **Neutral**. A pane has
  one primary affirmative, and in every one of those panes it is `Save`.
- `Add selected (N)` is **Primary**, not Success: it is the affirmative of the
  ADR split's second stage, and Success is reserved for advancing a card's state
  on the board.

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
    Button  ButtonSet      // [4]ButtonStyles by variant (§1.9); Rest, Focused, Hovered, Armed, Pressed
    Pressed lipgloss.Style
    Table   TableStyles    // Cell, Last — layout-only cells handed to lipgloss table

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
  reviewable rule, enforced by `theme/seam_test.go`, which walks `internal/tui`
  and fails on any construction outside `theme/`. Its allowlist may only shrink;
  as of #153 it is **empty** — `help.go` was the last entry and adopting
  `bubbles/help` removed it.
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

---

## 10. Craft layer

Sections 1-9 fix what kb renders: the token system, the geometry, the widget
inventory. This section fixes the layer on top of it — gradients, motion, timing,
glyph vocabulary, hover, brand, per-project accent, and the empty / loading /
error states. It is the difference between a TUI that is correctly specified and
one that is finished.

**Provenance.** The technique donor throughout is **crush at `25c8b72`**, read
through research tickets
[#178](https://github.com/RandomCodeSpace/kb/issues/178) and
[#179](https://github.com/RandomCodeSpace/kb/issues/179) on map
[#177](https://github.com/RandomCodeSpace/kb/issues/177). Every technique below
carries a `file:line` receipt into that tree. Where a value is adopted unchanged
the receipt is the argument; where it is adjusted, the reasoning is written out;
where a technique is refused, the refusal is written out too, so it is not
re-proposed.

**Three constraints are locked before this section and are not reopened by it.**

1. **`View() string`.** kb composes strings. It owns no cell grid, no
   compositor and no frame loop. A donor technique that depends on a cell buffer
   does not transfer, and this section says so at each point where that bites.
2. **Tick-only motion.** The single available mechanism is a `tea.Tick` chain
   that re-enters `Update`, mutates an integer, and lets the next `View()`
   render a different string. That is also the only mechanism crush uses — a
   repo-wide grep for `harmonica|easing|tween|spring|typewriter` returns zero
   hits, and its entire engine is `tea.Tick(time.Second/fps, ...)` frame stepping
   (`internal/ui/anim/anim.go:505-507`). No springs, no easing curves, and no
   wall-clock read on a paint path.
3. **Truecolor-first.** Truecolor on a dark background is the reference target,
   as it is for §1. Everything below it is a **degradation, never a design**
   (§10.7.5).

**§10 adds no palette slot.** Every color it names is a slot §1 already ships at
the hex and x256 index §1.7 already audited, so the collision guard, the alias
set and the §1.9 contrast audit are untouched by this section. The one
derivation that could have reopened them — the per-project accent — is closed by
construction instead (§10.7.2). Individual subsections do not repeat this claim.

Three cross-cutting rules are stated once and referenced everywhere else: all
timing values live in §10.3, all gradient ramps live in §10.1, and the no-reflow
parity rule lives in §10.4.4.

---

### 10.1 Gradient rules

A gradient in this language is **chrome that says something about state**. It is
never decoration laid over content. That is not a taste position: it is the rule
the donor already follows. Every gradient in crush lands on a wordmark, a dialog
title rule, a progress meter or a spinner glyph
(`internal/ui/logo/logo.go:90,158`, `internal/ui/model/header.go:58`,
`internal/ui/common/elements.go:239`, `internal/ui/model/pills.go:54`,
`internal/ui/anim/anim.go:234,237` — grep-verified, complete). Not one lands on a
line of user text.

kb's budget is tighter still: **four surfaces, five named ramps.**

#### 10.1.1 The primitive

`theme.Grad` is the only way a gradient reaches the screen. It lives in `theme/`
because that is the one package permitted to construct styles (§6.2).

```go
package theme

type Ramp uint8

const (
    GradSection       Ramp = iota // overlay section-break label, resting
    GradSectionDanger             // ... destructive pending
    GradSectionArmed              // ... armed
    GradMeter                     // progress meter fill
    GradWork                      // branded engine label; launch mark
    numRamps
)

// Grad paints s cluster by cluster along the named ramp. Foreground only.
func (s *Styles) Grad(r Ramp, text string) string
func (s *Styles) GradBold(r Ramp, text string) string
```

**Grapheme clusters, not runes, not bytes.** The input is split with
`uniseg.NewGraphemes` and each cluster is styled whole — crush's `grad.go:28-33`,
which is ~27 lines of real logic and the entire reason that file exists. Rune
splitting recolors the inside of an emoji ZWJ sequence and puts an SGR change
between a base character and its combining mark, which some terminals then draw
as two cells. kb renders emoji in card titles (§3.2) and cannot afford that, even
on a surface where a gradient is currently forbidden.

`rivo/uniseg v0.4.7` is already an indirect dependency (`go.mod:53`); `theme`
importing it promotes it to direct. **No new module enters the build.**

**No hand-rolled HCL ramp.** crush carries one (`anim.go:512-558`, 47 lines)
because it needed more than two stops. `lipgloss.Blend1D` in v2.0.6 is already
multi-stop (`blending.go:18,55-80`), so kb does not reproduce it and does not
take `lucasb-eyer/go-colorful` as a direct dependency. The colorspace differs —
`Blend1D` interpolates in CIELAB (`blending.go:78`, `BlendLab`) where crush's
ramp used HCL. On the two- and three-stop ramps below the difference is not
visible and is not worth a dependency.

**Ramps are prebuilt, never blended per frame.** §6.2 is non-negotiable: every
`lipgloss.Style` is constructed inside `New`, once. A ramp's interior colors are
not palette slots, so no per-slot style exists for them; the fix is to build the
ramp itself as styles at a fixed length and resample by index.

| Token | Value | Role |
|---|---|---|
| `GradSteps` | `24` | Steps in every prebuilt foreground ramp |
| `MeterCells` | `24` | Default bar width of the progress meter, before the caller narrows it. Promotes the literal at `issueimport/view.go:318` |
| `MeterMinCells` | `6` | Below this a meter renders its label only, no bar |

```
Styles.Grad [numRamps][GradSteps]lipgloss.Style   // foreground only
```

Cluster `i` of `n` takes ramp index `round(i * (GradSteps-1) / (n-1))`, which hits
both endpoints exactly. `24` is ~2.7x the longest gradient-bearing run in this
spec (`CHECKLIST`, 9 clusters); a run longer than 24 repeats colors, which is
correct rather than an error.

**Normative edge cases**, all from `blending.go:19-45`:

- `n == 0` — return `""`. Do not index the ramp.
- `n == 1` — index `0`. The single cluster wears the ramp's lead color.
- `n == 2` — the run is exactly the two endpoints. `Blend1D(steps, a, b)` returns
  `stops[:steps]` when `steps <= len(stops)` (`blending.go:23-25`), so a two-cell
  gradient is a two-tone pair by construction and needs no special case.

**Backgrounds.** Ramp styles set foreground only, and every gradient run is
emitted inside `Styles.SurfaceRun(ground, ...)` (§5.3). Per-cluster output is the
worst case of the hazard `SurfaceRun` was built for — dozens of short runs, each
closed with a reset that drops the band background for the rest of the row. One
rule, no exceptions: **a gradient run that is not wrapped in `SurfaceRun` is a
bug.**

#### 10.1.2 Where a gradient is permitted

Four surfaces. Nothing else.

| Surface | Ramp | Lead → tail | Hex | x256 | Ground |
|---|---|---|---|---|---|
| Overlay section-break label (`DETAIL`, `COMMENTS`, `CHECKLIST`) | `GradSection` | `FgBase` → `FgSubtle` | `#e3e9f2` → `#9aa5b6` | 255 → 248 | `OverlayBand` |
| ... destructive pending | `GradSectionDanger` | `TintDanger` → `FgSubtle` | `#ffa7a0` → `#9aa5b6` | 217 → 248 | `OverlayBand` |
| ... armed | `GradSectionArmed` | `TintDanger` → `StatusDanger` | `#ffa7a0` → `#ff5a48` | 217 → 203 | `OverlayBand` |
| Progress meter fill (§10.1.3) | `GradMeter` | `StatusInfo` → `StatusOK` | `#4f8ef7` → `#3fbf7f` | 69 → 72 | `OverlaySurf` |
| Branded engine label (§10.2.5) | `GradWork` | `Brand` → `TintPrimary` | `#4f8ef7` → `#a8b6ff` | 69 → 147 | caller's tier |
| Launch mark, per line (§10.6.3) | `GradWork` | `Brand` → `TintPrimary` | `#4f8ef7` → `#a8b6ff` | 69 → 147 | `Canvas` |

Every run is left to right across the run's full width. `GradWork` is one ramp
serving two surfaces: both are kb-identity chrome on a wait, both want the
brand hue moving toward its readable tint, and a second ramp between the same two
slots would be the same array under a different name.

**The section label carries the ramp, not a rule.** crush gradient-paints the
diagonal fill trailing a dialog title (`elements.go:230-243`). §4 step 5 forbids
that shape outright — section breaks are `OverlayBand` rows carrying a label,
*not rules* — so the ramp moves onto the label itself. The label's first cluster
becomes the brightest thing in the band and the run fades into it, which is the
same read the trailing rule produces with one fewer element on screen.

**The top-bar `kb` wordmark carries no ramp.** It carries the per-project accent
(§10.7.3), which is a flat foreground slot resolved once per theme build. A run
cannot be both a fixed brand ramp and a per-board identity hue, and the accent is
the one that tells two kb windows apart.

**Legibility.** Truecolor contrast of each endpoint against its ground:

| Ramp | Ground | Lead | Tail |
|---|---|---|---|
| `GradSection` | `OverlayBand` | 5.83 | 2.85 |
| `GradSectionDanger` | `OverlayBand` | 3.82 | 2.85 |
| `GradSectionArmed` | `OverlayBand` | 3.82 | 2.31 |
| `GradMeter` | `OverlaySurf` | 2.85 | 3.90 |
| `GradWork` | `OverlaySurf` | 2.85 | 4.70 |
| `GradWork` | `Canvas` | 6.02 | 9.93 |

§1.9's 4.5:1 floor governs **button labels** — "the smallest run in the TUI a
user must read before acting on it." None of these are that. §4 already ships the
section label at a flat 2.85:1 (`FgSubtle` on `OverlayBand`), so every ramp above
is *brighter at its lead* than the flat treatment it replaces, and no ramp lowers
the dimmest color already present on its ground. The build slice ships this table
as a test beside §1.7's; the figures are arithmetic, not test output, until it
does.

#### 10.1.3 Gradient as progress bar

The highest-yield trick in the donor (`pills.go:50-62`, 13 lines): build the ramp
across the meter's *full* width, then cut it at the filled count. The bar's color
position, not just its length, encodes how far along the work is.

kb does not hand-roll this. `bubbles/v2 progress` v2.1.1 implements exactly it:
with two or more colors and `scaleBlend` false (the default), `barView` computes
`blend = lipgloss.Blend1D(tw*multiplier, m.blend...)` over the total width and
writes only the first `fw` cells (`progress.go:371-402`, `:174-179`). §5.2 already
adopts the component for `issueimport`'s `writing i/N`; this section supplies its
configuration.

Built once in `New`, on `Styles.Progress progress.Model`:

| Option | Value | Why |
|---|---|---|
| `WithColors(pal[StatusInfo], pal[StatusOK])` | the `GradMeter` endpoints | Informational at the start, the completion hue at the end — both slots already mean that (§1.5). Below `FidelityFull` the component is configured with `StatusInfo` alone (§10.7.5) |
| `WithScaled(false)` | default | The whole point: the ramp spans the bar, the fill cuts it |
| `WithFillCharacters(Glyph.Rail, Glyph.Track)` | `▌` / `░` | Fill is the rail glyph the language already owns (§3.6); track is §10.4.1's addition |
| `WithoutPercentage()` | — | The caller already renders `i/N` |
| `EmptyColor = pal[FgMuted]` | — | 1.98:1 on `OverlaySurf` — dim is what a track wants |

Width is set per frame with `SetWidth` (`progress.go:342`), a layout call on an
already-built model, which §6.2 permits.

**Half-block doubles the ramp.** With `Full == '▌'` the component sets
`multiplier = 2` and paints each cell foreground `blend[i]` / background
`blend[i+1]` (`progress.go:365,371-374,387-397`), so a 24-cell meter resolves a
48-step ramp. That is why the fill glyph is the half block and not `█`.

**The track glyph is load-bearing at ASCII.** `theme.Downsample` strips color and
leaves runes, so a structure golden pinned to the ASCII profile (§6.4) sees only
glyphs. If fill and track were the same glyph in two colors, an ASCII-pinned
golden would show a bar with no readable position — it would assert nothing about
the one widget whose entire job is position.

**Pill form.** `widget.Meter(o MeterOpts) string` extends the §5.1 inventory:
`MeterOpts{Done, Total, Cells int, Ground theme.Slot}`. It wraps
`Styles.Progress.ViewAs(done/total)` in the §3.6 end caps — `Glyph.CapL` in the
ramp's first color, `Glyph.CapR` in its last — so the meter is a pill whose
interior is a meter. Cost is `Cells + 2` columns. Below `MeterMinCells` the caps
and bar are dropped and the caller's `i/N` text stands alone.

**A pill carries a gradient only when its interior is a bounded fraction.** That
is the whole rule for pills. The shipped counter, the column count, the
hidden-count chip and every label pill have no denominator; a ramp across them
would encode a position that does not exist. They stay flat.

**Never `SetPercent` / `IncrPercent`.** Those drive the component's built-in
harmonica spring at 60fps (`progress.go:288,304,314,351`, `defaultFrequency =
18.0`, `defaultDamping = 1.0`), which constraint 2 rules out. `ViewAs(done/total)`
bypasses the spring entirely: the meter is a pure function of the import's own
progress messages, `IsAnimating()` is never true, and the meter contributes no
tick chain at all. That satisfies the determinism contract (§10.2.2) without an
epsilon snap, because nothing is asymptotic.

#### 10.1.4 State-dependent chrome recolor

crush's sessions dialog re-ramps its title rule per mode — destructive→primary
while deleting, warningSubtle→accent while renaming
(`dialog/sessions.go:308-315`). The chrome itself changes color to say *you are
about to delete something*.

kb takes the same move on the overlay section-break label. The **mode** is a
property of the overlay, carried structurally the way §5.4 carries a button
variant — never recovered by matching a rendered label.

*Destructive pending* is any overlay state whose action row offers a Danger button
per §5.4 — delete confirm, kill prompt, remove integration, purge — before its
two-step arm. *Armed* is §1.9's Armed state. The lead is the same tint in both, so
arming does not re-tint the label; it **deepens the tail into the alarm hue**,
which is a change in the direction of the ramp rather than its identity. That is
the more honest signal: arming is an escalation of a state the user is already in,
not a new one.

**The overlay header band does not re-hue on destructive pending.** §4 step 4
pins it to solid `Brand`, and a transient mode inside a card detail is not worth
repainting the frame. **On Armed it does**: the band re-fills to `StatusAlarm`
with `FgBase` bold — the same pair §1.9 gives the armed button and measures there.
The frame and the button then say the same thing with the same color, which is
exactly the misread §1.9 exists to prevent. This is the only state in the TUI
that recolors a header band.

**The column header band never re-hues and never carries a gradient.** Its
focused contract is `FgOnAccent` bold on a solid column hue (§2.2), and any ramp
moves one end of that pair — the pair is the board's primary focus signal (§2.4),
and focus is binary. A gradient would make "which column is focused" a matter of
degree. The unfocused band is equally excluded: it is `BandRest`, the tier §1.1
spends its last free grey on, and a ramp off it lands in the 233-239 range where
every neighbouring tier already lives.

#### 10.1.5 Where a gradient is forbidden

Absolutely, on every surface not named in §10.1.2:

- **Anything a user reads as content.** Card titles and `#seq` (§3.2),
  description snippets (§3.3), field values (§4), overlay body prose, glamour
  markdown output (§5.2), help key and description columns, filter text, comment
  bodies.
- **Anything a user reads before acting.** Button labels and their hotkey
  underline (§5.1), footer hint ladders, choice rows. §1.9 measures those pairs to
  4.5:1 and a ramp puts one end below the measurement it was granted.
- **Any run whose color already carries meaning.** Priority markers (§1.4), label
  pill text (§1.6), status chips (§3.4), column band labels (§2.2). The color *is*
  the data; a ramp overwrites data with decoration.
- **Pill interiors** (§3.6) other than the meter of §10.1.3. `FgOnAccent` on a
  ramped fill varies its own contrast across the run.
- **The plain spinner tier** (§10.2.4). Its glyph is flat `FgSubtle`. A branded
  ramp on plumbing chrome devalues the branded tier, which is the entire argument
  the two tiers exist for.
- **Every background.** No `Blend2D`, no per-cell background ramp, anywhere. kb
  composes strings rather than owning a cell grid, and §4's bands must carry one
  background edge to edge for `SurfaceRun` (§5.3) to re-arm after a component's
  reset. A background ramp breaks that invariant on the exact rows that depend on
  it, and forces the foreground contrast to vary across a run that §1.9 or
  §10.1.2 measured at a single pair.

**Degradation.** Below `FidelityFull` a ramp does not render as a quantized ramp;
every index of it resolves to the ramp's lead slot, flat, across the whole run.
The mechanism, the reasoning and the one-place-it-is-decided are §10.7.5 rule 2;
the consequence here is that a ramp's *lead* endpoint is the color that has to
carry the surface on its own, which is why the legibility table of §10.1.2 leads
with it. Nothing in this section depends on a ramp being visible: the meter's fill
and track are distinguished by glyph as well as color, and the section label's
flat form is the treatment §4 already ships.

**Budget.** Four surfaces, five named ramps. A fifth surface is a spec change and
comes back here — it is not a slice decision.

---

### 10.2 Motion rules

Every moving thing in kb has exactly one available mechanism (constraint 2). The
donor and the recipient agree on the mechanism, so the technique transfers without
translation.

**Springs are out.** `charm.land/harmonica v0.2.0` is already an indirect
dependency and its `Update` is pure arithmetic with `dt` baked at construction, so
it would be deterministic and testable. It is still not adopted: springs never
exactly converge (asymptotic, `spring.go:220-221`), so every spring needs an
epsilon snap or its tick chain never terminates, and kb has no motion in this
section that interpolates a position over time. A dependency must earn its place;
this one has nothing to do.

**There is no reduce-motion flag.** Zero precedent exists across bubbletea,
bubbles, lipgloss, glow, crush and soft-serve. A flag doubles the code paths under
kb's coverage floor for no observed demand. The obligation it would discharge is
discharged instead by the shape of the motion: every animation in this spec is
brief, self-terminating, confined to a busy state, and never gates input.

#### 10.2.1 One clock

There is exactly one frame rate in the TUI. Every animated surface derives its
cadence from it as an integer stride; no surface authors a second interval. The
rate, the stride and every tick count this subsection names are tokens of
`theme.Timing` and are tabulated in §10.3.1 — this subsection references them by
name and states no duration of its own.

`Timing.PlainStride = 2` is not a free parameter. `bubbles/v2 spinner.Dot` ships
`FPS: time.Second / 10` (`spinner/spinner.go:33-34`), which is the cadence the
build renders today in `cardeditor`, `adrsplit` and `issueimport`. A stride of 2
on the single clock reproduces that exactly, so collapsing three independent
tickers onto one constant changes nothing a user can see. `theme.New` sets
`Styles.Spinner.FPS = Timing.PlainStride * Timing.Interval`; it must not be left
at the bubbles default, because a default is a second authored interval by another
name.

Every other timer already in the TUI stays what it is and is **not** motion.
`schedulePoll()` (`internal/tui/model.go:26,935-937`) fetches data and renders
nothing that moves. crush keeps the same separation — scrollbar auto-hide, toast
TTL and the double-click window are tick timers with no animation attached. Only
surfaces that change their rendered bytes on a tick answer to this section; the
rest answer to §10.3.

#### 10.2.2 Determinism contract

Reproduced verbatim from the animation feasibility verdict, §5. It is binding on
every animated surface in this spec and on every animated surface added after it.
A slice that cannot satisfy all seven points has not found an exception; it has
found an animation kb should not ship.

> 1. `View()` stays invariant under tick in the settled state — `TestViewIsByteStableAcrossMovingWallClock` keeps passing untouched.
> 2. Seed animated values **at target** on initial paint / `WindowSizeMsg` — existing goldens then need zero edits.
> 3. Every tick chain is self-terminating: cmd returns nil when settled (epsilon snap for anything asymptotic).
> 4. Tests step animation synchronously via injected tick msgs; intermediate frames are **unit tests only**, never teatest goldens.
> 5. No teatest `WaitFor` predicate may match mid-animation — gate predicates on settled markers. teatest has no virtual clock; a predicate matching a transient frame is a flake.
> 6. Timing constants (fps, grace periods, auto-hide delays) live as design constants in `internal/tui/theme/tokens.go`, injectable where tests need to collapse them.
> 7. Add one new byte-stability test per animated surface asserting the settled frame is tick-invariant.

**Reading point 2 for a spinner.** A spinner has no target position; its settled
value is *absence*. The zero value of `spin.Engine` renders the empty string, and
a surface that is not busy renders its ordinary static text with no engine output
in it at all. That is why the 21 Tier-A `View()` goldens and the 3 teatest goldens
need no edits: none of them capture a busy surface, and the animated state they do
carry renders as nothing. A golden that *does* want a busy surface pins a step
explicitly (§10.2.7); it never waits for one.

**Reading point 6 for this section.** Every timing value §10 names is a field of
`theme.Timing`, reached through the `*theme.Styles` a view already holds. A test
that needs to collapse the birth wipe sets `Timing.BirthDelay = 0` and
`Timing.BirthSteps = 1` on its own `*Styles` copy rather than sleeping.

#### 10.2.3 The in-repo reference implementation

The pattern is not new to kb. `internal/tui/cardeditor` already runs it, and it is
the shape every animated surface copies:

| Part | Site | What it does |
|---|---|---|
| Busy gate | `cardeditor/model.go:260` | `func (m Model) busy() bool { return m.drafting \|\| m.saving }` |
| Self-terminating tick | `cardeditor/model.go:264-271` | `spinTick` returns a nil command when `!busy()`, so an idle editor costs no timer |
| Chain start | `cardeditor/model.go:274` | `startSpinner` returns `m.spin.Tick`, batched with the operation's command |
| Routing | `cardeditor/model.go:251,414-415` | `spinner.TickMsg` is in the routed message set and dispatches to `spinTick` |
| Render | `cardeditor/view.go:323-330` | `busyPrefix` renders the current frame, and returns empty when the spinner has no frames |
| Synchronous test stepping | `cardeditor/view_test.go:151,169,185` | `model.Update(spinner.TickMsg{ID: model.spin.ID()})`, asserted frame by frame. No wall clock, no sleep |

Two properties of that code are load-bearing and are hereby normative for every
tick chain in the TUI, branded or plain:

- **The gate owns the chain.** The tick command is derived from the busy predicate
  on every tick, not from a separate "animating" flag that a code path can forget
  to clear. `view_test.go:151` asserts an idle editor's `spinTick` returns nil, and
  `:185` asserts a settled editor does not re-arm. Both assertions are required of
  every new surface.
- **The tick message is routed like any other message.** `IsMessage`
  (`cardeditor/model.go:251`) lists `spinner.TickMsg`, so the root forwards it. A
  branded surface adds `spin.StepMsg` to the same set;
  `cardeditor/view_test.go:188` is the test that catches the omission.

Everything §10.2.5 adds sits inside this frame. The branded engine replaces what
`busyPrefix` returns; it does not replace the gate, the chain, or the routing.

#### 10.2.4 Two tiers

crush runs exactly two spinner tiers and the split is by the weight of the work,
not by the surface: the branded gradient scrambler for agent turns
(`chat/assistant.go:233`, `chat/tools.go:194`, `chat/shell.go:86`) and plain
`bubbles` dots for plumbing — `spinner.MiniDot` for the todo pill
(`model/ui.go:434-437`), `spinner.Dot` for dialogs (`dialog/arguments.go:111-112`,
`dialog/oauth.go:97-100`, `dialog/aws_sso.go:68-70`, `dialog/commands.go:143-144`).
kb adopts the same split with the same reasoning. A branded frame is an
announcement that the machine is thinking on your behalf and may be a while;
spending it on a 30ms disk write devalues it, and a user who sees the branded
engine for a local file save learns to ignore it before the one case where it
means something.

**The rule.** A busy state gets the **branded** tier when the work is a network
round trip or a model inference — forge and AI. Everything else gets the **plain**
tier. Local store writes are plumbing regardless of how important the write is.
Those two names are the tier names; §10.8 uses them and no third name exists.

`Seed` is the deterministic per-surface birth seed of §10.2.5, a named constant,
never a runtime value.

| Surface | Busy state | Tier | Seed |
|---|---|---|---|
| Card editor | `drafting` (AI draft, `cardeditor/model.go:646`) | Branded | `SeedEditorDraft` = `1` |
| Card editor | `saving` (`cardeditor/model.go:971`) | Plain | — |
| Card detail | drift check (`Check selected`, AI) | Branded | `SeedDriftCheck` = `2` |
| Card detail | `saving` comment / link / delete (`carddetail/actions.go:343,360,379,403`) | Plain | — |
| ADR split | `Propose stories` (AI split, `adrsplit/model.go:228`) | Branded | `SeedAdrPropose` = `3` |
| ADR split | file load, card add (`adrsplit/model.go:228`) | Plain | — |
| Issue import | source list and preview fetch (`issueimport/model.go:170`) | Branded | `SeedImportFetch` = `4` |
| Issue import | `writing i/N` | Neither — `bubbles/v2 progress`, per §5.2 and §10.1.3 | — |
| Settings | `Test connection` / `Test` (`settings.go:553,638`) | Branded | `SeedSettingsTest` = `5` |
| Settings | `saving` (`settings.go:591,673`) | Plain | — |
| Board | `writeBusy` — move save, task action (`model.go:751`) | Plain | — |
| Board | `schedulePoll` | Neither — not motion (§10.2.1) | — |

The plain tier is unchanged from the current build in everything but where its FPS
comes from: `spinner.Dot`, foreground `Styles.Work.Label` (`FgSubtle`), flat, no
ramp (§10.1.5). No new widget, no new token beyond the stride.

#### 10.2.5 The branded spinner engine

Lives at `internal/tui/widget/spin.go`, alongside `card.go`, `panel.go`, `band.go`,
`chip.go` and `button.go` per §6.1. It imports `theme` and nothing from
`internal/tui`.

The engine paints with `Styles.Grad[GradWork]` (§10.1.2), `FgMuted` for the
pre-birth cells and `FgSubtle` for the suffix.

##### Engine vocabulary

Non-timing tokens. Every duration and tick count the engine reads is §10.3.1's.

| Token | Value | Role |
|---|---|---|
| `EllipsisStates` | `4` | `""`, `"."`, `".."`, `"..."` |
| `EllipsisField` | `3` columns | Width the ellipsis always occupies, space-padded |
| `SuffixField` | `6` columns | Reserve for `" 1m02s"`. Elapsed at or beyond one hour renders `59m+` |
| `MaxLabelW` | `48` columns | Longest branded label. Longer is truncated per §3.3 |
| `MaxEngines` | `1` | Branded engines that may tick at once (§10.2.6) |
| `BirthGlyph` | `"."` | The pre-birth cell |
| `ScrambleRunes` | `"abcdefghijklmnopqrstuvwxyz0123456789"` | 36 runes, one cell each, ASCII only |

##### Ramp indexing

The ramp is a fixed `GradSteps` long, not one style per column, and that is the
reason the engine constructs no style at render time. Column `i` of a label of
width `w` takes ramp index `i * (GradSteps - 1) / max(w - 1, 1)`, so a 48-column
label shares each ramp style across two columns — invisible on a two-stop blend
over a short run, and it keeps §6.2's rule intact without an amendment. Cells are
split with `uniseg.NewGraphemes` before indexing, per §10.1.1.

The styles carry no background; the caller lays the engine's output onto its shade
tier through `theme.Styles.SurfaceRun` (§5.3).

##### Frame set, prerendered and cached

crush renders every cell of every frame once at construction and caches the result
by an `xxh3` hash of its `Settings` (`anim.go:80-88`, `:201-314`), leaving the
render loop as pure `strings.Builder` concatenation (`:444-498`) with zero styling
work per frame. kb does the same with two changes.

First, kb caches **assembled frame strings**, not cells, because it has no cell
buffer to blit into and a `View()` app wants a string. Second, the cache key is
**FNV-1a 64** from stdlib `hash/fnv`, not `xxh3`. This drops a dependency crush
carries, and it matters for more than dependency hygiene: the same hash seeds the
birth schedule, so it must be stable across processes or contract point 4's
synchronous frame assertions would differ run to run.

`spin.Settings` is the hashed struct, and these are all of its fields:

| Field | Type | In the hash |
|---|---|---|
| `Label` | `string` | yes |
| `Width` | `int` — resolved label width after truncation | yes |
| `Seed` | `uint64` — the per-surface constant of §10.2.4 | yes |
| `Scramble` | `bool` — false is crush's `NoScramble` (`anim.go:115`) | yes |
| `Suffix` | `func() string` | no — a func has no stable identity; the suffix is appended live, never cached |

The frame array has a **derived** length:
`frames = Timing.BirthSteps + Timing.ScrambleSteps + EllipsisStates = 27`.
Indices `0 .. 22` are the birth frames, `23 .. 26` the settled frames, one per
ellipsis state. The engine holds exactly one cache entry — the frame array for its
current settings hash — and rebuilds it when the hash changes, which happens on a
theme rebuild, a resize that changes the label width, or a new operation with a
different label. **There is no package-level cache.** §6.2 forbids package-level
mutable style state, and a shared frame map is close enough to it to be worth
refusing; the ceiling of `MaxEngines = 1` makes a shared cache pointless anyway.

Cost at the worst case (`Width = MaxLabelW = 48`): 27 frames x 48 styled cells =
1296 `lipgloss.Render` calls, once per mount, plus 27 assembled strings. Every
subsequent frame is one array index and one concatenation.

##### Staggered birth

Each column gets its own birth step, so the label wipes in rather than appearing
whole. crush seeds this off `id + settingsHash` so different spinners stagger
differently while any one spinner stays deterministic (`anim.go:325-330`).

kb keeps the property and strengthens it: the seed is
`math/rand/v2 rand.NewPCG(hash, 0)` where `hash` is the FNV-1a of `Settings`,
whose `Seed` field is a **named per-surface constant** (§10.2.4) rather than a
runtime instance id. Two surfaces still stagger differently; the same surface now
staggers identically in every process, forever, which is what makes a frame
assertion a fixed golden rather than a seeded reproduction.

Column `i` draws `b_i = rand.IntN(Timing.BirthSteps)` in column order. At engine
step `s` (ticks since the chain started), with `b = s - Timing.BirthDelay`:

| Condition | Column `i` renders |
|---|---|
| `s < Timing.BirthDelay` | nothing — the engine emits `""` and the surface shows its static label |
| `b < b_i` | `BirthGlyph` in `Styles.Work.Birth` (`FgMuted`) |
| `b_i <= b < b_i + Timing.ScrambleSteps` | a rune drawn from `ScrambleRunes` by the same PRNG, in the ramp |
| `b >= b_i + Timing.ScrambleSteps` | the label's own rune, in the ramp |

Fully born at `b = Timing.BirthSteps + Timing.ScrambleSteps - 1 = 22`.
`Scramble: false` collapses the middle row into the last one — crush's
`NoScramble` (`anim.go:115`), for a surface that wants the wipe without the flash.

**The wipe is a class-B effect** (§10.7.6): its information lives in the color
difference between born and unborn cells, and a flattened wipe is a flash of
punctuation. Below `FidelityFull` the engine mounts at the settled frame and the
wipe is never armed. The ellipsis and the elapsed suffix are unaffected — they are
carried by glyph and text, not by hue.

##### Generation counter

The classic bubbletea bug: a tick chain restarted while an old one is still in
flight leaves two tickers running and the animation doubles speed. crush solves it
with a generation counter — `StepMsg{ID, Gen}`, `Start()` bumps the generation
(`anim.go:403-406`), `Animate()` drops a tick whose generation does not match
(`:421-423`), and `Stop()` bumps it again so the outstanding tick terminates the
chain (`:411-413`).

kb takes it as written, and it is binding on **every** repeating tick chain in the
TUI, not only this engine:

```
type StepMsg struct { Seed uint64; Gen uint32 }
```

- `Engine.Start()` increments `Gen` and returns a tick carrying the new `Gen`.
- `Engine.Step(msg StepMsg)` returns a nil command when `msg.Seed != e.seed`, when
  `msg.Gen != e.gen`, or when the surface's busy gate is false. The nil command is
  contract point 3; the generation check is the double-chain fix.
- `Engine.Stop()` increments `Gen` and clears the mount, so any tick still in
  flight is dropped on arrival.

`Seed` is in the message as well as the generation because a single `Update` tree
can route ticks for more than one engine — the front overlay's and, during the
handoff of §10.2.6, a background surface's last outstanding tick. Matching on
generation alone would let one engine consume the other's tick.

##### Dynamic suffix

`Settings.Suffix func() string` is crush's `anim.go:119`, wired there to a global
elapsed-turn timer (`internal/ui/common/timer.go:33-52`). kb wires it to the
elapsed time of the operation the busy gate is guarding, and it is the one place
in the engine that reads a clock — through the injected `now func() time.Time` the
board goldens already pin (`board_view_test.go:360-361`), never `time.Now()`
directly.

- The suffix is empty until `Timing.SuffixAfter` ticks after settle, then renders
  the whole elapsed seconds of the operation.
- Format: `12s` below one minute, `1m02s` at or above it, `59m+` at or above one
  hour. Right of the label, one space, `Styles.Work.Suffix` (`FgSubtle`),
  occupying `SuffixField` columns whether or not it is present (§10.4.4).
- **While the suffix is non-empty the ellipsis is suppressed** — the settled frame
  index is forced to `23` (state `""`). crush does the same (`anim.go:470-486`) and
  the reason is the whole rule: two things never move at once. Before the suffix
  appears the ellipsis is the motion; after, the counter is.

##### Assembled row

```
[SurfaceRun(surface,  <27-frame cache>  <ellipsis field, 3>  <suffix field, 6>)]
                      ^ label, ramped    ^ "" / . / .. / ...  ^ "" / 12s / 1m02s
```

Label width is `min(MaxLabelW, avail - EllipsisField - SuffixField)`; a longer
label is truncated with the §3.3 primitive. The row's total width is fixed for the
life of the mount, per §10.4.4.

#### 10.2.6 Concurrency ceiling and the background handoff

`MaxEngines = 1`. At most one branded engine ticks at a time, and it belongs to
the front-most open surface in the §4 z-order.

This is crush's offscreen-animation pausing (`internal/ui/model/chat.go:466-529`)
in kb's terms. crush pauses an item's chain when the item leaves the visible window
and restarts it on scroll (`ScrollXAndAnimate`, `:616-647`); kb's equivalent of
"offscreen" is "behind another overlay", because the z-order stack of §4 lets card
detail, settings, the editor and issue import all be open with only the last one
painted.

**The handoff, normatively:**

1. A surface that is not front-most calls `Engine.Stop()`. Its operation keeps
   running; only the animation stops. The generation bump kills the outstanding
   tick, so the chain does not leak.
2. The stopped surface renders its ordinary static busy label — the same string
   the branded tier shows during `Timing.BirthDelay`.
3. When the surface returns to front and its busy gate is still true, it calls
   `Engine.Start()`. The engine remounts at step 0 and wipes in again.

The remount is deliberate, not a compromise: resuming mid-wipe would require
persisting a step counter across a period in which no ticks arrived, which means
either a wall-clock read (contract point 4) or a frame count that lies. A second
wipe on return reads as the surface waking up, which is what happened.

The ceiling bounds the whole section's cost: one tick chain, one 27-entry frame
array, one PRNG.

#### 10.2.7 Test obligations

Contract point 7 requires one byte-stability test per animated surface. These are
the full obligations a motion slice carries, and they are unit tests: no teatest
golden in this repo may capture a mid-animation frame (contract point 4), and no
`WaitFor` predicate may name one (contract point 5).

| # | Assertion | Where |
|---|---|---|
| 1 | `spin.Engine{}.View() == ""` — the zero value renders nothing | `widget/spin_test.go` |
| 2 | `Frame(s)` for `s` = 0, `BirthDelay-1`, `BirthDelay`, `BirthDelay+22` (settled), and each of the 4 ellipsis states, asserted as fixed strings | `widget/spin_test.go` |
| 3 | The settled frame is identical for every `s` in a full `EllipsisStride` window | `widget/spin_test.go` |
| 4 | Two engines with different `Seed` constants produce different birth schedules; one engine reproduces its schedule across two constructions | `widget/spin_test.go` |
| 5 | The frame cache rebuilds exactly once for a repeated settings hash, and once more after a width change | `widget/spin_test.go` |
| 6 | A `StepMsg` with a stale `Gen` or a foreign `Seed` returns a nil command | `widget/spin_test.go` |
| 7 | Below `FidelityFull` the engine mounts settled and arms no wipe | `widget/spin_test.go` |
| 8 | Per surface: idle gate returns a nil tick; busy gate returns a tick; settled surface does not re-arm | each surface's `view_test.go` |
| 9 | Per surface: `IsMessage(spin.StepMsg{})` is true | each surface's `model_test.go` |
| 10 | Per animated surface: a settled-frame byte-stability test, contract point 7 | each surface's `view_test.go` |
| 11 | At most `MaxEngines` engines report mounted across the open z-order stack | `internal/tui/model_test.go` |
| 12 | `Styles.Spinner.FPS == Timing.PlainStride * Timing.Interval` | `theme/tokens_test.go` |

A busy-state golden, where a slice wants one, pins its step: build the model, set
the busy flag, `Update` exactly `n` `spin.StepMsg` values, then
`golden.RequireEqual` against `theme.Downsample(view, theme.ColorProfile)` per
§6.4. It never runs a program and never waits.

One crush technique explicitly does **not** transfer. `chat/assistant.go:257-269`
calls `a.Bump()` on every tick because crush's item render caches key off content
hashes that do not see the spinner's frame counter, so without the bump the spinner
renders frozen. kb caches no rendered rows; `View()` re-renders from the model
every frame. There is nothing to invalidate, and a slice that adds a row cache has
taken on this problem and owes a test for it.

---

#### 10.2.8 Ship celebration

Added by ticket #191 (map #177), which found this effect specified nowhere in §10.
The celebration is the board's acknowledgement that a card landed in DONE: the DONE
column's meta row (§2.3) pulses `FgMuted` -> `StatusOK` twice over
`Timing.CelebrateSteps` = 12 ticks (600ms), then settles. Both ship paths arm it —
the card drop and the task action.

The form is a flash, not a gradient sweep, because §10.1.2's four-surface gradient
budget is closed and a fifth surface is a spec change, not a slice decision. The
meta row is the one legal host: it is the row that just changed (`4 cards` ->
`5 cards`) and it is column chrome, while every neighbour is spoken for — the
header band never re-hues (§10.1.4), the card rail carries priority in every state
(§2.4), and the scroll affordance's tint belongs to the linger (§10.3.4).

Rules, all inherited and all binding: class B (§10.7.6 — below `FidelityFull` the
effect does not run and its chain is never armed; `Graded()` read once at the arm,
never on a render path); the determinism contract (§10.2.2 — the settled state is
the effect's absence, so `View()` stays tick-invariant; generation-guarded,
self-terminating); one motion per surface (§10.8.4 rule 4 — the busy predicate is
read on every step, so a write starting mid-celebration takes the motion back on
the same update); no-reflow parity (§10.4.4 — lit and dark differ in SGR alone).
The widget seam is `widget.PanelOpts.MetaLit`; the zero value is the old behavior.

### 10.3 Timing tokens

Timing is a token family for the same reason color is. A duration written inline at
its call site is a design decision made by whoever happened to be editing that
file, it cannot be reviewed as a set, and it cannot be turned off. kb has four such
literals today — `pollInterval` (`model.go:26`), `autoShipDelay`
(`ship_actions.go:21`), `similarDelay` (`cardeditor/model.go:29`) and
`settingsTestTimeout` (`settings.go:22`) — none of which was chosen against the
others.

**Every timing value in §10 is normative and lives in this subsection.** Other
subsections reference these tokens by name and state no duration of their own.

Two hazards bound the whole family, and both are already enforced by tests kb runs
today:

1. **A duration may never be read on a paint path.** `View()` must be invariant
   under the wall clock — `TestViewIsByteStableAcrossMovingWallClock`
   (`board_view_test.go:426`) asserts exactly this, and it passes today because the
   board renders against `m.renderedAt`, not `m.now()`. An effect expires because a
   *message* arrived, never because a render compared `time.Since` to a token. This
   is the difference between a timing system that is goldenable and one that is a
   flake generator.
2. **Every effect must collapse to zero.** No test in `internal/tui` may sleep. The
   determinism contract's rule 4 is the primary path; the collapse rule of §10.3.9
   is what makes the secondary path — a running teatest program — deterministic
   too.

crush is the donor for the values. It is a cell-buffer app, but every number here
governs *when a message is scheduled*, not how a frame is drawn, so the structural
difference does not reach this section.

#### 10.3.1 The token set

`theme.Timing`, a struct on `*Styles` beside `Metrics` and `Glyph` (§6.1).

**The clock.** One rate, and the strides derived from it (§10.2.1).

| Token | Value | Applies to |
|---|---|---|
| `FPS` | `20` | The only frame rate in the TUI. crush's `fps = 20` (`anim.go:21-49`) |
| `Interval` | `50ms` | Derived: `time.Second / FPS`. The argument to every animation tick |
| `PlainStride` | `2` | Ticks per frame of the plain spinner tier — `100ms`, today's `spinner.Dot` cadence |

**Tick counts.** Integers, not durations, because they index frame arrays.

| Token | Value | Applies to |
|---|---|---|
| `BirthDelay` | `5` ticks (`250ms`) | Ticks a busy gate holds before the branded engine becomes visible (§10.2.5) |
| `BirthSteps` | `20` ticks (`1000ms`) | Span over which the branded entrance wipe staggers. crush's `maxBirthSteps = 20` |
| `ScrambleSteps` | `3` ticks (`150ms`) | Ticks one cell spends scrambled between its birth step and its real rune |
| `EllipsisStride` | `8` ticks (`400ms`) | Ticks per ellipsis step. crush's `ellipsisAnimSpeed = 8` |
| `SuffixAfter` | `60` ticks (`3s`) | Ticks after settle at which the elapsed suffix appears |
| `BrandBirthSteps` | `12` ticks (`600ms`) | Span of the launch-screen reveal (§10.6.6) |
| `CelebrateSteps` | `12` ticks (`600ms`) | Span of the ship celebration (§10.2.8); its beat is derived, `CelebrateBeat() = span/4`, not a separate token |

**One-shots.** `time.Duration`, scheduled once and awaited by message.

| Token | Value | Applies to |
|---|---|---|
| `DialogGraceQuiet` | `425ms` | Input-quiet window a destructive prompt swallows |
| `DialogGraceMax` | `1500ms` | Absolute ceiling on that swallow |
| `DialogGraceReopen` | `500ms` | Reopen of the same prompt identity skips the grace |
| `ScrollActiveLinger` | `2000ms` | Scroll affordance holds its active tint after the last scroll |
| `DoubleClickWindow` | `400ms` | Second click on the same region id counts as a double-click |
| `InputCoalesce` | `16ms` | Program-level motion/wheel coalescing interval |
| `NoticeTTL` | `5000ms` | Footer notice self-dismissal (§10.3.7) |
| `PollInterval` | `1000ms` | Board poll tick (migrated, value unchanged) |
| `AutoShipDelay` | `350ms` | Ship-check follow-up tick (migrated, value unchanged) |
| `SimilarDelay` | `400ms` | Card editor similar-items debounce (migrated, unchanged) |

Twenty tokens. The rate/count split against the duration split is deliberate and
it drives the collapse rule of §10.3.9: zero means "do not run" to a clock and "run
now" to a one-shot, and conflating them produces a test that spins the CPU.

No surface may invent a twentieth. A slice that needs a duration not written here
has found a spec gap and comes back, per the preamble of this document.

#### 10.3.2 The clock, and its budget gate

`50ms` per frame is the floor at which stepped motion stops reading as a slideshow,
and it is crush's number. crush can afford it because every frame's every cell is
`lipgloss.Render`ed once at construction and cached (`anim.go:240-294`), leaving
the per-tick render loop as pure `strings.Builder` concatenation (`:444-498`). kb
is a `View() string` app: a tick re-runs the whole board render, overlay included.
Two rules follow, both normative:

- A motion surface **prerenders its frames at construction** and indexes them per
  tick. This is §6.2's cached style factory applied one level up: no styling work on
  a frame boundary. §10.2.5's frame cache is the reference implementation.
- The slice that lands the first `FPS`-driven surface adds `BenchmarkBoardView`
  over a full board at the reference frame (211x52). If a full `View()` exceeds
  **25ms** — half the frame period, leaving headroom for the terminal write — `FPS`
  drops to `10` and the benchmark result is recorded here. The gate is a rule, not a
  measurement; the 25ms figure is reasoned, not measured.

kb already pays a full re-render at 10fps whenever the card editor, ADR split or
issue import spinner runs, so the plain tier's cost is not new. The branded tier
doubles it.

**Generation guard.** Every repeating tick chain carries a generation counter and
drops mismatched ticks. The mechanism is §10.2.5's `StepMsg{Seed, Gen}`. kb's
existing self-terminating chain (`cardeditor/model.go:264-271`) satisfies
determinism-contract rule 3 but not this one: a second `Start()` while the first
chain lives doubles the rate. The guard is not optional.

#### 10.3.3 Destructive-prompt grace

crush swallows input into an async dialog until input has been quiet 425ms or
1500ms absolute has elapsed since open, and skips the grace entirely when the same
dialog ID reopens within 500ms (`dialog/dialog.go:52-62`, `:120-148`, `:168-176`,
`:216-234`).

**Values adopted unchanged.** They are feel constants tuned against the same
bubbletea key plumbing kb runs. One of the three stops being arbitrary once it sits
next to `DoubleClickWindow`: `DialogGraceQuiet` is deliberately **longer than**
`DoubleClickWindow`, so the trailing half of a double-click that opened a prompt
lands inside the grace by construction rather than by luck. Do not reduce one
without the other.

**Scope adjusted.** crush graces async dialogs — the ones that appear while the
user is typing. kb has no async dialog; every prompt opens from a deliberate
keystroke. kb's equivalent hazard class is the prompt whose affirmative is
destructive, and §5.4 already marks those structurally. The grace applies to, and
only to, a prompt carrying a `ButtonDanger` affirmative:

| Surface | Graced affirmative (§5.4) |
|---|---|
| Card detail, delete confirm | `Delete`, `Confirm delete` |
| Kill prompt | `Kill without reason`, `Kill with reason` |
| Purge prompt | the single arm button |
| Settings | `Remove`, `Confirm remove` |
| Ship guard | `Ship anyway` |

Every other overlay opens ungraced.

**What is swallowed.** Key messages and click messages both; either one resets the
quiet timer. A click has to be included: the second half of a double-click is
exactly the input the grace exists to eat.

**What is not swallowed: the dismissal ladder** (`esc`, `q`, `ctrl+c`). crush
swallows every key. kb does not, because the two directions have asymmetric costs:
a swallowed affirmative is the entire point of the mechanism, while a swallowed
cancel is a user who concludes the app has hung. The grace guards the commit, never
the exit.

**Relationship to the Armed two-step.** §1.9's `Armed` state and this grace guard
different things and both stay. The grace guards *arrival at* the prompt against
type-ahead; the arm guards *the commit* against a deliberate but mistaken press.
`DialogGraceReopen` exists so the two do not compound: the arm/confirm transition
re-renders the same prompt identity, and re-arming the grace on each step would
make a confirm feel broken. Prompt identity for the reopen check is the overlay's
z-order slot (§4) plus the target card id.

#### 10.3.4 Scroll activity

crush auto-hides its scrollbar 2s after scrolling and reserves the column only when
the bar will actually show (`common/scrollbar.go`, `model/chat.go:143`, `:46-50`,
`dialog/common.go:60-67`).

**Value adopted. Behavior adjusted: kb dims, it does not hide.** crush hides
because the bar owns a full column of pane width and it has a cell buffer to reflow
into. kb's scroll affordance is the `ScrollHint` label of §5.1 and any rail §10
puts on an overlay body edge, and kb composes strings: a column that appears and
disappears reflows the body measure of every row under it, twice, for a cue
(§10.4.4). So the column is reserved for the whole time the body overflows, and the
token governs tint rather than visibility:

| State | Treatment |
|---|---|
| Body does not overflow | Affordance absent, column not reserved |
| Overflows, settled | `FgMuted` |
| Overflows, within `ScrollActiveLinger` of the last scroll | `FgSubtle` |

Both are §1.2 slots at their §1.2 hexes. The settled state is the muted form, so
per determinism-contract rule 2 an overlay golden captures it on initial paint with
no edit, and the active form is a unit test that injects the scroll message and
asserts the frame.

The dim-at-rest reading also answers a discoverability problem hiding creates: a
body that scrolls but shows nothing saying so is a body the user does not scroll.
The muted rail is permanently legible; the linger only says *you just moved*.

#### 10.3.5 Double-click

crush: 400ms (`model/chat.go:23`). Adopted unchanged.

kb has no double-click gesture today — `pointer.handleClick`
(`pointer/pointer.go:247-260`) classifies a single press and hands back a press
command. The token adds one classifier rule, in `pointer`, not in a view:

- A click is a double-click when it lands within `DoubleClickWindow` of the previous
  click, on the **same region id**, and the previous click's gesture ended with
  `dragged == false`. A click on a different id resets the window; so does any drag.
  The drag exclusion is not optional — kb's board is a drag-and-drop miller board and
  a lift that ends on its origin must never register as a double-click.
- Binding: a double-click on a board card opens the card detail overlay. The single
  click keeps its current meaning (select).
- A double-click is never the only route to an action. kb's mouse-first-class model
  is locked by v1.0.1, and that cuts both ways: every pointer gesture has a keyboard
  equivalent, and `enter` on a selected card is this one's.

#### 10.3.6 Input coalescing

crush throttles motion and wheel messages to 16ms (~60Hz) at the program level via
`tea.WithFilter`, and accumulates wheel deltas into a coalesced message rather than
dropping them, resetting the accumulator on direction reversal
(`model/filter.go:13`, `:38-59`, `:41-46`). This is why scrolling feels smooth
without any smooth-scroll code.

Applied in `run.go:89` where the program is constructed, as a filter built from the
token. Three rules:

- **Motion coalescing keeps the last message, wheel coalescing sums.** Dropping a
  wheel notch loses distance the user asked for; dropping an intermediate motion
  coordinate loses nothing, because hover is resolved from the latest coordinate at
  draw time (§10.5.3).
- **The accumulator resets on direction reversal.** Without it a flick up followed
  immediately by a flick down inside one window nets to zero and the board does not
  move.
- **Per-notch distance is unchanged.** `pointer.AddWheel`'s action already takes an
  `int` delta (`pointer/pointer.go:185-186`) and `wheelDelta` already yields -1/+1
  (`:318-326`); coalescing changes how many notches arrive per message, never how far
  one notch travels.

One free adjacent win, no token needed: crush guards its motion dispatch with a
coordinate-change check so idle motion costs nothing (`model/ui.go:1093-1109`).
kb's `handleMotion` (`pointer/pointer.go:261-269`) does the same — see §10.5.3,
where the guard is load-bearing rather than merely cheap.

#### 10.3.7 Notice TTL

crush's status toast expires after 5s (`model/status.go:17`, `:119-123`).

kb's equivalent is the footer notice: `m.actionNotice` and `m.move.notice`, which
take over the footer's state segment (`board_view.go:357-368`) and are cleared only
by the next board user input (`model.go:286-288`). That is a defect, not a design:
`noticeOwnsFooter` returning true suppresses the entire hint ladder *and* its hit
regions (`board_view.go:341-343`), so a board left unattended after a move keeps a
stale `moved to DOING` where its affordances should be, indefinitely.

`NoticeTTL` is adopted as a **second** dismissal path. Input dismissal stays and
remains the faster one. Two rules keep it honest:

- **Sequence guard.** The model carries a `noticeSeq` incremented on every raise;
  the expiry message carries the sequence it was scheduled for and is dropped when it
  does not match. Without it, a notice raised at t+1s is killed by the previous
  notice's expiry at t+5s. Same mechanism, same reason as §10.3.2's generation
  counter.
- **Errors are not notices.** `loadErr`, `pollErr` and `preferenceErr`
  (`board_view.go:382-386`) are *state*: they clear when the condition clears and
  they must never expire on a timer. A board that cannot reach its store says so
  until it can.

#### 10.3.8 Migrated literals

The three presentation literals move into the struct at their current values. This
is §9.1's table for timing, and the same rule applies: the migration is mechanical,
no behavior changes.

| Site | What | Token |
|---|---|---|
| `model.go:26,935-937` | `pollInterval = time.Second` | `Timing.PollInterval` |
| `ship_actions.go:21,570` | `autoShipDelay = 350ms` | `Timing.AutoShipDelay` |
| `cardeditor/model.go:29,1186` | `similarDelay = 400ms` | `Timing.SimilarDelay` |

`settings.go:22`'s `settingsTestTimeout = 20 * time.Second` **does not move.** It is
the deadline on a forge connection test — an I/O bound, not a feel constant, and
`theme` is not where network policy lives. It is the seam test's one named
allowance.

#### 10.3.9 Go shape and injectability

```go
package theme

// Timing is the clock of spec section 10. Every duration the TUI schedules
// against is named here; nothing under internal/tui writes one inline.
// Rates and tick counts are int; one-shots are time.Duration.
type Timing struct {
    FPS         int
    PlainStride int

    BirthDelay      int
    BirthSteps      int
    ScrambleSteps   int
    EllipsisStride  int
    SuffixAfter     int
    BrandBirthSteps int

    DialogGraceQuiet   time.Duration
    DialogGraceMax     time.Duration
    DialogGraceReopen  time.Duration
    ScrollActiveLinger time.Duration
    DoubleClickWindow  time.Duration
    InputCoalesce      time.Duration
    NoticeTTL          time.Duration

    PollInterval  time.Duration
    AutoShipDelay time.Duration
    SimilarDelay  time.Duration
}

// DefaultTiming is the table of section 10.3.1.
var DefaultTiming = Timing{ /* ... */ }

// TimingCollapsed is the test configuration: the struct zero value. Every
// one-shot fires immediately and no frame clock runs. It is not "fast" timing,
// it is no timing, which is the only kind a golden can assert against.
var TimingCollapsed = Timing{}

// Interval is the tick period of the one clock, or 0 when motion is off.
func (t Timing) Interval() time.Duration { return framePeriod(t.FPS) }

// PlainFrame is the tick period of a plain-tier spinner.
func (t Timing) PlainFrame() time.Duration {
    return time.Duration(t.PlainStride) * t.Interval()
}

func framePeriod(fps int) time.Duration {
    if fps <= 0 {
        return 0
    }
    return time.Second / time.Duration(fps)
}

// Tick schedules msg after d. A non-positive d dispatches immediately instead
// of scheduling: tea.Tick(0, ...) still round-trips through the runtime timer,
// which is a wall-clock dependency wearing a zero. Every tea.Tick call site
// under internal/tui goes through here.
func Tick(d time.Duration, msg tea.Msg) tea.Cmd {
    if d <= 0 {
        return func() tea.Msg { return msg }
    }
    return tea.Tick(d, func(time.Time) tea.Msg { return msg })
}
```

`theme` may hold `Tick` without breaking §6.1's import rule: `bubbletea/v2` is
already in the package's dependency closure through the `textinput`, `spinner` and
`help` style types `Styles` embeds. `widget` is still not imported.

Injection follows §6.2's cached factory rather than mutating a built `*Styles`:
`New(isDark)` delegates to a `NewWith(isDark, t Timing)` that sets `Styles.Timing`
and the same `t` on `Dimmed`. `Styles` gains one field, `Timing Timing`, beside
`Metrics` and `Glyph`.

**Binding rules for every token above.**

1. **Read once, in `Update`, never in a paint path.** A token is read only where a
   `theme.Tick` command is constructed. No `render*` or `View` function may
   reference `Timing`, `time.Now`, or `time.Since`.
2. **The model reads `Timing` off the `*Styles` it already holds** (§9.2), not off a
   package const. Sub-package models get it through the same
   `New(..., *theme.Styles)` constructor argument that carries the styles.
3. **Unit tests never involve a token.** They call `Update(<expiry or tick msg>)`
   directly and assert the frame — the shape `cardeditor/view_test.go:185` already
   uses. Every intermediate frame is a unit test.
4. **teatest programs build with `theme.TimingCollapsed`.** One-shots dispatch
   immediately, the frame clock returns 0 and its surfaces render the settled frame
   with a nil command — so a collapsed spinner does not busy-loop, it simply does not
   animate.
5. **No sleeps.** No `time.Sleep`, `time.After` or `teatest.WaitFor` on a
   timing-dependent predicate anywhere under `internal/tui`.
6. **The seam is enforced, not documented.** `theme/seam_test.go` gains a second
   walk, built exactly like the existing `lipgloss.NewStyle` one, failing on any of
   `tea.Tick(`, `tea.Every(`, `time.Sleep(`, `time.After(`, `time.Millisecond` or
   `time.Second` found under `internal/tui/` outside `internal/tui/theme/`. Its
   allowlist starts with one entry — `settings.go` for `settingsTestTimeout` — and,
   like the style allowlist, may only shrink.
7. **One byte-stability test per timed surface**, asserting the settled frame is
   invariant under tick. For this subsection that means: settled spinner frame,
   settled scroll affordance, post-expiry footer (hint ladder restored, hit regions
   back).

---

### 10.4 Micro-typography and icon vocabulary

The craft in this layer is sub-cell. Two invariants govern all of it:

1. **Every mark is a token.** A glyph, a separator, a marker prefix — if it is
   display vocabulary rather than data, it is named in one file and nowhere else.
   Donor precedent: crush keeps 38 single-glyph constants in
   `styles/styles.go:20-57` and has no inline glyph literal anywhere in its UI tree.
2. **No mark changes the width of the thing it marks** (§10.4.4).

Every mark inherits the foreground / background pair its row already resolved
(§1.2, §1.9), so the contrast floors measured in §1.9 stand unchanged.

#### 10.4.1 One glyph file

**Rule.** `theme.Glyphs` in `internal/tui/theme/tokens.go` is the only place under
`internal/tui` where a display glyph or a separator string may be written as a
literal. Views and widgets name tokens. This is the §6.2 `NewStyle` seam applied to
runes: a view that needs a mark the vocabulary does not carry has found a missing
token, and the fix is a new token, not a literal.

This table is the whole vocabulary, including every glyph the rest of §10
introduces. The Status column tracks which tokens are in use today; the rest are added by the
slice that adopts the rule that needs them.

| Token | Glyph | Code point | Cells | Role | Status |
|---|---|---|---|---|---|
| `Rail` | `▌` | U+258C | 1 | Card rail resting (§2.4); unfocused band rail (§2.2); focus gutter bar (§10.4.3); progress meter fill (§10.1.3) | present |
| `RailFull` | `█` | U+2588 | 1 | Selected card rail (§2.4) | present |
| `CapL` | `▐` | U+2590 | 1 | Pill left end cap (§3.6) | present |
| `CapR` | `▌` | U+258C | 1 | Pill right end cap (§3.6) | present |
| `Dot` | `●` | U+25CF | 1 | Column status dot (§2.2) | present |
| `Check` | `☐` | U+2610 | 1 | Unchecked checklist row | present |
| `CheckOn` | `☑` | U+2611 | 1 | Checked checklist row | present |
| `CheckOff` | `☒` | U+2612 | 1 | Dropped checklist row; cancelled-blocker mark in a blocker chip (#224) | present |
| `Tick` | `✓` | U+2713 | 1 | Resolved-blocker mark inside a blocker chip (#224) | new |
| `Diamond` | `◇` | U+25C7 | 1 | Effort marker for a value outside the S/M/L scale (§3.4) | present |
| `EffortS` | `🟦` | U+1F7E6 | **2** | Effort marker, S (§3.4) | present |
| `EffortM` | `🟨` | U+1F7E8 | **2** | Effort marker, M (§3.4) | present |
| `EffortL` | `🟧` | U+1F7E7 | **2** | Effort marker, L (§3.4) | present |
| `Focus` | `▸` | U+25B8 | 1 | Focused band caret (§2.2) | present |
| `More` | `+` | U+002B | 1 | Overflow cue prefix (§3.7) | present |
| `Ellipsis` | `…` | U+2026 | 1 | Truncation tail (§3.3) | present |
| `Blocked` | `⛔` | U+26D4 | **2** | Compact blocked mark (§3.4) | present |
| `Track` | `░` | U+2591 | 1 | Progress meter track (§10.1.3) | new |
| `Empty` | `○` | U+25CB | 1 | Empty-state mark (§10.8.3) | new |
| `Alert` | `▲` | U+25B2 | 1 | Failure mark (§10.8.5) | new |
| `HalfTop` | `▀` | U+2580 | 1 | Brand letterform upper half (§10.6.1) | new |
| `HalfBottom` | `▄` | U+2584 | 1 | Brand letterform lower half (§10.6.1) | new |
| `Bullet` | `·` | U+00B7 | 1 | Meta separator: `4 cards · 1 blocked`, `overdue · 2d` | new |
| `Times` | `×` | U+00D7 | 1 | Shipped-counter prefix in the top bar | new |
| `EmDash` | `—` | U+2014 | 1 | Reason clause separator in the editor's stale banner | new |
| `Chevron` | `›` | U+203A | 1 | Similar-item marker in the editor | new |
| `HintSep` | `" \| "` | U+0020 U+007C U+0020 | 3 | Separator between two rungs of a ladder (§10.4.6) | new |
| `PathSep` | `" / "` | U+0020 U+002F U+0020 | 3 | Top-bar crumb separator | new |

**Font risk.** `Track` is in the same Block Elements range as `RailFull`, `Rail`
and `CapL` and degrades identically on a font without them, so it carries no risk
§3.6 has not already accepted. `Empty` and `Alert` are in Geometric Shapes, the
block §2.2 and §3.4 already spend on `Dot` and `Diamond` — same terms. They also do
real work under the ASCII profile: §6.4 keeps ASCII-pinned structure goldens, and
with the profile flattened the hue is the one thing that does not survive, so a
glyph is what tells an ASCII terminal that a row is empty or failed. `HalfTop` and
`HalfBottom` genuinely **widen** the §3.6 risk, and are accepted only for the launch
mark, where §10.6.1 argues the case.

**Emoji admission** (added by #223). A pictograph enters the vocabulary only
as a **single code point with `Emoji_Presentation=Yes`**: no variation-selector
form, no zero-width-joiner sequence, no modifier. Anything else is a grapheme
cluster whose rendered width is a property of the terminal's segmentation
rather than of its runes, which is exactly what the `Cells` column promises kb
has pinned. Such a glyph is East Asian Wide by construction, so its `Cells`
value is 2 and `ansi.StringWidth` agrees. `Blocked` already satisfied the rule
and is now held to it.

The cost is font coverage: a terminal font without the pictograph draws tofu.
That is accepted consciously on the same terms §3.6 accepts the block glyphs,
and on one condition — a square never carries a fact alone. The effort letter
is rendered beside it and is what stays readable when the pictograph fails, so
the failure mode is an ugly chip rather than a lost value.

**Markers.** ASCII prefixes that are vocabulary, not prose.

| Token | Text | Cells | Role |
|---|---|---|---|
| `MarkPrio` | `P` | 1 | Priority chip prefix, `P1` (§3.4 position 1) |
| `MarkSeq` | `#` | 1 | Card reference prefix, `#142` (§3.2) |
| `MarkTag` | `#` | 1 | Plain label pill prefix, `#tag` (§3.5) |
| `MarkDue` | `!` | 1 | Compact due prefix, `!2d` (§3.4 position 4) |
| `MarkFilterOff` | `+ ` | 2 | Unselected filter pill marker, inside the caps (§3.6 inactive variant; #207) |
| `MarkFilterOn` | `x ` | 2 | Selected filter pill marker, inside the caps (§3.6 inactive variant; #207) |

`MarkSeq` and `MarkTag` are a same-text alias, deliberate and not a collision — the
§1.7 convention applied to glyphs. They are separate tokens because they answer to
different sections and either may be re-spelled without the other.

**Width rule.** Every token is one cell wide except `HintSep` and `PathSep` (3
each), `Blocked` and the three effort squares (2 each), and the filter marks
`MarkFilterOff`/`MarkFilterOn` (2 each). The three squares are deliberately
equal-width, so the effort chip changes value without moving a column. A token wider than one cell is ineligible as a state alternative to any
one-cell mark (§10.4.4); the two filter marks are deliberately equal-width so
they are eligible as alternatives to *each other* — the toggle never moves a
cell.

**Adjacency rule** (added by #218/#220, widened by #223). An ambiguous- or
wide-width mark owns the column after it: the cell following it is a space, or
the row ends. East Asian Ambiguous glyphs measure one cell to
`ansi.StringWidth` and to every width calculation kb makes, but many terminal
fonts draw them wider than the cell the cursor was advanced past, so anything
written in the next column is drawn on top of the mark. A wide pictograph
measures honestly at two cells but is drawn by font machinery that respects
the cell grid no better, and an emoji-less font substitutes tofu of whatever
width it has. The rule binds `Dot`, `Diamond`, `Ellipsis`, `Empty`, `Alert`,
`Bullet`, `Times`, `EmDash`, `Tick` (#224), `Blocked` and
`EffortS`/`EffortM`/`EffortL`. It does not bind the Block Elements — `Rail`,
`RailFull`, `CapR`, `Track`, `HalfTop`, `HalfBottom` — whose adjacency to their
neighbour *is* the primitive: a rail that does not touch its card and a cap that
does not touch its text are a different widget, and their cell alignment is the
block-glyph risk §3.6 already accepts. It does not bind `Check`, `CheckOn`,
`CheckOff`, `Focus`, `CapL` or `Chevron`, which are Neutral width, though those
are written with a following space anyway. The rule is about the column a mark
occupies, not about its token: the space belongs to the render site, and every
`Cells` value in the table above is unchanged.

**Guard.** `theme/seam_test.go` gains a walk over `internal/tui`. It fails on any
non-test `.go` file outside `theme/` containing a **string literal** with a rune at
or above U+00A0, or with either of the token-owned separator strings `" | "` and
`" / "`. Comments are not literals and are not scanned; the guard is about what is
rendered, not what is explained. A second test asserts every `Glyphs` field's
`ansi.StringWidth` matches the cells column above, so a future re-spelling that
silently changes a mark's width fails the build rather than the layout. A third
test (#220, widened by #223) walks the rendered board at both densities and
asserts no mark bound by the adjacency rule is followed by a printable
non-space; it resolves the bound set by reflection over `theme.Glyphs`, taking
every single-rune token that is East Asian Ambiguous or East Asian Wide, less
the Block Elements carve-out. It is scoped to the glyph vocabulary rather than
to every ambiguous rune on the surface: ambiguity is a property of ordinary
text — every accented Latin letter carries it — so the broader walk would fail
on a legitimately spelled card title. What kb controls is the marks kb writes
itself. A fourth test (#223) is the emoji admission rule: it walks every
`Glyphs` field and fails on any token carrying U+FE0E, U+FE0F, U+200D or
U+1F3FB–U+1F3FF, and on any East Asian Wide token that is not exactly one rune
measuring two cells. It checks the consequence rather than the property name,
because `Emoji_Presentation` is not in the standard library and is not worth
vendoring a table for: East Asian Wide is what the property guarantees under
UAX #11 and is also the only part of it the layout arithmetic consumes. A
pictograph smuggled in as a VS16 sequence fails the first clause; one smuggled
in with its text presentation fails the second, because it is not Wide and
cannot claim the two cells the table would owe it.

The marker tokens are deliberately **out** of the walker's deny-list: `#`, `P` and
`!` occur legitimately in format strings, identifiers and parse code, and a
literal-text guard over them would be noise. They are enforced by the width test
and by review. The allowlist rule of §6.2 carries over verbatim: it may only
shrink, and it starts empty.

#### 10.4.2 Hotkey underline

**Rule.** Every button label underlines exactly one rune: the key that already
drives that button. This is a display convention and never a keymap change (§5.1).
The resolver is `widget.Hotkey`, generalized out of the card-detail implementation
that is the only current caller of `UnderlineIndex`:

| Step | Condition | Label | `UnderlineIndex` |
|---|---|---|---|
| 1 | The control's message is not a single printable rune with no modifier | `label` | `-1` |
| 2 | `label` spells the rune, case-insensitively | `label` | first matching rune offset |
| 3 | Otherwise | `label + " (" + key + ")"` | `len([]rune(label)) + 2` |

Step 3 costs **4 cells** for a one-cell key: space, `(`, key, `)`. That figure is
normative for the width arithmetic of any action row (§5.4) and for the
narrow-panel stacking of a `ButtonGroup` (§5.2).

**Padding offset.** `UnderlineIndex` is a rune offset **into the label**, never into
the rendered button. A primitive that styles the padded string —
`lipgloss.StyleRanges`, as crush uses at `common/button.go:49-51` — must offset the
index by the button's left padding; kb's `widget.Button` renders the two padding
runs separately and slices the label, so its offset is zero by construction. Both
forms conform. The invariant is that the underline lands on the label rune and
never on a padding cell.

**Attribute only.** The cue is SGR 4 and nothing else. Never a color change, never
a bold run on top of a non-bold label: the underline must not disturb the pair §1.9
measured for the whole label, and a hued hotkey rune inside a label would put a
second, unmeasured pair inside every button in the TUI.

**State-invariant.** The underline is present in every state §1.9 defines. A hotkey
is a fact about the keymap, not about focus. It survives the pressed token because
SGR 7 reverses the color pair and leaves the underline attribute set.

**Coverage.** Every button surface of §5.4 whose action carries a single-rune hotkey
passes it. Six call sites currently pass `-1` unconditionally
(`issueimport/view.go:367`, `ship_actions.go:1069`, `:1114`, `settings_view.go:233`,
`cardeditor/view.go:262`, `adrsplit/view.go:190`); they resolve through
`widget.Hotkey` instead. The `huh` `Confirm` pair is the one exemption — huh renders
its own buttons from one style pair (§5.2) and exposes no range hook — and it stays
unmarked rather than acquiring a second, divergent cue.

The two button geometry literals in the card-detail pane become tokens with the
values §5.1 already fixes: `ButtonPadX` = `1`, `ButtonGap` = `1`.

#### 10.4.3 Focus gutter bars

The donor's signature move: a one-sided half-block border that says which row has
the keyboard (`quickstyle.go:855-897`), drawn manually on every wrapped
continuation line so the bar is unbroken (`dialog/question_choice_base.go:286-287`,
`:323-346`).

**Rule.** Every focusable row that is not a card carries a one-column gutter at its
left edge, **always reserved**, plus one column of gap before content.

| Row state | Gutter cell | Foreground |
|---|---|---|
| Blurred | space | — (the row's surface) |
| Focused | `Rail` `▌` | the row's accent slot, below |

Hover is not a gutter state: it changes the row's fill (§10.5.1). That is the same
separation §1.9 draws between hover as pointer feedback and focus as keyboard
position, and it is the donor's own choice at `question_choice_base.go:321-368`.

| Surface | Focused gutter hue |
|---|---|
| Overlay body row — choice, field, comment, checklist, similar item | `Brand` |
| Settings pane row, including a forge integration group's rows | `Brand` |
| A non-card row inside a board column | that column's hue (§1.3) |
| Card | not applicable — the card rail of §2.4 **is** the gutter, priority-hued and present in every state |

**Unbroken.** The gutter is emitted per **rendered line**, not per logical row. A
row wrapped to `k` lines emits `k` gutter cells in the same hue. A blank spacer line
between two rows carries no gutter: the bar spans exactly the lines its row
occupies.

**Not a lipgloss border.** The gutter is a literal cell rendered by the widget, not
`Border{Left: …}` on a style. The widget owns the wrap, so it owns the continuation
lines; a block-level border would also paint a cell the row's own background does
not reach under, which is the same hole `SurfaceRun` exists to close (§5.3).

| Token | Value | Applies to |
|---|---|---|
| `FocusGutterW` | `1` | Gutter column, reserved on every focusable non-card row |
| `FocusGutterGap` | `1` | Column between the gutter and the row's content |

**Interaction with §4.** A body row that can take focus resolves its content width
as `min(pw - 2*OverlayInsetX, ContentMax) - FocusGutterW - FocusGutterGap` — two
columns narrower than a static body row on the same panel. The two-column total is
the donor's exactly (`PaddingLeft(2)` blurred against `PaddingLeft(1)` plus a
one-cell border focused).

#### 10.4.4 No-reflow parity

**Rule.** For every element with state variants, the rendered cell width is a
function of its content and its width argument **only** — never of its state.
`width(blurred) == width(hovered) == width(focused) == width(armed) ==
width(pressed)`, cell for cell, at every density. The rule is stated once, here,
and is binding on every subsection of §10.

The corollary is what makes it checkable: **a state change may alter colors and
attributes freely, and may substitute a glyph only for another of identical cell
width.** Bold, underline and reverse cost zero cells. Padding compensates for
anything that appears.

| Element | Blurred left edge | Focused left edge | Compensation |
|---|---|---|---|
| Card (§2.4) | `Rail` `▌` + `CardPadLeft` | `RailFull` `█` + `CardPadLeft` | `CardRail` = 1 is reserved in both; the glyphs are both one cell |
| Focusable overlay row (§10.4.3) | 2 cells of surface | `Rail` `▌` + 1 gap cell | `FocusGutterW + FocusGutterGap` = 2 in both |
| Button | per §1.9 | per §1.9 | §1.9's state matrix varies color and weight only; no state in it changes a cell count |
| Checklist row | `Check` `☐` | `CheckOn` `☑` / `CheckOff` `☒` | All three are one cell |
| Column header band (§2.2) | `Rail` + `Dot` + `" i "` = 5 | `Focus` + `" i "` = 4 | **Violated today** — see below |
| Branded engine row (§10.2.5) | label + `EllipsisField` + `SuffixField` | same | Both fields are reserved whether or not their content is present |
| Scroll affordance (§10.3.4) | column reserved | column reserved | Tint changes; geometry does not |
| Filter pill (§3.6, #207) | `CapL` `▐` + mark + text + `CapR` `▌` | `RailFull` `█` caps, same interior | Caps swap for same-width glyphs; toggle marks are equal-width (`MarkFilterOff`/`MarkFilterOn`, 2 each); the retired `>label<` form violated this rule |

**The band exception.** The band's *total* width is identical in both states — both
fill the panel width — but its interior is not: the label starts at column 5
unfocused and column 4 focused, so moving focus across the board jitters every label
one cell. The fix that costs nothing is to keep the status dot on the focused band,
`head = Focus + Dot`, restoring the 5-cell reserve (`BandHeadW` = `5`, the widget's
current `bandLabelReserve` promoted to a metric) and holding the label column fixed:

```
▌● 1 TO DO                                4     unfocused
▸● 2 DOING                                3     focused
```

This amends §2.2's rendering, so it is recorded rather than applied.

**Guard.** One test per stateful widget renders every state permutation of a fixed
input and asserts equal `ansi.StringWidth`: `Button`, `Rail`, `Band`, `Card`, the
focusable overlay row, and the checklist row. A second test asserts the same for
interior column offsets on `Band` and the overlay row, which is the assertion the
band currently fails. These are pure string tests: no terminal, no tick, no golden.

#### 10.4.5 Section and dialog titles

kb already renders the donor's title shape in three places — the column header band
(§2.2), the overlay header band (§4 step 4) and the overlay section break (§4 step
5) — each as *title, fill, right-aligned info*, which is crush's `Section` /
`DialogTitle` (`common/elements.go:206-243`). What this subsection fixes is the fill
and the order of sacrifice.

**Casing.** Section-break labels are uppercase (`DETAIL`, `COMMENTS`, `CHECKLIST`).
Panel titles and column labels render their source string verbatim after
sanitization — never case-folded by the theme or the widget. Casing is data; the one
exception is the section-break label, which is not data but a constant in the
caller.

**Fill.** The run between the title and the right-aligned info is the band's own
background painted with spaces. It is **never** a rule of dashes, box-drawing runes
or a diagonal texture. The donor fills its dialog titles with a diagonal field; kb
does not, because the accent vocabulary already carries one font-dependency risk it
cannot degrade out of (§3.6) and a texture would add a second for decoration alone.

**Order of sacrifice.** `w` is the band width, `t` the title, `i` the info (`#seq`,
a count, a version). Minimum fill is one cell. The info is never truncated — the
same rule §3.2 gives `#seq`.

| Condition | Result |
|---|---|
| `w >= width(t) + 1 + width(i)` | title, fill spaces, info |
| `width(i) + 2 <= w < width(t) + 1 + width(i)` | title truncated to `w - 1 - width(i)` per §3.3, fill 1, info |
| `w < width(i) + 2` | info dropped entirely; title truncated to `w` |

A title is truncated, never wrapped — a band is one row by definition (§2.2, §4).

**Interaction with §10.1.** The fill run is the only part of a band that §10.1 is
permitted to gradient-paint. The title run and the info run stay flat, on the band's
own slot, so their contrast pairs remain the ones §1.2 and §1.9 measured.

#### 10.4.6 Help-line packing

**Rule.** A hint ladder is an ordered list of rungs. It is packed, never
right-trimmed. Today's board footer builds one string and calls `fitLine` on it,
which is `ansi.Truncate` — so a narrow frame cuts a rung mid-word and leaves a
dangling separator. It also carries a second, width-conditional copy of the ladder
for wide frames. Both go away: **rungs are declared once, ordered once, and the
packer resolves the frame.** Donor: `dialog/common.go:135-174`.

**Ladder shape.** A ladder is a pinned **head**, a droppable **middle**, and a
pinned **tail**.

**Separator ownership.** The separator belongs to the rung that follows it. Dropping
a rung drops its leading separator with it, so a packed line can never begin or end
with `HintSep`.

**Algorithm.** All widths are `ansi.StringWidth`; `total(list)` is
`sum(widths) + 3*(len(list)-1)`, the 3 being `HintSep`.

1. If `total(head ++ tail) > w`: drop tail rungs from the end while the total still
   exceeds `w` and the tail is non-empty; then truncate the head per §3.3. Emit.
   (This is the only path that truncates a rung.)
2. Otherwise let `k = len(middle)`. While `k > 0` and
   `total(head ++ middle[:k] ++ ellipsisRung(k) ++ tail) > w`, decrement `k`.
3. If `k == 0`, the mark is suppressed entirely. A mark with no admitted middle
   rung reports only that everything useful is gone, at the price of the cells
   that could carry the most important middle rung — `[Close] | ...` where
   `[Close] | ? or esc close help` fits is the mark working against its purpose
   (found in #187's dogfood, amended here). Retry once without it: if
   `total(head ++ middle[:1] ++ tail) <= w`, emit that; otherwise emit
   `head ++ tail`, truncating the head per step 1's rule if even that exceeds `w`.
4. Emit `strings.Join(rungs, HintSep)`.

`ellipsisRung(k)` is the single token `Ellipsis` when `0 < k < len(middle)` and
nothing when `k == len(middle)` or `k == 0` (step 3). Step 2 is where "ellipsis
only if it fits" falls out for free: the mark costs 1 cell plus its 3-cell
separator, and if that does not fit, the loop drops one more rung and frees the
room.

**Contrast with §3.4.** A dropped rung is **terminal** — rungs behind it are not
attempted. This is the opposite of the meta chip row, which skips an oversized chip
individually and still attempts the shorter ones behind it. The reason for the
difference: a chip row is a set of independent facts about one card, while a ladder
is ordered by importance, and admitting a short rung after dropping a more important
one misreports what the ladder is for.

**Hit regions.** The packer returns each admitted rung's start column, the same
contract `widget.joinAt` already gives the chip row. Pointer regions are built from
those offsets, never by re-splitting the rendered footer on the separator — a
rendered separator carries its own SGR runs and is not a safe split key.

**Board footer ladder.** Normative order. The state segment is the head and carries
its own hue (§1.5). `? help` and `q quit` are the tail because a frame too narrow
for the ladder is exactly the frame where a user most needs the two rungs that get
them out.

| Rung | Text | Section | Condition |
|---|---|---|---|
| 0 | board state (`boardState`) | head, pinned | always |
| 1 | `n new` | middle | editor enabled |
| 2 | `e edit` | middle | editor enabled |
| 3 | `j/k cards` | middle | always |
| 4 | `h/l/tab columns` | middle | always |
| 5 | `1-4 jump` | middle | always |
| 6 | `i import` | middle | issue import enabled |
| 7 | `c cancelled:on` / `c cancelled:off` | middle | always — no longer wide-frame-only |
| 8 | `? help` | tail, pinned | always |
| 9 | `q quit` | tail, pinned | always |

A transient move or action notice still owns the whole footer: it is a single pinned
head rung with no middle and no tail, unchanged, until it is dismissed by input or
by `NoticeTTL` (§10.3.7).

**Overlay footer ladders** take the same shape. The help pane's `[Close]` control is
its pinned head and its dismissal keys are the middle — which is what
`help.go:170-183` already does by hand, one ladder ahead of the rule.

**Guard.** One property test packs every ladder in the TUI across widths 1 to 200
and asserts: the line never starts or ends with `HintSep`; the rendered width never
exceeds the frame; the ellipsis rung is present if and only if a middle rung was
dropped and at least one middle rung is admitted; every pinned rung is present
whenever the pinned set fits; and the reported
start columns match the rendered offsets.

**Determinism.** Nothing in §10.4 is tick-driven. Every rule here is a property of
the settled frame, so the animation contract is untouched. The goldens that change —
the six button surfaces that gain hotkey cues, the footers that gain packing, the
bands if call 11 of §10.9 is accepted — change once, in the slice that adopts the
rule, per §6.4.

---

### 10.5 Hover states and mouse mode

§1.9 already defines hover for one control: a button wears its variant's tint as a
fill, and the pair is contrast-audited at both profiles. That definition is not
repeated here. What is missing is everything else the pointer can land on — a card,
a row inside an overlay, a column header band, a label pill — and the question §1.9
never had to answer, because a button has no cursor: when the pointer and the
keyboard disagree about what is selected, which one wins.

crush answers it with about forty lines (`question_choice_base.go:51-53`, `:79`,
`:94`, `:110-156`, `:188-189`), and those forty lines are the difference between
mouse support that feels bolted on and mouse support that feels designed. kb already
has the harder half — a real hit-region map with stable control IDs, press feedback
and drag (`internal/tui/pointer/pointer.go`) — and no hover at all: `handleMotion`
(`pointer.go:261-269`) returns `nil` for any motion that is not a left-button drag.
Bare motion is currently discarded.

#### 10.5.1 The hover step

Hover is a one-tier raise in the §1.1 depth order. It never re-hues, never bolds
text that was not already bold, and never adds or removes a cell (§10.4.4).

Where an element's *selected* state already spends the tier step — the card, and
only the card (§2.4: `Card` → `Raised`) — hover raises the element's rail cell
instead of its surface. Otherwise hover raises the whole element. That single
exception is what keeps hover and selection legible as two different things rather
than one thing seen twice.

| Element | Raised span | Resting fill | Hovered fill |
|---|---|---|---|
| Board card | rail column only, `CardRail` = 1 cell, all content rows | `Card`, or `Zebra` at compact density | `Raised` |
| Overlay choice row | full row, panel edge to panel edge | `OverlaySurf` | `OverlayBand` |
| Column header band, unfocused | none — glyph only, see below | `BandRest` | `BandRest` |
| Column header band, focused | none | column hue, solid | column hue, solid |
| Chip / label pill | none — attribute only, see below | wheel or status hue | unchanged |
| Inline reference (`kb://task/<seq>`) in overlay body prose (#212) | the reference run only | `OverlaySurf` | `OverlayBand` |
| Blocker chip (`#<seq>`) in the overlay's link rows and completion gate (#222) | the chip run only — explicitly not the "Chip / label pill" row: this chip is bracketed text on the panel surface, not a §3.6 pill, so the tier step is available | `OverlaySurf` | `OverlayBand` |

**Card.** The rail cell's background steps to `Raised`; the rail glyph stays `Rail`
and stays priority-hued, under the same rule §2.4 gives selection — the rail carries
P1 and nothing is allowed to take that away. The right half of the rail cell is the
raised ground, so a hovered card reads as a hue half-block against a lighter half,
and a selected card reads as `RailFull` with no ground showing at all. The two are
distinguishable at a glance and neither is the other's dimmer version.

A **selected** card renders no hover: its surface is already `Raised` edge to edge,
so the step has nowhere to go. This is correct rather than merely convenient — the
pointer over the already-selected card is offering nothing new.

**Overlay choice row.** `FgBase` on `OverlayBand` is the pair §1.9 already measured
as the Neutral hovered button and cleared at both profiles. It is reused
deliberately, so a hovered row and a hovered Neutral button inside the same panel
are the same surface and the panel reads as one system. Unlike the button, the row
does **not** bold — bolding a full-width run is a shout where bolding a six-cell
label is a nudge. A row's own selected state stays what it was (focus gutter glyph
per §10.4.3 plus `FgBase` against `FgSubtle` body text), which does not spend the
tier step, so the full-row raise collides with nothing.

Applies to: the checklist rows, the drift row list, the ADR-split per-story rows,
the issue-import row list, the comment and blocker-link pickers, and the settings
key/value rows fed through `widget.Table` (§5.2). Not to `Field` rows (§5.1) — they
are not activatable, so they are not hoverable.

**Column header band.** crush's tab hover adds bold
(`question_form.go:578-630`); kb's band is already bold (§2.2), so bold is not
available as a cue, and the band cannot change background without becoming the
focused band. The cue is the rail glyph, borrowing selection's own vocabulary in the
one slot the band has spare:

| Band state | Rail cell | Hovered rail cell |
|---|---|---|
| Unfocused | `Rail` `▌` in column hue on `BandRest` | `RailFull` `█` in column hue on `BandRest` |
| Focused | `Focus` `▸` in `FgOnAccent` on column hue | unchanged |

**Inline reference** (added by #212). The run already wears the §5.2 link color
*and* glamour's underline, so the pill's underline cue and any re-hue are both
spent; the one-tier ground raise scoped to the run is the only cue left, and it
is the §10.5.1 rule applied at its smallest span. Costs one cached seam,
`Styles.HoverRun(surface, raised, content)`, beside `PressedRun`/`BandRun`/
`SurfaceRun`. Zero cells, like everything else in this table.

The focused band is already the acting column; there is nothing for hover to
promise. Cost: zero cells, both states are one glyph. Any future tab strip is built
the same way — hovered-and-unselected thickens its rail, hovered-and-selected does
nothing — and gets no new tokens for it.

**Chip / label pill.** A pill (§3.6) is a saturated fill with `FgOnAccent` text and
two half-block end caps; there is no tier left to raise and the caps cannot grow
without costing columns. The cue is **underline on the body run** — zero cells,
survives 256-color quantization, survives the ASCII structure profile, and does not
touch the pair's contrast because it changes no color. Bold is unavailable: §2.6
step 7 already spends bold on the compact flat chip, so bold would mean "compact" at
one density and "hovered" at the other. A hue swap would break the label wheel's
identity (§1.6). The hotkey underline of §10.4.2 lives on buttons and never on
pills, so the two underlines never appear in the same widget.

This costs two cached styles, `ChipStyles.BodyHover` and `ChipStyles.FlatHover`,
built in `New` beside the rest (§6.2). No view toggles the attribute itself.

Hover introduces no timing constant: it has no animation, no grace period and no
auto-hide, so nothing here reaches §10.3.

#### 10.5.2 Mouse mode

Mouse mode is not a stored flag. **Mouse mode is on for a surface exactly when hover
is set and resolves to one of that surface's own regions.** Turning it off is
clearing hover, and there is one bit of state to clear. A second boolean would be
the same fact written twice, and the two copies would eventually disagree.

While mouse mode is on for a surface, the hovered element is the **acting
selection**: it renders the surface's cursor cue, and the keyboard cursor's own
position renders nothing. Exactly one cursor is visible at any moment. That is the
whole point of the machine — two simultaneous cursors is the failure it exists to
prevent.

| # | Event | Precondition | Effect |
|---|---|---|---|
| 1 | `MouseMotionMsg`, coords equal the last motion | any | dropped before the region scan; no state write, no re-render |
| 2 | `MouseMotionMsg`, coords changed, no button held | point hits a hoverable region | hover set to that region's `ControlID` and point; mouse mode on for its surface |
| 3 | `MouseMotionMsg`, coords changed, no button held | point hits no hoverable region | hover cleared; mouse mode off; the keyboard cursor renders again where it stood |
| 4 | `MouseMotionMsg` with `MouseLeft` held | a press is active | drag path, unchanged from `pointer.go:261-269`; hover is neither read nor written |
| 5 | `MouseClickMsg` / `MouseReleaseMsg` | any | press/release path unchanged; hover is retained across the gesture |
| 6 | `MouseWheelMsg` | any | scroll applies first, then hover is **re-resolved** from the retained point against the new frame's map |
| 7 | Arrow key or `hjkl` | mouse mode on for this surface | cursor **adopts** the hovered index, *then* the key's own motion applies from there; hover cleared; mouse mode off |
| 8 | Any other key | mouse mode on for this surface | hover cleared; mouse mode off; the key runs against the keyboard cursor, unadopted |
| 9 | Re-render with a changed region set (resize, refresh, filter) | hover set | hover re-resolved from the retained point; unresolvable → cleared |

Row 7's ordering is normative and is the row most easily got wrong: **adopt, then
move.** `↓` while row 7 is hovered lands on row 8, not on `cursor+1`. Row 8 is the
opposite and equally deliberate — a hotkey, `Enter` or `Esc` acts on the keyboard
cursor, never on whatever the pointer happens to be resting over, because a key the
user typed without looking at the mouse must not be redirected by it.

Row 6 exists because the pointer can stand still while the content moves under it.
Without the re-resolve, wheeling a list leaves the hover lit on a row the pointer is
no longer over, and no further event ever corrects it.

**Which surfaces run the machine.** The overlay choice surfaces of §10.5.1, and
those only.

| Surface | Machine | Hover renders |
|---|---|---|
| Overlay choice rows (checklist, drift, ADR stories, issue import, pickers, settings rows) | full machine, rows 1-9 | acting selection |
| Buttons and `ButtonGroup` rows | rows 1-6 only; a button has no cursor to adopt | §1.9 hovered state |
| Board cards | rows 1-6 only | affordance cue only — the board cursor does not follow the pointer |
| Column header bands | rows 1-6 only | affordance cue only — focus does not follow the pointer |
| Label pills, chips | rows 1-6 only | affordance cue only |
| Inline references (`kb://task/<seq>`) in overlay body prose | rows 1-6 only — a prose run has no cursor to adopt | affordance cue only; activation opens the referenced card (#212) |

The board is excluded on purpose. Its cursor is not just a highlight: it is the drag
source, the anchor every board keybinding resolves against, and the card the detail
overlay opens. A pointer sweep across four columns would rewrite it a dozen times on
the way to the scrollbar. It is also the web-parity answer — the web UI the TUI is
at parity with moves selection on click, never on hover.

#### 10.5.3 Dispatch

**Hoverable is opt-in, on the region that already exists.** Hover resolves against
the same region list `handlerSnapshot.hit` scans for clicks (`pointer.go:303-311`),
topmost-wins, same precedence. A region opts into hover by carrying a non-empty
`ControlID`: `Map.AddControl` is clickable **and** hoverable, `Map.Add` is clickable
only. There is no second region list and no second scan.

**The coord guard comes before the scan.** `handlerSnapshot` grows a
`lastMotion Point`; a `MouseMotionMsg` whose `(X, Y)` equals it returns `nil`
immediately, before any region is touched. The guard is on raw cell coordinates and
not on the resolved ID, so idle motion inside one large region costs one integer
comparison rather than a linear scan — crush's guard, same placement
(`model/ui.go:1093-1109`). This is the same guard §10.3.6 names as a free adjacent
win; here it is what makes the region scan affordable at all.

**Hover state mirrors press state.** `pointer.State` grows `hovered ControlID` and
`hoverAt Point` beside `pressed`, with `Hover(id, point)`, `ClearHover()`,
`IsHovered(id) bool` and `HoverPoint() (Point, bool)`. The motion path emits a
`hoverMsg` in the same unexported `interactionMsg` family the press path uses, so
`pointer.IsMessage` (`pointer.go:118-121`) already routes it and the overlay
message-routing rules need no change at all.

**Overlays receive mouse before the board**, in the §4 z-order, and the first
surface whose map resolves the point consumes the event. An overlay whose panel does
not contain the point still consumes the motion — clearing its own hover — rather
than passing it down: the region outside the panel is the overlay's own backdrop
(`Map.AddBackdrop`, `pointer.go:170-183`), not a hole. The board therefore never
sees motion while an overlay is up, and cannot light a card under a dimmed backdrop.

**Hit regions: per-site judgment, one rule over both.**

| Mechanism | Sites | Why |
|---|---|---|
| Recorded bounds | cards and label pills (`widget.CardSpan` already emits them), bands, panel rows, every scrolled row via `pointer.Viewport.Row` | the render already computes the span; recording it is free, and it is kb's established mechanism (crush does the same for chips, `attachments/attachments.go:130-135`) |
| `lipgloss.Compositor` hit-test | the overlay action row's `ButtonGroup` | the row is a join of variable-width rendered runs whose individual widths the caller does not otherwise need; one compositor of space layers at the group's coordinates beats re-measuring each button (crush `common/button.go:82-117`) |
| Rendered-text scan | inline references in the overlay body (#212) | the renderer, not the caller, decides where the run lands — glamour wraps and repositions the reference — so the span is recovered from the rendered row by an ANSI-aware walk (skipping OSC 8 hyperlink parameters, which repeat the reference at zero width) and projected through `pointer.Viewport` so it tracks scroll and clips at the pane |

Whichever a site picks, one rule binds both: **the region set must be
byte-for-byte identical between the hovered and unhovered render of the same
content.** That is §10.4.4 applied to the one state driven by cell coordinates,
which would otherwise feed its own reflow back into the pointer. It is also what
makes the whole scheme sound: hover is resolved at draw time against the map
recorded by the *previous* frame, and that is only correct because hover cannot have
moved anything.

Cell cost of every treatment in §10.5.1: card 0, row 0, band 0, chip 0.

#### 10.5.4 Test contract

Existing goldens need no edits. The zero value of `pointer.State` has an empty
`hovered`, so every golden in the two tiers renders the unhovered frame it already
contains — the same seeding discipline the animation contract requires, arriving for
free because hover has no settling behavior to seed.

Three additions, in the shape §1.7 and §1.9 established for the palette audits — the
rule ships as a test, not as a paragraph:

1. **No-reflow assertion, one per hover treatment.** Render the element hovered and
   unhovered from identical opts; assert `ansi.StringWidth` is equal on every row,
   and that the two renders are identical after `theme.Downsample` to the ASCII
   structure profile. Attributes and colors are exactly what that profile strips, so
   an equal-after-strip pair is a proof that hover moved nothing.
2. **Region-set stability.** Assert the recorded spans (`CardSpan`, viewport row
   rects) are identical between the hovered and unhovered render of the same content.
3. **Machine table coverage.** One unit test per numbered row of §10.5.2, driving
   `pointer.State` and a surface model with synthesized `tea.MouseMotionMsg` /
   `tea.KeyPressMsg` values. No wall clock and no `teatest`: every row of that table
   is a pure state transition, and none of them is timed.

---

### 10.6 Brand mark and launch treatment

kb has a two-glyph name and a board that owns the frame. Both facts constrain this
section: the mark is cheap to draw, and it is almost never allowed on screen.

crush is the technique donor throughout: half-block letterforms
(`internal/ui/logo/letterforms.go`, 419 lines for 15 glyphs), memoized stretch
randomness (`internal/ui/logo/rand.go`, 24 lines), per-line gradient
(`internal/ui/logo/logo.go:90`) and the meta row (`logo.go:94-105`). None of it
needs a cell buffer, so all of it transfers to a `View() string` app unchanged.

#### 10.6.1 Letterforms

Two letterforms, hand-drawn at 5 rows on the half-block grid: each terminal cell
carries `HalfTop`, `HalfBottom`, `RailFull` or nothing, giving 10 half-rows of
vertical resolution. Lowercase, matching the binary name. Ascender occupies rows
0-1; the x-height is rows 2-4.

`k`, 4 columns:

```
█
█
█ ▄▀
██
█ ▀▄
```

`b`, 5 columns:

```
█
█
█▀▀▀▄
█   █
█▄▄▄▀
```

`BrandKern` is 1 blank column. The assembled mark is 5 rows by 10 columns:

```
█    █
█    █
█ ▄▀ █▀▀▀▄
██   █   █
█ ▀▄ █▄▄▄▀
```

Every row is padded to the full mark width before it is painted (§10.6.3 depends on
this). The letterforms are string literals in `internal/tui/widget/brand.go`, joined
with `lipgloss.JoinHorizontal`, which is crush's construction minus its heredoc
dependency — two letters do not need `MakeNowJust/heredoc`.

**Glyph vocabulary cost.** `HalfTop` and `HalfBottom` (§10.4.1) are new to the
design language; §3.6's accent vocabulary is `█`/`▌`/`▐` only. This widens the
block-glyph risk §3.6 records and carries consciously: on a font without block
glyphs the mark degrades to garbage, worse than a border would. It is acceptable
here and only here, because the mark is purely decorative — every launch-screen fact
a user needs (the load status, the build version) lives in the meta row as plain
text (§10.6.5).

#### 10.6.2 Stretch, and why the randomness is memoized

crush stretches one randomly chosen letter by repeating its middle segment
(`logo.go:405-419`, `:396-402`). kb has one stretchable letter: `b`'s bowl is built
from repeatable vertical slices, while every non-stem column of `k` is a diagonal
step, and repeating a step breaks the slope rather than lengthening the arm. So the
random dimension is the *amount*, not the letter.

The stretch repeats `b`'s column index 2 — the slice `(row2 ▀, row4 ▄)`, blank
elsewhere — `BrandStretch` additional times, where `BrandStretch` is drawn once from
`[0, BrandStretchMax]`. Mark width is `BrandMarkW + BrandStretch`, so 10 to 12
columns. At `BrandStretch = 2`:

```
█    █
█    █
█ ▄▀ █▀▀▀▀▀▄
██   █     █
█ ▀▄ █▄▄▄▄▄▀
```

**The randomness is drawn exactly once per process.** This is the whole point of
crush's `cachedRandN` (`rand.go`, 24 lines): a mark that re-rolls its width on every
render jitters on window resize and reads as a rendering fault, not as character. kb
takes the same memo but resolves it one level up, at the model rather than in
package state, because §6.2 forbids package-level mutable state in the render path
and the repo already has the injection shape for exactly this — `m.now` is pinned by
`board_view_test.go:360-361`:

- `Model.brandStretch int` and `Model.brandSeed int64` are set once in `NewModel`
  from `widget.RollBrand()`.
- Both are plain fields, so a test pins them by assignment. No `sync.Once`, no
  package-level cache, no seam exemption.
- `widget.Brand` is pure: it takes the resolved values as `BrandOpts` fields and
  draws nothing on its own.

#### 10.6.3 Per-line gradient

The mark is painted with `Styles.Grad[GradWork]` (§10.1.2), one ramp per line,
running left to right across the row's full padded width.

**Pad before ramping.** crush ramps each line over its own grapheme clusters, which
is correct for its logo because every line of it is full-width. kb's rows are sparse
— row 0 paints 2 cells, row 2 paints 8 — so ramping over painted clusters would give
row 0 the entire ramp in two cells and tilt the mark's color against its own
geometry. Every row is therefore padded to the mark width first, and the ramp runs
over the padded row, so column `c` carries the same ramp position in all five rows.

**Contrast.** The mark is decorative large type and the §1.9 body-text floor does not
bind it, but it clears that floor anyway on `Canvas` — the `GradWork`-on-`Canvas` row
of §10.1.2's legibility table, 6.02 at the lead and 9.93 at the tail, so no ramp
position is below AA.

**Degradation.** Below `FidelityFull` the ramp resolves to its lead slot flat
(§10.7.5 rule 2) and the whole mark is one blue, which is the intended flat
degradation. The reveal of §10.6.6 is suppressed entirely at the same threshold.

#### 10.6.4 The top bar

The top bar carries no mark of its own. Its two leading columns are the per-project
accent rail and the accent-hued `kb` wordmark of §10.7.3 — flat, bold, three columns
in total, replacing the bare bold `kb` the row renders today
(`board_view.go:404`) and filling the "brand pill" the §5.1 `TopBar` row already
names. The rest of the row — `PathSep + title + PathSep + user`, the shipped counter
— is unchanged.

Neither a ramp nor a filled pill is used there. A two-cell text run cannot carry a
legible gradient, and a fixed brand ramp would overwrite the one hue on the row that
identifies *which board this is*. The launch screen is the place the mark gets to be
a mark; the top bar is the place identity gets to be a color.

#### 10.6.5 Meta row

One row, `BrandMetaGapRows` = 1 blank `Canvas` row below the mark. Brand context
left, version right-aligned by computed gap — crush's `logo.go:94-105` shape.

| Slot | Content | Style |
|---|---|---|
| Left | The board's resolved state string, from the existing status resolver (`board_view.go:380-394`): `loading board…`, `ready`, or an `error: …` line | The hue that resolver already returns (`FgMuted`, `StatusOK`, `StatusDanger`, `StatusWarn`) |
| Right | The build version with a single leading `v` — the prefix is added only when the source string lacks one, since `debug.BuildInfo` already reports `v1.2.0`-form strings; `devel` / `unknown` render unprefixed | `FgSubtle` |

Rules:

- Row width is `BrandMetaW` capped at `frameW - 2*PageMarginX`, centered on the same
  frame center as the mark.
- The gap between the two slots is at least `BrandMetaGap` = 2 columns.
- **The version is never truncated; the left slot is.** Same rule and same primitive
  as §3.2's `#seq` and §3.3's truncation. If the left slot's allotment falls below 4
  columns it is dropped entirely and the version renders alone, right-aligned.

**`FgSubtle`, not `FgMuted`, for the version.** `FgMuted` on `Canvas` measures
4.20:1 — below the AA floor §1.9 establishes for text a user reads before acting.
That is tolerable for a card's `#seq` sitting inside a dense board, and it is not
tolerable for the one build identifier a bug report will ever ask for. `FgSubtle` on
`Canvas` measures 7.76:1.

**Version plumbing.** The TUI cannot reach `versionParts` — it lives in package
`main` (`dispatch.go:137`). The version arrives as a string parameter on `tui.Run`,
passed by `runTUI` (`tui_dispatch.go`) from the same `debug.BuildInfo` the
`kb version` command reads, and is stored on the root `Model`. An empty string
renders the meta row with the left slot only.

#### 10.6.6 Launch reveal

The reveal reuses §10.2.5's staggered-birth mechanic on the one clock of §10.3.1.

**Mechanic.** Each of the mark's `markW` columns is assigned
`birth[c] = rand.IntN(Timing.BrandBirthSteps)` from a PCG source seeded with
`Model.brandSeed` (§10.6.2). On frame `f`:

- `f < birth[c]` — the column renders its final glyphs in `FgMuted` on `Canvas`.
- `f >= birth[c]` — the column renders its final glyphs in its §10.6.3 ramp color.

So the reveal is a staggered **color** wipe, not a shape wipe. crush scrambles runes
before birth because its letterforms are drawn from a large glyph set where a wrong
glyph still reads as *a* glyph; scrambling half-blocks produces visual noise with no
letter under it. The mechanic — per-column birth step, seeded, deterministic,
self-terminating — is the donated part, and it is unchanged.

**Termination.** Every birth value is strictly less than `Timing.BrandBirthSteps`,
so frame `f = Timing.BrandBirthSteps` is the settled frame by construction, and the
tick command returns `nil` there. That is the `cardeditor` `spinTick` shape the repo
already uses (`cardeditor/model.go:264-271`) and it satisfies point 3 of the
determinism contract.

**Suppression.** The reveal is a class-B effect (§10.7.6): its only signal is the
`FgMuted` → ramp step. Below `FidelityFull` the frame counter is seeded at
`Timing.BrandBirthSteps` and no tick is ever scheduled. This has a useful consequence
for §6.4: the ASCII-pinned structure goldens can never capture a mid-reveal frame,
because at that profile there is no mid-reveal frame to capture.

**No minimum hold.** The launch screen is dropped the instant the first board
snapshot lands, even if the reveal is 2 frames in. On a warm local store the mark may
flash for under 100 ms. That is accepted: a brand flash is not an error state and
costs the user nothing, while a hold would make a board-first tool measurably slower
to open, which is the one thing it must not be.

#### 10.6.7 Placement

The mark appears in exactly two places. Everything else gets nothing.

| Surface | Form | Cost |
|---|---|---|
| Launch screen — the frame from program start until the first board snapshot lands (`m.loading && !m.haveBoardSnapshot`, `board_view.go:390`) | Full mark + meta row, centered | 0 steady-state rows: the screen exists only while there is nothing else to draw |
| Top bar, every board frame | The accent rail and wordmark of §10.6.4 | 3 columns, replacing 2 |

**Not the help overlay.** The help pane is width-capped at 56 (§4) and its height is
its content; a 5-row mark would spend five of those rows on decoration in the one
overlay whose entire job is to fit a keymap. A one-row variant does not work there
either: §4 step 4 fills the overlay header band solid `Brand`.

**Not the empty board, not the settings pane, not the card detail.** A board with no
cards is a board the user is about to fill, not a splash opportunity; §5.3's "do not
force-fit" applies to brand chrome as much as to components.

**Launch screen geometry.** Block height is `BrandMarkH + BrandMetaGapRows + 1` = 7
rows, on `Canvas`, no chrome:

- Horizontal origin `x0 = floor((frameW - markW) / 2)`.
- Vertical origin `y0 = floor((frameH - 7) / 2)`; the odd remainder goes below
  (the floor puts the extra row under the mark; `TestLaunchScreenGolden` pins it).
- The meta row is centered on the same frame center, at width `BrandMetaW` capped to
  `frameW - 2*PageMarginX`.
- If `frameW < BrandMinW` or `frameH < BrandMinH`, the full mark is dropped and only
  the meta row renders, centered.

#### 10.6.8 Tokens

All of these live in `internal/tui/theme/tokens.go` beside the existing `Metrics`
and `Glyphs`, per the locked seam rule; none of them is a literal at a call site.
The reveal's span is a timing token and lives in §10.3.1; the two half-block glyphs
are vocabulary and live in §10.4.1.

| Token | Value | Applies to |
|---|---|---|
| `BrandMarkW` | `10` | Unstretched mark width, `k`(4) + `BrandKern`(1) + `b`(5) |
| `BrandMarkH` | `5` | Mark height, both letterforms |
| `BrandKern` | `1` | Blank columns between letterforms |
| `BrandStretchMax` | `2` | Inclusive upper bound of the memoized stretch; mark width is 10-12 |
| `BrandMetaW` | `48` | Meta row width before the frame cap |
| `BrandMetaGap` | `2` | Minimum columns between the meta row's two slots |
| `BrandMetaGapRows` | `1` | Blank `Canvas` rows between the mark and the meta row |
| `BrandMinW` | `16` | Frame width below which the full mark is dropped |
| `BrandMinH` | `9` | Frame height below which it is dropped |

Palette slots used, all pre-existing: `Brand`, `TintPrimary`, `FgSubtle`, `FgMuted`,
`Canvas`, plus the status slots the meta row's left slot inherits.

#### 10.6.9 Widget contract

One row joins the §5.1 inventory.

| Element | Source | API sketch |
|---|---|---|
| Brand mark | kb `widget` | `Brand(o BrandOpts) []string` — `BrandOpts{Styles *theme.Styles, Width, Height, Stretch, Frame int, Seed int64, Status string, StatusSlot theme.Slot, Version string, On theme.Slot}`; returns the whole launch block, mark and meta row, one string per row, each carrying `On` edge to edge |

`Brand` is pure and stateless: `Stretch`, `Frame` and `Seed` are all resolved by the
caller (§10.6.2), which is what makes both the stretch and the reveal pinnable
without a package-level cache.

#### 10.6.10 Determinism and goldens

1. **Tick invariance.** The launch screen is not the board, so
   `TestViewIsByteStableAcrossMovingWallClock` (`board_view_test.go:426`) is
   untouched — the board's `View()` gains no animated state from this section. The
   top bar's accent run is static.
2. **Seeded at target.** The launch screen's color golden pins
   `Frame = Timing.BrandBirthSteps` and renders the settled mark. There is no existing
   golden of the loading state to regenerate — `internal/tui/testdata` has none — so
   this section adds goldens rather than editing any.
3. **Self-terminating.** The reveal's tick returns `nil` at the settled frame, and
   the whole screen is torn down when the snapshot lands.
4. **Stepped synchronously.** Intermediate frames are unit tests over `widget.Brand`
   with an explicit `Frame` and `Seed`, never teatest goldens.
5. **No `WaitFor` on a transient.** No teatest predicate may match the launch screen;
   predicates gate on board content, which only exists after the launch screen is
   gone.

New goldens: a truecolor `TestLaunchScreenColorGolden` at the settled frame, and an
ASCII `TestLaunchScreenGolden` for geometry — centering, the meta row's gap
arithmetic, and left-slot truncation. Both pin `brandStretch` explicitly; a golden
that let the memo roll would be a width flake by construction. One unit test asserts
that the frame before the settled one differs from it, so a reveal that silently
stopped animating fails rather than passing quietly.

---

### 10.7 Per-project accent hue and the terminal floor

Two rules that bound everything else in §10: what a board is allowed to recolor
about itself, and what happens to every effect in this section on a terminal that
cannot render it.

#### 10.7.1 Why the accent exists

The top bar is the only row on screen whose job is identity, and today it spends no
color on identity at all. `renderTopBar` (`internal/tui/board_view.go:399-410`)
emits `kb` in `Brand`, then the board title, the user, and a `StatusOK` shipped
counter — so two kb windows side by side, on two different boards, differ only in a
string of `FgBase` text that the frame edge truncates first. The board title is
already the identity the user typed; it is not carrying any of the recognition
weight.

The accent gives it that weight: a deterministic, per-board hue on the row's leading
run. It is a recognition aid and nothing else. **No state, count, severity or
affordance may ever be encoded in the accent** — a build slice that reads the accent
to decide anything has misread this section.

#### 10.7.2 Derivation

The accent is **not** free HSL. A hue computed at runtime is not a palette slot, so
it cannot be audited by §1.7, and the §1.7 guard is the mechanism that keeps
256-color rendering honest. A derivation that emits arbitrary hexes reopens the audit
on every board title a user invents, which is the same as not having one.

The accent therefore derives from the **§1.6 label pill wheel**, using the hash the
wheel already uses:

```
AccentSlot(name):
    n = strings.ToLower(strings.TrimSpace(name))
    if n == "" || n == "board":  return theme.Brand
    return theme.LabelSlot(LabelWheel(n))
```

- `LabelWheel` is `widget.LabelWheel` (`internal/tui/widget/chip.go:68-78`),
  unchanged.
- `LabelSlot` is `theme.LabelSlot` (`internal/tui/theme/palette.go:98-100`),
  unchanged.
- The function is therefore **total over `{Brand, Label1..Label5}`** and can emit
  nothing else. No new hex, no new slot, no new x256 index. The §1.7 audit is closed
  by construction rather than by re-running it. This is the one derivation in §10
  that could have opened it.

**Input.** `board.Board.Title` — the `# Title` line of the board markdown
(`internal/board/markdown.go:160-162`), which is the string the top bar already
prints. Normalization is `TrimSpace` then `ToLower`, so `KB` and `kb` are one board.
Case-folding is the only transformation; no Unicode normalization, because the wheel
hash is defined over runes and any folding beyond case would make the accent
disagree with the label wheel on the same string.

**The unnamed board.** `Parse` defaults the title to the literal `Board`
(`internal/board/markdown.go:149`) and so does the zero model
(`internal/tui/model.go:160`). Hashing that default would hand every unnamed board
the same arbitrary hue and call it identity. It is not identity, so the default and
the empty string both resolve to `Brand`: a board that declared no name looks
exactly like kb looks today.

**Worked examples** — normative; a slice implementing `AccentSlot` asserts these
exact rows:

| Title | Normalized | runes + first | `% 5` | Slot | Hex | x256 |
|---|---|---|---|---|---|---|
| `Board` (default) | `board` | — | — | `Brand` | `#4f8ef7` | 69 |
| *(empty)* | `` | — | — | `Brand` | `#4f8ef7` | 69 |
| `webtui` | `webtui` | 6 + 119 = 125 | 0 | `Label1` | `#ff7b54` | 209 |
| `Strata` | `strata` | 6 + 115 = 121 | 1 | `Label2` | `#4f8ef7` | 69 |
| `roadmap` | `roadmap` | 7 + 114 = 121 | 1 | `Label2` | `#4f8ef7` | 69 |
| `kb-tui` | `kb-tui` | 6 + 107 = 113 | 3 | `Label4` | `#b98af7` | 141 |
| `kb` | `kb` | 2 + 107 = 109 | 4 | `Label5` | `#ffb020` | 214 |

`Strata` and `roadmap` deliberately collide in that table. A five-slot wheel collides
once in five, and `Label2` is `Brand`'s hex, so roughly one board in five also looks
identical to an unnamed one. That is the honest cost of keeping the audit closed, and
it is survivable precisely because the accent carries no meaning: two boards sharing
an accent lose a recognition cue, not information.

#### 10.7.3 Where the accent appears

Exhaustive. The accent is confined to the top bar row, which is the one row of chrome
that hosts no semantic hue of its own on its left half.

| Surface | Accent? | Reason |
|---|---|---|
| Top bar leading rail, `Rail` (U+258C), 1 column | **Yes** | The row's identity mark. Same glyph grammar as the card rail (priority identity) and the unfocused band rail (column identity): a rail names the thing to its right |
| Top bar `kb` wordmark run | **Yes** | Bold, accent hue on `Canvas`. Continuous with the rail, so `▌kb` reads as one 3-column mark |
| Top bar board title, user, separators | No | `FgBase` on `Canvas`, unchanged (§2.1) |
| Top bar shipped counter | No | `StatusOK`. It is a count, and counts are semantic |
| Focused column header band | No | The column hue (§2.1). TO DO / DOING / DONE / CANCELLED identity is semantic and outranks board identity |
| Unfocused band, band rail, status dot | No | Column hue (§2.2) |
| Card rail, selected or not | No | Priority hue, always (§2.4) |
| Chips, pills, labels, buttons | No | §1.4, §1.5, §1.6, §1.9 |
| Overlay header band | No | `Brand` (§4 step 4). An overlay body carries `StatusWarn` and `StatusDanger` chips; a header band in a board's accent could put a warn-orange band directly above a warn-orange chip that means something |
| Launch mark | No | The launch screen renders before the first board snapshot lands, so there is no title to hash. It is kb's identity, not the board's (§10.6.3) |
| Shade tiers, shadow, footer, filter bar | No | Depth is never hued (§1.1) |

**Row budget.** The rail costs 1 column of the top bar. The row is rendered at full
frame width with no page margin (`internal/tui/board_view.go:306`), and it already
clips through `fitLine`, so the cost is one column of clip headroom and no layout
change anywhere else.

**Separation from `StatusOK`.** The shipped counter is the only semantic color on
this row, and `Label3` (`#3f9d58` → 71) is a near neighbour of `StatusOK`
(`#3fbf7f` → 72). They are never adjacent: the rendered order is
`▌kb / <title> / <user>` and only then the counter, and the title is never empty
because `Parse` defaults it. Minimum separation is
`PathSep + title + PathSep + user + PathSep`, which is 7 columns for a one-character
title and an empty user, and 11 for the default `Board`. Recorded so a future top-bar
reflow knows this ordering is load-bearing.

**Motion.** The accent never animates, never blends and never gradients. It is a flat
foreground slot resolved once per theme build.

**Dimmed variant.** The accent is a palette slot, so `Styles.Dimmed` (§1.8) already
carries its dimmed form and the overlay backdrop needs no special case.

**Glyph risk.** The accent rail inherits the block-glyph risk carried consciously at
§3.6. Accepted on the same terms — the wordmark run keeps the hue either way, so
identity survives the glyph failing.

#### 10.7.4 Contrast

The accent is drawn as a foreground on `Canvas`, so its contrast pair is fixed and
there are exactly five of them. Every pair clears WCAG 2.x AA for normal text
(4.5:1) in truecolor and again after 256-color quantization, the same floor §1.9
holds button labels to.

| Slot | Hex | x256 | x256 hex | on `Canvas` (truecolor) | on `Canvas` x256 (233 = `#121212`) |
|---|---|---|---|---|---|
| `Brand` / `Label2` | `#4f8ef7` | 69 | `#5f87ff` | 6.02 | 5.71 |
| `Label1` | `#ff7b54` | 209 | `#ff875f` | 7.55 | 7.92 |
| `Label3` | `#3f9d58` | 71 | `#5faf5f` | 5.68 | 6.94 |
| `Label4` | `#b98af7` | 141 | `#af87ff` | 7.40 | 6.90 |
| `Label5` | `#ffb020` | 214 | `#ffaf00` | 10.56 | 10.16 |

Worst cell 5.68 (truecolor, `Label3`), 5.71 (x256, `Brand`). §1.3 describes `Brand`'s
role as the "`kb` wordmark pill" while the implementation renders it as colored text;
if a later slice promotes the wordmark to a filled pill, the table above is
unchanged, because `FgOnAccent` and `Canvas` are the same hex (§1.2) and inverting a
pair does not change its ratio.

#### 10.7.5 The terminal floor

Truecolor is the reference target. Everything below it is a **degradation, never a
design**. Three rules, binding on all of §10:

1. **No bespoke 256 design.** There is no second palette, no per-profile hex, and no
   view that branches on the color profile. The 256-color appearance of the TUI is
   entirely determined by the x256 column of §1 and the guard of §1.7. All color
   reduction is done by exactly two things: the bubbletea renderer, per cell, against
   the detected profile; and `theme.Downsample` (`internal/tui/theme/profile.go:38`)
   at the string level. `Downsample` is a test-only entry point — verified: every call
   site in the repo today is a `_test.go` file. Production calls it nowhere and must
   continue not to.

2. **Every gradient and blend degrades to its flat base hue.** A graded run is
   declared as `(base Slot, toward Slot)`. Below the truecolor floor it renders as
   `base`, flat, across the whole run. Not the midpoint, not the endpoint, not the
   per-cluster quantization of the ramp. Two hues eight steps apart in the truecolor
   ramp routinely quantize onto the same 256 index, so the honest 256 rendering of a
   nine-cell ramp is two or three visible bands with boundaries falling wherever the
   color cube happens to land — an artifact that looks like a bug, costs per-cell SGR
   churn, and communicates nothing. This is the rule §10.1.5's degradation paragraph
   defers to, and it is why every ramp in §10.1.2 is legible at its *lead* endpoint.

3. **An effect that cannot degrade honestly is suppressed, not approximated.** Where
   the *information* lives in the color difference across a run rather than in the
   color itself, flattening it destroys the information and approximating it lies
   about the value. Those effects do not run below the floor at all.

**Fidelity token.** The profile resolves once into a named token, and views never see
the profile.

| Token | Profiles | Means |
|---|---|---|
| `FidelityFull` | `colorprofile.TrueColor`, `colorprofile.Unknown` | Gradients, blends and color-carried effects run |
| `FidelityIndexed` | `colorprofile.ANSI256`, `colorprofile.ANSI` | Flat palette slots only |
| `FidelityFlat` | `colorprofile.ASCII`, `colorprofile.NoTTY` | The renderer emits no color; only glyph, weight and geometry survive |

`Unknown` maps to `FidelityFull` because it only ever occurs before detection
completes — bubbletea always resolves a profile before it sends `ColorProfileMsg`
(`tea.go:1080-1089`) — and the reference target is the correct assumption until told
otherwise. This mirrors §6.3's "default `isDark` to `true` until the message lands".

**Shape.** In `internal/tui/theme`:

```go
type Fidelity uint8
const ( FidelityFlat Fidelity = iota; FidelityIndexed; FidelityFull )

func FidelityFor(p colorprofile.Profile) Fidelity

// NewFor is the full constructor. New(isDark) == NewFor(isDark, colorprofile.TrueColor),
// so the huh ThemeFunc seam of section 6.3 keeps its exact signature.
func NewFor(isDark bool, profile colorprofile.Profile) *Styles

// On Styles:
//   Fidelity Fidelity
//   func (s *Styles) Graded() bool { return s.Fidelity == FidelityFull }
```

`Fidelity` deliberately does not reuse the word *tier*, which §1.1 already spends on
depth.

**Rebuild triggers.** §6.2 says `New` is called on program start and on
`tea.BackgroundColorMsg`, "nowhere else". That list gains exactly one entry:
`tea.ColorProfileMsg` (`charm.land/bubbletea/v2@v2.0.8/profile.go:13`), which
bubbletea sends at startup and again if a terminal upgrades to truecolor via an
`RGB`/`Tc` capability report (`tea.go:775-782`). The rebuild is the same
once-per-message rebuild as the background-color one, and it keeps §6.2's real rule —
no style construction in a render path — intact.

**The branch is invisible to views.** `Graded()` may be read for exactly one purpose:
deciding whether to *start* an effect (arm a tick chain, allocate a prerender cache).
It may never be read to pick a color. Color selection lives inside the theme's graded
helpers, which return the flat `base` form themselves when `!Graded()`. A view that
writes `if styles.Graded() { colorA } else { colorB }` has reintroduced the bespoke
256 design this section forbids.

#### 10.7.6 Degradation classes

Every effect adopted anywhere in §10 declares one of three classes. An effect that
cannot be argued into class A or class C **is** class B.

| Class | Definition | Below `FidelityFull` | Members in §10 |
|---|---|---|---|
| **A — degrades** | The effect's value at any instant is one `Slot` plus a blend fraction toward a second `Slot`. Removing the fraction leaves a correct, meaningful frame | Renders `base` flat. Any tick chain that only drives the fraction does not arm | Every ramp of §10.1.2; the state-dependent section-label recolor of §10.1.4 (crush `dialog/sessions.go:308-315`) |
| **B — suppressed** | The information lives in the color *difference* across the run, or in a transient the settled frame does not contain | The element renders its settled, ungraded state directly. The effect does not run, and its tick chain is never armed | The branded engine's staggered birth wipe (§10.2.5) and the launch reveal (§10.6.6), both crush `anim.go:325-330` — pre-birth cells render `.`, so a flattened wipe is a flash of punctuation |
| **C — profile-independent** | Carried by glyph, geometry, weight, shade tier or SGR attribute, not by hue | Runs unchanged at every fidelity, including `FidelityFlat` | Half-block rails and pill caps (§2.4, §3.6); the meter's fill/track glyph split (§10.1.3); the hover underline and rail thickening (§10.5.1); `Styles.Pressed` reverse video; the hotkey underline (§10.4.2); the shade-tier depth model |

The gradient-as-progress-bar of §10.1.3 is a deliberate hybrid: its ramp is class A
and its *position* is class C, which is exactly why `Glyph.Track` exists. Without a
distinct track glyph the meter would be class B and would have to be suppressed on
the terminals most likely to be watching a long import.

The suppression rule has a second consequence the animation contract already demands:
a class-B effect's tick chain must not merely render nothing, it must not be armed at
all. A chain that ticks invisibly still fails the self-termination requirement
(determinism contract, item 3) for no benefit.

#### 10.7.7 Interaction with golden profile pinning

§6.4 pins a profile per golden. §10.7.5 makes the *styles* depend on a profile too,
so the two must be pinned together or a golden asserts a combination that cannot
occur on a real terminal.

**Rule: a golden's `Styles` fidelity and its rendering profile are pinned from the
same constant.**

| Golden class | Program / render pin | Styles built as | What it asserts |
|---|---|---|---|
| Structure (ANSI stripped) | `theme.PinStructure()` / `theme.Downsample(s, theme.StructureProfile)` | `theme.New(true)` is permitted | Layout, truncation, drop order |
| Color | `theme.PinColor()` / `theme.Downsample(s, theme.ColorProfile)` | `theme.NewFor(true, theme.ColorProfile)` | The palette and every class-A and class-B effect, at full fidelity |
| Indexed | `theme.PinProfile(colorprofile.ANSI256)` / `theme.Downsample(s, colorprofile.ANSI256)` | `theme.NewFor(true, colorprofile.ANSI256)` | That the degradation is what §1.7 says it is: flat base hues, suppressed class-B effects |

Three consequences, each verified against how the tests actually work today:

1. **teatest goldens need no new plumbing.** `tea.WithColorProfile` sets `p.profile`,
   and bubbletea then sends `ColorProfileMsg{that profile}` on start
   (`tea.go:1080-1089`) whether or not it was pinned. A teatest golden's fidelity
   therefore follows its pin automatically, through the same rebuild trigger
   production uses. The three sites pinning `colorprofile.ASCII` today
   (`model_test.go:2355`, `:2417`, `move_model_test.go:499`) keep working unchanged
   and simply run at `FidelityFlat`.

2. **Existing structure goldens need no edits.** Structure goldens strip ANSI, so a
   class-A effect — which is color-only by definition — cannot change their bytes. A
   class-B effect could change glyphs, but under the determinism contract every
   animated surface is seeded at its settled value on initial paint (item 2), and a
   suppressed class-B effect renders exactly that settled state. Structure goldens are
   therefore **fidelity-invariant**, which is why `theme.New(true)` is permitted and
   why §10 costs the 21 Tier-A goldens nothing.

3. **Direct `View()` goldens must construct their styles at the matching fidelity.**
   These build a model by hand and never run a program, so no `ColorProfileMsg`
   arrives and the default (`FidelityFull`) stands. A color golden built that way is
   correct as written. An indexed golden built that way is not:
   `theme.Downsample(content, colorprofile.ANSI256)` quantizes the ramp *after* the
   fact, which is precisely the banded artifact rule 2 forbids kb from shipping. It
   must be built from `theme.NewFor(true, colorprofile.ANSI256)` so the flattening
   happens in the theme, before render.

**Golden footprint.** §6.4's list of new color-pinned goldens is unchanged. §10.7 adds
exactly **one** indexed golden — the board with cards, at `colorprofile.ANSI256` — as
the standing proof of the degradation rule. One golden, one view; the class is not
replicated per view, because what it asserts is a property of the theme, not of any
particular layout.

#### 10.7.8 Guards

Four tests, all pure functions over the theme, none needing a terminal. They ship
with the slice that lands this subsection, alongside the guards of §1.7 and §1.9.

| Guard | Asserts |
|---|---|
| `TestAccentSlotStaysInTheAuditedSet` | `AccentSlot` over a corpus including the §10.7.2 table, the empty string, non-ASCII titles and titles longer than a frame returns only `{Brand, Label1..Label5}`. This is what keeps the §1.7 audit closed without re-running it per title |
| `TestAccentSlotMatchesTheWorkedExamples` | The §10.7.2 table, row for row, including both default rows |
| `TestAccentContrastClearsAA` | The five pairs of §10.7.4 at >= 4.5:1 in truecolor and after quantization to their x256 index. Same mechanism as §1.9's contrast guard: re-hue the wheel below the floor and the build fails |
| `TestFidelityForMapsEveryProfile` | All six `colorprofile` constants including `Unknown`, against the §10.7.5 table. A profile added upstream that this map does not name is a compile-visible gap, not a silent `FidelityFull` |

---

### 10.8 Empty, loading and error state anatomy

Three states are first-class screens, not gaps in a screen. A surface with nothing
in it, a surface waiting on a write, and a surface that failed each get a specified
row, a specified token and a specified next action. The rule this subsection exists
to enforce: **no surface ever renders a bare blank panel, and no two of the three
states may render alike.**

#### 10.8.1 What the TUI does today

Inventory of every surface in `internal/tui`, read at the branch state of this spec.
The receipts are `file:line`.

| Surface | Empty | Loading | Error |
|---|---|---|---|
| Board column | `(empty)`, `Column.Meta` (`FgMuted`/`Surface`), one row, no glyph, no action, no filter distinction (`board_view.go:816`) | none — while the first snapshot is outstanding every column renders `(empty)` | none at column scope |
| Board footer bar | n/a | `"loading board..."` in `FgMuted` (`board_view.go:390-391`) | `"error: "+err` in `StatusDanger` on `Surface` (`board_view.go:381-388`), one row, hard-cut by `fitLine` with no ellipsis (`:1219-1221`) |
| Board, card move | n/a | `move.saving` gates keys; no indicator | **every** move notice paints `StatusWarn` (`board_view.go:373-375`). `move.statusError` is written in five places (`move_store.go:38,102,119`, `move_model.go:181,201`) and read in **none** — a failed write is indistinguishable from a successful drop |
| Card detail, COMMENTS | section count renders `none` (`carddetail/model.go:929-932`); no body row | static `"loading comments and context..."` (`:917-921`) | `"comments error: "+err` through `m.row` — `FgBase` on `OverlaySurf`, **no hue at all** (`:923-928`) |
| Card detail, blocker links | absent by design; the completion gate row carries the meaning (`model.go:1006-1038`) | gate reads `unknown: linked blockers loading` in `FgSubtle` (`:1025-1026`) | `"blocker links error: "+err` through `m.row`, no hue (`:910-912`) |
| Card detail, pane status | n/a | footer band `"write in progress \| esc stays here"`, static (`actions.go:586-588`) | body row with a literal `"error: "` text prefix, `FgBase` on `OverlaySurf` (`model.go:823-828`; action-mode copy at `actions.go:550-556`) |
| Card detail, drift | n/a | body line `"<op> in progress..."` (`drift.go:241-243`), footer `"check in progress \| input locked"` (`:266-268`); no spinner | folded into the same untinted status row (`:271-277`) |
| Card detail, delete confirm | the action self-cancels with `"…none remain to remove"` (`model.go:408`) | none | as pane status |
| Card editor, labels | `(no label suggestions; Enter adds typed labels)` (`cardeditor/view.go:462-466`) — the **only** empty state in the TUI that names its next action | none | `rowError`, `StatusDanger` on `OverlaySurf` (`:384-386`, token at `:267-268`) |
| Card editor, similar items | absent when zero (`view.go:491-493`) | static `"similar items: searching..."`, `rowHint` (`:486-487`) | `rowError` (`:488-489`) |
| Card editor, draft / save | n/a | footer band with a `bubbles` spinner frame, colour stripped by `ansi.Strip` (`:315-319`, `:326-330`) | body `rowError` plus a duplicate text-prefixed footer copy (`:392-398`, `:302-307`) |
| Settings, AI block | n/a | static `"loading settings..."`, hint token (`settings_view.go:109-110`) | footer band `"error: "+status`, `FooterBand` (`FgSubtle`/`OverlayBand`) — **no hue** (`:128-136`) |
| Settings, forge rows | `(none configured)` (`settings_view.go:121-123`) | none | as above |
| Task action dialog | checklist mode never opens empty; the board reports `"Checklist is empty"` instead (`ship_actions.go:145`) | literal `" (saving...)"` appended to the **headline** (`:1005-1007`) | `appendActionError`, `StatusDanger` on `OverlaySurf`, one row, hard-cut by `actionFit` (`:1218-1230`) |
| Task action, purge | n/a | as above | arm state is the `Armed` button on `StatusAlarm` (`:1054-1067`) — correct, unchanged |
| Issue import, sources | falls back to a plain field row; no empty state (`issueimport/view.go:389-391`) | body hint row with a stripped spinner frame (`:169-173`, `:390-396`) | `rowError`, `StatusDanger` on `OverlaySurf` (`:136-138`) |
| Issue import, review | `fetched 0` then an `ISSUES` section with no rows (`view.go:256-267`) | determinate `bubbles/progress`, body row, bar width `min(w-label-1, 24)` (`:309-320`) | per-row `rowError` under its issue (`:265-267`) |
| ADR split, review | `STORIES` section with no rows (`adrsplit/view.go:242-244`) | footer band with a stripped spinner frame (`:396-398`, `:409-413`) | per-row `rowError` (`:265-268`); pane status is a text-prefixed footer line with no hue (`:399-404`) |
| Help overlay | not reachable — the registry always has rows, and a feature the board lacks is a **disabled** binding rather than an omitted line (`help.go:87-91`) | none | none |
| Project switcher | **does not exist.** No switcher surface is present in `internal/tui` |

Four facts fall out, and they are what the rest of this subsection fixes.

1. **Loading and empty render identically on the board.** Until the first snapshot
   lands, every column says `(empty)` while the footer says `loading board...` two
   rows away. The most-looked-at surface in the app tells the user the board is empty
   every time it starts.
2. **Half the error surfaces carry no hue.** The card detail pane, the drift view,
   the settings pane and the ADR split pane all report failure with the ASCII prefix
   `error: ` in body or band text. §1.5 named `StatusDanger` for exactly this and four
   surfaces never took it.
3. **Every error truncates bare.** `fitLine` (`board_view.go:1219`), `fit`
   (`cardeditor/view.go:597`, `adrsplit/view.go:559`) and `actionFit`
   (`ship_actions.go:1228`) all call `ansi.Truncate(s, w, "")`. §3.3 fixed the
   truncation primitive as `ansi.Truncate(s, w-1, "") + "…"` for a card *description*;
   the strings a user needs most are cut with no mark at all.
4. **There is one spinner and it has no colour.** `Styles.Spinner` is `spinner.Dot`
   (`theme/styles.go:418`) and all three call sites wrap it in `ansi.Strip`
   (`cardeditor/view.go:330`, `adrsplit/view.go:413`, `issueimport/view.go:396`). The
   two-tier split of §10.2.4 has nothing behind it yet.

#### 10.8.2 Tokens

The three states are built from `FgSubtle`, `FgMuted`, `StatusDanger` and
`TintDanger`, all of which §1 already carries. `Glyph.Empty` and `Glyph.Alert` are
declared in §10.4.1. Six metrics are added.

| Token | Value | Role |
|---|---|---|
| `EmptyHeadlineMin` | `24` | Inner width at or above which an empty row keeps its headline |
| `EmptyActionMin` | `10` | Inner width at or above which an empty row keeps its action tail |
| `ActionGap` | `2` | Columns before an empty row's action tail and before an error row's retry tail |
| `BusyGap` | `1` | Columns between a spinner frame and its label |
| `ErrorMaxLines` | `3` | Lines an error message may wrap to inside a panel |

The determinate bar's width cap is `MeterCells` (§10.1.1), which is the token that
promotes the literal at `issueimport/view.go:318`.

#### 10.8.3 Empty state anatomy

One row. Three parts, in this order.

```
○ no cards  n new card
^ ^         ^ ^
| |         | verb, FgSubtle
| |         key, FgBase bold
| headline, FgSubtle
Glyph.Empty, FgMuted
```

- The glyph is `FgMuted` on the row's own surface; the headline and verb are
  `FgSubtle`; the key is `FgBase` bold. The key is the brightest run in the row
  because it is the only part the user has to act on.
- One space after the glyph, `ActionGap` columns between headline and tail, one space
  between key and verb.
- The headline is a lowercase noun phrase with no terminating period, matching the
  footer's voice (`ready`, `loading board...`). Never a sentence, never an apology,
  never "there is nothing here".
- **Never a bare blank panel.** A surface with no content renders this row or it
  renders nothing at all — see the eligibility rule below.

**Width ladder.** Not a survival scan like §3.4's chip row; the three parts have a
strict priority and the ladder is applied whole at each rung, against the surface's
inner width.

| Inner width | Rendered |
|---|---|
| `>= EmptyHeadlineMin` | glyph + headline + tail |
| `>= EmptyActionMin` | glyph + tail |
| below `EmptyActionMin` | glyph alone |

The headline goes first because the tail is the actionable half. A 12-column column
that says `○ n new card` is more useful than one that says `○ no cards`.

**The action tail.** The tail names whatever the user must actually operate.

- On a key-driven surface the tail is the key and its verb, taken from the
  `bubbles/v2 key` registry §5.2 adopted (`help.go:56-91`), never from a literal. A
  binding that is disabled — `n`, `e`, `s`, `a`, `i` all are, depending on how the
  board was constructed (`help.go:87-91`) — is not eligible; the surface falls through
  a declared order of candidate bindings and, if none is enabled, renders the headline
  alone. This is the same self-managing-keymap property §5.2 adopted the registry for,
  applied to a second surface.
- On a surface whose action row (§4 step 6) owns the action, the tail is the
  **button's label**, rendered in the same `FgBase` bold a key would take, and the
  panel seeds the action row's focus on that button. The empty state names the control
  and puts the cursor on it in one move, so `Enter` works immediately.

**Which surfaces get one.** An empty state belongs to (a) a panel or column that
would otherwise have no body content at all, or (b) a section that renders its header
whether or not it is filled. A section that is simply absent when it has no content —
the card's checklist, the editor's similar-items block — **stays absent**. Three
"nothing here" rows stacked in a detail pane is a worse pane, and the discipline is
what keeps the empty state meaningful where it does appear.

#### 10.8.4 Loading anatomy

**Tier.** §10.2.4 defines the two tiers and the rule that assigns them: an operation
whose wait is an AI or forge round trip takes the **branded** tier; everything backed
by the local store takes the **plain** tier. The axis is latency, not importance. A
local card save completes inside two frames; a branded animation on it is noise that
never finishes a cycle.

**Shape.** `frame + BusyGap + label`. The label is `FgSubtle` on the row's surface,
lowercase, present continuous, no ellipsis — the animation is the ellipsis.
`ansi.Strip` on the frame is deleted at all three sites: the frame is the one part of
the row that is supposed to carry a colour.

**Slot.** The overlay **footer band** is the loading slot. This is crush's help-line
precedent — a busy dialog puts spinner and label in the slot that otherwise carries
the key hints (`dialog/commands.go:326`), with a `LoadingDialog` interface letting the
overlay drive the front dialog's loading state (`dialog/dialog.go:46-50`, `:238-253`)
— and it is already where `cardeditor` and `adrsplit` put their busy prefix
(`cardeditor/view.go:315-319`, `adrsplit/view.go:396-398`). Normative:

1. A busy panel replaces the **head** of its footer band's hint ladder (§10.4.6) with
   the busy line. Hints that are still live — `esc cancel` while a cancellable
   operation runs — survive as the ladder's tail. The band is the only row whose
   content changes, so the body does not reflow when the operation lands.
2. A **section** whose content is still arriving carries its own busy row as the
   section's first body row instead, because the footer is already describing what the
   panel as a whole is doing. Card detail's COMMENTS is the case.
3. A **determinate** operation — one that knows its denominator — takes the meter of
   §10.1.3 in a body row pinned directly above the action row, never the footer,
   because the band's next hint would overwrite a count. Bar width is
   `max(min(inner - width(label) - 1, MeterCells), 1)`.
4. **One motion per surface.** At most one spinner may animate on a surface at a
   time; the footer band wins, and a body busy row under a busy footer renders its
   label with no frame. crush enforces the same discipline by suppressing its animated
   ellipsis whenever the dynamic suffix is non-empty, so two things never move at once
   (`anim/anim.go:470-486`; §10.2.5 carries the same rule inside the engine).
5. A busy state **never** rides on a headline. `ship_actions.go:1005-1007` appends
   `" (saving...)"` to the dialog's question; that string is deleted and the state
   moves to the band. A headline is not a status slot.

**Loading beats empty.** A surface that is waiting on its first content renders the
busy row, not the empty row. On the board this is the whole fix for finding 1 above:
while `!haveBoardSnapshot`, a column body renders one plain-tier busy row in place of
`(empty)`.

**Determinism.** Every busy row obeys the contract of §10.2.2 and schedules against
§10.3.1: the settled state is the initial paint, the tick chain returns a nil command
the moment the operation ends, tests step it by injecting the tick message, and each
animated surface gets one added byte-stability test. Existing goldens need no edits.
The spinner never gates input — where input is gated it is the operation's own busy
flag doing it, and that gating is frozen v1.0.1 behaviour this subsection does not
touch. The destructive-prompt grace of §10.3.3 is a separate mechanism on a separate
set of surfaces and is unaffected.

#### 10.8.5 Error anatomy

**Token.** The hue is the message's foreground; an error is never a filled chip.
Which slot depends on the tier the row sits on, and the split is measured, not
stylistic:

| Pair | Truecolor | Verdict |
|---|---|---|
| `StatusDanger` on `Canvas` | 6.28 | passes |
| `StatusDanger` on `Surface` | 5.75 | passes; the board footer is already correct |
| `StatusDanger` on `OverlaySurf` | **2.96** | **fails AA** — this is what four panels render today |
| `TintDanger` on `OverlaySurf` | 4.91 | passes |
| `TintDanger` on `OverlayBand` | 3.82 | fails AA |
| `StatusDanger` on `OverlayBand` | 2.31 | fails AA |
| `FgOnAccent` on `StatusDanger` | 6.28 | passes (the §3.4 overdue pill, unchanged) |

(WCAG 2.x relative luminance, the same formula and 4.5:1 floor §1.9 applies to button
labels. Computed for this subsection and **not yet asserted by a test**; §1.9's
contrast audit is extended to cover these non-button pairs, and if the arithmetic here
is off the build is what says so. No x256 column: these are existing slots whose
indices §1.7 already fixed.)

So:

- On the board tiers (`Canvas`, `Surface`) an error is **`StatusDanger`**. Unchanged
  from `board_view.go:381-388`.
- Inside a panel an error is **`TintDanger` on `OverlaySurf`**. §1.9 built
  `TintDanger` as the readable-on-a-raised-tier form of the Danger hue; this is the
  second thing it is for, and it costs no new slot.
- A footer band **never carries an error message**. Neither slot clears the floor on
  `OverlayBand`, and §4 step 7 already bars anything that re-arms its own style from a
  band row. Settings (`settings_view.go:128-136`) and ADR split
  (`adrsplit/view.go:399-404`) move their error out of the band and into a body row.
  The band goes back to hints.

**Row.** `Glyph.Alert + " " + message`, in a body row pinned directly above the
action row — the same anchor §4 step 6 gives the action row itself, so an error and
the control that will retry it are adjacent. A panel with no action row puts the
error in the last body row.

**Wrapping.** Errors carry store paths, URLs and wrapped Go error chains, so a single
truncated line is the common case, not the edge case.

1. Sanitize first: control characters to spaces and every newline collapsed to a
   single space, through the existing `sanitizeTerminal` / `safeText` / `sanitize`
   helpers. An embedded newline must never reach the row grid.
2. Wrap greedily to the panel's content measure —
   `min(pw - 2*OverlayInsetX, ContentMax)` from §4 — using §3.3's rules verbatim: a
   word longer than the measure is hard-truncated rather than overflowing, and the
   **last** allotted line carries the ellipsis when text remains.
3. At most `ErrorMaxLines` lines, with §3.3's truncation primitive. The bare
   `ansi.Truncate(s, w, "")` used by `fitLine`, `fit` and `actionFit` is never the
   primitive for an error.
4. Continuation lines hang-indent by `width(Glyph.Alert) + 1` so the message block
   reads as one object. The glyph is not repeated and the word `error` is not repeated
   per line.
5. The board's footer bar is one row and stays one row: the message is ellipsized,
   not wrapped.

**Retry.** An errored operation does **not** grow a `Retry` button. It returns the
control that started it to its blurred state, keeps it focusable, and the error row
names it:

```
▲ forge fetch failed: dial tcp 10.0.0.4:443: connection refused  Import
```

Tail separated by `ActionGap`, rendered like an empty state's tail — `FgBase` bold
for the key or button label, `FgSubtle` for a verb where the label does not already
read as one. This is the smallest correct thing: §5.4 makes the variant a property of
the action carried structurally on the control, and a second control for the same
action would need a second variant assignment for an action that already has one.

An error with no retryable trigger carries no tail. Validation failures —
`"comment must not be empty"` (`carddetail/actions.go:339`), `"nothing selected"`
(`issueimport/model.go:457`) — are fixed by editing the field the focus is already in.

**Per-item errors are not panel errors.** An error attached to one row of a list — an
issue that failed to write (`issueimport/view.go:265-267`), a story that failed to
create (`adrsplit/view.go:265-268`) — stays inline under its own row, one line,
`TintDanger`, ellipsized, no glyph and no tail. The panel-level row is reserved for
what failed the *operation*.

#### 10.8.6 Widgets

Three additions to `internal/tui/widget`, in the shape of §5.1. Every surface in
§10.8.7 renders its three states through these and through nothing else.

| Element | API sketch |
|---|---|
| Empty state | `Empty(styles *theme.Styles, o EmptyOpts) string` — `EmptyOpts{Headline, Key, Verb string, On theme.Slot, Width int}`; owns the ladder of §10.8.3 and the glyph, and returns exactly one row |
| Busy line | `Busy(styles *theme.Styles, o BusyOpts) string` — `BusyOpts{Frame, Label string, On theme.Slot, Width int}`; `Frame` is the tier's rendered frame, empty for the suppressed second motion of §10.8.4 rule 4 |
| Error block | `Error(styles *theme.Styles, o ErrorOpts) []string` — `ErrorOpts{Message, Key, Verb string, On theme.Slot, Width, MaxLines int}`; owns sanitize, wrap, hanging indent and the retry tail, and returns one to `ErrorMaxLines` rows |

`On` is the surface slot the row sits on, which is what selects `StatusDanger` against
`TintDanger` inside `Error` — the caller names its tier, never its hue. Panel callers
wrap the result in `widget.OverlayRow` as they already do.

#### 10.8.7 Per-surface assignment

Normative. Copy strings are the literal strings to render. Tier names are §10.2.4's.

| Surface | Empty copy | Loading treatment | Error treatment |
|---|---|---|---|
| Board column, no filter | `○ no cards  n new card`; falls to `i import forge issue` when `n` is disabled, then to the headline alone | plain tier, one busy row `loading` in the column body while `!haveBoardSnapshot`; the empty row is not rendered | none at column scope |
| Board column, filter active | `○ no matches  X clear filter` | as above | none |
| Board footer bar | n/a | plain tier in the state segment, `loading board` (the trailing `...` is deleted; the frame is the ellipsis) | `▲ ` + message, `StatusDanger` on `Surface`, one row, ellipsized. No retry tail: `schedulePoll` retries every `PollInterval` on its own and the row clears itself |
| Board, card move | n/a | plain tier in the state segment while `move.saving`, label `saving move` | `▲ ` + message in `StatusDanger` when `move.statusError` is set — the flag is finally read; a non-failure notice keeps `StatusWarn` as today |
| Card detail, COMMENTS | `○ no comments  Comment`, one body row under the section band; the band keeps its `none` count | plain tier, section's first body row, `loading comments` | `▲ ` + message, `TintDanger`, section's first body row, tail `Close` — the pane cannot re-fetch, so the tail names the dismissal |
| Card detail, blocker links | none — absent by design; the completion gate carries it | gate keeps its `unknown: linked blockers loading` form in `FgSubtle` and takes **no** spinner (it is a tri-state indicator, not a busy state) | `▲ ` + message, `TintDanger`, pinned above the action row, tail `Link` |
| Card detail, write in progress | n/a | plain tier, footer band, `saving`; ladder tail `esc stays here` survives | `▲ ` + message, `TintDanger`, pinned above the action row, tail = the button that failed (`Save comment`, `Add link`, `Delete`) |
| Card detail, drift | n/a | **branded** tier, footer band, `checking drift` / `updating baseline`; ladder tail `esc cancel`. The body's `"<op> in progress..."` line is deleted | `▲ ` + message, `TintDanger`, above the action row, tail `Check selected` |
| Card editor, label suggestions | `○ no label suggestions  enter add typed labels` | none (local, sub-frame) | `▲ ` + message, `TintDanger`, inline under the labels field, no tail: the field is already focused and the fix is retyping in it |
| Card editor, similar items | none — the block is absent when empty | plain tier, one body row where the section band would be, `searching similar items` | `▲ ` + message, `TintDanger`, same row, no tail (advisory; the save is not blocked) |
| Card editor, draft | n/a | **branded** tier, footer band, `drafting card`; ladder tail `esc cancel` | `▲ ` + message, `TintDanger`, above the action row, tail `Draft`. The duplicate text-prefixed footer copy is deleted |
| Card editor, save | n/a | plain tier, footer band, `saving card` | `▲ ` + message, `TintDanger`, above the action row, tail `Save card` |
| Settings, AI block | n/a | plain tier, one body row under the `AI SETTINGS` band, `loading settings` | `▲ ` + message, `TintDanger`, body row above the action row, tail = the button that failed. Out of the footer band |
| Settings, test connection | n/a | **branded** tier, footer band, `testing connection` | as above, tail `Test connection` |
| Settings, forge rows | `○ no integrations  + Add integration` under the `FORGE INTEGRATIONS` band | none | as above, tail `Save` or `Confirm remove` |
| Task action dialog | none — the board reports `Checklist is empty` before the dialog opens; unchanged | plain tier, footer band, `saving`. The `" (saving...)"` headline suffix is deleted | `▲ ` + message, `TintDanger`, `appendActionError`'s row, wrapped to `ErrorMaxLines`, tail = the dialog's affirmative (`Ship anyway`, `Kill with reason`) |
| Task action, purge | n/a | as above | arm state unchanged: `Armed` on `StatusAlarm` (§1.9). A purge failure is an ordinary error row below it |
| Issue import, sources | `○ no forge configured  Cancel` | **branded** tier (`SeedImportFetch`, per §10.2.4 — a forge round trip; the tier rule governs where this table disagreed), footer band, `loading sources` | `▲ ` + message, `TintDanger`, above the action row, tail `Cancel` |
| Issue import, fetch | n/a | **branded** tier, footer band, `fetching and drafting`; ladder tail `esc cancel`. The body hint row at `view.go:169-173` is deleted — one place per state | `▲ ` + message, `TintDanger`, above the action row, tail `Import` |
| Issue import, review | `○ no issues fetched  Back` under the `ISSUES` band | determinate: the meter of §10.1.3 in a body row above the action row; no spinner | per-item: inline under its issue, one line, `TintDanger`, ellipsized, no glyph, no tail. Batch failure: the panel row, tail `Import` |
| ADR split, input | none | **branded** tier, footer band, `proposing stories`; ladder tail `esc cancel` | `▲ ` + message, `TintDanger`, above the action row, tail `Propose stories`. Out of the footer band |
| ADR split, review | `○ no stories proposed  Back to source` under the `STORIES` band | plain tier, footer band, `adding stories` | per-item inline as issue import; batch failure on the panel row, tail `Add selected (N)` |
| Help overlay | not reachable — a feature the board lacks is a disabled binding, which is the model this subsection generalizes | none | none |
| Project switcher | surface does not exist; nothing to specify | — | — |

#### 10.8.8 Goldens

Per §6.4, and additive to the per-view goldens the restyle slices already regenerate:

- Each surface in the table above gets an **ASCII-pinned structure golden per state**
  it can reach. Structure is the point: the ladder rung, the row count, the wrap and
  the truncation mark all survive the ASCII profile, and so do `Glyph.Empty` and
  `Glyph.Alert`.
- Each **error** state additionally gets a `colorprofile.TrueColor`-pinned golden.
  The whole error treatment is a hue on a tier; an ASCII golden of it asserts nothing,
  which is §6.4's own argument.
- Loading goldens capture the **settled** frame only. Intermediate spinner frames are
  unit tests that inject the tick message, never teatest goldens, and no
  `teatest.WaitFor` predicate may match a transient busy line.

---

### 10.9 Contestable calls

Thirteen decisions in §10 are genuine judgement rather than derivation from the donor
or from map #177. All thirteen were ratified as recommended in #181 (map #177); the section is locked. Everything
else in §10 either carries a receipt or falls out of a rule §1-§9 already fixed.

**1. One clock at 20fps for the whole TUI, gated by an unmeasured benchmark.**
Chosen: a single `Timing.FPS` of 20 (crush `anim.go:21-49`), with the plain spinner
tier preserved exactly by `PlainStride = 2` and the branded tier bounded to one engine
by `MaxEngines = 1`; plus the two binding rules of §10.3.2 — motion surfaces prerender
their frames, and the slice landing the first motion surface adds `BenchmarkBoardView`
at 211x52 and drops the token to 10 if a full `View()` exceeds 25ms.
Alternative: start at 10 and raise it once the benchmark exists; or add a settled-state
stride that drops to 1s once the elapsed suffix appears.
Why: a mounted engine wakes the program 20 times a second to render a cached string,
which is the cost being ratified. The settled-stride alternative saves ~19 wakeups per
second at the price of a second effective cadence, which is the thing the one-clock
rule exists to prevent. The 25ms threshold is reasoned — half the frame period — not
measured, and that is the contestable half.

**2. Ramps are prebuilt at 24 steps and resampled by index.**
Chosen: `GradSteps = 24`, with cluster `i` of `n` taking `round(i*(GradSteps-1)/(n-1))`.
Alternative: `theme.Grad` blends and constructs styles per call, which the §6.2 seam
test permits (it greps `internal/tui` outside `theme/`) but the "built once" rule does
not.
Why: prebuilding is the only option consistent with §6.2's non-negotiable; endpoints
are hit exactly; 24 is ~2.7x the longest gradient-bearing run in the spec. Cost is 5
ramps x 24 = 120 styles built once. Fidelity loss versus an exact per-run blend is not
visible at `n <= 24`.

**3. The top-bar wordmark carries the per-project accent, and the accent goes nowhere
else.**
Chosen: a 3-column run — accent rail plus bold accent-hued `kb` — and no other surface
in the TUI takes the accent. The wordmark carries no gradient and is not promoted to a
filled pill.
Alternative: re-hue the wordmark as a fixed `HueTodo` → `Brand` ramp (two clusters,
two distinct indices at x256, the one gradient that survives quantization intact); or
extend the accent to the overlay header band or the footer.
Why: a run cannot be both a fixed brand ramp and a per-board identity hue, and identity
is what the row is for. Three columns is a thin signal, but the top bar's left run is
the only place on screen hosting no semantic hue; every candidate extension puts a
meaningless hue adjacent to a meaningful one — an overlay header in a board's accent
could render warn-orange directly above a `StatusWarn` chip that means something.

**4. The accent derives from the board title, on the five-slot label wheel.**
Chosen: hash `TrimSpace + ToLower` of `board.Board.Title` through the §1.6 wheel;
`Board` and the empty string resolve to `Brand`.
Alternative: hash the board file's absolute path (stable across renames); or add a
dedicated wider accent wheel.
Why: the accent is a recognition cue for the identity the user typed and controls, so a
rename deliberately changing the color is correct behavior, not a defect; a path hash is
stable but unpredictable and unchooseable, which makes it decoration rather than
identity. A wider wheel costs at least three new hexes and three new x256 indices,
reopening the §1.7 audit that deriving from the wheel exists to keep closed. The price
is that ~20% of boards collide with each other and ~20% look identical to an unnamed
board — survivable precisely because the accent carries no meaning.

**5. Below full fidelity a ramp renders flat, and three new glyphs carry what hue
cannot.**
Chosen: §10.7.5 rule 2 — a graded run resolves to its lead slot below `FidelityFull`,
resolved inside `theme.NewFor`, never branched on in a view; plus `Glyph.Track`,
`Glyph.Empty` and `Glyph.Alert` (§10.4.1); plus one `colorprofile.ANSI256`-pinned board
golden as standing proof; plus one amendment to §6.2's closed rebuild-trigger list for
`tea.ColorProfileMsg`.
Alternative: emit every ramp unconditionally and let the renderer's per-cell
quantization band it; carry the empty and error states on hue and copy alone; resolve
fidelity somewhere other than the theme.
Why: a nine-cell ramp quantized onto two or three indices with boundaries wherever the
color cube lands is an artifact that looks like a bug and communicates nothing. Under
the ASCII profile hue is the one thing that does not survive, so a glyph is what tells
an ASCII terminal that a row is an error or that a meter has a position — without
`Glyph.Track` the structure golden of the one widget whose job is position asserts
nothing about position. `NewFor` is the only place fidelity can be resolved without
putting a profile branch in a render path. The four `#178`-era candidate glyphs sit in
Unicode blocks §3.6 already spends on, so they add no new font risk.

**6. The armed overlay header band re-fills `StatusAlarm`, amending §4 step 4.**
Chosen: on Armed only — not on destructive pending — the header band takes `FgBase`
bold on `StatusAlarm`, the same pair §1.9 gives the armed button.
Alternative: leave §4 step 4's solid `Brand` untouched in every state.
Why: §1.9 already argues that an armed state a user misreads is the worst failure in the
button system; having the frame and the button say the same thing with the same fill
closes that gap. Repainting the header on every transient destructive mode inside a card
detail would be loud and would devalue the armed signal, so pending gets only the
section-label re-ramp. This is the only header-band recolor proposed anywhere in the TUI.

**7. The destructive-prompt grace is rescoped, and exempts the dismissal ladder.**
Chosen: crush's three values unchanged (425 / 1500 / 500,
`dialog/dialog.go:52-62`, `:120-148`, `:168-176`), applied only to prompts whose
affirmative carries the `ButtonDanger` variant of §5.4, and never swallowing
`esc` / `q` / `ctrl+c`.
Alternative: crush's own scope — every dialog, every key.
Why: crush graces async dialogs because they appear while the user is typing; kb has no
async dialog, so its equivalent hazard is type-ahead into a destructive commit, which
§5.4 already marks structurally. The costs of the exemption are asymmetric: a swallowed
affirmative is the mechanism working, a swallowed cancel is a user concluding the app has
hung. Note 425 > 400 is load-bearing — it puts the trailing half of a double-click inside
the grace by construction. The exemption is a deliberate divergence from the donor and
adds a branch to the grace filter.

**8. The footer notice self-dismisses after 5s, changing frozen v1.0.1 behavior.**
Chosen: `NoticeTTL` as a second dismissal path, with input dismissal kept as the faster
one, a sequence guard, and errors explicitly excluded.
Alternative: leave the notice cleared only by the next board input, as shipped.
Why: `noticeOwnsFooter` (`board_view.go:341-343`, `:357-368`) suppresses the entire hint
ladder *and* its hit regions while a notice stands, so an unattended board keeps a stale
`moved to DOING` where its affordances should be, indefinitely. `loadErr` / `pollErr` /
`preferenceErr` are state, not notices, and must never expire on a timer.

**9. Pointer scope: the board cursor never follows the pointer, hoverability is opt-in,
and a double-click is added.**
Chosen: the full mouse-mode machine is scoped to overlay choice surfaces only (checklist,
drift, ADR-split stories, issue import, the pickers, the settings rows); board cards and
column bands get an affordance cue and nothing more. Hover opts in via a non-empty
`ControlID` on the existing region list — `Map.AddControl` is clickable and hoverable,
`Map.Add` is clickable only. A 400ms double-click is classified in `pointer` and bound to
one thing: open the card detail.
Alternative: crush's full machine on every surface including the board; a separate hover
region list; no double-click at all.
Why: the board cursor is simultaneously the drag source, the anchor every board
keybinding resolves against, and the card the detail overlay opens from — a pointer sweep
across four columns on the way to the scrollbar would rewrite it a dozen times, and the
web UI the TUI is at parity with moves selection on click, never on hover. The
consequence a human should ratify is that kb then has two hover contracts and a user has
to learn which surfaces respond to the pointer as a cursor; and that every site calling
`Map.Add` is silently unhoverable until migrated, with "has press feedback" and "has hover
feedback" becoming one property that a separate list could have kept apart. The
double-click's drag exclusion is mandatory — a lift ending on its origin must not register
as one — and keyboard parity already exists (`enter` on a selected card), so the gesture
is never the only route.

**10. Discoverability cells: the hotkey underline becomes mandatory, and every focusable
overlay row permanently reserves two columns.**
Chosen: the resolver moves out of `carddetail` into `widget.Hotkey`, the six `-1` call
sites adopt it, and step 3 appends `" (k)"` at 4 cells; the focus gutter reserves
`FocusGutterW + FocusGutterGap` in every state, so a focusable row's prose column is 2
narrower than a static one.
Alternative: keep the underline opt-in per surface as it is today; draw the gutter only
when focused and accept a 1-cell reflow of the row's text.
Why: a button that hides the key still driving it is the discoverability gap the button
row was introduced to close, and a reserved gutter is the only form that satisfies
§10.4.4. Both cost real cells: a narrow ADR-split or issue-import row can gain 4 cells per
button and stack one-per-row sooner (§5.2), and a panel that alternates focusable and
static rows shows a ragged left edge. The alternative to the gutter reserve is text that
jumps as focus moves, which is worse. huh's `Confirm` pair stays exempt — it renders its
own buttons from one style pair and exposes no range hook.

**11. The column header band keeps its status dot when focused, amending §2.2.**
Chosen: `head = Focus + Dot`, `BandHeadW = 5`, so the label column is fixed across focus.
Alternative: accept the one-cell shift on the grounds that the band's total width is
unchanged and its whole background changes color at the same instant.
Why: it costs nothing, holds the label column fixed, and preserves the caret that makes
the focused band read as filled edge to edge. It is recorded rather than applied because
§2.2's art is normative and a subsection may not silently overwrite it.

**12. Error text inside a panel moves to `TintDanger` and leaves the footer band; a
board-scope error longer than the frame stays unrecoverable.**
Chosen: `StatusDanger` keeps the board tiers (`Canvas` 6.28, `Surface` 5.75);
`TintDanger` takes `OverlaySurf` (4.91 against `StatusDanger`'s 2.96); no error renders
in a band row; the board footer ellipsizes rather than wraps and no error-log surface is
built.
Alternative: keep `StatusDanger` in panels and accept the AA failure; keep settings and
ADR split reporting in the band; add an error-detail overlay.
Why: `TintDanger` is an existing slot §1.9 already built and audited as the
readable-on-a-raised-tier Danger form, so the fix costs no slot, and extending §1.9's
contrast test to non-button text pairs makes the arithmetic the build's problem. Two
independent reasons converge on the band: neither Danger slot clears the floor on
`OverlayBand`, and §4 step 7 already bars anything that re-arms its own style from a band
row. The one-row footer limit is accepted because load and poll errors self-retry every
`PollInterval` and clear themselves; a whole overlay to read the tail of a string is not
proportional. Incidental finding, outside this section's remit and flagged rather than
fixed: `FgSubtle` on `OverlayBand` — the shipped `Overlay.FooterBand` token of §2.1 —
computes at 2.85:1, a pre-existing AA failure.

**13. Branded-tier scope: latency axis, one engine, 250ms anti-flash, remount on return.**
Chosen: the branded tier is assigned by whether the wait is an AI or forge round trip
(crush's own split, `ui.go:434-437` against `dialog/arguments.go:111-112`); `Test
connection` is branded; `MaxEngines = 1` with a background surface stopping its engine and
remounting at step 0 when it returns to front; `BirthDelay = 5` ticks before the engine
becomes visible.
Alternative: assign by importance rather than latency; give `Test connection` the plain
dot; resume mid-wipe on return; mount immediately as crush does.
Why: a local card save completes inside two frames and a branded animation on it never
finishes a cycle, so latency is the right axis. `Test connection` is the single coin flip
— a real network round trip that can take seconds, but also a diagnostic the user asked
for and already expects to block. `BirthDelay` has no direct crush receipt (the nearest
precedent is the 425ms dialog grace) and exists because kb's branded tier covers a test
against a warm local forge that can return in 80ms; an engine that appears and vanishes
inside 100ms is a flash, not feedback. The remount is the contestable half of the ceiling:
resuming mid-wipe would need a step counter persisted across a gap with no ticks, which
means either a wall-clock read or a frame count that lies — but some reviewers will read
the second wipe as a stutter rather than as the surface waking up.
