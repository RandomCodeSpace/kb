#!/usr/bin/env sh
set -eu

repo_root="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
test_dir="$(mktemp -d "${TMPDIR:-/tmp}/kb-go-checkers-test.XXXXXX")"

cleanup() {
  rm -rf -- "$test_dir"
}
trap cleanup EXIT HUP INT TERM

fail() {
  echo "check-go-checkers.test: $*" >&2
  exit 1
}

run_capture() {
  output_file="$1"
  shift
  if "$@" >"$output_file" 2>&1; then
    return 0
  else
    return $?
  fi
}

assert_status() {
  expected="$1"
  actual="$2"
  label="$3"
  [ "$actual" -eq "$expected" ] || fail "$label: expected status $expected, got $actual"
}

assert_contains() {
  pattern="$1"
  file="$2"
  label="$3"
  grep -F -- "$pattern" "$file" >/dev/null || fail "$label: missing output: $pattern"
}

assert_not_contains() {
  pattern="$1"
  file="$2"
  label="$3"
  if grep -F -- "$pattern" "$file" >/dev/null; then
    fail "$label: unexpected output: $pattern"
  fi
}

fake_go_dir="$test_dir/fake-go"
mkdir -p -- "$fake_go_dir"
cat >"$fake_go_dir/go" <<'EOF'
#!/usr/bin/env sh
set -eu
case "$1" in
  test)
    if [ "$#" -eq 4 ] && \
      [ "$2" = "./internal/tui/testdata/generate_web_lower_fixture.go" ] && \
      [ "$3" = "./internal/tui/testdata/generate_web_lower_fixture_test.go" ] && \
      [ "$4" = "-count=1" ]; then
      printf '%s\n' "${FAKE_HELPER_TEST_OUTPUT:-ok  command-line-arguments  0.001s  generator helper fixture test ran}"
      exit "${FAKE_HELPER_TEST_STATUS:-0}"
    fi
    for argument do
      case "$argument" in
        -coverprofile=*) : >"${argument#-coverprofile=}" ;;
      esac
    done
    if [ -n "${FAKE_TEST_OUTPUT_FILE:-}" ]; then
      cat "$FAKE_TEST_OUTPUT_FILE"
    else
      printf 'ok  example.test/one  0.001s  coverage: %s%% of statements\n' "${FAKE_PACKAGE_ONE:-95.0}"
      printf 'ok  example.test/two  0.001s  coverage: %s%% of statements\n' "${FAKE_PACKAGE_TWO:-100.0}"
    fi
    exit "${FAKE_TEST_STATUS:-0}"
    ;;
  list)
    if [ -n "${FAKE_LIST_OUTPUT_FILE:-}" ]; then
      cat "$FAKE_LIST_OUTPUT_FILE"
    else
      printf 'example.test/one\nexample.test/two\n'
    fi
    exit "${FAKE_LIST_STATUS:-0}"
    ;;
  tool)
    if [ -n "${FAKE_COVER_OUTPUT_FILE:-}" ]; then
      cat "$FAKE_COVER_OUTPUT_FILE"
    else
      printf 'total: (statements) %s%%\n' "${FAKE_TOTAL:-96.4}"
    fi
    exit "${FAKE_COVER_STATUS:-0}"
    ;;
  *)
    exit 64
    ;;
esac
EOF
chmod +x "$fake_go_dir/go"

coverage_output="$test_dir/coverage-output"
coverage_profile="$test_dir/coverage.out"
if ! PATH="$fake_go_dir:$PATH" GO_COVERAGE_PROFILE="$coverage_profile" \
  sh "$repo_root/scripts/check-go-coverage.sh" --full >"$coverage_output" 2>&1; then
  fail "coverage success case failed"
fi
[ -f "$coverage_profile" ] || fail "GO_COVERAGE_PROFILE was not retained"
assert_contains 'Go statement coverage: 96.4% (required: 96.4%)' "$coverage_output" "coverage success"
assert_contains 'generator helper fixture test ran' "$coverage_output" "coverage helper invocation"

status=0
PATH="$fake_go_dir:$PATH" run_capture "$coverage_output" \
  sh "$repo_root/scripts/check-go-coverage.sh" || status=$?
assert_status 2 "$status" "coverage requires explicit mode"
assert_contains 'usage: check-go-coverage.sh --full | --packages PACKAGE...' "$coverage_output" "coverage usage"

if ! PATH="$fake_go_dir:$PATH" FAKE_HELPER_TEST_STATUS=19 FAKE_COVER_STATUS=19 GO_TOTAL_COVERAGE_THRESHOLD=invalid \
  sh "$repo_root/scripts/check-go-coverage.sh" --packages example.test/one example.test/two >"$coverage_output" 2>&1; then
  fail "selected coverage case failed"
fi
assert_contains 'Selected Go package coverage passed: 2 package(s)' "$coverage_output" "selected coverage success"
assert_not_contains 'generator helper fixture test ran' "$coverage_output" "selected coverage helper scope"
assert_not_contains 'Go statement coverage:' "$coverage_output" "selected coverage aggregate exclusion"

status=0
PATH="$fake_go_dir:$PATH" FAKE_HELPER_TEST_STATUS=19 FAKE_HELPER_TEST_OUTPUT='generator helper fixture test failed' \
  run_capture "$coverage_output" sh "$repo_root/scripts/check-go-coverage.sh" --full || status=$?
assert_status 19 "$status" "coverage helper failure"
assert_contains 'generator helper fixture test failed' "$coverage_output" "coverage helper failure"

status=0
PATH="$fake_go_dir:$PATH" GO_PACKAGE_COVERAGE_THRESHOLD=94.9 \
  run_capture "$coverage_output" sh "$repo_root/scripts/check-go-coverage.sh" --full || status=$?
assert_status 2 "$status" "invalid package threshold"
assert_contains 'GO_PACKAGE_COVERAGE_THRESHOLD must be a number from 95 through 100' "$coverage_output" "invalid package threshold"

status=0
PATH="$fake_go_dir:$PATH" GO_TOTAL_COVERAGE_THRESHOLD=100.1 \
  run_capture "$coverage_output" sh "$repo_root/scripts/check-go-coverage.sh" --full || status=$?
assert_status 2 "$status" "invalid total threshold"
assert_contains 'GO_TOTAL_COVERAGE_THRESHOLD must be a number from 95 through 100' "$coverage_output" "invalid total threshold"

status=0
PATH="$fake_go_dir:$PATH" FAKE_PACKAGE_ONE=94.9 FAKE_TOTAL=99.0 \
  run_capture "$coverage_output" sh "$repo_root/scripts/check-go-coverage.sh" --full || status=$?
assert_status 1 "$status" "package floor"
assert_contains 'package example.test/one is 94.9%, below 95.0%' "$coverage_output" "package floor"

tui_package_output="$test_dir/tui-package-output"
tui_list_output="$test_dir/tui-list-output"
printf 'ok  github.com/RandomCodeSpace/kb/internal/tui  0.001s  coverage: 90.0%% of statements\n' >"$tui_package_output"
printf 'github.com/RandomCodeSpace/kb/internal/tui\n' >"$tui_list_output"
if ! PATH="$fake_go_dir:$PATH" FAKE_TEST_OUTPUT_FILE="$tui_package_output" FAKE_LIST_OUTPUT_FILE="$tui_list_output" \
  sh "$repo_root/scripts/check-go-coverage.sh" --packages github.com/RandomCodeSpace/kb/internal/tui >"$coverage_output" 2>&1; then
  fail "internal/tui 90 percent override failed"
fi
assert_contains 'generator helper fixture test ran' "$coverage_output" "internal/tui helper invocation"
assert_contains 'Selected Go package coverage passed: 1 package(s)' "$coverage_output" "internal/tui selected coverage"

printf 'ok  github.com/RandomCodeSpace/kb/internal/tui  0.001s  coverage: 89.9%% of statements\n' >"$tui_package_output"
status=0
PATH="$fake_go_dir:$PATH" FAKE_TEST_OUTPUT_FILE="$tui_package_output" FAKE_LIST_OUTPUT_FILE="$tui_list_output" \
  run_capture "$coverage_output" sh "$repo_root/scripts/check-go-coverage.sh" --packages github.com/RandomCodeSpace/kb/internal/tui || status=$?
assert_status 1 "$status" "internal/tui package floor"
assert_contains 'package github.com/RandomCodeSpace/kb/internal/tui is 89.9%, below 90.0%' "$coverage_output" "internal/tui package floor"

printf 'ok  example.test/internal/tui  0.001s  coverage: 90.0%% of statements\n' >"$tui_package_output"
printf 'example.test/internal/tui\n' >"$tui_list_output"
status=0
PATH="$fake_go_dir:$PATH" FAKE_TEST_OUTPUT_FILE="$tui_package_output" FAKE_LIST_OUTPUT_FILE="$tui_list_output" \
  run_capture "$coverage_output" sh "$repo_root/scripts/check-go-coverage.sh" --packages example.test/internal/tui || status=$?
assert_status 1 "$status" "override exact package identity"
assert_contains 'package example.test/internal/tui is 90.0%, below 95.0%' "$coverage_output" "override exact package identity"

status=0
PATH="$fake_go_dir:$PATH" FAKE_PACKAGE_ONE=95.0 FAKE_TOTAL=96.3 \
  run_capture "$coverage_output" sh "$repo_root/scripts/check-go-coverage.sh" --full || status=$?
assert_status 1 "$status" "aggregate floor"
assert_contains 'Go statement coverage: 96.3% (required: 96.4%)' "$coverage_output" "aggregate floor"

malformed_package_output="$test_dir/malformed-package-output"
printf 'ok  example.test/one  0.001s  coverage: 95.0junk%% of statements\nok  example.test/two  0.001s  coverage: 100.0%% of statements\n' \
  >"$malformed_package_output"
status=0
PATH="$fake_go_dir:$PATH" FAKE_TEST_OUTPUT_FILE="$malformed_package_output" \
  run_capture "$coverage_output" sh "$repo_root/scripts/check-go-coverage.sh" --full || status=$?
assert_status 1 "$status" "malformed package percentage"
assert_contains 'invalid coverage percentage 95.0junk%' "$coverage_output" "malformed package percentage"

duplicate_package_output="$test_dir/duplicate-package-output"
printf 'ok  example.test/one  0.001s  coverage: 95.0%% of statements\nok  example.test/one  0.001s  coverage: 100.0%% of statements\n' \
  >"$duplicate_package_output"
status=0
PATH="$fake_go_dir:$PATH" FAKE_TEST_OUTPUT_FILE="$duplicate_package_output" \
  run_capture "$coverage_output" sh "$repo_root/scripts/check-go-coverage.sh" --full || status=$?
assert_status 1 "$status" "duplicate and missing package identities"
assert_contains 'duplicate package result example.test/one' "$coverage_output" "duplicate package identity"
assert_contains 'missing package result example.test/two' "$coverage_output" "missing package identity"

malformed_total_output="$test_dir/malformed-total-output"
printf 'total: (statements) 96.4junk%%\n' >"$malformed_total_output"
status=0
PATH="$fake_go_dir:$PATH" FAKE_COVER_OUTPUT_FILE="$malformed_total_output" \
  run_capture "$coverage_output" sh "$repo_root/scripts/check-go-coverage.sh" --full || status=$?
assert_status 1 "$status" "malformed aggregate"
assert_contains 'expected exactly one numeric total Go statement coverage result' "$coverage_output" "malformed aggregate"

duplicate_total_output="$test_dir/duplicate-total-output"
printf 'total: (statements) 96.4%%\ntotal: (statements) 99.0%%\n' >"$duplicate_total_output"
status=0
PATH="$fake_go_dir:$PATH" FAKE_COVER_OUTPUT_FILE="$duplicate_total_output" \
  run_capture "$coverage_output" sh "$repo_root/scripts/check-go-coverage.sh" --full || status=$?
assert_status 1 "$status" "duplicate aggregate"
assert_contains 'expected exactly one numeric total Go statement coverage result' "$coverage_output" "duplicate aggregate"

format_repo="$test_dir/format-repo"
format_tmp="$test_dir/format-tmp"
mkdir -p -- "$format_repo" "$format_tmp"
(
  cd "$format_repo"
  git init -q
  printf 'package main\n\nfunc main() {}\n' >'clean.go'
  printf 'package main\n' >'-leading.go'
  printf 'package main\n' >'space name.go'
  tab_name="$(printf 'tab\tname.go')"
  printf 'package main\n' >"$tab_name"
  printf 'package main\n' >'odd
name.go'
  mkdir -p nested
  printf 'package nested\n' >'nested/clean.go'
  git add -- '*.go'
  git add -- 'nested/clean.go'
  printf 'this is deliberately not Go\n' >'untracked-ignored.go'
  TMPDIR="$format_tmp" sh "$repo_root/scripts/check-go-format.sh" --full
)
if find "$format_tmp" -type f -print | grep . >/dev/null; then
  fail "format success left temporary files behind"
fi

format_output="$test_dir/format-output"
printf 'package main\n\nfunc broken(\n' >"$format_repo/syntax-error.go"
(
  cd "$format_repo"
  git add -- 'syntax-error.go'
)
status=0
(
  cd "$format_repo"
  TMPDIR="$format_tmp" run_capture "$format_output" sh "$repo_root/scripts/check-go-format.sh" --full
) || status=$?
[ "$status" -ne 0 ] || fail "syntactically malformed tracked Go file unexpectedly passed"
assert_contains 'syntax-error.go' "$format_output" "syntactically malformed tracked Go file"
if find "$format_tmp" -type f -print | grep . >/dev/null; then
  fail "syntax failure left temporary files behind"
fi
(
  cd "$format_repo"
  git rm -q -f -- 'syntax-error.go'
)

printf 'package main\nfunc badlyFormatted( ){ }\n' >"$format_repo/bad.go"
(
  cd "$format_repo"
  git add -- 'bad.go'
)
status=0
(
  cd "$format_repo"
  TMPDIR="$format_tmp" run_capture "$format_output" sh "$repo_root/scripts/check-go-format.sh" --full
) || status=$?
assert_status 1 "$status" "unformatted file"
assert_contains 'bad.go' "$format_output" "unformatted file"

status=0
(
  cd "$format_repo"
  TMPDIR="$format_tmp" run_capture "$format_output" sh "$repo_root/scripts/check-go-format.sh"
) || status=$?
assert_status 2 "$status" "format requires explicit mode"
assert_contains 'usage: check-go-format.sh --full | --changed BASE HEAD' "$format_output" "format usage"

changed_format_repo="$test_dir/changed-format-repo"
mkdir -p -- "$changed_format_repo"
(
  cd "$changed_format_repo"
  git init -q
  git config user.name 'kb format test'
  git config user.email 'format@example.invalid'
  printf 'package sample\nfunc existingBad( ){ }\n' >existing_bad.go
  printf 'package sample\n\nfunc Changed() {}\n' >changed.go
  printf 'package sample\n\nfunc Renamed() {}\n' >rename.go
  git add -- '*.go'
  git commit -q -m initial
)
format_base="$(git -C "$changed_format_repo" rev-parse HEAD)"
printf 'package sample\nfunc Changed( ){ }\n' >"$changed_format_repo/changed.go"
git -C "$changed_format_repo" add -- changed.go
git -C "$changed_format_repo" commit -q -m changed
format_target="$(git -C "$changed_format_repo" rev-parse HEAD)"
status=0
(
  cd "$changed_format_repo"
  TMPDIR="$format_tmp" run_capture "$format_output" sh "$repo_root/scripts/check-go-format.sh" \
    --changed "$format_base" "$format_target"
) || status=$?
assert_status 1 "$status" "changed format failure"
assert_contains 'changed.go' "$format_output" "changed format target"
assert_not_contains 'existing_bad.go' "$format_output" "unchanged format exclusion"

printf 'package sample\n\nfunc Changed() {}\n' >"$changed_format_repo/changed.go"
git -C "$changed_format_repo" mv rename.go renamed.go
git -C "$changed_format_repo" add -- changed.go
git -C "$changed_format_repo" commit -q -m fixed
format_fixed="$(git -C "$changed_format_repo" rev-parse HEAD)"
(
  cd "$changed_format_repo"
  TMPDIR="$format_tmp" sh "$repo_root/scripts/check-go-format.sh" --changed "$format_target" "$format_fixed"
)

fake_git_dir="$test_dir/fake-git"
mkdir -p -- "$fake_git_dir"
cat >"$fake_git_dir/git" <<'EOF'
#!/usr/bin/env sh
exit 23
EOF
chmod +x "$fake_git_dir/git"
status=0
PATH="$fake_git_dir:$PATH" TMPDIR="$format_tmp" \
  run_capture "$format_output" sh "$repo_root/scripts/check-go-format.sh" --full || status=$?
assert_status 23 "$status" "git enumeration failure"

fake_gofmt_dir="$test_dir/fake-gofmt"
mkdir -p -- "$fake_gofmt_dir"
cat >"$fake_gofmt_dir/gofmt" <<'EOF'
#!/usr/bin/env sh
exit 17
EOF
chmod +x "$fake_gofmt_dir/gofmt"
status=0
(
  cd "$format_repo"
  PATH="$fake_gofmt_dir:$PATH" TMPDIR="$format_tmp" \
    run_capture "$format_output" sh "$repo_root/scripts/check-go-format.sh" --full
) || status=$?
assert_status 17 "$status" "exact gofmt failure through xargs"

fake_xargs_dir="$test_dir/fake-xargs"
mkdir -p -- "$fake_xargs_dir"
cat >"$fake_xargs_dir/xargs" <<'EOF'
#!/usr/bin/env sh
exit 42
EOF
chmod +x "$fake_xargs_dir/xargs"
status=0
(
  cd "$format_repo"
  PATH="$fake_xargs_dir:$PATH" TMPDIR="$format_tmp" \
    run_capture "$format_output" sh "$repo_root/scripts/check-go-format.sh" --full
) || status=$?
assert_status 42 "$status" "xargs infrastructure failure"
assert_contains 'format: xargs failed' "$format_output" "xargs infrastructure failure"

if find "$format_tmp" -type f -print | grep . >/dev/null; then
  fail "format failure left temporary files behind"
fi

quality_workflow="$repo_root/.github/workflows/quality.yml"
release_script="$repo_root/scripts/release.sh"
for job_name in focused-quality contract-race migration-recovery tui-performance \
  binary-release-contract ci-contract docs-contract; do
  assert_contains "  $job_name:" "$quality_workflow" "stable $job_name job"
  assert_contains "    name: $job_name" "$quality_workflow" "stable $job_name name"
done
assert_contains 'base=$(git merge-base "$PR_BASE_SHA" "$head")' \
  "$quality_workflow" "pull-request merge-base impact"
assert_contains 'base=$(git rev-parse "$PUSH_BEFORE_SHA^{commit}")' \
  "$quality_workflow" "main-push before impact"
assert_contains '--format github' "$quality_workflow" "GitHub impact output"
assert_contains 'actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02' \
  "$quality_workflow" "pinned performance evidence upload"
assert_contains 'if-no-files-found: error' "$quality_workflow" \
  "required performance evidence"
assert_contains '--format plan' "$release_script" "release impact plan"
assert_contains 'bash scripts/verify-release-artifacts.sh' "$release_script" \
  "shared release artifact verification"

if grep -E 'go (test|vet)([[:space:]][^[:space:]]+)*[[:space:]]+\./\.\.\.' \
  "$quality_workflow" "$release_script" >/dev/null; then
  fail 'quality or release gate contains a blanket Go package command'
fi
if grep -E 'scripts/check-go-(coverage|format)\.sh[[:space:]]*$' \
  "$quality_workflow" "$release_script" >/dev/null; then
  fail 'quality or release gate invokes a checker without an explicit scope'
fi

echo "check-go-checkers.test: pass"
