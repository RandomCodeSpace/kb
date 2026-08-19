# Prototype: web-faithful

Throwaway visual prototype for #137 (map #136). Hardcoded fake data, no
dependency on `internal/tui`, not shipped.

## The language

- Every card is a rounded-border surface with `Padding(0, 1)` and its own
  background, sitting on a panel background.
- Every column is a full box with a header band (status dot, name, count) that
  has its own background and is separated from the card stack by a rule.
- Semantic accents: priority scale P1-P4, blocked in warn, due in ok, overdue
  in danger, scoped label pills on the five-color wheel from `board_view.go`.
- Focus is a brighter border plus a background tint on the card, and a tinted
  band on the column.
- Cancelled is toggled off and survives as a collapsed rail carrying its count.
- The overlay is composed as a z-ordered `lipgloss.Compositor` layer with a
  drop shadow, so it reads as above the board rather than cut into it.

## Row cost

Per card: 2 rows border + 2 rows content compact, or 2 + 5..7 full.
Per column: 4 rows chrome (top border, band, band rule, bottom border).
Per frame: 3 rows chrome (header, filter bar, footer).
Cards compact below 28 rows of terminal height.

## Reproduce

```
go run ./prototypes/web-faithful -width 140 -height 40            # ANSI
go run ./prototypes/web-faithful -width 140 -height 40 -plain     # stripped
go run ./prototypes/web-faithful -width 140 -height 40 -overlay
go run ./prototypes/web-faithful -light -width 140 -height 40     # LightDark seam
go run ./prototypes/web-faithful -check 80x24.txt -width 80 -height 24
```

`-check` re-measures a capture: exact line count, no line wider than the
target. All eight captures in this directory pass.
