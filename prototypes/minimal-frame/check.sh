#!/usr/bin/env bash
# Geometry check: every captured frame must be exactly N rows, none wider than W.
# Display width is measured with the same wcwidth rules the terminal uses.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
status=0
check() {
  local file="$1" w="$2" h="$3"
  python3 - "$here/$file" "$w" "$h" <<'PY' || status=1
import sys, unicodedata
path, want_w, want_h = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
def dw(s):
    n = 0
    for ch in s:
        if unicodedata.combining(ch):
            continue
        n += 2 if unicodedata.east_asian_width(ch) in ("W", "F") else 1
    return n
lines = open(path, encoding="utf-8").read().split("\n")
if lines and lines[-1] == "":
    lines.pop()
bad = [(i + 1, dw(l)) for i, l in enumerate(lines) if dw(l) > want_w]
ok = len(lines) == want_h and not bad
print(("PASS " if ok else "FAIL ") + f"{path.split('/')[-1]} rows={len(lines)}/{want_h} maxw={max((dw(l) for l in lines), default=0)}/{want_w}")
if bad:
    print("  overflowing rows:", bad[:5])
sys.exit(0 if ok else 1)
PY
}
check 80x24.txt 80 24
check 140x40.txt 140 40
check 200x50.txt 200 50
check overlay-140x40.txt 140 40
exit $status
