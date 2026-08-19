#!/usr/bin/env bash
# Regenerate every capture in this directory and re-measure each one.
set -euo pipefail
cd "$(dirname "$0")/../.."
P=prototypes/web-faithful

for spec in "80 24" "140 40" "200 50"; do
  set -- $spec
  go run ./$P -width "$1" -height "$2" > "$P/${1}x${2}.ans"
  go run ./$P -width "$1" -height "$2" -plain > "$P/${1}x${2}.txt"
done
go run ./$P -width 140 -height 40 -overlay > "$P/overlay-140x40.ans"
go run ./$P -width 140 -height 40 -overlay -plain > "$P/overlay-140x40.txt"

for f in 80x24 140x40 200x50; do
  W=${f%x*}
  H=${f#*x}
  go run ./$P -check "$P/$f.txt" -width "$W" -height "$H"
  go run ./$P -check "$P/$f.ans" -width "$W" -height "$H"
done
go run ./$P -check "$P/overlay-140x40.txt" -width 140 -height 40
go run ./$P -check "$P/overlay-140x40.ans" -width 140 -height 40
