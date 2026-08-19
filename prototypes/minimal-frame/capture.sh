#!/usr/bin/env bash
# Regenerate every capture in this directory, then verify the geometry.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../.." && pwd)"
bin="$(mktemp -d)/minimal-frame"
go build -o "$bin" "$root/prototypes/minimal-frame"

for geo in "80 24" "140 40" "200 50"; do
  set -- $geo
  "$bin" -width "$1" -height "$2" -plain -out "$here/${1}x${2}.txt"
  "$bin" -width "$1" -height "$2" -out "$here/${1}x${2}.ans"
done
"$bin" -width 140 -height 40 -mode overlay -plain -out "$here/overlay-140x40.txt"
"$bin" -width 140 -height 40 -mode overlay -out "$here/overlay-140x40.ans"

bash "$here/check.sh"
