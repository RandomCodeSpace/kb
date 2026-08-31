#!/usr/bin/env sh
set -eu

package_threshold="${GO_PACKAGE_COVERAGE_THRESHOLD:-95.0}"
total_threshold="${GO_TOTAL_COVERAGE_THRESHOLD:-96.4}"
cleanup_profile=0

case "${1:-}" in
  --full)
    mode=full
    shift
    [ "$#" -eq 0 ] || {
      echo 'coverage: --full does not accept package arguments' >&2
      exit 2
    }
    set -- ./...
    ;;
  --packages)
    mode=selected
    shift
    [ "$#" -gt 0 ] || {
      echo 'coverage: --packages requires at least one package' >&2
      exit 2
    }
    ;;
  *)
    echo 'coverage: usage: check-go-coverage.sh --full | --packages PACKAGE...' >&2
    exit 2
    ;;
esac

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

validate_threshold() {
  name="$1"
  value="$2"
  if ! awk -v required="$value" 'BEGIN {
    exit !(required ~ /^[0-9]+([.][0-9]+)?$/ && required + 0 >= 95 && required + 0 <= 100)
  }'; then
    printf 'coverage: %s must be a number from 95 through 100\n' "$name" >&2
    exit 2
  fi
}

validate_threshold GO_PACKAGE_COVERAGE_THRESHOLD "$package_threshold"
if [ "$mode" = full ]; then
  validate_threshold GO_TOTAL_COVERAGE_THRESHOLD "$total_threshold"
fi

: "${CGO_ENABLED:=0}"
export CGO_ENABLED

if package_list="$(go list "$@")"; then
  :
else
  exit $?
fi

run_helper=0
if [ "$mode" = full ] || printf '%s\n' "$package_list" | grep -Fx 'github.com/RandomCodeSpace/kb/internal/tui' >/dev/null; then
  run_helper=1
fi
if [ "$run_helper" -eq 1 ]; then
  if helper_test_output="$(go test \
    ./internal/tui/testdata/generate_web_lower_fixture.go \
    ./internal/tui/testdata/generate_web_lower_fixture_test.go \
    -count=1)"; then
    :
  else
    status=$?
    printf '%s\n' "$helper_test_output"
    exit "$status"
  fi
  printf '%s\n' "$helper_test_output"
fi

if test_output="$(go test "$@" -count=1 -covermode=atomic -coverprofile="$profile")"; then
  :
else
  status=$?
  printf '%s\n' "$test_output"
  exit "$status"
fi
printf '%s\n' "$test_output"

if ! printf '%s\n' "$test_output" | awk -v required="$package_threshold" -v expected_packages="$package_list" '
  BEGIN {
    expected_count = split(expected_packages, expected, "\n")
    for (i = 1; i <= expected_count; i++) {
      if (expected[i] != "") {
        expected_set[expected[i]] = 1
      }
    }
  }
  /coverage:/ {
    package_name = $2
    for (i = 1; i <= NF; i++) {
      if ($i == "coverage:") {
        raw_value = $(i + 1)
        if (raw_value !~ /^[0-9]+([.][0-9]+)?%$/) {
          printf "coverage: package %s has invalid coverage percentage %s\n", package_name, raw_value > "/dev/stderr"
          failed = 1
          next
        }
        value = raw_value
        sub(/%$/, "", value)
        if (value + 0 < 0 || value + 0 > 100) {
          printf "coverage: package %s has invalid coverage percentage %s\n", package_name, raw_value > "/dev/stderr"
          failed = 1
          next
        }
        if (!(package_name in expected_set)) {
          printf "coverage: unexpected package result %s\n", package_name > "/dev/stderr"
          failed = 1
        }
        seen[package_name]++
        if (seen[package_name] > 1) {
          printf "coverage: duplicate package result %s\n", package_name > "/dev/stderr"
          failed = 1
        }
        package_required = required
        if (package_name == "github.com/RandomCodeSpace/kb/internal/tui") {
          package_required = 90.0
        }
        if (value + 0 < package_required + 0) {
          printf "coverage: package %s is %.1f%%, below %.1f%%\n", package_name, value, package_required > "/dev/stderr"
          failed = 1
        }
      }
    }
  }
  END {
    for (package_name in expected_set) {
      if (!(package_name in seen)) {
        printf "coverage: missing package result %s\n", package_name > "/dev/stderr"
        failed = 1
      }
    }
    exit failed
  }
'; then
  exit 1
fi

if [ "$mode" = selected ]; then
  package_count="$(printf '%s\n' "$package_list" | awk 'NF { count++ } END { print count + 0 }')"
  printf 'Selected Go package coverage passed: %s package(s)\n' "$package_count"
  exit 0
fi

if cover_output="$(go tool cover -func="$profile")"; then
  :
else
  exit $?
fi
if total="$(printf '%s\n' "$cover_output" | awk '
  /^total:/ {
    count++
    raw_value = $NF
    if (raw_value !~ /^[0-9]+([.][0-9]+)?%$/) {
      invalid = 1
    } else {
      value = raw_value
      sub(/%$/, "", value)
      if (value + 0 < 0 || value + 0 > 100) {
        invalid = 1
      }
    }
  }
  END {
    if (count != 1 || invalid) {
      exit 1
    }
    print value
  }
')"; then
  :
else
  echo "coverage: expected exactly one numeric total Go statement coverage result" >&2
  exit 1
fi

printf 'Go statement coverage: %s%% (required: %s%%)\n' "$total" "$total_threshold"
awk -v actual="$total" -v required="$total_threshold" 'BEGIN { exit !(actual + 0 >= required + 0) }'
