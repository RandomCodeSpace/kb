#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "$0")/.." && pwd -P)
test_root=$(mktemp -d "${TMPDIR:-/tmp}/kb-release-test.XXXXXX")

cleanup() {
  case $test_root in
    "${TMPDIR:-/tmp}"/kb-release-test.*) rm -rf -- "$test_root" ;;
    *) printf 'release.test: refusing to remove %s\n' "$test_root" >&2 ;;
  esac
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

fail() {
  printf 'release.test: %s\n' "$*" >&2
  exit 1
}

assert_contains() {
  local expected=$1
  local file=$2
  grep -F -- "$expected" "$file" >/dev/null || \
    fail "missing '$expected' in $file"
}

run_fails() {
  local expected=$1
  shift
  local status=0
  "$@" >"$test_root/failure.out" 2>&1 || status=$?
  [[ $status -eq 2 ]] || fail "expected status 2, got $status: $*"
  assert_contains "$expected" "$test_root/failure.out"
}

fake_bin="$test_root/bin"
mkdir -p "$fake_bin"

cat >"$fake_bin/go" <<'FAKE_GO'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${FAKE_GO_LOG:-/dev/null}"
case ${1:-} in
  run)
    base=''
    head=''
    output_format=''
    shift
    while [[ $# -gt 0 ]]; do
      case $1 in
        --base) base=$2; shift 2 ;;
        --head) head=$2; shift 2 ;;
        --format) output_format=$2; shift 2 ;;
        *) shift ;;
      esac
    done
    [[ -n $base && -n $head && $output_format == plan ]]
    base=$(git rev-parse "$base^{commit}")
    head=$(git rev-parse "$head^{commit}")
    printf 'schema_version\t1\n'
    printf 'base\t%s\n' "$base"
    printf 'head\t%s\n' "$head"
    printf 'compile_all\tfalse\n'
    for check_name in focused_quality contract_race migration_recovery \
      tui_performance binary_release_contract ci_contract docs_contract sonar; do
      printf 'check\t%s\tfalse\n' "$check_name"
    done
    ;;
  test)
    exit 0
    ;;
  list)
    printf '%s\n' 'github.com/RandomCodeSpace/kb'
    ;;
  env)
    case ${2:-} in
      GOOS) printf '%s\n' linux ;;
      GOARCH) printf '%s\n' amd64 ;;
      GOVERSION) printf '%s\n' go1.26.6 ;;
      *) exit 64 ;;
    esac
    ;;
  build)
    if [[ ${FAKE_GO_TERM_BUILD:-0} == 1 ]]; then
      kill -TERM "$PPID"
      sleep 1
    fi
    output=''
    while [[ $# -gt 0 ]]; do
      if [[ $1 == -o ]]; then
        output=$2
        shift 2
      else
        shift
      fi
    done
    [[ -n $output ]]
    version=$(git describe --tags --exact-match HEAD)
    revision=$(git rev-parse HEAD)
    [[ ${FAKE_GO_BAD_REVISION:-0} == 0 ]] || revision=deadbeef
    revision_short=${revision:0:12}
    cat >"$output" <<EOF
#!/usr/bin/env bash
# fake-version=$version
# fake-revision=$revision
# fake-goos=${GOOS:-linux}
# fake-goarch=${GOARCH:-amd64}
set -euo pipefail
case \${1:-} in
  version) printf 'kb %s (%s)\\n' '$version' '$revision_short' ;;
  --help) printf 'usage: kb\\n  mcp        expose the local board over MCP stdio\\n' ;;
  help) printf 'usage: kb <command>\\n  add "title"\\n' ;;
  project) printf 'active project: release-smoke\\n' ;;
  add) printf 'added Release smoke\\n' ;;
  list) printf '[{"title":"Release smoke","tags":["project::release-smoke"]}]\\n' ;;
  tui) exit 0 ;;
  '') printf 'usage: kb\\n' ;;
  *) exit 64 ;;
esac
EOF
    chmod +x "$output"
    ;;
  version)
    [[ ${2:-} == -m ]]
    artifact=$3
    version=$(sed -n 's/^# fake-version=//p' "$artifact")
    revision=$(sed -n 's/^# fake-revision=//p' "$artifact")
    goos=$(sed -n 's/^# fake-goos=//p' "$artifact")
    goarch=$(sed -n 's/^# fake-goarch=//p' "$artifact")
    printf '%s: go1.26.6\n' "$artifact"
    printf '\tpath\tgithub.com/RandomCodeSpace/kb\n'
    printf '\tmod\tgithub.com/RandomCodeSpace/kb\t%s\th1:fake\n' "$version"
    printf '\tbuild\tGOOS=%s\n' "$goos"
    printf '\tbuild\tGOARCH=%s\n' "$goarch"
    printf '\tbuild\tvcs.revision=%s\n' "$revision"
    printf '\tbuild\tvcs.modified=false\n'
    ;;
  *)
    exit 64
    ;;
esac
FAKE_GO
chmod +x "$fake_bin/go"

cat >"$fake_bin/file" <<'FAKE_FILE'
#!/usr/bin/env bash
set -euo pipefail
artifact=${2:-}
case $artifact in
  *linux-amd64) printf '%s\n' 'ELF 64-bit LSB executable, x86-64, statically linked' ;;
  *linux-arm64) printf '%s\n' 'ELF 64-bit LSB executable, ARM aarch64, statically linked' ;;
  *darwin-amd64) printf '%s\n' 'Mach-O 64-bit x86_64 executable' ;;
  *darwin-arm64) printf '%s\n' 'Mach-O 64-bit arm64 executable' ;;
  *windows-amd64.exe) printf '%s\n' 'PE32+ executable (console) x86-64, for MS Windows' ;;
  *) exit 64 ;;
esac
FAKE_FILE
chmod +x "$fake_bin/file"

cat >"$fake_bin/script" <<'FAKE_SCRIPT'
#!/usr/bin/env bash
set -euo pipefail
command_text=''
while [[ $# -gt 0 ]]; do
  case $1 in
    --command) command_text=$2; shift 2 ;;
    *) shift ;;
  esac
done
[[ -n $command_text ]]
bash -c "$command_text"
FAKE_SCRIPT
chmod +x "$fake_bin/script"

cat >"$fake_bin/gh" <<'FAKE_GH'
#!/usr/bin/env bash
set -euo pipefail
case "${1:-} ${2:-}" in
  'auth status') exit 0 ;;
  'repo view') printf '%s\n' 'RandomCodeSpace/kb' ;;
  'release create') printf '%s\n' "$*" >>"$FAKE_GH_LOG" ;;
  *) exit 64 ;;
esac
FAKE_GH
chmod +x "$fake_bin/gh"

remote="$test_root/origin.git"
source_repo="$test_root/source"
git init -q --bare "$remote"
git init -q -b main "$source_repo"
mkdir -p "$source_repo/scripts/ci" "$source_repo/docs/releases"
printf 'module github.com/RandomCodeSpace/kb\n\ngo 1.26.6\n' >"$source_repo/go.mod"
printf 'release fixture baseline\n' >"$source_repo/baseline.txt"
(
  cd "$source_repo"
  git config user.name 'Release Test'
  git config user.email 'release-test@example.invalid'
  git add .
  git commit -q -m 'baseline fixture'
  git tag -a v1.0.0 -m v1.0.0
)
cp "$repo_root/scripts/release.sh" "$source_repo/scripts/release.sh"
cp "$repo_root/scripts/verify-release-artifacts.sh" \
  "$source_repo/scripts/verify-release-artifacts.sh"
cp "$repo_root/scripts/ci/impact.sh" "$source_repo/scripts/ci/impact.sh"
printf '# Notes\n\nVerified release.\n' >"$source_repo/docs/releases/v1.2.3.md"
printf '# Notes\n\nVerified release.\n' >"$source_repo/docs/releases/v1.2.4.md"
(
  cd "$source_repo"
  git config user.name 'Release Test'
  git config user.email 'release-test@example.invalid'
  git add .
  git commit -q -m 'candidate fixture'
  git remote add origin "$remote"
  git push -q -u origin HEAD:main
  git push -q origin refs/tags/v1.0.0
)

release() {
  (
    cd "$source_repo"
    PATH="$fake_bin:$PATH" FAKE_GH_LOG="$test_root/gh.log" FAKE_GO_LOG="$test_root/go.log" \
      bash scripts/release.sh "$@"
  )
}

bad_revision_release() {
  (
    cd "$source_repo"
    PATH="$fake_bin:$PATH" FAKE_GH_LOG="$test_root/gh.log" FAKE_GO_LOG="$test_root/go.log" \
      FAKE_GO_BAD_REVISION=1 \
      bash scripts/release.sh v1.2.3 docs/releases/v1.2.3.md --dry-run
  )
}

terminated_release() {
  (
    cd "$source_repo"
    PATH="$fake_bin:$PATH" FAKE_GH_LOG="$test_root/gh.log" FAKE_GO_LOG="$test_root/go.log" \
      FAKE_GO_TERM_BUILD=1 \
      bash scripts/release.sh v1.2.3 docs/releases/v1.2.3.md --dry-run
  )
}

linked_release() {
  (
    cd "$linked"
    PATH="$fake_bin:$PATH" FAKE_GH_LOG="$test_root/gh.log" FAKE_GO_LOG="$test_root/go.log" \
      bash scripts/release.sh v1.2.3 docs/releases/v1.2.3.md --dry-run
  )
}

run_fails 'version must match vX.Y.Z' release nope docs/releases/v1.2.3.md --dry-run
run_fails 'release notes must be a readable, non-empty regular file' \
  release v1.2.3 docs/releases/missing.md --dry-run
run_fails 'release notes must be a readable, non-empty regular file' \
  release v1.2.3 docs/releases --dry-run

printf 'dirty\n' >"$source_repo/untracked"
run_fails 'working tree, index, or untracked-file set is not clean' \
  release v1.2.3 docs/releases/v1.2.3.md --dry-run
rm "$source_repo/untracked"

printf 'dirty\n' >>"$source_repo/go.mod"
run_fails 'working tree, index, or untracked-file set is not clean' \
  release v1.2.3 docs/releases/v1.2.3.md --dry-run
git -C "$source_repo" restore go.mod

printf 'staged\n' >>"$source_repo/go.mod"
git -C "$source_repo" add go.mod
run_fails 'working tree, index, or untracked-file set is not clean' \
  release v1.2.3 docs/releases/v1.2.3.md --dry-run
git -C "$source_repo" restore --staged --worktree go.mod

git -C "$source_repo" tag -a v1.2.3 -m v1.2.3
run_fails 'local tag v1.2.3 already exists' \
  release v1.2.3 docs/releases/v1.2.3.md --dry-run
git -C "$source_repo" tag -d v1.2.3 >/dev/null

linked="$test_root/linked"
git -C "$source_repo" worktree add -q -b release-test-linked "$linked"
run_fails 'linked git worktrees are not release sources' \
  linked_release
git -C "$source_repo" worktree remove "$linked"

git -C "$source_repo" tag -a v9.9.9 -m v9.9.9
git -C "$source_repo" push -q origin refs/tags/v9.9.9
git -C "$source_repo" tag -d v9.9.9 >/dev/null
run_fails 'remote tag v9.9.9 already exists' \
  release v9.9.9 docs/releases/v1.2.3.md

git -C "$source_repo" switch -q -c release-test-off-main
git -C "$source_repo" commit -q --allow-empty -m 'off-main fixture'
run_fails 'is not current origin/main' \
  release v1.2.3 docs/releases/v1.2.3.md
git -C "$source_repo" switch -q main
git -C "$source_repo" branch -D release-test-off-main >/dev/null

head_before=$(git -C "$source_repo" rev-parse HEAD)
tree_before=$(git -C "$source_repo" rev-parse 'HEAD^{tree}')
index_before=$(git -C "$source_repo" write-tree)
git -C "$source_repo" remote set-url origin "$test_root/does-not-exist"
release v1.2.3 docs/releases/v1.2.3.md --dry-run >"$test_root/dry-run.out"
git -C "$source_repo" remote set-url origin "$remote"
assert_contains 'dry run complete: v1.2.3 verified locally; nothing published' \
  "$test_root/dry-run.out"
assert_contains 'run -buildvcs=false ./scripts/ci/impactcmd' "$test_root/go.log"
assert_contains '--format plan' "$test_root/go.log"
[[ $(git -C "$source_repo" rev-parse HEAD) == "$head_before" ]] || fail 'dry run changed HEAD'
[[ $(git -C "$source_repo" rev-parse 'HEAD^{tree}') == "$tree_before" ]] || fail 'dry run changed tree'
[[ $(git -C "$source_repo" write-tree) == "$index_before" ]] || fail 'dry run changed index'
[[ -z $(git -C "$source_repo" status --porcelain=v2 --untracked-files=all) ]] || fail 'dry run changed status'
git -C "$source_repo" show-ref --verify --quiet refs/tags/v1.2.3 && fail 'dry run left its tag'
[[ ! -e $test_root/gh.log ]] || fail 'dry run invoked gh'

run_fails 'has the wrong source revision' \
  bad_revision_release
git -C "$source_repo" show-ref --verify --quiet refs/tags/v1.2.3 && fail 'failed build left its tag'
[[ -z $(git -C "$source_repo" status --porcelain=v2 --untracked-files=all) ]] || fail 'failed build changed status'

term_status=0
terminated_release >"$test_root/terminated.out" 2>&1 || term_status=$?
[[ $term_status -eq 143 ]] || fail "TERM returned $term_status, want 143"
git -C "$source_repo" show-ref --verify --quiet refs/tags/v1.2.3 && fail 'terminated build left its tag'
[[ -z $(git -C "$source_repo" status --porcelain=v2 --untracked-files=all) ]] || fail 'terminated build changed status'

release v1.2.4 docs/releases/v1.2.4.md >"$test_root/publish.out"
assert_contains 'v1.2.4 published from' "$test_root/publish.out"
[[ $(git --git-dir="$remote" cat-file -t refs/tags/v1.2.4) == tag ]] || \
  fail 'published tag is not annotated'
[[ $(git --git-dir="$remote" rev-parse 'v1.2.4^{}') == "$head_before" ]] || \
  fail 'published tag targets the wrong commit'
assert_contains 'release create v1.2.4 --verify-tag --title v1.2.4 --notes-file docs/releases/v1.2.4.md' \
  "$test_root/gh.log"
for asset in kb-linux-amd64 kb-linux-arm64 kb-darwin-amd64 kb-darwin-arm64 \
  kb-windows-amd64.exe SHA256SUMS; do
  assert_contains "/$asset" "$test_root/gh.log"
done

printf '%s\n' 'release.test: pass'
