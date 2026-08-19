#!/usr/bin/env bash
# Regenerates every capture in this directory and fails if a render does not
# fit its target geometry. Run from the repo root: prototypes/wildcard/capture.sh
set -euo pipefail

dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$dir/../.." && pwd)"
cd "$root"

shot() { # name width height mode
  local name="$1" w="$2" h="$3" mode="$4"
  go run ./prototypes/wildcard -w "$w" -h "$h" -mode "$mode" -plain >"$dir/$name.txt"
  go run ./prototypes/wildcard -w "$w" -h "$h" -mode "$mode" >"$dir/$name.ans"
  go run ./prototypes/wildcard -verify -w "$w" -h "$h" -mode "$mode"
  echo "ok  $name.txt/.ans  ${w}x${h}  $mode"
}

shot 80x24 80 24 board
shot 140x40 140 40 board
shot 200x50 200 50 board
shot overlay-140x40 140 40 overlay
