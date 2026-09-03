#!/usr/bin/env sh
set -eu

source_root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/kb-impact-test.XXXXXX")"
fixture="$test_root/repo"
output="$test_root/impact.json"

cleanup() {
  rm -rf -- "$test_root"
}
trap cleanup EXIT HUP INT TERM

fail() {
  echo "test-impact: $*" >&2
  exit 1
}

assert_contains() {
  pattern="$1"
  label="$2"
  grep -F -- "$pattern" "$output" >/dev/null || fail "$label: missing $pattern"
}

assert_not_contains() {
  pattern="$1"
  label="$2"
  if grep -F -- "$pattern" "$output" >/dev/null; then
    fail "$label: unexpected $pattern"
  fi
}

commit_all() {
  message="$1"
  git -C "$fixture" add -A
  git -C "$fixture" commit -q -m "$message"
  git -C "$fixture" rev-parse HEAD
}

run_impact() {
  base="$1"
  target="$2"
  shift 2
  if (CDPATH='' cd -- "$fixture" && sh "$source_root/scripts/ci/impact.sh" --base "$base" --head "$target" "$@" >"$output"); then
    return 0
  else
    return $?
  fi
}

mkdir -p -- "$fixture/internal/leaf" "$fixture/internal/importer" "$fixture/internal/tui/testdata" \
  "$fixture/internal/board/testdata" "$fixture/internal/store" \
  "$fixture/.github/workflows" "$fixture/scripts/ci" "$fixture/docs/releases"
git -C "$fixture" init -q -b main
git -C "$fixture" config user.name 'kb impact test'
git -C "$fixture" config user.email 'impact@example.invalid'
printf 'module example.test/impact\n\ngo 1.26.5\n' >"$fixture/go.mod"
printf 'package leaf\n\nfunc Value() int { return 1 }\n' >"$fixture/internal/leaf/leaf.go"
printf 'package importer\n\nimport "example.test/impact/internal/leaf"\n\nfunc Value() int { return leaf.Value() }\n' >"$fixture/internal/importer/importer.go"
printf 'package main\n\nimport "example.test/impact/internal/importer"\n\nfunc main() { _ = importer.Value() }\n' >"$fixture/main.go"
printf 'package tui\n\nfunc fixture() int { return 1 }\n' >"$fixture/internal/tui/performance_harness_test.go"
printf 'golden\n' >"$fixture/internal/tui/testdata/TestBoardGolden.golden"
printf '//go:build ignore\n\npackage main\n\nfunc main() {}\n' >"$fixture/internal/tui/testdata/generate_fixture.go"
printf 'package board\n\nfunc Value() int { return 1 }\n' >"$fixture/internal/board/board.go"
printf '{"cards": 1}\n' >"$fixture/internal/board/testdata/golden.json"
printf 'package store\n\nfunc Schema() int { return 1 }\n' >"$fixture/internal/store/schema.go"
printf 'package store\n\nfunc Migrate() int { return 1 }\n' >"$fixture/internal/store/migrate.go"
printf '\n' >"$fixture/go.sum"
printf 'sonar.sources=.\n' >"$fixture/sonar-project.properties"
printf '// ci monitor fixture\n' >"$fixture/scripts/ci_monitor.cjs"
printf '# local fixture\n' >"$fixture/README.md"
printf 'name: fixture\n' >"$fixture/.github/workflows/quality.yml"
printf '#!/usr/bin/env sh\nexit 0\n' >"$fixture/scripts/check-go-coverage.sh"
printf '#!/usr/bin/env sh\nexit 0\n' >"$fixture/scripts/check-docs.sh"
printf '#!/usr/bin/env sh\nexit 0\n' >"$fixture/scripts/release.sh"
printf '#!/usr/bin/env sh\nexit 0\n' >"$fixture/scripts/verify-release-artifacts.sh"
printf '# release\n' >"$fixture/docs/releases/v1.0.0.md"
initial="$(commit_all initial)"
git -C "$fixture" tag -a v1.0.0 -m v1.0.0 "$initial"

printf '# local fixture\n\nDocumentation only.\n' >"$fixture/README.md"
docs="$(commit_all docs)"
run_impact "$initial" "$docs" || fail 'docs-only impact failed'
assert_contains '"docs_contract": true' 'docs-only classification'
assert_contains '"focused_quality": false' 'docs-only Go exclusion'
assert_contains '"owners": []' 'docs-only owner set'

printf 'MIT License\n' >"$fixture/LICENSE"
license="$(commit_all license)"
run_impact "$docs" "$license" || fail 'license impact failed'
assert_contains '"docs_contract": true' 'license documentation classification'
assert_contains '"focused_quality": false' 'license Go exclusion'

printf 'package leaf\n\nfunc Value() int { return 2 }\n' >"$fixture/internal/leaf/leaf.go"
leaf="$(commit_all leaf)"
run_impact "$license" "$leaf" || fail 'package impact failed'
assert_contains '"focused_quality": true' 'package classification'
assert_contains 'example.test/impact/internal/leaf' 'changed owner'
assert_contains 'example.test/impact/internal/importer' 'direct importer'
assert_contains '"compile_all": false' 'ordinary package compile scope'

run_impact "$license" "$leaf" --format compact || fail 'compact impact failed'
[ "$(wc -l <"$output")" -eq 1 ] || fail 'compact manifest was not one line'
assert_contains '"focused_quality":true' 'compact check output'

run_impact "$license" "$leaf" --format github || fail 'GitHub output failed'
assert_contains 'manifest={"schema_version":1' 'GitHub manifest output'
assert_contains 'focused_quality=true' 'GitHub check output'
assert_contains 'docs_contract=false' 'GitHub unaffected output'

run_impact "$license" "$leaf" --format plan || fail 'release plan output failed'
assert_contains "$(printf 'schema_version\t1')" 'plan schema output'
assert_contains "$(printf 'check\tfocused_quality\ttrue')" 'plan check output'
assert_contains "$(printf 'owner\texample.test/impact/internal/leaf')" 'plan owner output'
assert_contains "$(printf 'compile_package\texample.test/impact/internal/importer')" 'plan importer output'

printf '\n// toolchain contract changed\n' >>"$fixture/go.mod"
module="$(commit_all module)"
run_impact "$leaf" "$module" || fail 'module impact failed'
assert_contains '"compile_all": true' 'module compile expansion'
assert_contains '"repository_tests": false' 'module test restraint'
assert_contains '"binary_release_contract": true' 'module release classification'

printf 'package main\n\nimport "example.test/impact/internal/importer"\n\nfunc main() { println(importer.Value()) }\n' >"$fixture/main.go"
root="$(commit_all root)"
run_impact "$module" "$root" || fail 'root impact failed'
assert_contains '"binary_release_contract": true' 'root release classification'
assert_contains 'example.test/impact' 'root owner'

printf 'package tui\n\nfunc fixture() int { return 2 }\n' >"$fixture/internal/tui/performance_harness_test.go"
performance="$(commit_all performance)"
run_impact "$root" "$performance" || fail 'performance impact failed'
assert_contains '"tui_performance": true' 'performance classification'

printf 'name: changed fixture\n' >"$fixture/.github/workflows/quality.yml"
printf '#!/usr/bin/env sh\nexit 1\n' >"$fixture/scripts/check-go-coverage.sh"
printf '#!/usr/bin/env sh\nexit 1\n' >"$fixture/scripts/check-docs.sh"
ci="$(commit_all ci)"
run_impact "$performance" "$ci" || fail 'CI impact failed'
assert_contains '"ci_contract": true' 'CI classification'

printf '# release\n\nCandidate notes.\n' >"$fixture/docs/releases/v1.0.0.md"
release="$(commit_all release)"
run_impact v1.0.0 "$release" || fail 'tag-base impact failed'
assert_contains "\"base\": \"$initial\"" 'annotated tag base resolution'
assert_contains '"binary_release_contract": true' 'release-notes classification'

git -C "$fixture" mv internal/leaf/leaf.go internal/leaf/value.go
renamed="$(commit_all rename)"
run_impact "$release" "$renamed" || fail 'rename impact failed'
assert_contains '"status": "R100"' 'rename status'
assert_contains 'internal/leaf/value.go' 'rename target'
assert_not_contains '"compile_all": true' 'same-package rename scope'

git -C "$fixture" rm -q internal/leaf/value.go
deleted="$(commit_all delete)"
run_impact "$renamed" "$deleted" || fail 'deleted package impact failed'
assert_contains 'example.test/impact/internal/leaf' 'deleted package identity'
assert_contains '"compile_all": true' 'deleted package compile expansion'
assert_contains '"sonar": false' 'deleted Go exclusion from Sonar'

printf 'golden changed\n' >"$fixture/internal/tui/testdata/TestBoardGolden.golden"
golden="$(commit_all golden)"
run_impact "$deleted" "$golden" || fail 'golden testdata impact failed'
assert_contains 'example.test/impact/internal/tui' 'golden testdata owner'
assert_contains '"focused_quality": true' 'golden testdata classification'
assert_contains '"sonar": false' 'golden testdata Sonar exclusion'

printf '{"cards": 2}\n' >"$fixture/internal/board/testdata/golden.json"
board="$(commit_all board-testdata)"
run_impact "$golden" "$board" || fail 'board testdata impact failed'
assert_contains 'example.test/impact/internal/board' 'board testdata owner'
assert_not_contains 'example.test/impact/internal/tui"' 'board testdata owner isolation'

printf '//go:build ignore\n\npackage main\n\nfunc main() { println(1) }\n' >"$fixture/internal/tui/testdata/generate_fixture.go"
generator="$(commit_all generator)"
run_impact "$board" "$generator" || fail 'generator impact failed'
assert_contains 'example.test/impact/internal/tui' 'generator owner'
assert_contains '"compile_all": false' 'generator compile scope'

printf 'sonar.sources=.\nsonar.sourceEncoding=UTF-8\n' >"$fixture/sonar-project.properties"
sonar="$(commit_all sonar)"
run_impact "$generator" "$sonar" || fail 'sonar properties impact failed'
assert_contains '"ci_contract": true' 'sonar properties classification'
assert_contains '"focused_quality": false' 'sonar properties Go exclusion'

printf 'example.test/dep v1.0.0/go.mod h1:fixture=\n' >"$fixture/go.sum"
checksums="$(commit_all checksums)"
run_impact "$sonar" "$checksums" || fail 'go.sum impact failed'
assert_contains '"compile_all": true' 'go.sum compile expansion'
assert_contains '"binary_release_contract": true' 'go.sum release classification'
assert_contains '"focused_quality": true' 'go.sum quality classification'

printf 'package store\n\nfunc Schema() int { return 2 }\n' >"$fixture/internal/store/schema.go"
schema="$(commit_all schema)"
run_impact "$checksums" "$schema" || fail 'schema impact failed'
assert_contains '"migration_recovery": true' 'schema migration classification'
assert_contains '"contract_race": false' 'schema race exclusion'

printf 'package store\n\nfunc Migrate() int { return 2 }\n' >"$fixture/internal/store/migrate.go"
migrate="$(commit_all migrate)"
run_impact "$schema" "$migrate" || fail 'migrate impact failed'
assert_contains '"contract_race": true' 'migrate race classification'
assert_contains '"migration_recovery": true' 'migrate recovery classification'

printf '// ci monitor fixture\nprocess.exit(0);\n' >"$fixture/scripts/ci_monitor.cjs"
monitor="$(commit_all monitor)"
run_impact "$migrate" "$monitor" || fail 'ci monitor impact failed'
assert_contains '"ci_contract": true' 'ci monitor classification'
assert_contains '"focused_quality": false' 'ci monitor Go exclusion'

printf 'unknown\n' >"$fixture/mystery.bin"
unknown="$(commit_all unknown)"
status=0
run_impact "$monitor" "$unknown" 2>/dev/null || status=$?
[ "$status" -ne 0 ] || fail 'unclassified path unexpectedly passed'
assert_contains 'mystery.bin' 'unclassified path manifest'

mirror="$test_root/mirror"
mkdir -p -- "$mirror"
git -C "$mirror" init -q -b main
git -C "$mirror" config user.name 'kb impact test'
git -C "$mirror" config user.email 'impact@example.invalid'
git -C "$mirror" commit -q --allow-empty -m empty
mirror_base="$(git -C "$mirror" rev-parse HEAD)"
git -C "$source_root" archive HEAD | tar -x -C "$mirror"
git -C "$mirror" add -A
git -C "$mirror" commit -q -m tracked
(CDPATH='' cd -- "$mirror" && sh "$source_root/scripts/ci/impact.sh" --base "$mirror_base" --head HEAD >"$output") ||
  fail "tracked paths remain unclassified: $(grep -A 100 '"unclassified"' "$output" | tr -d ' \n')"
assert_contains '"unclassified": []' 'tracked path classification'

echo 'test-impact: pass'
