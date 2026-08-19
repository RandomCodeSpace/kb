# Prototype: web-faithful

Throwaway visual prototype for #137 (map #136). Hardcoded fake data, no
dependency on `internal/tui`, not shipped.

## The language

- Every card is a rounded-border surface with `Padding(0, 1)` and its own
  background, sitting on a panel background.
- Every column is a full box with a header band (status dot, name, count) that
  has its own background and is separated from the card stack by a rule.
- Every card carries a description snippet under the title rule: up to two
  muted lines, wrapped to the card width, ellipsised when it does not fit. It
  is the first thing dropped when density goes compact.
- Semantic accents: priority scale P1-P4, blocked in warn, due in ok, overdue
  in danger, scoped label pills on the five-color wheel from `board_view.go`.
- Focus is a brighter border plus a background tint on the card, and a tinted
  band on the column.
- Cancelled is toggled off and survives as a collapsed rail carrying its count.
- The overlay is composed as a z-ordered `lipgloss.Compositor` layer with a
  drop shadow, so it reads as above the board rather than cut into it.

## Row cost

Per card: 2 rows border + 2 rows content compact, or 2 + 7..9 full (the
description snippet costs 2 of those rows).
Per column: 4 rows chrome (top border, band, band rule, bottom border).
Per frame: 3 rows chrome (header, filter bar, footer).
Cards compact below 28 rows of terminal height, which drops the description.

The snippet costs one visible card in the tallest column at 140x40: TO DO
shows 3 of 4 and picks up a "+1 more" cue. 200x50 still shows every card,
80x24 is compact and unaffected.

## Reproduce

```
go run ./prototypes/web-faithful -width 140 -height 40            # ANSI
go run ./prototypes/web-faithful -width 140 -height 40 -plain     # stripped
go run ./prototypes/web-faithful -width 140 -height 40 -overlay
go run ./prototypes/web-faithful -light -width 140 -height 40     # LightDark seam
go run ./prototypes/web-faithful -check 80x24.txt -width 80 -height 24
```

`capture.sh` regenerates all eight captures and re-measures each one.

`-check` re-measures a capture: exact line count, no line wider than the
target. All eight captures in this directory pass.
