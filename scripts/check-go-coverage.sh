#!/usr/bin/env sh
set -eu

threshold="${GO_COVERAGE_THRESHOLD:-95.0}"
cleanup_profile=0

if [ -n "${GO_COVERAGE_PROFILE:-}" ]; then
  profile="$GO_COVERAGE_PROFILE"
  mkdir -p -- "$(dirname "$profile")"
else
  profile="$(mktemp "${TMPDIR:-/tmp}/kb-go-coverage.XXXXXX")"
  cleanup_profile=1
fi

cleanup() {
  if [ "$cleanup_profile" -eq 1 ]; then
    rm -f "$profile"
  fi
}
trap cleanup EXIT HUP INT TERM

if ! awk -v required="$threshold" 'BEGIN {
  exit !(required ~ /^[0-9]+([.][0-9]+)?$/ && required + 0 >= 95 && required + 0 <= 100)
}'; then
  echo "coverage: GO_COVERAGE_THRESHOLD must be a number from 95 through 100" >&2
  exit 2
fi

: "${CGO_ENABLED:=0}"
export CGO_ENABLED

if ! test_output="$(go test ./... -count=1 -covermode=atomic -coverprofile="$profile")"; then
  printf '%s\n' "$test_output"
  exit 1
fi
printf '%s\n' "$test_output"

package_count="$(go list ./... | awk 'END { print NR }')"
if ! printf '%s\n' "$test_output" | awk -v required="$threshold" -v expected="$package_count" '
  /coverage: [0-9.]+% of statements/ {
    seen++
    package_name = $2
    for (i = 1; i <= NF; i++) {
      if ($i == "coverage:") {
        value = $(i + 1)
        sub(/%$/, "", value)
        if (value + 0 < required + 0) {
          printf "coverage: package %s is %.1f%%, below %.1f%%\n", package_name, value, required > "/dev/stderr"
          failed = 1
        }
      }
    }
  }
  END {
    if (seen != expected) {
      printf "coverage: read %d package results, expected %d\n", seen, expected > "/dev/stderr"
      failed = 1
    }
    exit failed
  }
'; then
  exit 1
fi

total="$(go tool cover -func="$profile" | awk '/^total:/ { value=$NF; sub(/%$/, "", value); print value }')"

if [ -z "$total" ]; then
  echo "coverage: could not read the total Go statement coverage" >&2
  exit 1
fi

printf 'Go statement coverage: %s%% (required: %s%%)\n' "$total" "$threshold"
awk -v actual="$total" -v required="$threshold" 'BEGIN { exit !(actual + 0 >= required + 0) }'
